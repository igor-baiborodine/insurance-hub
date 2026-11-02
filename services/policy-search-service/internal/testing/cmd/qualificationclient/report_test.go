package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildQueryReport_RecordsExactWorkload(t *testing.T) {
	// given
	fixturePath := filepath.Join(
		"..",
		"..",
		"..",
		"..",
		"testdata",
		"qualification",
		"workload.json",
	)
	fixture, _, err := loadFixture(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	queries := make(map[string]queryCase, len(fixture.Queries))
	for _, query := range fixture.Queries {
		queries[query.Query] = query
	}
	server := httptest.NewServer(
		http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			query, ok := queries[request.URL.Query().Get("q")]
			if !ok {
				http.Error(response, "unknown query", http.StatusBadRequest)
				return
			}
			numbers := query.ExpectedNumbers
			switch query.ExpectedRule {
			case "empty":
				_, _ = response.Write([]byte("{}"))
				return
			case "stable-order":
				numbers = []string{
					"newest",
					"z-equal-first",
					"a-equal-second",
					"older",
					"null-first",
					"null-second",
				}
			case "cap-window":
				numbers = capWindowNumbers()
			}
			policies := make([]map[string]string, len(numbers))
			for index := range numbers {
				policies[index] = map[string]string{"number": numbers[index]}
			}
			_ = json.NewEncoder(response).Encode(map[string]any{"policies": policies})
		}),
	)
	t.Cleanup(server.Close)
	settings := options{
		candidate: "java", httpAddress: server.URL + "/policies", fixture: fixturePath,
		warmupCycles: 2, blocks: 5, cycles: 10, concurrency: "1,4",
		candidateOrder: pairedCandidateOrder, blockIndex: -1,
	}

	// when
	report, err := buildQueryReport(t.Context(), settings)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if !report.Complete || len(report.Samples) != 700 {
		t.Fatalf(
			"complete=%v samples=%d, want true/700",
			report.Complete,
			len(report.Samples),
		)
	}
	for _, sample := range report.Samples {
		if sample.Outcome != "passed" || sample.Status != http.StatusOK {
			t.Fatalf("sample outcome/status = %s/%d", sample.Outcome, sample.Status)
		}
	}
}

func TestCompareReports_DetectsDelayedCandidate(t *testing.T) {
	// given
	directory := t.TempDir()
	paths := comparisonPaths{
		javaQuery: filepath.Join(directory, "java-query.json"),
		goQuery:   filepath.Join(directory, "go-query.json"),
		javaEvent: filepath.Join(directory, "java-event.json"),
		goEvent:   filepath.Join(directory, "go-event.json"),
		output:    filepath.Join(directory, "comparison.json"),
	}
	writeComparisonInputs(t, paths, time.Millisecond, 2*time.Millisecond)
	settings := paths.settings()

	// when
	report, err := compareReports(settings)

	// then
	if err != nil || !report.Passed {
		t.Fatalf("comparison err=%v report=%+v", err, report)
	}
	var goReport queryReport
	readTestJSON(t, paths.goQuery, &goReport)
	goReport.Samples[0].DurationNS = (3 * time.Second).Nanoseconds()
	writeTestJSON(t, paths.goQuery, goReport)
	report, err = compareReports(settings)
	if err == nil || report.Passed ||
		!slices.Contains(report.Failures, "Go retained query exceeded 2.5 seconds") {
		t.Fatalf("degraded comparison err=%v report=%+v", err, report)
	}
}

func TestOwnedCoordinates_RejectSharedTargets(t *testing.T) {
	// given
	settings := options{
		kafkaBrokers: "127.0.0.1:9093", registeredTopic: "policy-registered",
		terminatedTopic: "policy-terminated", consumerGroup: "policy-search-service",
		kafkaCAFile: "ca.crt",
	}

	// when
	err := validateOwnedKafka(settings)

	// then
	if err == nil {
		t.Fatal("shared Kafka coordinates were accepted")
	}
	if err := requireLoopbackURL("https://qa.example/policies"); err == nil {
		t.Fatal("non-loopback qualification URL was accepted")
	}
	if err := validateLegacyRelay(options{candidate: "java"}); err == nil {
		t.Fatal("Java report without pinned relay topics was accepted")
	}
	if err := validateLegacyRelay(options{
		candidate: "java", legacyRegisteredTopic: "policy-registered",
		legacyTerminatedTopic: "policy-terminated",
	}); err != nil {
		t.Fatalf("pinned Java relay topics were rejected: %v", err)
	}
	if err := validateLegacyRelay(options{
		candidate: "go", legacyRegisteredTopic: "policy-registered",
	}); err == nil {
		t.Fatal("Go report with a legacy relay topic was accepted")
	}
}

type comparisonPaths struct {
	javaQuery string
	goQuery   string
	javaEvent string
	goEvent   string
	output    string
}

func (paths comparisonPaths) settings() options {
	return options{
		javaQuery: paths.javaQuery, goQuery: paths.goQuery,
		javaEvent: paths.javaEvent, goEvent: paths.goEvent, output: paths.output,
	}
}

func writeComparisonInputs(t *testing.T, paths comparisonPaths, baseline, candidate time.Duration) {
	t.Helper()
	resources := resourceObservation{
		CPUSeconds: 1, PeakRSSBytes: 64 << 20, MeasuredWallSeconds: 10,
		DependencyCPUSeconds: 1,
	}
	javaQuery := queryReport{
		SchemaVersion: 1, Kind: "query-report", Candidate: "java", FixtureSHA256: "fixture",
		Complete: true, Resources: resources, CandidateOrder: pairedCandidateOrder,
	}
	goQuery := queryReport{
		SchemaVersion: 1, Kind: "query-report", Candidate: "go", FixtureSHA256: "fixture",
		Complete: true, Resources: resources, CandidateOrder: pairedCandidateOrder,
	}
	for _, profile := range []int{1, 4} {
		for _, queryID := range []string{"a", "b", "c", "d", "e", "f", "g"} {
			for sample := range 50 {
				javaQuery.Samples = append(javaQuery.Samples, querySample{
					Block:       sample / 10,
					Cycle:       sample % 10,
					Concurrency: profile,
					Endpoint:    "http",
					QueryID:     queryID,
					DurationNS:  baseline.Nanoseconds(),
					Status:      200,
					Outcome:     "passed",
				})
				goQuery.Samples = append(goQuery.Samples,
					querySample{
						Block:       sample / 10,
						Cycle:       sample % 10,
						Concurrency: profile,
						Endpoint:    "http",
						QueryID:     queryID,
						DurationNS:  candidate.Nanoseconds(),
						Status:      200,
						Outcome:     "passed",
					},
					querySample{
						Block:       sample / 10,
						Cycle:       sample % 10,
						Concurrency: profile,
						Endpoint:    "grpc",
						QueryID:     queryID,
						DurationNS:  baseline.Nanoseconds(),
						Status:      200,
						Outcome:     "passed",
					},
				)
			}
		}
	}
	javaEvent := eventReport{
		SchemaVersion: 1, Kind: "event-report", Candidate: "java", FixtureSHA256: "fixture",
		Complete: true, Resources: resources,
	}
	goEvent := eventReport{
		SchemaVersion: 1, Kind: "event-report", Candidate: "go", FixtureSHA256: "fixture",
		Complete: true, Resources: resources,
	}
	for sample := range 48 {
		javaEvent.Samples = append(
			javaEvent.Samples,
			eventSample{
				Block:      sample / 16,
				DurationNS: baseline.Nanoseconds(),
				Outcome:    "passed",
			},
		)
		goEvent.Samples = append(
			goEvent.Samples,
			eventSample{
				Block:      sample / 16,
				DurationNS: candidate.Nanoseconds(),
				Outcome:    "passed",
			},
		)
	}
	writeTestJSON(t, paths.javaQuery, javaQuery)
	writeTestJSON(t, paths.goQuery, goQuery)
	writeTestJSON(t, paths.javaEvent, javaEvent)
	writeTestJSON(t, paths.goEvent, goEvent)
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, value); err != nil {
		t.Fatal(err)
	}
}

func TestFixturePath_IsRepositoryOwned(t *testing.T) {
	// This protects the relative path used by the focused test from accidental traversal changes.
	path, err := url.JoinPath("testdata", "qualification", "workload.json")
	if err != nil || path != "testdata/qualification/workload.json" {
		t.Fatalf("fixture path = %q, err=%v", path, err)
	}
}

func TestCorpusFixture_DefinesExactIndependentDocuments(t *testing.T) {
	// given
	path := filepath.Join(
		"..", "..", "..", "..", "testdata", "qualification", "corpus.ndjson",
	)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// when
	digest := sha256.Sum256(payload)
	lines := strings.Split(strings.TrimSpace(string(payload)), "\n")

	// then
	if got := fmt.Sprintf("%x", digest); got !=
		"34a5fe432ea4a1bf79bd9b7abae71a0c37f632e3118f93f94cc634ac4710bb65" {
		t.Fatalf("corpus SHA-256 = %s", got)
	}
	if len(lines) != 240 {
		t.Fatalf("corpus lines = %d, want 240", len(lines))
	}
	for index := 0; index < len(lines); index += 2 {
		var action struct {
			Index struct {
				ID string `json:"_id"`
			} `json:"index"`
		}
		var document map[string]any
		if json.Unmarshal([]byte(lines[index]), &action) != nil ||
			json.Unmarshal([]byte(lines[index+1]), &document) != nil ||
			action.Index.ID == "" || len(document) == 0 {
			t.Fatalf("invalid corpus pair at line %d", index+1)
		}
	}
}

func TestMergeQueryReport_AppendsFiveDistinctBlocks(t *testing.T) {
	// given
	path := filepath.Join(t.TempDir(), "query.json")
	var merged *queryReport
	for block := range 5 {
		current := &queryReport{
			SchemaVersion: 1, Kind: "query-report", Candidate: "java",
			CandidateOrder: pairedCandidateOrder, FixtureSHA256: "fixture",
			Blocks: 5, Cycles: 10,
		}
		for range 140 {
			current.Samples = append(current.Samples, querySample{Block: block})
		}
		var err error
		merged, err = mergeQueryReport(path, current)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(path, merged); err != nil {
			t.Fatal(err)
		}
	}

	// then
	if !merged.Complete || len(merged.Samples) != 700 {
		t.Fatalf(
			"complete=%v samples=%d, want true/700",
			merged.Complete,
			len(merged.Samples),
		)
	}
	duplicate := &queryReport{
		SchemaVersion: 1, Kind: "query-report", Candidate: "java",
		CandidateOrder: pairedCandidateOrder, FixtureSHA256: "fixture",
		Blocks: 5, Cycles: 10, Samples: []querySample{{Block: 4}},
	}
	if _, err := mergeQueryReport(path, duplicate); err == nil {
		t.Fatal("duplicate query block was accepted")
	}
}
