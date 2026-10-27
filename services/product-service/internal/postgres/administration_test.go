package postgres

import (
	"context"
	"testing"
)

func TestOpenMigrationDatabase_RejectsInvalidConnectionConfiguration(t *testing.T) {
	if db, err := OpenMigrationDatabase(
		context.Background(),
		"not-a-postgres-url",
	); err == nil {
		_ = db.Close()
		t.Fatal("invalid PostgreSQL URL was accepted")
	}
}

func TestOpenSeedDatabase_RejectsInvalidConnectionConfiguration(t *testing.T) {
	if db, err := OpenSeedDatabase(context.Background(), "not-a-postgres-url"); err == nil {
		_ = db.Close()
		t.Fatal("invalid PostgreSQL URL was accepted")
	}
}
