package service

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

type observedRendererFunc func(context.Context, domain.PolicyRegistration) ([]byte, error)

func (function observedRendererFunc) Render(
	ctx context.Context,
	registration domain.PolicyRegistration,
) ([]byte, error) {
	return function(ctx, registration)
}

type observedUploaderFunc func(context.Context, string, []byte) error

func (function observedUploaderFunc) UploadPDF(
	ctx context.Context,
	key string,
	pdf []byte,
) error {
	return function(ctx, key, pdf)
}

type observedMetadataWriterFunc func(context.Context, string, string) error

func (function observedMetadataWriterFunc) InsertObjectKey(
	ctx context.Context,
	policyNumber string,
	key string,
) error {
	return function(ctx, policyNumber, key)
}

func TestObservedGenerationDependencies_RecordBoundedStageOutcomes(t *testing.T) {
	const privateMarker = "private-SYN-customer"
	failure := errors.New(privateMarker)
	tests := []struct {
		name          string
		renderError   error
		uploadError   error
		metadataError error
		wantError     error
		wantSignals   []string
	}{
		{
			name: "success",
			wantSignals: []string{
				`"dependency":"jsreport","operation":"generate_document","stage":"render","outcome":"success"`,
				`"dependency":"object_store","operation":"generate_document","stage":"upload","outcome":"success"`,
				`"dependency":"postgresql","operation":"generate_document","stage":"metadata","outcome":"success"`,
			},
		},
		{
			name: "render failure", renderError: failure, wantError: failure,
			wantSignals: []string{
				`"dependency":"jsreport","operation":"generate_document","stage":"render","outcome":"error"`,
			},
		},
		{
			name: "upload failure", uploadError: failure, wantError: failure,
			wantSignals: []string{
				`"dependency":"jsreport","operation":"generate_document","stage":"render","outcome":"success"`,
				`"dependency":"object_store","operation":"generate_document","stage":"upload","outcome":"error"`,
			},
		},
		{
			name: "metadata failure", metadataError: failure, wantError: failure,
			wantSignals: []string{
				`"dependency":"jsreport","operation":"generate_document","stage":"render","outcome":"success"`,
				`"dependency":"object_store","operation":"generate_document","stage":"upload","outcome":"success"`,
				`"dependency":"postgresql","operation":"generate_document","stage":"metadata","outcome":"error"`,
			},
		},
		{
			name:        "render cancellation",
			renderError: context.Canceled,
			wantError:   context.Canceled,
			wantSignals: []string{
				`"dependency":"jsreport","operation":"generate_document","stage":"render","outcome":"cancelled"`,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			var logs bytes.Buffer
			observer, err := telemetry.NewDependencyObserver(
				slog.New(slog.NewJSONHandler(&logs, nil)),
				metricnoop.NewMeterProvider(),
				tracenoop.NewTracerProvider(),
			)
			if err != nil {
				t.Fatal(err)
			}
			generation, err := application.NewGenerateDocument(
				newObservedPolicyRenderer(
					observedRendererFunc(func(
						context.Context,
						domain.PolicyRegistration,
					) ([]byte, error) {
						return []byte("%PDF-safe"), test.renderError
					}),
					observer,
				),
				newObservedDocumentUploader(
					observedUploaderFunc(
						func(context.Context, string, []byte) error {
							return test.uploadError
						},
					),
					observer,
				),
				newObservedDocumentMetadataWriter(
					observedMetadataWriterFunc(
						func(context.Context, string, string) error {
							return test.metadataError
						},
					),
					observer,
				),
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			_, err = generation.Execute(context.Background(), observedRegistration(t))

			// then
			if !errors.Is(err, test.wantError) {
				t.Fatalf("error = %v, want %v", err, test.wantError)
			}
			output := logs.String()
			if strings.Count(output, "\n") != len(test.wantSignals) {
				t.Fatalf("unexpected signal count: %s", output)
			}
			for _, signal := range test.wantSignals {
				if !strings.Contains(output, signal) {
					t.Errorf("missing signal %s: %s", signal, output)
				}
			}
			if strings.Contains(output, privateMarker) ||
				strings.Contains(output, "SYN-364") {
				t.Fatalf("private value escaped: %s", output)
			}
		})
	}
}

func observedRegistration(t *testing.T) domain.PolicyRegistration {
	t.Helper()
	registration, err := domain.NewPolicyRegistration(domain.PolicyRegistrationInput{
		Number: "SYN-364", From: [3]int{2026, 11, 15}, To: [3]int{2027, 11, 14},
		PolicyHolder: "Synthetic Holder", ProductCode: "HSI", TotalPremium: "42.50",
	})
	if err != nil {
		t.Fatal(err)
	}
	return registration
}
