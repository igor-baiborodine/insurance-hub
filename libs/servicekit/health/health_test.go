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

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"
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

func TestReadinessCheck_RejectsWithdrawalDuringProbe(t *testing.T) {
	// given
	started := make(chan struct{})
	release := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	state, err := health.NewState(time.Second, func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	handler := health.NewHandler(state)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("readiness handler did not stop")
		}
	})
	go func() {
		handler.ServeHTTP(
			recorder,
			httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil),
		)
		close(done)
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("dependency probe did not start")
	}
	// when
	state.SetReady(false)
	close(release)
	// then
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("readiness request did not finish")
	}
	if recorder.Code != http.StatusServiceUnavailable ||
		recorder.Body.String() != "not ready\n" {
		t.Errorf("withdrawn readiness = (%d, %q)", recorder.Code, recorder.Body.String())
	}
	live := httptest.NewRecorder()
	handler.ServeHTTP(live, httptest.NewRequest(http.MethodGet, "/livez", nil))
	if live.Code != http.StatusOK {
		t.Errorf("liveness = %d", live.Code)
	}
}

func TestReadinessCheck_RejectsSuccessAfterCancellation(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	state, err := health.NewState(time.Second, func(context.Context) error {
		cancel()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	recorder := httptest.NewRecorder()
	// when
	health.NewHandler(state).
		ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
	// then
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("cancelled readiness = %d", recorder.Code)
	}
}

func TestHandler_RealHTTPKeepsResponsesConstantAndUncached(t *testing.T) {
	// given
	state, err := health.NewState(
		time.Second,
		func(context.Context) error { return errors.New("secret-marker database credentials") },
	)
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	server := httptest.NewServer(health.NewHandler(state))
	t.Cleanup(server.Close)
	client := server.Client()
	client.Timeout = time.Second
	for _, test := range []struct {
		path   string
		status int
		body   string
	}{
		{"/livez", http.StatusOK, "live\n"}, {"/readyz", http.StatusServiceUnavailable, "not ready\n"},
	} {
		t.Run(test.path, func(t *testing.T) {
			// when
			response, err := client.Get(server.URL + test.path)
			// then
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.status || string(body) != test.body {
				t.Fatalf("response = %d %q", response.StatusCode, body)
			}
			if response.Header.Get("Cache-Control") != "no-store" ||
				response.Header.Get("Content-Type") != "text/plain; charset=utf-8" {
				t.Fatalf("unsafe response headers: %v", response.Header)
			}
		})
	}
}

func TestReadinessCheck_PreservesShorterCallerDeadline(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	wantDeadline, _ := ctx.Deadline()
	var gotDeadline time.Time
	state, err := health.NewState(time.Second, func(ctx context.Context) error {
		gotDeadline, _ = ctx.Deadline()
		<-ctx.Done()
		return nil // Even a dependency returning success after its deadline is not ready.
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	recorder := httptest.NewRecorder()
	// when
	health.NewHandler(state).
		ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
	// then
	if !gotDeadline.Equal(wantDeadline) {
		t.Fatalf("deadline = %v, want %v", gotDeadline, wantDeadline)
	}
	if recorder.Code != http.StatusServiceUnavailable ||
		recorder.Body.String() != "not ready\n" {
		t.Fatalf("expired readiness = %d %q", recorder.Code, recorder.Body.String())
	}
}

func TestReadinessCheck_SkipsDependencyBeforeStartupAndAfterCancellation(t *testing.T) {
	// given
	for _, ready := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		state, err := health.NewState(
			time.Second,
			func(context.Context) error { calls++; return nil },
		)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		state.SetReady(ready)
		if ready {
			cancel()
		}
		recorder := httptest.NewRecorder()
		// when
		health.NewHandler(state).
			ServeHTTP(recorder, httptest.NewRequestWithContext(ctx, http.MethodGet, "/readyz", nil))
		cancel()
		// then
		if calls != 0 || recorder.Code != http.StatusServiceUnavailable {
			t.Fatalf("dependency calls = %d, response = %d", calls, recorder.Code)
		}
	}
}
