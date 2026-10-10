// Package producthttp owns the direct Product HTTP transport boundary.
package producthttp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
)

type (
	ListProducts func(context.Context) ([]domain.Product, error)
	GetProduct   func(context.Context, string) (domain.Product, error)
)

type Settings struct {
	RequestTimeout time.Duration
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
		logger:         logger,
		requestTimeout: settings.RequestTimeout,
		listProducts:   listProducts,
		getProduct:     getProduct,
	}
	return http.HandlerFunc(handler.serveHTTP), nil
}

type catalogHandler struct {
	logger         *slog.Logger
	requestTimeout time.Duration
	listProducts   ListProducts
	getProduct     GetProduct
}

func (handler *catalogHandler) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	path := request.URL.EscapedPath()
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
		WriteNotFound(writer, request)
		return
	}
	if errors.Is(err, context.Canceled) && request.Context().Err() != nil {
		return
	}
	handler.logger.ErrorContext(
		request.Context(),
		"Product HTTP request failed",
		slog.String("route", routeName(request.URL.EscapedPath())),
	)
	writeJSON(
		writer,
		http.StatusInternalServerError,
		[]byte(`{"message":"Internal Server Error"}`),
	)
}

func routeName(path string) string {
	if path == "/products" || path == "/products/" {
		return "list-products"
	}
	return "get-product"
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
