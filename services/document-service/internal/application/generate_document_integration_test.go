//go:build integration

package application_test

import (
	"context"
	"errors"
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
	databaseURL := os.Getenv("DOCUMENT_GENERATION_POSTGRES_URL")
	if databaseURL == "" {
		t.Fatal("DOCUMENT_GENERATION_POSTGRES_URL is required")
	}
	generation := newIntegrationGeneration(t, ctx, databaseURL)
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

func TestGenerateDocumentIntegration_DatabaseFailureLeavesUploadedObject(t *testing.T) {
	// given
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL := os.Getenv("DOCUMENT_GENERATION_POSTGRES_URL")
	if databaseURL == "" {
		t.Fatal("DOCUMENT_GENERATION_POSTGRES_URL is required")
	}
	generation := newIntegrationGeneration(t, ctx, databaseURL)
	registration := syntheticRegistrationFor(t, "SYN-363-ORPHAN")

	// when
	_, err := generation.Execute(ctx, registration)

	// then
	if !errors.Is(err, application.ErrUnavailable) {
		t.Fatalf("database failure = %v", err)
	}
}

func newIntegrationGeneration(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
) *application.GenerateDocument {
	t.Helper()
	configured, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	renderer, err := jsreport.NewClient(jsreport.Settings{
		Origin:         requiredTestURL(t, "DOCUMENT_GENERATION_JSREPORT_URL"),
		ConnectTimeout: 2 * time.Second, RequestTimeout: 8 * time.Second,
		MaximumBytes: configured.MaximumDocumentBytes,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(renderer.Close)
	uploader, err := objectstore.NewWriter(
		configured.Events.WriterObjectStore,
		configured.Events.UploadTimeout,
		configured.MaximumDocumentBytes,
	)
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
	return generation
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
