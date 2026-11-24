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

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/service"
)

const maximumTimeout = 180 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1:], os.Stdout, service.RunMigration)
	stop()
	if err != nil {
		logger.New(os.Stderr, "policy-migrate", slog.LevelInfo).
			ErrorContext(ctx, "policy administration failed", "error", err)
		os.Exit(1)
	}
}

type runMigration func(context.Context, string, string, string, string, string, string) (
	string, error,
)

func run(ctx context.Context, args []string, output io.Writer, migrate runMigration) error {
	flags := flag.NewFlagSet("policy-migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	operation := flags.String("operation", "", "migrate or status")
	environment := flags.String("environment", "", "explicit environment")
	contextName := flags.String("context", "", "explicit Kubernetes context")
	targetPath := flags.String("target-file", "", "protected target enrollment")
	urlPath := flags.String("database-url-file", "", "migrator credential file")
	caPath := flags.String("ca-file", "", "selected PostgreSQL CA")
	timeout := flags.Duration("timeout", 120*time.Second, "overall operation timeout")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid migration command arguments")
	}
	if *timeout <= 0 || *timeout > maximumTimeout {
		return errors.New("migration timeout must be greater than zero and at most 180s")
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	result, err := migrate(ctx, *operation, *environment, *contextName,
		*targetPath, *urlPath, *caPath)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, result)
	return err
}
