package health_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"
)

func TestNewManagementServer_DefaultsAndProbeRoutes(t *testing.T) {
	// given
	checks := 0
	state, err := health.NewState(time.Second, func(context.Context) error {
		checks++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// when
	server := health.NewManagementServer(state)

	// then
	if server == nil || server.Handler == nil {
		t.Fatal("management server has no probe handler")
	}
	if server.Addr != "" || server.ReadHeaderTimeout != 5*time.Second ||
		server.IdleTimeout != 30*time.Second || server.ReadTimeout != 0 ||
		server.WriteTimeout != 0 {
		t.Errorf(
			"server defaults = (addr %q, header %s, idle %s, read %s, write %s)",
			server.Addr,
			server.ReadHeaderTimeout,
			server.IdleTimeout,
			server.ReadTimeout,
			server.WriteTimeout,
		)
	}
	if state.Ready() {
		t.Error("constructor changed readiness")
	}

	for _, test := range []struct {
		name       string
		method     string
		path       string
		ready      bool
		wantStatus int
		wantBody   string
		wantChecks int
	}{
		{name: "live before startup", method: http.MethodGet, path: "/livez", wantStatus: http.StatusOK, wantBody: "live\n"},
		{name: "ready before startup", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusServiceUnavailable, wantBody: "not ready\n"},
		{name: "ready after startup", method: http.MethodGet, path: "/readyz", ready: true, wantStatus: http.StatusOK, wantBody: "ready\n", wantChecks: 1},
		{name: "unknown route", method: http.MethodGet, path: "/products", ready: true, wantStatus: http.StatusNotFound, wantBody: "404 page not found\n"},
		{name: "unsupported method", method: http.MethodPost, path: "/readyz", ready: true, wantStatus: http.StatusMethodNotAllowed, wantBody: "Method Not Allowed\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			state.SetReady(test.ready)
			checks = 0
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)

			// when
			server.Handler.ServeHTTP(recorder, request)

			// then
			if recorder.Code != test.wantStatus ||
				recorder.Body.String() != test.wantBody {
				t.Errorf(
					"response = (%d, %q), want (%d, %q)",
					recorder.Code,
					recorder.Body.String(),
					test.wantStatus,
					test.wantBody,
				)
			}
			if checks != test.wantChecks {
				t.Errorf("dependency checks = %d, want %d", checks, test.wantChecks)
			}
		})
	}
}

func TestNewManagementServer_ReturnsFreshConfigurableServer(t *testing.T) {
	// given
	state, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}

	// when
	first := health.NewManagementServer(state)
	second := health.NewManagementServer(state)
	first.ReadHeaderTimeout = 2 * time.Second
	first.IdleTimeout = 10 * time.Second

	// then
	if first == second || second.ReadHeaderTimeout != 5*time.Second ||
		second.IdleTimeout != 30*time.Second {
		t.Error("timeout override changed another management server")
	}
}
