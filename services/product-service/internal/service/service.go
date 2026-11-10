// Package service owns Product construction, startup, serving, and bounded shutdown.
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

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	kithealth "github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	productv1 "github.com/igor-baiborodine/insurance-hub/services/product-service/gen/product/v1"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/domain"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/product-service/internal/grpc"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/health"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/product-service/internal/telemetry"
)

const gatewayConnectionIdleTimeout time.Duration = 0

// Run starts the configured Product service and blocks until cancellation or a serving failure.
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

type gatewayConnection interface {
	grpcgo.ClientConnInterface
	Connect()
	GetState() connectivity.State
	WaitForStateChange(context.Context, connectivity.State) bool
	Close() error
}

type dependencies struct {
	listen              func(string, string) (net.Listener, error)
	newTelemetry        func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	openPool            func(context.Context, config.Database) (*pgxpool.Pool, error)
	checkDatabase       func(context.Context, *pgxpool.Pool) error
	newDatabaseObserver func(*pgxpool.Pool, trace.TracerProvider, metric.MeterProvider) (*postgres.Observer, error)
	newReader           func(*pgxpool.Pool, time.Duration, time.Duration, *postgres.Observer) (application.ProductReader, error)
	newGRPCServer       func(*slog.Logger, trace.TracerProvider, propagation.TextMapPropagator, transport.Settings, transport.ListProducts, transport.GetProduct) (grpcServer, error)
	newConnection       func(string, telemetryProvider, config.Config) (gatewayConnection, error)
	newGateway          func(productv1.ProductServiceClient, gateway.Settings) (http.Handler, error)
	newUnavailable      func(gateway.Settings) (http.Handler, error)
	newBusiness         func(http.Handler, config.Config) *http.Server
	newManagement       func(*health.State) *http.Server
	serveBusiness       func(*http.Server, net.Listener) error
}

func productionDependencies() dependencies {
	return dependencies{
		listen: net.Listen,
		newTelemetry: func(ctx context.Context, name string, settings config.Telemetry) (telemetryProvider, error) {
			return telemetry.New(ctx, name, settings)
		},
		openPool:            postgres.OpenPool,
		checkDatabase:       postgres.CheckReadAccess,
		newDatabaseObserver: postgres.NewObserver,
		newReader: func(pool *pgxpool.Pool, acquire, query time.Duration, observer *postgres.Observer) (application.ProductReader, error) {
			return postgres.NewReader(pool, acquire, query, observer)
		},
		newGRPCServer: func(logger *slog.Logger, tracer trace.TracerProvider, propagator propagation.TextMapPropagator, settings transport.Settings, list transport.ListProducts, get transport.GetProduct) (grpcServer, error) {
			return transport.NewServer(logger, tracer, propagator, settings, list, get)
		},
		newConnection:  newGatewayConnection,
		newGateway:     gateway.NewHandler,
		newUnavailable: gateway.NewUnavailableHandler,
		newBusiness: func(handler http.Handler, settings config.Config) *http.Server {
			return &http.Server{
				Handler:           handler,
				ReadHeaderTimeout: settings.ReadHeaderTimeout,
				ReadTimeout:       settings.ReadTimeout,
				WriteTimeout:      settings.WriteTimeout,
				IdleTimeout:       settings.IdleTimeout,
			}
		},
		newManagement: func(state *health.State) *http.Server {
			return kithealth.NewManagementServer(state)
		},
		serveBusiness: gateway.Serve,
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
		grpcgo.WithIdleTimeout(gatewayConnectionIdleTimeout),
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
	pool               *pgxpool.Pool
	databaseObserver   *postgres.Observer
	grpcServer         grpcServer
	grpcListener       net.Listener
	connection         gatewayConnection
	businessServer     *http.Server
	businessListener   net.Listener
	managementServer   *http.Server
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
	if settings.StartupTimeout <= 0 {
		return errors.New("run service: startup timeout must be positive")
	}
	if settings.ShutdownTimeout <= 0 || settings.ExporterTimeout <= 0 ||
		settings.ExporterTimeout >= settings.ShutdownTimeout {
		return errors.New("run service: shutdown budgets must be positive and reserved")
	}
	startupCtx, cancelStartup := context.WithTimeout(ctx, settings.StartupTimeout)
	defer cancelStartup()

	runtime := &runningService{}
	provider, err := deps.newTelemetry(startupCtx, settings.ServiceName, settings.Telemetry)
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
	runtime.pool, err = deps.openPool(startupCtx, settings.Database)
	if err != nil {
		return fail(fmt.Errorf("run service: open Product database: %w", err))
	}
	if err := deps.checkDatabase(startupCtx, runtime.pool); err != nil {
		return fail(fmt.Errorf("run service: verify Product database: %w", err))
	}
	runtime.databaseObserver, err = deps.newDatabaseObserver(
		runtime.pool, provider.TracerProvider(), provider.MeterProvider(),
	)
	if err != nil {
		return fail(fmt.Errorf("run service: create database observer: %w", err))
	}
	reader, err := deps.newReader(
		runtime.pool, settings.Database.AcquireTimeout, settings.Database.QueryTimeout,
		runtime.databaseObserver,
	)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	listProducts, err := application.NewListProducts(reader)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	getProduct, err := application.NewGetProduct(reader)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	observer, err := telemetry.NewRequestObserver(
		logger,
		provider.TracerProvider(),
		provider.MeterProvider(),
		provider.HTTPPropagator(),
	)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	list := func(ctx context.Context) (products []domain.Product, err error) {
		err = observer.Application(
			ctx,
			telemetry.OperationList,
			func(ctx context.Context) error {
				products, err = listProducts.Execute(ctx)
				return err
			},
		)
		return products, err
	}
	get := func(ctx context.Context, code string) (product domain.Product, err error) {
		err = observer.Application(
			ctx,
			telemetry.OperationGet,
			func(ctx context.Context) error {
				product, err = getProduct.Execute(ctx, code)
				return err
			},
		)
		return product, err
	}
	runtime.grpcServer, err = deps.newGRPCServer(
		logger, provider.TracerProvider(), provider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.MaxReceiveBytes,
			MaxSendBytes:    settings.MaxSendBytes,
			Observer:        observer,
		}, list, get,
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
	readyCtx, cancelReady := context.WithTimeout(startupCtx, settings.ProbeTimeout)
	err = waitForConnection(readyCtx, runtime.connection)
	if err == nil {
		err = deps.checkDatabase(readyCtx, runtime.pool)
	}
	cancelReady()
	if err != nil {
		return fail(fmt.Errorf("run service: dependency not ready: %w", err))
	}
	state, err := health.NewState(settings.ProbeTimeout, func(probeCtx context.Context) error {
		if runtime.connection.GetState() != connectivity.Ready {
			return errors.New("gateway connection is not ready")
		}
		return deps.checkDatabase(probeCtx, runtime.pool)
	})
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	runtime.state = state
	gatewaySettings := gateway.Settings{
		RequestTimeout: settings.RequestTimeout,
		Observer:       observer,
	}
	handler, err := deps.newGateway(
		productv1.NewProductServiceClient(runtime.connection), gatewaySettings,
	)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	unavailable, err := deps.newUnavailable(gatewaySettings)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	runtime.tracker = newHandlerTracker(handler, unavailable)
	runtime.businessServer = deps.newBusiness(runtime.tracker, settings)
	runtime.managementServer = deps.newManagement(state)
	businessResult := make(chan error, 1)
	managementResult := make(chan error, 1)
	go func() {
		businessResult <- deps.serveBusiness(runtime.businessServer, runtime.businessListener)
	}()
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
	logger.InfoContext(ctx, "product service started",
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
		overall, started.Add(timeout-exporterTimeout),
	)
	defer cancelGraceful()
	var shutdownErrors []error
	if runtime.tracker != nil {
		runtime.tracker.beginDrain()
	}
	if runtime.businessServer != nil {
		if err := runtime.businessServer.Shutdown(graceful); err != nil &&
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
			runtime.grpcServer.Stop()
			select {
			case <-stopped:
			case <-overall.Done():
				shutdownErrors = append(
					shutdownErrors,
					fmt.Errorf("stop gRPC server: %w", overall.Err()),
				)
			}
		}
	}
	for _, item := range []struct {
		name     string
		listener net.Listener
	}{
		{"business HTTP", runtime.businessListener},
		{"gRPC", runtime.grpcListener},
	} {
		if err := closeListener("close "+item.name+" listener", item.listener); err != nil {
			shutdownErrors = append(shutdownErrors, err)
		}
	}
	if runtime.managementServer != nil {
		if err := runtime.managementServer.Shutdown(graceful); err != nil &&
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
	if err := closeListener(
		"close management listener",
		runtime.managementListener,
	); err != nil {
		shutdownErrors = append(shutdownErrors, err)
	}
	if runtime.databaseObserver != nil {
		if err := runtime.databaseObserver.Close(); err != nil {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("close database observer: %w", err),
			)
		}
	}
	if runtime.pool != nil {
		runtime.pool.Close()
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
	next        http.Handler
	unavailable http.Handler
	mu          sync.Mutex
	draining    bool
	active      sync.WaitGroup
}

func newHandlerTracker(next, unavailable http.Handler) *handlerTracker {
	return &handlerTracker{next: next, unavailable: unavailable}
}

func (tracker *handlerTracker) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	tracker.mu.Lock()
	if tracker.draining {
		tracker.mu.Unlock()
		tracker.unavailable.ServeHTTP(writer, request)
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
	if listener == nil {
		return nil
	}
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}
