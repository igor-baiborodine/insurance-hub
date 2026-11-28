package importer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
)

func TestWorker_ProcessesPostedThenEligibleSourcesWithoutOverlappingTicks(t *testing.T) {
	// given
	clock := time.Date(2018, time.August, 2, 23, 59, 0, 0, time.UTC)
	ticks := make(chan time.Time, 1)
	started := make(chan string, 4)
	release := make(chan struct{})
	var active atomic.Int32
	var maximum atomic.Int32
	imports := &fakeImports{
		run: func(ctx context.Context, source string) (application.StatementReceipt, error) {
			current := active.Add(1)
			defer active.Add(-1)
			if current > maximum.Load() {
				maximum.Store(current)
			}
			started <- source
			if source == "bankStatements_2018_8_1.csv" {
				select {
				case <-release:
				case <-ctx.Done():
					return application.StatementReceipt{}, ctx.Err()
				}
			}
			return application.StatementReceipt{
				ID:    1,
				State: application.StatementArchived,
			}, nil
		},
	}
	var observed []Outcome
	var observedMu sync.Mutex
	worker, err := NewWorker(
		Settings{
			Interval: time.Minute,
			Now:      func() time.Time { return clock },
			Ticks:    ticks,
			Observe: func(ctx context.Context) (context.Context, func(context.Context, Outcome, error)) {
				return ctx, func(_ context.Context, outcome Outcome, _ error) {
					observedMu.Lock()
					observed = append(observed, outcome)
					observedMu.Unlock()
				}
			},
		},
		&fakeCatalog{sources: []string{
			"bankStatements_2018_8_1.csv",
			"bankStatements_2018_8_2.csv",
		}},
		&fakePosted{sources: []string{"bankStatements_2018_8_1.csv"}},
		imports,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() { result <- worker.Run(ctx) }()
	if source := receiveSource(t, started); source != "bankStatements_2018_8_1.csv" {
		t.Fatalf("first source = %q", source)
	}
	clock = clock.Add(2 * time.Minute)
	ticks <- clock

	// when
	close(release)
	if source := receiveSource(t, started); source != "bankStatements_2018_8_2.csv" {
		t.Fatalf("second source = %q", source)
	}
	if source := receiveSource(t, started); source != "bankStatements_2018_8_1.csv" {
		t.Fatalf("next-tick first source = %q", source)
	}
	if source := receiveSource(t, started); source != "bankStatements_2018_8_2.csv" {
		t.Fatalf("next-tick second source = %q", source)
	}
	cancel()

	// then
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if maximum.Load() != 1 {
		t.Fatalf("maximum concurrent imports = %d", maximum.Load())
	}
	if fmt.Sprint(imports.sources()) != fmt.Sprint([]string{
		"bankStatements_2018_8_1.csv",
		"bankStatements_2018_8_2.csv",
		"bankStatements_2018_8_1.csv",
		"bankStatements_2018_8_2.csv",
	}) {
		t.Fatalf("imported sources = %v", imports.sources())
	}
	observedMu.Lock()
	defer observedMu.Unlock()
	if len(observed) != 2 || observed[0] != OutcomeArchived || observed[1] != OutcomeArchived {
		t.Fatalf("observed outcomes = %v", observed)
	}
}

func TestWorker_ClassifiesBoundedTickOutcomes(t *testing.T) {
	// given
	tests := []struct {
		name       string
		posted     []string
		sources    []string
		catalogErr error
		receipt    application.StatementReceipt
		err        error
		want       Outcome
	}{
		{name: "idle", want: OutcomeIdle},
		{
			name:       "denied list is a dependency failure",
			catalogErr: errors.New("access denied"),
			want:       OutcomeDependencyFailure,
		},
		{
			name:    "review",
			sources: []string{"bankStatements_2018_8_1.csv"},
			receipt: application.StatementReceipt{State: application.StatementReview},
			want:    OutcomeReview,
		},
		{
			name:   "posted awaiting archive",
			posted: []string{"bankStatements_2018_8_1.csv"},
			err:    application.ErrStatementNotFound,
			want:   OutcomePostedAwaitingArchive,
		},
		{
			name:    "dependency failure",
			sources: []string{"bankStatements_2018_8_1.csv"},
			err:     errors.New("private dependency detail"),
			want:    OutcomeDependencyFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			worker, err := NewWorker(
				Settings{
					Interval: time.Minute,
					Now:      time.Now,
					Observe:  discardObservation,
				},
				&fakeCatalog{sources: test.sources, err: test.catalogErr},
				&fakePosted{sources: test.posted},
				&fakeImports{receipt: test.receipt, err: test.err},
			)
			if err != nil {
				t.Fatal(err)
			}

			// when
			got, _ := worker.importEligible(t.Context(), time.Now())

			// then
			if got != test.want {
				t.Fatalf("outcome = %q, want %q", got, test.want)
			}
		})
	}
}

func discardObservation(
	ctx context.Context,
) (context.Context, func(context.Context, Outcome, error)) {
	return ctx, func(context.Context, Outcome, error) {}
}

type fakeCatalog struct {
	sources []string
	err     error
}

func (catalog *fakeCatalog) DiscoverStatements(context.Context, time.Time) ([]string, error) {
	return append([]string(nil), catalog.sources...), catalog.err
}

type fakePosted struct {
	sources []string
	err     error
}

func (posted *fakePosted) PostedStatementSources(context.Context) ([]string, error) {
	return append([]string(nil), posted.sources...), posted.err
}

type fakeImports struct {
	mu      sync.Mutex
	called  []string
	receipt application.StatementReceipt
	err     error
	run     func(context.Context, string) (application.StatementReceipt, error)
}

func (imports *fakeImports) Import(
	ctx context.Context,
	source string,
) (application.StatementReceipt, error) {
	imports.mu.Lock()
	imports.called = append(imports.called, source)
	imports.mu.Unlock()
	if imports.run != nil {
		return imports.run(ctx, source)
	}
	return imports.receipt, imports.err
}

func (imports *fakeImports) sources() []string {
	imports.mu.Lock()
	defer imports.mu.Unlock()
	return append([]string(nil), imports.called...)
}

func receiveSource(t *testing.T, sources <-chan string) string {
	t.Helper()
	select {
	case source := <-sources:
		return source
	case <-time.After(time.Second):
		t.Fatal("statement import did not start")
		return ""
	}
}
