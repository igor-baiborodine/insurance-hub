package administration

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
)

//go:embed seeds/base.json
var seedAsset []byte

type seedCatalog struct {
	Version int          `json:"version"`
	Tariffs []seedTariff `json:"tariffs"`
}

type seedTariff struct {
	Code   string       `json:"code"`
	Base   []baseRule   `json:"base"`
	Markup []markupRule `json:"markup"`
}

type baseRule struct {
	Cover     string  `json:"cover"`
	Condition *string `json:"condition"`
	Formula   string  `json:"formula"`
}

type markupRule struct {
	Kind      string  `json:"kind"`
	Condition *string `json:"condition"`
	Factor    string  `json:"factor"`
}

// SeedResult reports newly inserted and already present tariffs.
type SeedResult struct {
	Inserted int
	Existing int
}

type tariffAdoption struct {
	tariff seedTariff
	id     int64
	exists bool
	base   []positionUpdate
	markup []positionUpdate
}

// Seed validates every existing tariff before it inserts or assigns any positions.
func Seed(ctx context.Context, db *sql.DB, target Target) (SeedResult, error) {
	if target.SeedAllowed == nil || !*target.SeedAllowed {
		return SeedResult{}, errors.New("seeding is disabled for the Pricing enrollment")
	}
	var catalog seedCatalog
	if err := json.Unmarshal(
		seedAsset,
		&catalog,
	); err != nil || catalog.Version != 1 ||
		len(catalog.Tariffs) == 0 {
		return SeedResult{}, errors.New("invalid embedded Pricing seed version")
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return SeedResult{}, databaseError("begin Pricing seed", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(
		ctx,
		acquireAdministrationLockQuery,
		administrationLockID,
	); err != nil {
		return SeedResult{}, databaseError("acquire Pricing administration lock", err)
	}
	if err := inspectPlatform(ctx, tx, target, seederRole, seederRole); err != nil {
		return SeedResult{}, err
	}
	state, err := inspectSchema(ctx, tx)
	if err != nil {
		return SeedResult{}, err
	}
	if state != AppliedStatus {
		return SeedResult{}, errors.New("migrate the Pricing schema before seeding")
	}

	// Java does not acquire the administration lock. These relation locks allow readers
	// to continue and prevent Java writes from changing the adoption plan mid-transaction.
	if _, err := tx.ExecContext(ctx, lockTariffsQuery); err != nil {
		return SeedResult{}, databaseError("lock Pricing tariffs for adoption", err)
	}

	plans := make([]tariffAdoption, 0, len(catalog.Tariffs))
	for _, tariff := range catalog.Tariffs {
		plan, err := inspectTariff(ctx, tx, tariff)
		if err != nil {
			return SeedResult{}, err
		}
		plans = append(plans, plan)
	}
	var result SeedResult
	for _, plan := range plans {
		if plan.exists {
			if err := assignPositions(ctx, tx, plan); err != nil {
				return SeedResult{}, err
			}
			result.Existing++
		} else {
			if err := insertTariff(ctx, tx, plan.tariff); err != nil {
				return SeedResult{}, err
			}
			result.Inserted++
		}
	}
	if err := tx.Commit(); err != nil {
		return SeedResult{}, databaseError(
			"commit Pricing seed; inspect before retrying",
			err,
		)
	}
	return result, nil
}

type ruleValue struct {
	key, value sql.NullString
	condition  sql.NullString
}

type positionUpdate struct {
	row      string
	position int
}

func inspectTariff(ctx context.Context, tx *sql.Tx, tariff seedTariff) (tariffAdoption, error) {
	plan := tariffAdoption{tariff: tariff}
	var count int
	if err := tx.QueryRowContext(ctx, findTariffQuery, tariff.Code).
		Scan(&count, &plan.id); err != nil {
		return plan, databaseError("inspect Pricing tariff", err)
	}
	if count == 0 {
		return plan, nil
	}
	if count != 1 {
		return plan, seedConflict(tariff.Code, "multiple matching tariffs")
	}
	plan.exists = true
	base := make([]ruleValue, 0, len(tariff.Base))
	for _, rule := range tariff.Base {
		base = append(base, seedRuleValue(rule.Cover, rule.Condition, rule.Formula))
	}
	markup := make([]ruleValue, 0, len(tariff.Markup))
	for _, rule := range tariff.Markup {
		// PostgreSQL trims trailing decimal zeros for matching only. The stored factor is exact.
		factor := rule.Factor
		if strings.Contains(factor, ".") {
			factor = strings.TrimRight(strings.TrimRight(factor, "0"), ".")
		}
		markup = append(markup, seedRuleValue(rule.Kind, rule.Condition, factor))
	}
	var err error
	plan.base, err = matchRules(ctx, tx, readBaseRulesQuery, tariff.Code, plan.id, base)
	if err != nil {
		return plan, err
	}
	plan.markup, err = matchRules(ctx, tx, readMarkupRulesQuery, tariff.Code, plan.id, markup)
	return plan, err
}

func seedRuleValue(key string, condition *string, value string) ruleValue {
	rule := ruleValue{
		key:   sql.NullString{String: key, Valid: true},
		value: sql.NullString{String: value, Valid: true},
	}
	if condition != nil {
		rule.condition = sql.NullString{String: *condition, Valid: true}
	}
	return rule
}

func seedConflict(code, detail string) error {
	return errors.New("Pricing seed conflict for " + code + ": " + detail)
}

func matchRules(
	ctx context.Context, tx *sql.Tx, query, code string, tariffID int64, expected []ruleValue,
) ([]positionUpdate, error) {
	rows, err := tx.QueryContext(ctx, query, tariffID)
	if err != nil {
		return nil, databaseError("read Pricing rules", err)
	}
	defer func() { _ = rows.Close() }()
	updates := make([]positionUpdate, 0, len(expected))
	positions := make(map[ruleValue]int, len(expected))
	for index, definition := range expected {
		positions[definition] = index + 1
	}
	if len(positions) != len(expected) {
		return nil, seedConflict(code, "seed definitions are ambiguous")
	}
	for rows.Next() {
		var row string
		var rule ruleValue
		var position sql.NullInt64
		if err := rows.Scan(
			&row,
			&rule.key,
			&rule.condition,
			&rule.value,
			&position,
		); err != nil {
			return nil, databaseError("scan Pricing rule", err)
		}
		want, found := positions[rule]
		if !found || (position.Valid && position.Int64 != int64(want)) {
			return nil, seedConflict(code, "rule definition or position differs")
		}
		delete(positions, rule)
		if !position.Valid {
			updates = append(updates, positionUpdate{row, want})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("read Pricing rules to completion", err)
	}
	if len(positions) != 0 {
		return nil, seedConflict(code, "expected rule is missing")
	}
	return updates, nil
}

func assignPositions(ctx context.Context, tx *sql.Tx, plan tariffAdoption) error {
	// Hibernate's base-rule collection has no row ID. The ctid is transaction-local,
	// protected by the table lock and consumed once per matched row.
	for _, update := range plan.base {
		if _, err := tx.ExecContext(
			ctx,
			positionBaseRuleQuery,
			update.position,
			update.row,
		); err != nil {
			return databaseError("position existing Pricing base rule", err)
		}
	}
	for _, update := range plan.markup {
		if _, err := tx.ExecContext(
			ctx,
			positionMarkupRuleQuery,
			update.position,
			update.row,
		); err != nil {
			return databaseError("position existing Pricing markup", err)
		}
	}
	return nil
}

func insertTariff(ctx context.Context, tx *sql.Tx, tariff seedTariff) error {
	definition, err := json.Marshal(tariff)
	if err != nil {
		return errors.New("encode Pricing tariff")
	}
	if _, err := tx.ExecContext(ctx, insertTariffQuery, definition); err != nil {
		return databaseError("insert Pricing tariff and rules", err)
	}
	return nil
}
