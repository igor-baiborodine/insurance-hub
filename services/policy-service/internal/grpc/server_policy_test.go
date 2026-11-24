package grpc_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/grpc"
)

func TestPolicyService_IssuePolicy_MapsTheCommand(t *testing.T) {
	// given
	var got application.IssuePolicyCommand
	client := newLifecycleClient(t, transport.Handlers{
		IssuePolicy: func(_ context.Context, command application.IssuePolicyCommand) (string, error) {
			got = command
			return "policy-1", nil
		},
	}, new(bytes.Buffer))

	// when
	response, err := client.IssuePolicy(t.Context(), &policyv1.IssuePolicyRequest{
		OfferNumber: "offer-1",
		PolicyHolder: &policyv1.PolicyHolder{
			FirstName: "Synthetic", LastName: "Holder", TaxId: "00000",
		},
		AgentLogin: "agent",
	})

	// then
	want := application.IssuePolicyCommand{
		OfferNumber: "offer-1",
		Holder: domain.PolicyHolder{
			FirstName: "Synthetic", LastName: "Holder", TaxID: "00000",
		},
		AgentLogin: "agent",
	}
	if err != nil || response.GetPolicyNumber() != "policy-1" || got != want {
		t.Errorf("response = %v (%v), command %+v, want %+v", response, err, got, want)
	}
}

func TestPolicyService_GetPolicy_ReturnsLatestDetailsWithExactCovers(t *testing.T) {
	// given
	client := newLifecycleClient(t, transport.Handlers{
		GetPolicy: func(context.Context, string) (application.PolicyDetails, error) {
			return application.PolicyDetails{
				Number: "policy-1",
				Latest: domain.PolicyVersion{
					Number:        2,
					ProductCode:   "HSI",
					AccountNumber: "account-1",
					Holder: domain.PolicyHolder{
						FirstName: "Synthetic",
						LastName:  "Holder",
					},
					VersionFrom:  time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC),
					VersionTo:    time.Date(2027, 11, 13, 0, 0, 0, 0, time.UTC),
					TotalPremium: "3.71",
					Covers: []domain.Cover{
						{
							Code:  "C2",
							Price: "0.70",
						},
						{Code: "C1", Price: "3.01"},
					},
				},
			}, nil
		},
	}, new(bytes.Buffer))

	// when
	response, err := client.GetPolicy(t.Context(),
		&policyv1.GetPolicyRequest{PolicyNumber: "policy-1"})

	// then
	want := &policyv1.PolicyDetails{
		Number: "policy-1", DateFrom: "2026-11-24", DateTo: "2027-11-13",
		PolicyHolder: "Synthetic Holder", TotalPremium: "3.71", ProductCode: "HSI",
		AccountNumber: "account-1", Covers: []string{"C1 - 3.01", "C2 - 0.70"},
	}
	if err != nil || !proto.Equal(response.GetPolicy(), want) {
		t.Errorf("response = %v (%v), want %v", response, err, want)
	}
}

func TestPolicyService_TerminatePolicy_ReturnsTheTerminatedNumber(t *testing.T) {
	// given
	var got string
	client := newLifecycleClient(t, transport.Handlers{
		TerminatePolicy: func(_ context.Context, number string) (string, error) {
			got = number
			return number, nil
		},
	}, new(bytes.Buffer))

	// when
	response, err := client.TerminatePolicy(t.Context(),
		&policyv1.TerminatePolicyRequest{PolicyNumber: "policy-1"})

	// then
	if err != nil || response.GetPolicyNumber() != "policy-1" || got != "policy-1" {
		t.Errorf("response = %v (%v), use case received %q", response, err, got)
	}
}

func TestPolicyService_Lifecycle_MapsFailuresToSafeStatuses(t *testing.T) {
	for _, test := range []struct {
		name     string
		err      error
		wantCode codes.Code
		wantText string
		wantLog  bool
	}{
		{"absent offer", application.ErrOfferNotFound, codes.NotFound, "not found", false},
		{"absent policy", application.ErrPolicyNotFound, codes.NotFound, "not found", false},
		{"expired offer", domain.ErrOfferExpired, codes.FailedPrecondition, "conflict", false},
		{
			"termination outside coverage", domain.ErrTerminationOutsideCoverage,
			codes.FailedPrecondition, "conflict", false,
		},
		{
			"storage unavailable", application.ErrStorageUnavailable, codes.Unavailable,
			"service unavailable", false,
		},
		{
			"storage not configured", application.ErrPoliciesUnavailable, codes.Unavailable,
			"service unavailable", false,
		},
		{
			"deadline", context.DeadlineExceeded, codes.DeadlineExceeded,
			"context deadline exceeded", false,
		},
		{"defect", errors.New("private-marker"), codes.Internal, "internal error", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			logs := new(bytes.Buffer)
			failure := fmt.Errorf("use case: %w", test.err)
			client := newLifecycleClient(t, transport.Handlers{
				IssuePolicy: func(context.Context, application.IssuePolicyCommand) (string, error) {
					return "", failure
				},
				GetPolicy: func(context.Context, string) (application.PolicyDetails, error) {
					return application.PolicyDetails{}, failure
				},
				TerminatePolicy: func(context.Context, string) (string, error) {
					return "", failure
				},
			}, logs)

			// when
			_, issueErr := client.IssuePolicy(t.Context(), &policyv1.IssuePolicyRequest{
				OfferNumber: "offer-1", PolicyHolder: &policyv1.PolicyHolder{},
			})
			_, getErr := client.GetPolicy(t.Context(),
				&policyv1.GetPolicyRequest{PolicyNumber: "policy-1"})
			_, terminateErr := client.TerminatePolicy(t.Context(),
				&policyv1.TerminatePolicyRequest{PolicyNumber: "policy-1"})

			// then
			for _, err := range []error{issueErr, getErr, terminateErr} {
				if got := status.Convert(err); got.Code() != test.wantCode ||
					got.Message() != test.wantText {
					t.Errorf(
						"status = %v, want %v %q",
						got,
						test.wantCode,
						test.wantText,
					)
				}
			}
			if strings.Contains(logs.String(), "private-marker") ||
				strings.Contains(
					logs.String(),
					"policy request failed",
				) != test.wantLog {
				t.Errorf("logs = %s", logs.String())
			}
		})
	}
}

func TestPolicyService_Lifecycle_RejectsInvalidInputBeforeUseCase(t *testing.T) {
	// given
	called := false
	client := newLifecycleClient(t, transport.Handlers{
		IssuePolicy: func(context.Context, application.IssuePolicyCommand) (string, error) {
			called = true
			return "policy-1", nil
		},
		GetPolicy: func(context.Context, string) (application.PolicyDetails, error) {
			called = true
			return application.PolicyDetails{}, nil
		},
		TerminatePolicy: func(context.Context, string) (string, error) {
			called = true
			return "", nil
		},
	}, new(bytes.Buffer))

	for _, test := range []struct {
		name string
		call func() error
	}{
		{"issue without offer", func() error {
			_, err := client.IssuePolicy(t.Context(), &policyv1.IssuePolicyRequest{
				PolicyHolder: &policyv1.PolicyHolder{},
			})
			return err
		}},
		{"issue without holder", func() error {
			_, err := client.IssuePolicy(t.Context(),
				&policyv1.IssuePolicyRequest{OfferNumber: "offer-1"})
			return err
		}},
		{"holder longer than storage", func() error {
			_, err := client.IssuePolicy(t.Context(), &policyv1.IssuePolicyRequest{
				OfferNumber:  "offer-1",
				PolicyHolder: &policyv1.PolicyHolder{LastName: strings.Repeat("x", 256)},
			})
			return err
		}},
		{"termination without number", func() error {
			_, err := client.TerminatePolicy(t.Context(), &policyv1.TerminatePolicyRequest{})
			return err
		}},
		{"lookup without number", func() error {
			_, err := client.GetPolicy(t.Context(), &policyv1.GetPolicyRequest{})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			err := test.call()

			// then
			if status.Code(err) != codes.InvalidArgument || called {
				t.Errorf("status = %v, use case called %t", err, called)
			}
		})
	}
}

func TestPolicyService_Lifecycle_ReportsUnconfiguredStorage(t *testing.T) {
	// given
	client := newLifecycleClient(t, transport.Handlers{}, new(bytes.Buffer))

	// when
	_, err := client.GetPolicy(t.Context(), &policyv1.GetPolicyRequest{PolicyNumber: "p"})

	// then
	if status.Code(err) != codes.Unavailable {
		t.Errorf("status = %v, want Unavailable", err)
	}
}

func newLifecycleClient(
	t *testing.T,
	handlers transport.Handlers,
	logs *bytes.Buffer,
) policyv1.PolicyServiceClient {
	t.Helper()
	handlers.CreateOffer = func(context.Context, domain.OfferRequest) (domain.Offer, error) {
		return domain.Offer{}, errors.New("offer creation is not under test")
	}
	return newHandlersClient(t, handlers, noop.NewTracerProvider(), propagation.TraceContext{},
		logs)
}
