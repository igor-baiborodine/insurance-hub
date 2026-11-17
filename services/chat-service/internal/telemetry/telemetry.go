// Package telemetry composes the published Servicekit provider for Chat.
package telemetry

import (
	"context"
	"errors"

	kittelemetry "github.com/igor-baiborodine/insurance-hub/libs/servicekit/telemetry"

	"github.com/igor-baiborodine/insurance-hub/services/chat-service/internal/config"
)

// Provider owns Chat telemetry lifecycle through the published Servicekit implementation.
type Provider struct{ *kittelemetry.Provider }

// New creates disabled no-op telemetry or the configured OTLP provider.
func New(ctx context.Context, serviceName string, settings config.Telemetry) (*Provider, error) {
	if settings.Enabled {
		if serviceName == "" {
			return nil, errors.New("initialize telemetry: service name must not be empty")
		}
		if settings.Identity.ServiceName != serviceName {
			return nil, errors.New("initialize telemetry: service identity mismatch")
		}
		if err := (config.Identity{Identity: settings.Identity}).Validate(true); err != nil {
			return nil, errors.New("initialize telemetry: invalid resource identity")
		}
	}
	provider, err := kittelemetry.New(ctx, settings, kittelemetry.Options{
		RuntimeScope: "insurance-hub/chat-service/runtime",
	})
	if err != nil {
		return nil, err
	}
	return &Provider{Provider: provider}, nil
}
