//go:build integration

package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"io"
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
// composition on disposable PostgreSQL holding a pre-existing multi-version aggregate.
func TestRun_ProductionCompositionIssuesPolicies(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	fixture.LoadLifecycleData(t, ctx)
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
	ageOffer(postgresfixture.LifecycleOfferNumber, 30)
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
				postgresfixture.LifecycleOfferNumber,
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
				goOffer: "TRI", postgresfixture.LifecycleOfferNumber: "HSI",
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
				"existing multi-version policy", postgresfixture.LifecyclePolicyNumber,
				&policyv1.PolicyDetails{
					Number: postgresfixture.LifecyclePolicyNumber, DateFrom: "2026-11-24",
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

	httpPolicies := "http://" + addresses.http + "/policies"
	issueBody := func(offer string) string {
		return `{"offerNumber":"` + offer + `","policyHolder":{"firstName":"Synthetic",` +
			`"lastName":"Holder","taxId":"00000000000"},"agentLogin":"synthetic.agent"}`
	}
	createOffer := func(t *testing.T, from, to time.Time) string {
		t.Helper()
		pricing.reset(nil, &pricingv1.CalculatePriceResponse{
			TotalPrice: "120.00", CoversPrices: map[string]string{"C1": "120.00"},
		})
		product.reset(nil)
		body := strings.NewReplacer("2017-04-16", from.Format(time.DateOnly),
			"2018-04-15", to.Format(time.DateOnly)).Replace(triOffer)
		code, response := postOffer(t, ctx, addresses.http, body)
		if code != http.StatusOK {
			t.Fatalf("create offer: %d %s", code, response)
		}
		return requireOfferResponse120(t, response)
	}

	t.Run("S01 private HTTP issues and reads the latest version", func(t *testing.T) {
		// when
		code, body := callPolicyHTTP(
			t,
			ctx,
			http.MethodPost,
			httpPolicies,
			issueBody(goOffer),
		)

		// then
		number := requirePolicyNumberBody(t, code, body)
		code, body = callPolicyHTTP(t, ctx, http.MethodGet, httpPolicies+"/"+number, "")
		requirePolicyDetailsBody(t, code, body, map[string]any{
			"number": number, "dateFrom": "2017-04-16", "dateTo": "2018-04-15",
			"policyHolder": "Synthetic Holder", "totalPremium": json.Number("123.45"),
			"productCode": "TRI", "covers": []string{"C1 - 100.10", "C2 - 23.35"},
		})
		code, body = callPolicyHTTP(t, ctx, http.MethodGet,
			httpPolicies+"/"+postgresfixture.LifecyclePolicyNumber, "")
		requirePolicyDetailsBody(t, code, body, map[string]any{
			"number": postgresfixture.LifecyclePolicyNumber, "dateFrom": "2026-11-24",
			"dateTo": "2027-11-13", "policyHolder": "Synthetic Holder",
			"totalPremium": json.Number("3.71"), "productCode": "HSI",
			"accountNumber": "31f5d9a2-8b7c-4e61-a0d4-6c2b9e7f1a33",
			"covers":        []string{"C1 - 3.01", "C2 - 0.70"},
		})
	})

	t.Run("S02 private HTTP terminates on the business date", func(t *testing.T) {
		// given
		// 181 of 365 covered days have elapsed today, so 120.00 becomes 59.51.
		from, to := today.AddDate(0, 0, -180), today.AddDate(0, 0, 184)
		offer := createOffer(t, from, to)
		code, body := callPolicyHTTP(
			t,
			ctx,
			http.MethodPost,
			httpPolicies,
			issueBody(offer),
		)
		number := requirePolicyNumberBody(t, code, body)

		// when
		code, body = callPolicyHTTP(t, ctx, http.MethodPost, httpPolicies+"/terminate",
			`{"policyNumber":"`+number+`"}`)

		// then
		if got := requirePolicyNumberBody(t, code, body); got != number {
			t.Fatalf("terminated %q, want %q", got, number)
		}
		code, body = callPolicyHTTP(t, ctx, http.MethodGet, httpPolicies+"/"+number, "")
		requirePolicyDetailsBody(t, code, body, map[string]any{
			"number": number, "dateFrom": today.AddDate(0, 0, 1).Format(time.DateOnly),
			"dateTo": to.Format(time.DateOnly), "policyHolder": "Synthetic Holder",
			"totalPremium": json.Number("59.51"), "productCode": "TRI",
			"covers": []string{"C1 - 59.51"},
		})
	})

	t.Run("S03 private HTTP rejections leave no aggregate or intent", func(t *testing.T) {
		future := createOffer(t, today.AddDate(0, 0, 1), today.AddDate(0, 0, 30))
		code, body := callPolicyHTTP(
			t,
			ctx,
			http.MethodPost,
			httpPolicies,
			issueBody(future),
		)
		futurePolicy := requirePolicyNumberBody(t, code, body)
		ageOffer(goOffer, 31)
		t.Cleanup(func() { ageOffer(goOffer, 30) })
		for _, test := range []struct {
			name, method, target, body string
			wantStatus                 int
			wantBody                   string
		}{
			{
				"expired offer", http.MethodPost, httpPolicies, issueBody(goOffer),
				409, errorBody(9, "conflict"),
			},
			{
				"absent offer", http.MethodPost, httpPolicies, issueBody("missing-offer"),
				404, errorBody(5, "not found"),
			},
			{
				"missing offer number", http.MethodPost, httpPolicies,
				`{"policyHolder":{"firstName":"Synthetic"}}`, 400, errorBody(3, "invalid request"),
			},
			{
				"unknown member", http.MethodPost, httpPolicies,
				strings.Replace(issueBody(goOffer), `"agentLogin"`, `"agent"`, 1),
				400, errorBody(3, "invalid request"),
			},
			{
				"absent policy lookup", http.MethodGet, httpPolicies + "/missing", "",
				404, errorBody(5, "not found"),
			},
			{
				"lookup with query", http.MethodGet, httpPolicies + "/missing?full=true", "",
				400, errorBody(3, "invalid request"),
			},
			{
				"absent policy termination", http.MethodPost, httpPolicies + "/terminate",
				`{"policyNumber":"missing"}`, 404, errorBody(5, "not found"),
			},
			{
				"termination before coverage", http.MethodPost, httpPolicies + "/terminate",
				`{"policyNumber":"` + futurePolicy + `"}`, 409, errorBody(9, "conflict"),
			},
			{
				"unsupported method", http.MethodDelete, httpPolicies + "/" + futurePolicy, "",
				405, errorBody(12, "method not allowed"),
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				// given
				before := counts()

				// when
				code, body := callPolicyHTTP(
					t,
					ctx,
					test.method,
					test.target,
					test.body,
				)

				// then
				if code != test.wantStatus || body != test.wantBody {
					t.Errorf("response = (%d, %s), want (%d, %s)", code, body,
						test.wantStatus, test.wantBody)
				}
				if after := counts(); after != before {
					t.Errorf(
						"rejection changed rows from %q to %q",
						before,
						after,
					)
				}
			})
		}
	})
}

func callPolicyHTTP(
	t *testing.T,
	ctx context.Context,
	method, target, body string,
) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, method, target, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response.StatusCode, string(data)
}

// requirePolicyNumberBody checks the exact {"policyNumber": UUID} command response.
func requirePolicyNumberBody(t *testing.T, code int, body string) string {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal([]byte(body), &response); err != nil || code != http.StatusOK ||
		len(response) != 1 {
		t.Fatalf("command response = (%d, %s)", code, body)
	}
	number, _ := response["policyNumber"].(string)
	if !uuidV4.MatchString(number) {
		t.Fatalf("command response = %s", body)
	}
	return number
}

// requirePolicyDetailsBody checks the private lookup body: a policy envelope with exactly eight
// members, [year, month, day] date arrays, a numeric premium and the covers as a set. A missing
// expected account number only has to be a UUID.
func requirePolicyDetailsBody(t *testing.T, code int, body string, want map[string]any) {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var envelope map[string]map[string]any
	if err := decoder.Decode(&envelope); err != nil || code != http.StatusOK ||
		len(envelope) != 1 || len(envelope["policy"]) != 8 {
		t.Fatalf("details response = (%d, %s)", code, body)
	}
	got := envelope["policy"]
	covers, _ := got["covers"].([]any)
	gotCovers := make([]string, 0, len(covers))
	for _, cover := range covers {
		text, _ := cover.(string)
		gotCovers = append(gotCovers, text)
	}
	slices.Sort(gotCovers)
	account, _ := got["accountNumber"].(string)
	wantAccount, accountGiven := want["accountNumber"].(string)
	if got["number"] != want["number"] || dateArrayText(got["dateFrom"]) != want["dateFrom"] ||
		dateArrayText(got["dateTo"]) != want["dateTo"] ||
		got["policyHolder"] != want["policyHolder"] ||
		got["totalPremium"] != want["totalPremium"] || got["productCode"] != want["productCode"] ||
		!slices.Equal(gotCovers, want["covers"].([]string)) ||
		accountGiven &&
			account != wantAccount || !accountGiven && !uuidV4.MatchString(account) {
		t.Errorf("details response %s, want %v", body, want)
	}
}

// requireOfferResponse120 checks a one-cover 120.00 offer response and returns its number.
func requireOfferResponse120(t *testing.T, body string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var response map[string]any
	if err := decoder.Decode(
		&response,
	); err != nil ||
		response["totalPrice"] != json.Number("120.00") {
		t.Fatalf("offer response = %s", body)
	}
	number, _ := response["offerNumber"].(string)
	return number
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
