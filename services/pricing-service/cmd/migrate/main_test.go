package main

import (
	"bytes"
	"testing"
)

func TestRun_RejectsInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown flag", []string{"--unknown"}, "invalid migration command arguments"},
		{"positional input", []string{"status"}, "invalid migration command arguments"},
		{
			"unbounded timeout",
			[]string{"--timeout", "181s"},
			"migration timeout must be greater than zero and at most 180s",
		},
		{
			"unsupported operation",
			[]string{"--operation", "down"},
			"operation must be migrate, status or version",
		},
		{
			"missing enrollment",
			[]string{"--operation", "status"},
			"target enrollment must be a protected regular file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given invalid input, when the entry point runs, then it fails without output.
			var output bytes.Buffer
			err := run(t.Context(), tt.args, &output)
			if err == nil || err.Error() != tt.want || output.Len() != 0 {
				t.Fatalf(
					"want %q and empty output, got %v and %q",
					tt.want,
					err,
					output.String(),
				)
			}
		})
	}
}
