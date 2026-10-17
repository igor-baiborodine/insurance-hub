// Command telemetryacceptance owns a disposable Product process and drives bounded HTTP/gRPC reads.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

const (
	setupTimeout = 90 * time.Second
	stopTimeout  = 12 * time.Second
)

type endpoints struct {
	HTTP      string `json:"http"`
	GRPC      string `json:"grpc"`
	Health    string `json:"health"`
	PID       int    `json:"pid"`
	Container string `json:"container"`
}

type pathResult struct {
	Requests  int     `json:"requests"`
	Successes int     `json:"successes"`
	Failures  int     `json:"failures"`
	P95Millis float64 `json:"p95Millis"`
	P99Millis float64 `json:"p99Millis"`
	MaxMillis float64 `json:"maxMillis"`
}

type loadResult struct {
	StartedAt string                `json:"startedAt"`
	EndedAt   string                `json:"endedAt"`
	Rate      float64               `json:"rate"`
	MaxFlight int                   `json:"maxInFlight"`
	Paths     map[string]pathResult `json:"paths"`
	TraceIDs  []string              `json:"traceIds"`
}

type observation struct {
	duration time.Duration
	failed   bool
	traceID  string
}

type operation struct {
	name string
	call func(context.Context) error
}

func main() {
	if len(os.Args) < 2 {
		fatal("subcommand server or load is required")
	}
	var err error
	switch os.Args[1] {
	case "server":
		err = runServer(os.Args[2:])
	case "load":
		err = runLoad(os.Args[2:])
	default:
		err = fmt.Errorf("unsupported subcommand %q", os.Args[1])
	}
	if err != nil {
		fatal(err.Error())
	}
}

func runServer(arguments []string) (runErr error) {
	flags := flag.NewFlagSet("server", flag.ContinueOnError)
	endpointFile := flags.String("endpoint-file", "", "private endpoint record")
	otelEnabled := flags.Bool("otel-enabled", false, "enable Product telemetry")
	otlpEndpoint := flags.String("otlp-endpoint", "http://127.0.0.1:4317", "collector endpoint")
	version := flags.String("version", "sha-135acceptance", "service version")
	instance := flags.String(
		"instance",
		"13500000-0000-4000-8000-000000000017",
		"service instance",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *endpointFile == "" {
		return errors.New("endpoint-file is required")
	}
	if _, err := os.Stat(*endpointFile); !errors.Is(err, os.ErrNotExist) {
		return errors.New("endpoint file already exists or cannot be inspected")
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("working directory: %w", err)
	}
	fixtureRoot, err := integrationtest.ResolveBaselineRoot(workingDirectory)
	if err != nil {
		return err
	}
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), setupTimeout)
	defer cancelSetup()
	database, err := integrationtest.Start(setupCtx, integrationtest.Options{
		FixtureRoot: fixtureRoot,
		Image:       integrationtest.DefaultPostgresImage,
	})
	if err != nil {
		return fmt.Errorf("start disposable PostgreSQL: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, database.Close()) }()
	if _, err := database.LoadCatalog(setupCtx, integrationtest.FixtureQA); err != nil {
		return fmt.Errorf("load accepted QA catalog: %w", err)
	}
	selected, settings, err := serviceSettings(
		database.RuntimeDatabaseURL(), *otelEnabled, *otlpEndpoint, *version, *instance,
	)
	if err != nil {
		return err
	}
	serviceCtx, cancelService := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- service.Run(
			serviceCtx,
			settings,
			logger.NewIdentity(os.Stdout, settings.Telemetry.Identity, settings.LogLevel),
		)
	}()
	if err := waitReady(selected, done); err != nil {
		cancelService()
		<-done
		return err
	}
	record := endpoints{
		HTTP:      "http://" + selected.HTTP,
		GRPC:      selected.GRPC,
		Health:    "http://" + selected.Health,
		PID:       os.Getpid(),
		Container: database.ContainerID(),
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode endpoints: %w", err)
	}
	if err := os.WriteFile(*endpointFile, encoded, 0o600); err != nil {
		return fmt.Errorf("write endpoints: %w", err)
	}
	signalCtx, stopSignals := signal.NotifyContext(
		context.Background(), syscall.SIGINT, syscall.SIGTERM,
	)
	defer stopSignals()
	select {
	case err := <-done:
		cancelService()
		return fmt.Errorf("Product stopped unexpectedly: %w", err)
	case <-signalCtx.Done():
	}
	started := time.Now()
	cancelService()
	select {
	case err := <-done:
		if elapsed := time.Since(started); elapsed > 10*time.Second {
			return fmt.Errorf("Product shutdown exceeded ten seconds: %s", elapsed)
		}
		return err
	case <-time.After(stopTimeout):
		return errors.New("Product did not stop inside the shared shutdown budget")
	}
}

func runLoad(arguments []string) error {
	flags := flag.NewFlagSet("load", flag.ContinueOnError)
	httpEndpoint := flags.String("http", "", "Product HTTP endpoint")
	grpcEndpoint := flags.String("grpc", "", "Product gRPC endpoint")
	requestsPerPath := flags.Int("requests-per-path", 0, "request count for each of four paths")
	rate := flags.Float64("rate", 5, "aggregate requests per second")
	maxInFlight := flags.Int("max-in-flight", 8, "maximum concurrent requests")
	deadline := flags.Duration("deadline", 9*time.Second, "per-request deadline")
	tracePrefix := flags.String("trace-prefix", "acceptance", "deterministic trace namespace")
	code := flags.String("code", "CAR", "Product code for get requests")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *httpEndpoint == "" || *grpcEndpoint == "" || *requestsPerPath < 1 ||
		*rate <= 0 || *maxInFlight < 1 || *deadline <= 0 {
		return errors.New(
			"valid endpoints, positive requests-per-path/rate/max-in-flight/deadline are required",
		)
	}
	connection, err := grpcgo.NewClient(
		*grpcEndpoint, grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("create gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	grpcClient := productv1.NewProductServiceClient(connection)
	httpClient := &http.Client{Transport: traceTransport{base: http.DefaultTransport}}
	operations := []operation{
		{name: "http_list", call: func(ctx context.Context) error {
			return callHTTP(ctx, httpClient, *httpEndpoint+"/products")
		}},
		{name: "http_get", call: func(ctx context.Context) error {
			return callHTTP(ctx, httpClient, *httpEndpoint+"/products/"+*code)
		}},
		{name: "grpc_list", call: func(ctx context.Context) error {
			response, callErr := grpcClient.ListProducts(
				ctx,
				&productv1.ListProductsRequest{},
			)
			if callErr == nil && len(response.GetProducts()) == 0 {
				return errors.New("gRPC list returned no rows")
			}
			return callErr
		}},
		{name: "grpc_get", call: func(ctx context.Context) error {
			response, callErr := grpcClient.GetProduct(
				ctx,
				&productv1.GetProductRequest{Code: *code},
			)
			if callErr == nil && response.GetProduct().GetCode() != *code {
				return errors.New("gRPC get returned the wrong row")
			}
			return callErr
		}},
	}
	return executeLoad(
		operations,
		*requestsPerPath,
		*rate,
		*maxInFlight,
		*deadline,
		*tracePrefix,
	)
}

func executeLoad(
	operations []operation,
	requestsPerPath int,
	rate float64,
	maxInFlight int,
	deadline time.Duration,
	tracePrefix string,
) error {
	total := requestsPerPath * len(operations)
	results := make([][]observation, len(operations))
	for index := range results {
		results[index] = make([]observation, 0, requestsPerPath)
	}
	var resultLocks []sync.Mutex = make([]sync.Mutex, len(operations))
	semaphore := make(chan struct{}, maxInFlight)
	var active atomic.Int64
	var peak atomic.Int64
	var group sync.WaitGroup
	started := time.Now().UTC()
	interval := time.Duration(float64(time.Second) / rate)
	next := time.Now()
	for index := 0; index < total; index++ {
		if delay := time.Until(next); delay > 0 {
			time.Sleep(delay)
		}
		next = next.Add(interval)
		operationIndex := index % len(operations)
		traceID := deterministicTraceID(tracePrefix, index)
		semaphore <- struct{}{}
		group.Add(1)
		go func() {
			defer group.Done()
			defer func() { <-semaphore }()
			current := active.Add(1)
			defer active.Add(-1)
			for current > peak.Load() && !peak.CompareAndSwap(peak.Load(), current) {
			}
			ctx, cancel := context.WithTimeout(context.Background(), deadline)
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
				"traceparent", "00-"+traceID+"-0123456789abcdef-01",
			))
			startedCall := time.Now()
			err := operations[operationIndex].call(withTraceparent(ctx, traceID))
			cancel()
			resultLocks[operationIndex].Lock()
			results[operationIndex] = append(results[operationIndex], observation{
				duration: time.Since(
					startedCall,
				),
				failed:  err != nil,
				traceID: traceID,
			})
			resultLocks[operationIndex].Unlock()
		}()
	}
	group.Wait()
	measurementEnd := started.Add(time.Duration(float64(total) / rate * float64(time.Second)))
	if remaining := time.Until(measurementEnd); remaining > 0 {
		time.Sleep(remaining)
	}
	output := loadResult{
		StartedAt: started.Format(time.RFC3339Nano),
		EndedAt:   time.Now().UTC().Format(time.RFC3339Nano),
		Rate:      rate,
		MaxFlight: int(peak.Load()),
		Paths:     make(map[string]pathResult, len(operations)),
	}
	failed := false
	for index, values := range results {
		durations := make([]time.Duration, 0, len(values))
		failures := 0
		for _, value := range values {
			durations = append(durations, value.duration)
			if value.failed {
				failures++
			}
		}
		sort.Slice(
			durations,
			func(left, right int) bool { return durations[left] < durations[right] },
		)
		path := pathResult{
			Requests:  len(values),
			Successes: len(values) - failures,
			Failures:  failures,
		}
		if len(durations) > 0 {
			path.P95Millis = milliseconds(percentile(durations, 0.95))
			path.P99Millis = milliseconds(percentile(durations, 0.99))
			path.MaxMillis = milliseconds(durations[len(durations)-1])
			output.TraceIDs = append(
				output.TraceIDs,
				values[0].traceID,
				values[len(values)-1].traceID,
			)
		}
		output.Paths[operations[index].name] = path
		failed = failed || failures > 0 || len(values) != requestsPerPath
	}
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		return fmt.Errorf("encode load result: %w", err)
	}
	if failed {
		return errors.New("one or more Product workload requests failed")
	}
	return nil
}

func callHTTP(ctx context.Context, client *http.Client, url string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP status %d", response.StatusCode)
	}
	return nil
}

func withTraceparent(ctx context.Context, traceID string) context.Context {
	return context.WithValue(ctx, traceparentKey{}, traceID)
}

type traceparentKey struct{}

type traceTransport struct{ base http.RoundTripper }

func (transport traceTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if traceID, ok := request.Context().Value(traceparentKey{}).(string); ok {
		request = request.Clone(request.Context())
		request.Header.Set("traceparent", "00-"+traceID+"-0123456789abcdef-01")
	}
	return transport.base.RoundTrip(request)
}

func deterministicTraceID(prefix string, index int) string {
	sum := sha256.Sum256([]byte(prefix + ":" + strconv.Itoa(index)))
	return hex.EncodeToString(sum[:16])
}

func percentile(values []time.Duration, quantile float64) time.Duration {
	position := int(float64(len(values))*quantile+0.999999999) - 1
	if position < 0 {
		position = 0
	}
	if position >= len(values) {
		position = len(values) - 1
	}
	return values[position]
}

func milliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

type addresses struct{ HTTP, GRPC, Health string }

func serviceSettings(
	databaseURL string,
	enabled bool,
	endpoint string,
	version string,
	instance string,
) (addresses, config.Config, error) {
	selected := addresses{}
	used := make(map[string]struct{}, 3)
	for _, target := range []*string{&selected.HTTP, &selected.GRPC, &selected.Health} {
		address, err := freeDistinctAddress(used)
		if err != nil {
			return addresses{}, config.Config{}, err
		}
		*target = address
	}
	values := map[string]string{
		"PRODUCT_DATABASE_URL": databaseURL,
		"HTTP_ADDR":            selected.HTTP,
		"GRPC_ADDR":            selected.GRPC,
		"HEALTH_ADDR":          selected.Health,
		"DB_MAX_CONNS":         "8",
		"GOMEMLIMIT":           "256MiB",
		"SHUTDOWN_TIMEOUT":     "10s",
		"OTEL_ENABLED": strconv.FormatBool(
			enabled,
		),
		"OTEL_EXPORTER_OTLP_ENDPOINT": endpoint,
		"OTEL_EXPORTER_OTLP_TIMEOUT":  "2s",
		"DEPLOYMENT_ENVIRONMENT":      "qa",
		"SERVICE_VERSION":             version,
		"SERVICE_INSTANCE_ID":         instance,
		"LOG_LEVEL":                   "info",
	}
	for name, value := range values {
		if err := os.Setenv(name, value); err != nil {
			return addresses{}, config.Config{}, fmt.Errorf("set %s: %w", name, err)
		}
	}
	settings, err := config.Load()
	if err != nil {
		return addresses{}, config.Config{}, fmt.Errorf("load Product settings: %w", err)
	}
	return selected, settings, nil
}

func freeDistinctAddress(used map[string]struct{}) (string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", fmt.Errorf("allocate loopback port: %w", err)
		}
		address := listener.Addr().String()
		if err := listener.Close(); err != nil {
			return "", fmt.Errorf("release loopback port: %w", err)
		}
		if _, exists := used[address]; !exists {
			used[address] = struct{}{}
			return address, nil
		}
	}
	return "", errors.New("could not allocate distinct listener addresses")
}

func waitReady(selected addresses, done <-chan error) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return fmt.Errorf("Product stopped before readiness: %w", err)
		default:
		}
		response, err := http.Get("http://" + selected.Health + "/readyz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return errors.New("Product readiness deadline exceeded")
}

func fatal(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
