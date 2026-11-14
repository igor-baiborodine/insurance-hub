//go:build integration

package application_test

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/jsreport"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/objectstore"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/postgres"
)

func TestGenerateDocumentIntegration_RendersUploadsAndInsertsDuplicate(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	jsreportOrigin := requiredTestURL(t, "DOCUMENT_GENERATION_JSREPORT_URL")
	minioOrigin := requiredTestURL(t, "DOCUMENT_GENERATION_MINIO_URL")
	databaseURL := os.Getenv("DOCUMENT_GENERATION_POSTGRES_URL")
	if databaseURL == "" {
		t.Fatal("DOCUMENT_GENERATION_POSTGRES_URL is required")
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	renderer, err := jsreport.NewClient(jsreport.Settings{
		Origin: jsreportOrigin, ConnectTimeout: 2 * time.Second,
		RequestTimeout: 8 * time.Second, MaximumBytes: 8 * 1024 * 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(renderer.Close)
	storeSettings := config.ObjectStore{
		Endpoint: minioOrigin, Bucket: "policies", FetchTimeout: 3 * time.Second,
	}
	uploader, err := objectstore.NewWriter(storeSettings, 8*time.Second, 8*1024*1024)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(uploader.Close)
	metadata, err := postgres.NewWriter(pool, time.Second, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := application.NewGenerateDocument(renderer, uploader, metadata)
	if err != nil {
		t.Fatal(err)
	}
	registration := syntheticRegistration(t)

	// when
	first, err := generation.Execute(ctx, registration)
	if err == nil {
		_, err = generation.Execute(ctx, registration)
	}

	// then
	if err != nil {
		t.Fatal(err)
	}
	if first.ObjectKey != "2026/02/SYN-363.pdf" {
		t.Fatalf("object key = %q", first.ObjectKey)
	}
}

func requiredTestURL(t *testing.T, name string) *url.URL {
	t.Helper()
	value := os.Getenv(name)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		t.Fatalf("%s must be a valid URL", name)
	}
	return parsed
}
