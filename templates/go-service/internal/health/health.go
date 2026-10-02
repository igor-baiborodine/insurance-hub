// Package health exposes lifecycle-only HTTP liveness and readiness probes.
package health

import (
	"net/http"
	"sync/atomic"
)

const (
	liveResponse     = "live\n"
	readyResponse    = "ready\n"
	notReadyResponse = "not ready\n"
)

// State tracks whether the service currently accepts application work.
type State struct {
	ready atomic.Bool
}

// SetReady changes the readiness state safely across serving and shutdown goroutines.
func (state *State) SetReady(ready bool) {
	state.ready.Store(ready)
}

// Ready reports the current readiness state.
func (state *State) Ready() bool {
	return state.ready.Load()
}

// NewHandler returns management-only HTTP routes with constant, non-sensitive responses.
func NewHandler(state *State) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /livez", func(writer http.ResponseWriter, _ *http.Request) {
		writeResponse(writer, http.StatusOK, liveResponse)
	})
	mux.HandleFunc("GET /readyz", func(writer http.ResponseWriter, _ *http.Request) {
		if !state.Ready() {
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
