// Package telemetry retains policy search signal policy over shared provider mechanics.
package telemetry

import (
	"context"
	"errors"
	"time"

	kitidentity "github.com/igor-baiborodine/insurance-hub/libs/servicekit/identity"
	kittelemetry "github.com/igor-baiborodine/insurance-hub/libs/servicekit/telemetry"
	"go.opentelemetry.io/contrib/propagators/b3"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
)

var requestBuckets = []float64{
	0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5,
	0.75, 1, 2.5, 5, 7.5, 10,
}

// Provider retains policy search's transport-facing interface while servicekit owns SDK resources.
type Provider struct{ *kittelemetry.Provider }

func New(ctx context.Context, serviceName string, settings config.Telemetry) (*Provider, error) {
	// Disabled callers need no exporter timeout. The startup loader still supplies 2s.
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
		RuntimeScope: "insurance-hub/policy-search/runtime",
		HTTPPropagator: propagation.NewCompositeTextMapPropagator(
			propagation.TraceContext{},
			b3.New(b3.WithInjectEncoding(b3.B3MultipleHeader)),
		),
		MetricViews: []sdkmetric.View{
			kittelemetry.HistogramView(
				"insurance_hub.policy_search.request.duration",
				requestBuckets,
			),
		},
	})
	if err != nil {
		return nil, err
	}
	return &Provider{Provider: provider}, nil
}
