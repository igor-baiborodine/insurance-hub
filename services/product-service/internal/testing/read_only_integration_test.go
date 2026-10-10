//go:build integration

package integrationtest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

type permissionCase struct {
	ID             string `json:"id"`
	OperationClass string `json:"operationClass"`
	SQL            string `json:"sql"`
}

type permissionFixture struct {
	Cases []permissionCase `json:"cases"`
}

func TestProductReadOnly(t *testing.T) {
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
	permissionCases := loadPermissionCases(t, baselineRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	t.Cleanup(func() { _ = harness.Close() })
	fixture, err := harness.LoadCatalog(ctx, FixtureQA)
	if err != nil {
		t.Fatalf("load QA catalog: %v", err)
	}
	before := requireSnapshot(t, ctx, harness)
	if before.RowCount != fixture.DataIdentity.RowCount {
		t.Fatalf(
			"initial row count = %d, want %d",
			before.RowCount,
			fixture.DataIdentity.RowCount,
		)
	}
	roleBefore := requireRestrictedRole(t, ctx, harness)
	assertFixtureReads(t, ctx, harness, permissionCases)

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
	waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)

	// when
	listStatus, _ := httpBody(t, addresses.http, "/products")
	getStatus, _ := httpBody(t, addresses.http, "/products/CAR")
	missingStatus, _ := httpBody(t, addresses.http, "/products/MISSING")

	// then
	if listStatus != http.StatusOK || getStatus != http.StatusOK ||
		missingStatus != http.StatusNotFound {
		t.Fatalf(
			"read statuses = (list=%d, get=%d, missing=%d)",
			listStatus,
			getStatus,
			missingStatus,
		)
	}
	assertSnapshot(t, "successful and missing reads", before, requireSnapshot(t, ctx, harness))

	// when
	assertMutationDenials(t, ctx, harness, permissionCases)

	// then
	assertSnapshot(t, "mutation probes", before, requireSnapshot(t, ctx, harness))

	// when
	execAdmin(t, harness, "REVOKE SELECT ON TABLE public.product FROM "+runtimeRole)
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
	failedStatus, _ := httpBody(t, addresses.http, "/products")
	execAdmin(t, harness, "GRANT SELECT ON TABLE public.product TO "+runtimeRole)
	accessRestored = true
	waitForHTTPStatus(t, addresses.health, "/readyz", http.StatusOK)
	recoveredStatus, _ := httpBody(t, addresses.http, "/products")

	// then
	if failedStatus != http.StatusInternalServerError || recoveredStatus != http.StatusOK {
		t.Fatalf("failure/recovery statuses = (%d, %d)", failedStatus, recoveredStatus)
	}
	assertSnapshot(t, "read failure and recovery", before, requireSnapshot(t, ctx, harness))
	if roleAfterRecovery := requireRestrictedRole(t, ctx, harness); !reflect.DeepEqual(
		roleAfterRecovery,
		roleBefore,
	) {
		t.Fatalf(
			"role changed after recovery: before=%#v after=%#v",
			roleBefore,
			roleAfterRecovery,
		)
	}

	// when
	stopService()
	select {
	case runErr := <-result:
		if runErr != nil {
			t.Fatalf("run Product service: %v", runErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Product service did not stop within the test bound")
	}
	waitForReaderConnections(t, harness, 0)

	// then
	assertSnapshot(t, "shutdown", before, requireSnapshot(t, ctx, harness))
	if roleAfterShutdown := requireRestrictedRole(t, ctx, harness); !reflect.DeepEqual(
		roleAfterShutdown,
		roleBefore,
	) {
		t.Fatalf(
			"role changed after shutdown: before=%#v after=%#v",
			roleBefore,
			roleAfterShutdown,
		)
	}
}

func loadPermissionCases(t *testing.T, baselineRoot string) []permissionCase {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(baselineRoot, "db-permissions", "cases.json"))
	if err != nil {
		t.Fatalf("read permission fixture: %v", err)
	}
	var fixture permissionFixture
	if err := json.Unmarshal(content, &fixture); err != nil {
		t.Fatalf("decode permission fixture: %v", err)
	}
	if len(fixture.Cases) != 10 {
		t.Fatalf("permission case count = %d, want 10", len(fixture.Cases))
	}
	return fixture.Cases
}

func requireRestrictedRole(t *testing.T, ctx context.Context, harness *Harness) RoleInspection {
	t.Helper()
	inspection, err := harness.InspectRole(ctx)
	if err != nil {
		t.Fatalf("inspect restricted role: %v", err)
	}
	want := RoleInspection{
		CurrentUser:   runtimeRole,
		DatabaseOwner: adminRole,
		SchemaOwner:   databaseOwnerRole,
		TableOwner:    adminRole,
		MemberOfRoles: []string{},
		CanConnect:    true,
		CanUseSchema:  true,
		CanSelect:     true,
	}
	if !reflect.DeepEqual(inspection, want) {
		t.Fatalf("role inspection = %#v, want %#v", inspection, want)
	}
	return inspection
}

func assertFixtureReads(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	cases []permissionCase,
) {
	t.Helper()
	want := map[string]string{
		"DB-READ-CONNECT-001": runtimeRole,
		"DB-READ-LIST-001":    "4",
		"DB-READ-GET-001":     "CAR",
	}
	for _, permissionCase := range cases {
		if permissionCase.OperationClass != "read" {
			continue
		}
		t.Run(permissionCase.ID, func(t *testing.T) {
			// when
			value, err := harness.QueryRuntimeValue(ctx, permissionCase.SQL)
			// then
			if err != nil {
				t.Fatalf("runtime read: %v", err)
			}
			if value != want[permissionCase.ID] {
				t.Errorf(
					"runtime read value = %q, want %q",
					value,
					want[permissionCase.ID],
				)
			}
		})
	}
}

func assertMutationDenials(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	cases []permissionCase,
) {
	t.Helper()
	denialCount := 0
	for _, permissionCase := range cases {
		if permissionCase.OperationClass == "read" {
			continue
		}
		denialCount++
		t.Run(permissionCase.ID, func(t *testing.T) {
			// when
			err := harness.ExecuteRuntimeStatement(ctx, permissionCase.SQL)

			// then
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
				t.Fatalf("mutation error = %v, want SQLSTATE 42501", err)
			}
		})
	}
	if denialCount != 7 {
		t.Fatalf("mutation denial count = %d, want 7", denialCount)
	}
}

func requireSnapshot(t *testing.T, ctx context.Context, harness *Harness) Snapshot {
	t.Helper()
	snapshot, err := harness.Snapshot(ctx)
	if err != nil {
		t.Fatalf("capture schema/data snapshot: %v", err)
	}
	return snapshot
}

func assertSnapshot(t *testing.T, stage string, before, after Snapshot) {
	t.Helper()
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("snapshot changed after %s: before=%#v after=%#v", stage, before, after)
	}
}
