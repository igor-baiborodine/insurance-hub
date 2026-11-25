package telemetry

import (
	"context"
	"errors"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DependencyObserver records the latest readiness result of each required dependency so an
// operator can tell which boundary withdrew readiness.
type DependencyObserver struct {
	ready metric.Int64Gauge
}

// NewDependencyObserver constructs the Policy dependency readiness signal owner.
func NewDependencyObserver(provider metric.MeterProvider) (*DependencyObserver, error) {
	if provider == nil {
		return nil, errors.New(
			"create Policy dependency observer: meter provider is required",
		)
	}
	ready, err := provider.Meter("insurance-hub/service").Int64Gauge(
		"insurance_hub.policy.dependency.ready",
		metric.WithUnit("{dependency}"),
	)
	if err != nil {
		return nil, errors.New("create Policy dependency readiness gauge")
	}
	return &DependencyObserver{ready: ready}, nil
}

// Record stores 1 when the dependency check passed and 0 otherwise, without error details.
func (observer *DependencyObserver) Record(ctx context.Context, dependency string, err error) {
	value := int64(1)
	if err != nil {
		value = 0
	}
	observer.ready.Record(ctx, value, metric.WithAttributes(
		attribute.String(
			"insurance_hub.policy.dependency",
			boundedPolicyDependency(dependency),
		),
	))
}

func boundedPolicyDependency(dependency string) string {
	switch dependency {
	case "gateway", "database", "kafka":
		return dependency
	default:
		return "unknown"
	}
}
