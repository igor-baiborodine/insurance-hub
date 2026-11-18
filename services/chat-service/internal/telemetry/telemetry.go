// Package telemetry owns Chat signals and composes shared provider mechanics.
package telemetry

import (
	"context"
	"errors"

	kittelemetry "github.com/igor-baiborodine/insurance-hub/libs/servicekit/telemetry"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

var handshakeBuckets = []float64{
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

// Provider retains Chat's service-facing interface while Servicekit owns SDK resources.
type Provider struct{ *kittelemetry.Provider }

// New creates disabled no-op telemetry or the configured OTLP provider.
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
		RuntimeScope: "insurance-hub/service/runtime",
		MetricViews: []sdkmetric.View{
			kittelemetry.HistogramView(
				"insurance_hub.chat.handshake.duration",
				handshakeBuckets,
			),
		},
	})
	if err != nil {
		return nil, err
	}
	return &Provider{Provider: provider}, nil
}
