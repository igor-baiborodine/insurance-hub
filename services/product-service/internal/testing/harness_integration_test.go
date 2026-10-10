//go:build integration

package integrationtest

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgresHarness(t *testing.T) {
	// given
	image := os.Getenv("PRODUCT_TEST_POSTGRES_IMAGE")
	if image != DefaultPostgresImage {
		t.Fatalf(
			"PRODUCT_TEST_POSTGRES_IMAGE = %q, want pinned %q",
			image,
			DefaultPostgresImage,
		)
	}
	fixtureSets := selectedFixtureSets(t, os.Getenv("PRODUCT_INTEGRATION_FIXTURE_SET"))
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	baselineRoot, err := ResolveBaselineRoot(workingDirectory)
	if err != nil {
		t.Fatalf("resolve baseline root: %v", err)
	}
	beforeHashes := fixtureHashes(t, baselineRoot)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	harness, err := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
	if err != nil {
		t.Fatalf("start harness: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = harness.Close()
		}
	})

	t.Run("uses pinned server and restricted identity", func(t *testing.T) {
		version, versionErr := harness.ServerVersion(ctx)
		if versionErr != nil {
			t.Fatalf("server version: %v", versionErr)
		}
		if !strings.HasPrefix(version, "17.10") {
			t.Errorf("server version = %q, want 17.10", version)
		}
		inspection, inspectionErr := harness.InspectRole(ctx)
		if inspectionErr != nil {
			t.Fatalf("inspect role: %v", inspectionErr)
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
			t.Errorf("role inspection = %#v, want %#v", inspection, want)
		}
		if strings.Contains(harness.RuntimeDatabaseURL(), adminRole) ||
			!strings.Contains(harness.RuntimeDatabaseURL(), runtimeRole) {
			t.Error("runtime URL does not contain only the restricted role identity")
		}
	})

	for _, fixtureSet := range fixtureSets {
		t.Run("replays "+string(fixtureSet), func(t *testing.T) {
			// when
			fixture, loadErr := harness.LoadCatalog(ctx, fixtureSet)
			if loadErr != nil {
				t.Fatalf("load catalog: %v", loadErr)
			}
			snapshot, snapshotErr := harness.Snapshot(ctx)
			if snapshotErr != nil {
				t.Fatalf("snapshot: %v", snapshotErr)
			}
			checksums, checksumErr := harness.DefinitionChecksums(ctx)
			if checksumErr != nil {
				t.Fatalf("definition checksums: %v", checksumErr)
			}

			// then
			if snapshot.RowCount != fixture.DataIdentity.RowCount ||
				snapshot.DataIdentity != fixture.DataIdentity.Value ||
				snapshot.SchemaIdentity != fixture.SchemaIdentity.Value ||
				!reflect.DeepEqual(snapshot.Codes, fixture.DataIdentity.Codes) {
				t.Errorf(
					"snapshot = %#v, fixture identity = %#v",
					snapshot,
					fixture.DataIdentity,
				)
			}
			for _, row := range fixture.Rows {
				if checksums[row.Code] != row.Checksum.Value {
					t.Errorf(
						"%s checksum = %s, want %s",
						row.Code,
						checksums[row.Code],
						row.Checksum.Value,
					)
				}
			}
		})
	}

	t.Run("resets empty and exact numeric synthetic catalogs", func(t *testing.T) {
		// when
		if resetErr := harness.ResetRows(ctx, nil); resetErr != nil {
			t.Fatalf("reset empty catalog: %v", resetErr)
		}
		empty, snapshotErr := harness.Snapshot(ctx)
		if snapshotErr != nil {
			t.Fatalf("snapshot empty catalog: %v", snapshotErr)
		}
		const decimalToken = "12345678901234567890.2300"
		if resetErr := harness.ResetRows(ctx, []FixtureRow{{
			Code:                      "PRECISION",
			RawLosslessDefinitionJSON: `{"amount":12345678901234567890.2300}`,
		}}); resetErr != nil {
			t.Fatalf("reset synthetic catalog: %v", resetErr)
		}
		actualToken, numberErr := harness.DefinitionNumber(ctx, "PRECISION", "amount")
		if numberErr != nil {
			t.Fatalf("read exact numeric token: %v", numberErr)
		}

		// then
		if empty.RowCount != 0 || empty.DataIdentity != "" || len(empty.Codes) != 0 {
			t.Errorf("empty snapshot = %#v", empty)
		}
		if actualToken != decimalToken {
			t.Errorf("numeric token = %q, want %q", actualToken, decimalToken)
		}
	})

	// when
	if closeErr := harness.Close(); closeErr != nil {
		t.Fatalf("close harness: %v", closeErr)
	}
	closed = true

	// then
	if closeErr := harness.Close(); closeErr != nil {
		t.Errorf("second close: %v", closeErr)
	}
	afterHashes := fixtureHashes(t, baselineRoot)
	if !reflect.DeepEqual(beforeHashes, afterHashes) {
		t.Errorf(
			"authoritative fixture hashes changed: before=%v after=%v",
			beforeHashes,
			afterHashes,
		)
	}

	t.Run("cleans up a partial setup failure", func(t *testing.T) {
		setupFailure := errors.New("injected setup failure")
		var started *postgres.PostgresContainer
		_, startErr := Start(ctx, Options{
			FixtureRoot: baselineRoot,
			Image:       image,
			provision: func(context.Context, *pgxpool.Pool, string) error {
				return setupFailure
			},
			onContainerStarted: func(container *postgres.PostgresContainer) {
				started = container
			},
		})
		if !errors.Is(startErr, setupFailure) {
			t.Fatalf("partial start error = %v, want injected failure", startErr)
		}
		if started == nil {
			t.Fatal("partial start did not create the expected disposable container")
		}
		if started.IsRunning() {
			t.Error("container remains running after partial setup failure")
		}
	})

	t.Run("refuses an external Product database URL", func(t *testing.T) {
		t.Setenv(
			"PRODUCT_DATABASE_URL",
			"postgresql://shared-owner:private@shared.example/product",
		)
		_, startErr := Start(ctx, Options{FixtureRoot: baselineRoot, Image: image})
		if startErr == nil || !strings.Contains(startErr.Error(), "PRODUCT_DATABASE_URL") {
			t.Errorf("external database refusal = %v", startErr)
		}
		if strings.Contains(startErr.Error(), "shared-owner") ||
			strings.Contains(startErr.Error(), "private") {
			t.Errorf("external database refusal exposed value: %v", startErr)
		}
	})
}

func selectedFixtureSets(t *testing.T, selection string) []FixtureSet {
	t.Helper()
	switch selection {
	case string(FixtureQA):
		return []FixtureSet{FixtureQA}
	default:
		t.Fatalf("unknown fixture set %q", selection)
		return nil
	}
}

func fixtureHashes(t *testing.T, baselineRoot string) map[FixtureSet]string {
	t.Helper()
	result := make(map[FixtureSet]string, 1)
	for _, fixtureSet := range []FixtureSet{FixtureQA} {
		path := filepath.Join(baselineRoot, "catalog", string(fixtureSet)+".json")
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s for hash: %v", fixtureSet, err)
		}
		result[fixtureSet] = fmt.Sprintf("%x", sha256.Sum256(content))
	}
	return result
}
