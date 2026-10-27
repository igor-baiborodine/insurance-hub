//go:build image

package integrationtest

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestProductAdministrationImage(t *testing.T) {
	// given: only this suite's disposable database, synthetic logins and TLS CA.
	image := os.Getenv("PRODUCT_TEST_IMAGE")
	if image == "" {
		t.Fatal("PRODUCT_TEST_IMAGE is required")
	}
	if _, ok := os.LookupEnv("PRODUCT_DATABASE_URL"); ok {
		t.Fatal("PRODUCT_DATABASE_URL must be unset")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := ResolveBaselineRoot(cwd)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	tlsFiles := createImageTLSFiles(t)
	harness, err := Start(
		ctx,
		Options{
			FixtureRoot: root,
			Image:       DefaultPostgresImage,
			TLS: &TLSOptions{
				CAFile:     tlsFiles.ca,
				CertFile:   tlsFiles.cert,
				KeyFile:    tlsFiles.key,
				ConfigFile: tlsFiles.config,
			},
			provision: provisionImageAdministration,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := harness.Close(); err != nil {
			t.Error(err)
		}
	})
	selectImageProductDatabase(t, ctx, harness)
	port, err := harness.container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatal(err)
	}
	host, err := harness.container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	backend := net.JoinHostPort(host, port.Port())

	runSuccess := func(t *testing.T, operation, want string) {
		t.Helper()
		files, args := imageAdministrationInputs(t, tlsFiles.ca, port.Port(), operation)
		executable := "/product-migrate"
		if operation == "seed" {
			executable = "/product-seed"
		}
		runImageAdministrationCommand(t, ctx, image, executable, files, args, want)
	}
	reset := func(t *testing.T, operation string) {
		t.Helper()
		if _, err := harness.adminPool.Exec(
			ctx,
			"DROP TABLE IF EXISTS public.product, public.goose_db_version",
		); err != nil {
			t.Fatal(err)
		}
		if operation == "seed" {
			runSuccess(t, "migrate", "version=1")
		}
	}
	assertEmpty := func(t *testing.T, operation string) {
		t.Helper()
		var count int
		query := "SELECT count(*) FROM pg_class WHERE oid IN (to_regclass('public.product'),to_regclass('public.goose_db_version'))"
		if operation == "seed" {
			query = "SELECT count(*) FROM public.product"
		}
		if err := harness.adminPool.QueryRow(ctx, query).
			Scan(&count); err != nil ||
			count != 0 {
			t.Fatalf("failure changed database state: count=%d error=%v", count, err)
		}
	}
	for _, operation := range []string{"migrate", "seed"} {
		t.Run(operation, func(t *testing.T) {
			executable := "/product-" + operation
			for _, failure := range []string{"signal", "lock_timeout", "partial_start_cleanup"} {
				t.Run(failure, func(t *testing.T) {
					reset(t, operation)
					holder, err := harness.adminPool.Acquire(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer holder.Release()
					if _, err := holder.Exec(
						ctx,
						"SELECT pg_advisory_lock(190001)",
					); err != nil {
						t.Fatal(err)
					}
					unlock := func() {
						cleanup, stop := context.WithTimeout(
							context.Background(),
							5*time.Second,
						)
						defer stop()
						if _, err := holder.Exec(
							cleanup,
							"SELECT pg_advisory_unlock(190001)",
						); err != nil {
							t.Error(err)
						}
					}
					defer unlock()
					files, args := imageAdministrationInputs(
						t,
						tlsFiles.ca,
						port.Port(),
						operation,
					)
					if failure == "partial_start_cleanup" {
						var partial *testcontainers.DockerContainer
						// A failed wait strategy returns a live handle. Cleanup must run even then.
						t.Run("failed_wait", func(t *testing.T) {
							partial, err = startImageAdministrationCommand(
								t,
								ctx,
								image,
								executable,
								files,
								args,
								wait.ForExit().
									WithExitTimeout(200*time.Millisecond),
							)
							if err == nil || partial == nil {
								t.Fatalf(
									"expected partial start handle plus error: %v",
									err,
								)
							}
						})
						cleanup, stop := context.WithTimeout(
							context.Background(),
							10*time.Second,
						)
						defer stop()
						if _, err := partial.State(
							cleanup,
						); err == nil ||
							!strings.Contains(
								err.Error(),
								"No such container: "+partial.ID,
							) {
							t.Fatalf(
								"partially started container was not removed: %v",
								err,
							)
						}

					} else {
						// when: the real command waits behind a separately owned session lock.
						started := time.Now()
						command, err := startImageAdministrationCommand(
							t,
							ctx,
							image,
							executable,
							files,
							args,
							nil,
						)
						if err != nil {
							t.Fatal(err)
						}
						awaitImageAdministrationLock(
							t,
							ctx,
							harness,
							operation,
						)
						kind := ""
						if failure == "signal" {
							grace := 5 * time.Second
							if err := command.Stop(
								ctx,
								&grace,
							); err != nil {
								t.Fatal(err)
							}
							kind = "cancelled"
						} else {
							if err := wait.ForExit().
								WithExitTimeout(40*time.Second).
								WaitUntilReady(ctx, command); err != nil {
								t.Fatal(err)
							}
							if elapsed := time.Since(
								started,
							); elapsed < 25*time.Second ||
								elapsed > 45*time.Second {
								t.Fatalf(
									"lock deadline elapsed=%s",
									elapsed,
								)
							}
						}
						assertImageAdministrationFailure(
							t,
							ctx,
							command,
							operation,
							kind,
						)
					}
					assertEmpty(t, operation)
					assertImageAdministrationReleased(t, ctx, harness)
					unlock()
					if operation == "seed" {
						runSuccess(t, operation, "inserted=4 existing=0")
					} else {
						runSuccess(t, operation, "version=1")
					}
				})
			}
			t.Run("connection_failure", func(t *testing.T) {
				reset(t, operation)
				// A valid TLS handshake followed by a closed upstream tests the actual driver.
				proxy := newImageFaultProxy(t, "127.0.0.1:1", tlsFiles, "", 1)
				files, args := imageAdministrationInputs(
					t,
					tlsFiles.ca,
					proxy.port,
					operation,
				)
				command, err := startImageAdministrationCommand(
					t,
					ctx,
					image,
					executable,
					files,
					args,
					wait.ForExit().WithExitTimeout(15*time.Second),
				)
				if err != nil {
					t.Fatal(err)
				}
				assertImageAdministrationFailure(t, ctx, command, operation, "")
				assertEmpty(t, operation)
				assertImageAdministrationReleased(t, ctx, harness)
				if operation == "seed" {
					runSuccess(t, operation, "inserted=4 existing=0")
				} else {
					runSuccess(t, operation, "version=1")
				}
			})
			t.Run("lost_commit_acknowledgement", func(t *testing.T) {
				reset(t, operation)
				occurrence := int64(1)
				if operation == "migrate" {
					occurrence = 2
				} // Goose history initialization commits first.
				proxy := newImageFaultProxy(
					t,
					backend,
					tlsFiles,
					"COMMIT",
					occurrence,
				)
				files, args := imageAdministrationInputs(
					t,
					tlsFiles.ca,
					proxy.port,
					operation,
				)
				// when: PostgreSQL commits, but the packaged client never receives confirmation.
				command, err := startImageAdministrationCommand(
					t,
					ctx,
					image,
					executable,
					files,
					args,
					wait.ForExit().WithExitTimeout(20*time.Second),
				)
				if err != nil {
					t.Fatal(err)
				}
				// then: failure is honest; committed state is independently visible and replay safe.
				if !proxy.fired.Load() {
					t.Fatal("commit acknowledgement fault was not reached")
				}
				assertImageAdministrationFailure(t, ctx, command, operation, "")
				assertImageAdministrationReleased(t, ctx, harness)
				var version int
				if err := harness.adminPool.QueryRow(ctx, "SELECT version_id FROM public.goose_db_version WHERE is_applied ORDER BY id DESC LIMIT 1").
					Scan(&version); err != nil ||
					version != 1 {
					t.Fatalf(
						"committed migration history version=%d error=%v",
						version,
						err,
					)
				}
				runSuccess(t, "status", "version=1")
				if operation == "seed" {
					var count int
					if err := harness.adminPool.QueryRow(ctx, "SELECT count(*) FROM public.product").
						Scan(&count); err != nil ||
						count != 4 {
						t.Fatalf(
							"committed seed rows=%d error=%v",
							count,
							err,
						)
					}
					before, err := harness.Snapshot(ctx)
					if err != nil {
						t.Fatal(err)
					}
					runSuccess(t, operation, "inserted=0 existing=4")
					after, err := harness.Snapshot(ctx)
					if err != nil || !reflect.DeepEqual(before, after) {
						t.Fatalf("commit replay changed catalog: %v", err)
					}
				} else {
					runSuccess(t, operation, "version=1")
				}
			})
		})
	}
	t.Run("seed_mid_batch_rollback", func(t *testing.T) {
		reset(t, "seed")
		proxy := newImageFaultProxy(t, backend, tlsFiles, "INSERT 0 1", 2)
		files, args := imageAdministrationInputs(t, tlsFiles.ca, proxy.port, "seed")
		// when: the second INSERT ran in PostgreSQL before its connection was severed.
		command, err := startImageAdministrationCommand(
			t,
			ctx,
			image,
			"/product-seed",
			files,
			args,
			wait.ForExit().WithExitTimeout(20*time.Second),
		)
		if err != nil {
			t.Fatal(err)
		}
		// then: both earlier writes roll back, and the same catalog retries completely.
		if !proxy.fired.Load() {
			t.Fatal("mid-batch INSERT fault was not reached")
		}
		assertImageAdministrationFailure(t, ctx, command, "seed", "")
		assertImageAdministrationReleased(t, ctx, harness)
		assertEmpty(t, "seed")
		runSuccess(t, "seed", "inserted=4 existing=0")
		runSuccess(t, "seed", "inserted=0 existing=4")
	})
}

func assertImageAdministrationReleased(t *testing.T, ctx context.Context, harness *Harness) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		var sessions, locks int
		err := harness.adminPool.QueryRow(ctx, `SELECT count(*), (SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND pid IN (SELECT pid FROM pg_stat_activity WHERE usename IN ('go_product_migrator','go_product_seeder'))) FROM pg_stat_activity WHERE usename IN ('go_product_migrator','go_product_seeder')`).
			Scan(&sessions, &locks)
		if err != nil {
			t.Fatal(err)
		}
		if sessions == 0 && locks == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("administration leaked sessions=%d locks=%d", sessions, locks)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func assertImageAdministrationFailure(
	t *testing.T,
	ctx context.Context,
	command *testcontainers.DockerContainer,
	operation, kind string,
) {
	t.Helper()
	state, err := command.State(ctx)
	if err != nil || state.Running || state.ExitCode != 1 {
		t.Fatalf("command did not exit normally with failure: %+v error=%v", state, err)
	}
	logs, err := command.Logs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(logs)
	closeErr := logs.Close()
	if err != nil || closeErr != nil {
		t.Fatalf("read diagnostics: %v/%v", err, closeErr)
	}
	var diagnostic map[string]any
	if json.Unmarshal(content, &diagnostic) != nil ||
		diagnostic["msg"] != "administration failed" ||
		diagnostic["operation"] != operation ||
		diagnostic["environment"] != imageEnvironment ||
		diagnostic["error"] != "[REDACTED]" {
		t.Fatalf("invalid structured diagnostic: %s", content)
	}
	if kind != "" && diagnostic["error_kind"] != kind {
		t.Fatalf("unexpected failure classification: %v", diagnostic)
	}
	for _, secret := range []string{imageMigratorPassword, imageSeederPassword, "postgres://", "SELECT ", "INSERT ", "/database-url"} {
		if strings.Contains(string(content), secret) {
			t.Fatal("unsafe command diagnostic")
		}
	}
}

func awaitImageAdministrationLock(
	t *testing.T,
	ctx context.Context,
	harness *Harness,
	operation string,
) {
	t.Helper()
	role := "go_product_migrator"
	if operation == "seed" {
		role = "go_product_seeder"
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var observed bool
		if err := harness.adminPool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE usename=$1 AND query LIKE '%pg_try_advisory%')", role).
			Scan(&observed); err != nil {
			t.Fatal(err)
		}
		if observed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("command never reached advisory lock acquisition")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
