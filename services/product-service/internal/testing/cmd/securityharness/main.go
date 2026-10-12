// Command securityharness runs the isolated Product side of the gateway security smoke test.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
	integrationtest "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/testing"
)

const (
	setupDeadline = 90 * time.Second
	readyDeadline = 30 * time.Second
	stopDeadline  = 15 * time.Second
)

type endpointRecord struct {
	HTTP           string `json:"http"`
	GRPC           string `json:"grpc"`
	Health         string `json:"health"`
	Nonce          string `json:"nonce"`
	Fixture        string `json:"fixture"`
	RowCount       int    `json:"rowCount"`
	DataIdentity   string `json:"dataIdentity"`
	SchemaIdentity string `json:"schemaIdentity"`
	Reader         string `json:"reader"`
	ContainerID    string `json:"containerId"`
	PID            int    `json:"pid"`
}

func main() {
	endpointFile := flag.String("endpoint-file", "", "private endpoint record path")
	stopFile := flag.String("stop-file", "", "private graceful-stop marker path")
	flag.Parse()
	if *endpointFile == "" || *stopFile == "" {
		fmt.Fprintln(
			os.Stderr,
			"security harness: endpoint-file and stop-file are required",
		)
		os.Exit(2)
	}
	if err := run(*endpointFile, *stopFile); err != nil {
		fmt.Fprintf(os.Stderr, "security harness: %v\n", err)
		os.Exit(1)
	}
}

func run(endpointFile, stopFile string) (runErr error) {
	if _, present := os.LookupEnv("PRODUCT_DATABASE_URL"); present {
		return errors.New("PRODUCT_DATABASE_URL must be unset")
	}
	if _, err := os.Stat(endpointFile); !errors.Is(err, os.ErrNotExist) {
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
	setupCtx, cancelSetup := context.WithTimeout(context.Background(), setupDeadline)
	defer cancelSetup()
	harness, err := integrationtest.Start(setupCtx, integrationtest.Options{
		FixtureRoot: fixtureRoot,
		Image:       integrationtest.DefaultPostgresImage,
	})
	if err != nil {
		return fmt.Errorf("start disposable PostgreSQL: %w", err)
	}
	defer func() { runErr = errors.Join(runErr, harness.Close()) }()
	fixture, err := harness.LoadCatalog(setupCtx, integrationtest.FixtureQA)
	if err != nil {
		return fmt.Errorf("load accepted QA catalog: %w", err)
	}
	snapshot, err := harness.Snapshot(setupCtx)
	if err != nil {
		return fmt.Errorf("inspect accepted QA catalog: %w", err)
	}
	role, err := harness.InspectRole(setupCtx)
	if err != nil {
		return fmt.Errorf("inspect restricted reader: %w", err)
	}
	if snapshot.RowCount != fixture.DataIdentity.RowCount ||
		snapshot.DataIdentity != fixture.DataIdentity.Value ||
		snapshot.SchemaIdentity != fixture.SchemaIdentity.Value ||
		role.CurrentUser != "go_product_reader" || !role.CanSelect ||
		role.CanInsert || role.CanUpdate || role.CanDelete || role.CanCreateInDatabase ||
		role.OwnsDatabase || role.OwnsSchema || role.OwnsTable {
		return errors.New(
			"catalog or restricted reader does not match the accepted fixture",
		)
	}

	running, err := startProduct(harness.RuntimeDatabaseURL())
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, running.stop()) }()

	nonceBytes := make([]byte, 16)
	if _, err := rand.Read(nonceBytes); err != nil {
		return fmt.Errorf("generate run nonce: %w", err)
	}
	record := endpointRecord{
		HTTP:           running.addresses.HTTP,
		GRPC:           running.addresses.GRPC,
		Health:         running.addresses.Health,
		Nonce:          hex.EncodeToString(nonceBytes),
		Fixture:        fixture.ScenarioID,
		RowCount:       snapshot.RowCount,
		DataIdentity:   snapshot.DataIdentity,
		SchemaIdentity: snapshot.SchemaIdentity,
		Reader:         role.CurrentUser,
		ContainerID:    harness.ContainerID(),
		PID:            os.Getpid(),
	}
	if err := writeEndpoint(endpointFile, record); err != nil {
		return err
	}
	fmt.Println("security harness ready: fixture=DATA-QA-001 rows=4 reader=go_product_reader")

	signalCtx, stopSignals := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stopSignals()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-signalCtx.Done():
			return nil
		case <-running.done:
			return fmt.Errorf("product service stopped unexpectedly: %w", running.err)
		case <-ticker.C:
			if _, err := os.Stat(stopFile); err == nil {
				return nil
			} else if !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("inspect stop marker: %w", err)
			}
		}
	}
}

type addresses struct {
	HTTP   string
	GRPC   string
	Health string
}

type runningProduct struct {
	addresses addresses
	cancel    context.CancelFunc
	done      chan struct{}
	err       error
}

func startProduct(databaseURL string) (*runningProduct, error) {
	for attempt := 0; attempt < 3; attempt++ {
		selected, settings, err := serviceSettings(databaseURL)
		if err != nil {
			return nil, err
		}
		ctx, cancel := context.WithCancel(context.Background())
		running := &runningProduct{
			addresses: selected,
			cancel:    cancel,
			done:      make(chan struct{}),
		}
		go func() {
			running.err = service.Run(
				ctx,
				settings,
				slog.New(slog.NewTextHandler(os.Stdout, nil)),
			)
			close(running.done)
		}()
		readyErr := waitReady(selected, running.done)
		if readyErr == nil {
			return running, nil
		}
		stopErr := running.stop()
		if attempt == 2 || stopErr == nil ||
			!strings.Contains(stopErr.Error(), "listen for") {
			return nil, errors.Join(readyErr, stopErr)
		}
	}
	return nil, errors.New("product listener address retries exhausted")
}

func (running *runningProduct) stop() error {
	running.cancel()
	select {
	case <-running.done:
		return running.err
	case <-time.After(stopDeadline):
		return errors.New("product service did not stop within deadline")
	}
}

func serviceSettings(databaseURL string) (addresses, config.Config, error) {
	used := make(map[string]struct{}, 3)
	httpAddress, err := freeDistinctAddress(used)
	if err != nil {
		return addresses{}, config.Config{}, err
	}
	grpcAddress, err := freeDistinctAddress(used)
	if err != nil {
		return addresses{}, config.Config{}, err
	}
	healthAddress, err := freeDistinctAddress(used)
	if err != nil {
		return addresses{}, config.Config{}, err
	}
	selected := addresses{HTTP: httpAddress, GRPC: grpcAddress, Health: healthAddress}
	for name, value := range map[string]string{
		"PRODUCT_DATABASE_URL": databaseURL,
		"HTTP_ADDR":            selected.HTTP,
		"GRPC_ADDR":            selected.GRPC,
		"HEALTH_ADDR":          selected.Health,
		"OTEL_ENABLED":         "false",
		"STARTUP_TIMEOUT":      "10s",
		"SHUTDOWN_TIMEOUT":     "5s",
	} {
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

func freeAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("allocate loopback port: %w", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return "", fmt.Errorf("release loopback port: %w", err)
	}
	return address, nil
}

func freeDistinctAddress(used map[string]struct{}) (string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		address, err := freeAddress()
		if err != nil {
			return "", err
		}
		if _, exists := used[address]; !exists {
			used[address] = struct{}{}
			return address, nil
		}
	}
	return "", errors.New("could not allocate three distinct loopback ports")
}

func waitReady(selected addresses, serviceDone <-chan struct{}) error {
	ctx, cancel := context.WithTimeout(context.Background(), readyDeadline)
	defer cancel()
	client := &http.Client{Timeout: time.Second}
	connection, err := grpcgo.NewClient(
		selected.GRPC,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("create generated gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	grpcClient := productv1.NewProductServiceClient(connection)
	for ctx.Err() == nil {
		select {
		case <-serviceDone:
			return errors.New("product stopped before readiness")
		default:
		}
		request, err := http.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"http://"+selected.Health+"/readyz",
			nil,
		)
		if err != nil {
			return err
		}
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				probeCtx, stopProbe := context.WithTimeout(ctx, time.Second)
				list, listErr := grpcClient.ListProducts(
					probeCtx,
					&productv1.ListProductsRequest{},
				)
				stopProbe()
				if listErr == nil && len(list.GetProducts()) == 4 {
					return nil
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("product HTTP/gRPC readiness deadline exceeded")
}

func writeEndpoint(path string, record endpointRecord) error {
	content, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode endpoint record: %w", err)
	}
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, content, 0o600); err != nil {
		return fmt.Errorf("write endpoint record: %w", err)
	}
	if err := os.Rename(temporary, filepath.Clean(path)); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish endpoint record: %w", err)
	}
	return nil
}
