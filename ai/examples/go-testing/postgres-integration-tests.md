# Example: PostgreSQL Migrations and Stale Writes

Read the [adaptation guide](README.md). This adapts campsite's PostgreSQL integration/bootstrap
tests with cleanup registered during setup, a bounded startup, and isolated storage per test.
It intentionally uses a real database rather than proving SQL behavior with a mock.

**Adaptation points:** this template assumes a `database/sql` adapter using the pgx driver, a
production `migrations.Up(ctx, db) error` entry point applying the service's real migrations, and
a repository with `Insert`, `Find`, and `Update` methods. `domain.Policy` has `ID`, `Status`, and
`Version int64`; inserts start at version 1, updates compare/increment that version atomically,
and stale updates wrap `domain.ErrConflict`. These are illustrative contracts, not a schema proposal.

Use the actual migration API, driver, schema, and repository in the target module. For GORM,
adapt connection wiring and retain the same observable assertions. Do not create a test-only
schema that omits real migrations. The owning integration Make target must set
`POSTGRES_TEST_IMAGE` to its pinned PostgreSQL test image and provide a working container runtime.

Suggested location: `internal/postgres/policy_repository_integration_test.go`.

```go
//go:build integration

package postgres_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"example.com/policy/db/migrations"
	"example.com/policy/internal/domain"
	"example.com/policy/internal/postgres"
	_ "github.com/jackc/pgx/v4/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	image := os.Getenv("POSTGRES_TEST_IMAGE")
	require.NotEmpty(t, image, "integration Make target must supply the pinned test image")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	container, err := pgcontainer.Run(ctx, image,
		pgcontainer.WithDatabase("policy_test"),
		pgcontainer.WithUsername("test_user"),
		pgcontainer.WithPassword("test_password"), // Disposable test container only.
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).WithStartupTimeout(60*time.Second),
			wait.ForListeningPort("5432/tcp").WithStartupTimeout(30*time.Second),
		),
	)
	// A partially started container may accompany an error.
	if container != nil {
		t.Cleanup(func() {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cleanupCancel()
			if err := container.Terminate(cleanupCtx); err != nil {
				t.Errorf("terminate test database: %v", err)
			}
		})
	}
	require.NoError(t, err)
	require.NotNil(t, container)
	dsn, err := container.ConnectionString(ctx, "sslmode=disable") // Local test connection only.
	require.NoError(t, err)
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, migrations.Up(ctx, db))
	return db
}

func TestPolicyRepository_RejectsStaleUpdate(t *testing.T) {
	db := newTestDB(t)
	repo := postgres.NewPolicyRepository(db)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	const id = "89acf5f5-5590-47b7-bb4a-d208dd4cbb52"
	require.NoError(t, repo.Insert(ctx, &domain.Policy{ID: id, Status: "active", Version: 1}))

	original, err := repo.Find(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, original)
	require.Equal(t, int64(1), original.Version)
	firstWriter := *original
	staleWriter := *original
	firstWriter.Status = "cancelled"
	require.NoError(t, repo.Update(ctx, &firstWriter))

	err = repo.Update(ctx, &staleWriter)

	require.ErrorIs(t, err, domain.ErrConflict)
	stored, err := repo.Find(ctx, id)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, "cancelled", stored.Status)
	assert.Equal(t, int64(2), stored.Version)
}
```

The [Testcontainers PostgreSQL module](https://golang.testcontainers.org/modules/postgres/)
provides the disposable server and connection details. Pin the image/tool versions in module
tooling, not in an agent's guessed command. A configured readiness strategy and `PingContext`
replace fixed sleeps. Cleanup uses a fresh bounded context and closes the DB before the container.

Do not mark this helper's users parallel if the real migration library mutates global state (for
example, global Goose configuration). Separate containers alone do not isolate process globals.
If using a suite, assertions in subtests must use that subtest's `t`; do not reuse suite-wide mocks
or cleanup that only works after full setup succeeds.

This test proves stale-write protection and stored-state preservation, not a simultaneous-writer
race or a multi-row transaction's atomicity. Add separate cases using two real transactions and
explicit barriers for those acceptance criteria; verify the final state after rollback or conflict.
Do not use schema recreation or blanket truncation against a shared database.

Run through the owning `test-integration` Make target after adaptation. Do not skip on Docker or
database startup failure when this gate is requested; report a failed prerequisite. The target
should force a fresh integration run when external dependencies change rather than rely on cached
test results. No container or migration has been executed by this documentation example.
