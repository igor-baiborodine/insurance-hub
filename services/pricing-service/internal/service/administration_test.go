package service

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRunMigration_RejectsInvalidOperationAndMissingEnrollment(t *testing.T) {
	// given
	missing := filepath.Join(t.TempDir(), "target.json")
	for _, tc := range []struct {
		name, operation string
		want            string
	}{
		{"unsupported operation", "unsupported", "operation must be migrate, status or version"},
		{"missing enrollment", MigrationOperationStatus, "target enrollment must be a protected regular file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// when
			state, err := RunMigration(
				t.Context(),
				tc.operation,
				"qa",
				"qa-insurance-hub",
				missing,
				"",
				"",
			)
			// then
			if state != "" || err == nil || !strings.Contains(err.Error(), tc.want) {
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
