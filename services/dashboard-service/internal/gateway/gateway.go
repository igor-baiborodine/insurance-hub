// Package gateway owns the strict private HTTP-to-gRPC boundary.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
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

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
)

const (
	totalSalesPath  = "/dashboard/totalsales"
	totalSalesRoute = "POST " + totalSalesPath
	agentSalesPath  = "/dashboard/agentssales"
	agentSalesRoute = "POST " + agentSalesPath
)

type Settings struct {
	RequestTimeout time.Duration
	MaxBodyBytes   int
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Observer       *telemetry.RequestObserver
}

// NewHandler registers the generated Dashboard client while retaining the accepted HTTP contract.
func NewHandler(
	client dashboardv1.DashboardServiceClient,
	settings Settings,
) (http.Handler, error) {
	if client == nil || !validSettings(settings) {
		return nil, errors.New("create gateway: all dependencies and limits are required")
	}
	marshaler := newLegacyMarshaler()
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, marshaler),
		runtime.WithIncomingHeaderMatcher(denyHeader),
		runtime.WithOutgoingHeaderMatcher(denyHeader),
		runtime.WithOutgoingTrailerMatcher(denyHeader),
		runtime.WithErrorHandler(safeGatewayError),
		runtime.WithDisableHTTPMethodOverride(),
		runtime.WithDisablePathLengthFallback(),
	)
	if err := dashboardv1.RegisterDashboardServiceHandlerClient(
		context.Background(),
		mux,
		client,
	); err != nil {
		return nil, errors.New("create gateway: register generated handler")
	}
	handler := &Handler{mux: mux, settings: settings}
	handler.totalSales = handler.instrument(
		totalSalesRoute,
		http.HandlerFunc(handler.serveTotalSales),
	)
	handler.agentSales = handler.instrument(
		agentSalesRoute,
		http.HandlerFunc(handler.serveAgentSales),
	)
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
	mux        http.Handler
	settings   Settings
	totalSales http.Handler
	agentSales http.Handler
	unmatched  http.Handler
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
	if facts.originalPath == totalSalesPath && request.Method == http.MethodPost {
		handler.totalSales.ServeHTTP(writer, request)
		return
	}
	if facts.originalPath == agentSalesPath && request.Method == http.MethodPost {
		handler.agentSales.ServeHTTP(writer, request)
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

func (handler *Handler) serveTotalSales(writer http.ResponseWriter, request *http.Request) {
	handler.serveSales(writer, request, normalizeTotalSalesRequestJSON)
}

func (handler *Handler) serveAgentSales(writer http.ResponseWriter, request *http.Request) {
	handler.serveSales(writer, request, normalizeAgentSalesRequestJSON)
}

func (handler *Handler) serveSales(
	writer http.ResponseWriter,
	request *http.Request,
	normalize func([]byte) ([]byte, bool),
) {
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
	normalized, ok := normalize(body)
	if !ok {
		writeError(writer, http.StatusBadRequest, codes.InvalidArgument, "invalid request")
		return
	}
	request.Body = io.NopCloser(bytes.NewReader(normalized))
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) serveUnmatched(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.originalPath == totalSalesPath || facts.originalPath == agentSalesPath {
		writer.Header().Set("Allow", http.MethodPost)
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
		options...,
	)
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
	default:
		writeError(writer, http.StatusInternalServerError, codes.Internal, "internal error")
	}
}

func writeError(writer http.ResponseWriter, httpStatus int, code codes.Code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(httpStatus)
	_, _ = io.WriteString(writer, `{"code":`+strconv.FormatInt(int64(code), 10)+
		`,"message":"`+message+`","details":[]}`)
}

func readBounded(body io.ReadCloser, maximum int) ([]byte, bool, error) {
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, int64(maximum)+1))
	return data, len(data) > maximum, err
}

type normalizedRequest struct {
	ProductCode  *string `json:"productCode,omitempty"`
	SaleDateFrom string  `json:"saleDateFrom"`
	SaleDateTo   string  `json:"saleDateTo"`
}

func normalizeTotalSalesRequestJSON(data []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	request := normalizedRequest{}
	var fromSet, toSet bool
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		switch name {
		case "productCode":
			if bytes.Equal(value, []byte("null")) {
				request.ProductCode = nil
				continue
			}
			var product string
			if err := json.Unmarshal(value, &product); err != nil {
				return nil, false
			}
			request.ProductCode = &product
		case "saleDateFrom":
			if bytes.Equal(value, []byte("null")) {
				return nil, false
			}
			if err := json.Unmarshal(value, &request.SaleDateFrom); err != nil {
				return nil, false
			}
			fromSet = true
		case "saleDateTo":
			if bytes.Equal(value, []byte("null")) {
				return nil, false
			}
			if err := json.Unmarshal(value, &request.SaleDateTo); err != nil {
				return nil, false
			}
			toSet = true
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) || !fromSet || !toSet {
		return nil, false
	}
	normalized, err := json.Marshal(request)
	return normalized, err == nil
}

type normalizedAgentRequest struct {
	AgentLogin   *string `json:"agentLogin,omitempty"`
	ProductCode  *string `json:"productCode,omitempty"`
	SaleDateFrom string  `json:"saleDateFrom"`
	SaleDateTo   string  `json:"saleDateTo"`
}

func normalizeAgentSalesRequestJSON(data []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	request := normalizedAgentRequest{}
	var fromSet, toSet bool
	for decoder.More() {
		name, err := decoder.Token()
		if err != nil {
			return nil, false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, false
		}
		switch name {
		case "agentLogin":
			if bytes.Equal(value, []byte("null")) {
				request.AgentLogin = nil
				continue
			}
			var agent string
			if err := json.Unmarshal(value, &agent); err != nil {
				return nil, false
			}
			request.AgentLogin = &agent
		case "productCode":
			if bytes.Equal(value, []byte("null")) {
				request.ProductCode = nil
				continue
			}
			var product string
			if err := json.Unmarshal(value, &product); err != nil {
				return nil, false
			}
			request.ProductCode = &product
		case "saleDateFrom":
			if bytes.Equal(value, []byte("null")) ||
				json.Unmarshal(value, &request.SaleDateFrom) != nil {
				return nil, false
			}
			fromSet = true
		case "saleDateTo":
			if bytes.Equal(value, []byte("null")) ||
				json.Unmarshal(value, &request.SaleDateTo) != nil {
				return nil, false
			}
			toSet = true
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) || !fromSet || !toSet {
		return nil, false
	}
	normalized, err := json.Marshal(request)
	return normalized, err == nil
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

type legacyMarshaler struct{ *runtime.JSONPb }

func newLegacyMarshaler() *legacyMarshaler {
	return &legacyMarshaler{JSONPb: &runtime.JSONPb{
		MarshalOptions:   protojson.MarshalOptions{},
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: true},
	}}
}

func (marshaler *legacyMarshaler) Marshal(value any) ([]byte, error) {
	if response, ok := value.(*dashboardv1.GetTotalSalesResponse); ok {
		return marshalLegacyResponse(response)
	}
	if response, ok := value.(*dashboardv1.GetAgentSalesResponse); ok {
		return marshalLegacyAgentResponse(response)
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

type legacyHTTPValue struct {
	PoliciesCount int64           `json:"policiesCount"`
	PremiumAmount json.RawMessage `json:"premiumAmount"`
}

type legacyHTTPResponse struct {
	Total           legacyHTTPValue            `json:"total"`
	PerProductTotal map[string]legacyHTTPValue `json:"perProductTotal,omitempty"`
}

type legacyAgentHTTPResponse struct {
	PerAgentTotal map[string]legacyHTTPValue `json:"perAgentTotal"`
}

func marshalLegacyAgentResponse(response *dashboardv1.GetAgentSalesResponse) ([]byte, error) {
	if response == nil {
		return nil, errors.New("marshal agent sales: missing response")
	}
	result := legacyAgentHTTPResponse{
		PerAgentTotal: make(map[string]legacyHTTPValue, len(response.PerAgentTotal)),
	}
	for agent, value := range response.PerAgentTotal {
		if value == nil {
			return nil, errors.New("marshal agent sales: missing agent value")
		}
		amount, err := domain.NewAgentPremiumAmount(value.GetPremiumAmount())
		if err != nil {
			return nil, errors.New("marshal agent sales: invalid premium amount")
		}
		number := json.RawMessage(amount.String())
		if !json.Valid(number) {
			return nil, errors.New("marshal agent sales: invalid JSON premium amount")
		}
		result.PerAgentTotal[agent] = legacyHTTPValue{
			PoliciesCount: value.GetPoliciesCount(),
			PremiumAmount: number,
		}
	}
	return json.Marshal(result)
}

func marshalLegacyResponse(response *dashboardv1.GetTotalSalesResponse) ([]byte, error) {
	if response == nil || response.Total == nil {
		return nil, errors.New("marshal total sales: missing total")
	}
	withScale := len(response.PerProductTotal) > 0
	result := legacyHTTPResponse{
		Total: legacyValue(response.Total, withScale),
	}
	if withScale {
		result.PerProductTotal = make(
			map[string]legacyHTTPValue,
			len(response.PerProductTotal),
		)
		for product, value := range response.PerProductTotal {
			if value == nil {
				return nil, errors.New("marshal total sales: missing product value")
			}
			result.PerProductTotal[product] = legacyValue(value, true)
		}
	}
	return json.Marshal(result)
}

func legacyValue(value *dashboardv1.SalesValue, withScale bool) legacyHTTPValue {
	return legacyHTTPValue{
		PoliciesCount: value.GetPoliciesCount(),
		PremiumAmount: json.RawMessage(
			formatMinorUnits(value.GetPremiumAmountMinorUnits(), withScale),
		),
	}
}

func formatMinorUnits(value int64, withScale bool) string {
	if value == 0 && !withScale {
		return "0"
	}
	integer := big.NewInt(value)
	sign := ""
	if integer.Sign() < 0 {
		sign = "-"
		integer.Abs(integer)
	}
	whole, fraction := new(big.Int), new(big.Int)
	whole.QuoRem(integer, big.NewInt(100), fraction)
	return sign + whole.String() + "." + leftPadTwo(fraction.String())
}

func leftPadTwo(value string) string {
	if len(value) == 1 {
		return "0" + value
	}
	return value
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
