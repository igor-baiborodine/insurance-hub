//go:build integration

package integrationtest

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	transport "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/http"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/logger"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
)

func TestProductHTTP(t *testing.T) {
	image := os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE")
	if image != DefaultPostgresImage {
		t.Fatalf("PRODUCT_TEST_POSTGRES_IMAGE does not contain the pinned image")
	}
	if selection := os.Getenv(
		"PRODUCT_INTEGRATION_FIXTURE_SET",
	); selection != string(
		FixtureQA,
	) {
		t.Fatalf("fixture selection = %q, want qa", selection)
	}
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	if _, err := harness.LoadCatalog(ctx, FixtureQA); err != nil {
		t.Fatalf("load QA catalog: %v", err)
	}
	t.Setenv("PRODUCT_DATABASE_URL", harness.RuntimeDatabaseURL())
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load Product configuration: %v", err)
	}
	pool, err := postgres.OpenPool(ctx, settings.Database)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	poolClosed := false
	t.Cleanup(func() {
		if !poolClosed {
			pool.Close()
		}
	})
	reader, err := postgres.NewReader(
		pool,
		settings.Database.AcquireTimeout,
		settings.Database.QueryTimeout,
	)
	if err != nil {
		t.Fatalf("create reader: %v", err)
	}
	listProducts, err := application.NewListProducts(reader)
	if err != nil {
		t.Fatal(err)
	}
	getProduct, err := application.NewGetProduct(reader)
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	handler, err := transport.NewHandler(
		logger.New(&logs, settings.ServiceName, settings.LogLevel),
		transport.Settings{RequestTimeout: settings.RequestTimeout},
		transport.ListProducts(listProducts.Execute),
		transport.GetProduct(getProduct.Execute),
	)
	if err != nil {
		t.Fatalf("new HTTP handler: %v", err)
	}
	address, client := startHTTPIntegrationServer(t, settings, handler)
	qa := loadQAHTTPExpectations(t, baselineRoot)
	failures := loadHTTPFailureExpectations(t, baselineRoot)

	t.Run("lists and gets the accepted QA catalog anonymously", func(t *testing.T) {
		assertHTTPExpectation(t, client, address, "/products", qa["HTTP-DIRECT-LIST-001"])
		for _, code := range []string{"CAR", "FAI", "HSI", "TRI"} {
			assertHTTPExpectation(
				t,
				client,
				address,
				"/products/"+code,
				qa["HTTP-DIRECT-GET-"+code+"-001"],
			)
		}
	})

	t.Run("preserves exact encoded lookup and self links", func(t *testing.T) {
		for _, id := range []string{
			"FAIL-PRODUCT-MISSING-001",
			"FAIL-PRODUCT-LOOKUP-LOWERCASE-001",
			"FAIL-PRODUCT-LOOKUP-WHITESPACE-001",
			"FAIL-PRODUCT-LOOKUP-ENCODED-SLASH-001",
		} {
			entry := failures[id]
			assertHTTPExpectation(t, client, address, entry.Path, entry.Expectation)
		}
	})

	t.Run("preserves malformed URI and trailing slash behavior", func(t *testing.T) {
		malformed := failures["FAIL-PRODUCT-LOOKUP-BAD-ENCODING-001"]
		actual := rawHTTPIntegrationGET(t, address, malformed.Path)
		if err := CompareHTTP(malformed.Expectation, actual); err != nil {
			t.Errorf("malformed URI parity: %v", err)
		}
		assertHTTPExpectation(t, client, address, "/products/", qa["HTTP-DIRECT-LIST-001"])
	})

	t.Run("returns an empty catalog", func(t *testing.T) {
		if err := harness.ResetRows(ctx, nil); err != nil {
			t.Fatalf("reset empty catalog: %v", err)
		}
		assertHTTPExpectation(t, client, address, "/products", HTTPExpectation{
			Status: http.StatusOK, ContentType: "application/json", Body: []byte("[]"),
		})
	})

	t.Run("maps corrupt storage to generic errors without partial success", func(t *testing.T) {
		if err := harness.ResetRows(ctx, []FixtureRow{
			{Code: "GOOD", RawLosslessDefinitionJSON: `{}`},
			{
				Code:                      "CORRUPT",
				RawLosslessDefinitionJSON: `{"questions":[{"type":"numeric","unexpected":true}]}`,
			},
		}); err != nil {
			t.Fatalf("reset corrupt catalog: %v", err)
		}
		want := HTTPExpectation{
			Status: http.StatusInternalServerError, ContentType: "application/json",
			Body: []byte(`{"message":"Internal Server Error"}`),
		}
		assertHTTPExpectation(t, client, address, "/products", want)
		assertHTTPExpectation(t, client, address, "/products/CORRUPT", want)
		assertSafeHTTPLogs(t, &logs)
	})

	t.Run("maps unavailable database without retry or detail", func(t *testing.T) {
		pool.Close()
		poolClosed = true
		want := HTTPExpectation{
			Status: http.StatusInternalServerError, ContentType: "application/json",
			Body: []byte(`{"message":"Internal Server Error"}`),
		}
		assertHTTPExpectation(t, client, address, "/products", want)
		assertHTTPExpectation(t, client, address, "/products/CAR", want)
		assertSafeHTTPLogs(t, &logs)
	})
}

type httpFailureExpectation struct {
	Path        string
	Expectation HTTPExpectation
}

func loadQAHTTPExpectations(t *testing.T, baselineRoot string) map[string]HTTPExpectation {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "http", "qa.json"))
	if err != nil {
		t.Fatalf("read QA HTTP expectations: %v", err)
	}
	var fixture struct {
		Observations []struct {
			ScenarioID string `json:"scenarioId"`
			Boundary   string `json:"boundary"`
			Response   struct {
				Status  int    `json:"status"`
				RawBody string `json:"rawBody"`
				Headers struct {
					ContentType string `json:"contentType"`
				} `json:"headers"`
			} `json:"response"`
		} `json:"observations"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode QA HTTP expectations: %v", err)
	}
	result := make(map[string]HTTPExpectation)
	for _, observation := range fixture.Observations {
		if observation.Boundary == "direct" {
			result[observation.ScenarioID] = HTTPExpectation{
				Status:      observation.Response.Status,
				ContentType: observation.Response.Headers.ContentType,
				Body:        []byte(observation.Response.RawBody),
			}
		}
	}
	if len(result) != 5 {
		t.Fatalf("QA direct HTTP expectations = %d, want 5", len(result))
	}
	return result
}

func loadHTTPFailureExpectations(
	t *testing.T,
	baselineRoot string,
) map[string]httpFailureExpectation {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "failures", "cases.json"))
	if err != nil {
		t.Fatalf("read HTTP failure expectations: %v", err)
	}
	var fixture struct {
		Cases []struct {
			ID       string `json:"id"`
			Target   string `json:"target"`
			RawPath  string `json:"rawPath"`
			Expected struct {
				Status      int    `json:"status"`
				ContentType string `json:"contentType"`
				RawBody     string `json:"rawBody"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode HTTP failure expectations: %v", err)
	}
	result := make(map[string]httpFailureExpectation)
	for _, entry := range fixture.Cases {
		if entry.Target == "product" {
			result[entry.ID] = httpFailureExpectation{
				Path: entry.RawPath,
				Expectation: HTTPExpectation{
					Status:      entry.Expected.Status,
					ContentType: entry.Expected.ContentType,
					Body:        []byte(entry.Expected.RawBody),
				},
			}
		}
	}
	return result
}

func startHTTPIntegrationServer(
	t *testing.T,
	settings config.Config,
	handler http.Handler,
) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: settings.HTTPServer.ReadHeaderTimeout,
		ReadTimeout:       settings.HTTPServer.ReadTimeout,
		WriteTimeout:      settings.HTTPServer.WriteTimeout,
		IdleTimeout:       settings.HTTPServer.IdleTimeout,
	}
	serveResult := make(chan error, 1)
	go func() { serveResult <- transport.Serve(server, listener) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				t.Errorf("serve HTTP: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("HTTP server did not stop")
		}
	})
	return listener.Addr().String(), &http.Client{Timeout: 5 * time.Second}
}

func assertHTTPExpectation(
	t *testing.T,
	client *http.Client,
	address string,
	path string,
	want HTTPExpectation,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, "http://"+address+path, nil,
	)
	if err != nil {
		t.Fatalf("create HTTP request %s: %v", path, err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("execute HTTP request %s: %v", path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read HTTP response %s: %v", path, err)
	}
	actual := HTTPExpectation{
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}
	if err := CompareHTTP(want, actual); err != nil {
		t.Errorf("HTTP parity %s: %v", path, err)
	}
}

func rawHTTPIntegrationGET(t *testing.T, address, path string) HTTPExpectation {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatalf("dial HTTP server: %v", err)
	}
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := fmt.Fprintf(
		connection, "GET %s HTTP/1.1\r\nHost: localhost\r\nConnection: close\r\n\r\n", path,
	); err != nil {
		t.Fatalf("write raw HTTP request: %v", err)
	}
	response, err := http.ReadResponse(
		bufio.NewReader(connection),
		&http.Request{Method: http.MethodGet},
	)
	if err != nil {
		t.Fatalf("read raw HTTP response: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read raw HTTP body: %v", err)
	}
	return HTTPExpectation{
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        body,
	}
}

func assertSafeHTTPLogs(t *testing.T, logs *bytes.Buffer) {
	t.Helper()
	for _, marker := range []string{
		"postgres", "reader", "password", "secret", "definition", "127.0.0.1",
	} {
		if strings.Contains(logs.String(), marker) {
			t.Fatalf("unsafe marker %q exposed in logs: %s", marker, logs.String())
		}
	}
}
