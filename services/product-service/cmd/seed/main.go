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
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/administration"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/service"
)

const seedCommandBudget = 60 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "product seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	return runWith(ctx, args, output, service.RunSeed)
}

func runWith(
	ctx context.Context,
	args []string,
	output io.Writer,
	runSeed func(
		context.Context,
		string,
		administration.Target,
		string,
	) (administration.SeedResult, error),
) error {
	ctx, cancel := context.WithTimeout(ctx, seedCommandBudget)
	defer cancel()
	flags := flag.NewFlagSet("product-seed", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	environment := flags.String("environment", "", "explicit environment")
	contextName := flags.String("context", "", "explicit Kubernetes context")
	targetPath := flags.String("target-file", "", "protected target enrollment")
	catalogPath := flags.String("catalog-file", "", "product seed catalog")
	urlPath := flags.String("database-url-file", "", "seeder credential file")
	caPath := flags.String("ca-file", "", "selected CNPG CA")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return errors.New("invalid seed command arguments")
	}
	target, err := administration.ReadTarget(*targetPath)
	if err != nil {
		return err
	}
	if *catalogPath == "" {
		return errors.New("product seed catalog file is required")
	}
	if *urlPath == "" {
		return errors.New("database URL file is required")
	}
	content, err := os.ReadFile(*urlPath)
	if err != nil {
		return errors.New("cannot read seeder credential file")
	}
	databaseURL := strings.TrimSpace(string(content))
	if err := target.ValidateSeedConnection(
		*environment,
		*contextName,
		databaseURL,
		*caPath,
	); err != nil {
		return err
	}
	info, err := os.Stat(*caPath)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("selected CNPG CA file is unavailable")
	}
	result, err := runSeed(ctx, databaseURL, target, *catalogPath)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(
		output,
		"inserted=%d existing=%d\n",
		result.Inserted,
		result.Existing,
	)
	return err
}
