package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/grpc"
)

func TestPolicyService_CreateOffer_MapsRequestAndExactResponse(t *testing.T) {
	// given
	recorder := &offerRecorder{offer: domain.Offer{
		Number:      "5f3d2a1b-7c6e-4d8f-9a0b-1c2d3e4f5a66",
		TotalPrice:  "123.45",
		CoverPrices: map[string]string{"C1": "100.10", "C2": "23.35"},
	}}
	client := newPolicyClient(t, recorder.createOffer, new(bytes.Buffer))
	want := domain.OfferRequest{
		ProductCode:    "TRI",
		PolicyFrom:     time.Date(2017, 4, 16, 0, 0, 0, 0, time.UTC),
		PolicyTo:       time.Date(2018, 4, 15, 0, 0, 0, 0, time.UTC),
		SelectedCovers: []string{"C1", "C2"},
		Answers: []domain.Answer{
			{QuestionCode: "DESTINATION", Kind: domain.ChoiceAnswer, Value: "EUR"},
			{QuestionCode: "NOTE", Kind: domain.TextAnswer, Value: "first trip"},
			{QuestionCode: "NUM_OF_ADULTS", Kind: domain.NumericAnswer, Value: "1"},
		},
	}

	// when
	response, err := client.CreateOffer(t.Context(), validOfferRequest())
	// then
	if err != nil {
		t.Fatal(err)
	}
	if got := recorder.request(); !reflect.DeepEqual(got, want) {
		t.Errorf("use case request = %+v, want %+v", got, want)
	}
	if response.GetOfferNumber() != "5f3d2a1b-7c6e-4d8f-9a0b-1c2d3e4f5a66" ||
		response.GetTotalPrice() != "123.45" ||
		!reflect.DeepEqual(
			response.GetCoversPrices(),
			map[string]string{"C1": "100.10", "C2": "23.35"},
		) {
		t.Errorf("response = %v", response)
	}
}

func TestPolicyService_CreateOffer_RejectsInvalidInputBeforeUseCase(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*policyv1.CreateOfferRequest)
	}{
		{"empty product", func(request *policyv1.CreateOfferRequest) { request.ProductCode = "" }},
		{"impossible date", func(request *policyv1.CreateOfferRequest) { request.PolicyFrom = "2017-02-30" }},
		{"malformed date", func(request *policyv1.CreateOfferRequest) { request.PolicyTo = "15.04.2018" }},
		{"no covers", func(request *policyv1.CreateOfferRequest) { request.SelectedCovers = nil }},
		{"answer without value", func(request *policyv1.CreateOfferRequest) {
			request.Answers[0].Value = nil
		}},
		{"exponent number", func(request *policyv1.CreateOfferRequest) {
			request.Answers[2].Value = &policyv1.OfferAnswer_Number{Number: "1e2"}
		}},
		{"answer longer than storage", func(request *policyv1.CreateOfferRequest) {
			request.Answers[1].Value = &policyv1.OfferAnswer_Text{Text: strings.Repeat("é", 256)}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			recorder := &offerRecorder{}
			client := newPolicyClient(t, recorder.createOffer, new(bytes.Buffer))
			request := validOfferRequest()
			test.mutate(request)

			// when
			_, err := client.CreateOffer(t.Context(), request)

			// then
			if status.Code(err) != codes.InvalidArgument || recorder.calls() != 0 {
				t.Errorf("status = %v, use case calls = %d", err, recorder.calls())
			}
		})
	}
}

func TestPolicyService_CreateOffer_MapsFailuresToSafeStatuses(t *testing.T) {
	privateMarker := "private-offer-marker"
	for _, test := range []struct {
		name    string
		err     error
		code    codes.Code
		message string
	}{
		{"repeated answer", domain.ErrInvalidOffer, codes.InvalidArgument, "invalid request"},
		{"quote rejected", application.ErrQuoteRejected, codes.InvalidArgument, "invalid request"},
		{"unknown product", application.ErrProductNotFound, codes.NotFound, "not found"},
		{"provider unavailable", application.ErrProviderUnavailable, codes.Unavailable, "service unavailable"},
		{"not configured", application.ErrOffersUnavailable, codes.Unavailable, "service unavailable"},
		{"deadline", context.DeadlineExceeded, codes.DeadlineExceeded, "context deadline exceeded"},
		{"canceled", context.Canceled, codes.Canceled, "context canceled"},
		{"unexpected", errors.New(privateMarker), codes.Internal, "internal error"},
		{"invalid quote", domain.ErrInvalidQuote, codes.Internal, "internal error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			logs := new(bytes.Buffer)
			recorder := &offerRecorder{
				err: errors.Join(test.err, errors.New(privateMarker)),
			}
			client := newPolicyClient(t, recorder.createOffer, logs)

			// when
			_, err := client.CreateOffer(t.Context(), validOfferRequest())

			// then
			if status.Code(err) != test.code ||
				status.Convert(err).Message() != test.message {
				t.Errorf("status = %v, want %s %q", err, test.code, test.message)
			}
			if strings.Contains(logs.String(), privateMarker) {
				t.Errorf("logs disclose the private failure: %s", logs.String())
			}
		})
	}
}

func validOfferRequest() *policyv1.CreateOfferRequest {
	return &policyv1.CreateOfferRequest{
		ProductCode:    "TRI",
		PolicyFrom:     "2017-04-16",
		PolicyTo:       "2018-04-15",
		SelectedCovers: []string{"C1", "C2"},
		Answers: []*policyv1.OfferAnswer{
			{
				QuestionCode: "DESTINATION",
				Value:        &policyv1.OfferAnswer_Choice{Choice: "EUR"},
			},
			{
				QuestionCode: "NOTE",
				Value:        &policyv1.OfferAnswer_Text{Text: "first trip"},
			},
			{
				QuestionCode: "NUM_OF_ADULTS",
				Value:        &policyv1.OfferAnswer_Number{Number: "1"},
			},
		},
	}
}

type offerRecorder struct {
	mu       sync.Mutex
	count    int
	received domain.OfferRequest
	offer    domain.Offer
	err      error
}

func (recorder *offerRecorder) createOffer(
	_ context.Context,
	request domain.OfferRequest,
) (domain.Offer, error) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.count++
	recorder.received = request
	return recorder.offer, recorder.err
}

func (recorder *offerRecorder) calls() int {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.count
}

func (recorder *offerRecorder) request() domain.OfferRequest {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.received
}

func newPolicyClient(
	t *testing.T,
	createOffer transport.CreateOffer,
	logs *bytes.Buffer,
) policyv1.PolicyServiceClient {
	t.Helper()
	return newTracedPolicyClient(
		t, createOffer, noop.NewTracerProvider(), propagation.TraceContext{}, logs,
	)
}
