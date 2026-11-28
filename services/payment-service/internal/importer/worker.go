// Package importer schedules isolated Payment bank-statement imports.
package importer

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
)

// Outcome is one bounded, payload-free statement tick result.
type Outcome string

const (
	OutcomeIdle                  Outcome = "idle"
	OutcomeArchived              Outcome = "archived"
	OutcomeReview                Outcome = "review"
	OutcomePostedAwaitingArchive Outcome = "posted_awaiting_archive"
	OutcomeDependencyFailure     Outcome = "dependency_failure"
)

const (
	workerReady int32 = iota + 1
	workerRunning
	workerStopped
)

// SourceCatalog discovers eligible source identities through one UTC business date.
type SourceCatalog interface {
	DiscoverStatements(context.Context, time.Time) ([]string, error)
}

// PostedSourceReader lists durable postings that need archive reconciliation.
type PostedSourceReader interface {
	PostedStatementSources(context.Context) ([]string, error)
}

// StatementImporter posts or resumes one logical statement source.
type StatementImporter interface {
	Import(context.Context, string) (application.StatementReceipt, error)
}

// Observe starts one bounded statement-import tick observation.
type Observe func(context.Context) (context.Context, func(context.Context, Outcome, error))

// Settings controls one process-owned, non-overlapping scheduler.
type Settings struct {
	Interval time.Duration
	Now      func() time.Time
	Observe  Observe
	Ticks    <-chan time.Time
}

// Worker serializes immediate and scheduled statement-import ticks.
type Worker struct {
	catalog  SourceCatalog
	posted   PostedSourceReader
	imports  StatementImporter
	interval time.Duration
	now      func() time.Time
	observe  Observe
	ticks    <-chan time.Time
	state    atomic.Int32
}

// NewWorker constructs one statement scheduler without starting it.
func NewWorker(
	settings Settings,
	catalog SourceCatalog,
	posted PostedSourceReader,
	imports StatementImporter,
) (*Worker, error) {
	if settings.Interval <= 0 || settings.Now == nil || settings.Observe == nil ||
		catalog == nil || posted == nil || imports == nil {
		return nil, errors.New(
			"configure statement importer: all dependencies are required",
		)
	}
	worker := &Worker{
		catalog: catalog, posted: posted, imports: imports,
		interval: settings.Interval, now: settings.Now, observe: settings.Observe,
		ticks: settings.Ticks,
	}
	worker.state.Store(workerReady)
	return worker, nil
}

// Check reports whether the scheduler is ready or running.
func (worker *Worker) Check(context.Context) error {
	if worker == nil || (worker.state.Load() != workerReady &&
		worker.state.Load() != workerRunning) {
		return errors.New("check statement importer: worker is unavailable")
	}
	return nil
}

// Preflight confirms the constructed scheduler is ready without touching dependencies.
func (worker *Worker) Preflight(ctx context.Context) error { return worker.Check(ctx) }

// Shutdown has no client of its own; Run exits when its owned context is cancelled.
func (*Worker) Shutdown(context.Context) error { return nil }

// Run executes one immediate tick and then every configured interval without overlap.
func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil || !worker.state.CompareAndSwap(workerReady, workerRunning) {
		return errors.New("run statement importer: worker is unavailable")
	}
	defer worker.state.Store(workerStopped)
	ticks := worker.ticks
	var ticker *time.Ticker
	if ticks == nil {
		ticker = time.NewTicker(worker.interval)
		ticks = ticker.C
		defer ticker.Stop()
	}
	worker.runTick(ctx, worker.now().UTC())
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, open := <-ticks:
			if !open {
				return nil
			}
			worker.runTick(ctx, worker.now().UTC())
		}
	}
}

func (worker *Worker) runTick(ctx context.Context, businessDate time.Time) {
	observeCtx, finish := worker.observe(ctx)
	outcome, err := worker.importEligible(observeCtx, businessDate)
	finish(observeCtx, outcome, err)
}

func (worker *Worker) importEligible(
	ctx context.Context,
	businessDate time.Time,
) (Outcome, error) {
	postedSources, err := worker.posted.PostedStatementSources(ctx)
	if err != nil {
		return OutcomeDependencyFailure, err
	}
	discovered, err := worker.catalog.DiscoverStatements(ctx, businessDate)
	if err != nil {
		return OutcomeDependencyFailure, err
	}
	posted := make(map[string]bool, len(postedSources))
	queued := make(map[string]bool, len(postedSources)+len(discovered))
	sources := make([]string, 0, len(postedSources)+len(discovered))
	for _, sourceName := range append(postedSources, discovered...) {
		if queued[sourceName] {
			continue
		}
		queued[sourceName] = true
		sources = append(sources, sourceName)
	}
	for _, sourceName := range postedSources {
		posted[sourceName] = true
	}
	if len(sources) == 0 {
		return OutcomeIdle, nil
	}
	outcome := OutcomeArchived
	var failures []error
	for _, sourceName := range sources {
		receipt, importErr := worker.imports.Import(ctx, sourceName)
		itemOutcome := classify(receipt, importErr, posted[sourceName])
		if outcomePriority(itemOutcome) > outcomePriority(outcome) {
			outcome = itemOutcome
		}
		if importErr != nil {
			failures = append(failures, importErr)
		}
	}
	return outcome, errors.Join(failures...)
}

func classify(
	receipt application.StatementReceipt,
	err error,
	posted bool,
) Outcome {
	if receipt.State == application.StatementReview ||
		errors.Is(err, application.ErrStatementInvalid) ||
		errors.Is(err, application.ErrStatementLimit) ||
		errors.Is(err, application.ErrStatementConflict) {
		return OutcomeReview
	}
	if receipt.State == application.StatementPosted || (posted && err != nil) {
		return OutcomePostedAwaitingArchive
	}
	if err != nil {
		return OutcomeDependencyFailure
	}
	return OutcomeArchived
}

func outcomePriority(outcome Outcome) int {
	switch outcome {
	case OutcomeDependencyFailure:
		return 4
	case OutcomePostedAwaitingArchive:
		return 3
	case OutcomeReview:
		return 2
	case OutcomeArchived:
		return 1
	default:
		return 0
	}
}
