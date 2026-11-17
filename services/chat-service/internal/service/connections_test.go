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

func TestConnectionRegistryDrainClosesJoinsAndRejectsRegistration(t *testing.T) {
	registry := NewConnectionRegistry()
	connection := &fakeConnection{}
	if err := registry.Register(connection); err != nil {
		t.Fatal(err)
	}
	if err := registry.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if connection.closed.Load() != 1 {
		t.Errorf("close count = %d", connection.closed.Load())
	}
	if !registry.Draining() {
		t.Error("registry did not enter drain")
	}
	if err := registry.Register(&fakeConnection{}); !errors.Is(err, errConnectionDrainStarted) {
		t.Fatalf("register during drain = %v", err)
	}
}

func TestConnectionRegistryDrainBoundsBlockedJoinAndPreservesErrors(t *testing.T) {
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
	err := registry.Drain(ctx)
	if !errors.Is(err, closeErr) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain error = %v", err)
	}
	if connection.closed.Load() != 1 {
		t.Errorf("close count = %d", connection.closed.Load())
	}
}
