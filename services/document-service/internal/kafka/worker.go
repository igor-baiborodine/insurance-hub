// Package kafka consumes the legacy policy registration envelope for Document generation.
package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"go.opentelemetry.io/otel/propagation"

	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/domain"
)

const (
	pollTimeout         = 100 * time.Millisecond
	maxRecords          = 1
	maximumRecordBytes  = 64 * 1024
	assignmentStability = 250 * time.Millisecond
)

var numericIdentityPattern = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

// Settings captures the accepted Dashboard-aligned registration client behavior.
type Settings struct {
	Brokers       []string
	Group         string
	Topic         string
	ClientID      string
	CACert        []byte
	EventType     string
	Propagator    propagation.TextMapPropagator
	Observe       func(context.Context, string) (context.Context, func(context.Context, string, error))
	CommitFailure func(context.Context, string)
}

type generator interface {
	Execute(context.Context, domain.PolicyRegistration) (application.GenerationResult, error)
}

type pollResult struct {
	records []*kgo.Record
	errors  []error
}

type consumerClient interface {
	Poll(context.Context, int) pollResult
	Ping(context.Context) error
	AllowRebalance()
	CommitUncommittedOffsets(context.Context) error
	CloseAllowingRebalance()
}

type franzClient struct{ client *kgo.Client }

func (client franzClient) Poll(ctx context.Context, limit int) pollResult {
	fetches := client.client.PollRecords(ctx, limit)
	result := pollResult{records: fetches.Records()}
	for _, fetchErr := range fetches.Errors() {
		result.errors = append(result.errors, fetchErr.Err)
	}
	return result
}

func (client franzClient) Ping(ctx context.Context) error { return client.client.Ping(ctx) }
func (client franzClient) AllowRebalance()                { client.client.AllowRebalance() }
func (client franzClient) CommitUncommittedOffsets(ctx context.Context) error {
	return client.client.CommitUncommittedOffsets(ctx)
}
func (client franzClient) CloseAllowingRebalance() { client.client.CloseAllowingRebalance() }

// Worker synchronously handles one registration at a time with weak auto-commit semantics.
type Worker struct {
	client        consumerClient
	preflight     func(context.Context) (consumerClient, error)
	generator     generator
	logger        *slog.Logger
	assigned      *atomic.Bool
	failed        atomic.Bool
	eventType     string
	propagator    propagation.TextMapPropagator
	observe       func(context.Context, string) (context.Context, func(context.Context, string, error))
	commitFailure func(context.Context, string)
}

// NewWorker constructs one independently owned Document group consumer after preflight.
func NewWorker(settings Settings, generator generator, logger *slog.Logger) (*Worker, error) {
	if len(settings.Brokers) == 0 || settings.Group == "" || settings.Topic == "" ||
		settings.ClientID == "" || settings.EventType == "" || generator == nil || logger == nil {
		return nil, errors.New(
			"configure Document registration worker: incomplete settings",
		)
	}
	if len(bytes.TrimSpace(settings.CACert)) == 0 {
		return nil, errors.New(
			"configure Document registration worker: empty CA certificate",
		)
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("configure Document registration worker: load system roots")
	}
	if !roots.AppendCertsFromPEM(settings.CACert) {
		return nil, errors.New(
			"configure Document registration worker: invalid CA certificate",
		)
	}
	tlsSettings := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
	baseOptions := []kgo.Opt{
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.DialTLSConfig(tlsSettings),
	}
	assigned := &atomic.Bool{}
	consumerOptions := []kgo.Opt{
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.ConsumerGroup(settings.Group),
		kgo.ConsumeTopics(settings.Topic),
		kgo.AutoCommitInterval(5 * time.Second),
		kgo.SessionTimeout(10 * time.Second),
		kgo.HeartbeatInterval(3 * time.Second),
		kgo.RebalanceTimeout(5 * time.Minute),
		kgo.Balancers(kgo.RangeBalancer()),
		kgo.FetchMaxWait(500 * time.Millisecond),
		kgo.BlockRebalanceOnPoll(),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		kgo.DialTLSConfig(tlsSettings),
		kgo.OnPartitionsAssigned(func(context.Context, *kgo.Client, map[string][]int32) {
			assigned.Store(true)
		}),
		kgo.OnPartitionsRevoked(func(context.Context, *kgo.Client, map[string][]int32) {
			assigned.Store(false)
		}),
		kgo.OnPartitionsLost(func(context.Context, *kgo.Client, map[string][]int32) {
			assigned.Store(false)
		}),
		kgo.AutoCommitCallback(func(
			_ *kgo.Client,
			_ *kmsg.OffsetCommitRequest,
			_ *kmsg.OffsetCommitResponse,
			err error,
		) {
			if err == nil {
				return
			}
			if settings.CommitFailure != nil {
				settings.CommitFailure(context.Background(), settings.EventType)
				return
			}
			logger.Error("policy event offset commit failed")
		}),
	}
	worker := &Worker{
		generator: generator, logger: logger, assigned: assigned,
		eventType: settings.EventType, propagator: settings.Propagator,
		observe: settings.Observe, commitFailure: settings.CommitFailure,
	}
	worker.preflight = func(ctx context.Context) (consumerClient, error) {
		inspector, err := kgo.NewClient(baseOptions...)
		if err != nil {
			return nil, errors.New(
				"preflight Document registration worker: create Kafka inspector",
			)
		}
		if err := verifyCommittedProgress(
			ctx,
			inspector,
			settings.Topic,
			settings.Group,
		); err != nil {
			inspector.Close()
			return nil, err
		}
		inspector.Close()
		consumer, err := kgo.NewClient(consumerOptions...)
		if err != nil {
			return nil, errors.New(
				"preflight Document registration worker: create Kafka consumer",
			)
		}
		return franzClient{client: consumer}, nil
	}
	return worker, nil
}

// Preflight proves stable committed progress before acquiring the group consumer.
func (worker *Worker) Preflight(ctx context.Context) error {
	if worker == nil {
		return errors.New("preflight Document registration worker: worker is required")
	}
	if worker.preflight == nil {
		return nil
	}
	client, err := worker.preflight(ctx)
	if err != nil {
		return err
	}
	if client == nil {
		return errors.New("preflight Document registration worker: consumer is required")
	}
	worker.client = client
	worker.preflight = nil
	return nil
}

func newWorker(client consumerClient, generator generator, logger *slog.Logger) *Worker {
	assigned := &atomic.Bool{}
	assigned.Store(true)
	return &Worker{
		client: client, generator: generator, logger: logger, assigned: assigned,
		eventType: "registered",
	}
}

// Check verifies broker reachability, handler state, and stable group assignment.
func (worker *Worker) Check(ctx context.Context) error {
	if worker == nil || worker.client == nil {
		return errors.New("check Document registration worker: preflight is required")
	}
	if worker.failed.Load() {
		return errors.New(
			"check Document registration worker: generation dependency failed",
		)
	}
	if err := worker.client.Ping(ctx); err != nil {
		worker.logger.ErrorContext(
			ctx,
			"policy event worker readiness failed",
			slog.String("event_type", worker.eventType),
			slog.String("reason", "kafka_unavailable"),
		)
		return errors.New("check Document registration worker: Kafka unavailable")
	}
	stableSince := time.Time{}
	for {
		if worker.failed.Load() {
			return errors.New(
				"check Document registration worker: generation dependency failed",
			)
		}
		if worker.assigned.Load() {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= assignmentStability {
				return nil
			}
		} else {
			stableSince = time.Time{}
		}
		select {
		case <-ctx.Done():
			worker.logger.ErrorContext(
				context.Background(),
				"policy event worker readiness failed",
				slog.String("event_type", worker.eventType),
				slog.String("reason", "group_assignment_unavailable"),
			)
			return errors.New(
				"check Document registration worker: group assignment unavailable",
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Run polls and processes one record until cancellation. Every decoded or rejected record is a
// terminal consumed outcome under the selected weak auto-commit behavior.
func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil || worker.client == nil {
		return errors.New("run Document registration worker: preflight is required")
	}
	for ctx.Err() == nil {
		pollCtx, cancel := context.WithTimeout(ctx, pollTimeout)
		result := worker.client.Poll(pollCtx, maxRecords)
		cancel()
		if ctx.Err() != nil {
			worker.client.AllowRebalance()
			return nil
		}
		for _, fetchErr := range result.errors {
			if !errors.Is(fetchErr, context.DeadlineExceeded) &&
				!errors.Is(fetchErr, context.Canceled) {
				worker.logger.ErrorContext(ctx, "policy event fetch failed")
			}
		}
		for _, record := range result.records {
			recordCtx := ctx
			if worker.propagator != nil {
				recordCtx = worker.propagator.Extract(
					ctx,
					kafkaHeaderCarrier(record.Headers),
				)
			}
			finish := func(context.Context, string, error) {}
			if worker.observe != nil {
				recordCtx, finish = worker.observe(recordCtx, worker.eventType)
			}
			registration, err := decode(record.Key, record.Value)
			if err != nil {
				finish(recordCtx, "malformed", err)
				worker.logger.WarnContext(
					recordCtx,
					"invalid policy registration consumed",
				)
				continue
			}
			_, err = worker.generator.Execute(recordCtx, registration)
			if err != nil {
				outcome := "error"
				if errors.Is(err, context.Canceled) ||
					errors.Is(err, context.DeadlineExceeded) {
					outcome = "cancelled"
				} else {
					worker.failed.Store(true)
				}
				finish(recordCtx, outcome, err)
				worker.logger.ErrorContext(
					recordCtx,
					"policy document generation failed",
				)
				continue
			}
			worker.failed.Store(false)
			finish(recordCtx, "success", nil)
		}
		worker.client.AllowRebalance()
	}
	return nil
}

type kafkaHeaderCarrier []kgo.RecordHeader

func (carrier kafkaHeaderCarrier) Get(key string) string {
	for _, header := range carrier {
		if header.Key == key {
			return string(header.Value)
		}
	}
	return ""
}

func (carrier kafkaHeaderCarrier) Set(key, value string) {
	for index := range carrier {
		if carrier[index].Key == key {
			carrier[index].Value = []byte(value)
			return
		}
	}
}

func (carrier kafkaHeaderCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier))
	for _, header := range carrier {
		keys = append(keys, header.Key)
	}
	return keys
}

// Shutdown commits the current consumed position, releases rebalances, and closes the client.
func (worker *Worker) Shutdown(ctx context.Context) error {
	if worker == nil || worker.client == nil {
		return nil
	}
	commitErr := worker.client.CommitUncommittedOffsets(ctx)
	if commitErr != nil && worker.commitFailure != nil {
		worker.commitFailure(context.Background(), worker.eventType)
	}
	worker.client.AllowRebalance()
	closed := make(chan struct{})
	go func() {
		worker.client.CloseAllowingRebalance()
		close(closed)
	}()
	select {
	case <-closed:
	case <-ctx.Done():
		return errors.Join(commitErr, ctx.Err())
	}
	return commitErr
}

type envelope struct {
	Policy *policy `json:"policy"`
}

type policy struct {
	Number       json.RawMessage `json:"number"`
	From         json.RawMessage `json:"from"`
	To           json.RawMessage `json:"to"`
	PolicyHolder *string         `json:"policyHolder"`
	ProductCode  *string         `json:"productCode"`
	TotalPremium json.RawMessage `json:"totalPremium"`
	AgentLogin   *string         `json:"agentLogin"`
}

func decode(key, value []byte) (domain.PolicyRegistration, error) {
	if len(key) == 0 || len(key)+len(value) > maximumRecordBytes {
		return domain.PolicyRegistration{}, errors.New(
			"decode policy registration: invalid size",
		)
	}
	var input envelope
	if err := json.Unmarshal(value, &input); err != nil || input.Policy == nil {
		return domain.PolicyRegistration{}, errors.New(
			"decode policy registration: invalid envelope",
		)
	}
	number, err := parseIdentity(input.Policy.Number)
	if err != nil || string(key) != number {
		return domain.PolicyRegistration{}, errors.New(
			"decode policy registration: mismatched identity",
		)
	}
	from, err := parseDate(input.Policy.From)
	if err != nil {
		return domain.PolicyRegistration{}, err
	}
	to, err := parseDate(input.Policy.To)
	if err != nil {
		return domain.PolicyRegistration{}, err
	}
	if input.Policy.PolicyHolder == nil || input.Policy.ProductCode == nil {
		return domain.PolicyRegistration{}, errors.New(
			"decode policy registration: missing required text",
		)
	}
	premium, err := parsePremium(input.Policy.TotalPremium)
	if err != nil {
		return domain.PolicyRegistration{}, err
	}
	registration, err := domain.NewPolicyRegistration(domain.PolicyRegistrationInput{
		Number: number, From: from, To: to,
		PolicyHolder: *input.Policy.PolicyHolder, ProductCode: *input.Policy.ProductCode,
		TotalPremium: premium, AgentLogin: input.Policy.AgentLogin,
	})
	if err != nil {
		return domain.PolicyRegistration{}, errors.New(
			"decode policy registration: invalid policy values",
		)
	}
	return registration, nil
}

func parseIdentity(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", errors.New("decode policy registration: missing policy number")
	}
	if trimmed[0] == '"' {
		var identity string
		if err := json.Unmarshal(trimmed, &identity); err != nil || identity == "" {
			return "", errors.New("decode policy registration: invalid policy number")
		}
		return identity, nil
	}
	if !numericIdentityPattern.Match(trimmed) {
		return "", errors.New("decode policy registration: invalid policy number")
	}
	return string(trimmed), nil
}

func parseDate(value json.RawMessage) ([3]int, error) {
	var parts []int
	if err := json.Unmarshal(value, &parts); err != nil || len(parts) != 3 {
		return [3]int{}, errors.New("decode policy registration: invalid date")
	}
	return [3]int{parts[0], parts[1], parts[2]}, nil
}

func parsePremium(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", errors.New("decode policy registration: missing total premium")
	}
	if trimmed[0] == '"' {
		var token string
		if err := json.Unmarshal(trimmed, &token); err != nil || token == "" {
			return "", errors.New("decode policy registration: invalid total premium")
		}
		return token, nil
	}
	var token json.Number
	if err := json.Unmarshal(trimmed, &token); err != nil || token.String() == "" {
		return "", errors.New("decode policy registration: invalid total premium")
	}
	return token.String(), nil
}
