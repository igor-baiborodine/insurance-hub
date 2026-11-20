package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
)

// Reader implements the application tariff reader over the process-owned restricted pool.
type Reader struct {
	pool           *pgxpool.Pool
	acquireTimeout time.Duration
	queryTimeout   time.Duration
}

var _ application.TariffReader = (*Reader)(nil)

// GetTariff reads one complete tariff in a single PostgreSQL statement snapshot.
func (reader *Reader) GetTariff(
	ctx context.Context,
	code string,
) (domain.Tariff, bool, error) {
	connection, err := reader.acquire(ctx)
	if err != nil {
		return domain.Tariff{}, false, err
	}
	defer connection.Release()

	queryCtx, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()
	rows, err := connection.Query(queryCtx, readTariffQuery, code)
	if err != nil {
		return domain.Tariff{}, false, classifyReaderFailure(ctx, "query tariff", err)
	}
	defer rows.Close()

	var tariffs []domain.Tariff
	for rows.Next() {
		var storedCode string
		var rawBase, rawMarkup []byte
		if err := rows.Scan(&storedCode, &rawBase, &rawMarkup); err != nil {
			return domain.Tariff{}, false, classifyReaderFailure(
				ctx,
				"scan tariff",
				err,
			)
		}
		tariff, err := decodeTariff(storedCode, rawBase, rawMarkup)
		if err != nil {
			return domain.Tariff{}, false, err
		}
		tariffs = append(tariffs, tariff)
	}
	if err := rows.Err(); err != nil {
		return domain.Tariff{}, false, classifyReaderFailure(ctx, "read tariff", err)
	}

	switch len(tariffs) {
	case 0:
		return domain.Tariff{}, false, nil
	case 1:
		if tariffs[0].Code != code {
			return domain.Tariff{}, false, application.ErrTariffInvalid
		}
		return tariffs[0], true, nil
	default:
		return domain.Tariff{}, false, application.ErrTariffAmbiguous
	}
}

func (reader *Reader) acquire(ctx context.Context) (*pgxpool.Conn, error) {
	acquireCtx, cancel := context.WithTimeout(ctx, reader.acquireTimeout)
	defer cancel()
	connection, err := reader.pool.Acquire(acquireCtx)
	if err != nil {
		return nil, classifyReaderFailure(ctx, "acquire connection", err)
	}
	return connection, nil
}

type storedBaseRule struct {
	Position  *int    `json:"position"`
	CoverCode *string `json:"coverCode"`
	Condition *string `json:"condition"`
	Formula   *string `json:"formula"`
}

type storedMarkupRule struct {
	Position  *int    `json:"position"`
	Kind      *string `json:"kind"`
	Condition *string `json:"condition"`
	Factor    *string `json:"factor"`
}

func decodeTariff(code string, rawBase, rawMarkup []byte) (domain.Tariff, error) {
	if code == "" {
		return domain.Tariff{}, application.ErrTariffInvalid
	}
	var storedBase []storedBaseRule
	if err := json.Unmarshal(rawBase, &storedBase); err != nil || len(storedBase) == 0 {
		return domain.Tariff{}, application.ErrTariffInvalid
	}
	base := make([]domain.BaseRule, len(storedBase))
	for index, rule := range storedBase {
		if !validPosition(rule.Position, index) || rule.CoverCode == nil ||
			*rule.CoverCode == "" || rule.Formula == nil || *rule.Formula == "" {
			return domain.Tariff{}, application.ErrTariffInvalid
		}
		base[index] = domain.BaseRule{
			CoverCode: *rule.CoverCode,
			Condition: rule.Condition,
			Formula:   *rule.Formula,
		}
	}

	var storedMarkup []storedMarkupRule
	if err := json.Unmarshal(rawMarkup, &storedMarkup); err != nil || len(storedMarkup) == 0 {
		return domain.Tariff{}, application.ErrTariffInvalid
	}
	markup := make([]domain.MarkupRule, len(storedMarkup))
	for index, rule := range storedMarkup {
		if !validPosition(rule.Position, index) || rule.Kind == nil || *rule.Kind == "" ||
			rule.Factor == nil {
			return domain.Tariff{}, application.ErrTariffInvalid
		}
		factor, err := domain.NewDecimal(*rule.Factor)
		if err != nil {
			return domain.Tariff{}, application.ErrTariffInvalid
		}
		markup[index] = domain.MarkupRule{
			Kind:      *rule.Kind,
			Condition: rule.Condition,
			Factor:    factor,
		}
	}
	return domain.Tariff{Code: code, BaseRules: base, MarkupRules: markup}, nil
}

func validPosition(position *int, index int) bool {
	return position != nil && *position == index+1
}

func classifyReaderFailure(ctx context.Context, operation string, cause error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("postgres %s: %w", operation, err)
	}
	return readerFailure{operation: operation, cause: cause}
}

type readerFailure struct {
	operation string
	cause     error
}

func (failure readerFailure) Error() string {
	return "postgres " + failure.operation + ": " + application.ErrReaderUnavailable.Error()
}

func (failure readerFailure) Unwrap() []error {
	return []error{application.ErrReaderUnavailable, failure.cause}
}
