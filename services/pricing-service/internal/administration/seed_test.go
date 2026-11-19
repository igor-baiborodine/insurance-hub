package administration

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestDecodeSeedCatalog_ContainsAcceptedTariffs(t *testing.T) {
	// given
	content, err := os.ReadFile("testdata/accepted-tariffs.json")
	if err != nil {
		t.Fatal(err)
	}
	var want seedCatalog
	if err := json.Unmarshal(content, &want); err != nil {
		t.Fatal(err)
	}

	// when
	got, err := decodeSeedCatalog(seedAsset)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != want.Version || len(got.Tariffs) != len(want.Tariffs) {
		t.Fatalf(
			"catalog version and tariff count = %d/%d, want %d/%d",
			got.Version, len(got.Tariffs), want.Version, len(want.Tariffs),
		)
	}
	for i, tariff := range want.Tariffs {
		if !reflect.DeepEqual(got.Tariffs[i], tariff) {
			t.Errorf(
				"tariff %s differs from the accepted values or rule order",
				tariff.Code,
			)
		}
	}
}

func TestDecodeSeedCatalog_RejectsInvalidInput(t *testing.T) {
	// given
	for _, test := range []struct{ name, content, want string }{
		{"unknown field", `{"version":1,"tariffs":[],"unexpected":true}`, "unknown field"},
		{"trailing content", `{"version":1,"tariffs":[{"code":"TRI"}]} {}`, "trailing content"},
		{"unsupported version", `{"version":2,"tariffs":[{"code":"TRI"}]}`, "seed version"},
		{"empty catalog", `{"version":1,"tariffs":[]}`, "empty Pricing seed catalog"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			content := []byte(test.content)

			// when
			_, err := decodeSeedCatalog(content)

			// then
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("decode error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestSeed_RejectsDisabledEnrollmentBeforeDatabaseAccess(t *testing.T) {
	// given
	disabled := false
	target := Target{SeedAllowed: &disabled}

	// when
	result, err := Seed(t.Context(), nil, target)

	// then
	if result != (SeedResult{}) || err == nil ||
		!strings.Contains(err.Error(), "seeding is disabled") {
		t.Fatalf("disabled enrollment result = %+v, error = %v", result, err)
	}
}

func TestSeed_ReportsTransactionFailuresWithoutClaimingSuccess(t *testing.T) {
	// given
	privateCause := errors.New("private database detail")
	for _, test := range []struct {
		name, diagnostic                        string
		failInsert, inserts, commits, rollbacks int
		commitError                             error
	}{
		{
			name: "third insert fails", diagnostic: "insert Pricing tariff and rules failed",
			failInsert: 3, inserts: 3, rollbacks: 1,
		},
		{
			name: "commit result is uncertain", diagnostic: "commit Pricing seed; inspect before retrying",
			inserts: 4, commits: 1, commitError: privateCause,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			enabled := true
			target := Target{SeedAllowed: &enabled}
			inserts := 0
			connection := &transactionTestConnection{
				exec: func(query string) error {
					switch query {
					case acquireAdministrationLockQuery, lockTariffsQuery:
						return nil
					case insertTariffQuery:
						inserts++
						if inserts == test.failInsert {
							return privateCause
						}
						return nil
					}
					return errors.New("unexpected seed statement")
				},
				query: func(query string) ([]driver.Value, error) {
					switch query {
					case inspectPlatformQuery,
						inspectPlatformMarkerQuery,
						inspectHistorySchemaQuery,
						inspectPricingSchemaQuery:
						return []driver.Value{true}, nil
					case inspectSchemaObjectsQuery:
						return []driver.Value{true, true}, nil
					case inspectHistoryRowsQuery:
						return []driver.Value{AppliedStatus}, nil
					case findTariffQuery:
						return []driver.Value{int64(0), int64(0)}, nil
					}
					return nil, errors.New("unexpected seed query")
				},
				commitError: test.commitError,
			}
			db := openTransactionTestDB(t, connection)

			// when
			got, err := Seed(t.Context(), db, target)

			// then
			if got != (SeedResult{}) || err == nil ||
				!strings.Contains(err.Error(), test.diagnostic) ||
				strings.Contains(err.Error(), privateCause.Error()) {
				t.Fatalf(
					"seed result = %+v, error = %v, want redacted failure",
					got,
					err,
				)
			}
			if connection.begins != 1 || inserts != test.inserts ||
				connection.commits != test.commits || connection.rollbacks != test.rollbacks {
				t.Fatalf(
					"seed transactions = %d begins, %d inserts, %d commits, %d rollbacks",
					connection.begins,
					inserts,
					connection.commits,
					connection.rollbacks,
				)
			}
		})
	}
}
