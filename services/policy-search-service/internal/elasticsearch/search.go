package elasticsearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	elasticsearchapi "github.com/elastic/go-elasticsearch/v8"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

const (
	PolicyIndex   = "policy-views-issue-229"
	selectedLimit = 100
)

var (
	ErrClientRequired  = errors.New("elasticsearch client is required")
	ErrSearchFailed    = errors.New("elasticsearch search failed")
	ErrInvalidResponse = errors.New("invalid elasticsearch search response")
)

// Searcher implements the application search port against the isolated policy index.
type Searcher struct{ client *elasticsearchapi.Client }

var _ application.PolicySearcher = (*Searcher)(nil)

func NewSearcher(client *Client) (*Searcher, error) {
	if client == nil || client.Client == nil {
		return nil, ErrClientRequired
	}
	return &Searcher{client: client.Client}, nil
}

func (searcher *Searcher) SearchPolicies(
	ctx context.Context,
	query string,
) ([]domain.Policy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(searchRequest{
		Query: searchQuery{QueryString: queryString{
			Query:  query,
			Fields: []string{"number", "policyHolder"},
		}},
		Size: selectedLimit,
	})
	if err != nil {
		return nil, ErrSearchFailed
	}
	response, err := searcher.client.Search(
		searcher.client.Search.WithContext(ctx),
		searcher.client.Search.WithIndex(PolicyIndex),
		searcher.client.Search.WithBody(bytes.NewReader(body)),
	)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, contextErr
		}
		return nil, ErrSearchFailed
	}
	defer func() { _ = response.Body.Close() }()
	if response.IsError() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, ErrSearchFailed
	}

	var result searchResponse
	decoder := json.NewDecoder(response.Body)
	if err := decoder.Decode(&result); err != nil {
		return nil, ErrInvalidResponse
	}
	if err := requireJSONEnd(decoder); err != nil || len(result.Hits.Hits) > selectedLimit {
		return nil, ErrInvalidResponse
	}

	policies := make([]domain.Policy, len(result.Hits.Hits))
	for index, hit := range result.Hits.Hits {
		policy, err := hit.Source.policy()
		if err != nil {
			return nil, fmt.Errorf("decode hit %d: %w", index, ErrInvalidResponse)
		}
		policies[index] = policy
	}
	return policies, nil
}

type searchRequest struct {
	Query searchQuery `json:"query"`
	Size  int         `json:"size"`
}

type searchQuery struct {
	QueryString queryString `json:"query_string"`
}

type queryString struct {
	Query  string   `json:"query"`
	Fields []string `json:"fields"`
}

type searchResponse struct {
	Hits struct {
		Hits []searchHit `json:"hits"`
	} `json:"hits"`
}

type searchHit struct {
	Source policySource `json:"_source"`
}

type policySource struct {
	Number       *string `json:"number"`
	DateFrom     *string `json:"dateFrom"`
	DateTo       *string `json:"dateTo"`
	PolicyHolder *string `json:"policyHolder"`
}

func (source policySource) policy() (domain.Policy, error) {
	policy := domain.Policy{
		Number:       value(source.Number),
		PolicyHolder: value(source.PolicyHolder),
	}
	var err error
	policy.DateFrom, err = parseOptionalDate(source.DateFrom)
	if err != nil {
		return domain.Policy{}, err
	}
	policy.DateTo, err = parseOptionalDate(source.DateTo)
	if err != nil {
		return domain.Policy{}, err
	}
	return policy, nil
}

func parseOptionalDate(value *string) (*domain.Date, error) {
	if value == nil {
		return nil, nil
	}
	date, err := domain.ParseDate(*value)
	if err != nil {
		return nil, err
	}
	return &date, nil
}

func value(optional *string) string {
	if optional == nil {
		return ""
	}
	return *optional
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalidResponse
	}
	return nil
}
