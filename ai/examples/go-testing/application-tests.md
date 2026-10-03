# Example: Application Ports, Errors, and Cancellation

Read the [adaptation guide](README.md). This follows campsite's query-handler tests without exposing
SQL details to the application test.

**Illustrative API:** `domain.Policy` has `ID` and `Status` string fields. The consuming port has
`Find(context.Context, string) (*domain.Policy, error)`. `query.NewGetPolicyHandler(port)` returns a
handler whose `Handle(ctx, query.GetPolicy{PolicyID: id})` method returns the policy or preserves the
repository error cause. An empty ID returns `query.ErrInvalidID` without calling the repository.
`domain.ErrNotFound` is a sentinel error.

Generate `domain.MockPolicyReader` from that port using the owning Makefile's Mockery target;
do not handwrite generated output. This example assumes, as in campsite, that its constructor
registers expectation verification with `t.Cleanup`. Check the actual generated constructor.

Suggested location: `internal/application/query/get_policy_test.go`.

```go
package query_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"example.com/policy/internal/application/query"
	"example.com/policy/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetPolicyHandler(t *testing.T) {
	const id = "89acf5f5-5590-47b7-bb4a-d208dd4cbb52"
	errUnavailable := errors.New("repository unavailable")
	tests := []struct {
		name, id string
		repoErr  error
		wantErr  error
	}{
		{name: "found", id: id},
		{name: "invalid input", id: "", wantErr: query.ErrInvalidID},
		{
			name: "wrapped not found", id: id,
			repoErr: fmt.Errorf("find policy: %w", domain.ErrNotFound), wantErr: domain.ErrNotFound,
		},
		{name: "dependency failure", id: id, repoErr: errUnavailable, wantErr: errUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			t.Cleanup(cancel)
			repo := domain.NewMockPolicyReader(t)
			var repoResult *domain.Policy
			if tc.id != "" {
				if tc.repoErr == nil {
					repoResult = &domain.Policy{ID: id, Status: "active"}
				}
				repo.On("Find", ctx, tc.id).Return(repoResult, tc.repoErr).Once()
			}
			handler := query.NewGetPolicyHandler(repo)

			// when
			got, err := handler.Handle(ctx, query.GetPolicy{PolicyID: tc.id})

			// then
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.NotNil(t, got)
				assert.Equal(t, &domain.Policy{ID: id, Status: "active"}, got)
			}
			if tc.id == "" {
				repo.AssertNumberOfCalls(t, "Find", 0)
			}
		})
	}
}
```

Expect meaningful arguments and call counts: [Testify's Once](https://pkg.go.dev/github.com/stretchr/testify/mock#Call.Once)
checks a single call. Avoid `mock.Anything` for IDs or commands. Exact context matching is appropriate
here because this example's handler forwards its context unchanged; if the real handler derives a
deadline, use a matcher that checks preserved values/deadlines instead of object identity.

## In-flight cancellation without sleeps

Append the following to the same file. This small handwritten function adapter is a test double for
a one-method port, not a replacement for generated multi-method mocks. The worker returns results
through a buffered channel; assertions stay in the test goroutine.

```go
type policyReaderFunc func(context.Context, string) (*domain.Policy, error)

func (f policyReaderFunc) Find(ctx context.Context, id string) (*domain.Policy, error) {
	return f(ctx, id)
}

func TestGetPolicyHandler_CancelsInFlightRead(t *testing.T) {
	// given
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	finished := make(chan struct{})
	type result struct {
		policy *domain.Policy
		err    error
	}
	results := make(chan result, 1)
	repo := policyReaderFunc(func(ctx context.Context, _ string) (*domain.Policy, error) {
		close(started)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return nil, errors.New("repository did not receive cancellation")
		}
	})
	handler := query.NewGetPolicyHandler(repo)
	t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Error("handler worker did not exit")
		}
	})
	go func() {
		defer close(finished)
		policy, err := handler.Handle(ctx, query.GetPolicy{
			PolicyID: "89acf5f5-5590-47b7-bb4a-d208dd4cbb52",
		})
		results <- result{policy: policy, err: err}
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("handler did not reach repository")
	}

	// when
	cancel()

	// then
	select {
	case got := <-results:
		require.ErrorIs(t, got.err, context.Canceled)
		require.Nil(t, got.policy)
	case <-time.After(time.Second):
		t.Fatal("handler ignored cancellation")
	}
}
```

The fallback timer prevents this fake from leaking if the handler discards the caller's context;
the test must still fail in that case. Channels establish ordering. This proves cancellation through
the handler/port boundary, not database-driver cancellation. Use the module's race-enabled test
Make target and bound the entire test run too.
