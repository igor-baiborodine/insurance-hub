// Package application owns Policy use cases, the ports they consume and their failures.
package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

var (
	// ErrProductNotFound reports that Product has no product with the submitted code.
	ErrProductNotFound = errors.New("product not found")
	// ErrQuoteRejected reports that Pricing rejected the quote input.
	ErrQuoteRejected = errors.New("quote input rejected")
	// ErrProviderUnavailable reports that Product or Pricing cannot currently serve the call.
	ErrProviderUnavailable = errors.New("provider unavailable")
	// ErrStorageUnavailable reports that offer storage could not be reached before any write.
	ErrStorageUnavailable = errors.New("offer storage unavailable")
	// ErrOffersUnavailable reports that this instance has no storage or providers for offers.
	ErrOffersUnavailable = errors.New("offer creation is not configured")
)

// ProductCatalog checks that a product exists.
type ProductCatalog interface {
	CheckProduct(ctx context.Context, code string) error
}

// QuoteCalculator prices an offer request.
type QuoteCalculator interface {
	CalculatePrice(ctx context.Context, request domain.OfferRequest) (domain.Quote, error)
}

// OfferStore commits an offer with its answers and cover prices together.
type OfferStore interface {
	CreateOffer(ctx context.Context, offer domain.Offer) error
}

// CreateOffer checks the product, prices the request and stores a NEW offer.
type CreateOffer struct {
	products ProductCatalog
	pricing  QuoteCalculator
	offers   OfferStore
	now      func() time.Time
	zone     *time.Location
}

// NewCreateOffer wires the use case. now and zone define the business date of a new offer.
func NewCreateOffer(
	products ProductCatalog,
	pricing QuoteCalculator,
	offers OfferStore,
	now func() time.Time,
	zone *time.Location,
) (*CreateOffer, error) {
	if products == nil || pricing == nil || offers == nil || now == nil || zone == nil {
		return nil, errors.New("create offer use case: all dependencies are required")
	}
	return &CreateOffer{
		products: products,
		pricing:  pricing,
		offers:   offers,
		now:      now,
		zone:     zone,
	}, nil
}

// Execute runs both provider calls before the write so that no transaction waits on a provider.
// It returns the offer only after it has been committed.
func (useCase *CreateOffer) Execute(
	ctx context.Context,
	request domain.OfferRequest,
) (domain.Offer, error) {
	businessTime := useCase.now().In(useCase.zone)
	if _, err := domain.AnswerValues(request.Answers); err != nil {
		return domain.Offer{}, err
	}
	if err := useCase.products.CheckProduct(ctx, request.ProductCode); err != nil {
		return domain.Offer{}, fmt.Errorf("create offer: %w", err)
	}
	quote, err := useCase.pricing.CalculatePrice(ctx, request)
	if err != nil {
		return domain.Offer{}, fmt.Errorf("create offer: %w", err)
	}
	number, err := newOfferNumber()
	if err != nil {
		return domain.Offer{}, err
	}
	offer, err := domain.NewOffer(number, request, quote, businessTime)
	if err != nil {
		return domain.Offer{}, fmt.Errorf("create offer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return domain.Offer{}, fmt.Errorf("create offer: %w", err)
	}
	if err := useCase.offers.CreateOffer(ctx, offer); err != nil {
		return domain.Offer{}, fmt.Errorf("create offer: %w", err)
	}
	return offer, nil
}

// newOfferNumber returns a random (version 4) UUID.
func newOfferNumber() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate offer number: %w", err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", value[0:4], value[4:6], value[6:8], value[8:10],
		value[10:16]), nil
}

// UnavailableOffers is the offer operation of an instance without storage or providers.
func UnavailableOffers(context.Context, domain.OfferRequest) (domain.Offer, error) {
	return domain.Offer{}, ErrOffersUnavailable
}
