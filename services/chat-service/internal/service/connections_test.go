package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type fakeConnection struct {
	closed   atomic.Int32
	wait     func(context.Context) error
	closeErr error
}

func (connection *fakeConnection) Close() error {
	connection.closed.Add(1)
	return connection.closeErr
}

func (connection *fakeConnection) Wait(ctx context.Context) error {
	if connection.wait != nil {
		return connection.wait(ctx)
	}
	return nil
}

func TestConnectionRegistry_Drain_ClosesJoinsAndRejectsRegistration(t *testing.T) {
	// given
	registry := NewConnectionRegistry()
	connection := &fakeConnection{}
	if err := registry.Register(connection); err != nil {
		t.Fatal(err)
	}

	// when
	if err := registry.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	registrationErr := registry.Register(&fakeConnection{})

	// then
	if connection.closed.Load() != 1 {
		t.Errorf("close count = %d", connection.closed.Load())
	}
	if !registry.Draining() {
		t.Error("registry did not enter drain")
	}
	if !errors.Is(registrationErr, errConnectionDrainStarted) {
		t.Fatalf("register during drain = %v", registrationErr)
	}
}

func TestConnectionRegistry_Drain_BoundsBlockedJoinAndPreservesErrors(t *testing.T) {
	// given
	closeErr := errors.New("close failed")
	registry := NewConnectionRegistry()
	connection := &fakeConnection{
		closeErr: closeErr,
		wait: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	if err := registry.Register(connection); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	// when
	err := registry.Drain(ctx)

	// then
	if !errors.Is(err, closeErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error = %v", err)
	}
	if connection.closed.Load() != 1 {
		t.Errorf("close count = %d", connection.closed.Load())
	}
}
