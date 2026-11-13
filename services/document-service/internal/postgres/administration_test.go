package postgres

import (
	"context"
	"errors"
	"testing"
)

func TestOpenMigrationDatabase_RejectsInvalidConfigurationAndCancellation(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, url := range []string{"not-a-postgres-url", "postgres://synthetic@localhost/document"} {
		t.Run(url, func(t *testing.T) {
			// when
			db, err := OpenMigrationDatabase(ctx, url)
			// then
			if err == nil || db != nil {
				t.Fatal("invalid or canceled connection was accepted")
			}
			if url != "not-a-postgres-url" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was lost: %v", err)
			}
		})
	}
}
