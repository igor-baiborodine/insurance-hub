package main

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func TestRun_RejectsInvalidArguments(t *testing.T) {
	for _, test := range []struct {
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
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			var output bytes.Buffer
			called := false

			// when
			err := run(t.Context(), test.args, &output, func(
				context.Context, string, string, string, string, string, string,
			) (string, error) {
				called = true
				return "", nil
			})

			// then
			if err == nil || err.Error() != test.want || called || output.Len() != 0 {
				t.Fatalf("want %q without a database call, got %v", test.want, err)
			}
		})
	}
}

func TestRun_PrintsOnlySafeOperationResult(t *testing.T) {
	// given
	var output bytes.Buffer
	args := []string{
		"--operation", "status", "--environment", "local-dev", "--context", "kind",
		"--target-file", "target.json", "--database-url-file", "url", "--ca-file", "ca.crt",
	}
	var received []string

	// when
	err := run(t.Context(), args, &output, func(
		_ context.Context, operation, environment, contextName, target, url, ca string,
	) (string, error) {
		received = []string{operation, environment, contextName, target, url, ca}
		return "unmanaged", nil
	})
	failure := run(t.Context(), args, &output, func(
		context.Context, string, string, string, string, string, string,
	) (string, error) {
		return "", errors.New("refused")
	})

	// then
	if err != nil || output.String() != "unmanaged\n" || len(received) != 6 ||
		received[0] != "status" || received[5] != "ca.crt" || failure == nil {
		t.Fatalf(
			"unexpected result %q, %v, %v, %v",
			output.String(),
			received,
			err,
			failure,
		)
	}
}
