//go:build integration

package administration_test

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
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
	credential := seedCredential(t, address, ca, password)
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
		if queryString(t, ctx, admin, inspectIdentityCatalogQuery) != "true" {
			t.Fatal(
				"expected exactly the two bigint identity sequences in the fresh public schema",
			)
		}
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

	for _, conflict := range []struct{ name, change, code string }{
		{"changed expression", changeCarFormulaQuery, "CAR"},
		{"duplicate rule", duplicateCarRuleQuery, "CAR"},
		{"contradictory position", contradictCarPositionQuery, "CAR"},
		{"changed markup factor", changeHSIMarkupFactorQuery, "HSI"},
	} {
		t.Run(conflict.name, func(t *testing.T) {
			// given
			execSQL(t, ctx, admin, truncateTariffsQuery)
			seed(t, 4, 0)
			execSQL(t, ctx, admin, conflict.change)
			before := seedSnapshot(t, ctx, admin)

			// when
			refuse(t, credential, "seed conflict for "+conflict.code)

			// then
			if seedSnapshot(t, ctx, admin) != before {
				t.Fatal(
					"seed conflict changed rows, positions, or history",
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

func seedSnapshot(t *testing.T, ctx context.Context, admin *pgx.Conn) string {
	t.Helper()
	return queryString(t, ctx, admin, snapshotSeedStateQuery)
}

func seedCredential(t *testing.T, address *url.URL, ca, password string) string {
	t.Helper()
	credential := filepath.Join(t.TempDir(), "seed-url")
	seederURL := *address
	seederURL.User = url.UserPassword("go_pricing_seeder", password)
	seederURL.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {ca}}.Encode()
	writeFixture(t, credential, []byte(seederURL.String()))
	return credential
}

func verifyPriorSchema(t *testing.T, ctx context.Context, admin *pgx.Conn, address *url.URL,
	ca, targetPath, migrationCredential, password string,
) {
	t.Helper()
	// given
	execSQL(t, ctx, admin, setupPriorSchemaQuery)
	before := seedSnapshot(t, ctx, admin)

	// when
	status, err := service.RunMigration(ctx, "status", "disposable", "pricing-smoke",
		targetPath, migrationCredential, ca)
	if err != nil || status != "version=1" {
		t.Fatalf("prior schema status = %q (%v)", status, err)
	}
	result, err := service.RunSeed(
		ctx,
		"disposable",
		"pricing-smoke",
		targetPath,
		seedCredential(t, address, ca, password),
		ca,
	)
	if err != nil || result != (administration.SeedResult{Existing: 4}) {
		t.Fatalf("prior schema seed replay = %+v (%v)", result, err)
	}

	// then
	assertOrderedTariffs(t, ctx, admin, acceptedTariffs(t))
	if seedSnapshot(t, ctx, admin) != before {
		t.Fatal("managed seed replay changed prior tariff data or history")
	}
	execSQL(t, ctx, admin, verifyPriorInsertQuery)
}
