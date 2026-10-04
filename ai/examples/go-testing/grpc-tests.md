# Example: gRPC Validation and Error Mapping

Read the [adaptation guide](README.md). Adapted from campsite's real gRPC client/server tests.
Calling a server method directly bypasses middleware; use a real client when asserting protobuf
validation, interceptors, serialization, or status mapping.

**Illustrative contract:** versioned generated `policypb/v1` exposes `PolicyService.GetPolicy`,
`GetPolicyRequest{PolicyId}`, and `GetPolicyResponse{Policy: *Policy}` where `Policy` has `PolicyId`
and `Status`. Protobuf validation requires a UUID. `rpc.NewServer(app)` returns a registered
`*grpc.Server` with the production validation middleware; the app port is
`GetPolicy(ctx, query.GetPolicy) (*domain.Policy, error)`. `query.GetPolicy` contains `PolicyID`.

The contract maps a wrapped `domain.ErrNotFound` to `NotFound` with a safe message, and unexpected
errors to `Internal` with a generic message. These types and messages are examples; adapt to the
actual approved proto, server constructor/registration, middleware, and error contract. Do not
substitute a test-only server that omits the real interceptors. Generate protobufs through Make.

Suggested location: `internal/grpc/server_integration_test.go`.

```go
//go:build integration

package grpc_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"example.com/policy/internal/application/query"
	"example.com/policy/internal/domain"
	rpc "example.com/policy/internal/grpc"
	api "example.com/policy/policypb/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// A one-method fake records calls without invoking test assertions in server goroutines.
type policyAppFunc func(context.Context, query.GetPolicy) (*domain.Policy, error)

func (f policyAppFunc) GetPolicy(ctx context.Context, q query.GetPolicy) (*domain.Policy, error) {
	return f(ctx, q)
}

func newPolicyClient(t *testing.T, app policyAppFunc) api.PolicyServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("close listener: %v", err)
		}
	})
	srv, err := rpc.NewServer(app) // Adapt to the real constructor plus registration.
	require.NoError(t, err)
	serveResult := make(chan error, 1)
	go func() { serveResult <- srv.Serve(listener) }()
	t.Cleanup(func() {
		srv.Stop() // Bound test teardown; graceful shutdown needs a separate lifecycle test.
		select {
		case err := <-serveResult:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				t.Errorf("serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	})
	conn, err := grpc.NewClient(listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), // Loopback test only.
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close client: %v", err)
		}
	})
	return api.NewPolicyServiceClient(conn)
}

func TestPolicyServer_GetPolicy(t *testing.T) {
	const id = "89acf5f5-5590-47b7-bb4a-d208dd4cbb52"
	tests := []struct {
		name, id    string
		appErr      error
		wantCode    codes.Code
		wantMessage string
		wantCalls   int
	}{
		{name: "response mapping", id: id, wantCode: codes.OK, wantCalls: 1},
		{
			name: "validation before application", id: "not-a-uuid",
			wantCode: codes.InvalidArgument,
		},
		{
			name: "wrapped not found", id: id,
			appErr: fmt.Errorf("find policy: %w", domain.ErrNotFound),
			wantCode: codes.NotFound, wantMessage: "policy not found", wantCalls: 1,
		},
		{
			name: "internal details are hidden", id: id,
			appErr: errors.New("database failure: private adapter detail"),
			wantCode: codes.Internal, wantMessage: "internal error", wantCalls: 1,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// given
			calls := make(chan query.GetPolicy, 4)
			app := policyAppFunc(func(ctx context.Context, q query.GetPolicy) (*domain.Policy, error) {
				select {
				case calls <- q:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
				if tc.appErr != nil {
					return nil, tc.appErr
				}
				return &domain.Policy{ID: id, Status: "active"}, nil
			})
			client := newPolicyClient(t, app)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			t.Cleanup(cancel)

			// when
			got, err := client.GetPolicy(ctx, &api.GetPolicyRequest{PolicyId: tc.id})

			// then
			assert.Equal(t, tc.wantCode, status.Code(err))
			assert.Len(t, calls, tc.wantCalls)
			if len(calls) > 0 {
				assert.Equal(t, query.GetPolicy{PolicyID: tc.id}, <-calls)
			}
			if tc.wantCode != codes.OK {
				require.Error(t, err)
				assert.Nil(t, got)
				if tc.wantMessage != "" {
					assert.Equal(t, tc.wantMessage, status.Convert(err).Message())
				}
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			want := &api.GetPolicyResponse{Policy: &api.Policy{PolicyId: id, Status: "active"}}
			assert.True(t, proto.Equal(want, got), "response does not match the contract")
		})
	}
}
```

The OS allocates the port; each case owns its server and client. No readiness sleep is needed:
the RPC has a deadline and the client connects on use. Confirm the module supports
[gRPC NewClient](https://pkg.go.dev/google.golang.org/grpc#NewClient); its constructor does not
prove connectivity. Server errors travel over a buffered channel and are checked during cleanup.
`proto.Equal` compares the [protobuf message contents](https://pkg.go.dev/google.golang.org/protobuf/proto#Equal).

The invalid-input case asserts zero application calls. The success expectation is a literal
response, not the production mapper's output. Status assertions compare codes and only
contractually stable messages, not formatting of the entire error string.

Run the module's format/lint and integration Make targets. Extend this pattern for required
authentication, authorization, deadlines, metadata, and HTTP gateway behavior. This example uses
an app fake and loopback plaintext; it proves neither real persistence nor production TLS/security
configuration, and `Stop` deliberately does not test graceful shutdown.
