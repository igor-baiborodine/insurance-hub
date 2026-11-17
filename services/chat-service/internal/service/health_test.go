package service_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"
)

func TestManagementHealthReflectsLifecycleState(t *testing.T) {
	state, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	handler := health.NewHandler(state)
	assertHealth(t, handler, "/livez", http.StatusOK, "live\n")
	assertHealth(t, handler, "/readyz", http.StatusServiceUnavailable, "not ready\n")
	state.SetReady(true)
	assertHealth(t, handler, "/readyz", http.StatusOK, "ready\n")
	state.SetReady(false)
	assertHealth(t, handler, "/readyz", http.StatusServiceUnavailable, "not ready\n")
}

func TestManagementHealthBoundsDependencyCheck(t *testing.T) {
	state, err := health.NewState(10*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("dependency unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)
	assertHealth(t, health.NewHandler(state), "/readyz", http.StatusServiceUnavailable, "not ready\n")
}

func assertHealth(t *testing.T, handler http.Handler, path string, status int, body string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != status || recorder.Body.String() != body {
		t.Errorf("%s = (%d, %q), want (%d, %q)", path, recorder.Code, recorder.Body.String(), status, body)
	}
}
