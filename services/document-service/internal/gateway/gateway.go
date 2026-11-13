// Package gateway owns the strict HTTP-to-gRPC Document lookup boundary.
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
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
)

const (
	documentPath  = "/documents/"
	documentRoute = "GET /documents/{policyNumber}"
)

type Settings struct {
	RequestTimeout       time.Duration
	MaximumResponseBytes int
	TracerProvider       trace.TracerProvider
	MeterProvider        metric.MeterProvider
	Propagator           propagation.TextMapPropagator
	Observer             *telemetry.RequestObserver
}

// NewHandler registers the generated client while retaining the accepted private HTTP contract.
func NewHandler(client documentv1.DocumentServiceClient, settings Settings) (http.Handler, error) {
	if client == nil || !validSettings(settings) {
		return nil, errors.New("create gateway: dependencies and limits are required")
	}
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(
			runtime.MIMEWildcard,
			newDocumentMarshaler(settings.MaximumResponseBytes),
		),
		runtime.WithIncomingHeaderMatcher(denyHeader),
		runtime.WithOutgoingHeaderMatcher(denyHeader),
		runtime.WithOutgoingTrailerMatcher(denyHeader),
		runtime.WithErrorHandler(safeGatewayError),
		runtime.WithDisableHTTPMethodOverride(),
		runtime.WithDisablePathLengthFallback(),
	)
	decodedClient := &pathClient{DocumentServiceClient: client}
	if err := documentv1.RegisterDocumentServiceHandlerClient(
		context.Background(), mux, decodedClient,
	); err != nil {
		return nil, errors.New("create gateway: register generated handler")
	}
	handler := &Handler{mux: mux, settings: settings}
	handler.lookup = handler.instrument(documentRoute, http.HandlerFunc(handler.serveLookup))
	handler.unmatched = handler.instrument(
		"unmatched",
		http.HandlerFunc(handler.serveUnmatched),
	)
	return handler, nil
}

// NewUnavailableHandler creates the fixed-envelope response used after drain starts.
func NewUnavailableHandler(settings Settings) (http.Handler, error) {
	if !validSettings(settings) {
		return nil, errors.New(
			"create unavailable gateway handler: dependencies are required",
		)
	}
	handler := &Handler{settings: settings}
	return handler.instrument(
		"unmatched",
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writeError(
				writer,
				http.StatusServiceUnavailable,
				codes.Unavailable,
				"service unavailable",
			)
		}),
	), nil
}

func validSettings(settings Settings) bool {
	return settings.RequestTimeout > 0 && settings.MaximumResponseBytes > 0 &&
		settings.TracerProvider != nil &&
		settings.MeterProvider != nil && settings.Propagator != nil && settings.Observer != nil
}

type Handler struct {
	mux       http.Handler
	settings  Settings
	lookup    http.Handler
	unmatched http.Handler
}

type (
	requestFacts struct {
		rawPath  string
		rawQuery string
	}
	factsKey         struct{}
	decodedPolicyKey struct{}
)

type pathClient struct {
	documentv1.DocumentServiceClient
}

func (client *pathClient) FindDocuments(
	ctx context.Context,
	request *documentv1.FindDocumentsRequest,
	options ...grpc.CallOption,
) (*documentv1.FindDocumentsResponse, error) {
	if policyNumber, ok := ctx.Value(decodedPolicyKey{}).(string); ok {
		request = &documentv1.FindDocumentsRequest{PolicyNumber: policyNumber}
	}
	return client.DocumentServiceClient.FindDocuments(ctx, request, options...)
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	facts := requestFacts{
		rawPath:  request.URL.EscapedPath(),
		rawQuery: request.URL.RawQuery,
	}
	request = sanitizeRequest(
		request.WithContext(context.WithValue(request.Context(), factsKey{}, facts)),
	)
	requestCtx, cancel := context.WithTimeout(
		request.Context(),
		handler.settings.RequestTimeout,
	)
	defer cancel()
	request = request.WithContext(requestCtx)
	if request.Method == http.MethodGet && strings.HasPrefix(facts.rawPath, documentPath) {
		handler.lookup.ServeHTTP(writer, request)
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
		observed, route,
		otelhttp.WithTracerProvider(handler.settings.TracerProvider),
		otelhttp.WithMeterProvider(handler.settings.MeterProvider),
		otelhttp.WithPropagators(handler.settings.Propagator),
		otelhttp.WithSpanNameFormatter(func(string, *http.Request) string { return route }),
	)
}

func (handler *Handler) serveLookup(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	rawPolicyNumber := strings.TrimPrefix(facts.rawPath, documentPath)
	if facts.rawQuery != "" {
		writeError(writer, http.StatusBadRequest, codes.InvalidArgument, "invalid request")
		return
	}
	if rawPolicyNumber == "" || strings.Contains(rawPolicyNumber, "/") {
		writeError(writer, http.StatusNotFound, codes.NotFound, "not found")
		return
	}
	policyNumber, err := url.PathUnescape(rawPolicyNumber)
	if err != nil {
		writeError(writer, http.StatusBadRequest, codes.InvalidArgument, "invalid request")
		return
	}
	if strings.Contains(policyNumber, "/") {
		writeError(writer, http.StatusNotFound, codes.NotFound, "not found")
		return
	}
	request = request.WithContext(context.WithValue(
		request.Context(), decodedPolicyKey{}, policyNumber,
	))
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) serveUnmatched(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.rawPath == "/documents" || strings.HasPrefix(facts.rawPath, documentPath) {
		if request.Method != http.MethodGet {
			writer.Header().Set("Allow", http.MethodGet)
			writeError(
				writer,
				http.StatusMethodNotAllowed,
				codes.Unimplemented,
				"method not allowed",
			)
			return
		}
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

// ClearApplicationMetadata removes gateway-created metadata before trace injection.
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
	case codes.ResourceExhausted:
		writeError(writer, http.StatusTooManyRequests, code, "response limit exceeded")
	case codes.Unavailable:
		writeError(writer, http.StatusServiceUnavailable, code, "service unavailable")
	default:
		writeError(writer, http.StatusInternalServerError, codes.Internal, "internal error")
	}
}

func writeError(writer http.ResponseWriter, httpStatus int, code codes.Code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(httpStatus)
	_, _ = io.WriteString(
		writer,
		`{"code":`+strconv.FormatInt(
			int64(code),
			10,
		)+`,"message":"`+message+`","details":[]}`,
	)
}

type documentMarshaler struct {
	*runtime.JSONPb
	maximumResponseBytes int
}

func newDocumentMarshaler(maximumResponseBytes int) *documentMarshaler {
	return &documentMarshaler{
		JSONPb: &runtime.JSONPb{
			MarshalOptions:   protojson.MarshalOptions{EmitDefaultValues: true},
			UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
		},
		maximumResponseBytes: maximumResponseBytes,
	}
}

func (marshaler *documentMarshaler) Marshal(value any) ([]byte, error) {
	response, ok := value.(*documentv1.FindDocumentsResponse)
	if !ok {
		return marshaler.JSONPb.Marshal(value)
	}
	type httpDocument struct {
		PolicyNumber string `json:"policyNumber"`
		Content      []byte `json:"content"`
	}
	httpResponse := struct {
		Documents []httpDocument `json:"documents"`
	}{Documents: make([]httpDocument, len(response.GetDocuments()))}
	for index, document := range response.GetDocuments() {
		if document == nil {
			return nil, errors.New("marshal document lookup: missing document")
		}
		content := document.GetContent()
		if content == nil {
			content = []byte{}
		}
		httpResponse.Documents[index] = httpDocument{
			PolicyNumber: document.GetPolicyNumber(),
			Content:      content,
		}
	}
	encoded, err := json.Marshal(httpResponse)
	if err != nil {
		return nil, err
	}
	if len(encoded) > marshaler.maximumResponseBytes {
		return nil, status.Error(codes.ResourceExhausted, "response limit exceeded")
	}
	return encoded, nil
}

func (marshaler *documentMarshaler) NewEncoder(writer io.Writer) runtime.Encoder {
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

func (writer *statusWriter) Write(body []byte) (int, error) {
	if !writer.wrote {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}
