package main

import (
	"testing"
	"time"
)

func TestPercentile_UsesNearestRank(t *testing.T) {
	// given
	values := []time.Duration{
		time.Millisecond,
		2 * time.Millisecond,
		3 * time.Millisecond,
		4 * time.Millisecond,
	}

	// when
	p95 := percentile(values, 0.95)
	p50 := percentile(values, 0.50)

	// then
	if p95 != 4*time.Millisecond || p50 != 2*time.Millisecond {
		t.Fatalf("percentiles = %s/%s, want 4ms/2ms", p95, p50)
	}
}

func TestDeterministicTraceID_IsStableAndUnique(t *testing.T) {
	// when
	first := deterministicTraceID("run", 1)
	repeated := deterministicTraceID("run", 1)
	second := deterministicTraceID("run", 2)

	// then
	if len(first) != 32 || first != repeated || first == second {
		t.Fatalf(
			"trace IDs are not stable unique 128-bit values: %q/%q/%q",
			first,
			repeated,
			second,
		)
	}
}
