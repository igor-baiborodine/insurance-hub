// Package service owns construction, startup, serving, and bounded shutdown.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	scaffoldv1 "github.com/igor-baiborodine/insurance-hub/templates/go-service/gen/scaffold/v1"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/templates/go-service/internal/telemetry"
)

// Run starts the configured service and blocks until cancellation or an unexpected serving error.
func Run(ctx context.Context, settings config.Config, logger *slog.Logger) error {
	return run(ctx, settings, logger, productionDependencies())
}

type telemetryProvider interface {
	MeterProvider() metric.MeterProvider
	TracerProvider() trace.TracerProvider
	HTTPPropagator() propagation.TextMapPropagator
	GRPCPropagator() propagation.TextMapPropagator
	Shutdown(context.Context) error
}

type grpcServer interface {
	Serve(net.Listener) error
	GracefulStop()
	Stop()
}

type httpServer interface {
	Serve(net.Listener) error
	Shutdown(context.Context) error
	Close() error
}

type gatewayConnection interface {
	grpcgo.ClientConnInterface
	Connect()
	GetState() connectivity.State
	WaitForStateChange(context.Context, connectivity.State) bool
	Close() error
}

type dependencies struct {
	listen        func(string, string) (net.Listener, error)
	newTelemetry  func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	newGRPCServer func(*slog.Logger, trace.TracerProvider, propagation.TextMapPropagator, transport.Settings, transport.Echo) (grpcServer, error)
	newConnection func(string, telemetryProvider, config.Config) (gatewayConnection, error)
	newGateway    func(scaffoldv1.ExampleServiceClient, gateway.Settings) (http.Handler, error)
	newBusiness   func(http.Handler, config.Config) httpServer
	newManagement func(*health.State) httpServer
	echo          transport.Echo
}

func productionDependencies() dependencies {
	return dependencies{
		listen: net.Listen,
		newTelemetry: func(ctx context.Context, name string, settings config.Telemetry) (telemetryProvider, error) {
			return telemetry.New(ctx, name, settings)
		},
		newGRPCServer: func(logger *slog.Logger, tracer trace.TracerProvider, propagator propagation.TextMapPropagator, settings transport.Settings, echo transport.Echo) (grpcServer, error) {
			return transport.NewServer(logger, tracer, propagator, settings, echo)
		},
		newConnection: newGatewayConnection,
		newGateway:    gateway.NewHandler,
		newBusiness: func(handler http.Handler, settings config.Config) httpServer {
			return &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: settings.ReadHeaderTimeout,
				ReadTimeout:       settings.ReadTimeout,
				WriteTimeout:      settings.WriteTimeout,
				IdleTimeout:       settings.IdleTimeout,
				MaxHeaderBytes:    settings.HTTPMaxHeaderBytes,
			}
		},
		newManagement: func(state *health.State) httpServer { return health.NewManagementServer(state) },
		echo:          transport.IdentityEcho,
	}
}

func newGatewayConnection(
	target string,
	provider telemetryProvider,
	settings config.Config,
) (gatewayConnection, error) {
	connection, err := grpcgo.NewClient(
		target,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithStatsHandler(otelgrpc.NewClientHandler(
			otelgrpc.WithTracerProvider(provider.TracerProvider()),
			otelgrpc.WithMeterProvider(provider.MeterProvider()),
			otelgrpc.WithPropagators(provider.GRPCPropagator()),
		)),
		grpcgo.WithUnaryInterceptor(gateway.ClearApplicationMetadata),
		grpcgo.WithDefaultCallOptions(
			grpcgo.MaxCallSendMsgSize(settings.MaxReceiveBytes),
			grpcgo.MaxCallRecvMsgSize(settings.MaxSendBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create gateway connection: %w", err)
	}
	return connection, nil
}

type runningService struct {
	state              *health.State
	provider           telemetryProvider
	grpcServer         grpcServer
	grpcListener       net.Listener
	connection         gatewayConnection
	businessServer     httpServer
	businessListener   net.Listener
	managementServer   httpServer
	managementListener net.Listener
	tracker            *handlerTracker
}

func run(
	ctx context.Context,
	settings config.Config,
	logger *slog.Logger,
	deps dependencies,
) error {
	if logger == nil {
		return errors.New("run service: logger is required")
	}
	if settings.ShutdownTimeout <= 0 || settings.ExporterTimeout <= 0 ||
		settings.ExporterTimeout >= settings.ShutdownTimeout {
		return errors.New("run service: shutdown budgets must be positive and reserved")
	}

	runtime := &runningService{}
	provider, err := deps.newTelemetry(ctx, settings.ServiceName, settings.Telemetry)
	if err != nil {
		return fmt.Errorf("run service: %w", err)
	}
	runtime.provider = provider
	fail := func(failure error) error {
		return errors.Join(
			failure,
			runtime.shutdown(settings.ShutdownTimeout, settings.ExporterTimeout),
		)
	}
	observer, err := telemetry.NewRequestObserver(logger, provider.MeterProvider())
	if err != nil {
		return fail(err)
	}
	runtime.grpcServer, err = deps.newGRPCServer(
		logger, provider.TracerProvider(), provider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.MaxReceiveBytes,
			MaxSendBytes:    settings.MaxSendBytes,
			Observer:        observer,
			MeterProvider:   provider.MeterProvider(),
		}, deps.echo,
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize gRPC transport: %w", err))
	}
	if runtime.grpcListener, err = deps.listen(
		"tcp",
		settings.GRPCAddress.String(),
	); err != nil {
		return fail(fmt.Errorf("run service: listen for gRPC: %w", err))
	}
	if runtime.businessListener, err = deps.listen(
		"tcp",
		settings.HTTPAddress.String(),
	); err != nil {
		return fail(fmt.Errorf("run service: listen for business HTTP: %w", err))
	}
	if runtime.managementListener, err = deps.listen(
		"tcp",
		settings.HealthAddress.String(),
	); err != nil {
		return fail(fmt.Errorf("run service: listen for management HTTP: %w", err))
	}

	grpcResult := make(chan error, 1)
	go func() { grpcResult <- runtime.grpcServer.Serve(runtime.grpcListener) }()
	target, err := internalDialTarget(runtime.grpcListener.Addr())
	if err != nil {
		return fail(fmt.Errorf("run service: derive gateway target: %w", err))
	}
	runtime.connection, err = deps.newConnection(target, provider, settings)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	readyContext, cancelReady := context.WithTimeout(ctx, settings.ProbeTimeout)
	err = waitForConnection(readyContext, runtime.connection)
	cancelReady()
	if err != nil {
		return fail(fmt.Errorf("run service: gateway connection not ready: %w", err))
	}
	state, err := health.NewState(settings.ProbeTimeout, func(context.Context) error {
		if runtime.connection.GetState() != connectivity.Ready {
			return errors.New("gateway connection is not ready")
		}
		return nil
	})
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	runtime.state = state
	handler, err := deps.newGateway(
		scaffoldv1.NewExampleServiceClient(runtime.connection),
		gateway.Settings{
			RequestTimeout: settings.RequestTimeout,
			MaxBodyBytes:   settings.HTTPMaxBodyBytes,
			TracerProvider: provider.TracerProvider(),
			MeterProvider:  provider.MeterProvider(),
			Propagator:     provider.HTTPPropagator(),
			Observer:       observer,
		},
	)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	runtime.tracker = newHandlerTracker(handler)
	runtime.businessServer = deps.newBusiness(runtime.tracker, settings)
	runtime.managementServer = deps.newManagement(state)
	businessResult := make(chan error, 1)
	managementResult := make(chan error, 1)
	go func() { businessResult <- runtime.businessServer.Serve(runtime.businessListener) }()
	go func() { managementResult <- runtime.managementServer.Serve(runtime.managementListener) }()
	select {
	case err := <-grpcResult:
		return fail(unexpectedServeError("gRPC", err))
	case err := <-businessResult:
		return fail(unexpectedServeError("business HTTP", err))
	case err := <-managementResult:
		return fail(unexpectedServeError("management HTTP", err))
	default:
	}
	runtime.state.SetReady(true)
	logger.InfoContext(ctx, "service started",
		slog.String("grpc_address", runtime.grpcListener.Addr().String()),
		slog.String("http_address", runtime.businessListener.Addr().String()),
		slog.String("health_address", runtime.managementListener.Addr().String()),
	)

	var serveErr error
	select {
	case <-ctx.Done():
	case err := <-grpcResult:
		serveErr = unexpectedServeError("gRPC", err)
	case err := <-businessResult:
		serveErr = unexpectedServeError("business HTTP", err)
	case err := <-managementResult:
		serveErr = unexpectedServeError("management HTTP", err)
	}
	return errors.Join(
		serveErr,
		runtime.shutdown(settings.ShutdownTimeout, settings.ExporterTimeout),
	)
}

func waitForConnection(ctx context.Context, connection gatewayConnection) error {
	connection.Connect()
	for {
		state := connection.GetState()
		if state == connectivity.Ready {
			return nil
		}
		if state == connectivity.Shutdown {
			return errors.New("connection entered shutdown")
		}
		if !connection.WaitForStateChange(ctx, state) {
			return ctx.Err()
		}
	}
}

func internalDialTarget(address net.Addr) (string, error) {
	host, port, err := net.SplitHostPort(address.String())
	if err != nil {
		return "", errors.New("listener address is not TCP host:port")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsUnspecified() {
		if ip.To4() != nil {
			host = "127.0.0.1"
		} else {
			host = "::1"
		}
	}
	return net.JoinHostPort(host, port), nil
}

func (runtime *runningService) shutdown(timeout, exporterTimeout time.Duration) error {
	if runtime.state != nil {
		runtime.state.SetReady(false)
	}
	started := time.Now()
	overall, cancelOverall := context.WithDeadline(context.Background(), started.Add(timeout))
	defer cancelOverall()
	graceful, cancelGraceful := context.WithDeadline(
		overall,
		started.Add(timeout-exporterTimeout),
	)
	defer cancelGraceful()
	var shutdownErrors []error

	if runtime.tracker != nil {
		runtime.tracker.beginDrain()
	}
	if runtime.businessServer != nil {
		if err := runtime.businessServer.Shutdown(
			graceful,
		); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("shutdown business HTTP: %w", err),
			)
			if closeErr := runtime.businessServer.Close(); closeErr != nil &&
				!errors.Is(closeErr, http.ErrServerClosed) {
				shutdownErrors = append(
					shutdownErrors,
					fmt.Errorf("force close business HTTP: %w", closeErr),
				)
			}
		}
	}
	if runtime.tracker != nil && !runtime.tracker.wait(graceful) {
		shutdownErrors = append(
			shutdownErrors,
			fmt.Errorf("drain business HTTP handlers: %w", graceful.Err()),
		)
	}
	if runtime.connection != nil {
		if err := runtime.connection.Close(); err != nil {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("close gateway connection: %w", err),
			)
		}
	}
	if runtime.grpcServer != nil {
		stopped := make(chan struct{})
		go func() { runtime.grpcServer.GracefulStop(); close(stopped) }()
		select {
		case <-stopped:
		case <-graceful.Done():
			forced := make(chan struct{})
			go func() { runtime.grpcServer.Stop(); close(forced) }()
			select {
			case <-stopped:
			case <-forced:
			case <-overall.Done():
				shutdownErrors = append(
					shutdownErrors,
					fmt.Errorf("stop gRPC server: %w", overall.Err()),
				)
			}
		}
	}
	for _, item := range []struct {
		name string
		item net.Listener
	}{{"business HTTP", runtime.businessListener}, {"gRPC", runtime.grpcListener}} {
		if item.item != nil {
			if err := closeListener(
				"close "+item.name+" listener",
				item.item,
			); err != nil {
				shutdownErrors = append(shutdownErrors, err)
			}
		}
	}
	if runtime.managementServer != nil {
		if err := runtime.managementServer.Shutdown(
			graceful,
		); err != nil &&
			!errors.Is(err, http.ErrServerClosed) {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("shutdown management HTTP: %w", err),
			)
			if closeErr := runtime.managementServer.Close(); closeErr != nil &&
				!errors.Is(closeErr, http.ErrServerClosed) {
				shutdownErrors = append(
					shutdownErrors,
					fmt.Errorf("force close management HTTP: %w", closeErr),
				)
			}
		}
	}
	if runtime.managementListener != nil {
		if err := closeListener(
			"close management listener",
			runtime.managementListener,
		); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	if runtime.provider != nil {
		if err := runtime.provider.Shutdown(overall); err != nil {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("shutdown telemetry: %w", err),
			)
		}
	}
	return errors.Join(shutdownErrors...)
}

type handlerTracker struct {
	next     http.Handler
	mu       sync.Mutex
	draining bool
	active   sync.WaitGroup
}

func newHandlerTracker(next http.Handler) *handlerTracker { return &handlerTracker{next: next} }

func (tracker *handlerTracker) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	tracker.mu.Lock()
	if tracker.draining {
		tracker.mu.Unlock()
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	tracker.active.Add(1)
	tracker.mu.Unlock()
	defer tracker.active.Done()
	tracker.next.ServeHTTP(writer, request)
}

func (tracker *handlerTracker) beginDrain() {
	tracker.mu.Lock()
	tracker.draining = true
	tracker.mu.Unlock()
}

func (tracker *handlerTracker) wait(ctx context.Context) bool {
	done := make(chan struct{})
	go func() { tracker.active.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func unexpectedServeError(name string, err error) error {
	if err == nil {
		return fmt.Errorf("serve %s: stopped unexpectedly", name)
	}
	return fmt.Errorf("serve %s: %w", name, err)
}

func closeListener(operation string, listener net.Listener) error {
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

var _ grpcServer = (*grpcgo.Server)(nil)
