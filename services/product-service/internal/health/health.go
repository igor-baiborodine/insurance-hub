// Package health exposes lifecycle-only HTTP liveness and readiness probes.
package health

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

const (
	liveResponse     = "live\n"
	readyResponse    = "ready\n"
	notReadyResponse = "not ready\n"
)

// Check verifies one runtime readiness dependency without changing it.
type Check func(context.Context) error

// State combines process lifecycle readiness with a bounded dependency check.
type State struct {
	ready   atomic.Bool
	timeout time.Duration
	check   Check
}

// NewState constructs dependency-aware readiness state.
func NewState(timeout time.Duration, check Check) (*State, error) {
	if timeout <= 0 {
		return nil, errors.New("create health state: probe timeout must be positive")
	}
	if check == nil {
		return nil, errors.New("create health state: readiness check is required")
	}
	return &State{timeout: timeout, check: check}, nil
}

// SetReady changes the readiness state safely across serving and shutdown goroutines.
func (state *State) SetReady(ready bool) {
	state.ready.Store(ready)
}

// Ready reports the current readiness state.
func (state *State) Ready() bool {
	return state.ready.Load()
}

func (state *State) checkReady(ctx context.Context) bool {
	if !state.Ready() {
		return false
	}
	probeCtx, cancel := context.WithTimeout(ctx, state.timeout)
	defer cancel()
	return state.check(probeCtx) == nil
}

// NewHandler returns management-only HTTP routes with constant, non-sensitive responses.
func NewHandler(state *State) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(writer, http.StatusOK, liveResponse)
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, request *http.Request) {
		if !state.checkReady(request.Context()) {
			writeResponse(writer, http.StatusServiceUnavailable, notReadyResponse)
			return
		}
		writeResponse(writer, http.StatusOK, readyResponse)
	})
	return mux
}

func writeResponse(writer http.ResponseWriter, status int, body string) {
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_, _ = writer.Write([]byte(body))
}
