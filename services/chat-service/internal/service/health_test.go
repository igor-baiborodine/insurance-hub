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

func TestManagementHealth_ReflectsLifecycleState(t *testing.T) {
	// given
	state, err := health.NewState(time.Second, func(context.Context) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	handler := health.NewHandler(state)

	// when
	live := requestHealth(handler, "/livez")
	initial := requestHealth(handler, "/readyz")
	state.SetReady(true)
	ready := requestHealth(handler, "/readyz")
	state.SetReady(false)
	withdrawn := requestHealth(handler, "/readyz")

	// then
	assertHealth(t, "/livez", live, http.StatusOK, "live\n")
	assertHealth(t, "/readyz initially", initial, http.StatusServiceUnavailable, "not ready\n")
	assertHealth(t, "/readyz ready", ready, http.StatusOK, "ready\n")
	assertHealth(
		t,
		"/readyz withdrawn",
		withdrawn,
		http.StatusServiceUnavailable,
		"not ready\n",
	)
}

func TestManagementHealth_BoundsDependencyCheck(t *testing.T) {
	// given
	state, err := health.NewState(10*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("dependency unavailable")
	})
	if err != nil {
		t.Fatal(err)
	}
	state.SetReady(true)

	// when
	response := requestHealth(health.NewHandler(state), "/readyz")

	// then
	assertHealth(t, "/readyz", response, http.StatusServiceUnavailable, "not ready\n")
}

type healthResponse struct {
	status int
	body   string
}

func requestHealth(handler http.Handler, path string) healthResponse {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return healthResponse{status: recorder.Code, body: recorder.Body.String()}
}

func assertHealth(
	t *testing.T,
	scenario string,
	got healthResponse,
	wantStatus int,
	wantBody string,
) {
	t.Helper()
	if got.status != wantStatus || got.body != wantBody {
		t.Errorf(
			"%s = (%d, %q), want (%d, %q)",
			scenario,
			got.status,
			got.body,
			wantStatus,
			wantBody,
		)
	}
}
