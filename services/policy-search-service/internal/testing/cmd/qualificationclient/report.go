package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
)

const (
	maxHTTPResponseBytes = 1 << 20
	reportSchemaVersion  = 1
	fixtureOwnership     = "issue-234-qualification"
	pairedCandidateOrder = "java-go,go-java,java-go,go-java,java-go"
)

type workloadFixture struct {
	SchemaVersion int          `json:"schemaVersion"`
	Ownership     string       `json:"ownership"`
	Queries       []queryCase  `json:"queries"`
	Events        eventFixture `json:"events"`
}

type queryCase struct {
	ID              string   `json:"id"`
	Query           string   `json:"query"`
	ExpectedRule    string   `json:"expectedRule"`
	ExpectedNumbers []string `json:"expectedNumbers,omitempty"`
}

type eventFixture struct {
	Registration json.RawMessage `json:"registration"`
	Termination  json.RawMessage `json:"termination"`
}

type resourceObservation struct {
	CPUSeconds           float64 `json:"cpuSeconds"`
	PeakRSSBytes         int64   `json:"peakRssBytes"`
	ThrottledSeconds     float64 `json:"throttledSeconds"`
	MeasuredWallSeconds  float64 `json:"measuredWallSeconds"`
	DependencyCPUSeconds float64 `json:"dependencyCpuSeconds"`
}

type queryReport struct {
	SchemaVersion  int                 `json:"schemaVersion"`
	Kind           string              `json:"kind"`
	Candidate      string              `json:"candidate"`
	CandidateOrder string              `json:"candidateOrder"`
	FixtureSHA256  string              `json:"fixtureSha256"`
	CreatedAt      string              `json:"createdAt"`
	WarmupCycles   int                 `json:"warmupCycles"`
	Blocks         int                 `json:"blocks"`
	Cycles         int                 `json:"cycles"`
	Concurrency    []int               `json:"concurrency"`
	Resources      resourceObservation `json:"resources"`
	Samples        []querySample       `json:"samples"`
	Complete       bool                `json:"complete"`
	Failure        string              `json:"failure,omitempty"`
}

type querySample struct {
	Block        int    `json:"block"`
	Cycle        int    `json:"cycle"`
	Concurrency  int    `json:"concurrency"`
	Endpoint     string `json:"endpoint"`
	QueryID      string `json:"queryId"`
	DurationNS   int64  `json:"durationNs"`
	Status       int    `json:"status"`
	ResponseHash string `json:"responseHash"`
	Outcome      string `json:"outcome"`
}

type eventReport struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Kind          string              `json:"kind"`
	Candidate     string              `json:"candidate"`
	FixtureSHA256 string              `json:"fixtureSha256"`
	CreatedAt     string              `json:"createdAt"`
	Blocks        int                 `json:"blocks"`
	Identities    int                 `json:"identitiesPerBlock"`
	Resources     resourceObservation `json:"resources"`
	Relay         string              `json:"relay,omitempty"`
	Samples       []eventSample       `json:"samples"`
	Complete      bool                `json:"complete"`
	Failure       string              `json:"failure,omitempty"`
}

type eventSample struct {
	Block      int    `json:"block"`
	Identity   int    `json:"identity"`
	Sequence   int    `json:"sequence"`
	EventType  string `json:"eventType"`
	DurationNS int64  `json:"durationNs"`
	Outcome    string `json:"outcome"`
}

type comparisonReport struct {
	SchemaVersion int                `json:"schemaVersion"`
	Kind          string             `json:"kind"`
	CreatedAt     string             `json:"createdAt"`
	Passed        bool               `json:"passed"`
	Measures      []comparisonResult `json:"measures"`
	Failures      []string           `json:"failures"`
}

type comparisonResult struct {
	Name        string  `json:"name"`
	QueryID     string  `json:"queryId,omitempty"`
	Profile     int     `json:"concurrency,omitempty"`
	BaselineMS  float64 `json:"baselineMs"`
	CandidateMS float64 `json:"candidateMs"`
	Ratio       float64 `json:"ratio"`
	LimitMS     float64 `json:"limitMs"`
	IntervalLo  float64 `json:"intervalLow"`
	IntervalHi  float64 `json:"intervalHigh"`
	Passed      bool    `json:"passed"`
}

func runLegacyCheck(ctx context.Context, settings options) error {
	if settings.address == "" {
		return errors.New("address is required")
	}
	switch settings.mode {
	case "http":
		return checkHTTP(ctx, settings.address, settings.query)
	case "grpc":
		return checkGRPC(ctx, settings.address, settings.query)
	case "health":
		return checkHealth(ctx, settings.address)
	case "tcp":
		return checkTCP(ctx, settings.address, settings.expectDenied)
	default:
		return errors.New("unsupported legacy mode")
	}
}

func loadFixture(path string) (workloadFixture, string, error) {
	var fixture workloadFixture
	if path == "" {
		return fixture, "", errors.New("fixture is required")
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		return fixture, "", fmt.Errorf("read fixture: %w", err)
	}
	if err := json.Unmarshal(payload, &fixture); err != nil {
		return fixture, "", fmt.Errorf("decode fixture: %w", err)
	}
	if fixture.SchemaVersion != 1 || fixture.Ownership != fixtureOwnership ||
		len(fixture.Queries) != 7 {
		return fixture, "", errors.New("fixture identity or seven-query contract differs")
	}
	digest := sha256.Sum256(payload)
	return fixture, hex.EncodeToString(digest[:]), nil
}

func buildQueryReport(ctx context.Context, settings options) (*queryReport, error) {
	report := &queryReport{
		SchemaVersion:  reportSchemaVersion,
		Kind:           "query-report",
		Candidate:      settings.candidate,
		CandidateOrder: settings.candidateOrder,
		CreatedAt: time.Now().
			UTC().
			Format(time.RFC3339Nano),
		WarmupCycles: settings.warmupCycles,
		Blocks:       settings.blocks,
		Cycles:       settings.cycles,
		Resources:    resourcesFrom(settings),
	}
	fixture, digest, err := loadFixture(settings.fixture)
	report.FixtureSHA256 = digest
	if err != nil {
		return failQueryReport(report, err)
	}
	profiles, err := parseConcurrency(settings.concurrency)
	report.Concurrency = profiles
	if err != nil {
		return failQueryReport(report, err)
	}
	if settings.candidate != "java" && settings.candidate != "go" {
		return failQueryReport(report, errors.New("candidate must be java or go"))
	}
	if err := requireLoopbackURL(settings.httpAddress); err != nil {
		return failQueryReport(report, err)
	}
	if settings.candidate == "go" && settings.grpcAddress == "" {
		return failQueryReport(report, errors.New("go query report requires grpc-address"))
	}
	if settings.grpcAddress != "" {
		if err := requireLoopbackAddress(settings.grpcAddress); err != nil {
			return failQueryReport(report, err)
		}
	}
	if settings.blocks != 5 || settings.cycles != 10 || settings.warmupCycles != 2 ||
		!slices.Equal(profiles, []int{1, 4}) {
		return failQueryReport(
			report,
			errors.New(
				"query workload must use warmup 2, blocks 5, cycles 10, concurrency 1,4",
			),
		)
	}
	if settings.blockIndex < -1 || settings.blockIndex >= settings.blocks {
		return failQueryReport(
			report,
			errors.New("block-index is outside the query workload"),
		)
	}
	if settings.candidateOrder != pairedCandidateOrder {
		return failQueryReport(
			report,
			fmt.Errorf("candidate-order must be %s", pairedCandidateOrder),
		)
	}

	endpoints := []string{"http"}
	if settings.grpcAddress != "" {
		endpoints = append(endpoints, "grpc")
	}
	for _, profile := range profiles {
		for _, endpoint := range endpoints {
			for range settings.warmupCycles {
				if err := runQueryCycle(
					ctx,
					settings,
					fixture.Queries,
					profile,
					endpoint,
					-1,
					-1,
					nil,
				); err != nil {
					return failQueryReport(
						report,
						fmt.Errorf("warmup: %w", err),
					)
				}
			}
			for _, block := range selectedBlocks(settings.blocks, settings.blockIndex) {
				for cycle := range settings.cycles {
					if settings.degradedDelay > 0 &&
						settings.candidate == "go" &&
						endpoint == "http" &&
						block == 0 &&
						cycle == 0 {
						select {
						case <-time.After(settings.degradedDelay):
						case <-ctx.Done():
							return failQueryReport(report, ctx.Err())
						}
					}
					if err := runQueryCycle(
						ctx,
						settings,
						fixture.Queries,
						profile,
						endpoint,
						block,
						cycle,
						&report.Samples,
					); err != nil {
						return failQueryReport(report, err)
					}
				}
			}
		}
	}
	return mergeQueryReport(settings.output, report)
}

func runQueryCycle(
	ctx context.Context,
	settings options,
	queries []queryCase,
	profile int,
	endpoint string,
	block int,
	cycle int,
	samples *[]querySample,
) error {
	type result struct {
		sample querySample
		err    error
	}
	results := make(chan result, len(queries))
	semaphore := make(chan struct{}, profile)
	var group sync.WaitGroup
	for _, query := range queries {
		group.Add(1)
		go func(query queryCase) {
			defer group.Done()
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results <- result{err: ctx.Err()}
				return
			}
			sample, err := measureQuery(
				ctx,
				settings,
				endpoint,
				query,
				block,
				cycle,
				profile,
			)
			results <- result{sample: sample, err: err}
		}(query)
	}
	group.Wait()
	close(results)
	var firstErr error
	for result := range results {
		if samples != nil {
			*samples = append(*samples, result.sample)
		}
		if result.err != nil && firstErr == nil {
			firstErr = result.err
		}
	}
	return firstErr
}

func measureQuery(
	ctx context.Context,
	settings options,
	endpoint string,
	query queryCase,
	block int,
	cycle int,
	profile int,
) (querySample, error) {
	sample := querySample{
		Block:       block,
		Cycle:       cycle,
		Concurrency: profile,
		Endpoint:    endpoint,
		QueryID:     query.ID,
	}
	attempt, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	var numbers []string
	var payload []byte
	var err error
	if endpoint == "http" {
		sample.Status, payload, err = request(attempt, settings.httpAddress, query.Query)
		if err == nil {
			numbers, err = policyNumbersFromHTTP(payload)
		}
	} else {
		numbers, payload, err = requestGRPC(attempt, settings.grpcAddress, query.Query)
		sample.Status = http.StatusOK
	}
	sample.DurationNS = time.Since(started).Nanoseconds()
	digest := sha256.Sum256(payload)
	sample.ResponseHash = hex.EncodeToString(digest[:])
	if err != nil {
		sample.Outcome = "error"
		return sample, fmt.Errorf("%s %s: %w", endpoint, query.ID, err)
	}
	if sample.Status != http.StatusOK {
		sample.Outcome = "unexpected-status"
		return sample, fmt.Errorf("%s %s returned %d", endpoint, query.ID, sample.Status)
	}
	if err := validateQueryNumbers(query, numbers); err != nil {
		sample.Outcome = "mismatch"
		return sample, err
	}
	sample.Outcome = "passed"
	return sample, nil
}

func buildEventReport(ctx context.Context, settings options) (*eventReport, error) {
	report := &eventReport{
		SchemaVersion: reportSchemaVersion,
		Kind:          "event-report",
		Candidate:     settings.candidate,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Blocks:        settings.blocks,
		Identities:    settings.identitiesPerBlock,
		Resources:     resourcesFrom(settings),
	}
	fixture, digest, err := loadFixture(settings.fixture)
	report.FixtureSHA256 = digest
	if err != nil {
		return failEventReport(report, err)
	}
	if settings.candidate != "java" && settings.candidate != "go" {
		return failEventReport(report, errors.New("candidate must be java or go"))
	}
	if settings.blocks != 3 || settings.identitiesPerBlock != 4 {
		return failEventReport(
			report,
			errors.New("event workload must use blocks 3 and identities-per-block 4"),
		)
	}
	if settings.blockIndex < -1 || settings.blockIndex >= settings.blocks {
		return failEventReport(
			report,
			errors.New("block-index is outside the event workload"),
		)
	}
	if err := requireLoopbackURL(settings.httpAddress); err != nil {
		return failEventReport(report, err)
	}
	if err := validateOwnedKafka(settings); err != nil {
		return failEventReport(report, err)
	}
	if err := validateLegacyRelay(settings); err != nil {
		return failEventReport(report, err)
	}
	if settings.candidate == "java" {
		report.Relay = "owned audit topic to pinned legacy topic on the same disposable broker"
	}
	client, err := newKafkaClient(settings)
	if err != nil {
		return failEventReport(report, err)
	}
	defer client.Close()
	if err := client.Ping(ctx); err != nil {
		return failEventReport(report, fmt.Errorf("ping Kafka: %w", err))
	}
	if settings.blockIndex < 1 {
		if _, err := runEventIdentity(
			ctx,
			settings,
			client,
			fixture,
			"SYN-234-"+strings.ToUpper(settings.candidate)+"-WARMUP",
			-1,
			-1,
		); err != nil {
			return failEventReport(report, fmt.Errorf("event warmup: %w", err))
		}
	}
	for _, block := range selectedBlocks(settings.blocks, settings.blockIndex) {
		for identity := range settings.identitiesPerBlock {
			id := fmt.Sprintf(
				"SYN-234-%s-%02d-%02d",
				strings.ToUpper(settings.candidate),
				block,
				identity,
			)
			samples, err := runEventIdentity(
				ctx,
				settings,
				client,
				fixture,
				id,
				block,
				identity,
			)
			if err != nil {
				return failEventReport(report, err)
			}
			report.Samples = append(report.Samples, samples...)
		}
	}
	return mergeEventReport(settings.output, report)
}

func runEventIdentity(
	ctx context.Context,
	settings options,
	client *kgo.Client,
	fixture workloadFixture,
	id string,
	block, identity int,
) ([]eventSample, error) {
	sequence := []struct {
		name    string
		topic   string
		payload json.RawMessage
	}{
		{"registration", settings.registeredTopic, fixture.Events.Registration},
		{"registration-duplicate", settings.registeredTopic, fixture.Events.Registration},
		{"termination", settings.terminatedTopic, fixture.Events.Termination},
		{"registration-reverse", settings.registeredTopic, fixture.Events.Registration},
	}
	samples := make([]eventSample, 0, len(sequence))
	for index, event := range sequence {
		payload, expected, err := eventPayload(event.payload, id)
		if err != nil {
			return nil, err
		}
		started := time.Now()
		produceResult := client.ProduceSync(ctx, &kgo.Record{
			Topic: event.topic,
			Key:   []byte(id),
			Value: payload,
		})
		if err := produceResult.FirstErr(); err != nil {
			return nil, fmt.Errorf("produce %s: %w", event.name, err)
		}
		if settings.candidate == "java" {
			relayTopic := settings.legacyRegisteredTopic
			if event.topic == settings.terminatedTopic {
				relayTopic = settings.legacyTerminatedTopic
			}
			relayResult := client.ProduceSync(ctx, &kgo.Record{
				Topic: relayTopic,
				Key:   []byte(id),
				Value: payload,
			})
			if err := relayResult.FirstErr(); err != nil {
				return nil, fmt.Errorf("relay %s: %w", event.name, err)
			}
		}
		if err := waitForExpectedPolicy(
			ctx,
			settings.httpAddress,
			id,
			expected,
		); err != nil {
			return nil, err
		}
		if block >= 0 {
			samples = append(samples, eventSample{
				Block:      block,
				Identity:   identity,
				Sequence:   index,
				EventType:  event.name,
				DurationNS: time.Since(started).Nanoseconds(),
				Outcome:    "passed",
			})
		}
	}
	return samples, nil
}

func selectedBlocks(total, selected int) []int {
	if selected < 0 {
		blocks := make([]int, total)
		for index := range blocks {
			blocks[index] = index
		}
		return blocks
	}
	return []int{selected}
}

func mergeQueryReport(path string, current *queryReport) (*queryReport, error) {
	if current == nil {
		return nil, errors.New("query report is required")
	}
	if current.Blocks != 5 || current.Cycles != 10 ||
		(current.Candidate != "java" && current.Candidate != "go") {
		return failQueryReport(current, errors.New("query report merge identity differs"))
	}
	if len(current.Samples) == 0 {
		return failQueryReport(current, errors.New("query report block has no samples"))
	}
	if _, err := os.Stat(path); err == nil && current.Samples[0].Block >= 0 {
		var prior queryReport
		if err := readJSON(path, &prior); err != nil {
			return failQueryReport(current, err)
		}
		if prior.Candidate != current.Candidate ||
			prior.FixtureSHA256 != current.FixtureSHA256 ||
			prior.CandidateOrder != current.CandidateOrder {
			return failQueryReport(
				current,
				errors.New("existing query report identity differs"),
			)
		}
		seen := map[int]bool{}
		for _, sample := range prior.Samples {
			seen[sample.Block] = true
		}
		if seen[current.Samples[0].Block] {
			return failQueryReport(
				current,
				errors.New("query block was already recorded"),
			)
		}
		current.Samples = append(prior.Samples, current.Samples...)
	}
	want := 700
	if current.Candidate == "go" {
		want = 1400
	}
	current.Complete = len(current.Samples) == want
	return current, nil
}

func mergeEventReport(path string, current *eventReport) (*eventReport, error) {
	if current == nil {
		return nil, errors.New("event report is required")
	}
	if current.Blocks != 3 || current.Identities != 4 {
		return failEventReport(current, errors.New("event report merge identity differs"))
	}
	if len(current.Samples) == 0 {
		return failEventReport(current, errors.New("event report block has no samples"))
	}
	if _, err := os.Stat(path); err == nil && current.Samples[0].Block >= 0 {
		var prior eventReport
		if err := readJSON(path, &prior); err != nil {
			return failEventReport(current, err)
		}
		if prior.Candidate != current.Candidate ||
			prior.FixtureSHA256 != current.FixtureSHA256 || prior.Relay != current.Relay {
			return failEventReport(
				current,
				errors.New("existing event report identity differs"),
			)
		}
		seen := map[int]bool{}
		for _, sample := range prior.Samples {
			seen[sample.Block] = true
		}
		if seen[current.Samples[0].Block] {
			return failEventReport(
				current,
				errors.New("event block was already recorded"),
			)
		}
		current.Samples = append(prior.Samples, current.Samples...)
	}
	current.Complete = len(current.Samples) == 48
	return current, nil
}

func compareReports(settings options) (*comparisonReport, error) {
	report := &comparisonReport{
		SchemaVersion: reportSchemaVersion, Kind: "comparison",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), Passed: true,
	}
	if settings.javaQuery == "" || settings.goQuery == "" || settings.javaEvent == "" ||
		settings.goEvent == "" {
		report.Passed = false
		report.Failures = append(report.Failures, "all four report paths are required")
		return report, errors.New("comparison input is incomplete")
	}
	var javaQuery, goQuery queryReport
	var javaEvent, goEvent eventReport
	inputs := []struct {
		path   string
		target any
	}{
		{settings.javaQuery, &javaQuery},
		{settings.goQuery, &goQuery},
		{settings.javaEvent, &javaEvent},
		{settings.goEvent, &goEvent},
	}
	for _, input := range inputs {
		if err := readJSON(input.path, input.target); err != nil {
			report.Failures = append(report.Failures, err.Error())
			report.Passed = false
			return report, err
		}
	}
	if err := validateReportSet(javaQuery, goQuery, javaEvent, goEvent); err != nil {
		report.Failures = append(report.Failures, err.Error())
		report.Passed = false
		return report, err
	}
	keys := queryKeys(javaQuery.Samples, "http")
	for _, key := range keys {
		javaDurations := durationsFor(javaQuery.Samples, "http", key.queryID, key.profile)
		goHTTP := durationsFor(goQuery.Samples, "http", key.queryID, key.profile)
		goGRPC := durationsFor(goQuery.Samples, "grpc", key.queryID, key.profile)
		for _, percentile := range []float64{0.50, 0.95} {
			report.addLatencyMeasure(
				"direct-query-p"+strconv.Itoa(int(percentile*100)),
				key,
				javaDurations,
				goHTTP,
				percentile,
				2,
				50,
			)
			report.addLatencyMeasure(
				"translation-p"+strconv.Itoa(int(percentile*100)),
				key,
				goGRPC,
				goHTTP,
				percentile,
				2,
				50,
			)
		}
		checkBlockVariation(report, javaQuery.Samples, "http", key, "java-direct")
		checkBlockVariation(report, goQuery.Samples, "http", key, "go-http")
		checkBlockVariation(report, goQuery.Samples, "grpc", key, "go-grpc")
		checkCandidateOrderShift(report, goQuery.Samples, "http", key)
	}
	javaEvents := eventDurations(javaEvent.Samples)
	goEvents := eventDurations(goEvent.Samples)
	for _, percentile := range []float64{0.50, 0.95} {
		key := queryKey{queryID: "event-visibility"}
		report.addLatencyMeasure(
			"event-p"+strconv.Itoa(int(percentile*100)),
			key,
			javaEvents,
			goEvents,
			percentile,
			2,
			1000,
		)
	}
	checkEventBlockVariation(report, javaEvent.Samples, "java-event")
	checkEventBlockVariation(report, goEvent.Samples, "go-event")
	checkEventOrderShift(report, goEvent.Samples)
	if maxDuration(goQuery.Samples) > 2500*time.Millisecond {
		report.Failures = append(report.Failures, "Go retained query exceeded 2.5 seconds")
		report.Passed = false
	}
	if maxEventDuration(goEvent.Samples) > 5*time.Second {
		report.Failures = append(
			report.Failures,
			"Go event visibility exceeded five seconds",
		)
		report.Passed = false
	}
	compareResources(
		report,
		javaQuery.Resources,
		goQuery.Resources,
		len(javaQuery.Samples)+len(javaEvent.Samples),
		len(goQuery.Samples)+len(goEvent.Samples),
	)
	if !report.Passed {
		return report, errors.New("qualification comparison failed")
	}
	return report, nil
}

type queryKey struct {
	queryID string
	profile int
}

func (report *comparisonReport) addLatencyMeasure(
	name string,
	key queryKey,
	baseline []int64,
	candidate []int64,
	percentile float64,
	ratioLimit float64,
	absLimitMS float64,
) {
	baselineValue := durationPercentile(baseline, percentile)
	candidateValue := durationPercentile(candidate, percentile)
	limit := math.Max(ratioLimit*baselineValue, baselineValue+absLimitMS)
	low, high := bootstrapRatio(baseline, candidate, percentile)
	expectedSamples := 50
	if key.queryID == "event-visibility" {
		expectedSamples = 48
	}
	passed := len(baseline) == expectedSamples && len(candidate) == expectedSamples &&
		candidateValue <= limit &&
		high <= safeRatio(limit, baselineValue)
	result := comparisonResult{
		Name: name, QueryID: key.queryID, Profile: key.profile,
		BaselineMS: baselineValue, CandidateMS: candidateValue,
		Ratio: safeRatio(candidateValue, baselineValue), LimitMS: limit,
		IntervalLo: low, IntervalHi: high, Passed: passed,
	}
	report.Measures = append(report.Measures, result)
	if !passed {
		report.Passed = false
		report.Failures = append(
			report.Failures,
			fmt.Sprintf(
				"%s/%s/%d exceeded limit or sample/interval requirement",
				name,
				key.queryID,
				key.profile,
			),
		)
	}
}

func validateReportSet(javaQuery, goQuery queryReport, javaEvent, goEvent eventReport) error {
	for _, input := range []struct {
		candidate string
		report    queryReport
	}{
		{candidate: "java", report: javaQuery},
		{candidate: "go", report: goQuery},
	} {
		if err := validateQueryReportGrid(input.report, input.candidate); err != nil {
			return err
		}
	}
	for _, input := range []struct {
		candidate string
		report    eventReport
	}{
		{candidate: "java", report: javaEvent},
		{candidate: "go", report: goEvent},
	} {
		if err := validateEventReportGrid(input.report, input.candidate); err != nil {
			return err
		}
	}
	if javaQuery.FixtureSHA256 == "" || javaQuery.FixtureSHA256 != goQuery.FixtureSHA256 ||
		javaQuery.FixtureSHA256 != javaEvent.FixtureSHA256 || javaQuery.FixtureSHA256 != goEvent.FixtureSHA256 {
		return errors.New("fixture identities differ")
	}
	return nil
}

func validateQueryReportGrid(report queryReport, candidate string) error {
	endpoints := []string{"http"}
	if candidate == "go" {
		endpoints = append(endpoints, "grpc")
	}
	queryIDs := []string{
		"populated",
		"omitted-fields",
		"number-only",
		"quoted-holder",
		"zero-hit",
		"size-cap",
		"stable-order",
	}
	if report.SchemaVersion != reportSchemaVersion || report.Kind != "query-report" ||
		report.Candidate != candidate || report.CandidateOrder != pairedCandidateOrder ||
		report.WarmupCycles != 2 || report.Blocks != 5 || report.Cycles != 10 ||
		!slices.Equal(report.Concurrency, []int{1, 4}) || !report.Complete ||
		report.Failure != "" {
		return fmt.Errorf("%s query report identity differs", candidate)
	}
	want := report.Blocks * report.Cycles * len(report.Concurrency)
	want *= len(endpoints) * len(queryIDs)
	if len(report.Samples) != want {
		return fmt.Errorf("%s query report sample count differs", candidate)
	}
	allowedEndpoints := make(map[string]bool, len(endpoints))
	for _, endpoint := range endpoints {
		allowedEndpoints[endpoint] = true
	}
	allowedQueries := make(map[string]bool, len(queryIDs))
	for _, queryID := range queryIDs {
		allowedQueries[queryID] = true
	}
	seen := make(map[string]bool, want)
	for _, sample := range report.Samples {
		if sample.Block < 0 || sample.Block >= report.Blocks ||
			sample.Cycle < 0 || sample.Cycle >= report.Cycles ||
			!slices.Contains(report.Concurrency, sample.Concurrency) ||
			!allowedEndpoints[sample.Endpoint] || !allowedQueries[sample.QueryID] ||
			sample.DurationNS <= 0 || sample.Status != http.StatusOK ||
			sample.Outcome != "passed" {
			return fmt.Errorf("%s query report sample identity differs", candidate)
		}
		key := fmt.Sprintf(
			"%d/%d/%d/%s/%s",
			sample.Block,
			sample.Cycle,
			sample.Concurrency,
			sample.Endpoint,
			sample.QueryID,
		)
		if seen[key] {
			return fmt.Errorf("%s query report contains a duplicate sample", candidate)
		}
		seen[key] = true
	}
	if len(seen) != want {
		return fmt.Errorf("%s query report sample grid differs", candidate)
	}
	return nil
}

func validateEventReportGrid(report eventReport, candidate string) error {
	eventTypes := []string{
		"registration",
		"registration-duplicate",
		"termination",
		"registration-reverse",
	}
	wantRelay := ""
	if candidate == "java" {
		wantRelay = "owned audit topic to pinned legacy topic on the same disposable broker"
	}
	if report.SchemaVersion != reportSchemaVersion || report.Kind != "event-report" ||
		report.Candidate != candidate || report.Blocks != 3 || report.Identities != 4 ||
		report.Relay != wantRelay || !report.Complete || report.Failure != "" {
		return fmt.Errorf("%s event report identity differs", candidate)
	}
	want := report.Blocks * report.Identities * len(eventTypes)
	if len(report.Samples) != want {
		return fmt.Errorf("%s event report sample count differs", candidate)
	}
	seen := make(map[string]bool, want)
	for _, sample := range report.Samples {
		if sample.Block < 0 || sample.Block >= report.Blocks ||
			sample.Identity < 0 || sample.Identity >= report.Identities ||
			sample.Sequence < 0 || sample.Sequence >= len(eventTypes) ||
			sample.EventType != eventTypes[sample.Sequence] || sample.DurationNS <= 0 ||
			sample.Outcome != "passed" {
			return fmt.Errorf("%s event report sample identity differs", candidate)
		}
		key := fmt.Sprintf("%d/%d/%d", sample.Block, sample.Identity, sample.Sequence)
		if seen[key] {
			return fmt.Errorf("%s event report contains a duplicate sample", candidate)
		}
		seen[key] = true
	}
	if len(seen) != want {
		return fmt.Errorf("%s event report sample grid differs", candidate)
	}
	return nil
}

func checkBlockVariation(
	report *comparisonReport,
	samples []querySample,
	endpoint string,
	key queryKey,
	label string,
) {
	medians := make([]float64, 0, 5)
	for block := range 5 {
		values := durationsForBlock(samples, endpoint, key.queryID, key.profile, block)
		if len(values) != 10 {
			report.Passed = false
			report.Failures = append(
				report.Failures,
				label+" block sample count differs",
			)
			return
		}
		medians = append(medians, durationPercentile(values, 0.50))
	}
	mean := 0.0
	for _, value := range medians {
		mean += value
	}
	mean /= float64(len(medians))
	variance := 0.0
	for _, value := range medians {
		variance += (value - mean) * (value - mean)
	}
	coefficient := math.Sqrt(variance/float64(len(medians))) / mean
	if mean <= 0 || coefficient > 0.25 {
		report.Passed = false
		report.Failures = append(
			report.Failures,
			fmt.Sprintf(
				"%s/%s/%d block-median variation exceeded 25%%",
				label,
				key.queryID,
				key.profile,
			),
		)
	}
}

func checkCandidateOrderShift(
	report *comparisonReport,
	samples []querySample,
	endpoint string,
	key queryKey,
) {
	var javaFirst, goFirst []int64
	for _, sample := range samples {
		if sample.Endpoint != endpoint || sample.QueryID != key.queryID ||
			sample.Concurrency != key.profile || sample.Outcome != "passed" {
			continue
		}
		if sample.Block%2 == 0 {
			javaFirst = append(javaFirst, sample.DurationNS)
		} else {
			goFirst = append(goFirst, sample.DurationNS)
		}
	}
	first := durationPercentile(javaFirst, 0.50)
	second := durationPercentile(goFirst, 0.50)
	if math.Abs(first-second)/first > 0.20 {
		report.Passed = false
		report.Failures = append(
			report.Failures,
			fmt.Sprintf(
				"candidate-order shift for %s/%d exceeded 20%%",
				key.queryID,
				key.profile,
			),
		)
	}
}

func checkEventBlockVariation(
	report *comparisonReport,
	samples []eventSample,
	label string,
) {
	medians := make([]float64, 0, 3)
	for block := range 3 {
		values := eventDurationsForBlock(samples, block)
		if len(values) != 16 {
			report.Passed = false
			report.Failures = append(
				report.Failures,
				label+" block sample count differs",
			)
			return
		}
		medians = append(medians, durationPercentile(values, 0.50))
	}
	mean := 0.0
	for _, value := range medians {
		mean += value
	}
	mean /= float64(len(medians))
	variance := 0.0
	for _, value := range medians {
		variance += (value - mean) * (value - mean)
	}
	if mean <= 0 || math.Sqrt(variance/float64(len(medians)))/mean > 0.25 {
		report.Passed = false
		report.Failures = append(
			report.Failures,
			label+" block-median variation exceeded 25%",
		)
	}
}

func checkEventOrderShift(report *comparisonReport, samples []eventSample) {
	var javaFirst, goFirst []int64
	for _, sample := range samples {
		if sample.Outcome != "passed" {
			continue
		}
		if sample.Block%2 == 0 {
			javaFirst = append(javaFirst, sample.DurationNS)
		} else {
			goFirst = append(goFirst, sample.DurationNS)
		}
	}
	first := durationPercentile(javaFirst, 0.50)
	second := durationPercentile(goFirst, 0.50)
	if math.Abs(first-second)/first > 0.20 {
		report.Passed = false
		report.Failures = append(
			report.Failures,
			"event candidate-order shift exceeded 20%",
		)
	}
}

func compareResources(
	report *comparisonReport,
	java, candidate resourceObservation,
	javaOperations, candidateOperations int,
) {
	if java.CPUSeconds <= 0 || candidate.CPUSeconds <= 0 || java.PeakRSSBytes <= 0 ||
		candidate.PeakRSSBytes <= 0 ||
		candidate.MeasuredWallSeconds <= 0 ||
		javaOperations <= 0 || candidateOperations <= 0 {
		report.Passed = false
		report.Failures = append(report.Failures, "resource observations are incomplete")
		return
	}
	javaCPU := java.CPUSeconds * 100 / float64(javaOperations)
	goCPU := candidate.CPUSeconds * 100 / float64(candidateOperations)
	if goCPU > math.Max(2*javaCPU, javaCPU+0.25) {
		report.Passed = false
		report.Failures = append(report.Failures, "Go normalized CPU exceeded limit")
	}
	if float64(
		candidate.PeakRSSBytes,
	) > math.Max(
		1.5*float64(java.PeakRSSBytes),
		float64(java.PeakRSSBytes)+(128<<20),
	) {
		report.Passed = false
		report.Failures = append(report.Failures, "Go peak RSS exceeded limit")
	}
	if java.MeasuredWallSeconds <= 0 ||
		java.ThrottledSeconds/java.MeasuredWallSeconds >= 0.05 {
		report.Passed = false
		report.Failures = append(
			report.Failures,
			"Java throttled time reached five percent",
		)
	}
	if candidate.ThrottledSeconds/candidate.MeasuredWallSeconds >= 0.05 {
		report.Passed = false
		report.Failures = append(report.Failures, "Go throttled time reached five percent")
	}
	if java.DependencyCPUSeconds > 0 && candidate.DependencyCPUSeconds > 0 {
		delta := math.Abs(
			candidate.DependencyCPUSeconds-java.DependencyCPUSeconds,
		) / java.DependencyCPUSeconds
		if delta > 0.20 {
			report.Passed = false
			report.Failures = append(
				report.Failures,
				"dependency CPU differed by more than twenty percent",
			)
		}
	}
}

func validateQueryNumbers(query queryCase, got []string) error {
	want := query.ExpectedNumbers
	switch query.ExpectedRule {
	case "exact":
	case "empty":
		want = nil
	case "stable-order":
		want = []string{
			"newest",
			"z-equal-first",
			"a-equal-second",
			"older",
			"null-first",
			"null-second",
		}
	case "cap-window":
		want = capWindowNumbers()
	default:
		return fmt.Errorf("query %s has unsupported expected rule", query.ID)
	}
	if !slices.Equal(got, want) {
		return fmt.Errorf("query %s numbers differ: got %v want %v", query.ID, got, want)
	}
	return nil
}

func capWindowNumbers() []string {
	type item struct {
		number string
		day    int
	}
	items := make([]item, 100)
	for index := range items {
		items[index] = item{number: fmt.Sprintf("CAP-%03d", index), day: 1 + index%28}
	}
	sort.SliceStable(
		items,
		func(left, right int) bool { return items[left].day > items[right].day },
	)
	result := make([]string, len(items))
	for index := range items {
		result[index] = items[index].number
	}
	return result
}

func policyNumbersFromHTTP(payload []byte) ([]string, error) {
	if bytes.Equal(bytes.TrimSpace(payload), []byte("{}")) {
		return nil, nil
	}
	var response struct {
		Policies []struct {
			Number string `json:"number"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(payload, &response); err != nil {
		return nil, fmt.Errorf("decode HTTP policy response: %w", err)
	}
	result := make([]string, len(response.Policies))
	for index := range response.Policies {
		result[index] = response.Policies[index].Number
	}
	return result, nil
}

func eventPayload(template json.RawMessage, id string) ([]byte, map[string]string, error) {
	var envelope struct {
		Policy map[string]any `json:"policy"`
	}
	if err := json.Unmarshal(template, &envelope); err != nil {
		return nil, nil, fmt.Errorf("decode event template: %w", err)
	}
	envelope.Policy["number"] = id
	payload, err := json.Marshal(envelope)
	if err != nil {
		return nil, nil, err
	}
	expected := map[string]string{"number": id}
	for source, target := range map[string]string{"from": "dateFrom", "to": "dateTo", "policyHolder": "policyHolder"} {
		if value, ok := envelope.Policy[source].(string); ok && value != "" {
			expected[target] = value
		}
	}
	return payload, expected, nil
}

func waitForExpectedPolicy(
	ctx context.Context,
	address, id string,
	expected map[string]string,
) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		attempt, cancel := context.WithTimeout(ctx, time.Second)
		status, payload, err := request(attempt, address, `number:"`+id+`"`)
		cancel()
		if err == nil && status == http.StatusOK &&
			responseContainsPolicy(payload, expected) {
			return nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return fmt.Errorf("event %s was not query-visible within five seconds", id)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func responseContainsPolicy(payload []byte, expected map[string]string) bool {
	var response struct {
		Policies []map[string]any `json:"policies"`
	}
	if json.Unmarshal(payload, &response) != nil || len(response.Policies) != 1 {
		return false
	}
	for key, want := range expected {
		if got, _ := response.Policies[0][key].(string); got != want {
			return false
		}
	}
	return true
}

func newKafkaClient(settings options) (*kgo.Client, error) {
	pem, err := os.ReadFile(settings.kafkaCAFile)
	if err != nil {
		return nil, fmt.Errorf("read Kafka CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, errors.New("kafka CA is not a public certificate")
	}
	return kgo.NewClient(
		kgo.SeedBrokers(settings.kafkaBrokers),
		kgo.ClientID("issue-234-qualification-client"),
		kgo.ProducerBatchCompression(kgo.NoCompression()),
		kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}),
	)
}

func validateOwnedKafka(settings options) error {
	if err := requireLoopbackAddress(settings.kafkaBrokers); err != nil {
		return err
	}
	prefixes := map[string]string{
		settings.registeredTopic: "policy-registered-issue-234-",
		settings.terminatedTopic: "policy-terminated-issue-234-",
		settings.consumerGroup:   "policy-search-service-issue-234-",
	}
	for value, prefix := range prefixes {
		if !strings.HasPrefix(value, prefix) ||
			(!strings.HasSuffix(value, "-java") && !strings.HasSuffix(value, "-go")) {
			return fmt.Errorf(
				"kafka coordinate %q is not an owned candidate resource",
				value,
			)
		}
	}
	if settings.kafkaCAFile == "" {
		return errors.New("kafka-ca-file is required")
	}
	return nil
}

func validateLegacyRelay(settings options) error {
	if settings.candidate == "java" {
		if settings.legacyRegisteredTopic != "policy-registered" ||
			settings.legacyTerminatedTopic != "policy-terminated" {
			return errors.New(
				"java event report requires the two pinned legacy relay topics",
			)
		}
		return nil
	}
	if settings.legacyRegisteredTopic != "" || settings.legacyTerminatedTopic != "" {
		return errors.New("legacy relay topics are Java-only")
	}
	return nil
}

func parseConcurrency(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	result := make([]int, 0, len(parts))
	for _, part := range parts {
		parsed, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || parsed < 1 || parsed > 16 {
			return nil, errors.New("concurrency must contain integers from 1 to 16")
		}
		result = append(result, parsed)
	}
	return result, nil
}

func resourcesFrom(settings options) resourceObservation {
	return resourceObservation{
		CPUSeconds:           settings.cpuSeconds,
		PeakRSSBytes:         settings.peakRSSBytes,
		ThrottledSeconds:     settings.throttledSeconds,
		MeasuredWallSeconds:  settings.measuredWallSeconds,
		DependencyCPUSeconds: settings.dependencyCPUSeconds,
	}
}

func failQueryReport(report *queryReport, err error) (*queryReport, error) {
	report.Failure = err.Error()
	return report, err
}

func failEventReport(report *eventReport, err error) (*eventReport, error) {
	report.Failure = err.Error()
	return report, err
}

func requireLoopbackURL(address string) error {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" {
		return errors.New("HTTP address must be an unauthenticated loopback HTTP URL")
	}
	if parsed.Path != "/policies" {
		return errors.New("HTTP address path must be /policies")
	}
	return requireLoopbackHost(parsed.Hostname())
}

func requireLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return errors.New("address must be one host:port")
	}
	return requireLoopbackHost(host)
}

func requireLoopbackHost(host string) error {
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("address must use a loopback host")
	}
	return nil
}

func queryKeys(samples []querySample, endpoint string) []queryKey {
	set := map[queryKey]struct{}{}
	for _, sample := range samples {
		if sample.Endpoint == endpoint {
			set[queryKey{sample.QueryID, sample.Concurrency}] = struct{}{}
		}
	}
	result := make([]queryKey, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].queryID == result[right].queryID {
			return result[left].profile < result[right].profile
		}
		return result[left].queryID < result[right].queryID
	})
	return result
}

func durationsFor(samples []querySample, endpoint, queryID string, profile int) []int64 {
	var result []int64
	for _, sample := range samples {
		if sample.Endpoint == endpoint && sample.QueryID == queryID &&
			sample.Concurrency == profile &&
			sample.Outcome == "passed" {
			result = append(result, sample.DurationNS)
		}
	}
	return result
}

func durationsForBlock(
	samples []querySample,
	endpoint, queryID string,
	profile, block int,
) []int64 {
	var result []int64
	for _, sample := range samples {
		if sample.Endpoint == endpoint && sample.QueryID == queryID &&
			sample.Concurrency == profile && sample.Block == block &&
			sample.Outcome == "passed" {
			result = append(result, sample.DurationNS)
		}
	}
	return result
}

func eventDurations(samples []eventSample) []int64 {
	result := make([]int64, 0, len(samples))
	for _, sample := range samples {
		if sample.Outcome == "passed" {
			result = append(result, sample.DurationNS)
		}
	}
	return result
}

func eventDurationsForBlock(samples []eventSample, block int) []int64 {
	result := make([]int64, 0, 16)
	for _, sample := range samples {
		if sample.Block == block && sample.Outcome == "passed" {
			result = append(result, sample.DurationNS)
		}
	}
	return result
}

func durationPercentile(values []int64, percentile float64) float64 {
	if len(values) == 0 {
		return math.Inf(1)
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	index := int(math.Ceil(percentile*float64(len(ordered)))) - 1
	return float64(ordered[max(0, index)]) / float64(time.Millisecond)
}

func bootstrapRatio(baseline, candidate []int64, percentile float64) (float64, float64) {
	if len(baseline) == 0 || len(candidate) == 0 {
		return math.Inf(1), math.Inf(1)
	}
	random := rand.New(rand.NewPCG(234, 1))
	ratios := make([]float64, 1000)
	for index := range ratios {
		baseSample := make([]int64, len(baseline))
		candidateSample := make([]int64, len(candidate))
		for item := range baseSample {
			baseSample[item] = baseline[random.IntN(len(baseline))]
		}
		for item := range candidateSample {
			candidateSample[item] = candidate[random.IntN(len(candidate))]
		}
		ratios[index] = safeRatio(
			durationPercentile(candidateSample, percentile),
			durationPercentile(baseSample, percentile),
		)
	}
	sort.Float64s(ratios)
	return ratios[24], ratios[974]
}

func safeRatio(candidate, baseline float64) float64 {
	if baseline <= 0 {
		return math.Inf(1)
	}
	return candidate / baseline
}

func maxDuration(samples []querySample) time.Duration {
	var result time.Duration
	for _, sample := range samples {
		result = max(result, time.Duration(sample.DurationNS))
	}
	return result
}

func maxEventDuration(samples []eventSample) time.Duration {
	var result time.Duration
	for _, sample := range samples {
		result = max(result, time.Duration(sample.DurationNS))
	}
	return result
}

func writeJSON(path string, value any) error {
	if path == "" || !filepath.IsAbs(path) {
		return errors.New("output must be an absolute owned path")
	}
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create report directory: %w", err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func readJSON(path string, target any) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read report %s: %w", filepath.Base(path), err)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return fmt.Errorf("decode report %s: %w", filepath.Base(path), err)
	}
	return nil
}

func request(ctx context.Context, address, query string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		address+"?q="+url.QueryEscape(query),
		nil,
	)
	if err != nil {
		return 0, nil, fmt.Errorf("create HTTP request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil, fmt.Errorf("send HTTP request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxHTTPResponseBytes+1))
	if err != nil {
		return 0, nil, fmt.Errorf("read HTTP response: %w", err)
	}
	if len(payload) > maxHTTPResponseBytes {
		return 0, nil, errors.New("HTTP response exceeded limit")
	}
	return response.StatusCode, payload, nil
}

func requestGRPC(ctx context.Context, address, query string) ([]string, []byte, error) {
	connection, err := grpc.NewClient(
		address,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("create gRPC client: %w", err)
	}
	defer func() { _ = connection.Close() }()
	response, err := policysearchv1.NewPolicySearchServiceClient(connection).SearchPolicies(
		ctx,
		&policysearchv1.SearchPoliciesRequest{Q: proto.String(query)},
	)
	if err != nil {
		return nil, nil, fmt.Errorf("search over gRPC: %w", err)
	}
	numbers := make([]string, len(response.GetPolicies()))
	for index, policy := range response.GetPolicies() {
		numbers[index] = policy.GetNumber()
	}
	payload, err := proto.Marshal(response)
	return numbers, payload, err
}

func checkHTTP(ctx context.Context, address, query string) error {
	statusCode, body, err := request(ctx, address, query)
	if err != nil {
		return err
	}
	numbers, err := policyNumbersFromHTTP(body)
	if statusCode != http.StatusOK || err != nil || len(numbers) == 0 {
		return fmt.Errorf("HTTP policy search differed: status=%d: %w", statusCode, err)
	}
	statusCode, _, err = request(ctx, address, "")
	if err != nil || statusCode != http.StatusOK {
		return fmt.Errorf("empty HTTP policy query returned %d: %w", statusCode, err)
	}
	return nil
}

func checkGRPC(ctx context.Context, address, query string) error {
	numbers, _, err := requestGRPC(ctx, address, query)
	if err != nil || len(numbers) == 0 {
		return fmt.Errorf("gRPC policy search differed: %w", err)
	}
	return nil
}

func checkHealth(ctx context.Context, address string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return fmt.Errorf("create health request: %w", err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("send health request: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("health returned %d", response.StatusCode)
	}
	return nil
}

func checkTCP(ctx context.Context, address string, expectDenied bool) error {
	connection, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", address)
	if expectDenied {
		if err == nil {
			_ = connection.Close()
			return errors.New("excluded TCP caller connected")
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("allowed TCP caller failed: %w", err)
	}
	return connection.Close()
}
