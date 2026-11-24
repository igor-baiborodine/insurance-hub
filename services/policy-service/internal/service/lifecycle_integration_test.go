//go:build integration

package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/pricing/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

//go:embed testdata/sql/read-lifecycle-counts.sql
var readLifecycleCountsQuery string

//go:embed testdata/sql/read-issued-policy.sql
var readIssuedPolicyQuery string

//go:embed testdata/sql/set-offer-creation-date.sql
var setOfferCreationDateQuery string

//go:embed testdata/sql/reject-outbox-commit.sql
var rejectOutboxCommitQuery string

//go:embed testdata/sql/allow-outbox-commit.sql
var allowOutboxCommitQuery string

// TestRun_ProductionCompositionIssuesPolicies issues and reads policies through the production
// composition on disposable PostgreSQL holding a pre-existing adopted aggregate.
func TestRun_ProductionCompositionIssuesPolicies(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.LoadJavaData(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	product, pricing := &fakeProduct{}, &fakePricing{}
	providerAddress := serveProviders(t, product, pricing)
	addresses := startPolicyServers(t, ctx, map[string]string{
		"POLICY_DATABASE_URL":   fixture.WriterURL("policy"),
		"PRODUCT_GRPC_TARGET":   providerAddress,
		"PRICING_GRPC_TARGET":   providerAddress,
		"PROVIDER_CALL_TIMEOUT": "300ms",
	})
	client := newPolicyRPCClient(t, addresses.grpc)
	product.reset(nil)
	pricing.reset(nil, &pricingv1.CalculatePriceResponse{
		TotalPrice:   "123.45",
		CoversPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
	})
	code, body := postOffer(t, ctx, addresses.http, triOffer)
	if code != http.StatusOK {
		t.Fatalf("create offer: %d %s", code, body)
	}
	goOffer := requireOfferResponse(t, body)
	// The service's business zone is UTC; each offer's age is set relative to today there.
	today := time.Now().UTC()
	ageOffer := func(number string, days int) {
		t.Helper()
		if _, err := fixture.Admin.Exec(ctx, setOfferCreationDateQuery, number,
			today.AddDate(0, 0, -days).Format(time.DateOnly)); err != nil {
			t.Fatal(err)
		}
	}
	ageOffer(goOffer, 30)
	ageOffer(postgresfixture.JavaOfferNumber, 30)
	// Issuance must not consult Product or Pricing, even when both are unavailable.
	unavailable := status.Error(codes.Unavailable, "down")
	product.reset(unavailable)
	pricing.reset(unavailable, nil)
	counts := func() string { return fixture.Query(t, ctx, readLifecycleCountsQuery) }
	issue := func(offer string) (*policyv1.IssuePolicyResponse, error) {
		return client.IssuePolicy(ctx, &policyv1.IssuePolicyRequest{
			OfferNumber: offer,
			PolicyHolder: &policyv1.PolicyHolder{
				FirstName: "Synthetic", LastName: "Holder", TaxId: "00000000000",
			},
			AgentLogin: "synthetic.agent",
		})
	}
	var issued []string

	t.Run("S01 day-30 offers issue at the saved price", func(t *testing.T) {
		for _, test := range []struct {
			offer, wantPeriod, wantPremium string
		}{
			{
				goOffer, "cover=2017-04-16..2018-04-15;valid=2017-04-16..2018-04-15",
				"premium=123.45;covers=C1=100.10,C2=23.35",
			},
			{
				postgresfixture.JavaOfferNumber,
				"cover=2026-11-13..2027-11-13;valid=2026-11-13..2027-11-13",
				"premium=123.45;covers=C1=100.00,C2=23.45",
			},
		} {
			// when
			response, err := issue(test.offer)

			// then
			if err != nil || !uuidV4.MatchString(response.GetPolicyNumber()) {
				t.Fatalf("issue %s: %v (%v)", test.offer, response, err)
			}
			number := response.GetPolicyNumber()
			issued = append(issued, number)
			productCodes := map[string]string{
				goOffer: "TRI", postgresfixture.JavaOfferNumber: "HSI",
			}
			want := "agent=synthetic.agent;versions=1;version=1;" +
				"holder=Synthetic Holder/00000000000;product=" + productCodes[test.offer] + ";" +
				test.wantPeriod + ";" + test.wantPremium + ";intents=1/1"
			got, account, payload := readIssuedPolicy(t, ctx, fixture, number)
			if got != want || !uuidV4.MatchString(account) || account == number {
				t.Errorf("stored %q account %q, want %q", got, account, want)
			}
			requireRegistrationPayload(t, payload, number, test.wantPeriod,
				productCodes[test.offer])
		}
		if calls := len(product.order()) + len(pricing.order()); calls != 0 {
			t.Errorf(
				"provider calls = %v / %v, want none",
				product.order(),
				pricing.order(),
			)
		}
	})

	t.Run("S01 repeated issue creates a second policy and registration", func(t *testing.T) {
		// when
		response, err := issue(goOffer)

		// then
		if err != nil || slices.Contains(issued, response.GetPolicyNumber()) {
			t.Fatalf("repeat issue = %v (%v), earlier %v", response, err, issued)
		}
		if got, _, _ := readIssuedPolicy(
			t,
			ctx,
			fixture,
			response.GetPolicyNumber(),
		); !strings.
			HasSuffix(
				got,
				";intents=1/1",
			) {
			t.Errorf("repeat stored %q", got)
		}
		issued = append(issued, response.GetPolicyNumber())
	})

	t.Run("S03 rejected issuance leaves no aggregate or intent", func(t *testing.T) {
		for _, test := range []struct {
			name     string
			prepare  func()
			restore  func()
			offer    string
			wantCode codes.Code
		}{
			{
				name: "offer on day 31", offer: goOffer, wantCode: codes.FailedPrecondition,
				prepare: func() { ageOffer(goOffer, 31) }, restore: func() { ageOffer(goOffer, 30) },
			},
			{name: "absent offer", offer: "missing-offer", wantCode: codes.NotFound},
			{
				name: "rejected commit", offer: goOffer, wantCode: codes.Internal,
				prepare: func() { fixture.Exec(t, ctx, rejectOutboxCommitQuery) },
				restore: func() { fixture.Exec(t, ctx, allowOutboxCommitQuery) },
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				// given
				before := counts()
				if test.prepare != nil {
					test.prepare()
					t.Cleanup(test.restore)
				}

				// when
				_, err := issue(test.offer)

				// then
				if status.Code(err) != test.wantCode {
					t.Errorf("status = %v, want %v", err, test.wantCode)
				}
				if after := counts(); after != before {
					t.Errorf(
						"rejected issuance changed rows from %q to %q",
						before,
						after,
					)
				}
			})
		}
	})

	t.Run("S01 lookup returns the latest version", func(t *testing.T) {
		for _, test := range []struct {
			name, number string
			want         *policyv1.PolicyDetails
		}{
			{
				"adopted multi-version policy", postgresfixture.JavaPolicyNumber,
				&policyv1.PolicyDetails{
					Number: postgresfixture.JavaPolicyNumber, DateFrom: "2026-11-24",
					DateTo: "2027-11-13", PolicyHolder: "Synthetic Holder",
					TotalPremium: "3.71", ProductCode: "HSI",
					AccountNumber: "31f5d9a2-8b7c-4e61-a0d4-6c2b9e7f1a33",
					Covers:        []string{"C1 - 3.01", "C2 - 0.70"},
				},
			},
			{"issued policy", issued[0], &policyv1.PolicyDetails{
				Number: issued[0], DateFrom: "2017-04-16", DateTo: "2018-04-15",
				PolicyHolder: "Synthetic Holder", TotalPremium: "123.45", ProductCode: "TRI",
				Covers: []string{"C1 - 100.10", "C2 - 23.35"},
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				// when
				response, err := client.GetPolicy(ctx,
					&policyv1.GetPolicyRequest{PolicyNumber: test.number})

				// then
				got := response.GetPolicy()
				if test.want.AccountNumber == "" && got != nil {
					test.want.AccountNumber = got.GetAccountNumber()
				}
				if err != nil || got.String() != test.want.String() {
					t.Errorf("details = %v (%v), want %v", got, err, test.want)
				}
			})
		}
		_, err := client.GetPolicy(ctx, &policyv1.GetPolicyRequest{PolicyNumber: "missing"})
		if status.Code(err) != codes.NotFound {
			t.Errorf("absent policy status = %v, want NotFound", err)
		}
	})
}

func newPolicyRPCClient(t *testing.T, address string) policyv1.PolicyServiceClient {
	t.Helper()
	connection, err := grpcgo.NewClient(address,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return policyv1.NewPolicyServiceClient(connection)
}

func readIssuedPolicy(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
	number string,
) (facts, account, payload string) {
	t.Helper()
	if err := fixture.Admin.QueryRow(ctx, readIssuedPolicyQuery, number).
		Scan(&facts, &account, &payload); err != nil {
		t.Fatalf("read issued policy: %v", err)
	}
	return facts, account, payload
}

// requireRegistrationPayload checks the stored record value against the registration contract:
// a policy envelope with date arrays, a numeric premium and no agent.
func requireRegistrationPayload(t *testing.T, payload, number, period, product string) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var envelope map[string]map[string]any
	if err := decoder.Decode(&envelope); err != nil || len(envelope) != 1 {
		t.Fatalf("registration payload %s (%v)", payload, err)
	}
	event := envelope["policy"]
	from, to, _ := strings.Cut(
		strings.TrimPrefix(strings.Split(period, ";")[1], "valid="),
		"..",
	)
	keys := make([]string, 0, len(event))
	for key := range event {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{
		"from", "number", "policyHolder", "productCode", "to",
		"totalPremium",
	}) || event["number"] != number ||
		event["policyHolder"] != "Synthetic Holder" || event["productCode"] != product ||
		event["totalPremium"] != json.Number("123.45") ||
		dateArrayText(event["from"]) != from || dateArrayText(event["to"]) != to {
		t.Errorf("registration payload %s, want %s..%s", payload, from, to)
	}
}

// dateArrayText renders a [year, month, day] JSON array as YYYY-MM-DD.
func dateArrayText(value any) string {
	parts, ok := value.([]any)
	if !ok || len(parts) != 3 {
		return ""
	}
	text := make([]string, 0, 3)
	for index, part := range parts {
		number, ok := part.(json.Number)
		if !ok {
			return ""
		}
		digits := number.String()
		if index > 0 && len(digits) == 1 {
			digits = "0" + digits
		}
		text = append(text, digits)
	}
	return strings.Join(text, "-")
}
