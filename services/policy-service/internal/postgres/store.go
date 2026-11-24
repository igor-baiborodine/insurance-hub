package postgres

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres/dbgen"
)

// ErrNotFound reports that no offer or policy has the requested number.
var ErrNotFound = errors.New("policy storage record not found")

const storageCleanupBudget = 5 * time.Second

// Offer is a stored offer with its answers and per-cover prices. Amounts are exact decimals.
type Offer struct {
	ID           int64
	Number       string
	ProductCode  *string
	Status       *string
	CreationDate *time.Time
	PolicyFrom   *time.Time
	PolicyTo     *time.Time
	TotalPrice   *string
	Answers      map[string]*string
	CoverPrices  map[string]*string
}

// Policy is a stored policy with its versions in version-number order.
type Policy struct {
	ID         int64
	Number     string
	AgentLogin *string
	Versions   []Version
}

// Version is one policy version with its covers.
type Version struct {
	ID            int64
	Number        *int64
	ProductCode   *string
	FirstName     *string
	LastName      *string
	Pesel         *string
	AccountNumber *string
	CoverFrom     *time.Time
	CoverTo       *time.Time
	VersionFrom   *time.Time
	VersionTo     *time.Time
	TotalPremium  *string
	Covers        []Cover
}

// Cover is one priced cover of a policy version.
type Cover struct {
	Code  *string
	Price *string
}

// Intent is one logical event intent, stored with the aggregate change that caused it.
type Intent struct {
	ID      [16]byte
	Topic   string
	Key     string
	Payload []byte
}

// Issuance is a new policy with its first version and registration intent.
type Issuance struct {
	Number     string
	AgentLogin *string
	Version    Version
	Intent     Intent
}

// TxBeginner starts transactions on the process-owned writer pool.
type TxBeginner interface {
	BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error)
}

// Store reads and writes Policy aggregates through the writer pool.
type Store struct {
	pool TxBeginner
}

// NewStore wraps the process-owned writer pool; the caller owns closing it.
func NewStore(pool TxBeginner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("create Policy store: pool is required")
	}
	return &Store{pool: pool}, nil
}

// Offer loads one offer with its answers and cover prices.
func (s *Store) Offer(ctx context.Context, number string) (result Offer, resultErr error) {
	resultErr = s.readOnly(ctx, func(queries *dbgen.Queries) (err error) {
		result, err = readOffer(ctx, queries, number)
		return err
	})
	return result, resultErr
}

func readOffer(ctx context.Context, queries *dbgen.Queries, number string) (Offer, error) {
	row, err := queries.GetOffer(ctx, text(number))
	if err != nil {
		return Offer{}, readError("offer", err)
	}
	answers, err := queries.ListOfferAnswers(ctx, row.ID)
	if err != nil {
		return Offer{}, readError("offer answers", err)
	}
	covers, err := queries.ListOfferCovers(ctx, row.ID)
	if err != nil {
		return Offer{}, readError("offer covers", err)
	}
	totalPrice, err := decimalValue(row.TotalPrice)
	if err != nil {
		return Offer{}, readError("offer total price", err)
	}
	result := Offer{
		ID:           row.ID,
		Number:       row.Number.String,
		ProductCode:  textValue(row.ProductCode),
		Status:       textValue(row.Status),
		CreationDate: dateValue(row.CreationDate),
		PolicyFrom:   dateValue(row.PolicyFrom),
		PolicyTo:     dateValue(row.PolicyTo),
		TotalPrice:   totalPrice,
		Answers:      make(map[string]*string, len(answers)),
		CoverPrices:  make(map[string]*string, len(covers)),
	}
	for _, answer := range answers {
		result.Answers[answer.QuestionCode] = textValue(answer.Answer)
	}
	for _, cover := range covers {
		price, err := decimalValue(cover.Price)
		if err != nil {
			return Offer{}, readError("offer cover price", err)
		}
		result.CoverPrices[cover.CoverCode] = price
	}
	return result, nil
}

// Policy loads one policy with every version and cover.
func (s *Store) Policy(ctx context.Context, number string) (result Policy, resultErr error) {
	resultErr = s.readOnly(ctx, func(queries *dbgen.Queries) (err error) {
		result, err = readPolicy(ctx, queries, number)
		return err
	})
	return result, resultErr
}

func readPolicy(ctx context.Context, queries *dbgen.Queries, number string) (Policy, error) {
	row, err := queries.GetPolicy(ctx, text(number))
	if err != nil {
		return Policy{}, readError("policy", err)
	}
	versions, err := queries.ListPolicyVersions(
		ctx,
		pgtype.Int8{Int64: row.ID, Valid: true},
	)
	if err != nil {
		return Policy{}, readError("policy versions", err)
	}
	covers, err := queries.ListPolicyCovers(
		ctx,
		pgtype.Int8{Int64: row.ID, Valid: true},
	)
	if err != nil {
		return Policy{}, readError("policy covers", err)
	}
	result := Policy{
		ID:         row.ID,
		Number:     row.Number.String,
		AgentLogin: textValue(row.AgentLogin),
	}
	index := make(map[int64]int, len(versions))
	for _, version := range versions {
		totalPremium, err := decimalValue(version.TotalPremiumAmount)
		if err != nil {
			return Policy{}, readError("policy version total premium", err)
		}
		index[version.ID] = len(result.Versions)
		result.Versions = append(result.Versions, Version{
			ID:            version.ID,
			Number:        int64Value(version.VersionNumber),
			ProductCode:   textValue(version.ProductCode),
			FirstName:     textValue(version.FirstName),
			LastName:      textValue(version.LastName),
			Pesel:         textValue(version.Pesel),
			AccountNumber: textValue(version.AccountNumber),
			CoverFrom:     dateValue(version.CoverFrom),
			CoverTo:       dateValue(version.CoverTo),
			VersionFrom:   dateValue(version.VersionFrom),
			VersionTo:     dateValue(version.VersionTo),
			TotalPremium:  totalPremium,
		})
	}
	for _, cover := range covers {
		price, err := decimalValue(cover.Price)
		if err != nil {
			return Policy{}, readError("policy cover price", err)
		}
		position, found := index[cover.PolicyVersionID.Int64]
		if !cover.PolicyVersionID.Valid || !found {
			return Policy{}, errors.New(
				"read policy covers: cover has no visible policy version",
			)
		}
		result.Versions[position].Covers = append(result.Versions[position].Covers,
			Cover{Code: textValue(cover.Code), Price: price})
	}
	return result, nil
}

func (s *Store) readOnly(
	ctx context.Context,
	work func(*dbgen.Queries) error,
) (resultErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return beginError(ctx, "begin Policy read transaction", err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			storageCleanupBudget,
		)
		defer cancel()
		if err := tx.Rollback(cleanup); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("roll back Policy read transaction: %w", err),
			)
		}
	}()
	if err := work(dbgen.New(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Policy read transaction: %w", err)
	}
	return nil
}

// Transaction writes Policy rows inside one database transaction.
type Transaction struct {
	queries *dbgen.Queries
}

// InTransaction commits every write made by work together, or none of them.
func (s *Store) InTransaction(
	ctx context.Context,
	work func(context.Context, Transaction) error,
) (resultErr error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return beginError(ctx, "begin Policy transaction", err)
	}
	defer func() {
		if err := tx.Rollback(context.WithoutCancel(ctx)); err != nil &&
			!errors.Is(err, pgx.ErrTxClosed) {
			resultErr = errors.Join(
				resultErr,
				fmt.Errorf("roll back Policy transaction: %w", err),
			)
		}
	}()
	if err := work(ctx, Transaction{queries: dbgen.New(tx)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit Policy transaction: %w", err)
	}
	return nil
}

// CreateOffer commits the offer with its answers and cover prices together, or none of them.
func (s *Store) CreateOffer(ctx context.Context, offer domain.Offer) error {
	return s.InTransaction(ctx, func(ctx context.Context, tx Transaction) error {
		_, err := tx.CreateOffer(ctx, offer)
		return err
	})
}

// CreateOffer writes the offer and its child rows. The table identity allocates the offer key;
// amounts are written as numeric text, never through floating point.
func (t Transaction) CreateOffer(ctx context.Context, offer domain.Offer) (int64, error) {
	offerID, err := t.queries.InsertOffer(ctx, dbgen.InsertOfferParams{
		Number:       text(offer.Number),
		ProductCode:  text(offer.ProductCode),
		Status:       text(offer.Status),
		CreationDate: date(offer.CreationDate),
		PolicyFrom:   date(offer.PolicyFrom),
		PolicyTo:     date(offer.PolicyTo),
		TotalPrice:   offer.TotalPrice,
	})
	if err != nil {
		return 0, fmt.Errorf("insert offer: %w", err)
	}
	for _, code := range slices.Sorted(maps.Keys(offer.Answers)) {
		if err := t.queries.InsertOfferAnswer(ctx, dbgen.InsertOfferAnswerParams{
			OfferID: offerID, QuestionCode: code, Answer: text(offer.Answers[code]),
		}); err != nil {
			return 0, fmt.Errorf("insert offer answer: %w", err)
		}
	}
	for _, code := range slices.Sorted(maps.Keys(offer.CoverPrices)) {
		if err := t.queries.InsertOfferCover(ctx, dbgen.InsertOfferCoverParams{
			OfferID: offerID, CoverCode: code, Price: offer.CoverPrices[code],
		}); err != nil {
			return 0, fmt.Errorf("insert offer cover: %w", err)
		}
	}
	return offerID, nil
}

// Issue commits a new policy, its first version and covers, and its event intent together.
func (s *Store) Issue(ctx context.Context, issuance Issuance) (policyID int64, err error) {
	err = s.InTransaction(ctx, func(ctx context.Context, tx Transaction) error {
		policyID, err = tx.Issue(ctx, issuance)
		return err
	})
	return policyID, err
}

// Issue writes the policy, version, covers and intent. Generated keys link dependent rows.
func (t Transaction) Issue(ctx context.Context, issuance Issuance) (int64, error) {
	policyID, err := t.queries.InsertPolicy(ctx, dbgen.InsertPolicyParams{
		Number: text(issuance.Number), AgentLogin: nullableText(issuance.AgentLogin),
	})
	if err != nil {
		return 0, fmt.Errorf("insert policy: %w", err)
	}
	if err := t.insertVersion(ctx, policyID, issuance.Version); err != nil {
		return 0, err
	}
	if err := t.insertIntent(ctx, policyID, issuance.Intent); err != nil {
		return 0, err
	}
	return policyID, nil
}

// insertVersion writes one policy version and its covers.
func (t Transaction) insertVersion(ctx context.Context, policyID int64, version Version) error {
	versionID, err := t.queries.InsertPolicyVersion(ctx, dbgen.InsertPolicyVersionParams{
		PolicyID:      pgtype.Int8{Int64: policyID, Valid: true},
		VersionNumber: nullableInt64(version.Number),
		ProductCode:   nullableText(version.ProductCode),
		FirstName:     nullableText(version.FirstName),
		LastName:      nullableText(version.LastName),
		Pesel:         nullableText(version.Pesel),
		AccountNumber: nullableText(version.AccountNumber),
		CoverFrom:     nullableDate(version.CoverFrom),
		CoverTo:       nullableDate(version.CoverTo),
		VersionFrom:   nullableDate(version.VersionFrom),
		VersionTo:     nullableDate(version.VersionTo),
		TotalPremiumAmount: nullableText(
			version.TotalPremium,
		),
	})
	if err != nil {
		return fmt.Errorf("insert policy version: %w", err)
	}
	for _, cover := range version.Covers {
		if err := t.queries.InsertCover(ctx, dbgen.InsertCoverParams{
			PolicyVersionID: pgtype.Int8{Int64: versionID, Valid: true},
			Code:            nullableText(cover.Code), Price: nullableText(cover.Price),
		}); err != nil {
			return fmt.Errorf("insert cover: %w", err)
		}
	}
	return nil
}

// insertIntent writes the event intent caused by a change to the policy.
func (t Transaction) insertIntent(ctx context.Context, policyID int64, intent Intent) error {
	if _, err := t.queries.InsertOutboxIntent(ctx, dbgen.InsertOutboxIntentParams{
		IntentID: pgtype.UUID{Bytes: intent.ID, Valid: true}, PolicyID: policyID,
		Topic: intent.Topic, MessageKey: intent.Key, Payload: intent.Payload,
	}); err != nil {
		return fmt.Errorf("insert outbox intent: %w", err)
	}
	return nil
}

// beginError keeps cancellation and deadlines distinct and reports a failed connection attempt,
// which happens before any statement runs, as unavailable storage.
func beginError(ctx context.Context, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("%s: %w", operation, ctxErr)
	}
	if _, connect := errors.AsType[*pgconn.ConnectError](err); connect {
		return fmt.Errorf("%s: %w: %w", operation, application.ErrStorageUnavailable, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func readError(record string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("read %s: %w", record, err)
}

func text(value string) pgtype.Text { return pgtype.Text{String: value, Valid: true} }

func date(value time.Time) pgtype.Date { return pgtype.Date{Time: value, Valid: true} }

func textValue(value pgtype.Text) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func dateValue(value pgtype.Date) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func int64Value(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func decimalValue(value pgtype.Numeric) (*string, error) {
	if !value.Valid {
		return nil, nil
	}
	encoded, err := value.Value()
	if err != nil {
		return nil, err
	}
	decimal, ok := encoded.(string)
	if !ok {
		return nil, errors.New("numeric value has unexpected representation")
	}
	if value.NaN || value.InfinityModifier != pgtype.Finite {
		return &decimal, nil
	}
	number, ok := new(big.Rat).SetString(decimal)
	if !ok {
		return nil, errors.New("numeric value cannot be represented exactly")
	}
	decimal = number.FloatString(2)
	return &decimal, nil
}

func nullableText(value *string) pgtype.Text {
	if value == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *value, Valid: true}
}

func nullableDate(value *time.Time) pgtype.Date {
	if value == nil {
		return pgtype.Date{}
	}
	return pgtype.Date{Time: *value, Valid: true}
}

func nullableInt64(value *int64) pgtype.Int8 {
	if value == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *value, Valid: true}
}
