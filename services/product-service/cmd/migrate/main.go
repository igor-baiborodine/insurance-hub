package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "product migration:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	return runWith(ctx, args, output, service.RunMigration)
}

func runWith(
	ctx context.Context,
	args []string,
	output io.Writer,
	runMigration func(context.Context, string, administration.Target, string) (string, error),
) error {
	flags := flag.NewFlagSet("product-migrate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	operation := flags.String("operation", "", "migrate, status or version")
	environment := flags.String("environment", "", "explicit environment")
	contextName := flags.String("context", "", "explicit Kubernetes context")
	targetPath := flags.String("target-file", "", "protected target enrollment")
	urlPath := flags.String("database-url-file", "", "migrator credential file")
	caPath := flags.String("ca-file", "", "selected CNPG CA")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid migration command arguments")
	}
	if *operation != service.MigrationOperationMigrate &&
		*operation != service.MigrationOperationStatus &&
		*operation != service.MigrationOperationVersion {
		return errors.New("operation must be migrate, status or version")
	}
	target, err := administration.ReadTarget(*targetPath)
	if err != nil {
		return err
	}
	if *urlPath == "" {
		return errors.New("database URL file is required")
	}
	content, err := os.ReadFile(*urlPath)
	if err != nil {
		return errors.New("cannot read migrator credential file")
	}
	databaseURL := strings.TrimSpace(string(content))
	if err := target.ValidateMigrationConnection(
		*environment, *contextName, databaseURL, *caPath,
	); err != nil {
		return err
	}
	info, err := os.Stat(*caPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("selected CNPG CA file is unavailable")
	}
	state, err := runMigration(ctx, databaseURL, target, *operation)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(output, state)
	return err
}
