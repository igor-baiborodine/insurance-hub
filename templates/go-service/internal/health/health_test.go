package health_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/health"
)

func TestHandler_ReflectsLifecycleState(t *testing.T) {
	// given
	state := new(health.State)
	handler := health.NewHandler(state)
	tests := []struct {
		name       string
		ready      bool
		method     string
		path       string
		wantStatus int
		wantBody   string
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
			name:       "ready after startup",
			ready:      true,
			method:     http.MethodGet,
			path:       "/readyz",
			wantStatus: http.StatusOK,
			wantBody:   "ready\n",
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
