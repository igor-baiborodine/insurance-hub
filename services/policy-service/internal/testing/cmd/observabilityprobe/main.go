// Command observabilityprobe emits Policy owned signals into a caller-owned collector.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/telemetry"
)

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
	dependencyObserver, err := telemetry.NewDependencyObserver(provider.MeterProvider())
	if err != nil {
		return errors.Join(err, shutdown())
	}
	eventObserver, err := telemetry.NewEventObserver(
		log, provider.MeterProvider(), provider.TracerProvider(),
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
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)

	for _, dependency := range []string{"gateway", "database", "kafka"} {
		dependencyObserver.Record(ctx, dependency, nil)
	}
	request := requestObserver.StartHTTP(ctx, "POST /offers")
	dependencyObserver.ObserveCall(ctx, "product", "get_product", 20*time.Millisecond, nil)
	dependencyObserver.ObserveCall(ctx, "pricing", "calculate_price", 30*time.Millisecond, nil)
	request.FinishHTTP(ctx, 200)

	for _, eventType := range []string{"registered", "terminated"} {
		eventContext, event := eventObserver.Start(ctx, eventType)
		event.Finish(eventContext, "success", nil)
	}

	flushContext, cancel := context.WithTimeout(context.Background(), settings.ExporterTimeout)
	defer cancel()
	return errors.Join(provider.ForceFlush(flushContext), shutdown())
}
