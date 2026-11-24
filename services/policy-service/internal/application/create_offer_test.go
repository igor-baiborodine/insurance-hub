package application_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

var uuidV4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

func TestCreateOffer_Execute_ChecksProductPricesThenStores(t *testing.T) {
	// given
	calls := &callLog{}
	store := &fakeStore{calls: calls}
	useCase := newUseCase(t, calls, nil, nil, store, func() time.Time {
		return time.Date(2026, 11, 23, 23, 30, 0, 0, time.UTC)
	})
	request := triRequest()

	// when
	offer, err := useCase.Execute(t.Context(), request)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{
		"product:TRI",
		"pricing:TRI",
		"store",
	}; !slices.Equal(
		calls.entries,
		want,
	) {
		t.Errorf("calls = %v, want %v", calls.entries, want)
	}
	// 23:30 UTC is already 24 November in the UTC+1 business zone.
	if !uuidV4.MatchString(offer.Number) || offer.Status != "NEW" ||
		offer.CreationDate != time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC) ||
		offer.TotalPrice != "123.45" || offer.CoverPrices["C1"] != "100.10" {
		t.Errorf("offer = %+v", offer)
	}
	if store.offer.Number != offer.Number || store.offer.TotalPrice != offer.TotalPrice {
		t.Errorf("stored %+v, returned %+v", store.offer, offer)
	}
}

func TestCreateOffer_Execute_StopsBeforeLaterStepsOnFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		request    func() domain.OfferRequest
		productErr error
		pricingErr error
		quote      *domain.Quote
		want       error
		wantCalls  []string
	}{
		{
			name: "repeated question code",
			request: func() domain.OfferRequest {
				request := triRequest()
				request.Answers = append(request.Answers, request.Answers[0])
				return request
			},
			want: domain.ErrInvalidOffer,
		},
		{
			name: "unknown product", productErr: application.ErrProductNotFound,
			want: application.ErrProductNotFound, wantCalls: []string{"product:TRI"},
		},
		{
			name: "product unavailable", productErr: application.ErrProviderUnavailable,
			want: application.ErrProviderUnavailable, wantCalls: []string{"product:TRI"},
		},
		{
			name: "pricing rejects input", pricingErr: application.ErrQuoteRejected,
			want: application.ErrQuoteRejected, wantCalls: []string{"product:TRI", "pricing:TRI"},
		},
		{
			name: "pricing deadline", pricingErr: context.DeadlineExceeded,
			want: context.DeadlineExceeded, wantCalls: []string{"product:TRI", "pricing:TRI"},
		},
		{
			name: "quote not storable", quote: &domain.Quote{TotalPrice: "1.005"},
			want: domain.ErrInvalidQuote, wantCalls: []string{"product:TRI", "pricing:TRI"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			calls := &callLog{}
			useCase := newUseCase(t, calls, test.productErr, test.pricingErr,
				&fakeStore{calls: calls}, time.Now)
			if test.quote != nil {
				useCase = newUseCaseWithQuote(t, calls, *test.quote)
			}
			request := triRequest()
			if test.request != nil {
				request = test.request()
			}

			// when
			_, err := useCase.Execute(t.Context(), request)

			// then
			if !errors.Is(err, test.want) {
				t.Errorf("error = %v, want %v", err, test.want)
			}
			if !slices.Equal(calls.entries, test.wantCalls) {
				t.Errorf("calls = %v, want %v", calls.entries, test.wantCalls)
			}
		})
	}
}

func TestCreateOffer_Execute_DoesNotStoreAfterCancellation(t *testing.T) {
	// given
	calls := &callLog{}
	ctx, cancel := context.WithCancel(t.Context())
	pricing := &fakePricing{calls: calls, quote: fractionalQuote(), afterCall: cancel}
	store := &fakeStore{calls: calls}
	useCase, err := application.NewCreateOffer(&fakeProduct{calls: calls}, pricing, store,
		time.Now, time.UTC)
	if err != nil {
		t.Fatal(err)
	}

	// when
	_, err = useCase.Execute(ctx, triRequest())

	// then
	if !errors.Is(err, context.Canceled) || slices.Contains(calls.entries, "store") {
		t.Errorf("error = %v, calls = %v", err, calls.entries)
	}
}

func TestCreateOffer_Execute_ReturnsStoreFailure(t *testing.T) {
	// given
	calls := &callLog{}
	storeErr := errors.New("storage failed")
	useCase := newUseCase(t, calls, nil, nil, &fakeStore{calls: calls, err: storeErr}, time.Now)

	// when
	_, err := useCase.Execute(t.Context(), triRequest())

	// then
	if !errors.Is(err, storeErr) {
		t.Errorf("error = %v, want the store failure", err)
	}
}

func TestNewCreateOffer_RejectsMissingDependencies(t *testing.T) {
	// when
	_, err := application.NewCreateOffer(nil, nil, nil, nil, nil)

	// then
	if err == nil {
		t.Error("missing dependencies were accepted")
	}
}

func newUseCase(
	t *testing.T,
	calls *callLog,
	productErr, pricingErr error,
	store *fakeStore,
	now func() time.Time,
) *application.CreateOffer {
	t.Helper()
	useCase, err := application.NewCreateOffer(
		&fakeProduct{calls: calls, err: productErr},
		&fakePricing{calls: calls, err: pricingErr, quote: fractionalQuote()},
		store,
		now,
		time.FixedZone("UTC+1", 60*60),
	)
	if err != nil {
		t.Fatal(err)
	}
	return useCase
}

func newUseCaseWithQuote(
	t *testing.T,
	calls *callLog,
	quote domain.Quote,
) *application.CreateOffer {
	t.Helper()
	useCase, err := application.NewCreateOffer(
		&fakeProduct{calls: calls},
		&fakePricing{
			calls: calls,
			quote: quote,
		},
		&fakeStore{calls: calls},
		time.Now,
		time.UTC,
	)
	if err != nil {
		t.Fatal(err)
	}
	return useCase
}

func triRequest() domain.OfferRequest {
	return domain.OfferRequest{
		ProductCode:    "TRI",
		PolicyFrom:     time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC),
		PolicyTo:       time.Date(2018, 4, 15, 0, 0, 0, 0, time.UTC),
		SelectedCovers: []string{"C1", "C2"},
		Answers: []domain.Answer{
			{QuestionCode: "DESTINATION", Kind: domain.ChoiceAnswer, Value: "EUR"},
		},
	}
}

func fractionalQuote() domain.Quote {
	return domain.Quote{
		TotalPrice:  "123.45",
		CoverPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
	}
}

type callLog struct{ entries []string }

type fakeProduct struct {
	calls *callLog
	err   error
}

func (product *fakeProduct) CheckProduct(_ context.Context, code string) error {
	product.calls.entries = append(product.calls.entries, "product:"+code)
	return product.err
}

type fakePricing struct {
	calls     *callLog
	err       error
	quote     domain.Quote
	afterCall func()
}

func (pricing *fakePricing) CalculatePrice(
	_ context.Context,
	request domain.OfferRequest,
) (domain.Quote, error) {
	pricing.calls.entries = append(pricing.calls.entries, "pricing:"+request.ProductCode)
	if pricing.afterCall != nil {
		pricing.afterCall()
	}
	return pricing.quote, pricing.err
}

type fakeStore struct {
	calls *callLog
	err   error
	offer domain.Offer
}

func (store *fakeStore) CreateOffer(_ context.Context, offer domain.Offer) error {
	store.calls.entries = append(store.calls.entries, "store")
	store.offer = offer
	return store.err
}
