package providers_test

import (
	"context"
	"errors"
	"maps"
	"net"
	"sync"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/pricing/v1"
	productv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/providers"
)

func TestPricingCalculator_CalculatePrice_MapsAnswersAndExactAmounts(t *testing.T) {
	// given
	pricing := &fakePricing{response: &pricingv1.CalculatePriceResponse{
		TotalPrice:   "123.45",
		CoversPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
	}}
	connection := startProviders(t, &fakeProduct{}, pricing)
	calculator, err := providers.NewPricingCalculator(connection, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	request := domain.OfferRequest{
		ProductCode:    "TRI",
		PolicyFrom:     time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC),
		PolicyTo:       time.Date(2018, 4, 15, 0, 0, 0, 0, time.UTC),
		SelectedCovers: []string{"C1", "C2"},
		Answers: []domain.Answer{
			{QuestionCode: "DESTINATION", Kind: domain.ChoiceAnswer, Value: "EUR"},
			{QuestionCode: "NOTE", Kind: domain.TextAnswer, Value: "first trip"},
			{QuestionCode: "NUM_OF_ADULTS", Kind: domain.NumericAnswer, Value: "1.50"},
		},
	}
	wantRPC := &pricingv1.CalculatePriceRequest{
		ProductCode:    "TRI",
		PolicyFrom:     "2017-04-16",
		PolicyTo:       "2018-04-15",
		SelectedCovers: []string{"C1", "C2"},
		Answers: []*pricingv1.Answer{
			{QuestionCode: "DESTINATION", Value: &pricingv1.Answer_Text{Text: "EUR"}},
			{QuestionCode: "NOTE", Value: &pricingv1.Answer_Text{Text: "first trip"}},
			{
				QuestionCode: "NUM_OF_ADULTS",
				Value:        &pricingv1.Answer_Number{Number: "1.50"},
			},
		},
	}

	// when
	quote, err := calculator.CalculatePrice(t.Context(), request)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got := pricing.lastRequest(); !proto.Equal(got, wantRPC) {
		t.Errorf("RPC request = %v, want %v", got, wantRPC)
	}
	if quote.TotalPrice != "123.45" ||
		!maps.Equal(quote.CoverPrices, map[string]string{"C1": "100.10", "C2": "23.35"}) {
		t.Errorf("quote = %+v", quote)
	}
}

func TestProductCatalog_CheckProduct_MapsStatuses(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"existing product", nil, nil},
		{"missing product", status.Error(codes.NotFound, "product not found"), application.ErrProductNotFound},
		{"unavailable", status.Error(codes.Unavailable, "unavailable"), application.ErrProviderUnavailable},
		{"provider deadline", status.Error(codes.DeadlineExceeded, "deadline"), context.DeadlineExceeded},
		{"invalid argument", status.Error(codes.InvalidArgument, "invalid"), nil},
		{"internal", status.Error(codes.Internal, "internal"), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			product := &fakeProduct{err: test.err}
			catalog, err := providers.NewProductCatalog(
				startProviders(t, product, &fakePricing{}),
				time.Second,
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			err = catalog.CheckProduct(t.Context(), "TRI")

			// then
			requireMappedError(t, err, test.err, test.want)
			if product.lastCode() != "TRI" {
				t.Errorf("GetProduct code = %q, want TRI", product.lastCode())
			}
		})
	}
}

func TestPricingCalculator_CalculatePrice_MapsStatuses(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want error
	}{
		{"rejected input", status.Error(codes.InvalidArgument, "invalid"), application.ErrQuoteRejected},
		{"unavailable", status.Error(codes.Unavailable, "unavailable"), application.ErrProviderUnavailable},
		{"provider deadline", status.Error(codes.DeadlineExceeded, "deadline"), context.DeadlineExceeded},
		{"not found", status.Error(codes.NotFound, "missing"), nil},
		{"internal", status.Error(codes.Internal, "internal"), nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			calculator, err := providers.NewPricingCalculator(
				startProviders(t, &fakeProduct{}, &fakePricing{err: test.err}),
				time.Second,
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			_, err = calculator.CalculatePrice(t.Context(), domain.OfferRequest{})

			// then
			requireMappedError(t, err, test.err, test.want)
		})
	}
}

func TestProductCatalog_CheckProduct_BoundsCallByTimeout(t *testing.T) {
	// given
	product := &fakeProduct{block: true}
	catalog, err := providers.NewProductCatalog(
		startProviders(t, product, &fakePricing{}),
		50*time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()

	// when
	err = catalog.CheckProduct(t.Context(), "TRI")

	// then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CheckProduct error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Errorf("call took %v, want it bounded by the 50ms timeout", elapsed)
	}
	product.requireCallEnded(t)
}

func TestPricingCalculator_CalculatePrice_PropagatesCallerCancellation(t *testing.T) {
	// given
	pricing := &fakePricing{block: true, started: make(chan struct{})}
	calculator, err := providers.NewPricingCalculator(
		startProviders(t, &fakeProduct{}, pricing),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	go func() {
		<-pricing.started
		cancel()
	}()

	// when
	_, err = calculator.CalculatePrice(ctx, domain.OfferRequest{})

	// then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("CalculatePrice error = %v, want canceled", err)
	}
	pricing.requireCallEnded(t)
}

func TestNewProviders_RejectMissingDependencies(t *testing.T) {
	// when
	_, productErr := providers.NewProductCatalog(nil, time.Second)
	_, pricingErr := providers.NewPricingCalculator(&grpcgo.ClientConn{}, 0)

	// then
	if productErr == nil || pricingErr == nil {
		t.Errorf(
			"constructors accepted missing dependencies: %v, %v",
			productErr,
			pricingErr,
		)
	}
}

func requireMappedError(t *testing.T, got, statusErr, want error) {
	t.Helper()
	if statusErr == nil {
		if got != nil {
			t.Fatalf("error = %v, want nil", got)
		}
		return
	}
	if got == nil {
		t.Fatal("error = nil, want a mapped failure")
	}
	for _, sentinel := range []error{
		application.ErrProductNotFound, application.ErrQuoteRejected,
		application.ErrProviderUnavailable, context.DeadlineExceeded, context.Canceled,
	} {
		if matched := errors.Is(got, sentinel); matched != (sentinel == want) {
			t.Errorf("errors.Is(%v, %v) = %t", got, sentinel, matched)
		}
	}
	if status.Code(got) != status.Code(statusErr) {
		t.Errorf("retained status = %s, want %s", status.Code(got), status.Code(statusErr))
	}
}

// startProviders serves both fakes on one loopback listener and returns a client connection.
func startProviders(
	t *testing.T,
	product *fakeProduct,
	pricing *fakePricing,
) *grpcgo.ClientConn {
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
	connection, err := grpcgo.NewClient(
		listener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		server.Stop()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = connection.Close()
		server.Stop()
		<-served
	})
	return connection
}

type fakeProduct struct {
	productv1.UnimplementedProductServiceServer
	mu    sync.Mutex
	code  string
	err   error
	block bool
	ended chan struct{}
}

func (product *fakeProduct) GetProduct(
	ctx context.Context,
	request *productv1.GetProductRequest,
) (*productv1.GetProductResponse, error) {
	product.mu.Lock()
	product.code = request.GetCode()
	if product.block {
		product.ended = make(chan struct{})
	}
	ended, block, err := product.ended, product.block, product.err
	product.mu.Unlock()
	if block {
		<-ctx.Done()
		close(ended)
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if err != nil {
		return nil, err
	}
	return &productv1.GetProductResponse{
		Product: &productv1.Product{Code: request.GetCode()},
	}, nil
}

func (product *fakeProduct) lastCode() string {
	product.mu.Lock()
	defer product.mu.Unlock()
	return product.code
}

func (product *fakeProduct) requireCallEnded(t *testing.T) {
	t.Helper()
	product.mu.Lock()
	ended := product.ended
	product.mu.Unlock()
	requireClosed(t, ended, "Product call did not observe the canceled context")
}

type fakePricing struct {
	pricingv1.UnimplementedPricingServiceServer
	mu       sync.Mutex
	request  *pricingv1.CalculatePriceRequest
	response *pricingv1.CalculatePriceResponse
	err      error
	block    bool
	started  chan struct{}
	ended    chan struct{}
}

func (pricing *fakePricing) CalculatePrice(
	ctx context.Context,
	request *pricingv1.CalculatePriceRequest,
) (*pricingv1.CalculatePriceResponse, error) {
	pricing.mu.Lock()
	pricing.request = request
	if pricing.block {
		pricing.ended = make(chan struct{})
	}
	ended, block, err, response := pricing.ended, pricing.block, pricing.err, pricing.response
	pricing.mu.Unlock()
	if block {
		close(pricing.started)
		<-ctx.Done()
		close(ended)
		return nil, status.FromContextError(ctx.Err()).Err()
	}
	if err != nil {
		return nil, err
	}
	return response, nil
}

func (pricing *fakePricing) lastRequest() *pricingv1.CalculatePriceRequest {
	pricing.mu.Lock()
	defer pricing.mu.Unlock()
	return pricing.request
}

func (pricing *fakePricing) requireCallEnded(t *testing.T) {
	t.Helper()
	pricing.mu.Lock()
	ended := pricing.ended
	pricing.mu.Unlock()
	requireClosed(t, ended, "Pricing call did not observe the canceled context")
}

func requireClosed(t *testing.T, signal <-chan struct{}, failure string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(failure)
	}
}
