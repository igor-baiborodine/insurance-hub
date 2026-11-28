//go:build integration

// Package miniofixture provides a disposable payments-import bucket for integration tests.
package miniofixture

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	minioImage = "cgr.dev/chainguard/minio@sha256:7ad2a5efd83f0ca1b8e00af9f7ec7fc3ff977c38ed29b942b0c47460ad2f8f3e"
	bucketName = "payments-import"
	accessKey  = "fixture-admin"
	secretKey  = "synthetic-minio-admin-487"
)

// Fixture is one disposable MinIO server and isolated Payment bucket.
type Fixture struct {
	Client    *minio.Client
	Endpoint  string
	AccessKey string
	SecretKey string
}

// New starts the pinned MinIO image and creates an empty payments-import bucket.
func New(t *testing.T, ctx context.Context) *Fixture {
	t.Helper()
	container, err := testcontainers.Run(
		ctx,
		minioImage,
		testcontainers.WithEnv(map[string]string{
			"MINIO_ROOT_USER":     accessKey,
			"MINIO_ROOT_PASSWORD": secretKey,
		}),
		testcontainers.WithExposedPorts("9000/tcp"),
		testcontainers.WithTmpfs(map[string]string{
			"/data": "rw,uid=65532,gid=65532,mode=0700",
		}),
		testcontainers.WithEntrypoint("minio"),
		testcontainers.WithCmd("server", "/data", "--address", ":9000"),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/minio/health/ready").
				WithPort("9000/tcp").
				WithStartupTimeout(time.Minute),
		),
	)
	if err != nil {
		t.Fatal("start disposable MinIO:", err)
	}
	testcontainers.CleanupContainer(t, container, testcontainers.StopTimeout(2*time.Second))
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal("resolve disposable MinIO host")
	}
	port, err := container.MappedPort(ctx, "9000/tcp")
	if err != nil {
		t.Fatal("resolve disposable MinIO port")
	}
	endpoint := net.JoinHostPort(host, port.Port())
	client, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure:       false,
		BucketLookup: minio.BucketLookupPath,
		MaxRetries:   1,
	})
	if err != nil {
		t.Fatal("create disposable MinIO client")
	}
	makeBucket(t, ctx, client)
	return &Fixture{
		Client: client, Endpoint: fmt.Sprintf("http://%s", endpoint),
		AccessKey: accessKey, SecretKey: secretKey,
	}
}

func makeBucket(t *testing.T, ctx context.Context, client *minio.Client) {
	t.Helper()
	readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := client.MakeBucket(readyCtx, bucketName, minio.MakeBucketOptions{})
		if err == nil {
			return
		}
		response := minio.ToErrorResponse(err)
		if response.Code != "XMinioServerNotInitialized" && response.StatusCode != 503 {
			t.Fatal("create disposable payments-import bucket:", err)
		}
		select {
		case <-readyCtx.Done():
			t.Fatal("create disposable payments-import bucket:", readyCtx.Err())
		case <-ticker.C:
		}
	}
}
