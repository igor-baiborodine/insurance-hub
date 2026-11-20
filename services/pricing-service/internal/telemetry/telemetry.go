// Package telemetry owns Pricing request signals and composes shared provider mechanics.
package telemetry

import (
	"context"
	"errors"

	kittelemetry "github.com/igor-baiborodine/insurance-hub/libs/servicekit/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/config"
)

var (
	requestBuckets = []float64{
		0.005,
		0.01,
		0.025,
		0.05,
		0.075,
		0.1,
		0.25,
		0.5,
		0.75,
		1,
		2.5,
		5,
		7.5,
		10,
	}
	databaseBuckets = []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5, 10}
)

// Provider retains Pricing's gRPC-facing interface while the library owns SDK resources.
type Provider struct{ *kittelemetry.Provider }

func New(ctx context.Context, serviceName string, settings config.Telemetry) (*Provider, error) {
	if settings.Enabled {
		if serviceName == "" {
			return nil, errors.New(
				"initialize telemetry: service name must not be empty",
			)
		}
		if settings.Identity.ServiceName != serviceName {
			return nil, errors.New("initialize telemetry: service identity mismatch")
		}
		serviceIdentity := config.Identity{Identity: settings.Identity}
		if err := serviceIdentity.Validate(true); err != nil {
			return nil, errors.New("initialize telemetry: invalid resource identity")
		}
	}
	provider, err := kittelemetry.New(ctx, settings, kittelemetry.Options{
		RuntimeScope: "insurance-hub/pricing/runtime",
		MetricViews: []sdkmetric.View{
			kittelemetry.HistogramView(
				"insurance_hub.pricing.request.duration",
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
