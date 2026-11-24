package domain_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/domain"
)

func TestIssuePolicy_IssuesVersionOneAtTheSavedPrice(t *testing.T) {
	// given
	offer := storedOffer(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC))
	holder := domain.PolicyHolder{FirstName: "Synthetic", LastName: "Holder", TaxID: "00000"}
	// Day 30 after the creation date is the last day the offer can be issued.
	businessDate := time.Date(2026, 11, 23, 23, 59, 0, 0, time.UTC)

	// when
	policy, err := domain.IssuePolicy("policy-1", "account-1", offer, holder, "agent",
		businessDate)
	// then
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	to := time.Date(2027, 11, 13, 0, 0, 0, 0, time.UTC)
	want := domain.Policy{
		Number:     "policy-1",
		AgentLogin: "agent",
		Versions: []domain.PolicyVersion{
			{
				Number:        1,
				ProductCode:   "HSI",
				Holder:        holder,
				AccountNumber: "account-1",
				CoverFrom:     from,
				CoverTo:       to,
				VersionFrom:   from,
				VersionTo:     to,
				TotalPremium:  "123.45",
				Covers: []domain.Cover{
					{Code: "C1", Price: "100.10"},
					{Code: "C2", Price: "23.35"},
				},
			},
		},
	}
	if !reflect.DeepEqual(policy, want) {
		t.Errorf("policy = %+v, want %+v", policy, want)
	}
}

func TestIssuePolicy_RejectsAnOfferOnDay31(t *testing.T) {
	// given
	offer := storedOffer(time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC))
	businessDate := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	// when
	_, err := domain.IssuePolicy("policy-1", "account-1", offer, domain.PolicyHolder{}, "",
		businessDate)

	// then
	if !errors.Is(err, domain.ErrOfferExpired) {
		t.Errorf("error = %v, want %v", err, domain.ErrOfferExpired)
	}
}

func TestPolicy_LatestVersion_SelectsTheHighestNumber(t *testing.T) {
	// given
	policy := domain.Policy{Versions: []domain.PolicyVersion{
		{Number: 2, TotalPremium: "3.71"},
		{Number: 1, TotalPremium: "123.45"},
	}}

	// when
	latest, err := policy.LatestVersion()

	// then
	if err != nil || latest.Number != 2 || latest.TotalPremium != "3.71" {
		t.Errorf("latest = %+v (%v), want version 2", latest, err)
	}
}

func TestPolicy_LatestVersion_RejectsAPolicyWithoutVersions(t *testing.T) {
	// when
	_, err := domain.Policy{}.LatestVersion()

	// then
	if !errors.Is(err, domain.ErrPolicyHasNoVersion) {
		t.Errorf("error = %v, want %v", err, domain.ErrPolicyHasNoVersion)
	}
}

func TestRegistrationEvent_AnnouncesTheIssuedVersion(t *testing.T) {
	// given
	offer := storedOffer(time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC))
	holder := domain.PolicyHolder{FirstName: "Synthetic", LastName: "Holder"}
	policy, err := domain.IssuePolicy("policy-1", "account-1", offer, holder, "agent",
		time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	intentID := [16]byte{1, 2, 3}

	// when
	event, err := domain.RegistrationEvent(intentID, policy)

	// then
	want := domain.PolicyEvent{
		IntentID: intentID, Topic: "policy-registered", PolicyNumber: "policy-1",
		From:         time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		To:           time.Date(2027, 11, 13, 0, 0, 0, 0, time.UTC),
		PolicyHolder: "Synthetic Holder", ProductCode: "HSI", TotalPremium: "123.45",
	}
	if err != nil || event != want {
		t.Errorf("event = %+v (%v), want %+v", event, err, want)
	}
}

func storedOffer(creationDate time.Time) domain.Offer {
	return domain.Offer{
		Number:       "offer-1",
		ProductCode:  "HSI",
		PolicyFrom:   time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC),
		PolicyTo:     time.Date(2027, 11, 13, 0, 0, 0, 0, time.UTC),
		CreationDate: creationDate,
		Status:       domain.OfferStatusNew,
		TotalPrice:   "123.45",
		CoverPrices:  map[string]string{"C2": "23.35", "C1": "100.10"},
	}
}
