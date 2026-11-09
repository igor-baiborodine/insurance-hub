// Package kafka consumes the legacy policy event envelopes.
package kafka

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/kmsg"
	"go.opentelemetry.io/otel/propagation"

	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
)

const (
	pollTimeout         = 100 * time.Millisecond
	maxRecords          = 500
	assignmentStability = 250 * time.Millisecond
)

var (
	errMissingPolicy   = errors.New("project policy event: missing policy")
	errMissingIdentity = errors.New("project policy event: missing policy number")
)

// Settings captures the explicit Java-compatible registration client behavior.
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

type projection interface {
	ProjectPolicy(context.Context, application.PolicyEvent) error
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

// Worker synchronously projects one topic while preserving the selected weak auto-commit model.
type Worker struct {
	client        consumerClient
	projection    projection
	logger        *slog.Logger
	assigned      *atomic.Bool
	eventType     string
	propagator    propagation.TextMapPropagator
	observe       func(context.Context, string) (context.Context, func(context.Context, string, error))
	commitFailure func(context.Context, string)
}

// NewWorker creates one independently owned topic consumer.
func NewWorker(settings Settings, projection projection, logger *slog.Logger) (*Worker, error) {
	if len(settings.Brokers) == 0 || settings.Group == "" || settings.Topic == "" ||
		settings.ClientID == "" || settings.EventType == "" || projection == nil || logger == nil {
		return nil, errors.New(
			"configure dashboard registration worker: incomplete settings",
		)
	}
	if len(bytes.TrimSpace(settings.CACert)) == 0 {
		return nil, errors.New(
			"configure dashboard registration worker: empty CA certificate",
		)
	}
	options := []kgo.Opt{
		kgo.SeedBrokers(settings.Brokers...),
		kgo.ClientID(settings.ClientID),
		kgo.ConsumerGroup(settings.Group),
		kgo.ConsumeTopics(settings.Topic),
		kgo.ConsumeResetOffset(kgo.NoResetOffset()),
		kgo.AutoCommitInterval(5 * time.Second),
		kgo.SessionTimeout(10 * time.Second),
		kgo.HeartbeatInterval(3 * time.Second),
		kgo.RebalanceTimeout(5 * time.Minute),
		kgo.Balancers(kgo.RangeBalancer()),
		kgo.FetchMaxWait(500 * time.Millisecond),
		kgo.BlockRebalanceOnPoll(),
		kgo.AutoCommitCallback(func(
			_ *kgo.Client,
			_ *kmsg.OffsetCommitRequest,
			_ *kmsg.OffsetCommitResponse,
			err error,
		) {
			if err != nil {
				if settings.CommitFailure != nil {
					settings.CommitFailure(
						context.Background(),
						settings.EventType,
					)
				} else {
					logger.Error("policy event offset commit failed")
				}
			}
		}),
	}
	assigned := &atomic.Bool{}
	options = append(options,
		kgo.OnPartitionsAssigned(func(context.Context, *kgo.Client, map[string][]int32) {
			assigned.Store(true)
		}),
		kgo.OnPartitionsRevoked(func(context.Context, *kgo.Client, map[string][]int32) {
			assigned.Store(false)
		}),
		kgo.OnPartitionsLost(func(context.Context, *kgo.Client, map[string][]int32) {
			assigned.Store(false)
		}),
	)
	roots, err := x509.SystemCertPool()
	if err != nil {
		return nil, errors.New("configure dashboard registration worker: load system roots")
	}
	if !roots.AppendCertsFromPEM(settings.CACert) {
		return nil, errors.New(
			"configure dashboard registration worker: invalid CA certificate",
		)
	}
	options = append(options, kgo.DialTLSConfig(&tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
	}))
	consumer, err := kgo.NewClient(options...)
	if err != nil {
		return nil, errors.New(
			"configure dashboard registration worker: create Kafka client",
		)
	}
	return &Worker{
		client: franzClient{client: consumer}, projection: projection,
		logger: logger, assigned: assigned,
		eventType: settings.EventType, propagator: settings.Propagator,
		observe: settings.Observe, commitFailure: settings.CommitFailure,
	}, nil
}

func newWorker(client consumerClient, projection projection, logger *slog.Logger) *Worker {
	assigned := &atomic.Bool{}
	assigned.Store(true)
	return &Worker{
		client: client, projection: projection, logger: logger, assigned: assigned,
		eventType: "registered",
	}
}

// Check verifies broker reachability and waits for a stable group assignment.
func (worker *Worker) Check(ctx context.Context) error {
	if err := worker.client.Ping(ctx); err != nil {
		worker.logger.ErrorContext(
			ctx,
			"policy event worker readiness failed",
			slog.String("event_type", worker.eventType),
			slog.String("reason", "kafka_unavailable"),
		)
		return errors.New("check dashboard registration worker: Kafka unavailable")
	}
	stableSince := time.Time{}
	for {
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
				"check dashboard registration worker: group assignment unavailable",
			)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// Run polls and processes records until cancellation. Malformed envelopes are skipped, while an
// envelope without a policy follows the legacy handler-failure path and stops the fetched batch.
func (worker *Worker) Run(ctx context.Context) error {
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
			event, err := decode(record.Value)
			if errors.Is(err, errMissingPolicy) || errors.Is(err, errMissingIdentity) {
				finish(recordCtx, "error", err)
				worker.logger.ErrorContext(
					recordCtx,
					"policy event projection failed",
				)
				break
			}
			if err != nil {
				finish(recordCtx, "malformed", err)
				worker.logger.WarnContext(
					recordCtx,
					"malformed policy event skipped",
				)
				continue
			}
			if err := worker.projection.ProjectPolicy(recordCtx, event); err != nil {
				outcome := "error"
				if errors.Is(err, context.Canceled) ||
					errors.Is(err, context.DeadlineExceeded) {
					outcome = "cancelled"
				}
				finish(recordCtx, outcome, err)
				worker.logger.ErrorContext(
					recordCtx,
					"policy event projection failed",
				)
				break
			}
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

// Shutdown commits the current consumed position, releases rebalances and closes the client.
func (worker *Worker) Shutdown(ctx context.Context) error {
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

func decode(value []byte) (application.PolicyEvent, error) {
	var input envelope
	if err := json.Unmarshal(value, &input); err != nil {
		return application.PolicyEvent{}, errors.New(
			"decode policy event: invalid envelope",
		)
	}
	if input.Policy == nil {
		return application.PolicyEvent{}, errMissingPolicy
	}
	number, err := parseIdentity(input.Policy.Number)
	if err != nil {
		return application.PolicyEvent{}, err
	}
	from, err := parseDate(input.Policy.From)
	if err != nil {
		return application.PolicyEvent{}, err
	}
	to, err := parseDate(input.Policy.To)
	if err != nil {
		return application.PolicyEvent{}, err
	}
	premium, err := parsePremium(input.Policy.TotalPremium)
	if err != nil {
		return application.PolicyEvent{}, err
	}
	return application.PolicyEvent{
		Number: number, From: from, To: to, PolicyHolder: input.Policy.PolicyHolder,
		ProductCode: input.Policy.ProductCode, TotalPremium: premium,
		AgentLogin: input.Policy.AgentLogin,
	}, nil
}

func parseIdentity(value json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", errMissingIdentity
	}
	if trimmed[0] == '"' {
		var identity string
		if err := json.Unmarshal(trimmed, &identity); err != nil {
			return "", errors.New("decode policy event: invalid policy number")
		}
		if identity == "" {
			return "", errMissingIdentity
		}
		return identity, nil
	}
	var number json.Number
	if err := json.Unmarshal(trimmed, &number); err != nil || number.String() == "" {
		return "", errors.New("decode policy event: invalid policy number")
	}
	return number.String(), nil
}

func parseDate(value json.RawMessage) (*domain.Date, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	var components []int
	if err := json.Unmarshal(trimmed, &components); err != nil || len(components) != 3 {
		return nil, errors.New("decode policy event: invalid date")
	}
	if components[0] < 1 || components[0] > 9999 {
		return nil, errors.New("decode policy event: invalid date")
	}
	date, err := domain.ParseDate(
		fmt.Sprintf("%04d-%02d-%02d", components[0], components[1], components[2]),
	)
	if err != nil {
		return nil, errors.New("decode policy event: invalid date")
	}
	return &date, nil
}

func parsePremium(value json.RawMessage) (*domain.Premium, error) {
	trimmed := bytes.TrimSpace(value)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return nil, nil
	}
	token := string(trimmed)
	if strings.HasPrefix(token, `"`) {
		if err := json.Unmarshal(trimmed, &token); err != nil {
			return nil, errors.New("decode policy event: invalid premium")
		}
	}
	premium, err := domain.ParsePremium(token)
	if err != nil {
		return nil, errors.New("decode policy event: invalid premium")
	}
	return &premium, nil
}
