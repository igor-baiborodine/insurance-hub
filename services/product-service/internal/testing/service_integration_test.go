//go:build integration

package integrationtest

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

func TestProductStartup(t *testing.T) {
	// given
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

	t.Run("serves both transports and tracks dependency readiness", func(t *testing.T) {
		// given
		addresses := serviceAddresses{
			http:   freeAddress(t),
			grpc:   freeAddress(t),
			health: freeAddress(t),
		}
		settings := loadServiceSettings(t, harness.RuntimeDatabaseURL(), addresses)
		serviceCtx, stopService := context.WithCancel(context.Background())
		result := make(chan error, 1)
		go func() {
			result <- service.Run(
				serviceCtx,
				settings,
				slog.New(slog.NewTextHandler(io.Discard, nil)),
			)
		}()
		t.Cleanup(stopService)

		// when
		waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)
		liveStatus := httpStatus(t, addresses.health, "/livez")
		listStatus, listBody := httpBody(t, addresses.http, "/products")
		grpcConnection, err := grpcgo.NewClient(
			addresses.grpc,
			grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			t.Fatalf("create gRPC client: %v", err)
		}
		defer func() { _ = grpcConnection.Close() }()
		grpcResponse, grpcErr := productv1.NewProductServiceClient(grpcConnection).
			ListProducts(ctx, &productv1.ListProductsRequest{})

		// then
		if liveStatus != http.StatusOK {
			t.Errorf("liveness status = %d", liveStatus)
		}
		if listStatus != http.StatusOK || string(listBody) != "[]" {
			t.Errorf("empty HTTP catalog = (%d, %q)", listStatus, listBody)
		}
		if grpcErr != nil || len(grpcResponse.GetProducts()) != 0 {
			t.Errorf("empty gRPC catalog = (%v, %v)", grpcResponse, grpcErr)
		}

		// when
		if _, err := harness.adminPool.Exec(
			ctx,
			"REVOKE SELECT ON TABLE public.product FROM "+runtimeRole,
		); err != nil {
			t.Fatalf("revoke reader access: %v", err)
		}
		accessRestored := false
		defer func() {
			if !accessRestored {
				execAdmin(
					t,
					harness,
					"GRANT SELECT ON TABLE public.product TO "+runtimeRole,
				)
			}
		}()
		waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusServiceUnavailable)
		liveDuringLoss := httpStatus(t, addresses.health, "/livez")
		if _, err := harness.adminPool.Exec(
			ctx,
			"GRANT SELECT ON TABLE public.product TO "+runtimeRole,
		); err != nil {
			t.Fatalf("restore reader access: %v", err)
		}
		accessRestored = true
		waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)

		// then
		if liveDuringLoss != http.StatusOK {
			t.Errorf("liveness during dependency loss = %d", liveDuringLoss)
		}

		// when
		stopService()

		// then
		select {
		case err := <-result:
			if err != nil {
				t.Errorf("run Product service: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Product service did not stop within the test bound")
		}
		waitForReaderConnections(t, harness, 0)
	})

	t.Run("fails startup safely for database dependencies", func(t *testing.T) {
		tests := []struct {
			name        string
			databaseURL func() string
			before      func()
			after       func()
		}{
			{
				name: "bad credentials",
				databaseURL: func() string {
					return databaseURLWithPassword(
						t,
						harness.RuntimeDatabaseURL(),
						"bad-password",
					)
				},
			},
			{
				name: "unavailable database",
				databaseURL: func() string {
					return databaseURLWithHost(
						t,
						harness.RuntimeDatabaseURL(),
						freeAddress(t),
					)
				},
			},
			{
				name:        "missing table",
				databaseURL: harness.RuntimeDatabaseURL,
				before: func() {
					execAdmin(
						t,
						harness,
						"ALTER TABLE public.product RENAME TO product_hidden",
					)
				},
				after: func() {
					execAdmin(
						t,
						harness,
						"ALTER TABLE public.product_hidden RENAME TO product",
					)
				},
			},
			{
				name:        "insufficient read privilege",
				databaseURL: harness.RuntimeDatabaseURL,
				before: func() {
					execAdmin(
						t,
						harness,
						"REVOKE SELECT ON TABLE public.product FROM "+runtimeRole,
					)
				},
				after: func() {
					execAdmin(
						t,
						harness,
						"GRANT SELECT ON TABLE public.product TO "+runtimeRole,
					)
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				// given
				if test.before != nil {
					test.before()
				}
				if test.after != nil {
					defer test.after()
				}
				settings := loadServiceSettings(
					t,
					test.databaseURL(),
					serviceAddresses{
						http: freeAddress(
							t,
						),
						grpc:   freeAddress(t),
						health: freeAddress(t),
					},
				)

				// when
				err := service.Run(
					context.Background(),
					settings,
					slog.New(slog.NewTextHandler(io.Discard, nil)),
				)

				// then
				if err == nil {
					t.Fatal("Run() error = nil")
				}
				if strings.Contains(err.Error(), "bad-password") ||
					strings.Contains(
						err.Error(),
						harness.RuntimeDatabaseURL(),
					) {
					t.Fatalf(
						"startup error exposed database credentials: %v",
						err,
					)
				}
				waitForReaderConnections(t, harness, 0)
			})
		}
	})

	t.Run("cleans up after every listener collision", func(t *testing.T) {
		for _, listenerName := range []string{"http", "grpc", "health"} {
			t.Run(listenerName, func(t *testing.T) {
				// given
				occupied, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatalf("occupy listener: %v", err)
				}
				defer func() { _ = occupied.Close() }()
				addresses := serviceAddresses{
					http: freeAddress(
						t,
					),
					grpc:   freeAddress(t),
					health: freeAddress(t),
				}
				switch listenerName {
				case "http":
					addresses.http = occupied.Addr().String()
				case "grpc":
					addresses.grpc = occupied.Addr().String()
				case "health":
					addresses.health = occupied.Addr().String()
				}
				settings := loadServiceSettings(
					t,
					harness.RuntimeDatabaseURL(),
					addresses,
				)

				// when
				err = service.Run(
					context.Background(),
					settings,
					slog.New(slog.NewTextHandler(io.Discard, nil)),
				)

				// then
				if err == nil || !strings.Contains(err.Error(), "listen for") {
					t.Fatalf("listener collision error = %v", err)
				}
				waitForReaderConnections(t, harness, 0)
			})
		}
	})
}

type serviceAddresses struct {
	http   string
	grpc   string
	health string
}

func loadServiceSettings(
	t *testing.T,
	databaseURL string,
	addresses serviceAddresses,
) config.Config {
	t.Helper()
	t.Setenv("PRODUCT_DATABASE_URL", databaseURL)
	t.Setenv("HTTP_ADDR", addresses.http)
	t.Setenv("GRPC_ADDR", addresses.grpc)
	t.Setenv("HEALTH_ADDR", addresses.health)
	t.Setenv("DB_CONNECT_TIMEOUT", "500ms")
	t.Setenv("DB_ACQUIRE_TIMEOUT", "300ms")
	t.Setenv("DB_QUERY_TIMEOUT", "700ms")
	t.Setenv("STARTUP_TIMEOUT", "2s")
	t.Setenv("REQUEST_TIMEOUT", "1s")
	t.Setenv("PROBE_TIMEOUT", "300ms")
	t.Setenv("HTTP_READ_HEADER_TIMEOUT", "500ms")
	t.Setenv("HTTP_READ_TIMEOUT", "1s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "1s")
	t.Setenv("HTTP_IDLE_TIMEOUT", "1s")
	t.Setenv("SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("OTEL_ENABLED", "false")
	settings, err := config.Load()
	if err != nil {
		t.Fatalf("load service settings: %v", err)
	}
	return settings
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate loopback address: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback address: %v", err)
	}
	return address
}

func waitForHTTPStatus(t *testing.T, address, path string, want int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := tryHTTPStatus(address, path)
		if err == nil && status == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf(
				"GET %s%s did not reach status %d; last=(%d, %v)",
				address,
				path,
				want,
				status,
				err,
			)
		case <-ticker.C:
		}
	}
}

func httpStatus(t *testing.T, address, path string) int {
	t.Helper()
	status, err := tryHTTPStatus(address, path)
	if err != nil {
		t.Fatalf("GET %s%s: %v", address, path, err)
	}
	return status
}

func tryHTTPStatus(address, path string) (int, error) {
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + address + path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, response.Body)
	return response.StatusCode, nil
}

func httpBody(t *testing.T, address, path string) (int, []byte) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get("http://" + address + path)
	if err != nil {
		t.Fatalf("GET %s%s: %v", address, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read GET %s%s: %v", address, path, err)
	}
	return response.StatusCode, body
}

func waitForReaderConnections(t *testing.T, harness *Harness, want int) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := harness.adminPool.QueryRow(
			context.Background(),
			"SELECT count(*)::integer FROM pg_stat_activity "+
				"WHERE datname = current_database() AND usename = $1",
			runtimeRole,
		).Scan(&count)
		if err == nil && count == want {
			return
		}
		select {
		case <-deadline.C:
			t.Fatalf(
				"reader connections did not reach %d; last=(%d, %v)",
				want,
				count,
				err,
			)
		case <-ticker.C:
		}
	}
}

func execAdmin(t *testing.T, harness *Harness, statement string) {
	t.Helper()
	if _, err := harness.adminPool.Exec(context.Background(), statement); err != nil {
		t.Fatalf("execute admin statement: %v", err)
	}
}

func databaseURLWithPassword(t *testing.T, databaseURL, password string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	username := parsed.User.Username()
	parsed.User = url.UserPassword(username, password)
	return parsed.String()
}

func databaseURLWithHost(t *testing.T, databaseURL, host string) string {
	t.Helper()
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatalf("parse database URL: %v", err)
	}
	parsed.Host = host
	return parsed.String()
}
