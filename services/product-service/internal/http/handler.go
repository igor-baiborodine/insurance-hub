// Package producthttp owns the direct Product HTTP transport boundary.
package producthttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

type (
	ListProducts func(context.Context) ([]domain.Product, error)
	GetProduct   func(context.Context, string) (domain.Product, error)
)

type Settings struct {
	RequestTimeout time.Duration
	Observer       *telemetry.RequestObserver
}

// NewHandler constructs the direct Product HTTP routes.
func NewHandler(
	logger *slog.Logger,
	settings Settings,
	listProducts ListProducts,
	getProduct GetProduct,
) (http.Handler, error) {
	if logger == nil {
		return nil, errors.New("create Product HTTP handler: logger is required")
	}
	if settings.RequestTimeout <= 0 {
		return nil, errors.New(
			"create Product HTTP handler: request timeout must be positive",
		)
	}
	if listProducts == nil || getProduct == nil {
		return nil, errors.New("create Product HTTP handler: catalog handlers are required")
	}

	handler := &catalogHandler{
		requestTimeout: settings.RequestTimeout,
		listProducts:   listProducts,
		getProduct:     getProduct,
		observer:       settings.Observer,
	}
	return http.HandlerFunc(handler.serveHTTP), nil
}

type catalogHandler struct {
	requestTimeout time.Duration
	listProducts   ListProducts
	getProduct     GetProduct
	observer       *telemetry.RequestObserver
}

func (handler *catalogHandler) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.EscapedPath()
	var operation, route string
	if path == "/products" || path == "/products/" {
		operation, route = telemetry.OperationList, "/products"
	} else if strings.HasPrefix(path, "/products/") {
		rawCode := strings.TrimPrefix(path, "/products/")
		if rawCode != "" && !strings.Contains(rawCode, "/") {
			operation, route = telemetry.OperationGet, "/products/{productCode}"
		}
	}
	if operation != "" && handler.observer != nil {
		var state *telemetry.Request
		request, state = handler.observer.StartHTTP(request, operation, route)
		statusWriter := &statusResponseWriter{ResponseWriter: writer}
		defer func() {
			if err := request.Context().Err(); err != nil {
				state.SetError(err)
			} else if statusWriter.writeErr != nil {
				state.SetError(statusWriter.writeErr)
			}
			status := ""
			if statusWriter.status != 0 {
				status = strconv.Itoa(statusWriter.status)
			}
			state.Finish(request.Context(), "http.response.status_code", status)
		}()
		writer = statusWriter
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if path == "/products" || path == "/products/" {
		handler.list(writer, request)
		return
	}
	if !strings.HasPrefix(path, "/products/") {
		WriteNotFound(writer, request)
		return
	}

	rawCode := strings.TrimPrefix(path, "/products/")
	if rawCode == "" || strings.Contains(rawCode, "/") {
		WriteNotFound(writer, request)
		return
	}
	code, err := url.PathUnescape(rawCode)
	if err != nil {
		writeJSONError(writer, http.StatusBadRequest, "Malformed URI", "/")
		return
	}
	handler.get(writer, request, code)
}

func (handler *catalogHandler) list(writer http.ResponseWriter, request *http.Request) {
	ctx, cancel := context.WithTimeout(request.Context(), handler.requestTimeout)
	defer cancel()
	products, err := handler.listProducts(ctx)
	if err != nil {
		handler.writeApplicationError(writer, request, err)
		return
	}

	mapped := make([]productDTO, 0, len(products))
	for _, product := range products {
		if err := ctx.Err(); err != nil {
			handler.writeApplicationError(writer, request, err)
			return
		}
		value, mapErr := mapProduct(ctx, product)
		if mapErr != nil {
			handler.writeApplicationError(writer, request, mapErr)
			return
		}
		mapped = append(mapped, value)
	}
	handler.writeSuccess(ctx, writer, request, mapped)
}

func (handler *catalogHandler) get(
	writer http.ResponseWriter,
	request *http.Request,
	code string,
) {
	ctx, cancel := context.WithTimeout(request.Context(), handler.requestTimeout)
	defer cancel()
	product, err := handler.getProduct(ctx, code)
	if err != nil {
		handler.writeApplicationError(writer, request, err)
		return
	}
	mapped, err := mapProduct(ctx, product)
	if err != nil {
		handler.writeApplicationError(writer, request, err)
		return
	}
	handler.writeSuccess(ctx, writer, request, mapped)
}

func (handler *catalogHandler) writeSuccess(
	ctx context.Context,
	writer http.ResponseWriter,
	request *http.Request,
	value any,
) {
	if err := ctx.Err(); err != nil {
		handler.writeApplicationError(writer, request, err)
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		handler.writeApplicationError(writer, request, err)
		return
	}
	if err := ctx.Err(); err != nil {
		handler.writeApplicationError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, body)
}

func (handler *catalogHandler) writeApplicationError(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) {
	if errors.Is(err, application.ErrProductNotFound) {
		telemetry.MarkError(request.Context(), err)
		WriteNotFound(writer, request)
		return
	}
	if errors.Is(err, errInvalidDomainProduct) {
		telemetry.MarkError(request.Context(), application.ErrInvalidDefinition)
	} else {
		telemetry.MarkError(request.Context(), err)
	}
	if errors.Is(err, context.Canceled) && request.Context().Err() != nil {
		return
	}
	writeJSON(
		writer,
		http.StatusInternalServerError,
		[]byte(`{"message":"Internal Server Error"}`),
	)
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

// WriteNotFound preserves the incoming encoded path in the legacy self link.
func WriteNotFound(writer http.ResponseWriter, request *http.Request) {
	writeJSONError(writer, http.StatusNotFound, "Page Not Found", request.URL.EscapedPath())
}

func writeJSONError(writer http.ResponseWriter, status int, message, href string) {
	writeJSON(writer, status, jsonErrorBody(message, href))
}

func writeJSON(writer http.ResponseWriter, status int, body []byte) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}

type errorLink struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated"`
}

type errorBody struct {
	Message string `json:"message"`
	Links   struct {
		Self errorLink `json:"self"`
	} `json:"_links"`
}

func jsonErrorBody(message, href string) []byte {
	response := errorBody{Message: message}
	response.Links.Self = errorLink{Href: href}
	body, _ := json.Marshal(response)
	return body
}
