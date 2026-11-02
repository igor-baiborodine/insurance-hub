// Command qualificationclient exercises deployed Policy Search transports and produces bounded
// qualification reports. It is test tooling and is excluded from runtime images.
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
	mode                  string
	address               string
	query                 string
	expectDenied          bool
	candidate             string
	httpAddress           string
	grpcAddress           string
	fixture               string
	output                string
	warmupCycles          int
	blocks                int
	blockIndex            int
	cycles                int
	concurrency           string
	candidateOrder        string
	degradedDelay         time.Duration
	kafkaBrokers          string
	kafkaCAFile           string
	registeredTopic       string
	terminatedTopic       string
	consumerGroup         string
	legacyRegisteredTopic string
	legacyTerminatedTopic string
	identitiesPerBlock    int
	javaQuery             string
	goQuery               string
	javaEvent             string
	goEvent               string
	cpuSeconds            float64
	peakRSSBytes          int64
	throttledSeconds      float64
	measuredWallSeconds   float64
	dependencyCPUSeconds  float64
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	settings := parseFlags()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	switch settings.mode {
	case "http", "grpc", "health", "tcp":
		return runLegacyCheck(ctx, settings)
	case "query-report":
		report, err := buildQueryReport(ctx, settings)
		if report != nil {
			if writeErr := writeJSON(settings.output, report); writeErr != nil {
				return writeErr
			}
		}
		return err
	case "event-report":
		report, err := buildEventReport(ctx, settings)
		if report != nil {
			if writeErr := writeJSON(settings.output, report); writeErr != nil {
				return writeErr
			}
		}
		return err
	case "compare":
		report, err := compareReports(settings)
		if report != nil {
			if writeErr := writeJSON(settings.output, report); writeErr != nil {
				return writeErr
			}
		}
		return err
	default:
		return fmt.Errorf("unsupported mode %q", settings.mode)
	}
}

func parseFlags() options {
	var settings options
	flag.StringVar(
		&settings.mode,
		"mode",
		"",
		"http, grpc, health, tcp, query-report, event-report, or compare",
	)
	flag.StringVar(&settings.address, "address", "", "URL or host:port for legacy checks")
	flag.StringVar(
		&settings.query,
		"query",
		"Synthetic Person",
		"query for legacy HTTP/gRPC checks",
	)
	flag.BoolVar(
		&settings.expectDenied,
		"expect-denied",
		false,
		"require a legacy TCP connection to fail",
	)
	flag.StringVar(&settings.candidate, "candidate", "", "java or go")
	flag.StringVar(&settings.httpAddress, "http-address", "", "loopback HTTP policies URL")
	flag.StringVar(&settings.grpcAddress, "grpc-address", "", "loopback gRPC host:port")
	flag.StringVar(&settings.fixture, "fixture", "", "qualification fixture JSON")
	flag.StringVar(&settings.output, "output", "", "owned report output path")
	flag.IntVar(&settings.warmupCycles, "warmup-cycles", 2, "excluded complete warmup cycles")
	flag.IntVar(&settings.blocks, "blocks", 5, "measured blocks")
	flag.IntVar(
		&settings.blockIndex,
		"block-index",
		-1,
		"optional zero-based block to append to an owned report",
	)
	flag.IntVar(&settings.cycles, "cycles", 10, "complete query cycles per block")
	flag.StringVar(
		&settings.concurrency,
		"concurrency",
		"1,4",
		"comma-separated query concurrency profiles",
	)
	flag.StringVar(
		&settings.candidateOrder,
		"candidate-order",
		"unspecified",
		"recorded paired candidate order",
	)
	flag.DurationVar(
		&settings.degradedDelay,
		"degraded-delay",
		0,
		"measurement-only delay for one Go HTTP cycle",
	)
	flag.StringVar(
		&settings.kafkaBrokers,
		"kafka-brokers",
		"",
		"owned loopback Kafka host:port",
	)
	flag.StringVar(&settings.kafkaCAFile, "kafka-ca-file", "", "owned public Kafka CA")
	flag.StringVar(
		&settings.registeredTopic,
		"registered-topic",
		"",
		"owned registration topic",
	)
	flag.StringVar(&settings.terminatedTopic, "terminated-topic", "", "owned termination topic")
	flag.StringVar(
		&settings.consumerGroup,
		"consumer-group",
		"",
		"owned candidate consumer group",
	)
	flag.StringVar(
		&settings.legacyRegisteredTopic,
		"legacy-registered-topic",
		"",
		"optional pinned Java relay topic on the owned broker",
	)
	flag.StringVar(
		&settings.legacyTerminatedTopic,
		"legacy-terminated-topic",
		"",
		"optional pinned Java relay topic on the owned broker",
	)
	flag.IntVar(
		&settings.identitiesPerBlock,
		"identities-per-block",
		4,
		"event identities per block",
	)
	flag.StringVar(&settings.javaQuery, "java-query", "", "Java query report")
	flag.StringVar(&settings.goQuery, "go-query", "", "Go query report")
	flag.StringVar(&settings.javaEvent, "java-event", "", "Java event report")
	flag.StringVar(&settings.goEvent, "go-event", "", "Go event report")
	flag.Float64Var(&settings.cpuSeconds, "cpu-seconds", 0, "candidate cgroup CPU seconds")
	flag.Int64Var(&settings.peakRSSBytes, "peak-rss-bytes", 0, "candidate peak RSS bytes")
	flag.Float64Var(
		&settings.throttledSeconds,
		"throttled-seconds",
		0,
		"candidate cgroup throttled seconds",
	)
	flag.Float64Var(
		&settings.measuredWallSeconds,
		"measured-wall-seconds",
		0,
		"candidate measured wall seconds",
	)
	flag.Float64Var(
		&settings.dependencyCPUSeconds,
		"dependency-cpu-seconds",
		0,
		"paired dependency CPU seconds",
	)
	flag.Parse()
	settings.mode = strings.TrimSpace(settings.mode)
	return settings
}
