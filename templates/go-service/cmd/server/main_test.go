package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestShutdownContextHandlesSIGTERM(t *testing.T) {
	ctx, stop := shutdownContext(context.Background())
	defer stop()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("SIGTERM did not cancel the shutdown context")
	}
}
