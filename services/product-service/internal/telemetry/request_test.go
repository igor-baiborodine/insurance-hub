package telemetry_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	productgrpc "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	producthttp "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/http"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

func TestRequestObserver_RealTransportSignals(t *testing.T) {
	const private = "postgres://reader:private@database/catalog customer-secret"
	logs := &completionLog{written: make(chan struct{}, 1)}
	requestLogger := logger.New(logs, "product-service", 0)
	spans := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithView(
			sdkmetric.NewView(
				sdkmetric.Instrument{
					Name: "insurance_hub.product.request.duration",
				},
				sdkmetric.Stream{
					Aggregation: sdkmetric.AggregationExplicitBucketHistogram{
						Boundaries: []float64{
							0.005,
							0.01,
							0.025,
							0.05,
							0.075,
							0.1,
							0.25,
							0.5,
							0.75,
							1,
							2.5,
							5,
							7.5,
							10,
						},
					},
				},
			),
		),
	)
	observer, err := telemetry.NewRequestObserver(
		requestLogger, traceProvider, meterProvider, propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	list := func(ctx context.Context) (products []domain.Product, err error) {
		err = observer.Application(
			ctx,
			telemetry.OperationList,
			func(ctx context.Context) error {
				products = []domain.Product{{Code: "CAR"}}
				return nil
			},
		)
		return products, err
	}
	get := func(ctx context.Context, code string) (product domain.Product, err error) {
		err = observer.Application(
			ctx,
			telemetry.OperationGet,
			func(context.Context) error {
				switch code {
				case "CAR":
					product = domain.Product{Code: "CAR"}
				case "missing":
					err = application.ErrProductNotFound
				case "storage":
					err = errors.Join(
						application.ErrUnavailable,
						errors.New(private),
					)
				case "decode":
					err = errors.Join(
						application.ErrInvalidDefinition,
						errors.New(private),
					)
				case "internal":
					err = errors.New(private)
				case "cancel":
					err = context.Canceled
				case "deadline":
					err = context.DeadlineExceeded
				}
				return err
			},
		)
		return product, err
	}
	httpHandler, err := producthttp.NewHandler(requestLogger, producthttp.Settings{
		RequestTimeout: time.Second, Observer: observer,
	}, list, get)
	if err != nil {
		t.Fatal(err)
	}
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{
		Handler:           httpHandler,
		ReadHeaderTimeout: time.Second,
		IdleTimeout:       time.Second,
	}
	go func() { _ = producthttp.Serve(httpServer, httpListener) }()
	t.Cleanup(func() { _ = httpServer.Close() })
	grpcServer, err := productgrpc.NewServer(
		requestLogger,
		traceProvider,
		propagation.TraceContext{},
		productgrpc.Settings{
			RequestTimeout: time.Second, MaxReceiveBytes: 1 << 20,
			MaxSendBytes: 1 << 20, Observer: observer,
		},
		list,
		get,
	)
	if err != nil {
		t.Fatal(err)
	}
	grpcListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = grpcServer.Serve(grpcListener) }()
	t.Cleanup(grpcServer.Stop)
	connection, err := grpcgo.NewClient(
		grpcListener.Addr().String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	client := productv1.NewProductServiceClient(connection)

	httpCases := []struct {
		method, path string
		status       int
	}{
		{"GET", "/products", 200},
		{"GET", "/products/CAR", 200},
		{"GET", "/products/missing", 404},
		{"POST", "/products", 405},
		{"GET", "/products/storage", 500},
		{"GET", "/products/decode", 500},
		{"GET", "/products/internal", 500},
		{"GET", "/products/cancel", 500},
		{"GET", "/products/deadline", 500},
	}
	for _, test := range httpCases {
		request, requestErr := http.NewRequest(
			test.method,
			"http://"+httpListener.Addr().String()+test.path,
			nil,
		)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		response, requestErr := http.DefaultClient.Do(request)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf(
				"%s %s: status %d, want %d",
				test.method,
				test.path,
				response.StatusCode,
				test.status,
			)
		}
	}
	request, err := http.NewRequest(
		"GET",
		"http://"+httpListener.Addr().String()+"/unmatched",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 404 {
		t.Fatalf("unmatched HTTP status = %d", response.StatusCode)
	}
	raw, err := net.Dial("tcp", httpListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(raw, "GET /products/%ZZ HTTP/1.1\r\nHost: product\r\n\r\n")
	line, err := bufio.NewReader(raw).ReadString('\n')
	_ = raw.Close()
	if err != nil || !strings.Contains(line, "400") {
		t.Fatalf("pre-handler malformed URI: line=%q err=%v", line, err)
	}
	var unknown productv1.ListProductsResponse
	if err := connection.Invoke(
		context.Background(),
		"/product.v1.ProductService/Other",
		&productv1.ListProductsRequest{},
		&unknown,
	); status.Code(err) != codes.Unimplemented {
		t.Fatalf("unmatched gRPC method status = %s", status.Code(err))
	}
	if _, err := client.ListProducts(
		context.Background(),
		&productv1.ListProductsRequest{},
	); err != nil {
		t.Fatal(err)
	}
	grpcCases := []struct {
		code string
		want codes.Code
	}{
		{"CAR", codes.OK},
		{"missing", codes.NotFound},
		{"", codes.InvalidArgument},
		{"storage", codes.Unavailable},
		{"decode", codes.Internal},
		{"internal", codes.Internal},
		{"cancel", codes.Canceled},
		{"deadline", codes.DeadlineExceeded},
	}
	for _, test := range grpcCases {
		_, callErr := client.GetProduct(
			context.Background(),
			&productv1.GetProductRequest{Code: test.code},
		)
		if status.Code(callErr) != test.want {
			t.Fatalf(
				"gRPC get %q: code %s, want %s",
				test.code,
				status.Code(callErr),
				test.want,
			)
		}
	}
	completionDeadline := time.After(time.Second)
	for strings.Count(logs.String(), "\n") < 18 {
		select {
		case <-logs.written:
		case <-completionDeadline:
			t.Fatalf(
				"completion logs = %d, want 18",
				strings.Count(logs.String(), "\n"),
			)
		}
	}

	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	count, observations := 0, 0
	metricNames := map[string]bool{}
	for _, scope := range metrics.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			metricNames[instrument.Name] = true
			switch instrument.Name {
			case "insurance_hub.product.requests":
				data, ok := instrument.Data.(metricdata.Sum[int64])
				if !ok || instrument.Unit != "{request}" {
					t.Fatalf("request metric shape: %#v", instrument)
				}
				for _, point := range data.DataPoints {
					count += int(point.Value)
					assertMetricFields(t, point.Attributes)
				}
			case "insurance_hub.product.request.duration":
				data, ok := instrument.Data.(metricdata.Histogram[float64])
				if !ok || instrument.Unit != "s" {
					t.Fatalf("duration metric shape: %#v", instrument)
				}
				for _, point := range data.DataPoints {
					observations += int(point.Count)
					assertMetricFields(t, point.Attributes)
					if len(point.Bounds) != 14 || point.Bounds[0] != 0.005 ||
						point.Bounds[13] != 10 || point.Sum < 0 {
						t.Fatalf(
							"duration bounds/sum: %v, %f",
							point.Bounds,
							point.Sum,
						)
					}
				}
			default:
				t.Fatalf("unexpected automatic metric %s", instrument.Name)
			}
		}
	}
	if count != 18 || observations != 18 || len(metricNames) != 2 {
		t.Fatalf(
			"request count=%d duration observations=%d, want 18 each",
			count,
			observations,
		)
	}
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 18 {
		t.Fatalf("completion logs = %d, want 18: %s", len(lines), logs.String())
	}
	outcomes := map[string]int{}
	logSpans := map[string]string{}
	for _, line := range lines {
		if strings.Contains(line, private) {
			t.Fatalf("private data in completion log: %s", line)
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["msg"] != "Product request completed" ||
			record["duration_ms"].(float64) < 0 {
			t.Fatalf("invalid completion record: %v", record)
		}
		for _, key := range []string{"operation", "transport", "outcome", "trace_id", "span_id"} {
			if record[key] == nil {
				t.Fatalf("missing %s: %v", key, record)
			}
		}
		outcomes[record["outcome"].(string)]++
		logSpans[record["span_id"].(string)] = record["trace_id"].(string)
	}
	for outcome, want := range map[string]int{
		"success": 4, "not_found": 2, "invalid_request": 2,
		"storage_unavailable": 2, "invalid_data": 2,
		"cancelled": 2, "deadline_exceeded": 2, "internal_error": 2,
	} {
		if outcomes[outcome] != want {
			t.Fatalf(
				"outcome %s count = %d, want %d; all=%v logs=%s",
				outcome,
				outcomes[outcome],
				want,
				outcomes,
				logs.String(),
			)
		}
	}
	spanDeadline := time.Now().Add(time.Second)
	for len(spans.Ended()) < 34 && time.Now().Before(spanDeadline) {
		time.Sleep(time.Millisecond)
	}
	ended := spans.Ended()
	if len(ended) != 34 {
		t.Fatalf(
			"ended spans = %d, want 34 (18 server, 16 application): %v",
			len(ended),
			spanNames(ended),
		)
	}
	for _, span := range ended {
		switch span.Status().Description {
		case "",
			"internal error",
			"service unavailable",
			"context canceled",
			"context deadline exceeded":
		default:
			t.Fatalf("unapproved span status description %q", span.Status().Description)
		}
		switch span.Name() {
		case "GET /products", "GET /products/{productCode}",
			"product.v1.ProductService/ListProducts", "product.v1.ProductService/GetProduct",
			"product.list", "product.get":
		default:
			t.Fatalf("unapproved span name %s", span.Name())
		}
		if strings.Contains(span.Name(), "CAR") ||
			strings.Contains(span.Name(), "storage") ||
			strings.Contains(span.Name(), "decode") {
			t.Fatalf("unbounded span name: %s", span.Name())
		}
		for _, field := range span.Attributes() {
			switch string(field.Key) {
			case "insurance_hub.product.operation", "insurance_hub.product.transport",
				"insurance_hub.product.outcome", "http.request.method", "http.route",
				"http.response.status_code", "url.scheme", "network.protocol.version",
				"rpc.system.name", "rpc.method", "rpc.response.status_code", "error.type",
				"server.address", "server.port":
			default:
				t.Fatalf("unapproved span field %s on %s", field.Key, span.Name())
			}
		}
		if span.SpanKind() == trace.SpanKindServer {
			if logSpans[span.SpanContext().SpanID().String()] != span.SpanContext().
				TraceID().
				String() {
				t.Fatalf(
					"server span %s has no correlated completion log",
					span.Name(),
				)
			}
		}
	}
}

func TestRequestObserver_GRPCSendLimitUsesFinalStatus(t *testing.T) {
	logs := &completionLog{written: make(chan struct{}, 1)}
	log := logger.New(logs, "product-service", 0)
	spans := tracetest.NewSpanRecorder()
	traceProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	observer, err := telemetry.NewRequestObserver(
		log, traceProvider, meterProvider, propagation.TraceContext{},
	)
	if err != nil {
		t.Fatal(err)
	}
	list := func(ctx context.Context) (products []domain.Product, err error) {
		err = observer.Application(
			ctx,
			telemetry.OperationList,
			func(context.Context) error {
				products = []domain.Product{
					{Code: "BIG", Description: strings.Repeat("x", 1024)},
				}
				return nil
			},
		)
		return products, err
	}
	server, err := productgrpc.NewServer(
		log,
		traceProvider,
		propagation.TraceContext{},
		productgrpc.Settings{
			RequestTimeout: time.Second, MaxReceiveBytes: 1 << 20,
			MaxSendBytes: 64, Observer: observer,
		},
		list,
		func(context.Context, string) (domain.Product, error) { return domain.Product{}, nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpcgo.NewClient(
		listener.Addr().
			String(),
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	response, err := productv1.NewProductServiceClient(connection).ListProducts(
		context.Background(), &productv1.ListProductsRequest{},
	)
	if response != nil || status.Code(err) != codes.ResourceExhausted {
		t.Fatalf("response=%v status=%v error=%v", response, status.Code(err), err)
	}
	select {
	case <-logs.written:
	case <-time.After(time.Second):
		t.Fatal("gRPC completion log was not written")
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(logs.String()), &record); err != nil {
		t.Fatal(err)
	}
	if record["msg"] != "Product request completed" ||
		record["outcome"] != "invalid_request" ||
		record["rpc_status_code"] != "RESOURCE_EXHAUSTED" ||
		record["error_type"] != "invalid_request" {
		t.Fatalf("completion record: %v", record)
	}
	var metrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &metrics); err != nil {
		t.Fatal(err)
	}
	metricCount, durationCount := 0, 0
	for _, scope := range metrics.ScopeMetrics {
		for _, instrument := range scope.Metrics {
			var points []attribute.Set
			switch instrument.Name {
			case "insurance_hub.product.requests":
				for _, point := range instrument.Data.(metricdata.Sum[int64]).DataPoints {
					metricCount += int(point.Value)
					points = append(points, point.Attributes)
				}
			case "insurance_hub.product.request.duration":
				for _, point := range instrument.Data.(metricdata.Histogram[float64]).DataPoints {
					durationCount += int(point.Count)
					points = append(points, point.Attributes)
				}
			default:
				t.Fatalf("unexpected request metric %s", instrument.Name)
			}
			for _, attrs := range points {
				if value, ok := attrs.Value("rpc.response.status_code"); !ok ||
					value.AsString() != "RESOURCE_EXHAUSTED" {
					t.Fatalf("request status attributes: %v", attrs)
				}
				if value, ok := attrs.Value("insurance_hub.product.outcome"); !ok ||
					value.AsString() != "invalid_request" {
					t.Fatalf("request outcome attributes: %v", attrs)
				}
			}
		}
	}
	if metricCount != 1 || durationCount != 1 {
		t.Fatalf(
			"request metric count=%d duration count=%d, want 1 each",
			metricCount,
			durationCount,
		)
	}
	deadline := time.Now().Add(time.Second)
	for len(spans.Ended()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	serverSpans := 0
	for _, span := range spans.Ended() {
		if span.SpanKind() != trace.SpanKindServer {
			continue
		}
		serverSpans++
		attrs := attribute.NewSet(span.Attributes()...)
		if value, ok := attrs.Value("rpc.response.status_code"); !ok ||
			value.AsString() != "RESOURCE_EXHAUSTED" {
			t.Fatalf("server span status attributes: %v", attrs)
		}
		if value, ok := attrs.Value("insurance_hub.product.outcome"); !ok ||
			value.AsString() != "invalid_request" {
			t.Fatalf("server span outcome attributes: %v", attrs)
		}
		if record["trace_id"] != span.SpanContext().TraceID().String() ||
			record["span_id"] != span.SpanContext().SpanID().String() {
			t.Fatalf("completion log is not correlated with server span: %v", record)
		}
	}
	if serverSpans != 1 {
		t.Fatalf("server spans = %d, want 1", serverSpans)
	}
}

type completionLog struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	written chan struct{}
}

func (log *completionLog) Write(data []byte) (int, error) {
	log.mu.Lock()
	count, err := log.buffer.Write(data)
	log.mu.Unlock()
	select {
	case log.written <- struct{}{}:
	default:
	}
	return count, err
}

func (log *completionLog) String() string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return log.buffer.String()
}

func assertMetricFields(t *testing.T, set attribute.Set) {
	t.Helper()
	count := 0
	for iter := set.Iter(); iter.Next(); {
		key := string(iter.Attribute().Key)
		if key != "insurance_hub.product.operation" &&
			key != "insurance_hub.product.transport" &&
			key != "insurance_hub.product.outcome" &&
			key != "http.response.status_code" &&
			key != "rpc.response.status_code" {
			t.Fatalf("unapproved metric field %s", key)
		}
		count++
	}
	if count != 4 {
		t.Fatalf("metric field count = %d, want 4", count)
	}
}

func spanNames(spans []sdktrace.ReadOnlySpan) []string {
	names := make([]string, len(spans))
	for index, span := range spans {
		names[index] = span.Name() + ":" + strconv.Itoa(int(span.SpanKind()))
	}
	return names
}
