package telemetry

import (
	"errors"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

const diagnosticInterval = 30 * time.Second

var errSpanQueueSaturated = errors.New("telemetry span queue saturated")

// InstallDiagnostics replaces the OpenTelemetry SDK's process-wide raw-error output with bounded,
// safe diagnostics. The returned cleanup restores the previous handler and must be called in LIFO
// order before the supplied logger is released.
func InstallDiagnostics(logger *slog.Logger) func() {
	previous := otel.GetErrorHandler()
	otel.SetErrorHandler(&exportDiagnostics{logger: logger})
	var once sync.Once
	return func() {
		once.Do(func() { otel.SetErrorHandler(previous) })
	}
}

type exportDiagnostics struct {
	logger     *slog.Logger
	mu         sync.Mutex
	last       time.Time
	suppressed uint64
}

func (diagnostics *exportDiagnostics) Handle(err error) {
	if errors.Is(err, errSpanQueueSaturated) {
		diagnostics.logger.Warn("telemetry span queue saturated")
		return
	}
	now := time.Now()
	diagnostics.mu.Lock()
	if !diagnostics.last.IsZero() && now.Sub(diagnostics.last) < diagnosticInterval {
		diagnostics.suppressed++
		diagnostics.mu.Unlock()
		return
	}
	suppressed := diagnostics.suppressed
	diagnostics.last = now
	diagnostics.suppressed = 0
	diagnostics.mu.Unlock()
	diagnostics.logger.Warn(
		"telemetry export failed",
		slog.Uint64("suppressed_failures", suppressed),
	)
}
