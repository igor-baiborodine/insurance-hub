// Package telemetry retains Product's signal and propagation policy over shared provider mechanics.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
	kittelemetry "github.com/igor-baiborodine/insurance-hub/libs/servicekit/telemetry"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
)

var (
	requestBuckets = []float64{
		0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5,
		0.75, 1, 2.5, 5, 7.5, 10,
	}
	databaseBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
)

// Provider retains Product's transport-facing interface while servicekit owns SDK resources.
type Provider struct{ *kittelemetry.Provider }

func New(ctx context.Context, serviceName string, settings config.Telemetry) (*Provider, error) {
	// Disabled callers historically needed no exporter timeout. The startup loader still supplies 2s.
	if !settings.Enabled && settings.ExporterTimeout <= 0 {
		settings.ExporterTimeout = 2 * time.Second
	}
	if settings.Enabled {
		if serviceName == "" {
			return nil, errors.New(
				"initialize telemetry: service name must not be empty",
			)
		}
		if settings.ExporterEndpoint == nil {
			return nil, errors.New(
				"initialize telemetry: exporter endpoint is required",
			)
		}
		if settings.ExporterTimeout <= 0 {
			return nil, errors.New(
				"initialize telemetry: exporter timeout must be positive",
			)
		}
		if settings.Identity.ServiceName != serviceName {
			return nil, errors.New("initialize telemetry: service identity mismatch")
		}
		if err := kitidentity.ValidateDeployment(
			settings.Identity, config.DefaultServiceName, true,
		); err != nil {
			return nil, errors.New("initialize telemetry: invalid resource identity")
		}
	}
	provider, err := kittelemetry.New(ctx, settings, kittelemetry.Options{
		RuntimeScope: "insurance-hub/product/runtime",
		HTTPPropagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader)),
		),
		GRPCPropagator: propagation.TraceContext{},
		MetricViews: []sdkmetric.View{
			kittelemetry.HistogramView(
				"insurance_hub.product.request.duration",
				requestBuckets,
			),
			kittelemetry.HistogramView("db.client.operation.duration", databaseBuckets),
			kittelemetry.HistogramView(
				"db.client.connection.wait_time",
				databaseBuckets,
			),
		},
	})
	if err != nil {
		return nil, err
	}
	return &Provider{Provider: provider}, nil
}

// InstallDiagnostics owns the process-wide SDK error handler until its cleanup is called.
func InstallDiagnostics(logger *slog.Logger) func() {
	return kittelemetry.InstallDiagnostics(logger)
}
