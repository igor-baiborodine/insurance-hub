// Package gateway owns the generated Product HTTP-to-gRPC boundary.
package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

type Settings struct {
	RequestTimeout time.Duration
	Observer       *telemetry.RequestObserver
}

// NewHandler registers the generated Product client while retaining the accepted HTTP contract.
func NewHandler(client productv1.ProductServiceClient, settings Settings) (http.Handler, error) {
	if client == nil {
		return nil, errors.New("create Product gateway: client is required")
	}
	if settings.RequestTimeout <= 0 {
		return nil, errors.New("create Product gateway: request timeout must be positive")
	}
	marshaler := newLegacyMarshaler()
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, marshaler),
		runtime.WithIncomingHeaderMatcher(denyHeader),
		runtime.WithOutgoingHeaderMatcher(denyHeader),
		runtime.WithOutgoingTrailerMatcher(denyHeader),
		runtime.WithErrorHandler(safeGatewayError),
		runtime.WithUnescapingMode(runtime.UnescapingModeAllExceptReserved),
		runtime.WithDisableHTTPMethodOverride(),
		runtime.WithDisablePathLengthFallback(),
	)
	if err := productv1.RegisterProductServiceHandlerClient(
		context.Background(), mux, decodedPathClient{ProductServiceClient: client},
	); err != nil {
		return nil, errors.New("create Product gateway: register generated handler")
	}
	return &catalogHandler{mux: mux, settings: settings}, nil
}

// NewUnavailableHandler creates the stable response used after HTTP draining begins.
func NewUnavailableHandler(settings Settings) (http.Handler, error) {
	if settings.RequestTimeout <= 0 {
		return nil, errors.New(
			"create unavailable Product gateway: request timeout must be positive",
		)
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writeJSON(
			writer,
			http.StatusServiceUnavailable,
			[]byte(`{"message":"Service Unavailable"}`),
		)
	}), nil
}

type catalogHandler struct {
	mux      http.Handler
	settings Settings
}

type (
	gatewayCodeKey   struct{}
	callerContextKey struct{}
)

type decodedPathClient struct{ productv1.ProductServiceClient }

func (client decodedPathClient) GetProduct(
	ctx context.Context,
	request *productv1.GetProductRequest,
	options ...grpc.CallOption,
) (*productv1.GetProductResponse, error) {
	if code, ok := ctx.Value(gatewayCodeKey{}).(string); ok {
		request = &productv1.GetProductRequest{Code: code}
	}
	return client.ProductServiceClient.GetProduct(ctx, request, options...)
}

func (handler *catalogHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	path := request.URL.EscapedPath()
	operation, route := classify(path)
	if operation != "" && handler.settings.Observer != nil {
		var state *telemetry.Request
		request, state = handler.settings.Observer.StartHTTP(request, operation, route)
		statusWriter := &statusResponseWriter{ResponseWriter: writer}
		defer func() {
			if err := request.Context().Err(); err != nil {
				state.SetError(err)
			} else if statusWriter.writeErr != nil {
				state.SetError(statusWriter.writeErr)
			}
			statusCode := ""
			if statusWriter.status != 0 {
				statusCode = strconv.Itoa(statusWriter.status)
			}
			state.Finish(request.Context(), "http.response.status_code", statusCode)
		}()
		writer = statusWriter
	}
	if request.Method != http.MethodGet {
		writer.Header().Set("Allow", http.MethodGet)
		http.Error(writer, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if operation == "" {
		WriteNotFound(writer, request)
		return
	}
	if operation == telemetry.OperationGet {
		rawCode := strings.TrimPrefix(path, "/products/")
		code, err := url.PathUnescape(rawCode)
		if err != nil {
			writeJSONError(writer, http.StatusBadRequest, "Malformed URI", "/")
			return
		}
		request = request.WithContext(
			context.WithValue(request.Context(), gatewayCodeKey{}, code),
		)
	}

	request = sanitizeRequest(request)
	if path == "/products/" {
		request.URL.Path = "/products"
		request.URL.RawPath = ""
	}
	callerCtx := request.Context()
	requestCtx, cancel := context.WithTimeout(
		context.WithValue(callerCtx, callerContextKey{}, callerCtx),
		handler.settings.RequestTimeout,
	)
	defer cancel()
	handler.mux.ServeHTTP(writer, request.WithContext(requestCtx))
}

func classify(path string) (string, string) {
	if path == "/products" || path == "/products/" {
		return telemetry.OperationList, "/products"
	}
	if !strings.HasPrefix(path, "/products/") {
		return "", ""
	}
	rawCode := strings.TrimPrefix(path, "/products/")
	if rawCode == "" || strings.Contains(rawCode, "/") {
		return "", ""
	}
	return telemetry.OperationGet, "/products/{productCode}"
}

func sanitizeRequest(request *http.Request) *http.Request {
	request = request.Clone(request.Context())
	request.Header = request.Header.Clone()
	for name := range request.Header {
		lower := strings.ToLower(name)
		if lower == "authorization" || lower == "grpc-timeout" ||
			strings.HasPrefix(lower, "grpc-metadata-") || lower == "forwarded" ||
			lower == "x-forwarded-for" || lower == "x-forwarded-host" || lower == "x-real-ip" {
			request.Header.Del(name)
		}
	}
	urlCopy := *request.URL
	request.URL = &urlCopy
	request.URL.RawQuery = ""
	return request
}

func denyHeader(string) (string, bool) { return "", false }

// ClearApplicationMetadata removes gateway-created values before trace injection.
func ClearApplicationMetadata(ctx context.Context, method string, request, reply any,
	connection *grpc.ClientConn, invoker grpc.UnaryInvoker, options ...grpc.CallOption,
) error {
	return invoker(metadata.NewOutgoingContext(ctx, metadata.MD{}), method, request, reply,
		connection, options...)
}

func safeGatewayError(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler,
	writer http.ResponseWriter, request *http.Request, err error,
) {
	callerCtx, _ := request.Context().Value(callerContextKey{}).(context.Context)
	if callerCtx != nil && callerCtx.Err() != nil {
		return
	}
	telemetry.MarkError(request.Context(), err)
	if status.Code(err) == codes.NotFound {
		WriteNotFound(writer, request)
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

func (writer *statusResponseWriter) WriteHeader(statusCode int) {
	writer.status = statusCode
	writer.ResponseWriter.WriteHeader(statusCode)
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

func writeJSONError(writer http.ResponseWriter, statusCode int, message, href string) {
	writeJSON(writer, statusCode, jsonErrorBody(message, href))
}

func writeJSON(writer http.ResponseWriter, statusCode int, body []byte) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
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

type legacyMarshaler struct{ *runtime.JSONPb }

func newLegacyMarshaler() *legacyMarshaler {
	return &legacyMarshaler{JSONPb: &runtime.JSONPb{
		MarshalOptions:   protojson.MarshalOptions{},
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
	}}
}

func (marshaler *legacyMarshaler) Marshal(value any) ([]byte, error) {
	mapped, handled, err := mapGatewayResponse(context.Background(), value)
	if err != nil {
		return nil, err
	}
	if handled {
		return json.Marshal(mapped)
	}
	return marshaler.JSONPb.Marshal(value)
}

func (marshaler *legacyMarshaler) NewEncoder(writer io.Writer) runtime.Encoder {
	return encoderFunc(func(value any) error {
		data, err := marshaler.Marshal(value)
		if err != nil {
			return err
		}
		_, err = writer.Write(data)
		return err
	})
}

type encoderFunc func(any) error

func (encoder encoderFunc) Encode(value any) error { return encoder(value) }
