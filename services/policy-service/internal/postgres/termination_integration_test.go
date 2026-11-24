//go:build integration

package postgres_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

//go:embed testdata/sql/insert-termination-policies.sql
var insertTerminationPoliciesQuery string

//go:embed testdata/sql/read-policy-history.sql
var readPolicyHistoryQuery string

//go:embed testdata/sql/reject-outbox-commit.sql
var rejectOutboxCommitQuery string

//go:embed testdata/sql/allow-outbox-commit.sql
var allowOutboxCommitQuery string

const annualVersionOne = "v1 2026-01-01..2026-12-31 valid 2026-01-01..2026-12-31 " +
	"premium 120.00 covers C1=120.00"

// TestRun_PolicyTerminationAppendsHistory terminates stored policies through the use case, the
// restricted writer and one real transaction, with the business date under test control.
func TestRun_PolicyTerminationAppendsHistory(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=2")
	fixture.Exec(t, ctx, insertTerminationPoliciesQuery)
	pool, err := postgres.OpenPool(ctx, postgres.PoolSettings{
		URL: fixture.WriterURL(
			"policy",
		),
		MaxConnections: 4,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	businessDate := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	terminate, err := application.NewTerminatePolicy(store,
		func() time.Time { return businessDate }, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	read, err := application.NewGetPolicy(store)
	if err != nil {
		t.Fatal(err)
	}
	history := func(number string) policyHistory {
		t.Helper()
		return readPolicyHistory(t, ctx, fixture, number)
	}

	t.Run("S03 rejected termination leaves history and intents unchanged", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			number  string
			date    time.Time
			prepare string
			restore string
			want    error
		}{
			{
				name: "before coverage", number: "termination-annual",
				date: time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),
				want: domain.ErrTerminationOutsideCoverage,
			},
			{
				name: "after coverage", number: "termination-annual",
				date: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
				want: domain.ErrTerminationOutsideCoverage,
			},
			{
				name: "absent policy", number: "missing",
				date: businessDate, want: application.ErrPolicyNotFound,
			},
			{
				name: "rejected commit", number: "termination-annual", date: businessDate,
				prepare: rejectOutboxCommitQuery, restore: allowOutboxCommitQuery,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				// given
				before := history("termination-annual")
				previous := businessDate
				businessDate = test.date
				t.Cleanup(func() { businessDate = previous })
				if test.prepare != "" {
					fixture.Exec(t, ctx, test.prepare)
					t.Cleanup(func() { fixture.Exec(t, ctx, test.restore) })
				}

				// when
				_, err := terminate.Execute(ctx, test.number)

				// then
				if err == nil || test.want != nil && !errors.Is(err, test.want) {
					t.Errorf("error = %v, want %v", err, test.want)
				}
				if after := history("termination-annual"); after != before ||
					after.versions != annualVersionOne || after.intents != 0 {
					t.Errorf("history changed from %+v to %+v", before, after)
				}
			})
		}
	})

	t.Run("S02 June 30 termination appends 59.51 and keeps version 1", func(t *testing.T) {
		// when
		number, err := terminate.Execute(ctx, "termination-annual")

		// then
		if err != nil || number != "termination-annual" {
			t.Fatalf("terminate = %q (%v)", number, err)
		}
		got := history(number)
		wantVersions := annualVersionOne + " | v2 2026-01-01..2026-06-30 " +
			"valid 2026-07-01..2026-12-31 premium 59.51 covers C1=59.51"
		if got.versions != wantVersions || got.intents != 1 ||
			got.topic != "policy-terminated" || got.key != number {
			t.Errorf("history = %+v, want versions %q and one keyed termination intent",
				got, wantVersions)
		}
		requireTerminationPayload(t, got.payload, map[string]any{
			"number": number,
			"from": []any{
				json.Number("2026"), json.Number("1"),
				json.Number("1"),
			},
			"to": []any{
				json.Number("2026"),
				json.Number("6"),
				json.Number("30"),
			},
			"policyHolder": "Synthetic Holder",
			"productCode":  "HSI",
			"totalPremium": json.Number("59.51"),
		})
		details, err := read.Execute(ctx, number)
		if err != nil || details.Latest.Number != 2 ||
			details.Latest.VersionFrom != time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) ||
			details.Latest.VersionTo != time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC) ||
			details.Latest.TotalPremium != "59.51" || details.Latest.AccountNumber != "annual-account" {
			t.Errorf("latest details = %+v (%v)", details, err)
		}
	})

	t.Run("S02 each cover is rounded before the premium is summed", func(t *testing.T) {
		// when
		_, err := terminate.Execute(ctx, "termination-two-day")

		// then
		got := history("termination-two-day")
		if err != nil || !strings.HasSuffix(got.versions,
			" | v2 2026-06-30..2026-06-30 valid 2026-07-01..2026-07-01 premium 0.06 "+
				"covers C1=0.03,C2=0.03") {
			t.Errorf("history = %+v (%v), want two 0.03 covers totaling 0.06", got, err)
		}
	})
}

type policyHistory struct {
	versions, topic, key, payload string
	intents                       int64
}

func readPolicyHistory(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	number string,
) policyHistory {
	t.Helper()
	var history policyHistory
	var latest string
	if err := fixture.Admin.QueryRow(ctx, readPolicyHistoryQuery, number).
		Scan(&history.versions, &history.intents, &latest); err != nil {
		t.Fatal(err)
	}
	history.topic, latest, _ = strings.Cut(latest, " ")
	history.key, history.payload, _ = strings.Cut(latest, " ")
	return history
}

// requireTerminationPayload compares the stored record value with the independently stated
// termination envelope; the event carries no agent.
func requireTerminationPayload(t *testing.T, payload string, want map[string]any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var envelope map[string]map[string]any
	if err := decoder.Decode(&envelope); err != nil || len(envelope) != 1 ||
		!reflect.DeepEqual(envelope["policy"], want) {
		t.Errorf("termination payload %s (%v), want policy %v", payload, err, want)
	}
}
