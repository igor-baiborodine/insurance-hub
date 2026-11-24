package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres/dbgen"
)

// ErrNotFound reports that no offer or policy has the requested number.
var ErrNotFound = errors.New("policy storage record not found")

// Offer is a stored offer with its answers and per-cover prices. Amounts are exact decimals.
type Offer struct {
	ID           int64
	Number       string
	ProductCode  string
	Status       string
	CreationDate time.Time
	PolicyFrom   time.Time
	PolicyTo     time.Time
	TotalPrice   string
	Answers      map[string]string
	CoverPrices  map[string]string
}

// Policy is a stored policy with its versions in version-number order.
type Policy struct {
	ID         int64
	Number     string
	AgentLogin string
	Versions   []Version
}

// Version is one policy version with its covers.
type Version struct {
	ID            int64
	Number        int64
	ProductCode   string
	FirstName     string
	LastName      string
	Pesel         string
	AccountNumber string
	CoverFrom     time.Time
	CoverTo       time.Time
	VersionFrom   time.Time
	VersionTo     time.Time
	TotalPremium  string
	Covers        []Cover
}

// Cover is one priced cover of a policy version.
type Cover struct {
	Code  string
	Price string
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
	AgentLogin string
	Version    Version
	Intent     Intent
}

// Store reads and writes Policy aggregates through the writer pool.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps the process-owned writer pool.
func NewStore(pool *pgxpool.Pool) (*Store, error) {
	if pool == nil {
		return nil, errors.New("create Policy store: pool is required")
	}
	return &Store{pool: pool}, nil
}

// Offer loads one offer with its answers and cover prices.
func (s *Store) Offer(ctx context.Context, number string) (Offer, error) {
	queries := dbgen.New(s.pool)
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
	offer := Offer{
		ID:           row.ID,
		Number:       row.Number.String,
		ProductCode:  row.ProductCode.String,
		Status:       row.Status.String,
		CreationDate: row.CreationDate.Time,
		PolicyFrom:   row.PolicyFrom.Time,
		PolicyTo:     row.PolicyTo.Time,
		TotalPrice:   row.TotalPrice,
		Answers:      make(map[string]string, len(answers)),
		CoverPrices:  make(map[string]string, len(covers)),
	}
	for _, answer := range answers {
		offer.Answers[answer.QuestionCode] = answer.Answer.String
	}
	for _, cover := range covers {
		offer.CoverPrices[cover.CoverCode] = cover.Price
	}
	return offer, nil
}

// Policy loads one policy with every version and cover.
func (s *Store) Policy(ctx context.Context, number string) (Policy, error) {
	queries := dbgen.New(s.pool)
	row, err := queries.GetPolicy(ctx, text(number))
	if err != nil {
		return Policy{}, readError("policy", err)
	}
	versions, err := queries.ListPolicyVersions(ctx, pgtype.Int8{Int64: row.ID, Valid: true})
	if err != nil {
		return Policy{}, readError("policy versions", err)
	}
	covers, err := queries.ListPolicyCovers(ctx, pgtype.Int8{Int64: row.ID, Valid: true})
	if err != nil {
		return Policy{}, readError("policy covers", err)
	}
	policy := Policy{ID: row.ID, Number: row.Number.String, AgentLogin: row.AgentLogin.String}
	index := make(map[int64]int, len(versions))
	for _, version := range versions {
		index[version.ID] = len(policy.Versions)
		policy.Versions = append(policy.Versions, Version{
			ID:            version.ID,
			Number:        version.VersionNumber.Int64,
			ProductCode:   version.ProductCode.String,
			FirstName:     version.FirstName.String,
			LastName:      version.LastName.String,
			Pesel:         version.Pesel.String,
			AccountNumber: version.AccountNumber.String,
			CoverFrom:     version.CoverFrom.Time,
			CoverTo:       version.CoverTo.Time,
			VersionFrom:   version.VersionFrom.Time,
			VersionTo:     version.VersionTo.Time,
			TotalPremium:  version.TotalPremiumAmount,
		})
	}
	for _, cover := range covers {
		position := index[cover.PolicyVersionID.Int64]
		policy.Versions[position].Covers = append(policy.Versions[position].Covers,
			Cover{Code: cover.Code.String, Price: cover.Price})
	}
	return policy, nil
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
		return fmt.Errorf("begin Policy transaction: %w", err)
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
		Number: text(issuance.Number), AgentLogin: optionalText(issuance.AgentLogin),
	})
	if err != nil {
		return 0, fmt.Errorf("insert policy: %w", err)
	}
	version := issuance.Version
	versionID, err := t.queries.InsertPolicyVersion(ctx, dbgen.InsertPolicyVersionParams{
		PolicyID:      pgtype.Int8{Int64: policyID, Valid: true},
		VersionNumber: pgtype.Int8{Int64: version.Number, Valid: true},
		ProductCode:   text(version.ProductCode), FirstName: text(version.FirstName),
		LastName: text(version.LastName), Pesel: text(version.Pesel),
		AccountNumber: text(version.AccountNumber), CoverFrom: date(version.CoverFrom),
		CoverTo: date(version.CoverTo), VersionFrom: date(version.VersionFrom),
		VersionTo: date(version.VersionTo), TotalPremiumAmount: version.TotalPremium,
	})
	if err != nil {
		return 0, fmt.Errorf("insert policy version: %w", err)
	}
	for _, cover := range version.Covers {
		if err := t.queries.InsertCover(ctx, dbgen.InsertCoverParams{
			PolicyVersionID: pgtype.Int8{Int64: versionID, Valid: true},
			Code:            text(cover.Code), Price: cover.Price,
		}); err != nil {
			return 0, fmt.Errorf("insert cover: %w", err)
		}
	}
	if _, err := t.queries.InsertOutboxIntent(ctx, dbgen.InsertOutboxIntentParams{
		IntentID: pgtype.UUID{Bytes: issuance.Intent.ID, Valid: true}, PolicyID: policyID,
		Topic: issuance.Intent.Topic, MessageKey: issuance.Intent.Key,
		Payload: issuance.Intent.Payload,
	}); err != nil {
		return 0, fmt.Errorf("insert outbox intent: %w", err)
	}
	return policyID, nil
}

func readError(record string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return fmt.Errorf("read %s: %w", record, err)
}

func text(value string) pgtype.Text { return pgtype.Text{String: value, Valid: true} }

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}

func date(value time.Time) pgtype.Date {
	return pgtype.Date{Time: value, Valid: !value.IsZero()}
}
