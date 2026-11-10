// Command smokeclient performs bounded disposable HTTP, gRPC, health and access checks.
// It is test tooling and is excluded from runtime images.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

type options struct {
	mode         string
	address      string
	query        string
	expectDenied bool
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	settings := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return runCheck(ctx, settings)
}

func parseFlags() options {
	var settings options
	flag.StringVar(&settings.mode, "mode", "", "http, grpc, health, or tcp")
	flag.StringVar(&settings.address, "address", "", "URL or host:port")
	flag.StringVar(&settings.query, "query", "Synthetic Person", "search query")
	flag.BoolVar(
		&settings.expectDenied,
		"expect-denied",
		false,
		"require a TCP connection to fail",
	)
	flag.Parse()
	settings.mode = strings.TrimSpace(settings.mode)
	return settings
}
