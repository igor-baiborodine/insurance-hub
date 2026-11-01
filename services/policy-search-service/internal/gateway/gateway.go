// Package gateway owns the strict public HTTP-to-gRPC boundary.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
)

const searchRoute = "GET /policies"

type Settings struct {
	RequestTimeout time.Duration
	MaxBodyBytes   int
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Observer       *telemetry.RequestObserver
}

// NewHandler registers the generated client while retaining the accepted legacy HTTP contract.
func NewHandler(
	client policysearchv1.PolicySearchServiceClient,
	settings Settings,
) (http.Handler, error) {
	if client == nil || !validSettings(settings) {
		return nil, errors.New("create gateway: all dependencies and limits are required")
	}
	marshaler := &compactJSON{
		JSONPb: runtime.JSONPb{
			MarshalOptions:   protojson.MarshalOptions{},
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
		},
	}
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, marshaler),
		runtime.WithIncomingHeaderMatcher(denyHeader),
		runtime.WithOutgoingHeaderMatcher(denyHeader),
		runtime.WithOutgoingTrailerMatcher(denyHeader),
		runtime.WithErrorHandler(safeGatewayError),
		runtime.WithDisableHTTPMethodOverride(),
		runtime.WithDisablePathLengthFallback(),
	)
	if err := policysearchv1.RegisterPolicySearchServiceHandlerClient(
		context.Background(),
		mux,
		client,
	); err != nil {
		return nil, errors.New("create gateway: register generated handler")
	}
	handler := &Handler{mux: mux, settings: settings}
	handler.search = handler.instrument(searchRoute, http.HandlerFunc(handler.serveSearch))
	handler.unmatched = handler.instrument(
		"unmatched",
		http.HandlerFunc(handler.serveUnmatched),
	)
	return handler, nil
}

type compactJSON struct{ runtime.JSONPb }

func (marshaler *compactJSON) Marshal(value any) ([]byte, error) {
	data, err := marshaler.JSONPb.Marshal(value)
	if err != nil {
		return nil, err
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func (marshaler *compactJSON) NewEncoder(writer io.Writer) runtime.Encoder {
	return runtime.EncoderFunc(func(value any) error {
		data, err := marshaler.Marshal(value)
		if err != nil {
			return err
		}
		_, err = writer.Write(append(data, '\n'))
		return err
	})
}

// NewUnavailableHandler creates the fixed response used after drain starts.
func NewUnavailableHandler(settings Settings) (http.Handler, error) {
	if !validSettings(settings) {
		return nil, errors.New(
			"create unavailable gateway handler: all dependencies and limits are required",
		)
	}
	handler := &Handler{settings: settings}
	return handler.instrument(
		"unmatched",
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writeLegacyError(
				writer,
				http.StatusServiceUnavailable,
				"Service Unavailable",
				"",
				request.URL.Path,
			)
		}),
	), nil
}

func validSettings(settings Settings) bool {
	return settings.RequestTimeout > 0 && settings.MaxBodyBytes > 0 &&
		settings.TracerProvider != nil && settings.MeterProvider != nil &&
		settings.Propagator != nil && settings.Observer != nil
}

type Handler struct {
	mux       http.Handler
	settings  Settings
	search    http.Handler
	unmatched http.Handler
}

type (
	requestFacts struct{ originalPath string }
	factsKey     struct{}
)

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	facts := requestFacts{originalPath: request.URL.Path}
	request = sanitizeRequest(
		request.WithContext(context.WithValue(request.Context(), factsKey{}, facts)),
	)
	requestCtx, cancel := context.WithTimeout(
		request.Context(),
		handler.settings.RequestTimeout,
	)
	defer cancel()
	request = request.WithContext(requestCtx)
	if facts.originalPath == "/policies" && request.Method == http.MethodGet {
		handler.search.ServeHTTP(writer, request)
		return
	}
	handler.unmatched.ServeHTTP(writer, request)
}

func (handler *Handler) instrument(route string, next http.Handler) http.Handler {
	observed := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		observation := handler.settings.Observer.StartHTTP(request.Context(), route)
		next.ServeHTTP(captured, request)
		observation.FinishHTTP(request.Context(), captured.status)
	})
	return otelhttp.NewHandler(
		observed,
		route,
		otelhttp.WithTracerProvider(handler.settings.TracerProvider),
		otelhttp.WithMeterProvider(handler.settings.MeterProvider),
		otelhttp.WithPropagators(handler.settings.Propagator),
		otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return route }),
	)
}

func (handler *Handler) serveSearch(writer http.ResponseWriter, request *http.Request) {
	query, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil {
		writeLegacyError(writer, http.StatusBadRequest, "Bad Request", "", "/policies")
		return
	}
	values, present := query["q"]
	if !present || len(values) == 0 {
		writeLegacyError(
			writer,
			http.StatusBadRequest,
			"Required QueryValue [q] not specified",
			"/queryText",
			"/policies",
		)
		return
	}
	query["q"] = values[:1]
	request.URL.RawQuery = query.Encode()
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) serveUnmatched(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.originalPath == "/policies" {
		writer.Header().Set("Allow", http.MethodGet)
		writeLegacyError(
			writer,
			http.StatusMethodNotAllowed,
			"Method Not Allowed",
			"",
			"/policies",
		)
		return
	}
	writeLegacyError(writer, http.StatusNotFound, "Not Found", "", facts.originalPath)
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
	return request
}

func denyHeader(string) (string, bool) { return "", false }

// ClearApplicationMetadata removes gateway-created metadata before otelgrpc injects trace context.
func ClearApplicationMetadata(
	ctx context.Context,
	method string,
	request, reply any,
	connection *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	options ...grpc.CallOption,
) error {
	return invoker(
		metadata.NewOutgoingContext(ctx, metadata.MD{}),
		method,
		request,
		reply,
		connection,
		options...,
	)
}

func safeGatewayError(
	_ context.Context,
	_ *runtime.ServeMux,
	_ runtime.Marshaler,
	writer http.ResponseWriter,
	request *http.Request,
	err error,
) {
	href := request.URL.Path
	switch status.Code(err) {
	case codes.InvalidArgument:
		writeLegacyError(writer, http.StatusBadRequest, "Bad Request", "", href)
	case codes.Canceled:
		writeLegacyError(writer, 499, "Request Canceled", "", href)
	case codes.DeadlineExceeded:
		writeLegacyError(writer, http.StatusGatewayTimeout, "Gateway Timeout", "", href)
	case codes.Unavailable:
		writeLegacyError(
			writer,
			http.StatusServiceUnavailable,
			"Service Unavailable",
			"",
			href,
		)
	default:
		writeLegacyError(
			writer,
			http.StatusInternalServerError,
			"Internal Server Error",
			"",
			href,
		)
	}
}

type legacyError struct {
	Message string      `json:"message"`
	Path    string      `json:"path,omitempty"`
	Links   legacyLinks `json:"_links"`
}

type legacyLinks struct {
	Self legacyLink `json:"self"`
}

type legacyLink struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated"`
}

func writeLegacyError(
	writer http.ResponseWriter,
	statusCode int,
	message string,
	path string,
	href string,
) {
	payload, _ := json.Marshal(legacyError{
		Message: message,
		Path:    path,
		Links:   legacyLinks{Self: legacyLink{Href: href}},
	})
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_, _ = writer.Write(payload)
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (writer *statusWriter) WriteHeader(statusCode int) {
	if writer.wrote {
		return
	}
	writer.wrote = true
	writer.status = statusCode
	writer.ResponseWriter.WriteHeader(statusCode)
}
