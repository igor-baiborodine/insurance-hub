package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
)

// Reader implements the application tariff reader over the process-owned restricted pool.
type Reader struct {
	pool           *pgxpool.Pool
	acquireTimeout time.Duration
	queryTimeout   time.Duration
	observer       *Observer
}

var _ application.TariffReader = (*Reader)(nil)

type storedTariff struct {
	code               string
	rawBase, rawMarkup []byte
}

// GetTariff reads one complete tariff in a single PostgreSQL statement snapshot.
func (reader *Reader) GetTariff(
	ctx context.Context,
	code string,
) (domain.Tariff, bool, error) {
	connection, err := reader.acquire(ctx, "calculate_price")
	if err != nil {
		return domain.Tariff{}, false, err
	}
	defer connection.Release()

	readerContext := ctx
	queryCtx, cancel := context.WithTimeout(ctx, reader.queryTimeout)
	defer cancel()
	var stored []storedTariff
	err = reader.observer.stage(
		queryCtx,
		"calculate_price",
		"query",
		"SELECT tariff",
		trace.SpanKindClient,
		func(ctx context.Context) error {
			rows, queryErr := connection.Query(ctx, readTariffQuery, code)
			if queryErr != nil {
				return classifyReaderFailure(
					readerContext,
					"query tariff",
					queryErr,
				)
			}
			defer rows.Close()
			for rows.Next() {
				var row storedTariff
				if scanErr := rows.Scan(
					&row.code,
					&row.rawBase,
					&row.rawMarkup,
				); scanErr != nil {
					return classifyReaderFailure(
						readerContext,
						"scan tariff",
						scanErr,
					)
				}
				stored = append(stored, row)
			}
			if rowsErr := rows.Err(); rowsErr != nil {
				return classifyReaderFailure(readerContext, "read tariff", rowsErr)
			}
			return nil
		},
	)
	if err != nil {
		return domain.Tariff{}, false, err
	}
	if len(stored) == 0 {
		return domain.Tariff{}, false, nil
	}
	tariff, err := selectTariff(code, stored)
	reader.observer.decode(queryCtx, "calculate_price", err)
	if err != nil {
		return domain.Tariff{}, false, err
	}
	return tariff, true, nil
}

// selectTariff decodes every stored row before rejecting duplicates or a mismatched code.
func selectTariff(code string, stored []storedTariff) (domain.Tariff, error) {
	tariffs := make([]domain.Tariff, 0, len(stored))
	for _, row := range stored {
		tariff, err := decodeTariff(row.code, row.rawBase, row.rawMarkup)
		if err != nil {
			return domain.Tariff{}, err
		}
		tariffs = append(tariffs, tariff)
	}
	if len(tariffs) > 1 {
		return domain.Tariff{}, application.ErrTariffAmbiguous
	}
	if tariffs[0].Code != code {
		return domain.Tariff{}, application.ErrTariffInvalid
	}
	return tariffs[0], nil
}

func (reader *Reader) acquire(ctx context.Context, operation string) (*pgxpool.Conn, error) {
	readerContext := ctx
	acquireCtx, cancel := context.WithTimeout(ctx, reader.acquireTimeout)
	defer cancel()
	var connection *pgxpool.Conn
	err := reader.observer.stage(
		acquireCtx,
		operation,
		"acquire",
		"pricing.db.acquire",
		trace.SpanKindInternal,
		func(ctx context.Context) error {
			started := time.Now()
			var acquireErr error
			connection, acquireErr = reader.pool.Acquire(ctx)
			if reader.observer != nil {
				reader.observer.waitTime.Record(
					ctx,
					time.Since(started).Seconds(),
					poolAttributes,
				)
				if acquireErr != nil &&
					errors.Is(ctx.Err(), context.DeadlineExceeded) &&
					readerContext.Err() == nil {
					reader.observer.timeouts.Add(ctx, 1, poolAttributes)
				}
			}
			if acquireErr != nil {
				return classifyReaderFailure(
					readerContext,
					"acquire connection",
					acquireErr,
				)
			}
			return nil
		},
	)
	return connection, err
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
