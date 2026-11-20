package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	kitconfig "github.com/igor-baiborodine/insurance-hub/libs/servicekit/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/pricing-service/gen/pricing/v1"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/domain"
)

const triHTTPBody = `{"productCode":"TRI","policyFrom":"2017-04-16","policyTo":"2018-04-15","selectedCovers":["C1","C2"],"answers":[{"questionCode":"NUM_OF_ADULTS","answer":1},{"questionCode":"NUM_OF_CHILDREN","answer":1},{"questionCode":"DESTINATION","answer":"EUR"}]}`

func TestRun_RejectsDeploymentWithoutReaderBeforeOpeningListeners(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.Identity.Environment = "qa"
	deps := productionDependencies()
	deps.listen = func(string, string) (net.Listener, error) {
		t.Fatal("service opened a listener without reader credentials")
		return nil, nil
	}

	// when
	err := run(t.Context(), settings, discardLogger(), deps)

	// then
	if err == nil || !strings.Contains(err.Error(), "PRICING_DATABASE_URL is required") {
		t.Fatalf("expected missing reader refusal, got %v", err)
	}
}

func TestRun_ProductionCompositionServesCompatibleQuotesAndSafeFailures(t *testing.T) {
	// given
	settings := testConfig(t)
	reader := newFixtureReader(t)
	deps := productionDependencies()
	deps.newReader = func(_ *pgxpool.Pool, _, _ time.Duration) (application.TariffReader, error) {
		return reader, nil
	}
	addresses := make(chan string, 3)
	deps.listen = recordingListen(addresses)
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- run(ctx, settings, discardLogger(), deps) }()
	grpcAddress := receiveString(t, addresses)
	httpAddress := receiveString(t, addresses)
	healthAddress := receiveString(t, addresses)
	httpClient := &http.Client{Timeout: time.Second}
	waitForHTTPStatus(t, httpClient, "http://"+healthAddress+"/readyz", http.StatusOK)
	waitForHTTPStatus(t, httpClient, "http://"+healthAddress+"/livez", http.StatusOK)
	connection, err := grpc.NewClient(
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	rpcClient := pricingv1.NewPricingServiceClient(connection)

	// when
	httpResponse := postPrice(t, httpClient, httpAddress, triHTTPBody)
	rpcResponse, rpcErr := rpcClient.CalculatePrice(context.Background(), triRPCRequest())

	// then
	assertResponse(t, httpResponse, 200, `{"totalPrice":78,"coversPrices":{"C1":26,"C2":52}}`)
	if rpcErr != nil || rpcResponse.GetTotalPrice() != "78" ||
		rpcResponse.GetCoversPrices()["C1"] != "26" || rpcResponse.GetCoversPrices()["C2"] != "52" {
		t.Fatalf("RPC TRI = (%v, %v)", rpcResponse, rpcErr)
	}

	// when
	hsiHTTP := postPrice(t, httpClient, httpAddress, hsiHTTPBody())
	hsiRPC, hsiErr := rpcClient.CalculatePrice(context.Background(), hsiRPCRequest())

	// then
	assertResponse(
		t,
		hsiHTTP,
		200,
		`{"totalPrice":172.50,"coversPrices":{"C1":118.75,"C2":23.75,"C3":30}}`,
	)
	if hsiErr != nil || hsiRPC.GetTotalPrice() != "172.50" ||
		hsiRPC.GetCoversPrices()["C1"] != "118.75" || hsiRPC.GetCoversPrices()["C2"] != "23.75" ||
		hsiRPC.GetCoversPrices()["C3"] != "30" {
		t.Fatalf("RPC HSI = (%v, %v)", hsiRPC, hsiErr)
	}

	failures := []struct {
		name, body string
		request    *pricingv1.CalculatePriceRequest
		httpCode   int
		rpcCode    codes.Code
		message    string
	}{
		{
			name: "invalid date",
			body: strings.Replace(triHTTPBody, "2017-04-16", "invalid", 1),
			request: func() *pricingv1.CalculatePriceRequest {
				value := triRPCRequest()
				value.PolicyFrom = "invalid"
				return value
			}(),
			httpCode: 400,
			rpcCode:  codes.InvalidArgument,
			message:  "invalid request",
		},
		{
			name: "duplicate answer",
			body: strings.Replace(
				triHTTPBody,
				`{"questionCode":"NUM_OF_CHILDREN","answer":1}`,
				`{"questionCode":"NUM_OF_ADULTS","answer":1}`,
				1,
			),
			request: func() *pricingv1.CalculatePriceRequest {
				value := triRPCRequest()
				value.Answers[1].QuestionCode = "NUM_OF_ADULTS"
				return value
			}(),
			httpCode: 500,
			rpcCode:  codes.Internal,
			message:  "internal error",
		},
		{
			name: "missing referenced answer",
			body: strings.Replace(triHTTPBody, `"NUM_OF_ADULTS"`, `"OTHER"`, 1),
			request: func() *pricingv1.CalculatePriceRequest {
				value := triRPCRequest()
				value.Answers[0].QuestionCode = "OTHER"
				return value
			}(),
			httpCode: 500,
			rpcCode:  codes.Internal,
			message:  "internal error",
		},
		{
			name: "numeric-looking text",
			body: strings.Replace(triHTTPBody, `"answer":1}`, `"answer":"1"}`, 1),
			request: func() *pricingv1.CalculatePriceRequest {
				value := triRPCRequest()
				value.Answers[0].Value = &pricingv1.Answer_Text{Text: "1"}
				return value
			}(),
			httpCode: 500,
			rpcCode:  codes.Internal,
			message:  "internal error",
		},
		{
			name: "unknown tariff",
			body: strings.Replace(triHTTPBody, `"TRI"`, `"UNKNOWN"`, 1),
			request: func() *pricingv1.CalculatePriceRequest {
				value := triRPCRequest()
				value.ProductCode = "UNKNOWN"
				return value
			}(),
			httpCode: 500,
			rpcCode:  codes.Internal,
			message:  "internal error",
		},
		{
			name: "unknown cover prevents partial quote",
			body: strings.Replace(triHTTPBody, `"C2"`, `"UNKNOWN"`, 1),
			request: func() *pricingv1.CalculatePriceRequest {
				value := triRPCRequest()
				value.SelectedCovers[1] = "UNKNOWN"
				return value
			}(),
			httpCode: 500,
			rpcCode:  codes.Internal,
			message:  "internal error",
		},
	}
	for _, test := range failures {
		t.Run(test.name, func(t *testing.T) {
			// when
			httpResult := postPrice(t, httpClient, httpAddress, test.body)
			rpcResult, err := rpcClient.CalculatePrice(
				context.Background(),
				test.request,
			)

			// then
			assertResponse(
				t,
				httpResult,
				test.httpCode,
				errorBody(int(test.rpcCode), test.message),
			)
			if rpcResult != nil || status.Code(err) != test.rpcCode ||
				status.Convert(err).Message() != test.message {
				t.Fatalf("RPC result = (%v, %v)", rpcResult, err)
			}
		})
	}

	// when
	reader.unavailable.Store(true)
	unavailableHTTP := postPrice(t, httpClient, httpAddress, triHTTPBody)
	unavailableRPC, unavailableErr := rpcClient.CalculatePrice(
		context.Background(),
		triRPCRequest(),
	)
	reader.unavailable.Store(false)

	// then
	assertResponse(t, unavailableHTTP, 503, errorBody(14, "service unavailable"))
	if unavailableRPC != nil || status.Code(unavailableErr) != codes.Unavailable ||
		status.Convert(unavailableErr).Message() != "service unavailable" {
		t.Fatalf("unavailable RPC = (%v, %v)", unavailableRPC, unavailableErr)
	}

	// when
	cancel()

	// then
	if err := receiveError(t, runResult, "service did not stop"); err != nil {
		t.Fatalf("run returned %v", err)
	}
}

func TestRun_UnavailableRPCDependencyNeverBecomesReady(t *testing.T) {
	// given
	settings := testConfig(t)
	settings.ProbeTimeout = 30 * time.Millisecond
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := closed.Addr().String()
	_ = closed.Close()
	deps := productionDependencies()
	deps.newReader = func(_ *pgxpool.Pool, _, _ time.Duration) (application.TariffReader, error) {
		return newFixtureReader(t), nil
	}
	deps.newConnection = func(_ string, provider telemetryProvider, settings config.Config) (gatewayConnection, error) {
		return newGatewayConnection(unavailable, provider, settings)
	}

	// when
	err = run(context.Background(), settings, discardLogger(), deps)

	// then
	if err == nil || !strings.Contains(err.Error(), "dependency not ready") {
		t.Fatalf("run error = %v", err)
	}
}

func TestRun_PartialBindFailureClosesEarlierListeners(t *testing.T) {
	// given
	settings := testConfig(t)
	deps := productionDependencies()
	deps.newReader = func(_ *pgxpool.Pool, _, _ time.Duration) (application.TariffReader, error) {
		return newFixtureReader(t), nil
	}
	bindFailure := errors.New("management bind failed")
	var listeners []net.Listener
	deps.listen = func(network, address string) (net.Listener, error) {
		if len(listeners) == 2 {
			return nil, bindFailure
		}
		listener, listenErr := net.Listen(network, address)
		if listenErr == nil {
			listeners = append(listeners, listener)
		}
		return listener, listenErr
	}

	// when
	err := run(context.Background(), settings, discardLogger(), deps)

	// then
	if !errors.Is(err, bindFailure) {
		t.Fatalf("run error = %v", err)
	}
	for _, listener := range listeners {
		if _, acceptErr := listener.Accept(); !errors.Is(acceptErr, net.ErrClosed) {
			t.Errorf("listener remained open: %v", acceptErr)
		}
	}
}

func TestHandlerTracker_UsesUnavailableHandlerAfterDrain(t *testing.T) {
	// given
	active := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	unavailable := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	})
	tracker := newHandlerTracker(active, unavailable)

	// when / then
	first := httptest.NewRecorder()
	tracker.ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/", nil))
	if first.Code != http.StatusNoContent {
		t.Errorf("active status = %d", first.Code)
	}
	tracker.beginDrain()
	second := httptest.NewRecorder()
	tracker.ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/", nil))
	if second.Code != http.StatusServiceUnavailable {
		t.Errorf("drained status = %d", second.Code)
	}
}

func TestInternalDialTarget_NormalizesWildcardFamilies(t *testing.T) {
	// given
	for _, test := range []struct{ input, want string }{
		{"0.0.0.0:1234", "127.0.0.1:1234"},
		{"[::]:1234", "[::1]:1234"},
		{"127.0.0.2:1234", "127.0.0.2:1234"},
	} {
		// when
		got, err := internalDialTarget(testAddress(test.input))

		// then
		if err != nil || got != test.want {
			t.Errorf(
				"internalDialTarget(%q) = (%q, %v), want %q",
				test.input,
				got,
				err,
				test.want,
			)
		}
	}
}

type fixtureReader struct {
	tariffs     map[string]domain.Tariff
	unavailable atomic.Bool
}

// newFixtureReader serves the committed seed tariffs so quotes exercise the stored rule text.
func newFixtureReader(t *testing.T) *fixtureReader {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "administration", "seeds", "base.json"))
	if err != nil {
		t.Fatal(err)
	}
	var seeds struct {
		Tariffs []struct {
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
		} `json:"tariffs"`
	}
	if err := json.Unmarshal(data, &seeds); err != nil {
		t.Fatal(err)
	}
	tariffs := make(map[string]domain.Tariff, len(seeds.Tariffs))
	for _, seed := range seeds.Tariffs {
		tariff := domain.Tariff{Code: seed.Code}
		for _, rule := range seed.Base {
			tariff.BaseRules = append(tariff.BaseRules, domain.BaseRule{
				CoverCode: rule.Cover, Condition: rule.Condition, Formula: rule.Formula,
			})
		}
		for _, rule := range seed.Markup {
			factor, err := domain.NewDecimal(rule.Factor)
			if err != nil {
				t.Fatal(err)
			}
			tariff.MarkupRules = append(tariff.MarkupRules, domain.MarkupRule{
				Kind: rule.Kind, Condition: rule.Condition, Factor: factor,
			})
		}
		tariffs[seed.Code] = tariff
	}
	return &fixtureReader{tariffs: tariffs}
}

func (reader *fixtureReader) GetTariff(
	_ context.Context,
	code string,
) (domain.Tariff, bool, error) {
	if reader.unavailable.Load() {
		return domain.Tariff{}, false, application.ErrReaderUnavailable
	}
	tariff, found := reader.tariffs[code]
	return tariff, found, nil
}

func triRPCRequest() *pricingv1.CalculatePriceRequest {
	return &pricingv1.CalculatePriceRequest{
		ProductCode: "TRI", PolicyFrom: "2017-04-16", PolicyTo: "2018-04-15",
		SelectedCovers: []string{"C1", "C2"},
		Answers: []*pricingv1.Answer{
			{
				QuestionCode: "NUM_OF_ADULTS",
				Value:        &pricingv1.Answer_Number{Number: "1"},
			},
			{
				QuestionCode: "NUM_OF_CHILDREN",
				Value:        &pricingv1.Answer_Number{Number: "1"},
			},
			{QuestionCode: "DESTINATION", Value: &pricingv1.Answer_Text{Text: "EUR"}},
		},
	}
}

func hsiRPCRequest() *pricingv1.CalculatePriceRequest {
	return &pricingv1.CalculatePriceRequest{
		ProductCode: "HSI", PolicyFrom: "2017-04-16", PolicyTo: "2018-04-15",
		SelectedCovers: []string{"C1", "C2", "C3"},
		Answers: []*pricingv1.Answer{
			{QuestionCode: "TYP", Value: &pricingv1.Answer_Text{Text: "APT"}},
			{QuestionCode: "AREA", Value: &pricingv1.Answer_Number{Number: "95"}},
			{
				QuestionCode: "NUM_OF_CLAIM",
				Value:        &pricingv1.Answer_Number{Number: "1"},
			},
			{QuestionCode: "FLOOD", Value: &pricingv1.Answer_Text{Text: "NO"}},
		},
	}
}

func hsiHTTPBody() string {
	return `{"productCode":"HSI","policyFrom":"2017-04-16","policyTo":"2018-04-15","selectedCovers":["C1","C2","C3"],"answers":[{"questionCode":"TYP","answer":"APT"},{"questionCode":"AREA","answer":95},{"questionCode":"NUM_OF_CLAIM","answer":1},{"questionCode":"FLOOD","answer":"NO"}]}`
}

func recordingListen(addresses chan<- string) func(string, string) (net.Listener, error) {
	return func(network, address string) (net.Listener, error) {
		listener, err := net.Listen(network, address)
		if err == nil {
			addresses <- listener.Addr().String()
		}
		return listener, err
	}
}

func testConfig(t *testing.T) config.Config {
	t.Helper()
	parse := func(name string) config.Address {
		address, err := kitconfig.ParseAddress(name, "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		return address
	}
	return config.Config{
		StartupTimeout: 3 * time.Second,
		Runtime: kitconfig.Runtime{
			RequestTimeout:  500 * time.Millisecond,
			ProbeTimeout:    500 * time.Millisecond,
			ShutdownTimeout: 2 * time.Second,
		},
		GRPC: kitconfig.GRPC{MaxReceiveBytes: 1048576, MaxSendBytes: 8388608},
		HTTPServer: kitconfig.HTTPServer{
			ReadHeaderTimeout: time.Second,
			ReadTimeout:       time.Second,
			WriteTimeout:      time.Second,
			IdleTimeout:       time.Second,
		},
		Telemetry:   config.Telemetry{ExporterTimeout: 200 * time.Millisecond},
		ServiceName: "pricing-service",
		HTTPAddress: parse(kitconfig.EnvHTTPAddress),
		GRPCAddress: parse(
			kitconfig.EnvGRPCAddress,
		),
		HealthAddress:      parse(kitconfig.EnvHealthAddress),
		HTTPMaxHeaderBytes: 1024,
		HTTPMaxBodyBytes:   4096,
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func postPrice(t *testing.T, client *http.Client, address, body string) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+address+"/pricing/calculate",
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func assertResponse(t *testing.T, response *http.Response, wantStatus int, wantBody string) {
	t.Helper()
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != wantStatus || string(data) != wantBody {
		t.Errorf(
			"response = (%d, %q), want (%d, %q)",
			response.StatusCode,
			data,
			wantStatus,
			wantBody,
		)
	}
}

func errorBody(code int, message string) string {
	return fmt.Sprintf(`{"code":%d,"message":"%s","details":[]}`, code, message)
}

func waitForHTTPStatus(t *testing.T, client *http.Client, target string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(target)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			if response.StatusCode == want {
				return
			}
		}
	}
	t.Fatalf("%s did not return %d", target, want)
}

func receiveString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(time.Second):
		t.Fatal("listener address was not reported")
		return ""
	}
}

func receiveError(t *testing.T, result <-chan error, failure string) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(3 * time.Second):
		t.Fatal(failure)
		return nil
	}
}

type testAddress string

func (testAddress) Network() string        { return "tcp" }
func (address testAddress) String() string { return string(address) }
