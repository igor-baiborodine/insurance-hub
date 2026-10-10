package health_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/health"
)

func TestHandler_ReflectsLifecycleState(t *testing.T) {
	// given
	var dependencyReady atomic.Bool
	state, err := health.NewState(time.Second, func(context.Context) error {
		if !dependencyReady.Load() {
			return errors.New("dependency unavailable")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	handler := health.NewHandler(state)
	tests := []struct {
		name            string
		ready           bool
		dependencyReady bool
		method          string
		path            string
		wantStatus      int
		wantBody        string
	}{
		{
			name:       "live",
			method:     http.MethodGet,
			path:       "/livez",
			wantStatus: http.StatusOK,
			wantBody:   "live\n",
		},
		{
			name:       "not ready before startup",
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "not ready\n",
		},
		{
			name:            "ready after startup",
			ready:           true,
			dependencyReady: true,
			method:          http.MethodGet,
			path:            "/readyz",
			wantStatus:      http.StatusOK,
			wantBody:        "ready\n",
		},
		{
			name:       "not ready during dependency loss",
			ready:      true,
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "not ready\n",
		},
		{
			name:       "not ready after withdrawal",
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "not ready\n",
		},
		{
			name:       "unsupported method",
			method:     http.MethodPost,
			path:       "/livez",
			wantStatus: http.StatusMethodNotAllowed,
			wantBody:   "Method Not Allowed\n",
		},
		{
			name:       "unknown path",
			method:     http.MethodGet,
			path:       "/unknown",
			wantStatus: http.StatusNotFound,
			wantBody:   "404 page not found\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// given
			state.SetReady(test.ready)
			dependencyReady.Store(test.dependencyReady)
			request := httptest.NewRequest(test.method, test.path, nil)
			recorder := httptest.NewRecorder()

			// when
			handler.ServeHTTP(recorder, request)

			// then
			response := recorder.Result()
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if response.StatusCode != test.wantStatus || string(body) != test.wantBody {
				t.Errorf(
					"response = (%d, %q), want (%d, %q)",
					response.StatusCode,
					body,
					test.wantStatus,
					test.wantBody,
				)
			}
		})
	}
}

func TestReadinessCheck_UsesProbeDeadline(t *testing.T) {
	// given
	const timeout = 20 * time.Millisecond
	deadlineObserved := make(chan time.Duration, 1)
	state, err := health.NewState(timeout, func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		if !ok {
			return errors.New("probe has no deadline")
		}
		deadlineObserved <- time.Until(deadline)
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	recorder := httptest.NewRecorder()

	// when
	health.NewHandler(state).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/readyz", nil),
	)

	// then
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("readiness status = %d", recorder.Code)
	}
	observed := <-deadlineObserved
	if observed <= 0 || observed > timeout {
		t.Errorf("probe deadline = %s, want within %s", observed, timeout)
	}
}

func TestNewState_RejectsInvalidDependencies(t *testing.T) {
	// given
	checks := []struct {
		timeout time.Duration
		check   health.Check
	}{
		{timeout: 0, check: func(context.Context) error { return nil }},
		{timeout: time.Second},
	}

	for _, check := range checks {
		// when
		state, err := health.NewState(check.timeout, check.check)

		// then
		if err == nil || state != nil {
			t.Errorf("NewState() = (%v, %v), want (nil, error)", state, err)
		}
	}
}
