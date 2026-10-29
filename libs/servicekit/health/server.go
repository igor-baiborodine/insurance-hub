package health

import (
	"net/http"
	"time"
)

// NewManagementServer returns an unstarted server with the shared probe routes,
// a five-second header timeout, and a thirty-second idle timeout. The caller
// must supply a nonnil state and owns listener exposure, serving, readiness
// changes, and shutdown. Read and write timeouts remain zero by default; callers
// may change the server's timeout fields before serving.
func NewManagementServer(state *State) *http.Server {
	return &http.Server{
		Handler:           NewHandler(state),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
}
