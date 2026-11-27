// Package gateway owns the strict public HTTP-to-gRPC boundary.
package gateway

import (
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

	paymentv1 "github.com/igor-baiborodine/insurance-hub/services/payment-service/gen/payment/v1"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/telemetry"
)

const (
	accountListRoute   = "GET /payment/accounts"
	accountDetailRoute = "GET /payment/accounts/{accountNumber}"
)

type Settings struct {
	RequestTimeout time.Duration
	MaxBodyBytes   int
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Observer       *telemetry.RequestObserver
}

// NewHandler registers the generated clients while retaining a strict HTTP contract.
func NewHandler(client paymentv1.PaymentServiceClient, settings Settings) (http.Handler, error) {
	if client == nil || !validSettings(settings) {
		return nil, errors.New("create gateway: all dependencies and limits are required")
	}
	marshaler := newPaymentMarshaler()
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, marshaler),
		runtime.WithIncomingHeaderMatcher(denyHeader),
		runtime.WithOutgoingHeaderMatcher(denyHeader),
		runtime.WithOutgoingTrailerMatcher(denyHeader),
		runtime.WithErrorHandler(safeGatewayError),
		runtime.WithDisableHTTPMethodOverride(),
		runtime.WithDisablePathLengthFallback(),
	)
	if err := paymentv1.RegisterPaymentServiceHandlerClient(
		context.Background(),
		mux,
		client,
	); err != nil {
		return nil, errors.New("create gateway: register Payment handler")
	}
	handler := &Handler{mux: mux, settings: settings}
	handler.accountList = handler.instrument(
		accountListRoute,
		http.HandlerFunc(handler.serveAccount),
	)
	handler.accountDetail = handler.instrument(
		accountDetailRoute,
		http.HandlerFunc(handler.serveAccount),
	)
	handler.unmatched = handler.instrument(
		"unmatched",
		http.HandlerFunc(handler.serveUnmatched),
	)
	return handler, nil
}

// NewUnavailableHandler creates the instrumented fixed-envelope response used after drain starts.
func NewUnavailableHandler(settings Settings) (http.Handler, error) {
	if !validSettings(settings) {
		return nil, errors.New(
			"create unavailable gateway handler: all dependencies and limits are required",
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
	return settings.RequestTimeout > 0 && settings.MaxBodyBytes > 0 &&
		settings.TracerProvider != nil && settings.MeterProvider != nil &&
		settings.Propagator != nil && settings.Observer != nil
}

type Handler struct {
	mux           http.Handler
	settings      Settings
	accountList   http.Handler
	accountDetail http.Handler
	unmatched     http.Handler
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
	if facts.originalPath == "/payment/accounts" && request.Method == http.MethodGet {
		handler.accountList.ServeHTTP(writer, request)
		return
	}
	if accountDetailPath(facts.originalPath) && request.Method == http.MethodGet {
		request.URL.Path = "/payment/accounts/{accountNumber}"
		request.URL.RawPath = ""
		handler.accountDetail.ServeHTTP(writer, request)
		return
	}
	request.URL.Path = "/unmatched"
	handler.unmatched.ServeHTTP(writer, request)
}

func (handler *Handler) serveAccount(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.hadQuery {
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
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) instrument(route string, next http.Handler) http.Handler {
	observed := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		captured := &statusWriter{ResponseWriter: writer, status: http.StatusOK}
		observation := handler.settings.Observer.StartHTTP(request.Context(), route)
		nextRequest := request.Clone(request.Context())
		if facts, ok := request.Context().Value(factsKey{}).(requestFacts); ok {
			nextRequest.URL.Path = facts.originalPath
			nextRequest.URL.RawPath = ""
		}
		next.ServeHTTP(captured, nextRequest)
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

func (handler *Handler) serveUnmatched(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.originalPath == "/payment/accounts" || accountDetailPath(facts.originalPath) {
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

func accountDetailPath(path string) bool {
	const prefix = "/payment/accounts/"
	return strings.HasPrefix(path, prefix) && len(path) > len(prefix) &&
		!strings.Contains(path[len(prefix):], "/")
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
	request *http.Request,
	err error,
) {
	code := status.Code(err)
	if code == codes.NotFound && accountDetailPath(request.URL.Path) {
		writeLegacyError(writer, request.URL.EscapedPath())
		return
	}
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
		writeError(writer, http.StatusInternalServerError, codes.Internal, "internal error")
	default:
		writeError(writer, http.StatusInternalServerError, codes.Internal, "internal error")
	}
}

type legacyLink struct {
	Href      string `json:"href"`
	Templated bool   `json:"templated"`
}

type legacyLinks struct {
	Self legacyLink `json:"self"`
}

type legacyError struct {
	Links   legacyLinks `json:"_links"`
	Message string      `json:"message"`
}

func writeLegacyError(writer http.ResponseWriter, path string) {
	body := legacyError{
		Links:   legacyLinks{Self: legacyLink{Href: path}},
		Message: "Internal Server Error",
	}
	encoded, _ := json.Marshal(body)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusInternalServerError)
	_, _ = writer.Write(encoded)
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

type paymentMarshaler struct{ *runtime.JSONPb }

func newPaymentMarshaler() *paymentMarshaler {
	return &paymentMarshaler{JSONPb: &runtime.JSONPb{
		MarshalOptions:   protojson.MarshalOptions{},
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
	}}
}

type accountBody struct {
	PolicyAccountNumber string `json:"policyAccountNumber"`
	PolicyNumber        string `json:"policyNumber"`
	Created             int64  `json:"created"`
	Updated             int64  `json:"updated"`
}

type accountBalanceBody struct {
	PolicyAccountNumber string          `json:"policyAccountNumber"`
	PolicyNumber        string          `json:"policyNumber"`
	Balance             json.RawMessage `json:"balance"`
	Created             int64           `json:"created"`
	Updated             int64           `json:"updated"`
}

func (marshaler *paymentMarshaler) Marshal(value any) ([]byte, error) {
	switch response := value.(type) {
	case []*paymentv1.Account:
		accounts := make([]accountBody, 0, len(response))
		for _, account := range response {
			accounts = append(accounts, accountBody{
				PolicyAccountNumber: account.GetPolicyAccountNumber(),
				PolicyNumber:        account.GetPolicyNumber(),
				Created:             account.GetCreated(),
				Updated:             account.GetUpdated(),
			})
		}
		return json.Marshal(accounts)
	case *paymentv1.AccountBalance:
		balance, valid := exactJSONNumber(response.GetBalance())
		if !valid {
			return nil, errors.New("marshal Payment account: invalid balance")
		}
		return json.Marshal(accountBalanceBody{
			PolicyAccountNumber: response.GetPolicyAccountNumber(),
			PolicyNumber:        response.GetPolicyNumber(),
			Balance:             balance,
			Created:             response.GetCreated(),
			Updated:             response.GetUpdated(),
		})
	default:
		return marshaler.JSONPb.Marshal(value)
	}
}

func exactJSONNumber(text string) (json.RawMessage, bool) {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	if _, ok := value.(json.Number); !ok {
		return nil, false
	}
	_, err := decoder.Token()
	return json.RawMessage(text), errors.Is(err, io.EOF)
}

func (marshaler *paymentMarshaler) NewEncoder(writer io.Writer) runtime.Encoder {
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
