// Command observabilityprobe emits Chat-owned signals into a caller-owned collector.
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

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/telemetry"
	chatwebsocket "github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/websocket"
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
	observer, err := telemetry.NewObserver(
		log,
		provider.MeterProvider(),
		provider.TracerProvider(),
	)
	if err != nil {
		return errors.Join(err, shutdown())
	}
	closeObserver := func() error { return observer.Close() }

	spanContext := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    traceID,
		SpanID:     trace.SpanID{0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 0xcd, 0xef},
		TraceFlags: trace.FlagsSampled,
		Remote:     true,
	})
	probeContext := trace.ContextWithRemoteSpanContext(context.Background(), spanContext)
	started := time.Now().Add(-10 * time.Millisecond)
	observer.ObserveHandshake(probeContext, started, chatwebsocket.HandshakeAccepted)
	observer.ObserveHandshake(probeContext, started, chatwebsocket.HandshakeCapacityRejected)
	observer.ObserveHandshake(probeContext, started, chatwebsocket.HandshakeUpgradeFailed)

	observer.ConnectionOpened()
	observer.ObserveBroadcast(chat.BroadcastEventJoin, chat.DeliveryOutcomeDelivered)
	observer.ObserveBroadcast(chat.BroadcastEventMessage, chat.DeliveryOutcomeQueueFull)
	observer.ConnectionClosed(chatwebsocket.CloseReasonNormal)
	observer.ConnectionClosed(chatwebsocket.CloseReasonSlowPeer)
	observer.RecordShutdownFailure(probeContext)

	flushContext, cancel := context.WithTimeout(context.Background(), settings.ExporterTimeout)
	defer cancel()
	return errors.Join(provider.ForceFlush(flushContext), shutdown(), closeObserver())
}
