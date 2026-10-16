//go:build integration

package integrationtest

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

// TestProductContinuity uses the production HTTP observer/propagator and Brave's
// actual inject/extract implementation through a disposable Java JUnit fixture.
func TestProductContinuity(t *testing.T) {
	// given
	if os.Getenv("PRODUCT_CONTINUITY_REQUIRED") != "1" {
		t.Fatal("run through make test-telemetry-continuity")
	}
	propagatorProvider, err := telemetry.New(
		context.Background(),
		"product-service",
		config.Telemetry{},
	)
	if err != nil {
		t.Fatal(err)
	}
	propagator := propagatorProvider.HTTPPropagator()
	recorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())),
		sdktrace.WithSpanProcessor(recorder),
	)
	t.Cleanup(func() { _ = tracerProvider.Shutdown(context.Background()) })
	observer, err := telemetry.NewRequestObserver(
		slog.New(slog.NewTextHandler(io.Discard, nil)), tracerProvider,
		noop.NewMeterProvider(), propagator,
	)
	if err != nil {
		t.Fatal(err)
	}
	var handlerErrorsMu sync.Mutex
	var handlerErrors []string
	server := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			switch request.URL.Path {
			case "/java-to-go":
				parent := trace.SpanContextFromContext(propagator.Extract(
					request.Context(),
					propagation.HeaderCarrier(request.Header),
				))
				observed, state := observer.StartHTTP(
					request,
					telemetry.OperationList,
					"/products",
				)
				span := trace.SpanFromContext(observed.Context())
				if span.IsRecording() {
					recorded, ok := span.(sdktrace.ReadWriteSpan)
					if !ok {
						http.Error(
							writer,
							"unexpected span implementation",
							http.StatusInternalServerError,
						)
						state.Finish(
							observed.Context(),
							"http.response.status_code",
							"500",
						)
						return
					}
					parent = recorded.Parent()
				}
				_, _ = fmt.Fprintf(
					writer,
					"%s;%s;%t",
					span.SpanContext().TraceID(),
					parent.SpanID(),
					span.IsRecording(),
				)
				state.Finish(observed.Context(), "http.response.status_code", "200")
			case "/go-to-java":
				port, parseErr := strconv.Atoi(request.URL.Query().Get("port"))
				if parseErr != nil || port < 1 || port > 65535 {
					http.Error(writer, "invalid port", http.StatusBadRequest)
					return
				}
				parentContext := request.Context()
				if request.URL.Query().Get("sampled") == "0" {
					parentContext = trace.ContextWithRemoteSpanContext(
						parentContext,
						trace.NewSpanContext(
							trace.SpanContextConfig{
								TraceID: trace.TraceID{
									1,
								},
								SpanID: trace.SpanID{2},
								Remote: true,
							},
						),
					)
				}
				ctx, span := tracerProvider.Tracer("continuity-test").Start(
					parentContext,
					"GET java fixture", trace.WithSpanKind(trace.SpanKindClient),
				)
				defer span.End()
				outbound, makeErr := http.NewRequestWithContext(
					ctx,
					http.MethodGet,
					fmt.Sprintf("http://127.0.0.1:%d/products", port),
					nil,
				)
				if makeErr != nil {
					http.Error(
						writer,
						"construct request",
						http.StatusInternalServerError,
					)
					return
				}
				propagator.Inject(ctx, propagation.HeaderCarrier(outbound.Header))
				client := &http.Client{Timeout: 3 * time.Second}
				response, callErr := client.Do(outbound)
				if callErr != nil {
					http.Error(
						writer,
						"call Java fixture",
						http.StatusBadGateway,
					)
					return
				}
				defer response.Body.Close()
				body, readErr := io.ReadAll(io.LimitReader(response.Body, 512))
				if readErr != nil || response.StatusCode != http.StatusOK {
					http.Error(
						writer,
						"read Java fixture",
						http.StatusBadGateway,
					)
					return
				}
				parts := strings.Split(string(body), ";")
				if len(parts) != 4 ||
					parts[0] != span.SpanContext().TraceID().String() ||
					parts[1] != span.SpanContext().
						SpanID().
						String() ||
					parts[2] != strconv.FormatBool(
						span.SpanContext().IsSampled(),
					) ||
					parts[3] != outbound.Header.Get("Traceparent") {
					handlerErrorsMu.Lock()
					handlerErrors = append(handlerErrors, fmt.Sprintf(
						"Java context mismatch: fields=%d trace=%t span=%t sampled=%t w3c=%t",
						len(
							parts,
						),
						len(parts) > 0 &&
							parts[0] == span.SpanContext().
								TraceID().
								String(),
						len(parts) > 1 &&
							parts[1] == span.SpanContext().
								SpanID().
								String(),
						len(parts) > 2 &&
							parts[2] == strconv.FormatBool(
								span.SpanContext().IsSampled(),
							),
						len(parts) > 3 &&
							parts[3] == outbound.Header.Get(
								"Traceparent",
							),
					))
					handlerErrorsMu.Unlock()
					http.Error(
						writer,
						"Java context differs from Go client span",
						http.StatusBadGateway,
					)
					return
				}
				_, _ = io.WriteString(writer, "ok")
			default:
				http.NotFound(writer, request)
			}
		}),
	)
	t.Cleanup(server.Close)

	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"mvn",
		"-q",
		"-Dtest=TraceContinuityFixtureTest",
		"test",
	)
	command.Dir = filepath.Join(root, "legacy", "product-service")
	command.Env = append(os.Environ(),
		"PRODUCT_GO_CONTINUITY_URL="+server.URL,
		"TRACING_ZIPKIN_SAMPLER_PROBABILITY=1.0",
	)

	// when
	output, runErr := command.CombinedOutput()
	server.Close()

	// then
	if runErr != nil {
		t.Fatalf("Brave fixture failed: %v\n%s", runErr, output)
	}
	handlerErrorsMu.Lock()
	for _, message := range handlerErrors {
		t.Error(message)
	}
	handlerErrorsMu.Unlock()
	reportPath := filepath.Join(
		command.Dir,
		"target",
		"surefire-reports",
		"TEST-pl.altkom.asc.lab.micronaut.poc.product.service.TraceContinuityFixtureTest.xml",
	)
	reportBytes, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("read Brave fixture result: %v", err)
	}
	var report struct {
		Tests    int `xml:"tests,attr"`
		Errors   int `xml:"errors,attr"`
		Failures int `xml:"failures,attr"`
		Skipped  int `xml:"skipped,attr"`
	}
	if err := xml.Unmarshal(reportBytes, &report); err != nil {
		t.Fatalf("parse Brave fixture result: %v", err)
	}
	if report.Tests != 2 || report.Errors != 0 || report.Failures != 0 || report.Skipped != 0 {
		t.Errorf("Brave fixture results: %+v, want two passed tests", report)
	}
	var javaParents int
	for _, span := range recorder.Ended() {
		if span.Name() == "GET /products" && span.Parent().IsRemote() {
			javaParents++
		}
	}
	if javaParents < 2 {
		t.Errorf(
			"ended Go HTTP spans with Java remote parents = %d, want at least 2",
			javaParents,
		)
	}
}
