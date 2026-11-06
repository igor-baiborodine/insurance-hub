package grpc_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace/noop"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/domain"
	transport "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/grpc"
)

type executorFunc func(context.Context, application.TotalSalesQuery) (application.TotalSalesResult, error)

type agentExecutorFunc func(
	context.Context,
	application.AgentSalesQuery,
) (application.AgentSalesResult, error)

func (function executorFunc) Execute(
	ctx context.Context,
	query application.TotalSalesQuery,
) (application.TotalSalesResult, error) {
	return function(ctx, query)
}

func (function agentExecutorFunc) Execute(
	ctx context.Context,
	query application.AgentSalesQuery,
) (application.AgentSalesResult, error) {
	return function(ctx, query)
}

func TestDashboardService_UsesGeneratedRPCAndPreservesProductPresence(t *testing.T) {
	if dashboardv1.DashboardService_GetTotalSales_FullMethodName !=
		"/dashboard.v1.DashboardService/GetTotalSales" {
		t.Fatalf(
			"full method = %q",
			dashboardv1.DashboardService_GetTotalSales_FullMethodName,
		)
	}
	for _, test := range []struct {
		name    string
		product *string
		present bool
		value   string
	}{
		{name: "all"},
		{name: "exact empty", product: stringPointer(""), present: true},
		{name: "exact value", product: stringPointer(" SAFE "), present: true, value: " SAFE "},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := newClient(t, executorFunc(func(
				_ context.Context,
				query application.TotalSalesQuery,
			) (application.TotalSalesResult, error) {
				if query.Product.Present() != test.present ||
					query.Product.Value() != test.value ||
					query.DateFrom.String() != "2019-01-01" ||
					query.DateTo.String() != "2020-01-01" {
					t.Fatalf("query = %+v", query)
				}
				total, _ := domain.NewSalesValue(5, domain.NewPremiumAmount(500000))
				product, _ := domain.NewSalesValue(
					1,
					domain.NewPremiumAmount(100000),
				)
				return application.TotalSalesResult{
					Total: total,
					PerProductTotal: map[string]domain.SalesValue{
						"SAFE_FARM": product,
					},
				}, nil
			}))
			// when
			response, err := client.GetTotalSales(
				context.Background(),
				&dashboardv1.GetTotalSalesRequest{
					ProductCode:  test.product,
					SaleDateFrom: "2019-01-01",
					SaleDateTo:   "2020-01-01",
				},
			)
			// then
			if err != nil {
				t.Fatal(err)
			}
			if response.GetTotal().GetPoliciesCount() != 5 ||
				response.GetTotal().GetPremiumAmountMinorUnits() != 500000 ||
				response.GetPerProductTotal()["SAFE_FARM"].GetPoliciesCount() != 1 {
				t.Fatalf("response = %+v", response)
			}
		})
	}
}

func TestDashboardService_GetAgentSales_PreservesPresenceAndExactDecimal(t *testing.T) {
	if dashboardv1.DashboardService_GetAgentSales_FullMethodName !=
		"/dashboard.v1.DashboardService/GetAgentSales" {
		t.Fatalf(
			"full method = %q",
			dashboardv1.DashboardService_GetAgentSales_FullMethodName,
		)
	}
	// given
	agentExecutor := agentExecutorFunc(func(
		_ context.Context,
		query application.AgentSalesQuery,
	) (application.AgentSalesResult, error) {
		if !query.Agent.Present() || query.Agent.Value() != "" ||
			!query.Product.Present() || query.Product.Value() != " SAFE " ||
			query.DateFrom.String() != "2019-01-01" || query.DateTo.String() != "2020-01-01" {
			t.Fatalf("query = %+v", query)
		}
		amount, _ := domain.NewAgentPremiumAmount("1.005")
		value, _ := domain.NewAgentSalesValue(3, amount)
		return application.AgentSalesResult{
			PerAgentTotal: map[string]domain.AgentSalesValue{"": value},
		}, nil
	})
	client := newClient(t, nil, agentExecutor)

	// when
	response, err := client.GetAgentSales(
		context.Background(),
		&dashboardv1.GetAgentSalesRequest{
			AgentLogin: stringPointer(""), ProductCode: stringPointer(" SAFE "),
			SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
		},
	)

	// then
	if err != nil || response.GetPerAgentTotal()[""].GetPoliciesCount() != 3 ||
		response.GetPerAgentTotal()[""].GetPremiumAmount() != "1.005" {
		t.Fatalf("GetAgentSales() = (%v, %v)", response, err)
	}
}

func TestDashboardService_GetAgentSales_RejectsInvalidAndMapsUnavailable(t *testing.T) {
	// given
	calls := 0
	client := newClient(t, nil, agentExecutorFunc(func(
		context.Context,
		application.AgentSalesQuery,
	) (application.AgentSalesResult, error) {
		calls++
		return application.AgentSalesResult{}, application.ErrAgentSalesUnavailable
	}))

	// when
	_, invalidErr := client.GetAgentSales(
		context.Background(),
		&dashboardv1.GetAgentSalesRequest{},
	)
	_, unavailableErr := client.GetAgentSales(
		context.Background(),
		&dashboardv1.GetAgentSalesRequest{
			SaleDateFrom: "2019-01-01", SaleDateTo: "2020-01-01",
		},
	)

	// then
	if status.Code(invalidErr) != codes.InvalidArgument ||
		status.Code(unavailableErr) != codes.Unavailable || calls != 1 {
		t.Fatalf("errors/calls = (%v, %v, %d)", invalidErr, unavailableErr, calls)
	}
}

func TestDashboardService_RejectsMissingMalformedAndInvalidDatesBeforeUseCase(t *testing.T) {
	// given
	var calls int
	client := newClient(t, executorFunc(func(
		context.Context,
		application.TotalSalesQuery,
	) (application.TotalSalesResult, error) {
		calls++
		return application.TotalSalesResult{}, nil
	}))
	for _, request := range []*dashboardv1.GetTotalSalesRequest{
		{},
		{SaleDateFrom: "2019-01-01", SaleDateTo: ""},
		{SaleDateFrom: "2019-02-30", SaleDateTo: "2020-01-01"},
	} {
		// when
		_, err := client.GetTotalSales(context.Background(), request)

		// then
		if status.Code(err) != codes.InvalidArgument {
			t.Errorf("error = %v, want INVALID_ARGUMENT", err)
		}
	}
	if calls != 0 {
		t.Errorf("use-case calls = %d", calls)
	}
}

func TestDashboardService_MapsBoundedFailures(t *testing.T) {
	// given
	for _, test := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{name: "unavailable", err: application.ErrTotalSalesUnavailable, code: codes.Unavailable},
		{name: "internal", err: errors.New("private-upstream-body"), code: codes.Internal},
	} {
		t.Run(test.name, func(t *testing.T) {
			// given
			client := newClient(t, executorFunc(func(
				context.Context,
				application.TotalSalesQuery,
			) (application.TotalSalesResult, error) {
				return application.TotalSalesResult{}, test.err
			}))
			// when
			_, err := client.GetTotalSales(context.Background(), validRequest())

			// then
			if status.Code(err) != test.code ||
				(test.code == codes.Internal && status.Convert(err).Message() != "internal error") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDashboardService_PropagatesCallerCancellationAndDeadline(t *testing.T) {
	// given
	client := newClient(t, executorFunc(func(
		ctx context.Context,
		_ application.TotalSalesQuery,
	) (application.TotalSalesResult, error) {
		<-ctx.Done()
		return application.TotalSalesResult{}, ctx.Err()
	}))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	// when
	_, err := client.GetTotalSales(cancelled, validRequest())

	// then
	if status.Code(err) != codes.Canceled {
		t.Errorf("cancel error = %v", err)
	}

	deadline, stop := context.WithTimeout(context.Background(), time.Millisecond)
	defer stop()

	// when
	_, err = client.GetTotalSales(deadline, validRequest())

	// then
	if status.Code(err) != codes.DeadlineExceeded {
		t.Errorf("deadline error = %v", err)
	}
}

func TestNewServer_RequiresDependenciesAndPositiveLimits(t *testing.T) {
	// given
	settings := transport.Settings{
		RequestTimeout: time.Second, MaxReceiveBytes: 4096, MaxSendBytes: 4096,
		MeterProvider: metricnoop.NewMeterProvider(),
	}
	executor := executorFunc(
		func(context.Context, application.TotalSalesQuery) (application.TotalSalesResult, error) {
			return application.TotalSalesResult{}, nil
		},
	)
	agentExecutor := agentExecutorFunc(
		func(context.Context, application.AgentSalesQuery) (application.AgentSalesResult, error) {
			return application.AgentSalesResult{}, nil
		},
	)
	logger := slog.Default()
	tracer := noop.NewTracerProvider()
	propagator := propagation.TraceContext{}
	for _, call := range []func() error{
		func() error {
			_, err := transport.NewServer(nil, tracer, propagator, settings, executor, agentExecutor)
			return err
		},
		func() error {
			_, err := transport.NewServer(logger, nil, propagator, settings, executor, agentExecutor)
			return err
		},
		func() error {
			_, err := transport.NewServer(logger, tracer, nil, settings, executor, agentExecutor)
			return err
		},
		func() error {
			_, err := transport.NewServer(logger, tracer, propagator, settings, nil, agentExecutor)
			return err
		},
		func() error {
			_, err := transport.NewServer(logger, tracer, propagator, settings, executor, nil)
			return err
		},
		func() error {
			settings.RequestTimeout = 0
			_, err := transport.NewServer(logger, tracer, propagator, settings, executor, agentExecutor)
			return err
		},
	} {
		// when
		err := call()

		// then
		if err == nil {
			t.Error("invalid server dependencies accepted")
		}
	}
}

func newClient(
	t *testing.T,
	executor transport.TotalSalesExecutor,
	agentExecutors ...transport.AgentSalesExecutor,
) dashboardv1.DashboardServiceClient {
	t.Helper()
	if executor == nil {
		executor = executorFunc(
			func(context.Context, application.TotalSalesQuery) (application.TotalSalesResult, error) {
				return application.TotalSalesResult{}, nil
			},
		)
	}
	var agentExecutor transport.AgentSalesExecutor = agentExecutorFunc(
		func(context.Context, application.AgentSalesQuery) (application.AgentSalesResult, error) {
			return application.AgentSalesResult{}, nil
		},
	)
	if len(agentExecutors) > 0 {
		agentExecutor = agentExecutors[0]
	}
	listener := bufconn.Listen(1 << 20)
	server, err := transport.NewServer(
		slog.Default(), noop.NewTracerProvider(), propagation.TraceContext{},
		transport.Settings{
			RequestTimeout:  time.Second,
			MaxReceiveBytes: 1 << 20,
			MaxSendBytes:    1 << 20,
			MeterProvider:   metricnoop.NewMeterProvider(),
		},
		executor,
		agentExecutor,
	)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	connection, err := grpcgo.NewClient(
		"passthrough:///bufnet",
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return dashboardv1.NewDashboardServiceClient(connection)
}

func validRequest() *dashboardv1.GetTotalSalesRequest {
	return &dashboardv1.GetTotalSalesRequest{
		SaleDateFrom: "2019-01-01",
		SaleDateTo:   "2020-01-01",
	}
}

func stringPointer(value string) *string { return &value }
