// Package application owns policy search use cases and their storage ports.
package application

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/domain"
)

var ErrSearcherRequired = errors.New("policy searcher is required")

// PolicySearcher returns at most the selected legacy hit window in Elasticsearch hit order.
type PolicySearcher interface {
	SearchPolicies(context.Context, string) ([]domain.Policy, error)
}

// SearchPolicies performs the legacy query and orders only its selected hit window.
type SearchPolicies struct{ searcher PolicySearcher }

func NewSearchPolicies(searcher PolicySearcher) (SearchPolicies, error) {
	if searcher == nil {
		return SearchPolicies{}, ErrSearcherRequired
	}
	return SearchPolicies{searcher: searcher}, nil
}

func (useCase SearchPolicies) Execute(
	ctx context.Context,
	query string,
) ([]domain.Policy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	selected, err := useCase.searcher.SearchPolicies(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("search policies: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	ordered := append([]domain.Policy(nil), selected...)
	sort.SliceStable(ordered, func(left, right int) bool {
		leftDate := ordered[left].DateFrom
		rightDate := ordered[right].DateFrom
		switch {
		case leftDate == nil:
			return false
		case rightDate == nil:
			return true
		default:
			return leftDate.After(*rightDate)
		}
	})
	return ordered, nil
}
