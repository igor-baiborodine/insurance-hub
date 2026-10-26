//go:build image

package integrationtest

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
)

const (
	imageHTTPPort   = "8080/tcp"
	imageGRPCPort   = "9090/tcp"
	imageHealthPort = "8081/tcp"
)

func TestProductRuntimeImage(t *testing.T) {
	image := os.Getenv("PRODUCT_TEST_IMAGE")
	if image == "" {
		t.Fatal("PRODUCT_TEST_IMAGE is required")
	}
	if _, configured := os.LookupEnv("PRODUCT_DATABASE_URL"); configured {
		t.Fatal("PRODUCT_DATABASE_URL must be unset")
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
	harness, err := Start(ctx, Options{
		FixtureRoot: baselineRoot,
		Image:       DefaultPostgresImage,
	})
	if err != nil {
		t.Fatalf("start disposable PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := harness.Close(); closeErr != nil {
			t.Errorf("close disposable PostgreSQL: %v", closeErr)
		}
	})
	fixture, err := harness.LoadCatalog(ctx, FixtureQA)
	if err != nil {
		t.Fatalf("load accepted QA catalog: %v", err)
	}
	before, err := harness.Snapshot(ctx)
	if err != nil {
		t.Fatalf("capture catalog before image startup: %v", err)
	}
	role, err := harness.InspectRole(ctx)
	if err != nil {
		t.Fatalf("inspect runtime role: %v", err)
	}
	if role.CurrentUser != "go_product_reader" || !role.CanConnect || !role.CanUseSchema ||
		!role.CanSelect || role.CanCreateInDatabase || role.CanCreateInSchema || role.CanInsert ||
		role.CanUpdate || role.CanDelete || role.CanTruncate || role.OwnsDatabase ||
		role.OwnsSchema || role.OwnsTable || role.Superuser || role.CreateDatabase ||
		role.CreateRole || role.Inherit || role.Replication || role.BypassRLS ||
		len(role.MemberOfRoles) != 0 || role.PublicTableGrants != 0 ||
		role.PublicDefaultGrants != 0 || role.ReaderDefaultGrants != 0 {
		t.Fatalf("runtime role does not satisfy the restricted reader contract: %#v", role)
	}

	databaseURL, databasePort := containerDatabaseURL(t, harness.RuntimeDatabaseURL())
	runtime, err := testcontainers.Run(
		ctx,
		image,
		testcontainers.WithEnv(map[string]string{
			"SERVICE_NAME":         "product-service-image-check",
			"HTTP_ADDR":            "0.0.0.0:8080",
			"GRPC_ADDR":            "0.0.0.0:9090",
			"HEALTH_ADDR":          "0.0.0.0:8081",
			"PRODUCT_DATABASE_URL": databaseURL,
			"DB_MAX_CONNS":         "4",
			"STARTUP_TIMEOUT":      "10s",
			"SHUTDOWN_TIMEOUT":     "5s",
			"OTEL_ENABLED":         "false",
		}),
		testcontainers.WithExposedPorts(imageHTTPPort, imageGRPCPort, imageHealthPort),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			config.ExtraHosts = append(
				config.ExtraHosts,
				testcontainers.HostInternal+":host-gateway",
			)
			config.ReadonlyRootfs = true
			config.CapDrop = append(config.CapDrop, "ALL")
			config.SecurityOpt = append(config.SecurityOpt, "no-new-privileges")
		}),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/readyz").
				WithPort(imageHealthPort).
				WithStartupTimeout(45*time.Second),
		),
	)
	if err != nil {
		t.Fatalf(
			"start Product runtime image using disposable database port %d: %v",
			databasePort,
			err,
		)
	}
	t.Cleanup(func() {
		if terminateErr := runtime.Terminate(context.Background()); terminateErr != nil {
			t.Errorf("terminate Product runtime image: %v", terminateErr)
		}
	})

	httpEndpoint := containerEndpoint(t, ctx, runtime, imageHTTPPort, "http")
	healthEndpoint := containerEndpoint(t, ctx, runtime, imageHealthPort, "http")
	grpcEndpoint := containerEndpoint(t, ctx, runtime, imageGRPCPort, "")
	assertImageHealth(t, healthEndpoint)
	assertImageHTTPReads(t, httpEndpoint, fixture)
	assertImageRouteSeparation(t, httpEndpoint, healthEndpoint)
	assertImageGRPCReads(t, ctx, grpcEndpoint, fixture)

	stopTimeout := 15 * time.Second
	stopStarted := time.Now()
	if err := runtime.Stop(ctx, &stopTimeout); err != nil {
		t.Fatalf("send SIGTERM and stop Product runtime image: %v", err)
	}
	if elapsed := time.Since(stopStarted); elapsed > stopTimeout {
		t.Fatalf("Product runtime image shutdown took %s, limit %s", elapsed, stopTimeout)
	}
	state, err := runtime.State(ctx)
	if err != nil {
		t.Fatalf("inspect stopped Product runtime image: %v", err)
	}
	if state.Running || state.OOMKilled || state.Dead || state.ExitCode != 0 {
		t.Fatalf("Product runtime image exit state = %#v, want clean SIGTERM exit", state)
	}
	logs, err := runtime.Logs(ctx)
	if err != nil {
		t.Fatalf("read Product runtime logs: %v", err)
	}
	logContent, readErr := io.ReadAll(logs)
	closeErr := logs.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close Product runtime logs: %v/%v", readErr, closeErr)
	}
	parsedRuntimeURL, err := url.Parse(harness.RuntimeDatabaseURL())
	if err != nil {
		t.Fatal("parse runtime URL for redaction check")
	}
	password, hasPassword := parsedRuntimeURL.User.Password()
	if !hasPassword || password == "" {
		t.Fatal("disposable runtime password is missing")
	}
	if strings.Contains(string(logContent), password) ||
		strings.Contains(string(logContent), harness.RuntimeDatabaseURL()) {
		t.Fatal("Product runtime logs contain the disposable credential")
	}

	after, err := harness.Snapshot(ctx)
	if err != nil {
		t.Fatalf("capture catalog after image shutdown: %v", err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf(
			"catalog changed during image lifecycle: before=%#v after=%#v",
			before,
			after,
		)
	}
}

func containerDatabaseURL(t *testing.T, raw string) (string, int) {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal("parse disposable reader URL")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		t.Fatal("disposable reader URL does not contain a valid host port")
	}
	parsed.Host = net.JoinHostPort(testcontainers.HostInternal, parsed.Port())
	return parsed.String(), port
}

func containerEndpoint(
	t *testing.T,
	ctx context.Context,
	runtime *testcontainers.DockerContainer,
	port string,
	protocol string,
) string {
	t.Helper()
	endpoint, err := runtime.PortEndpoint(ctx, port, protocol)
	if err != nil {
		t.Fatalf("resolve container endpoint for %s: %v", port, err)
	}
	return endpoint
}

func assertImageHealth(t *testing.T, endpoint string) {
	t.Helper()
	for _, probe := range []struct {
		path string
		body string
	}{
		{path: "/livez", body: "live\n"},
		{path: "/readyz", body: "ready\n"},
	} {
		response, err := http.Get(
			endpoint + probe.path,
		) //nolint:gosec // Disposable loopback endpoint.
		if err != nil {
			t.Fatalf("request image health %s: %v", probe.path, err)
		}
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusOK ||
			string(body) != probe.body {
			t.Fatalf(
				"image health %s status/body/read/close = %d/%q/%v/%v",
				probe.path,
				response.StatusCode,
				body,
				readErr,
				closeErr,
			)
		}
	}
}

func assertImageRouteSeparation(t *testing.T, httpEndpoint, healthEndpoint string) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	for _, route := range []struct {
		name string
		url  string
	}{
		{name: "business listener rejects liveness", url: httpEndpoint + "/livez"},
		{name: "business listener rejects readiness", url: httpEndpoint + "/readyz"},
		{name: "management listener rejects catalog", url: healthEndpoint + "/products"},
	} {
		response, err := client.Get(
			route.url,
		) //nolint:gosec // Disposable loopback endpoint.
		if err != nil {
			t.Fatalf("%s: %v", route.name, err)
		}
		_, readErr := io.Copy(io.Discard, io.LimitReader(response.Body, 1024))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || response.StatusCode != http.StatusNotFound {
			t.Errorf("%s status/read/close = %d/%v/%v, want 404", route.name,
				response.StatusCode, readErr, closeErr)
		}
	}
}

func assertImageHTTPReads(t *testing.T, endpoint string, fixture CatalogFixture) {
	t.Helper()
	response, err := http.Get(
		endpoint + "/products",
	) //nolint:gosec // Disposable loopback endpoint.
	if err != nil {
		t.Fatalf("list products through image HTTP endpoint: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "application/json" {
		t.Fatalf(
			"image HTTP list status/content-type = %d/%q",
			response.StatusCode,
			response.Header.Get("Content-Type"),
		)
	}
	var products []struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).
		Decode(&products); err != nil {
		t.Fatalf("decode image HTTP list: %v", err)
	}
	actual := make([]string, 0, len(products))
	for _, product := range products {
		actual = append(actual, product.Code)
	}
	sort.Strings(actual)
	want := append([]string(nil), fixture.DataIdentity.Codes...)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("image HTTP product codes = %v, want %v", actual, want)
	}
}

func assertImageGRPCReads(
	t *testing.T,
	ctx context.Context,
	endpoint string,
	fixture CatalogFixture,
) {
	t.Helper()
	connection, err := grpcgo.NewClient(
		endpoint,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("connect to image gRPC endpoint: %v", err)
	}
	defer func() { _ = connection.Close() }()
	client := productv1.NewProductServiceClient(connection)
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	list, err := client.ListProducts(requestCtx, &productv1.ListProductsRequest{})
	if err != nil {
		t.Fatalf("list products through image gRPC endpoint: %v", err)
	}
	actual := make([]string, 0, len(list.GetProducts()))
	for _, product := range list.GetProducts() {
		actual = append(actual, product.GetCode())
	}
	sort.Strings(actual)
	want := append([]string(nil), fixture.DataIdentity.Codes...)
	sort.Strings(want)
	if !reflect.DeepEqual(actual, want) {
		t.Fatalf("image gRPC product codes = %v, want %v", actual, want)
	}
	item, err := client.GetProduct(requestCtx, &productv1.GetProductRequest{Code: "TRI"})
	if err != nil || item.GetProduct().GetCode() != "TRI" {
		t.Fatalf("get TRI through image gRPC endpoint: response=%v error=%v", item, err)
	}
}
