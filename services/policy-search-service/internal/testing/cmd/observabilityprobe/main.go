// Command observabilityprobe emits Policy Search owned signals into a caller-owned collector.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

type searcher struct{}

func (searcher) SearchPolicies(context.Context, string) ([]domain.Policy, error) {
	return []domain.Policy{{Number: "SYN-267", PolicyHolder: "Observability Probe"}}, nil
}

type writer struct{}

func (writer) ReplacePolicy(context.Context, domain.Policy) error { return nil }

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
	provider, err := telemetry.New(
		context.Background(),
		settings.ServiceName,
		settings.Telemetry,
	)
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
	eventObserver, err := telemetry.NewEventObserver(
		log, provider.MeterProvider(), provider.TracerProvider(), true,
	)
	if err != nil {
		return errors.Join(err, shutdown())
	}

	query, err := application.NewSearchPolicies(searcher{})
	if err != nil {
		return errors.Join(err, shutdown())
	}
	projection, err := application.NewProjectPolicy(writer{})
	if err != nil {
		return errors.Join(err, shutdown())
	}

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	queryContext := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)
	request := requestObserver.StartHTTP(queryContext, "GET /policies")
	dependencyContext, dependency := dependencyObserver.StartElasticsearch(
		queryContext, telemetry.ElasticsearchSearch,
	)
	policies, queryErr := query.Execute(dependencyContext, "SYN-267")
	dependency.Finish(dependencyContext, queryErr)
	if queryErr != nil || len(policies) != 1 {
		return errors.Join(errors.New("controlled query path failed"), queryErr, shutdown())
	}
	request.FinishHTTP(queryContext, 200)

	for _, eventType := range []string{"registered", "terminated"} {
		eventContext, event := eventObserver.Start(context.Background(), eventType)
		projectionContext, replace := dependencyObserver.StartElasticsearch(
			eventContext, telemetry.ElasticsearchReplace,
		)
		projectionErr := projection.Execute(projectionContext, application.PolicyEvent{
			Number: "SYN-267", PolicyHolder: "Observability Probe",
		})
		replace.Finish(projectionContext, projectionErr)
		outcome := "success"
		if projectionErr != nil {
			outcome = "error"
		}
		event.Finish(eventContext, outcome, projectionErr)
		if projectionErr != nil {
			return errors.Join(
				errors.New("controlled event path failed"),
				projectionErr,
				shutdown(),
			)
		}
	}

	flushContext, cancel := context.WithTimeout(context.Background(), settings.ExporterTimeout)
	defer cancel()
	return errors.Join(provider.ForceFlush(flushContext), shutdown())
}
