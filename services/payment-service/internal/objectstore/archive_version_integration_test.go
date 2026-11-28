//go:build integration

package objectstore

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"

	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/payment-service/internal/testing/miniofixture"
)

func TestRun_PaymentStatementArchiveVersions(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	objects := miniofixture.New(t, ctx)

	t.Run("replacement before exact-version deletion survives", func(t *testing.T) {
		const source = "bankStatements_2018_8_20.csv"
		accepted := []byte("accepted statement bytes")
		replacement := []byte("replacement statement bytes")
		acceptedVersion := putVersionedObject(t, ctx, objects.Client, source, accepted)
		reader := newVersionIntegrationReader(t, objects)
		deleteAccepted := reader.deleteAccepted
		reader.deleteAccepted = func(ctx context.Context, key string, versionID string) error {
			putVersionedObject(t, ctx, objects.Client, key, replacement)
			return deleteAccepted(ctx, key, versionID)
		}

		err := reader.ArchiveStatement(ctx, statementSnapshot(source, accepted))

		if !errors.Is(err, application.ErrStatementConflict) {
			t.Fatalf("ArchiveStatement error = %v, want statement conflict", err)
		}
		assertVersionedObject(t, ctx, objects.Client, source, replacement)
		assertVersionedObject(t, ctx, objects.Client, processedName(source), accepted)
		assertObjectVersionMissing(t, ctx, objects.Client, source, acceptedVersion)
	})

	t.Run("conflict before create-only processed write survives", func(t *testing.T) {
		const source = "bankStatements_2018_8_21.csv"
		accepted := []byte("accepted statement bytes")
		conflict := []byte("conflicting processed bytes")
		putVersionedObject(t, ctx, objects.Client, source, accepted)
		reader := newVersionIntegrationReader(t, objects)
		reader.client = &conflictingPutClient{
			minioClient: reader.client,
			beforePut: func(key string) {
				putVersionedObject(t, ctx, objects.Client, key, conflict)
			},
		}

		err := reader.ArchiveStatement(ctx, statementSnapshot(source, accepted))

		if !errors.Is(err, application.ErrStatementConflict) {
			t.Fatalf("ArchiveStatement error = %v, want statement conflict", err)
		}
		assertVersionedObject(t, ctx, objects.Client, source, accepted)
		assertVersionedObject(t, ctx, objects.Client, processedName(source), conflict)
	})
}

type conflictingPutClient struct {
	minioClient
	beforePut func(string)
}

func (client *conflictingPutClient) PutObject(
	ctx context.Context,
	bucket string,
	key string,
	content io.Reader,
	size int64,
	options minio.PutObjectOptions,
) (minio.UploadInfo, error) {
	if client.beforePut != nil {
		beforePut := client.beforePut
		client.beforePut = nil
		beforePut(key)
	}
	return client.minioClient.PutObject(ctx, bucket, key, content, size, options)
}

func newVersionIntegrationReader(
	t *testing.T,
	objects *miniofixture.Fixture,
) *Reader {
	t.Helper()
	t.Setenv(
		"PAYMENT_DATABASE_URL",
		"postgres://fixture:fixture@postgres.example:5432/payment?sslmode=verify-full&sslrootcert=/tmp/fixture-ca.crt",
	)
	t.Setenv("MINIO_ENDPOINT", objects.Endpoint)
	t.Setenv("MINIO_ACCESS_KEY", objects.AccessKey)
	t.Setenv("MINIO_SECRET_KEY", objects.SecretKey)
	t.Setenv("KAFKA_BROKERS", "kafka.example:9093")
	t.Setenv("KAFKA_CA_FILE", "/tmp/fixture-kafka-ca.crt")
	settings, err := config.Load()
	if err != nil {
		t.Fatal("load version integration settings:", err)
	}
	reader, err := NewReader(settings.ObjectStore)
	if err != nil {
		t.Fatal("create version integration reader:", err)
	}
	t.Cleanup(reader.Close)
	return reader
}

func putVersionedObject(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	key string,
	content []byte,
) string {
	t.Helper()
	upload, err := client.PutObject(
		ctx,
		"payments-import",
		key,
		bytes.NewReader(content),
		int64(len(content)),
		minio.PutObjectOptions{ContentType: "text/csv"},
	)
	if err != nil {
		t.Fatal("put versioned statement object:", err)
	}
	if upload.VersionID == "" || upload.VersionID == "null" {
		t.Fatal("versioned statement object has no version ID")
	}
	return upload.VersionID
}

func assertVersionedObject(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	key string,
	want []byte,
) {
	t.Helper()
	object, err := client.GetObject(ctx, "payments-import", key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatal("get versioned statement object:", err)
	}
	defer func() { _ = object.Close() }()
	got, err := io.ReadAll(object)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("object %q mismatch: bytes=%t error=%v", key, bytes.Equal(got, want), err)
	}
}

func assertObjectVersionMissing(
	t *testing.T,
	ctx context.Context,
	client *minio.Client,
	key string,
	versionID string,
) {
	t.Helper()
	_, err := client.StatObject(
		ctx,
		"payments-import",
		key,
		minio.StatObjectOptions{VersionID: versionID},
	)
	if response := minio.ToErrorResponse(err); err == nil || response.StatusCode != 404 {
		t.Fatalf("object %q version remains: %v", key, err)
	}
}
