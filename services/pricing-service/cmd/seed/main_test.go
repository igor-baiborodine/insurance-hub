package main

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/administration"
)

func TestRun_ReportsOnlySeedCounts(t *testing.T) {
	// given
	args := []string{
		"--environment",
		"qa",
		"--context",
		"qa-insurance-hub",
		"--target-file",
		"target.json",
		"--database-url-file",
		"credential",
		"--ca-file",
		"ca",
	}
	var output bytes.Buffer
	// when
	err := run(
		t.Context(),
		args,
		&output,
		func(ctx context.Context, env, cluster, target, url, ca string) (administration.SeedResult, error) {
			t.Helper()
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("seed has no command deadline")
			}
			got := []string{env, cluster, target, url, ca}
			if !slices.Equal(
				got,
				[]string{
					"qa",
					"qa-insurance-hub",
					"target.json",
					"credential",
					"ca",
				},
			) {
				t.Fatalf("incorrect seed arguments: %v", got)
			}
			return administration.SeedResult{Inserted: 1, Existing: 3}, nil
		},
	)
	// then
	if err != nil || output.String() != "inserted=1 existing=3\n" {
		t.Fatalf("unexpected seed output %q (%v)", output.String(), err)
	}
}

func TestRun_RejectsInvalidArgumentsAndReportsFailure(t *testing.T) {
	// given
	var output bytes.Buffer
	called := false
	failure := errors.New("seed refused")
	seed := func(context.Context, string, string, string, string, string) (administration.SeedResult, error) {
		called = true
		return administration.SeedResult{}, failure
	}
	// when
	err := run(t.Context(), []string{"unexpected"}, &output, seed)
	// then
	if err == nil || called {
		t.Fatal("invalid arguments reached the seed operation")
	}
	// when
	err = run(
		t.Context(),
		nil,
		&output,
		seed,
	)
	// then
	if !errors.Is(err, failure) || output.Len() != 0 {
		t.Fatalf(
			"seed failure produced output %q or unexpected error %v",
			output.String(),
			err,
		)
	}
}
