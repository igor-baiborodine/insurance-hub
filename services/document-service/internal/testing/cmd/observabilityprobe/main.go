// Command observabilityprobe emits Document-owned signals into a caller-owned collector.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

var errControlledStage = errors.New("controlled document stage failure")

type memoryStore struct {
	objects  map[string][]byte
	metadata map[string][]domain.StoredDocument
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		objects:  make(map[string][]byte),
		metadata: make(map[string][]domain.StoredDocument),
	}
}

type observedRenderer struct {
	observer *telemetry.DependencyObserver
}

func (renderer observedRenderer) Render(
	ctx context.Context,
	registration domain.PolicyRegistration,
) ([]byte, error) {
	ctx, observation := renderer.observer.Start(
		ctx, telemetry.OperationGenerateDocument, telemetry.DependencyJSReport,
	)
	var err error
	if strings.Contains(registration.Number(), "RENDER-FAIL") {
		err = errControlledStage
	}
	observation.Finish(ctx, telemetry.DependencyResultSuccess, err)
	if err != nil {
		return nil, err
	}
	return []byte("%PDF-1.7\ncontrolled observability document\n%%EOF\n"), nil
}

type observedUploader struct {
	observer *telemetry.DependencyObserver
	store    *memoryStore
	policy   string
}

func (uploader observedUploader) UploadPDF(ctx context.Context, key string, pdf []byte) error {
	ctx, observation := uploader.observer.Start(
		ctx, telemetry.OperationGenerateDocument, telemetry.DependencyObjectStore,
	)
	var err error
	if strings.Contains(uploader.policy, "UPLOAD-FAIL") {
		err = errControlledStage
	} else {
		uploader.store.objects[key] = append([]byte(nil), pdf...)
	}
	observation.Finish(ctx, telemetry.DependencyResultSuccess, err)
	return err
}

type observedMetadata struct {
	observer *telemetry.DependencyObserver
	store    *memoryStore
}

func (metadata observedMetadata) InsertObjectKey(
	ctx context.Context,
	policyNumber string,
	key string,
) error {
	ctx, observation := metadata.observer.Start(
		ctx, telemetry.OperationGenerateDocument, telemetry.DependencyPostgreSQL,
	)
	var err error
	if strings.Contains(policyNumber, "METADATA-FAIL") {
		err = errControlledStage
	} else {
		metadata.store.metadata[policyNumber] = append(
			metadata.store.metadata[policyNumber],
			domain.StoredDocument{PolicyNumber: policyNumber, Bytes: []byte(key)},
		)
	}
	observation.Finish(ctx, telemetry.DependencyResultSuccess, err)
	return err
}

func (metadata observedMetadata) FindByPolicyNumber(
	ctx context.Context,
	policyNumber string,
) ([]domain.StoredDocument, error) {
	ctx, observation := metadata.observer.Start(
		ctx, telemetry.OperationFindDocuments, telemetry.DependencyPostgreSQL,
	)
	rows := append([]domain.StoredDocument(nil), metadata.store.metadata[policyNumber]...)
	observation.Finish(ctx, telemetry.DependencyResultSuccess, nil)
	return rows, nil
}

type observedContent struct {
	observer *telemetry.DependencyObserver
	store    *memoryStore
}

func (content observedContent) Read(
	ctx context.Context,
	key []byte,
) ([]byte, bool, error) {
	ctx, observation := content.observer.Start(
		ctx, telemetry.OperationFindDocuments, telemetry.DependencyObjectStore,
	)
	value, present := content.store.objects[string(key)]
	observation.Finish(ctx, telemetry.DependencyResultSuccess, nil)
	return append([]byte(nil), value...), present, nil
}

func main() {
	traceIDValue := flag.String("trace-id", "", "32-character lowercase trace ID")
	flag.Parse()
	if err := run(*traceIDValue); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(traceIDValue string) error {
	traceID, err := trace.TraceIDFromHex(traceIDValue)
	if err != nil {
		return errors.New("parse probe trace ID")
	}
	settings, err := config.Load()
	if err != nil {
		return fmt.Errorf("load probe configuration: %w", err)
	}
	log := logger.NewIdentity(os.Stdout, settings.Identity, slog.LevelInfo)
	provider, err := telemetry.New(context.Background(), settings.ServiceName, settings.Telemetry)
	if err != nil {
		return fmt.Errorf("initialize probe telemetry: %w", err)
	}
	shutdown := func() error {
		ctx, cancel := context.WithTimeout(context.Background(), settings.ExporterTimeout)
		defer cancel()
		return provider.Shutdown(ctx)
	}

	requestObserver, err := telemetry.NewRequestObserver(log, provider.MeterProvider())
	if err != nil {
		return errors.Join(err, shutdown())
	}
	dependencyObserver, err := telemetry.NewDependencyObserver(
		log, provider.MeterProvider(), provider.TracerProvider(),
	)
	if err != nil {
		return errors.Join(err, shutdown())
	}
	lookupObserver, err := telemetry.NewLookupObserver(log, provider.MeterProvider())
	if err != nil {
		return errors.Join(err, shutdown())
	}
	eventObserver, err := telemetry.NewEventObserver(
		log, provider.MeterProvider(), provider.TracerProvider(), true,
	)
	if err != nil {
		return errors.Join(err, shutdown())
	}

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	probeContext := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)
	store := newMemoryStore()
	metadata := observedMetadata{observer: dependencyObserver, store: store}

	registration, err := newRegistration("SYN-DOCUMENT")
	if err != nil {
		return errors.Join(err, shutdown())
	}
	if err := generate(probeContext, eventObserver, dependencyObserver, metadata, store, registration); err != nil {
		return errors.Join(errors.New("controlled generation path failed"), err, shutdown())
	}
	lookup, err := application.NewFindDocuments(
		metadata,
		observedContent{observer: dependencyObserver, store: store},
		1024,
		1,
	)
	if err != nil {
		return errors.Join(err, shutdown())
	}
	request := requestObserver.StartHTTP(probeContext, "GET /documents/{policyNumber}")
	result, lookupErr := lookup.Execute(probeContext, registration.Number())
	lookupObserver.Record(probeContext, len(result.Documents), result.Omitted, lookupErr)
	if lookupErr != nil || len(result.Documents) != 1 ||
		!strings.HasPrefix(string(result.Documents[0].Content), "%PDF-") {
		return errors.Join(errors.New("controlled lookup path failed"), lookupErr, shutdown())
	}
	request.FinishHTTP(probeContext, 200)

	for _, policyNumber := range []string{
		"SYN-RENDER-FAIL", "SYN-UPLOAD-FAIL", "SYN-METADATA-FAIL",
	} {
		failedRegistration, registrationErr := newRegistration(policyNumber)
		if registrationErr != nil {
			return errors.Join(registrationErr, shutdown())
		}
		generationErr := generate(
			probeContext, eventObserver, dependencyObserver, metadata, store, failedRegistration,
		)
		if !errors.Is(generationErr, errControlledStage) {
			return errors.Join(errors.New("controlled stage failure was not preserved"), shutdown())
		}
		if len(store.metadata[policyNumber]) != 0 {
			return errors.Join(errors.New("failed generation registered metadata"), shutdown())
		}
	}
	if len(store.objects) != 2 {
		return errors.Join(errors.New("controlled orphan semantics differ"), shutdown())
	}
	eventObserver.RecordCommitFailure(probeContext, "registered")

	flushContext, cancel := context.WithTimeout(context.Background(), settings.ExporterTimeout)
	defer cancel()
	return errors.Join(provider.ForceFlush(flushContext), shutdown())
}

func generate(
	ctx context.Context,
	events *telemetry.EventObserver,
	dependencies *telemetry.DependencyObserver,
	metadata observedMetadata,
	store *memoryStore,
	registration domain.PolicyRegistration,
) error {
	generation, err := application.NewGenerateDocument(
		observedRenderer{observer: dependencies},
		observedUploader{observer: dependencies, store: store, policy: registration.Number()},
		metadata,
	)
	if err != nil {
		return err
	}
	eventContext, event := events.Start(ctx, "registered")
	_, generationErr := generation.Execute(eventContext, registration)
	outcome := "success"
	if generationErr != nil {
		outcome = "error"
	}
	event.Finish(eventContext, outcome, generationErr)
	return generationErr
}

func newRegistration(policyNumber string) (domain.PolicyRegistration, error) {
	return domain.NewPolicyRegistration(domain.PolicyRegistrationInput{
		Number:       policyNumber,
		From:         [3]int{2026, 11, 15},
		To:           [3]int{2027, 11, 15},
		PolicyHolder: "Observability Probe",
		ProductCode:  "SYNTHETIC",
		TotalPremium: "1.00",
	})
}
