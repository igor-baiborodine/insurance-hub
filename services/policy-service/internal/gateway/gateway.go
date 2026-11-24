// Package gateway owns the strict public HTTP-to-gRPC boundary.
package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/telemetry"
)

const (
	offersPath = "/offers"
	offerRoute = "POST " + offersPath
)

type Settings struct {
	RequestTimeout time.Duration
	MaxBodyBytes   int
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	Propagator     propagation.TextMapPropagator
	Observer       *telemetry.RequestObserver
}

// NewHandler registers the generated client while retaining a strict HTTP contract.
func NewHandler(client policyv1.PolicyServiceClient, settings Settings) (http.Handler, error) {
	if client == nil || !validSettings(settings) {
		return nil, errors.New("create gateway: all dependencies and limits are required")
	}
	marshaler := newPolicyMarshaler()
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, marshaler),
		runtime.WithIncomingHeaderMatcher(denyHeader),
		runtime.WithOutgoingHeaderMatcher(denyHeader),
		runtime.WithOutgoingTrailerMatcher(denyHeader),
		runtime.WithErrorHandler(safeGatewayError),
		runtime.WithDisableHTTPMethodOverride(),
		runtime.WithDisablePathLengthFallback(),
	)
	if err := policyv1.RegisterPolicyServiceHandlerClient(
		context.Background(),
		mux,
		client,
	); err != nil {
		return nil, errors.New("create gateway: register generated handler")
	}
	handler := &Handler{mux: mux, settings: settings}
	handler.offers = handler.instrument(offerRoute, http.HandlerFunc(handler.serveCreateOffer))
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
	mux       http.Handler
	settings  Settings
	offers    http.Handler
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
	if facts.originalPath == offersPath && request.Method == http.MethodPost {
		handler.offers.ServeHTTP(writer, request)
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

// serveCreateOffer applies the media, size and query rules before the generated route decodes
// the normalized RPC body.
func (handler *Handler) serveCreateOffer(writer http.ResponseWriter, request *http.Request) {
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
	normalized, ok := normalizeOfferJSON(body)
	if !ok {
		writeError(writer, http.StatusBadRequest, codes.InvalidArgument, "invalid request")
		return
	}
	request.Body = io.NopCloser(bytes.NewReader(normalized))
	handler.mux.ServeHTTP(writer, request)
}

func (handler *Handler) serveUnmatched(writer http.ResponseWriter, request *http.Request) {
	facts, _ := request.Context().Value(factsKey{}).(requestFacts)
	if facts.originalPath == offersPath {
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
	case codes.NotFound:
		writeError(writer, http.StatusNotFound, code, "not found")
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

func normalizeOfferJSON(data []byte) ([]byte, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	seen := make(map[string]struct{}, 5)
	request := &policyv1.CreateOfferRequest{}
	for decoder.More() {
		value, tokenErr := decoder.Token()
		name, nameOK := value.(string)
		if tokenErr != nil || !nameOK {
			return nil, false
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, false
		}
		seen[name] = struct{}{}
		switch name {
		case "productCode":
			err = decoder.Decode(&request.ProductCode)
		case "policyFrom":
			request.PolicyFrom, err = decodeHTTPDate(decoder)
		case "policyTo":
			request.PolicyTo, err = decodeHTTPDate(decoder)
		case "selectedCovers":
			err = decoder.Decode(&request.SelectedCovers)
		case "answers":
			var answers []json.RawMessage
			if err = decoder.Decode(&answers); err == nil {
				request.Answers = make([]*policyv1.OfferAnswer, 0, len(answers))
				for _, raw := range answers {
					answer, valid := decodeHTTPAnswer(raw)
					if !valid {
						return nil, false
					}
					request.Answers = append(request.Answers, answer)
				}
			}
		default:
			return nil, false
		}
		if err != nil {
			return nil, false
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	_, err = decoder.Token()
	if !errors.Is(err, io.EOF) {
		return nil, false
	}
	normalized, err := protojson.Marshal(request)
	return normalized, err == nil
}

// decodeHTTPDate converts an ISO date or [year, month, day] array to RPC date text.
func decodeHTTPDate(decoder *json.Decoder) (string, error) {
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return "", err
	}
	if !bytes.HasPrefix(raw, []byte("[")) {
		var text string
		err := json.Unmarshal(raw, &text)
		return text, err
	}
	var parts []any
	arrayDecoder := json.NewDecoder(bytes.NewReader(raw))
	arrayDecoder.UseNumber()
	if err := arrayDecoder.Decode(&parts); err != nil || len(parts) != 3 {
		return "", errors.New("invalid date array")
	}
	var values [3]int
	limits := [3][2]int{{0, 9999}, {1, 12}, {1, 31}}
	for index, part := range parts {
		number, isNumber := part.(json.Number)
		value, err := strconv.Atoi(number.String())
		if !isNumber || err != nil || value < limits[index][0] || value > limits[index][1] {
			return "", errors.New("invalid date array")
		}
		values[index] = value
	}
	return fmt.Sprintf("%04d-%02d-%02d", values[0], values[1], values[2]), nil
}

// decodeHTTPAnswer maps the legacy typed answer to its RPC variant. Choice and text answers are
// JSON strings; numeric answers are JSON numbers whose exact text is preserved.
func decodeHTTPAnswer(data []byte) (*policyv1.OfferAnswer, bool) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, false
	}
	var kind string
	var answerValue any
	answer := &policyv1.OfferAnswer{}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		value, tokenErr := decoder.Token()
		name, nameOK := value.(string)
		if tokenErr != nil || !nameOK {
			return nil, false
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, false
		}
		seen[name] = struct{}{}
		switch name {
		case "type":
			err = decoder.Decode(&kind)
		case "questionCode":
			err = decoder.Decode(&answer.QuestionCode)
		case "answer":
			err = decoder.Decode(&answerValue)
		default:
			return nil, false
		}
		if err != nil {
			return nil, false
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, false
	}
	if _, err = decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, false
	}
	text, isText := answerValue.(string)
	number, isNumber := answerValue.(json.Number)
	switch {
	case kind == "choice" && isText:
		answer.Value = &policyv1.OfferAnswer_Choice{Choice: text}
	case kind == "text" && isText:
		answer.Value = &policyv1.OfferAnswer_Text{Text: text}
	case kind == "numeric" && isNumber:
		answer.Value = &policyv1.OfferAnswer_Number{Number: number.String()}
	default:
		return nil, false
	}
	return answer, true
}

type policyMarshaler struct{ *runtime.JSONPb }

func newPolicyMarshaler() *policyMarshaler {
	return &policyMarshaler{JSONPb: &runtime.JSONPb{
		MarshalOptions:   protojson.MarshalOptions{},
		UnmarshalOptions: protojson.UnmarshalOptions{DiscardUnknown: false},
	}}
}

// Marshal writes offer amounts as JSON numbers from their exact decimal text.
func (marshaler *policyMarshaler) Marshal(value any) ([]byte, error) {
	response, ok := value.(*policyv1.CreateOfferResponse)
	if !ok {
		return marshaler.JSONPb.Marshal(value)
	}
	mapped := struct {
		OfferNumber  string                     `json:"offerNumber"`
		TotalPrice   json.RawMessage            `json:"totalPrice"`
		CoversPrices map[string]json.RawMessage `json:"coversPrices"`
	}{
		OfferNumber:  response.GetOfferNumber(),
		CoversPrices: make(map[string]json.RawMessage, len(response.GetCoversPrices())),
	}
	var valid bool
	mapped.TotalPrice, valid = exactJSONNumber(response.GetTotalPrice())
	if !valid {
		return nil, errors.New("marshal offer: invalid total")
	}
	for code, price := range response.GetCoversPrices() {
		number, valid := exactJSONNumber(price)
		if !valid {
			return nil, errors.New("marshal offer: invalid cover price")
		}
		mapped.CoversPrices[code] = number
	}
	return json.Marshal(mapped)
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

func (marshaler *policyMarshaler) NewEncoder(writer io.Writer) runtime.Encoder {
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
