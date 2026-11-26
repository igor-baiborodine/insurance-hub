package postgres_test

import (
	"os"
	"strings"
	"testing"
)

func TestSQLCSchema_EqualsMigratedSchema(t *testing.T) {
	// given
	schema := readStatements(t, "sql/schema.sql")
	migration := readStatements(t, "../administration/migrations/00001_policy.sql")

	// when
	equal := schema == migration

	// then
	if !equal || !strings.Contains(schema, "CREATE TABLE public.policy_outbox") {
		t.Fatalf("sqlc schema differs from the migration:\n%s\n---\n%s", schema, migration)
	}
}

// readStatements returns the SQL with comment and blank lines removed.
func readStatements(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for line := range strings.Lines(string(content)) {
		if trimmed := strings.TrimSpace(
			line,
		); trimmed != "" &&
			!strings.HasPrefix(trimmed, "--") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "")
}
