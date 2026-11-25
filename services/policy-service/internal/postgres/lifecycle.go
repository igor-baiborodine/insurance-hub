package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/propagation"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

// errIncompleteRecord reports a stored row without a value the lifecycle requires.
var errIncompleteRecord = errors.New("stored record lacks a required value")

// WithinTransaction runs a policy command's reads and writes in one transaction.
func (s *Store) WithinTransaction(
	ctx context.Context,
	work func(context.Context, application.PolicyWrites) error,
) error {
	return s.InTransaction(ctx, func(ctx context.Context, tx Transaction) error {
		return work(ctx, tx)
	})
}

// LoadPolicy reads the complete policy from one consistent snapshot.
func (s *Store) LoadPolicy(ctx context.Context, number string) (domain.Policy, error) {
	stored, err := s.Policy(ctx, number)
	if errors.Is(err, ErrNotFound) {
		return domain.Policy{}, application.ErrPolicyNotFound
	}
	if err != nil {
		return domain.Policy{}, err
	}
	return domainPolicy(stored)
}

// LoadOffer reads the stored offer inside the command's transaction.
func (t Transaction) LoadOffer(ctx context.Context, number string) (domain.Offer, error) {
	stored, err := readOffer(ctx, t.queries, number)
	if errors.Is(err, ErrNotFound) {
		return domain.Offer{}, application.ErrOfferNotFound
	}
	if err != nil {
		return domain.Offer{}, err
	}
	return domainOffer(stored)
}

// AddPolicy writes a newly issued policy, its only version and its registration intent.
func (t Transaction) AddPolicy(
	ctx context.Context,
	policy domain.Policy,
	event domain.PolicyEvent,
) error {
	if len(policy.Versions) != 1 {
		return errors.New("add policy: a new policy has exactly one version")
	}
	intent, err := outboxIntent(ctx, event)
	if err != nil {
		return err
	}
	var agentLogin *string
	if policy.AgentLogin != "" {
		agentLogin = &policy.AgentLogin
	}
	_, err = t.Issue(ctx, Issuance{
		Number:     policy.Number,
		AgentLogin: agentLogin,
		Version:    storedVersion(policy.Versions[0]),
		Intent:     intent,
	})
	return err
}

// LoadPolicy reads the complete policy inside the command's transaction.
func (t Transaction) LoadPolicy(ctx context.Context, number string) (domain.Policy, error) {
	stored, err := readPolicy(ctx, t.queries, number)
	if errors.Is(err, ErrNotFound) {
		return domain.Policy{}, application.ErrPolicyNotFound
	}
	if err != nil {
		return domain.Policy{}, err
	}
	return domainPolicy(stored)
}

// AppendVersion writes a further version with its covers and the event intent it causes.
func (t Transaction) AppendVersion(
	ctx context.Context,
	number string,
	version domain.PolicyVersion,
	event domain.PolicyEvent,
) error {
	intent, err := outboxIntent(ctx, event)
	if err != nil {
		return err
	}
	row, err := t.queries.GetPolicy(ctx, text(number))
	if err != nil {
		return readError("policy", err)
	}
	if err := t.insertVersion(ctx, row.ID, storedVersion(version)); err != nil {
		return err
	}
	return t.insertIntent(ctx, row.ID, intent)
}

func storedVersion(version domain.PolicyVersion) Version {
	covers := make([]Cover, 0, len(version.Covers))
	for _, cover := range version.Covers {
		covers = append(covers, Cover{Code: &cover.Code, Price: &cover.Price})
	}
	return Version{
		Number:        &version.Number,
		ProductCode:   &version.ProductCode,
		FirstName:     &version.Holder.FirstName,
		LastName:      &version.Holder.LastName,
		Pesel:         &version.Holder.TaxID,
		AccountNumber: &version.AccountNumber,
		CoverFrom:     &version.CoverFrom,
		CoverTo:       &version.CoverTo,
		VersionFrom:   &version.VersionFrom,
		VersionTo:     &version.VersionTo,
		TotalPremium:  &version.TotalPremium,
		Covers:        covers,
	}
}

// eventEnvelope is the Kafka record value: a "policy" object with date arrays and a numeric
// premium. The event carries no agent, so agentLogin is omitted.
type eventEnvelope struct {
	Policy eventPolicy `json:"policy"`
}

type eventPolicy struct {
	Number       string      `json:"number"`
	From         [3]int      `json:"from"`
	To           [3]int      `json:"to"`
	PolicyHolder string      `json:"policyHolder"`
	ProductCode  string      `json:"productCode"`
	TotalPremium json.Number `json:"totalPremium"`
}

// outboxIntent encodes the event as its keyed record; the policy number is the key.
func outboxIntent(ctx context.Context, event domain.PolicyEvent) (Intent, error) {
	payload, err := json.Marshal(eventEnvelope{Policy: eventPolicy{
		Number:       event.PolicyNumber,
		From:         dateArray(event.From),
		To:           dateArray(event.To),
		PolicyHolder: event.PolicyHolder,
		ProductCode:  event.ProductCode,
		TotalPremium: json.Number(event.TotalPremium),
	}})
	if err != nil {
		return Intent{}, fmt.Errorf("encode %s event: %w", event.Topic, err)
	}
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	traceState := carrier.Get("tracestate")
	if len(traceState) > maxTraceStateLength {
		traceState = ""
	}
	return Intent{
		ID:          event.IntentID,
		Topic:       event.Topic,
		Key:         event.PolicyNumber,
		Payload:     payload,
		TraceParent: carrier.Get("traceparent"),
		TraceState:  traceState,
	}, nil
}

// maxTraceStateLength is the stored trace_state bound. W3C Trace Context allows dropping a longer
// tracestate, so an oversized caller header never fails the policy mutation.
const maxTraceStateLength = 512

func dateArray(value time.Time) [3]int {
	return [3]int{value.Year(), int(value.Month()), value.Day()}
}

func domainOffer(stored Offer) (domain.Offer, error) {
	if stored.CreationDate == nil || stored.PolicyFrom == nil || stored.PolicyTo == nil ||
		stored.TotalPrice == nil {
		return domain.Offer{}, fmt.Errorf("read offer: %w", errIncompleteRecord)
	}
	covers := make(map[string]string, len(stored.CoverPrices))
	for code, price := range stored.CoverPrices {
		if price == nil {
			return domain.Offer{}, fmt.Errorf(
				"read offer cover: %w",
				errIncompleteRecord,
			)
		}
		covers[code] = *price
	}
	return domain.Offer{
		Number:       stored.Number,
		ProductCode:  valueOrEmpty(stored.ProductCode),
		PolicyFrom:   *stored.PolicyFrom,
		PolicyTo:     *stored.PolicyTo,
		CreationDate: *stored.CreationDate,
		Status:       valueOrEmpty(stored.Status),
		TotalPrice:   *stored.TotalPrice,
		CoverPrices:  covers,
	}, nil
}

func domainPolicy(stored Policy) (domain.Policy, error) {
	policy := domain.Policy{
		Number:     stored.Number,
		AgentLogin: valueOrEmpty(stored.AgentLogin),
		Versions:   make([]domain.PolicyVersion, 0, len(stored.Versions)),
	}
	for _, version := range stored.Versions {
		if version.Number == nil || version.CoverFrom == nil || version.CoverTo == nil ||
			version.VersionFrom == nil || version.VersionTo == nil ||
			version.TotalPremium == nil {
			return domain.Policy{}, fmt.Errorf(
				"read policy version: %w",
				errIncompleteRecord,
			)
		}
		covers := make([]domain.Cover, 0, len(version.Covers))
		for _, cover := range version.Covers {
			if cover.Code == nil || cover.Price == nil {
				return domain.Policy{}, fmt.Errorf(
					"read policy cover: %w",
					errIncompleteRecord,
				)
			}
			covers = append(
				covers,
				domain.Cover{Code: *cover.Code, Price: *cover.Price},
			)
		}
		policy.Versions = append(policy.Versions, domain.PolicyVersion{
			Number:      *version.Number,
			ProductCode: valueOrEmpty(version.ProductCode),
			Holder: domain.PolicyHolder{
				FirstName: valueOrEmpty(version.FirstName),
				LastName:  valueOrEmpty(version.LastName),
				TaxID:     valueOrEmpty(version.Pesel),
			},
			AccountNumber: valueOrEmpty(version.AccountNumber),
			CoverFrom:     *version.CoverFrom,
			CoverTo:       *version.CoverTo,
			VersionFrom:   *version.VersionFrom,
			VersionTo:     *version.VersionTo,
			TotalPremium:  *version.TotalPremium,
			Covers:        covers,
		})
	}
	return policy, nil
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
