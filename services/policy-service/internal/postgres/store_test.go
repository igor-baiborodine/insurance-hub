package postgres_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
)

func TestStore_CreateOffer_ReportsUnreachableStorage(t *testing.T) {
	// given
	store := unreachableStore(t)

	// when
	err := store.CreateOffer(t.Context(), domain.Offer{Number: "offer-1"})

	// then
	if !errors.Is(err, application.ErrStorageUnavailable) {
		t.Fatalf("error = %v, want unavailable storage", err)
	}
}

func TestStore_CreateOffer_KeepsCancellationDistinct(t *testing.T) {
	// given
	store := unreachableStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	// when
	err := store.CreateOffer(ctx, domain.Offer{Number: "offer-1"})

	// then
	if !errors.Is(err, context.Canceled) || errors.Is(err, application.ErrStorageUnavailable) {
		t.Fatalf("error = %v, want cancellation only", err)
	}
}

// unreachableStore returns a store whose pool dials a loopback port with no listener.
func unreachableStore(t *testing.T) *postgres.Store {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	configuration, err := pgxpool.ParseConfig(
		"postgres://go_policy_writer:synthetic@" + address + "/policy?sslmode=disable",
	)
	if err != nil {
		t.Fatal(err)
	}
	configuration.ConnConfig.ConnectTimeout = time.Second
	pool, err := pgxpool.NewWithConfig(t.Context(), configuration)
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
