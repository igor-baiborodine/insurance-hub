package telemetry

import (
	"errors"
	"log"
	"log/slog"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
)

const diagnosticInterval = 30 * time.Second

var errSpanQueueSaturated = errors.New("telemetry span queue saturated")

// InstallDiagnostics replaces the OpenTelemetry SDK's process-wide raw-error output with bounded,
// safe diagnostics. Cleanup restores the previous handler and detaches the supplied logger; call
// it in LIFO order after providers shut down. On the first installation, OpenTelemetry's default
// delegator permanently retains this handler, so post-cleanup errors use a fixed fallback warning.
func InstallDiagnostics(logger *slog.Logger) func() {
	previous := otel.GetErrorHandler()
	diagnostics := &exportDiagnostics{logger: logger}
	otel.SetErrorHandler(diagnostics)
	var once sync.Once
	return func() {
		once.Do(func() {
			otel.SetErrorHandler(previous)
			diagnostics.detach()
		})
	}
}

type exportDiagnostics struct {
	logger     *slog.Logger
	mu         sync.Mutex
	last       time.Time
	suppressed uint64
}

func (diagnostics *exportDiagnostics) Handle(err error) {
	diagnostics.mu.Lock()
	defer diagnostics.mu.Unlock()
	if diagnostics.logger != nil && errors.Is(err, errSpanQueueSaturated) {
		diagnostics.logger.Warn("telemetry span queue saturated")
		return
	}
	now := time.Now()
	if !diagnostics.last.IsZero() && now.Sub(diagnostics.last) < diagnosticInterval {
		diagnostics.suppressed++
		return
	}
	suppressed := diagnostics.suppressed
	diagnostics.last = now
	diagnostics.suppressed = 0
	if diagnostics.logger == nil {
		log.Print("telemetry export failed")
		return
	}
	diagnostics.logger.Warn(
		"telemetry export failed",
		slog.Uint64("suppressed_failures", suppressed),
	)
}

func (diagnostics *exportDiagnostics) detach() {
	diagnostics.mu.Lock()
	diagnostics.logger = nil
	diagnostics.mu.Unlock()
}
