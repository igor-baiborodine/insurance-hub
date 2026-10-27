//go:build image

package integrationtest

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
)

const (
	imageDatabase         = "product"
	imageEnvironment      = "disposable"
	imageContext          = "image-check"
	imageInstanceID       = "00000000-0000-4000-8000-000000000190"
	imageClusterUID       = "image-check-cluster"
	imageMigratorPassword = "image-check-migrator"
	imageSeederPassword   = "image-check-seeder"
)

type imageTLSFiles struct {
	ca     string
	cert   string
	key    string
	config string
}

type imageFileMount struct {
	hostPath      string
	containerPath string
}

func createImageTLSFiles(t *testing.T) imageTLSFiles {
	t.Helper()
	directory := t.TempDir()
	now := time.Now().UTC()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate image-check CA key: %v", err)
	}
	caTemplate := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Product image-check CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(
		rand.Reader,
		&caTemplate,
		&caTemplate,
		&caKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("create image-check CA certificate: %v", err)
	}
	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate image-check server key: %v", err)
	}
	serverTemplate := x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: testcontainers.HostInternal},
		DNSNames:     []string{testcontainers.HostInternal},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(
		rand.Reader,
		&serverTemplate,
		&caTemplate,
		&serverKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("create image-check server certificate: %v", err)
	}
	files := imageTLSFiles{
		ca: filepath.Join(
			directory,
			"ca.crt",
		),
		cert:   filepath.Join(directory, "server.crt"),
		key:    filepath.Join(directory, "server.key"),
		config: filepath.Join(directory, "postgresql.conf"),
	}
	writePEM := func(path, blockType string, content []byte, mode os.FileMode) {
		t.Helper()
		encoded := pem.EncodeToMemory(&pem.Block{Type: blockType, Bytes: content})
		if err := os.WriteFile(path, encoded, mode); err != nil {
			t.Fatalf("write image-check TLS file: %v", err)
		}
	}
	writePEM(files.ca, "CERTIFICATE", caDER, 0o444)
	writePEM(files.cert, "CERTIFICATE", serverDER, 0o600)
	writePEM(files.key, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(serverKey), 0o600)
	configuration := "listen_addresses='*'\nssl=on\n" +
		"ssl_ca_file='/tmp/testcontainers-go/postgres/ca_cert.pem'\n" +
		"ssl_cert_file='/tmp/testcontainers-go/postgres/server.cert'\n" +
		"ssl_key_file='/tmp/testcontainers-go/postgres/server.key'\n"
	if err := os.WriteFile(files.config, []byte(configuration), 0o600); err != nil {
		t.Fatalf("write image-check PostgreSQL TLS configuration: %v", err)
	}
	return files
}

func provisionImageAdministration(
	ctx context.Context,
	clusterAdmin *pgxpool.Pool,
	readerPassword string,
) error {
	for _, statement := range []string{
		`CREATE ROLE product LOGIN NOINHERIT`,
		`CREATE ROLE postgres NOLOGIN`,
		`CREATE ROLE go_product_migrator LOGIN NOINHERIT PASSWORD '` + imageMigratorPassword + `'`,
		`CREATE ROLE go_product_seeder LOGIN NOINHERIT PASSWORD '` + imageSeederPassword + `'`,
		`CREATE ROLE go_product_reader LOGIN NOINHERIT PASSWORD '` + readerPassword + `'`,
		`GRANT product TO go_product_migrator WITH INHERIT FALSE, SET TRUE, ADMIN FALSE`,
		`CREATE DATABASE product OWNER product`,
		`REVOKE ALL ON DATABASE product FROM PUBLIC`,
		`GRANT CONNECT ON DATABASE product TO go_product_migrator,go_product_seeder,go_product_reader`,
	} {
		if _, err := clusterAdmin.Exec(ctx, statement); err != nil {
			return fmt.Errorf("provision image-check cluster: %w", err)
		}
	}
	productConfig := clusterAdmin.Config().Copy()
	productConfig.ConnConfig.Database = imageDatabase
	productAdmin, err := pgxpool.NewWithConfig(ctx, productConfig)
	if err != nil {
		return fmt.Errorf("open image-check Product database: %w", err)
	}
	defer productAdmin.Close()
	for _, statement := range []string{
		`REVOKE ALL ON SCHEMA public FROM PUBLIC`,
		`GRANT USAGE ON SCHEMA public TO go_product_reader,go_product_seeder`,
		`CREATE SCHEMA product_admin AUTHORIZATION postgres`,
		`SET ROLE postgres`,
		`CREATE TABLE product_admin.target_identity(singleton boolean PRIMARY KEY CHECK(singleton),
environment text NOT NULL, instance_id uuid NOT NULL, cluster_uid text NOT NULL,
seed_allowed boolean NOT NULL)`,
		`RESET ROLE`,
		`INSERT INTO product_admin.target_identity VALUES(true,'` + imageEnvironment + `','` +
			imageInstanceID + `','` + imageClusterUID + `',true)`,
		`REVOKE ALL ON SCHEMA product_admin FROM PUBLIC`,
		`GRANT USAGE ON SCHEMA product_admin TO product,go_product_migrator,go_product_seeder`,
		`GRANT SELECT ON product_admin.target_identity TO product,go_product_migrator,go_product_seeder`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON TABLES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product IN SCHEMA public REVOKE ALL ON TABLES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE ALL ON SEQUENCES FROM PUBLIC`,
		`ALTER DEFAULT PRIVILEGES FOR ROLE product REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC`,
	} {
		if _, err := productAdmin.Exec(ctx, statement); err != nil {
			return fmt.Errorf("provision image-check Product database: %w", err)
		}
	}
	return nil
}

func selectImageProductDatabase(t *testing.T, ctx context.Context, harness *Harness) {
	t.Helper()
	configuration := harness.adminPool.Config().Copy()
	configuration.ConnConfig.Database = imageDatabase
	productAdmin, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		t.Fatalf("select image-check Product database: %v", err)
	}
	harness.adminPool.Close()
	harness.adminPool = productAdmin
	parsed, err := url.Parse(harness.runtimeURL)
	if err != nil {
		t.Fatalf("parse image-check reader URL: %v", err)
	}
	parsed.Path = "/" + imageDatabase
	harness.runtimeURL = parsed.String()
}

func runPackagedAdministration(
	t *testing.T,
	ctx context.Context,
	image string,
	harness *Harness,
	caFile string,
) {
	t.Helper()
	port, err := harness.container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("resolve image-check PostgreSQL port: %v", err)
	}
	files, args := imageAdministrationInputs(t, caFile, port.Port(), "migrate")
	runImageAdministrationCommand(t, ctx, image, "/product-migrate", files, args, "version=1")
	files, args = imageAdministrationInputs(t, caFile, port.Port(), "seed")
	runImageAdministrationCommand(
		t,
		ctx,
		image,
		"/product-seed",
		files,
		args,
		"inserted=4 existing=0",
	)
}

func imageAdministrationInputs(
	t *testing.T,
	caFile, port, operation string,
) ([]imageFileMount, []string) {
	t.Helper()
	directory := t.TempDir()
	target := administration.Target{
		Environment: imageEnvironment, Context: imageContext,
		DataNamespace: "owned", ServiceNamespace: "owned", Cluster: "image-check",
		ClusterUID: imageClusterUID, InstanceID: imageInstanceID, Database: imageDatabase,
		Host: testcontainers.HostInternal, SeedAllowed: true, Disposable: true,
	}
	targetContent, err := json.Marshal(target)
	if err != nil {
		t.Fatalf("encode image-check target: %v", err)
	}
	targetFile := filepath.Join(directory, "target.json")
	if err := os.WriteFile(targetFile, targetContent, 0o444); err != nil {
		t.Fatalf("write image-check target: %v", err)
	}
	connectionFile := func(name, role, password string) string {
		t.Helper()
		value := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(role, password),
			Host: net.JoinHostPort(
				testcontainers.HostInternal,
				port,
			),
			Path: "/" + imageDatabase,
		}
		query := value.Query()
		query.Set("sslmode", "verify-full")
		query.Set("sslrootcert", "/ca.crt")
		value.RawQuery = query.Encode()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(value.String()), 0o444); err != nil {
			t.Fatalf("write image-check credential: %v", err)
		}
		return path
	}
	migratorURL := connectionFile("migrator-url", "go_product_migrator", imageMigratorPassword)
	seederURL := connectionFile("seeder-url", "go_product_seeder", imageSeederPassword)
	files := []imageFileMount{
		{hostPath: targetFile, containerPath: "/target.json"},
		{hostPath: caFile, containerPath: "/ca.crt"},
	}

	credential := migratorURL
	args := []string{
		"--environment=" + imageEnvironment,
		"--context=" + imageContext,
		"--target-file=/target.json",
		"--database-url-file=/database-url",
		"--ca-file=/ca.crt",
	}
	if operation == "seed" {
		credential = seederURL
		args = append(
			args,
			"--catalog-file=/usr/share/insurance-hub/product/catalogs/base.json",
		)
	} else {
		args = append(args, "--operation="+operation)
	}
	files = append(files, imageFileMount{hostPath: credential, containerPath: "/database-url"})
	return files, args
}

func runImageAdministrationCommand(
	t *testing.T,
	ctx context.Context,
	image string,
	entrypoint string,
	files []imageFileMount,
	arguments []string,
	wantOutput string,
) {
	t.Helper()
	command, err := startImageAdministrationCommand(
		t,
		ctx,
		image,
		entrypoint,
		files,
		arguments,
		wait.ForExit().WithExitTimeout(90*time.Second),
	)
	if err != nil {
		t.Fatalf("run %s from Product image: %v", entrypoint, err)
	}
	state, err := command.State(ctx)
	if err != nil {
		t.Fatalf("inspect %s image command: %v", entrypoint, err)
	}
	logs, err := command.Logs(ctx)
	if err != nil {
		t.Fatalf("read %s image command logs: %v", entrypoint, err)
	}
	content, readErr := io.ReadAll(logs)
	closeErr := logs.Close()
	if readErr != nil || closeErr != nil {
		t.Fatalf("read/close %s image command logs: %v/%v", entrypoint, readErr, closeErr)
	}
	if state.ExitCode != 0 || strings.TrimSpace(string(content)) != wantOutput {
		t.Fatalf(
			"%s exit/output = %d/%q, want 0/%q",
			entrypoint,
			state.ExitCode,
			content,
			wantOutput,
		)
	}
}

// Register cleanup even when startup or its wait strategy fails after creation.
func startImageAdministrationCommand(
	t *testing.T,
	ctx context.Context,
	image, entrypoint string,
	files []imageFileMount,
	arguments []string,
	strategy wait.Strategy,
) (*testcontainers.DockerContainer, error) {
	t.Helper()
	options := []testcontainers.ContainerCustomizer{
		testcontainers.WithEntrypoint(entrypoint),
		testcontainers.WithCmd(arguments...),
		testcontainers.WithHostConfigModifier(func(config *container.HostConfig) {
			config.ExtraHosts = append(
				config.ExtraHosts,
				testcontainers.HostInternal+":host-gateway",
			)
			for _, file := range files {
				config.Binds = append(
					config.Binds,
					file.hostPath+":"+file.containerPath+":ro",
				)
			}
			config.ReadonlyRootfs = true
			config.CapDrop = append(config.CapDrop, "ALL")
			config.SecurityOpt = append(config.SecurityOpt, "no-new-privileges")
		}),
	}
	if strategy != nil {
		options = append(options, testcontainers.WithWaitStrategy(strategy))
	}
	command, err := testcontainers.Run(ctx, image, options...)
	if command != nil {
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := command.Terminate(cleanup); err != nil {
				t.Errorf("terminate %s image command: %v", entrypoint, err)
			}
		})
	}
	return command, err
}
