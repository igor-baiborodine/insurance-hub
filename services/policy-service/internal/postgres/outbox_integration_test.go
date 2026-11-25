//go:build integration

package postgres_test

import (
	"context"
	_ "embed"
	"slices"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/testing/postgresfixture"
)

//go:embed testdata/sql/insert-ordering-intents.sql
var insertOrderingIntentsQuery string

type publishResult struct {
	published bool
	err       error
}

// TestRun_PolicyOutboxOrdering runs two publishers against one database: while the first holds
// a policy's registration, the second must skip that policy's termination and take another
// policy's intent instead.
func TestRun_PolicyOutboxOrdering(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	fixture := postgresfixture.New(t, ctx)
	fixture.Call(t, ctx, "migrate", "version=2")
	fixture.Exec(t, ctx, insertOrderingIntentsQuery)
	first := openOutboxStore(t, ctx, fixture)
	second := openOutboxStore(t, ctx, fixture)
	locked := make(chan string, 1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	firstResult := make(chan publishResult, 1)
	go func() {
		published, err := first.PublishNext(
			ctx,
			func(_ context.Context, intent application.EventIntent) error {
				locked <- intentName(intent)
				<-release
				return nil
			},
		)
		firstResult <- publishResult{published: published, err: err}
	}()
	var held string
	select {
	case held = <-locked:
	case <-ctx.Done():
		t.Fatal("first publisher did not lock an intent")
	}
	var concurrent []string

	// when
	published, err := second.PublishNext(ctx, recordIntent(&concurrent))

	// then
	if err != nil || !published ||
		!slices.Equal(concurrent, []string{"ordering-b policy-registered"}) {
		t.Fatalf(
			"concurrent publish = %v %q (%v), want only ordering-b registration",
			published,
			concurrent,
			err,
		)
	}
	close(release)
	if result := <-firstResult; result.err != nil || !result.published {
		t.Fatalf("first publish = %v (%v)", result.published, result.err)
	}
	var remaining []string
	if published, err := second.PublishNext(ctx, recordIntent(&remaining)); err != nil ||
		!published {
		t.Fatalf("remaining publish = %v (%v)", published, err)
	}
	if held != "ordering-a policy-registered" ||
		!slices.Equal(remaining, []string{"ordering-a policy-terminated"}) {
		t.Errorf(
			"held %q then remaining %q, want registration before termination",
			held,
			remaining,
		)
	}
}

func openOutboxStore(
	t *testing.T,
	ctx context.Context,
	fixture *postgresfixture.Fixture,
) *postgres.Store {
	t.Helper()
	pool, err := postgres.OpenPool(ctx, postgres.PoolSettings{
		URL:            fixture.WriterURL("policy"),
		MaxConnections: 2,
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	store, err := postgres.NewStore(pool)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func recordIntent(
	names *[]string,
) func(context.Context, application.EventIntent) error {
	return func(_ context.Context, intent application.EventIntent) error {
		*names = append(*names, intentName(intent))
		return nil
	}
}

func intentName(intent application.EventIntent) string {
	return intent.Key + " " + intent.Topic
}
