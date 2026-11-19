package service

import (
	"path/filepath"
	"testing"
)

func TestRunMigration_RejectsInvalidOperationAndMissingEnrollment(t *testing.T) {
	// Given invalid operation or missing enrollment, the service rejects it before opening PostgreSQL.
	missing := filepath.Join(t.TempDir(), "target.json")
	for _, tc := range []struct {
		operation string
		want      string
	}{
		{"unsupported", "operation must be migrate, status or version"},
		{MigrationOperationStatus, "target enrollment must be a protected regular file"},
	} {
		t.Run(tc.operation, func(t *testing.T) {
			state, err := RunMigration(
				t.Context(),
				tc.operation,
				"qa",
				"qa-insurance-hub",
				missing,
				"",
				"",
			)
			if state != "" || err == nil || err.Error() != tc.want {
				t.Fatalf(
					"expected %q and empty state, got %v and %q",
					tc.want,
					err,
					state,
				)
			}
		})
	}
}
