package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/chat"
	servicelogger "github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/logger"
	chatwebsocket "github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/websocket"
)

type callbackRegistration interface{ Unregister() error }

// Observer records bounded Chat signals without content, identity or transport-error values.
type Observer struct {
	logger              *slog.Logger
	tracer              trace.Tracer
	activeConnections   atomic.Int64
	handshakes          metric.Int64Counter
	handshakeDuration   metric.Float64Histogram
	broadcastDeliveries metric.Int64Counter
	connectionCloses    metric.Int64Counter
	shutdownFailures    metric.Int64Counter
	registration        callbackRegistration
	closeOnce           sync.Once
	closeErr            error
}

// NewObserver constructs the Chat-specific signal owner over shared providers.
func NewObserver(
	logger *slog.Logger,
	meterProvider metric.MeterProvider,
	tracerProvider trace.TracerProvider,
) (*Observer, error) {
	if logger == nil || meterProvider == nil || tracerProvider == nil {
		return nil, errors.New(
			"create Chat observer: logger, meter provider and tracer provider are required",
		)
	}
	meter := meterProvider.Meter("insurance-hub/service")
	active, err := meter.Int64ObservableGauge(
		"insurance_hub.chat.connections.active",
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, errors.New("create active connection gauge")
	}
	handshakes, err := meter.Int64Counter(
		"insurance_hub.chat.handshakes",
		metric.WithUnit("{handshake}"),
	)
	if err != nil {
		return nil, errors.New("create handshake counter")
	}
	handshakeDuration, err := meter.Float64Histogram(
		"insurance_hub.chat.handshake.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create handshake duration")
	}
	broadcastDeliveries, err := meter.Int64Counter(
		"insurance_hub.chat.broadcast.deliveries",
		metric.WithUnit("{delivery}"),
	)
	if err != nil {
		return nil, errors.New("create broadcast delivery counter")
	}
	connectionCloses, err := meter.Int64Counter(
		"insurance_hub.chat.connection.closes",
		metric.WithUnit("{connection}"),
	)
	if err != nil {
		return nil, errors.New("create connection close counter")
	}
	shutdownFailures, err := meter.Int64Counter(
		"insurance_hub.chat.shutdown.failures",
		metric.WithUnit("{failure}"),
	)
	if err != nil {
		return nil, errors.New("create shutdown failure counter")
	}

	observer := &Observer{
		logger:              logger,
		tracer:              tracerProvider.Tracer("insurance-hub/service"),
		handshakes:          handshakes,
		handshakeDuration:   handshakeDuration,
		broadcastDeliveries: broadcastDeliveries,
		connectionCloses:    connectionCloses,
		shutdownFailures:    shutdownFailures,
	}
	registration, err := meter.RegisterCallback(
		func(_ context.Context, measurements metric.Observer) error {
			measurements.ObserveInt64(active, observer.activeConnections.Load())
			return nil
		},
		active,
	)
	if err != nil {
		return nil, errors.New("register active connection gauge")
	}
	observer.registration = registration
	return observer, nil
}

// ObserveHandshake records one short, bounded server-handshake observation.
func (observer *Observer) ObserveHandshake(
	ctx context.Context,
	started time.Time,
	outcome chatwebsocket.HandshakeOutcome,
) {
	ended := time.Now()
	boundedOutcome := handshakeOutcome(outcome)
	attrs := []attribute.KeyValue{
		attribute.String("insurance_hub.chat.handshake.outcome", boundedOutcome),
	}
	observer.handshakes.Add(ctx, 1, metric.WithAttributes(attrs...))
	observer.handshakeDuration.Record(
		ctx,
		ended.Sub(started).Seconds(),
		metric.WithAttributes(attrs...),
	)
	logContext, span := observer.tracer.Start(
		ctx,
		"Chat WebSocket handshake",
		trace.WithSpanKind(trace.SpanKindServer),
		trace.WithTimestamp(started),
	)
	span.SetAttributes(attrs...)
	if outcome != chatwebsocket.HandshakeAccepted {
		span.SetAttributes(attribute.String("error.type", boundedOutcome))
		span.SetStatus(codes.Error, "")
	}
	level := slog.LevelInfo
	if outcome != chatwebsocket.HandshakeAccepted {
		level = slog.LevelWarn
	}
	servicelogger.LogHandshake(
		logContext,
		observer.logger,
		level,
		slog.String("outcome", boundedOutcome),
		slog.Float64("duration_ms", float64(ended.Sub(started))/float64(time.Millisecond)),
	)
	span.End(trace.WithTimestamp(ended))
}

// ConnectionOpened records an upgraded connection without connection identity.
func (observer *Observer) ConnectionOpened() {
	observer.activeConnections.Add(1)
	servicelogger.LogConnectionOpened(observer.logger)
}

// ConnectionClosed records a bounded close reason without transport error details.
func (observer *Observer) ConnectionClosed(reason chatwebsocket.CloseReason) {
	for {
		current := observer.activeConnections.Load()
		if current == 0 || observer.activeConnections.CompareAndSwap(current, current-1) {
			break
		}
	}
	boundedReason := closeReason(reason)
	observer.connectionCloses.Add(
		context.Background(),
		1,
		metric.WithAttributes(attribute.String(
			"insurance_hub.chat.connection.close.reason",
			boundedReason,
		)),
	)
	servicelogger.LogConnectionClosed(
		observer.logger,
		slog.String("reason", boundedReason),
	)
}

// ObserveBroadcast records one bounded room-delivery result without Chat content or identities.
func (observer *Observer) ObserveBroadcast(
	event chat.BroadcastEvent,
	outcome chat.DeliveryOutcome,
) {
	boundedEvent := broadcastEvent(event)
	boundedOutcome := deliveryOutcome(outcome)
	observer.broadcastDeliveries.Add(
		context.Background(),
		1,
		metric.WithAttributes(
			attribute.String("insurance_hub.chat.broadcast.event", boundedEvent),
			attribute.String("insurance_hub.chat.broadcast.outcome", boundedOutcome),
		),
	)
	if outcome == chat.DeliveryOutcomeQueueFull {
		servicelogger.LogBroadcastFailure(
			observer.logger,
			slog.String("event", boundedEvent),
			slog.String("outcome", boundedOutcome),
		)
	}
}

// RecordShutdownFailure records a bounded service-shutdown failure.
func (observer *Observer) RecordShutdownFailure(ctx context.Context) {
	observer.shutdownFailures.Add(ctx, 1)
	servicelogger.LogShutdownFailure(ctx, observer.logger)
}

// Close unregisters the observable gauge callback once its provider has completed collection.
func (observer *Observer) Close() error {
	observer.closeOnce.Do(func() {
		if observer.registration != nil {
			observer.closeErr = observer.registration.Unregister()
		}
	})
	return observer.closeErr
}

func handshakeOutcome(outcome chatwebsocket.HandshakeOutcome) string {
	switch outcome {
	case chatwebsocket.HandshakeAccepted:
		return "accepted"
	case chatwebsocket.HandshakeCapacityRejected:
		return "capacity_rejected"
	case chatwebsocket.HandshakeUpgradeFailed:
		return "upgrade_failed"
	default:
		return "unknown"
	}
}

func closeReason(reason chatwebsocket.CloseReason) string {
	switch reason {
	case chatwebsocket.CloseReasonNormal:
		return "normal"
	case chatwebsocket.CloseReasonUnsupportedData:
		return "unsupported_data"
	case chatwebsocket.CloseReasonMessageTooLarge:
		return "message_too_large"
	case chatwebsocket.CloseReasonSlowPeer:
		return "slow_peer"
	case chatwebsocket.CloseReasonDraining:
		return "draining"
	case chatwebsocket.CloseReasonTransportFailure:
		return "transport_failure"
	default:
		return "unknown"
	}
}

func broadcastEvent(event chat.BroadcastEvent) string {
	switch event {
	case chat.BroadcastEventJoin:
		return "join"
	case chat.BroadcastEventMessage:
		return "message"
	case chat.BroadcastEventDisconnect:
		return "disconnect"
	default:
		return "unknown"
	}
}

func deliveryOutcome(outcome chat.DeliveryOutcome) string {
	switch outcome {
	case chat.DeliveryOutcomeDelivered:
		return "delivered"
	case chat.DeliveryOutcomeQueueFull:
		return "queue_full"
	default:
		return "unknown"
	}
}
