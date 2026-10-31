// Package policyhttp owns the direct policy-search HTTP transport boundary.
package policyhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

// SearchPolicies is the application use case exposed at the direct HTTP boundary.
type SearchPolicies func(context.Context, string) ([]domain.Policy, error)

// Settings contains request bounds and optional request observation.
type Settings struct {
	RequestTimeout time.Duration
	Observer       *telemetry.RequestObserver
}

// NewHandler constructs the direct GET /policies route.
func NewHandler(
	logger *slog.Logger,
	settings Settings,
	search SearchPolicies,
) (http.Handler, error) {
	if logger == nil {
		return nil, errors.New("create policy search HTTP handler: logger is required")
	}
	if settings.RequestTimeout <= 0 {
		return nil, errors.New(
			"create policy search HTTP handler: request timeout must be positive",
		)
	}
	if search == nil {
		return nil, errors.New(
			"create policy search HTTP handler: search handler is required",
		)
	}
	return http.HandlerFunc((&handler{
		requestTimeout: settings.RequestTimeout,
		search:         search,
		observer:       settings.Observer,
	}).serveHTTP), nil
}

type handler struct {
	requestTimeout time.Duration
	search         SearchPolicies
	observer       *telemetry.RequestObserver
}

func (handler *handler) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.EscapedPath()
	selected := path == "/policies" || path == "/policies/"
	if selected && handler.observer != nil {
		var state *telemetry.Request
		request, state = handler.observer.StartHTTP(request)
		statusWriter := &statusResponseWriter{ResponseWriter: writer}
		defer func() {
			if err := request.Context().Err(); err != nil {
				telemetry.MarkError(request.Context(), err)
			} else if statusWriter.writeErr != nil {
				telemetry.MarkError(request.Context(), statusWriter.writeErr)
			}
			state.Finish(request.Context(), statusWriter.status)
		}()
		writer = statusWriter
	}
	if !selected {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	values, present := request.URL.Query()["q"]
	if !present || len(values) == 0 {
		writeJSON(writer, http.StatusBadRequest, errorBody{
			Message: "Required QueryValue [q] not specified",
			Path:    "/queryText",
			Links:   links{Self: link{Href: selfHref(request)}},
		})
		return
	}

	ctx, cancel := context.WithTimeout(request.Context(), handler.requestTimeout)
	defer cancel()
	policies, err := handler.search(ctx, values[0])
	if err != nil {
		telemetry.MarkError(request.Context(), err)
		if errors.Is(err, context.Canceled) && request.Context().Err() != nil {
			return
		}
		writeJSON(writer, http.StatusInternalServerError, errorBody{
			Message: "Internal Server Error",
			Links:   links{Self: link{Href: selfHref(request)}},
		})
		return
	}
	if err := ctx.Err(); err != nil {
		telemetry.MarkError(request.Context(), err)
		if errors.Is(err, context.Canceled) && request.Context().Err() != nil {
			return
		}
		writeJSON(writer, http.StatusInternalServerError, errorBody{
			Message: "Internal Server Error",
			Links:   links{Self: link{Href: selfHref(request)}},
		})
		return
	}

	response := searchResponse{}
	if len(policies) > 0 {
		response.Policies = make([]policyDTO, 0, len(policies))
	}
	for _, policy := range policies {
		if err := ctx.Err(); err != nil {
			telemetry.MarkError(request.Context(), err)
			writeJSON(writer, http.StatusInternalServerError, errorBody{
				Message: "Internal Server Error",
				Links:   links{Self: link{Href: selfHref(request)}},
			})
			return
		}
		mapped := policyDTO{
			Number:       policy.Number,
			PolicyHolder: policy.PolicyHolder,
		}
		if policy.DateFrom != nil {
			mapped.DateFrom = policy.DateFrom.String()
		}
		if policy.DateTo != nil {
			mapped.DateTo = policy.DateTo.String()
		}
		response.Policies = append(response.Policies, mapped)
	}
	writeJSON(writer, http.StatusOK, response)
}

type searchResponse struct {
	Policies []policyDTO `json:"policies,omitempty"`
}

type policyDTO struct {
	Number       string `json:"number,omitempty"`
	DateFrom     string `json:"dateFrom,omitempty"`
	DateTo       string `json:"dateTo,omitempty"`
	PolicyHolder string `json:"policyHolder,omitempty"`
}

type link struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated"`
}

type links struct {
	Self link `json:"self"`
}

type errorBody struct {
	Message string `json:"message"`
	Path    string `json:"path,omitempty"`
	Links   links  `json:"_links"`
}

func selfHref(request *http.Request) string {
	path := request.URL.EscapedPath()
	if path == "/policies/" {
		path = "/policies"
	}
	if request.URL.RawQuery != "" {
		return path + "?" + request.URL.RawQuery
	}
	return path
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		body = []byte(`{"message":"Internal Server Error"}`)
		status = http.StatusInternalServerError
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

type statusResponseWriter struct {
	http.ResponseWriter
	status   int
	writeErr error
}

func (writer *statusResponseWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusResponseWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.status = http.StatusOK
	}
	written, err := writer.ResponseWriter.Write(body)
	if writer.writeErr == nil {
		switch {
		case err != nil:
			writer.writeErr = err
		case written < len(body):
			writer.writeErr = io.ErrShortWrite
		}
	}
	return written, err
}
