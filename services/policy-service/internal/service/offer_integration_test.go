//go:build integration

package service_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/pricing/v1"
	productv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/service"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

//go:embed testdata/sql/read-offer.sql
var readOfferQuery string

//go:embed testdata/sql/read-offer-counts.sql
var readOfferCountsQuery string

const (
	traceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	traceParent = "00-" + traceID + "-00f067aa0ba902b7-01"
	triOffer    = `{"productCode":"TRI","policyFrom":"2017-04-16","policyTo":"2018-04-15",` +
		`"selectedCovers":["C1","C2"],"answers":[` +
		`{"type":"choice","questionCode":"DESTINATION","answer":"EUR"},` +
		`{"type":"numeric","questionCode":"NUM_OF_ADULTS","answer":1},` +
		`{"type":"numeric","questionCode":"NUM_OF_CHILDREN","answer":1e0}]}`
)

var uuidV4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

// TestRun_ProductionCompositionCreatesOffers drives the private HTTP endpoint through the
// production composition, controlled Product and Pricing servers and disposable PostgreSQL.
func TestRun_ProductionCompositionCreatesOffers(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=1")
	product, pricing := &fakeProduct{}, &fakePricing{}
	providerAddress := serveProviders(t, product, pricing)
	address := startPolicy(t, ctx, map[string]string{
		"POLICY_DATABASE_URL":   fixture.WriterURL("policy"),
		"PRODUCT_GRPC_TARGET":   providerAddress,
		"PRICING_GRPC_TARGET":   providerAddress,
		"PROVIDER_CALL_TIMEOUT": "300ms",
	})
	counts := func() string { return fixture.Query(t, ctx, readOfferCountsQuery) }

	t.Run("S02 stores the fractional quote and returns numeric amounts", func(t *testing.T) {
		// given
		product.reset(nil)
		pricing.reset(nil, &pricingv1.CalculatePriceResponse{
			TotalPrice:   "123.45",
			CoversPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
		})
		today := time.Now().UTC().Format(time.DateOnly)

		// when
		code, body := postOffer(t, t.Context(), address, triOffer)

		// then
		if code != http.StatusOK {
			t.Fatalf("status = %d, body %s", code, body)
		}
		number := requireOfferResponse(t, body)
		want := "TRI|NEW|" + today + "|2017-04-16|2018-04-15|123.45|" +
			"DESTINATION=EUR,NUM_OF_ADULTS=1,NUM_OF_CHILDREN=1|C1=100.10,C2=23.35"
		var stored string
		if err := fixture.Admin.QueryRow(ctx, readOfferQuery, number).
			Scan(&stored); err != nil ||
			stored != want {
			t.Fatalf("stored offer = %q (%v), want %q", stored, err, want)
		}
		if got := product.order(); !reflect.DeepEqual(got, []string{"product:TRI"}) {
			t.Errorf("product calls = %v", got)
		}
		wantPricing := &pricingv1.CalculatePriceRequest{
			ProductCode: "TRI", PolicyFrom: "2017-04-16", PolicyTo: "2018-04-15",
			SelectedCovers: []string{"C1", "C2"},
			Answers: []*pricingv1.Answer{
				{
					QuestionCode: "DESTINATION",
					Value:        &pricingv1.Answer_Text{Text: "EUR"},
				},
				{
					QuestionCode: "NUM_OF_ADULTS",
					Value:        &pricingv1.Answer_Number{Number: "1"},
				},
				{
					QuestionCode: "NUM_OF_CHILDREN",
					Value:        &pricingv1.Answer_Number{Number: "1"},
				},
			},
		}
		if got := pricing.lastRequest(); got.String() != wantPricing.String() {
			t.Errorf("pricing request = %v, want %v", got, wantPricing)
		}
		if !strings.Contains(product.trace(), traceID) ||
			!strings.Contains(pricing.trace(), traceID) {
			t.Errorf("provider trace context = %q / %q, want trace %s",
				product.trace(), pricing.trace(), traceID)
		}
	})

	for _, test := range []struct {
		name           string
		body           string
		productErr     error
		pricingErr     error
		wantStatus     int
		wantBody       string
		wantPricingRPC int
	}{
		{
			name: "S03 malformed date", body: strings.Replace(triOffer, "2017-04-16", "2017-02-30", 1),
			wantStatus: 400, wantBody: errorBody(3, "invalid request"),
		},
		{
			name: "S04 unknown product", productErr: status.Error(codes.NotFound, "product not found"),
			wantStatus: 404, wantBody: errorBody(5, "not found"),
		},
		{
			name: "S04 product unavailable", productErr: status.Error(codes.Unavailable, "down"),
			wantStatus: 503, wantBody: errorBody(14, "service unavailable"),
		},
		{
			name: "S04 product deadline", productErr: errBlock,
			wantStatus: 504, wantBody: errorBody(4, "deadline exceeded"),
		},
		{
			name: "S05 pricing rejects input", pricingErr: status.Error(codes.InvalidArgument, "bad"),
			wantStatus: 400, wantBody: errorBody(3, "invalid request"), wantPricingRPC: 1,
		},
		{
			name: "S05 pricing unavailable", pricingErr: status.Error(codes.Unavailable, "down"),
			wantStatus: 503, wantBody: errorBody(14, "service unavailable"), wantPricingRPC: 1,
		},
		{
			name: "S05 pricing deadline", pricingErr: errBlock,
			wantStatus: 504, wantBody: errorBody(4, "deadline exceeded"), wantPricingRPC: 1,
		},
		{
			name: "unexpected pricing defect", pricingErr: status.Error(codes.Internal, "private-marker"),
			wantStatus: 500, wantBody: errorBody(13, "internal error"), wantPricingRPC: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			product.reset(test.productErr)
			pricing.reset(
				test.pricingErr,
				&pricingv1.CalculatePriceResponse{TotalPrice: "78"},
			)
			body := test.body
			if body == "" {
				body = triOffer
			}
			before := counts()

			// when
			code, got := postOffer(t, t.Context(), address, body)

			// then
			if code != test.wantStatus || got != test.wantBody {
				t.Errorf(
					"response = (%d, %s), want (%d, %s)",
					code,
					got,
					test.wantStatus,
					test.wantBody,
				)
			}
			wantProduct := 1
			if test.wantStatus == 400 && test.wantPricingRPC == 0 {
				wantProduct = 0
			}
			if len(product.order()) != wantProduct ||
				len(pricing.order()) != test.wantPricingRPC {
				t.Errorf(
					"provider calls = %v / %v",
					product.order(),
					pricing.order(),
				)
			}
			if after := counts(); after != before {
				t.Errorf("failed request changed rows from %q to %q", before, after)
			}
		})
	}

	t.Run("S06 caller cancellation stops before persistence", func(t *testing.T) {
		// given
		product.reset(nil)
		pricing.reset(errBlock, nil)
		before := counts()
		requestCtx, cancelRequest := context.WithCancel(t.Context())
		go func() {
			<-pricing.started()
			cancelRequest()
		}()

		// when
		_, err := postOfferRequest(requestCtx, address, triOffer)

		// then
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request error = %v, want caller cancellation", err)
		}
		select {
		case <-pricing.ended():
		case <-time.After(5 * time.Second):
			t.Fatal("pending Pricing call did not observe the cancellation")
		}
		if after := counts(); after != before {
			t.Errorf("canceled request changed rows from %q to %q", before, after)
		}
	})
}

var errBlock = errors.New("block until the call context ends")

func startPolicy(t *testing.T, ctx context.Context, environment map[string]string) string {
	t.Helper()
	addresses := startPolicyServers(t, ctx, environment)
	return addresses.http
}

// policyAddresses are the bound business HTTP and gRPC addresses of a started service.
type policyAddresses struct{ http, grpc string }

func startPolicyServers(
	t *testing.T,
	ctx context.Context,
	environment map[string]string,
) policyAddresses {
	t.Helper()
	for name, value := range map[string]string{
		"HTTP_ADDR": "127.0.0.1:0", "GRPC_ADDR": "127.0.0.1:0", "HEALTH_ADDR": "127.0.0.1:0",
	} {
		t.Setenv(name, value)
	}
	for name, value := range environment {
		t.Setenv(name, value)
	}
	settings, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	started := &startedHandler{addresses: make(chan policyAddresses, 1)}
	runCtx, stop := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() { result <- service.Run(runCtx, settings, slog.New(started)) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("run service: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Error("service did not stop")
		}
	})
	select {
	case addresses := <-started.addresses:
		return addresses
	case err := <-result:
		t.Fatalf("service stopped before starting: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("service did not start")
	}
	return policyAddresses{}
}

// startedHandler reports the bound business addresses from the startup record.
type startedHandler struct{ addresses chan policyAddresses }

func (*startedHandler) Enabled(context.Context, slog.Level) bool   { return true }
func (handler *startedHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *startedHandler) WithGroup(string) slog.Handler      { return handler }

func (handler *startedHandler) Handle(_ context.Context, record slog.Record) error {
	if record.Message != "service started" {
		return nil
	}
	var addresses policyAddresses
	record.Attrs(func(attribute slog.Attr) bool {
		switch attribute.Key {
		case "http_address":
			addresses.http = attribute.Value.String()
		case "grpc_address":
			addresses.grpc = attribute.Value.String()
		}
		return true
	})
	handler.addresses <- addresses
	return nil
}

func postOffer(t *testing.T, ctx context.Context, address, body string) (int, string) {
	t.Helper()
	response, err := postOfferRequest(ctx, address, body)
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

func postOfferRequest(ctx context.Context, address, body string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		"http://"+address+"/offers",
		strings.NewReader(body),
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("traceparent", traceParent)
	return (&http.Client{Timeout: 10 * time.Second}).Do(request)
}

// requireOfferResponse checks the exact response shape and numeric amounts.
func requireOfferResponse(t *testing.T, body string) string {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.UseNumber()
	var response map[string]any
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	number, _ := response["offerNumber"].(string)
	covers, _ := response["coversPrices"].(map[string]any)
	if len(response) != 3 || !uuidV4.MatchString(number) ||
		response["totalPrice"] != json.Number("123.45") || len(covers) != 2 ||
		covers["C1"] != json.Number("100.10") || covers["C2"] != json.Number("23.35") {
		t.Fatalf("offer response = %s", body)
	}
	return number
}

func errorBody(code int, message string) string {
	return fmt.Sprintf(`{"code":%d,"message":"%s","details":[]}`, code, message)
}

func serveProviders(t *testing.T, product *fakeProduct, pricing *fakePricing) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpcgo.NewServer()
	productv1.RegisterProductServiceServer(server, product)
	pricingv1.RegisterPricingServiceServer(server, pricing)
	served := make(chan struct{})
	go func() {
		defer close(served)
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		<-served
	})
	return listener.Addr().String()
}

// providerCalls records one controlled provider's calls and trace context.
type providerCalls struct {
	mu        sync.Mutex
	err       error
	calls     []string
	traceText string
	start     chan struct{}
	end       chan struct{}
}

func (calls *providerCalls) reset(err error) {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	calls.err, calls.calls, calls.traceText = err, nil, ""
	calls.start, calls.end = make(chan struct{}), make(chan struct{})
}

// record returns the configured failure, blocking until cancellation for errBlock.
func (calls *providerCalls) record(ctx context.Context, entry string) error {
	calls.mu.Lock()
	calls.calls = append(calls.calls, entry)
	if incoming, ok := metadata.FromIncomingContext(ctx); ok {
		calls.traceText = strings.Join(incoming.Get("traceparent"), ",")
	}
	err, start, end := calls.err, calls.start, calls.end
	calls.mu.Unlock()
	if !errors.Is(err, errBlock) {
		return err
	}
	close(start)
	<-ctx.Done()
	close(end)
	return status.FromContextError(ctx.Err()).Err()
}

func (calls *providerCalls) order() []string {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	return append([]string(nil), calls.calls...)
}

func (calls *providerCalls) trace() string {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	return calls.traceText
}

func (calls *providerCalls) started() <-chan struct{} {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	return calls.start
}

func (calls *providerCalls) ended() <-chan struct{} {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	return calls.end
}

type fakeProduct struct {
	productv1.UnimplementedProductServiceServer
	providerCalls
}

func (product *fakeProduct) GetProduct(
	ctx context.Context,
	request *productv1.GetProductRequest,
) (*productv1.GetProductResponse, error) {
	if err := product.record(ctx, "product:"+request.GetCode()); err != nil {
		return nil, err
	}
	return &productv1.GetProductResponse{
		Product: &productv1.Product{Code: request.GetCode()},
	}, nil
}

type fakePricing struct {
	pricingv1.UnimplementedPricingServiceServer
	providerCalls
	response *pricingv1.CalculatePriceResponse
	request  *pricingv1.CalculatePriceRequest
}

func (pricing *fakePricing) reset(err error, response *pricingv1.CalculatePriceResponse) {
	pricing.providerCalls.reset(err)
	pricing.mu.Lock()
	defer pricing.mu.Unlock()
	pricing.response, pricing.request = response, nil
}

func (pricing *fakePricing) CalculatePrice(
	ctx context.Context,
	request *pricingv1.CalculatePriceRequest,
) (*pricingv1.CalculatePriceResponse, error) {
	pricing.mu.Lock()
	pricing.request = request
	response := pricing.response
	pricing.mu.Unlock()
	if err := pricing.record(ctx, "pricing:"+request.GetProductCode()); err != nil {
		return nil, err
	}
	return response, nil
}

func (pricing *fakePricing) lastRequest() *pricingv1.CalculatePriceRequest {
	pricing.mu.Lock()
	defer pricing.mu.Unlock()
	return pricing.request
}
