package service

import (
	"strings"
	"testing"
)

func TestRunMigration_RefusesBeforeConnecting(t *testing.T) {
	for _, test := range []struct{ name, operation, want string }{
		{"unsupported operation", "down", "operation must be migrate or status"},
		{"missing enrollment", "status", "target enrollment must be a private regular file"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// when
			state, err := RunMigration(t.Context(), test.operation, "local-dev",
				"kind-local-dev-insurance-hub", "", "", "")

			// then
			if state != "" || err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected refusal %q, got %q (%v)", test.want, state, err)
			}
		})
	}
}
