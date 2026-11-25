package telemetry

import (
	"context"
	"errors"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// DependencyObserver records the latest readiness result of each required dependency so an
// operator can tell which boundary withdrew readiness, and the bounded outcome of each provider
// call.
type DependencyObserver struct {
	ready    metric.Int64Gauge
	calls    metric.Int64Counter
	duration metric.Float64Histogram
}

// NewDependencyObserver constructs the Policy dependency signal owner.
func NewDependencyObserver(provider metric.MeterProvider) (*DependencyObserver, error) {
	if provider == nil {
		return nil, errors.New(
			"create Policy dependency observer: meter provider is required",
		)
	}
	meter := provider.Meter("insurance-hub/service")
	ready, err := meter.Int64Gauge(
		"insurance_hub.policy.dependency.ready",
		metric.WithUnit("{dependency}"),
	)
	if err != nil {
		return nil, errors.New("create Policy dependency readiness gauge")
	}
	calls, err := meter.Int64Counter(
		"insurance_hub.policy.dependencies",
		metric.WithUnit("{request}"),
	)
	if err != nil {
		return nil, errors.New("create Policy dependency counter")
	}
	duration, err := meter.Float64Histogram(
		"insurance_hub.policy.dependency.duration",
		metric.WithUnit("s"),
	)
	if err != nil {
		return nil, errors.New("create Policy dependency duration")
	}
	return &DependencyObserver{ready: ready, calls: calls, duration: duration}, nil
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

// ObserveCall records one provider call by its fixed dependency, operation and the outcome the
// provider adapter classified, without request data or error details.
func (observer *DependencyObserver) ObserveCall(
	ctx context.Context,
	dependency string,
	operation string,
	elapsed time.Duration,
	outcome string,
) {
	options := metric.WithAttributes(
		attribute.String("insurance_hub.policy.dependency", boundedProvider(dependency)),
		attribute.String(
			"insurance_hub.policy.operation",
			boundedProviderOperation(operation),
		),
		attribute.String("insurance_hub.policy.outcome", boundedProviderOutcome(outcome)),
	)
	observer.calls.Add(ctx, 1, options)
	observer.duration.Record(ctx, elapsed.Seconds(), options)
}

func boundedProviderOutcome(outcome string) string {
	switch outcome {
	case "success", "invalid_request", "cancelled", "deadline_exceeded", "error":
		return outcome
	default:
		return "unknown"
	}
}

func boundedPolicyDependency(dependency string) string {
	switch dependency {
	case "gateway", "database", "kafka":
		return dependency
	default:
		return "unknown"
	}
}

func boundedProvider(dependency string) string {
	switch dependency {
	case "product", "pricing":
		return dependency
	default:
		return "unknown"
	}
}

func boundedProviderOperation(operation string) string {
	switch operation {
	case "get_product", "calculate_price":
		return operation
	default:
		return "unknown"
	}
}
