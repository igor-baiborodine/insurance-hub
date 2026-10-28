package telemetry

import (
	"context"
	"math"
	"runtime/metrics"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// registerRuntime observes the current Go process on each collection. Cumulative values remain
// process-local, so a new process starts fresh series under its caller-provided identity.
func registerRuntime(
	provider metric.MeterProvider,
	scope string,
) (metric.Registration, error) {
	meter := provider.Meter(scope)
	used, err := meter.Int64ObservableUpDownCounter("go.memory.used", metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	limit, err := meter.Int64ObservableUpDownCounter("go.memory.limit", metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	allocated, err := meter.Int64ObservableCounter("go.memory.allocated", metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	allocations, err := meter.Int64ObservableCounter(
		"go.memory.allocations",
		metric.WithUnit("{allocation}"),
	)
	if err != nil {
		return nil, err
	}
	goal, err := meter.Int64ObservableUpDownCounter("go.memory.gc.goal", metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	cycles, err := meter.Int64ObservableCounter(
		"go.memory.gc.cycles",
		metric.WithUnit("{gc_cycle}"),
	)
	if err != nil {
		return nil, err
	}
	gro, err := meter.Int64ObservableUpDownCounter(
		"go.goroutine.count",
		metric.WithUnit("{goroutine}"),
	)
	if err != nil {
		return nil, err
	}
	procs, err := meter.Int64ObservableUpDownCounter(
		"go.processor.limit",
		metric.WithUnit("{thread}"),
	)
	if err != nil {
		return nil, err
	}
	names := []string{
		"/memory/classes/total:bytes", "/memory/classes/heap/released:bytes",
		"/memory/classes/heap/stacks:bytes",
		"/gc/gomemlimit:bytes", "/gc/heap/allocs:bytes", "/gc/heap/allocs:objects",
		"/gc/heap/goal:bytes", "/gc/cycles/total:gc-cycles",
		"/sched/goroutines:goroutines", "/sched/gomaxprocs:threads",
	}
	samples := make([]metrics.Sample, len(names))
	for index, name := range names {
		samples[index].Name = name
	}
	var sampleMu sync.Mutex
	return meter.RegisterCallback(func(_ context.Context, observer metric.Observer) error {
		sampleMu.Lock()
		defer sampleMu.Unlock()
		metrics.Read(samples)
		value := func(index int) int64 {
			switch samples[index].Value.Kind() {
			case metrics.KindUint64:
				return int64(samples[index].Value.Uint64())
			case metrics.KindFloat64:
				return int64(samples[index].Value.Float64())
			default:
				return 0
			}
		}
		stack := value(2)
		other := value(0) - value(1) - stack
		if other < 0 {
			other = 0
		}
		observer.ObserveInt64(
			used,
			stack,
			metric.WithAttributes(attribute.String("go.memory.type", "stack")),
		)
		observer.ObserveInt64(
			used,
			other,
			metric.WithAttributes(attribute.String("go.memory.type", "other")),
		)
		if memoryLimit := value(3); memoryLimit != math.MaxInt64 {
			observer.ObserveInt64(limit, memoryLimit)
		}
		observer.ObserveInt64(allocated, value(4))
		observer.ObserveInt64(allocations, value(5))
		observer.ObserveInt64(goal, value(6))
		observer.ObserveInt64(cycles, value(7))
		observer.ObserveInt64(gro, value(8))
		observer.ObserveInt64(procs, value(9))
		return nil
	}, used, limit, allocated, allocations, goal, cycles, gro, procs)
}
