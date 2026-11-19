//go:build integration

package administration_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/service"
)

type expectedTariff struct {
	Code string `json:"code"`
	Base []struct {
		Cover     string  `json:"cover"`
		Condition *string `json:"condition"`
		Formula   string  `json:"formula"`
	} `json:"base"`
	Markup []struct {
		Kind      string  `json:"kind"`
		Condition *string `json:"condition"`
		Factor    string  `json:"factor"`
	} `json:"markup"`
}

func acceptedTariffs(t *testing.T) []expectedTariff {
	t.Helper()
	content, err := os.ReadFile("testdata/accepted-tariffs.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Tariffs []expectedTariff `json:"tariffs"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture.Tariffs
}

func verifyOrderedSeed(
	t *testing.T, ctx context.Context, admin *pgx.Conn, address *url.URL,
	ca, targetPath, migrationCredential, password string,
) {
	t.Helper()
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

	want := acceptedTariffs(t)
	t.Run("fresh seed and replay", func(t *testing.T) {
		// given
		if got := queryString(t, ctx, admin, countTariffsQuery); got != "0" {
			t.Fatalf("fresh migration left %s tariffs before seeding", got)
		}
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

	t.Run("populated tariffs and replay", func(t *testing.T) {
		// given
		execSQL(t, ctx, admin, truncateTariffsQuery)
		insertUnpositionedTariffs(t, ctx, admin, want)
		identity := tariffIdentity(t, ctx, admin)

		// when
		seed(t, 0, 4)

		// then
		if tariffIdentity(t, ctx, admin) != identity {
			t.Fatal(
				"seeding changed existing tariff identifiers, associations, or rule values",
			)
		}
		assertOrderedTariffs(t, ctx, admin, want)
		adopted := seedSnapshot(t, ctx, admin)

		// when
		result, err := service.RunMigration(ctx, "migrate", "disposable", "pricing-smoke",
			targetPath, migrationCredential, ca)
		// then
		if err != nil || result != "version=1" {
			t.Fatalf(
				"migration replay on populated tariffs failed: %q (%v)",
				result,
				err,
			)
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
			insertUnpositionedTariffs(t, ctx, admin, want)
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
	t.Run("restore reviewed tariffs after conflict fixtures", func(t *testing.T) {
		// given
		execSQL(t, ctx, admin, truncateTariffsQuery)
		// when
		seed(t, 4, 0)
		// then
		assertOrderedTariffs(t, ctx, admin, want)
	})
}

func insertUnpositionedTariffs(
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
				id, rule.Cover, rule.Condition, rule.Formula)
			if err != nil {
				t.Fatal(err)
			}
		}
		for position, rule := range slices.Backward(tariff.Markup) {
			_, err := admin.Exec(ctx, insertLegacyMarkupRuleQuery,
				id*10+position, rule.Kind, id, rule.Condition, rule.Factor)
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
		var raw []byte
		if err := admin.QueryRow(ctx, readOrderedTariffQuery, tariff.Code).
			Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got expectedTariff
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, tariff) {
			t.Fatalf(
				"%s rules differ from the accepted tariff or rule order: %s",
				tariff.Code,
				raw,
			)
		}
	}
}

func tariffIdentity(t *testing.T, ctx context.Context, admin *pgx.Conn) string {
	t.Helper()
	return queryString(t, ctx, admin, snapshotLegacyIdentityQuery)
}

func seedSnapshot(t *testing.T, ctx context.Context, admin *pgx.Conn) string {
	t.Helper()
	return queryString(t, ctx, admin, snapshotSeedStateQuery)
}
