// Package gateway owns the strict public HTTP-to-gRPC boundary.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
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

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/telemetry"
)

const echoRoute = "POST /v1/example/echo"

type Settings struct {
	RequestTimeout time.Duration
	MaxBodyBytes   int
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Observer       *telemetry.RequestObserver
}

// NewHandler registers the generated client while retaining a strict HTTP contract.
func NewHandler(client scaffoldv1.ExampleServiceClient, settings Settings) (http.Handler, error) {
	if client == nil || settings.RequestTimeout <= 0 || settings.MaxBodyBytes <= 0 ||
		settings.TracerProvider == nil || settings.MeterProvider == nil ||
		settings.Propagator == nil || settings.Observer == nil {
		return nil, errors.New("create gateway: all dependencies and limits are required")
	}
	marshaler := &runtime.JSONPb{
		MarshalOptions:   protojson.MarshalOptions{},
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
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
	if err := scaffoldv1.RegisterExampleServiceHandlerClient(
		context.Background(),
		mux,
		client,
	); err != nil {
		return nil, errors.New("create gateway: register generated handler")
	}
	handler := &Handler{mux: mux, settings: settings}
	handler.echo = handler.instrument(echoRoute, http.HandlerFunc(handler.serveEcho))
	handler.unmatched = handler.instrument(
		"unmatched",
		http.HandlerFunc(handler.serveUnmatched),
	)
	return handler, nil
}

type Handler struct {
	mux       http.Handler
	settings  Settings
	echo      http.Handler
	unmatched http.Handler
}

type requestFacts struct {
	originalPath string
	hadQuery     bool
}

type factsKey struct{}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	facts := requestFacts{originalPath: request.URL.Path, hadQuery: request.URL.RawQuery != ""}
	request = sanitizeRequest(
		request.WithContext(context.WithValue(request.Context(), factsKey{}, facts)),
	)
	requestCtx, cancel := context.WithTimeout(
		request.Context(),
		handler.settings.RequestTimeout,
	)
	defer cancel()
	request = request.WithContext(requestCtx)
	if facts.originalPath == "/v1/example/echo" && request.Method == http.MethodPost {
		handler.echo.ServeHTTP(writer, request)
		return
	}
	request.URL.Path = "/unmatched"
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

func (handler *Handler) serveEcho(writer http.ResponseWriter, request *http.Request) {
	if facts, _ := request.Context().Value(factsKey{}).(requestFacts); facts.hadQuery {
		writeError(writer, http.StatusBadRequest, codes.InvalidArgument, "invalid request")
		return
	}
	if !acceptsJSON(request.Header.Get("Accept")) {
		writeError(
			writer,
			http.StatusNotAcceptable,
			codes.InvalidArgument,
			"not acceptable",
		)
		return
	}
	if !isJSON(request.Header.Get("Content-Type")) {
		writeError(
			writer,
			http.StatusUnsupportedMediaType,
			codes.InvalidArgument,
			"unsupported media type",
		)
		return
	}
	body, tooLarge, err := readBounded(request.Body, handler.settings.MaxBodyBytes)
	if err != nil {
		writeError(writer, http.StatusInternalServerError, codes.Internal, "internal error")
		return
	}
	if tooLarge {
		writeError(
			writer,
			http.StatusRequestEntityTooLarge,
			codes.ResourceExhausted,
			"request body too large",
		)
		return
	}
	if !validRequestJSON(body) {
		writeError(writer, http.StatusBadRequest, codes.InvalidArgument, "invalid request")
		return
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) serveUnmatched(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.originalPath == "/v1/example/echo" {
		writeError(
			writer,
			http.StatusMethodNotAllowed,
			codes.Unimplemented,
			"method not allowed",
		)
		return
	}
	writeError(writer, http.StatusNotFound, codes.NotFound, "not found")
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

// ClearApplicationMetadata removes gateway-created application metadata before otelgrpc injects trace context.
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
		options...)
}

func safeGatewayError(
	_ context.Context,
	_ *runtime.ServeMux,
	_ runtime.Marshaler,
	writer http.ResponseWriter,
	_ *http.Request,
	err error,
) {
	code := status.Code(err)
	switch code {
	case codes.InvalidArgument:
		writeError(writer, http.StatusBadRequest, code, "invalid request")
	case codes.Canceled:
		writeError(writer, 499, code, "request canceled")
	case codes.DeadlineExceeded:
		writeError(writer, http.StatusGatewayTimeout, code, "deadline exceeded")
	case codes.Unavailable:
		writeError(writer, http.StatusServiceUnavailable, code, "service unavailable")
	case codes.ResourceExhausted:
		writeError(writer, http.StatusRequestEntityTooLarge, code, "request body too large")
	default:
		writeError(writer, http.StatusInternalServerError, codes.Internal, "internal error")
	}
}

func writeError(writer http.ResponseWriter, httpStatus int, code codes.Code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(httpStatus)
	_, _ = io.WriteString(
		writer,
		`{"code":`+jsonNumber(int32(code))+`,"message":"`+message+`","details":[]}`,
	)
}

func jsonNumber(value int32) string { return strconv.FormatInt(int64(value), 10) }

func readBounded(body io.ReadCloser, maximum int) ([]byte, bool, error) {
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, int64(maximum)+1))
	return data, len(data) > maximum, err
}

func validRequestJSON(data []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	seen := false
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil || name != "message" || seen {
			return false
		}
		seen = true
		var value any
		if err := decoder.Decode(&value); err != nil {
			return false
		}
		if value != nil {
			if _, ok := value.(string); !ok {
				return false
			}
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return false
	}
	_, err = decoder.Token()
	return errors.Is(err, io.EOF)
}

func isJSON(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	if err != nil || mediaType != "application/json" {
		return false
	}
	if len(parameters) == 0 {
		return true
	}
	return len(parameters) == 1 && strings.EqualFold(parameters["charset"], "utf-8")
}

func acceptsJSON(value string) bool {
	if value == "" {
		return true
	}
	for item := range strings.SplitSeq(value, ",") {
		mediaType, parameters, err := mime.ParseMediaType(strings.TrimSpace(item))
		if err == nil && len(parameters) == 0 &&
			(mediaType == "application/json" || mediaType == "*/*") {
			return true
		}
	}
	return false
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (writer *statusWriter) WriteHeader(status int) {
	if writer.wrote {
		return
	}
	writer.wrote = true
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}
