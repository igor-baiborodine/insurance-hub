package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/logger"

	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/pricing-service/internal/service"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, service.RunSeed)
	stop()
	if err != nil {
		logger.New(os.Stderr, "pricing-seed", slog.LevelInfo).
			ErrorContext(ctx, "pricing administration failed", "error", err)
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	args []string,
	output io.Writer,
	seed func(context.Context, string, string, string, string, string) (administration.SeedResult, error),
) error {
	flags := flag.NewFlagSet("pricing-seed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	environment := flags.String("environment", "", "explicit environment")
	contextName := flags.String("context", "", "explicit Kubernetes context")
	targetPath := flags.String("target-file", "", "protected target enrollment")
	urlPath := flags.String("database-url-file", "", "seeder credential file")
	caPath := flags.String("ca-file", "", "selected PostgreSQL CA")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid seed command arguments")
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	result, err := seed(
		ctx,
		*environment,
		*contextName,
		*targetPath,
		*urlPath,
		*caPath,
	)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "inserted=%d existing=%d\n", result.Inserted, result.Existing)
	return err
}
