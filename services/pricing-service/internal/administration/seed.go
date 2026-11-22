package administration

import (
	"bytes"
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

func decodeSeedCatalog(content []byte) (seedCatalog, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var catalog seedCatalog
	if err := decoder.Decode(&catalog); err != nil {
		return seedCatalog{}, fmt.Errorf("decode Pricing seed catalog: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		return seedCatalog{}, errors.New(
			"unexpected trailing content in Pricing seed catalog",
		)
	}
	if catalog.Version != 1 {
		return seedCatalog{}, errors.New("unsupported Pricing seed version")
	}
	if len(catalog.Tariffs) == 0 {
		return seedCatalog{}, errors.New("empty Pricing seed catalog")
	}
	return catalog, nil
}

// Seed validates every existing tariff before inserting missing tariffs.
func Seed(ctx context.Context, db *sql.DB, target Target) (SeedResult, error) {
	if target.SeedAllowed == nil || !*target.SeedAllowed {
		return SeedResult{}, errors.New("seeding is disabled for the Pricing enrollment")
	}
	catalog, err := decodeSeedCatalog(seedAsset)
	if err != nil {
		return SeedResult{}, err
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

	// The relation locks allow reads and keep concurrent writes from changing
	// the tariff definitions during verification.
	if _, err := tx.ExecContext(ctx, lockTariffsQuery); err != nil {
		return SeedResult{}, databaseError("lock Pricing tariffs", err)
	}

	var result SeedResult
	var missing []seedTariff
	for _, tariff := range catalog.Tariffs {
		exists, err := inspectTariff(ctx, tx, tariff)
		if err != nil {
			return SeedResult{}, err
		}
		if exists {
			result.Existing++
		} else {
			missing = append(missing, tariff)
		}
	}
	for _, tariff := range missing {
		if err := insertTariff(ctx, tx, tariff); err != nil {
			return SeedResult{}, err
		}
		result.Inserted++
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

func inspectTariff(ctx context.Context, tx *sql.Tx, tariff seedTariff) (bool, error) {
	var id int64
	var count int
	if err := tx.QueryRowContext(ctx, findTariffQuery, tariff.Code).
		Scan(&count, &id); err != nil {
		return false, databaseError("inspect Pricing tariff", err)
	}
	if count == 0 {
		return false, nil
	}
	if count != 1 {
		return false, seedConflict(tariff.Code, "multiple matching tariffs")
	}
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
	if err := matchRules(ctx, tx, readBaseRulesQuery, tariff.Code, id, base); err != nil {
		return false, err
	}
	err := matchRules(ctx, tx, readMarkupRulesQuery, tariff.Code, id, markup)
	return err == nil, err
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
) error {
	rows, err := tx.QueryContext(ctx, query, tariffID)
	if err != nil {
		return databaseError("read Pricing rules", err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var rule ruleValue
		var position sql.NullInt64
		if err := rows.Scan(
			&rule.key,
			&rule.condition,
			&rule.value,
			&position,
		); err != nil {
			return databaseError("scan Pricing rule", err)
		}
		if count >= len(expected) || !position.Valid || position.Int64 != int64(count+1) ||
			rule != expected[count] {
			return seedConflict(code, "rule definition or position differs")
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return databaseError("read Pricing rules to completion", err)
	}
	if count != len(expected) {
		return seedConflict(code, "expected rule is missing")
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
