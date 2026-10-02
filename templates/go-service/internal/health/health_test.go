package health_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/health"
)

func TestHandlerReflectsLifecycleState(t *testing.T) {
	state := new(health.State)
	handler := health.NewHandler(state)

	assertResponse(t, handler, http.MethodGet, "/livez", http.StatusOK, "live\n")
	assertResponse(
		t,
		handler,
		http.MethodGet,
		"/readyz",
		http.StatusServiceUnavailable,
		"not ready\n",
	)

	state.SetReady(true)
	assertResponse(t, handler, http.MethodGet, "/readyz", http.StatusOK, "ready\n")

	state.SetReady(false)
	assertResponse(
		t,
		handler,
		http.MethodGet,
		"/readyz",
		http.StatusServiceUnavailable,
		"not ready\n",
	)
	assertResponse(
		t,
		handler,
		http.MethodPost,
		"/livez",
		http.StatusMethodNotAllowed,
		"Method Not Allowed\n",
	)
	assertResponse(t, handler, http.MethodGet, "/unknown", http.StatusNotFound, "404 page not found\n")
}

func assertResponse(
	t *testing.T,
	handler http.Handler,
	method string,
	path string,
	wantStatus int,
	wantBody string,
) {
	t.Helper()
	request := httptest.NewRequest(method, path, nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	response := recorder.Result()
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if response.StatusCode != wantStatus || string(body) != wantBody {
		t.Errorf(
			"%s %s = (%d, %q), want (%d, %q)",
			method,
			path,
			response.StatusCode,
			body,
			wantStatus,
			wantBody,
		)
	}
}
