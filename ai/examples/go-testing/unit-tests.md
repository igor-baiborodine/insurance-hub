# Example: Deterministic Unit Tests

Use for pure validation and configuration. Read the [adaptation guide](README.md) first.
Inspired by campsite's validator and config tests; fixed dates avoid midnight-dependent failures.

## Table-driven boundary cases

**Illustrative contract:** `validator.ValidatePeriod(now, start, end time.Time) error` accepts a
start strictly after `now` and an end strictly after the start, otherwise returns/wraps
`validator.ErrPeriod`. This is a sample contract, not an insurance coverage rule. Use calendar
dates/time zones and boundaries from the real specification when adapting.

Suggested location: `internal/application/validator/period_test.go`.

```go
package validator_test

import (
	"testing"
	"time"

	"example.com/policy/internal/application/validator"
	"github.com/stretchr/testify/require"
)

func TestValidatePeriod(t *testing.T) {
	t.Parallel()

	// given
	now := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		start, end time.Time
		wantErr    error
	}{
		{
			name:  "valid future period",
			start: now.Add(time.Hour), end: now.Add(2 * time.Hour),
		},
		{
			name:  "start exactly now is rejected",
			start: now, end: now.Add(time.Hour), wantErr: validator.ErrPeriod,
		},
		{
			name:  "past start is rejected",
			start: now.Add(-time.Hour), end: now.Add(time.Hour), wantErr: validator.ErrPeriod,
		},
		{
			name:  "zero length is rejected",
			start: now.Add(time.Hour), end: now.Add(time.Hour), wantErr: validator.ErrPeriod,
		},
		{
			name:  "reversed period is rejected",
			start: now.Add(2 * time.Hour), end: now.Add(time.Hour), wantErr: validator.ErrPeriod,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel() // Independent immutable inputs; no process state or storage.

			// when
			err := validator.ValidatePeriod(now, tc.start, tc.end)

			// then
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}
```

The input/expected-output table makes the boundary explicit; Go's
[table-driven testing guidance](https://go.dev/wiki/TableDrivenTests) describes this structure.
The fixed clock and immutable case table are shared `given` state; each subtest keeps its action and
assertions under `when` and `then`. Add a subtest-level `given` when a case needs its own setup.
If the real API obtains time itself, inject a clock at its consuming boundary and fix that clock in
tests. Do not replace the production function with a test-only copy of its implementation.

## Environment parsing without leaked state

**Illustrative contract:** `config.Load()` returns a config with `ShutdownTimeout time.Duration`.
It reads `SHUTDOWN_TIMEOUT`, requires a positive duration, and returns an error for invalid values.
Configure any other required variables explicitly. Avoid developer `.env` files in these tests.

Suggested location: `internal/config/config_test.go`.

```go
package config_test

import (
	"testing"
	"time"

	"example.com/policy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoad_ShutdownTimeout(t *testing.T) {
	tests := []struct {
		name, value string
		want        time.Duration
		wantErr     bool
	}{
		{name: "valid", value: "15s", want: 15 * time.Second},
		{name: "invalid duration", value: "later", wantErr: true},
		{name: "zero", value: "0s", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			t.Setenv("SHUTDOWN_TIMEOUT", tc.value)

			// when
			got, err := config.Load()

			// then
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got.ShutdownTimeout)
		})
	}
}
```

`t.Setenv` restores the previous value during cleanup. Do not use `t.Parallel` in this test or its
ancestors because environment variables are process-wide. See [Go's Setenv contract](https://pkg.go.dev/testing#T.Setenv).
For required/unset values, add explicit cases according to the actual loader contract.

Run the owning service's formatting, lint, and unit-test Make targets after adaptation. These
snippets exercise no database, networking, or migration behavior.
