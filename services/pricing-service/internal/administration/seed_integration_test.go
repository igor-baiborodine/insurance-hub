//go:build integration

package administration_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/service"
)

const javaFactory = "legacy/pricing-service/src/main/java/pl/altkom/asc/lab/micronaut/poc/pricing/init/DemoTariffsFactory.java"

type expectedTariff struct {
	Code   string
	Base   []expectedRule
	Markup []expectedRule
}

type expectedRule struct {
	Key       string  `json:"key"`
	Condition *string `json:"condition"`
	Value     string  `json:"value"`
}

func javaTariffs(t *testing.T) []expectedTariff {
	t.Helper()
	factory, err := os.ReadFile(filepath.Join(os.Getenv("INSURANCE_HUB_ROOT"), javaFactory))
	if err != nil {
		t.Fatal(err)
	}
	pattern := regexp.MustCompile(
		`new Tariff\("([A-Z]+)"\)|addBasePriceRule\("([^"]+)", (null|"[^"]*"), ("[^"]*")\)|addPercentMarkup\((null|"[^"]*"), new BigDecimal\("([^"]+)"\)\)`,
	)
	unquote := func(value string) *string {
		t.Helper()
		if value == "null" {
			return nil
		}
		decoded, err := strconv.Unquote(value)
		if err != nil {
			t.Fatal(err)
		}
		return &decoded
	}
	var tariffs []expectedTariff
	for _, match := range pattern.FindAllStringSubmatch(string(factory), -1) {
		switch {
		case match[1] != "":
			tariffs = append(tariffs, expectedTariff{Code: match[1]})
		case match[2] != "":
			current := &tariffs[len(tariffs)-1]
			current.Base = append(
				current.Base,
				expectedRule{match[2], unquote(match[3]), *unquote(match[4])},
			)
		default:
			current := &tariffs[len(tariffs)-1]
			current.Markup = append(
				current.Markup,
				expectedRule{"perc_markup", unquote(match[5]), match[6]},
			)
		}
	}
	// Keep the independently agreed corpus and counts explicit, so an incomplete parser cannot pass.
	counts := []string{"TRI:3/1", "HSI:6/2", "FAI:4/2", "CAR:1/1"}
	var observed []string
	for _, tariff := range tariffs {
		observed = append(
			observed,
			fmt.Sprintf("%s:%d/%d", tariff.Code, len(tariff.Base), len(tariff.Markup)),
		)
	}
	if !slices.Equal(observed, counts) {
		t.Fatalf("Java fixture counts differ: %v", observed)
	}
	return tariffs
}

func verifyOrderedSeed(
	t *testing.T, ctx context.Context, admin *pgx.Conn, address *url.URL,
	ca, targetPath, migrationCredential string,
) {
	t.Helper()
	// Given a role with the disposable grants that Step 3 will provision.
	password := rand.Text()
	execSQL(t, ctx, admin, fmt.Sprintf(setupSeedFixtureQuery, password))
	credential := filepath.Join(t.TempDir(), "seed-url")
	seederURL := *address
	seederURL.User = url.UserPassword("go_pricing_seeder", password)
	seederURL.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {ca}}.Encode()
	writeFixture(t, credential, []byte(seederURL.String()))
	run := func(credential string) (administration.SeedResult, error) {
		return service.RunSeed(
			ctx,
			"disposable",
			"pricing-smoke",
			targetPath,
			credential,
			ca,
		)
	}
	seed := func(t *testing.T, inserted, existing int) {
		t.Helper()
		result, err := run(credential)
		if err != nil || result.Inserted != inserted || result.Existing != existing {
			t.Fatalf(
				"want %d inserted and %d existing; got %+v (%v)",
				inserted,
				existing,
				result,
				err,
			)
		}
	}
	refuse := func(t *testing.T, path, want string) {
		t.Helper()
		result, err := run(path)
		if err == nil || !strings.Contains(err.Error(), want) ||
			strings.Contains(err.Error(), password) ||
			result != (administration.SeedResult{}) {
			t.Fatalf("expected seed refusal for %q and zero result", want)
		}
	}

	want := javaTariffs(t)
	t.Run("seed policy refusals", func(t *testing.T) {
		// given
		before := seedSnapshot(t, ctx, admin)
		original, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			writeFixture(t, targetPath, original)
			execSQL(t, ctx, admin, setSeedMarkerQuery, true)
		})
		var enrollment map[string]any
		if err := json.Unmarshal(original, &enrollment); err != nil {
			t.Fatal(err)
		}
		enrollment["seedAllowed"] = false
		disabled, err := json.Marshal(enrollment)
		if err != nil {
			t.Fatal(err)
		}

		// when
		refuse(t, migrationCredential, "seeder target")
		execSQL(t, ctx, admin, setSeedMarkerQuery, false)
		refuse(t, credential, "marker differs from enrollment")
		writeFixture(t, targetPath, disabled)
		refuse(t, credential, "seeding is disabled")

		// then
		if seedSnapshot(t, ctx, admin) != before {
			t.Fatal("rejected seed altered the corpus or history")
		}
	})

	t.Run("fresh seed and replay", func(t *testing.T) {
		// given
		execSQL(t, ctx, admin, truncateTariffsQuery)
		// when
		seed(t, 4, 0)
		// then
		assertOrderedTariffs(t, ctx, admin, want)
		fresh := seedSnapshot(t, ctx, admin)

		// when
		seed(t, 0, 4)
		// then
		if seedSnapshot(t, ctx, admin) != fresh {
			t.Fatal("fresh-seed replay changed state")
		}
	})

	t.Run("legacy adoption and replay", func(t *testing.T) {
		// given
		execSQL(t, ctx, admin, truncateTariffsQuery)
		insertLegacyTariffs(t, ctx, admin, want)
		identity := legacyIdentity(t, ctx, admin)

		// when
		seed(t, 0, 4)

		// then
		if legacyIdentity(t, ctx, admin) != identity {
			t.Fatal("adoption changed identifiers, associations, or Java rule values")
		}
		assertOrderedTariffs(t, ctx, admin, want)
		adopted := seedSnapshot(t, ctx, admin)

		// when
		result, err := service.RunMigration(ctx, "migrate", "disposable", "pricing-smoke",
			targetPath, migrationCredential, ca)
		// then
		if err != nil || result != "version=1" {
			t.Fatalf("legacy migration replay failed: %q (%v)", result, err)
		}
		// when
		seed(t, 0, 4)
		// then
		if seedSnapshot(t, ctx, admin) != adopted {
			t.Fatal("migration and seed replay changed adopted state")
		}
	})

	for _, conflict := range []struct{ name, change string }{
		{"changed expression", changeCarFormulaQuery},
		{"duplicate rule", duplicateCarRuleQuery},
		{"contradictory position", contradictCarPositionQuery},
	} {
		t.Run(conflict.name, func(t *testing.T) {
			// given
			execSQL(t, ctx, admin, truncateTariffsQuery)
			insertLegacyTariffs(t, ctx, admin, want)
			execSQL(t, ctx, admin, conflict.change)
			before := seedSnapshot(t, ctx, admin)

			// when
			refuse(t, credential, "seed conflict for CAR")

			// then
			if seedSnapshot(t, ctx, admin) != before {
				t.Fatal(
					"seed conflict changed rows, positions, sequence, or history",
				)
			}
		})
	}
}

func insertLegacyTariffs(
	t *testing.T,
	ctx context.Context,
	admin *pgx.Conn,
	tariffs []expectedTariff,
) {
	t.Helper()
	execSQL(t, ctx, admin, assumePricingOwnerQuery)
	for index, tariff := range tariffs {
		id := 101 + index
		_, err := admin.Exec(
			ctx,
			insertLegacyTariffQuery,
			id,
			tariff.Code,
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range slices.Backward(tariff.Base) {
			_, err := admin.Exec(ctx, insertLegacyBaseRuleQuery,
				id, rule.Key, rule.Condition, rule.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
		for position, rule := range slices.Backward(tariff.Markup) {
			_, err := admin.Exec(ctx, insertLegacyMarkupRuleQuery,
				id*10+position, rule.Key, id, rule.Condition, rule.Value)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	execSQL(t, ctx, admin, resetRoleQuery)
}

func assertOrderedTariffs(
	t *testing.T,
	ctx context.Context,
	admin *pgx.Conn,
	tariffs []expectedTariff,
) {
	t.Helper()
	if got := queryString(
		t,
		ctx,
		admin,
		countTariffsQuery,
	); got != "4" {
		t.Fatalf("expected four tariff rows, found %s", got)
	}
	for _, tariff := range tariffs {
		var base, markup string
		err := admin.QueryRow(ctx, readOrderedTariffQuery, tariff.Code).Scan(&base, &markup)
		if err != nil {
			t.Fatal(err)
		}
		for _, list := range []struct {
			name, got string
			want      []expectedRule
		}{{"base", base, tariff.Base}, {"markup", markup, tariff.Markup}} {
			var got []expectedRule
			if err := json.Unmarshal([]byte(list.got), &got); err != nil {
				t.Fatal(err)
			}
			if !slices.EqualFunc(got, list.want, func(a, b expectedRule) bool {
				return a.Key == b.Key && a.Value == b.Value &&
					(a.Condition == b.Condition ||
						(a.Condition != nil && b.Condition != nil && *a.Condition == *b.Condition))
			}) {
				t.Fatalf(
					"%s %s rules differ from Java order: %s",
					tariff.Code,
					list.name,
					list.got,
				)
			}
		}
	}
}

func legacyIdentity(t *testing.T, ctx context.Context, admin *pgx.Conn) string {
	t.Helper()
	return queryString(t, ctx, admin, snapshotLegacyIdentityQuery)
}

func seedSnapshot(t *testing.T, ctx context.Context, admin *pgx.Conn) string {
	t.Helper()
	return queryString(t, ctx, admin, snapshotSeedStateQuery)
}
