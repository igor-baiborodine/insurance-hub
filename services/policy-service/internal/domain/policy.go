package domain

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"
)

// offerValidityDays is how long after its creation date an offer can still be issued.
const offerValidityDays = 30

// Event topics of the policy lifecycle.
const (
	TopicPolicyRegistered = "policy-registered"
	TopicPolicyTerminated = "policy-terminated"
)

var (
	// ErrOfferExpired reports an offer issued more than 30 days after its creation date.
	ErrOfferExpired = errors.New("offer expired")
	// ErrPolicyHasNoVersion reports a stored policy without any version.
	ErrPolicyHasNoVersion = errors.New("policy has no version")
)

// PolicyHolder is the insured person named on a policy version.
type PolicyHolder struct {
	FirstName string
	LastName  string
	TaxID     string
}

// FullName joins the first and last name with one space.
func (holder PolicyHolder) FullName() string {
	return holder.FirstName + " " + holder.LastName
}

// Cover is one priced cover of a policy version; the price is exact decimal text.
type Cover struct {
	Code  string
	Price string
}

// PolicyVersion is one version of a policy. Cover dates bound the insurance coverage;
// version dates bound when these details apply. Dates are calendar dates.
type PolicyVersion struct {
	Number        int64
	ProductCode   string
	Holder        PolicyHolder
	AccountNumber string
	CoverFrom     time.Time
	CoverTo       time.Time
	VersionFrom   time.Time
	VersionTo     time.Time
	TotalPremium  string
	Covers        []Cover
}

// Policy is an issued policy with its complete version history.
type Policy struct {
	Number     string
	AgentLogin string
	Versions   []PolicyVersion
}

// LatestVersion returns the version with the highest number; of equal numbers the last wins.
func (policy Policy) LatestVersion() (PolicyVersion, error) {
	if len(policy.Versions) == 0 {
		return PolicyVersion{}, ErrPolicyHasNoVersion
	}
	latest := policy.Versions[0]
	for _, version := range policy.Versions[1:] {
		if version.Number >= latest.Number {
			latest = version
		}
	}
	return latest, nil
}

// PolicyEvent is one logical lifecycle event. Its dates and premium describe the coverage the
// event announces; IntentID identifies the event across publication retries.
type PolicyEvent struct {
	IntentID     [16]byte
	Topic        string
	PolicyNumber string
	From         time.Time
	To           time.Time
	PolicyHolder string
	ProductCode  string
	TotalPremium string
}

// ExpiredOn reports whether the offer can no longer be issued on the business date.
func (offer Offer) ExpiredOn(businessDate time.Time) bool {
	return calendarDate(offer.CreationDate).AddDate(0, 0, offerValidityDays).
		Before(calendarDate(businessDate))
}

// IssuePolicy creates version 1 of a new policy from the offer at its saved price. Coverage
// and version validity both span the offer's period.
func IssuePolicy(
	number, accountNumber string,
	offer Offer,
	holder PolicyHolder,
	agentLogin string,
	businessDate time.Time,
) (Policy, error) {
	if offer.ExpiredOn(businessDate) {
		return Policy{}, fmt.Errorf("%w: %s", ErrOfferExpired, offer.Number)
	}
	covers := make([]Cover, 0, len(offer.CoverPrices))
	for _, code := range slices.Sorted(maps.Keys(offer.CoverPrices)) {
		covers = append(covers, Cover{Code: code, Price: offer.CoverPrices[code]})
	}
	return Policy{
		Number:     number,
		AgentLogin: agentLogin,
		Versions: []PolicyVersion{{
			Number:        1,
			ProductCode:   offer.ProductCode,
			Holder:        holder,
			AccountNumber: accountNumber,
			CoverFrom:     calendarDate(offer.PolicyFrom),
			CoverTo:       calendarDate(offer.PolicyTo),
			VersionFrom:   calendarDate(offer.PolicyFrom),
			VersionTo:     calendarDate(offer.PolicyTo),
			TotalPremium:  offer.TotalPrice,
			Covers:        covers,
		}},
	}, nil
}

// RegistrationEvent announces a newly issued policy with its version validity and premium.
func RegistrationEvent(intentID [16]byte, policy Policy) (PolicyEvent, error) {
	version, err := policy.LatestVersion()
	if err != nil {
		return PolicyEvent{}, err
	}
	return PolicyEvent{
		IntentID:     intentID,
		Topic:        TopicPolicyRegistered,
		PolicyNumber: policy.Number,
		From:         version.VersionFrom,
		To:           version.VersionTo,
		PolicyHolder: version.Holder.FullName(),
		ProductCode:  version.ProductCode,
		TotalPremium: version.TotalPremium,
	}, nil
}
