// Package providers adapts the private Product and Pricing gRPC contracts to Policy use cases.
package providers

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pricingv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/pricing/v1"
	productv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

// CallObserver records the outcome and duration of one provider call.
type CallObserver interface {
	ObserveCall(
		ctx context.Context,
		dependency string,
		operation string,
		elapsed time.Duration,
		outcome string,
	)
}

// ProductCatalog checks product existence through product.v1.ProductService/GetProduct.
type ProductCatalog struct {
	client   productv1.ProductServiceClient
	timeout  time.Duration
	observer CallObserver
}

// NewProductCatalog uses a borrowed connection; the caller owns closing it.
func NewProductCatalog(
	connection grpc.ClientConnInterface,
	timeout time.Duration,
	observer CallObserver,
) (*ProductCatalog, error) {
	if connection == nil || timeout <= 0 || observer == nil {
		return nil, errors.New(
			"create Product catalog: connection, timeout and observer are required",
		)
	}
	return &ProductCatalog{
		client:   productv1.NewProductServiceClient(connection),
		timeout:  timeout,
		observer: observer,
	}, nil
}

// CheckProduct returns nil when the product exists. The call is bounded by the shorter of the
// caller's deadline and the configured call timeout.
func (catalog *ProductCatalog) CheckProduct(ctx context.Context, code string) error {
	callCtx, cancel := context.WithTimeout(ctx, catalog.timeout)
	defer cancel()
	started := time.Now()
	_, err := catalog.client.GetProduct(callCtx, &productv1.GetProductRequest{Code: code})
	catalog.observer.ObserveCall(
		ctx,
		"product",
		"get_product",
		time.Since(started),
		callOutcome(err, codes.NotFound),
	)
	return providerError("get product", err, codes.NotFound, application.ErrProductNotFound)
}

// PricingCalculator obtains quotes through pricing.v1.PricingService/CalculatePrice.
type PricingCalculator struct {
	client   pricingv1.PricingServiceClient
	timeout  time.Duration
	observer CallObserver
}

// NewPricingCalculator uses a borrowed connection; the caller owns closing it.
func NewPricingCalculator(
	connection grpc.ClientConnInterface,
	timeout time.Duration,
	observer CallObserver,
) (*PricingCalculator, error) {
	if connection == nil || timeout <= 0 || observer == nil {
		return nil, errors.New(
			"create Pricing calculator: connection, timeout and observer are required",
		)
	}
	return &PricingCalculator{
		client:   pricingv1.NewPricingServiceClient(connection),
		timeout:  timeout,
		observer: observer,
	}, nil
}

// CalculatePrice sends choice and text answers as RPC text and numeric answers as exact decimal
// text, and returns Pricing's exact amounts unchanged.
func (calculator *PricingCalculator) CalculatePrice(
	ctx context.Context,
	request domain.OfferRequest,
) (domain.Quote, error) {
	answers := make([]*pricingv1.Answer, 0, len(request.Answers))
	for _, answer := range request.Answers {
		mapped := &pricingv1.Answer{QuestionCode: answer.QuestionCode}
		switch answer.Kind {
		case domain.ChoiceAnswer, domain.TextAnswer:
			mapped.Value = &pricingv1.Answer_Text{Text: answer.Value}
		case domain.NumericAnswer:
			mapped.Value = &pricingv1.Answer_Number{Number: answer.Value}
		default:
			return domain.Quote{}, errors.New("calculate price: unknown answer kind")
		}
		answers = append(answers, mapped)
	}
	callCtx, cancel := context.WithTimeout(ctx, calculator.timeout)
	defer cancel()
	started := time.Now()
	response, err := calculator.client.CalculatePrice(callCtx, &pricingv1.CalculatePriceRequest{
		ProductCode:    request.ProductCode,
		PolicyFrom:     request.PolicyFrom.Format(time.DateOnly),
		PolicyTo:       request.PolicyTo.Format(time.DateOnly),
		SelectedCovers: append([]string(nil), request.SelectedCovers...),
		Answers:        answers,
	})
	calculator.observer.ObserveCall(
		ctx, "pricing", "calculate_price", time.Since(started),
		callOutcome(err, codes.InvalidArgument),
	)
	if err := providerError(
		"calculate price", err, codes.InvalidArgument, application.ErrQuoteRejected,
	); err != nil {
		return domain.Quote{}, err
	}
	return domain.Quote{
		TotalPrice:  response.GetTotalPrice(),
		CoverPrices: maps.Clone(response.GetCoversPrices()),
	}, nil
}

// callOutcome classifies a provider status like providerError: only the provider's expected
// business status is invalid_request, and every unexpected status is a provider error.
func callOutcome(err error, expected codes.Code) string {
	switch code := status.Code(err); code {
	case codes.OK:
		return "success"
	case codes.Canceled:
		return "cancelled"
	case codes.DeadlineExceeded:
		return "deadline_exceeded"
	case expected:
		return "invalid_request"
	default:
		return "error"
	}
}

// providerError translates a provider status into a failure meaningful to the use case while
// keeping the status inspectable. Context failures keep their standard sentinels.
func providerError(operation string, err error, expected codes.Code, mapped error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch code := status.Code(err); code {
	case codes.Canceled:
		sentinel = context.Canceled
	case codes.DeadlineExceeded:
		sentinel = context.DeadlineExceeded
	case codes.Unavailable:
		sentinel = application.ErrProviderUnavailable
	case expected:
		sentinel = mapped
	default:
		return fmt.Errorf("%s: unexpected provider status %s: %w", operation, code, err)
	}
	return fmt.Errorf("%s: %w: %w", operation, sentinel, err)
}
