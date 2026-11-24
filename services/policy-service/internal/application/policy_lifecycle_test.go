package application_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

func TestIssuePolicy_Execute_StoresPolicyAndRegistrationInOneTransaction(t *testing.T) {
	// given
	repository := &fakePolicyRepository{offer: lifecycleOffer()}
	useCase := newIssuePolicy(t, repository)
	command := application.IssuePolicyCommand{
		OfferNumber: "offer-1",
		Holder:      domain.PolicyHolder{FirstName: "Synthetic", LastName: "Holder"},
		AgentLogin:  "agent",
	}

	// when
	number, err := useCase.Execute(t.Context(), command)
	// then
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"begin", "load offer:offer-1", "add policy", "commit"}; !slices.Equal(
		repository.calls, want) {
		t.Errorf("calls = %v, want %v", repository.calls, want)
	}
	stored := repository.added[0]
	if !uuidV4.MatchString(number) || stored.policy.Number != number ||
		!uuidV4.MatchString(stored.policy.Versions[0].AccountNumber) ||
		stored.policy.AgentLogin != "agent" || stored.policy.Versions[0].TotalPremium != "123.45" {
		t.Errorf("issued %q, stored %+v", number, stored.policy)
	}
	if stored.event.Topic != domain.TopicPolicyRegistered ||
		stored.event.PolicyNumber != number ||
		stored.event.IntentID == [16]byte{} {
		t.Errorf("event = %+v", stored.event)
	}
}

func TestIssuePolicy_Execute_IssuesTwoPoliciesForTwoCommands(t *testing.T) {
	// given
	repository := &fakePolicyRepository{offer: lifecycleOffer()}
	useCase := newIssuePolicy(t, repository)
	command := application.IssuePolicyCommand{OfferNumber: "offer-1"}

	// when
	first, firstErr := useCase.Execute(t.Context(), command)
	second, secondErr := useCase.Execute(t.Context(), command)

	// then
	if firstErr != nil || secondErr != nil || first == second || len(repository.added) != 2 ||
		repository.added[0].event.IntentID == repository.added[1].event.IntentID {
		t.Errorf(
			"issued %q and %q (%v, %v), stored %+v",
			first,
			second,
			firstErr,
			secondErr,
			repository.added,
		)
	}
}

func TestIssuePolicy_Execute_WritesNothingWhenIssuanceFails(t *testing.T) {
	for _, test := range []struct {
		name     string
		offerErr error
		created  time.Time
		want     error
	}{
		{
			name: "absent offer", offerErr: application.ErrOfferNotFound,
			want: application.ErrOfferNotFound,
		},
		{
			name: "offer on day 31", created: time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC),
			want: domain.ErrOfferExpired,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			offer := lifecycleOffer()
			if !test.created.IsZero() {
				offer.CreationDate = test.created
			}
			repository := &fakePolicyRepository{offer: offer, offerErr: test.offerErr}
			useCase := newIssuePolicy(t, repository)

			// when
			_, err := useCase.Execute(t.Context(),
				application.IssuePolicyCommand{OfferNumber: "offer-1"})

			// then
			if !errors.Is(err, test.want) || len(repository.added) != 0 ||
				slices.Contains(repository.calls, "commit") {
				t.Errorf("error = %v, calls %v, want %v without writes", err,
					repository.calls, test.want)
			}
		})
	}
}

func TestGetPolicy_Execute_ReturnsTheLatestVersion(t *testing.T) {
	// given
	repository := &fakePolicyRepository{policy: domain.Policy{
		Number: "policy-1",
		Versions: []domain.PolicyVersion{
			{Number: 1, TotalPremium: "123.45"},
			{Number: 2, TotalPremium: "3.71"},
		},
	}}
	useCase, err := application.NewGetPolicy(repository)
	if err != nil {
		t.Fatal(err)
	}

	// when
	details, err := useCase.Execute(t.Context(), "policy-1")

	// then
	if err != nil || details.Number != "policy-1" || details.Latest.Number != 2 ||
		details.Latest.TotalPremium != "3.71" {
		t.Errorf("details = %+v (%v)", details, err)
	}
}

func newIssuePolicy(t *testing.T, repository *fakePolicyRepository) *application.IssuePolicy {
	t.Helper()
	useCase, err := application.NewIssuePolicy(repository, func() time.Time {
		return time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	}, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	return useCase
}

// lifecycleOffer is 30 days old on the 23 November business date.
func lifecycleOffer() domain.Offer {
	return domain.Offer{
		Number:       "offer-1",
		ProductCode:  "HSI",
		PolicyFrom:   time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		PolicyTo:     time.Date(2027, 11, 13, 0, 0, 0, 0, time.UTC),
		CreationDate: time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC),
		TotalPrice:   "123.45",
		CoverPrices:  map[string]string{"C1": "100.10", "C2": "23.35"},
	}
}

type addedPolicy struct {
	policy domain.Policy
	event  domain.PolicyEvent
}

// fakePolicyRepository records one transaction's calls; work errors roll it back.
type fakePolicyRepository struct {
	offer    domain.Offer
	offerErr error
	policy   domain.Policy
	calls    []string
	added    []addedPolicy
}

func (repository *fakePolicyRepository) WithinTransaction(
	ctx context.Context,
	work func(context.Context, application.PolicyWrites) error,
) error {
	repository.calls = append(repository.calls, "begin")
	pending := &fakePolicyWrites{repository: repository}
	if err := work(ctx, pending); err != nil {
		repository.calls = append(repository.calls, "rollback")
		return err
	}
	repository.added = append(repository.added, pending.added...)
	repository.calls = append(repository.calls, "commit")
	return nil
}

func (repository *fakePolicyRepository) LoadPolicy(
	_ context.Context,
	number string,
) (domain.Policy, error) {
	if number != repository.policy.Number {
		return domain.Policy{}, application.ErrPolicyNotFound
	}
	return repository.policy, nil
}

type fakePolicyWrites struct {
	repository *fakePolicyRepository
	added      []addedPolicy
}

func (writes *fakePolicyWrites) LoadOffer(_ context.Context, number string) (domain.Offer, error) {
	writes.repository.calls = append(writes.repository.calls, "load offer:"+number)
	return writes.repository.offer, writes.repository.offerErr
}

func (writes *fakePolicyWrites) AddPolicy(
	_ context.Context,
	policy domain.Policy,
	event domain.PolicyEvent,
) error {
	writes.repository.calls = append(writes.repository.calls, "add policy")
	writes.added = append(writes.added, addedPolicy{policy: policy, event: event})
	return nil
}
