// Package service owns construction, startup, serving, and bounded shutdown.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
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

	policyv1 "github.com/igor-baiborodine/insurance-hub/services/policy-service/gen/policy/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/grpc"
	policykafka "github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/providers"
	"github.com/igor-baiborodine/insurance-hub/services/policy-service/internal/telemetry"
)

const gatewayConnectionIdleTimeout time.Duration = 0

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

// databasePool is the process-owned Policy writer pool.
type databasePool interface {
	postgres.QueryRower
	postgres.TxBeginner
	Close()
}

// providerConnection is a process-owned client connection to Product or Pricing.
type providerConnection interface {
	grpcgo.ClientConnInterface
	Close() error
}

type eventPublisher interface {
	Preflight(context.Context) error
	Check(context.Context) error
	Run(context.Context) error
	Shutdown(context.Context) error
}

type dependencies struct {
	openPool       func(context.Context, postgres.PoolSettings) (databasePool, error)
	checkDatabase  func(context.Context, postgres.QueryRower, time.Duration) error
	listen         func(string, string) (net.Listener, error)
	newTelemetry   func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	newGRPCServer  func(*slog.Logger, trace.TracerProvider, propagation.TextMapPropagator, transport.Settings, transport.Handlers) (grpcServer, error)
	newConnection  func(string, telemetryProvider, config.Config) (gatewayConnection, error)
	newProvider    func(string, telemetryProvider) (providerConnection, error)
	newPublisher   func(policykafka.Settings, application.EventIntentStore, *slog.Logger) (eventPublisher, error)
	now            func() time.Time
	newGateway     func(policyv1.PolicyServiceClient, gateway.Settings) (http.Handler, error)
	newUnavailable func(gateway.Settings) (http.Handler, error)
	newBusiness    func(http.Handler, config.Config) httpServer
	newManagement  func(*health.State) httpServer
	// createOffer, when set, replaces the composed offer use case.
	createOffer transport.CreateOffer
}

func productionDependencies() dependencies {
	return dependencies{
		openPool: func(ctx context.Context, settings postgres.PoolSettings) (databasePool, error) {
			return postgres.OpenPool(ctx, settings)
		},
		checkDatabase: postgres.CheckReadiness,
		listen:        net.Listen,
		newTelemetry: func(ctx context.Context, name string, settings config.Telemetry) (telemetryProvider, error) {
			return telemetry.New(ctx, name, settings)
		},
		newGRPCServer: func(logger *slog.Logger, tracer trace.TracerProvider, propagator propagation.TextMapPropagator, settings transport.Settings, handlers transport.Handlers) (grpcServer, error) {
			return transport.NewServer(
				logger,
				tracer,
				propagator,
				settings,
				handlers,
			)
		},
		newConnection: newGatewayConnection,
		newProvider:   newProviderConnection,
		newPublisher: func(
			settings policykafka.Settings,
			store application.EventIntentStore,
			logger *slog.Logger,
		) (eventPublisher, error) {
			return policykafka.NewPublisher(settings, store, logger)
		},
		now:            time.Now,
		newGateway:     gateway.NewHandler,
		newUnavailable: gateway.NewUnavailableHandler,
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

// newProviderConnection creates a lazily connecting client that propagates trace context.
func newProviderConnection(target string, provider telemetryProvider) (providerConnection, error) {
	connection, err := grpcgo.NewClient(
		target,
		grpcgo.WithTransportCredentials(insecure.NewCredentials()),
		grpcgo.WithStatsHandler(otelgrpc.NewClientHandler(
			otelgrpc.WithTracerProvider(provider.TracerProvider()),
			otelgrpc.WithMeterProvider(provider.MeterProvider()),
			otelgrpc.WithPropagators(provider.GRPCPropagator()),
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("create provider connection: %w", err)
	}
	return connection, nil
}

type runningService struct {
	pool               databasePool
	providers          []providerConnection
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
	publisher          eventPublisher
	publisherCancel    context.CancelFunc
	publisherResult    chan error
	publisherWG        sync.WaitGroup
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
	if err := requireDeployedDependencies(settings); err != nil {
		return fmt.Errorf("run service: %w", err)
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
	checkDatabase := func(ctx context.Context) error {
		if runtime.pool == nil {
			return nil
		}
		return deps.checkDatabase(ctx, runtime.pool, settings.Database.QueryTimeout)
	}
	if settings.Database.Configured() {
		pool, err := deps.openPool(ctx, postgres.PoolSettings{
			URL:            settings.Database.URL.Value(),
			MaxConnections: settings.Database.MaxConnections,
			ConnectTimeout: settings.Database.ConnectTimeout,
		})
		if err != nil {
			return fail(fmt.Errorf("run service: open Policy database: %w", err))
		}
		runtime.pool = pool
		if err := checkDatabase(ctx); err != nil {
			return fail(fmt.Errorf("run service: verify Policy database: %w", err))
		}
		logger.InfoContext(ctx, "policy database ready",
			slog.String("database_role", postgres.WriterRole))
	}
	observer, err := telemetry.NewRequestObserver(logger, provider.MeterProvider())
	if err != nil {
		return fail(err)
	}
	eventObserver, err := telemetry.NewEventObserver(
		logger,
		provider.MeterProvider(),
		provider.TracerProvider(),
	)
	if err != nil {
		return fail(err)
	}
	if settings.Events.Enabled {
		if err := runtime.configurePublisher(
			settings.Events,
			logger,
			deps,
			eventObserver,
			provider.HTTPPropagator(),
		); err != nil {
			return fail(fmt.Errorf("run service: %w", err))
		}
	}
	dependencyObserver, err := telemetry.NewDependencyObserver(provider.MeterProvider())
	if err != nil {
		return fail(err)
	}
	createOffer, err := runtime.composeOffers(settings, provider, dependencyObserver, deps)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	logger.InfoContext(ctx, "offer creation configured",
		slog.Bool("enabled", runtime.pool != nil && settings.Providers.Configured()))
	handlers, err := runtime.composePolicies(settings, deps)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	handlers.CreateOffer = createOffer
	runtime.grpcServer, err = deps.newGRPCServer(
		logger, provider.TracerProvider(), provider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.MaxReceiveBytes,
			MaxSendBytes:    settings.MaxSendBytes,
			Observer:        observer,
			MeterProvider:   provider.MeterProvider(),
		}, handlers,
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
	if err == nil && runtime.publisher != nil {
		err = runtime.publisher.Preflight(readyContext)
	}
	if err == nil {
		runtime.startPublisher(ctx)
		err = runtime.checkPublisher(readyContext)
	}
	cancelReady()
	if err != nil {
		return fail(fmt.Errorf("run service: gateway connection not ready: %w", err))
	}
	// Readiness includes the database; liveness stays independent of it. Each evaluated
	// dependency records its result so a withdrawal names the failing boundary.
	state, err := health.NewState(settings.ProbeTimeout, func(ctx context.Context) error {
		var gatewayErr error
		if runtime.connection.GetState() != connectivity.Ready {
			gatewayErr = errors.New("gateway connection is not ready")
		}
		dependencyObserver.Record(ctx, "gateway", gatewayErr)
		if gatewayErr != nil {
			return gatewayErr
		}
		if runtime.pool != nil {
			err := checkDatabase(ctx)
			dependencyObserver.Record(ctx, "database", err)
			if err != nil {
				return err
			}
		}
		if runtime.publisher == nil {
			return nil
		}
		err := runtime.checkPublisher(ctx)
		dependencyObserver.Record(ctx, "kafka", err)
		return err
	})
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	runtime.state = state
	gatewaySettings := gateway.Settings{
		RequestTimeout: settings.RequestTimeout,
		MaxBodyBytes:   settings.HTTPMaxBodyBytes,
		TracerProvider: provider.TracerProvider(),
		MeterProvider:  provider.MeterProvider(),
		Propagator:     provider.HTTPPropagator(),
		Observer:       observer,
	}
	handler, err := deps.newGateway(
		policyv1.NewPolicyServiceClient(runtime.connection),
		gatewaySettings,
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
	go func() { businessResult <- runtime.businessServer.Serve(runtime.businessListener) }()
	go func() { managementResult <- runtime.managementServer.Serve(runtime.managementListener) }()
	select {
	case err := <-grpcResult:
		return fail(unexpectedServeError("gRPC", err))
	case err := <-businessResult:
		return fail(unexpectedServeError("business HTTP", err))
	case err := <-managementResult:
		return fail(unexpectedServeError("management HTTP", err))
	case err := <-runtime.publisherResult:
		return fail(unexpectedPublisherError(err))
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
	case err := <-runtime.publisherResult:
		if ctx.Err() == nil {
			serveErr = unexpectedPublisherError(err)
		}
	}
	return errors.Join(
		serveErr,
		runtime.shutdown(settings.ShutdownTimeout, settings.ExporterTimeout),
	)
}

func (runtime *runningService) configurePublisher(
	settings config.Events,
	logger *slog.Logger,
	deps dependencies,
	eventObserver *telemetry.EventObserver,
	propagator propagation.TextMapPropagator,
) error {
	if runtime.pool == nil {
		return errors.New("configure Policy event publisher: database is required")
	}
	caCertificate, err := readBoundedFile(settings.CAFile, 1<<20)
	if err != nil {
		return errors.New("read Kafka CA file")
	}
	if len(bytes.TrimSpace(caCertificate)) == 0 {
		return errors.New("read Kafka CA file: empty certificate")
	}
	store, err := postgres.NewStore(runtime.pool)
	if err != nil {
		return err
	}
	runtime.publisher, err = deps.newPublisher(policykafka.Settings{
		Brokers: settings.Brokers, ClientID: "policy-service",
		RegisteredTopic: settings.RegisteredTopic,
		TerminatedTopic: settings.TerminatedTopic,
		CACert:          caCertificate,
		PublishTimeout:  settings.PublishTimeout,
		Propagator:      propagator,
		Observe: func(ctx context.Context, eventType string) (
			context.Context,
			func(context.Context, string, error),
		) {
			observedCtx, observation := eventObserver.Start(ctx, eventType)
			return observedCtx, observation.Finish
		},
	}, store, logger)
	return err
}

func readBoundedFile(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil || int64(len(data)) > maximum {
		return nil, errors.New("file exceeds accepted certificate size")
	}
	return data, nil
}

func (runtime *runningService) checkPublisher(ctx context.Context) error {
	if runtime.publisher == nil {
		return nil
	}
	return runtime.publisher.Check(ctx)
}

func (runtime *runningService) startPublisher(parent context.Context) {
	if runtime.publisher == nil {
		return
	}
	publisherCtx, cancel := context.WithCancel(parent)
	runtime.publisherCancel = cancel
	runtime.publisherResult = make(chan error, 1)
	runtime.publisherWG.Add(1)
	go func() {
		defer runtime.publisherWG.Done()
		runtime.publisherResult <- runtime.publisher.Run(publisherCtx)
	}()
}

func unexpectedPublisherError(err error) error {
	if err == nil {
		err = errors.New("stopped unexpectedly")
	}
	return fmt.Errorf("event publisher stopped: %w", err)
}

// requireDeployedDependencies refuses a deployed instance without storage or providers. Only
// local development may run without them, in which case offers report that they are unavailable.
func requireDeployedDependencies(settings config.Config) error {
	if environment := settings.Identity.Environment; environment == "" ||
		environment == "development" {
		return nil
	}
	for _, dependency := range []struct {
		setting string
		missing bool
	}{
		{"POLICY_DATABASE_URL", !settings.Database.Configured()},
		{"PRODUCT_GRPC_TARGET", settings.Providers.ProductTarget == ""},
		{"PRICING_GRPC_TARGET", settings.Providers.PricingTarget == ""},
	} {
		if dependency.missing {
			return fmt.Errorf(
				"%s is required for the deployed Policy service",
				dependency.setting,
			)
		}
	}
	return nil
}

// composeOffers wires offer creation when both storage and providers are configured; otherwise
// the operation reports that offers are unavailable.
func (runtime *runningService) composeOffers(
	settings config.Config,
	provider telemetryProvider,
	observer providers.CallObserver,
	deps dependencies,
) (transport.CreateOffer, error) {
	if deps.createOffer != nil {
		return deps.createOffer, nil
	}
	if runtime.pool == nil || !settings.Providers.Configured() {
		return application.UnavailableOffers, nil
	}
	connections := make([]grpcgo.ClientConnInterface, 0, 2)
	for _, target := range []string{
		settings.Providers.ProductTarget,
		settings.Providers.PricingTarget,
	} {
		connection, err := deps.newProvider(target, provider)
		if err != nil {
			return nil, err
		}
		runtime.providers = append(runtime.providers, connection)
		connections = append(connections, connection)
	}
	catalog, err := providers.NewProductCatalog(
		connections[0],
		settings.Providers.CallTimeout,
		observer,
	)
	if err != nil {
		return nil, err
	}
	calculator, err := providers.NewPricingCalculator(
		connections[1],
		settings.Providers.CallTimeout,
		observer,
	)
	if err != nil {
		return nil, err
	}
	store, err := postgres.NewStore(runtime.pool)
	if err != nil {
		return nil, err
	}
	useCase, err := application.NewCreateOffer(
		catalog, calculator, store, deps.now, settings.BusinessTimeZone,
	)
	if err != nil {
		return nil, err
	}
	return useCase.Execute, nil
}

// composePolicies wires the policy lifecycle when storage is configured; otherwise the
// operations report that policy storage is unavailable. No lifecycle operation calls a provider.
func (runtime *runningService) composePolicies(
	settings config.Config,
	deps dependencies,
) (transport.Handlers, error) {
	if runtime.pool == nil {
		return transport.Handlers{
			IssuePolicy:     application.UnavailableIssuance,
			GetPolicy:       application.UnavailablePolicyDetails,
			TerminatePolicy: application.UnavailableTermination,
		}, nil
	}
	store, err := postgres.NewStore(runtime.pool)
	if err != nil {
		return transport.Handlers{}, err
	}
	issue, err := application.NewIssuePolicy(store, deps.now, settings.BusinessTimeZone)
	if err != nil {
		return transport.Handlers{}, err
	}
	get, err := application.NewGetPolicy(store)
	if err != nil {
		return transport.Handlers{}, err
	}
	terminate, err := application.NewTerminatePolicy(store, deps.now, settings.BusinessTimeZone)
	if err != nil {
		return transport.Handlers{}, err
	}
	return transport.Handlers{
		IssuePolicy:     issue.Execute,
		GetPolicy:       get.Execute,
		TerminatePolicy: terminate.Execute,
	}, nil
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
	if runtime.publisherCancel != nil {
		runtime.publisherCancel()
		publisherStopped := make(chan struct{})
		go func() { runtime.publisherWG.Wait(); close(publisherStopped) }()
		select {
		case <-publisherStopped:
		case <-graceful.Done():
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("stop event publisher: %w", graceful.Err()),
			)
		}
	}
	if runtime.publisher != nil {
		if err := runtime.publisher.Shutdown(graceful); err != nil {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("shutdown event publisher: %w", err),
			)
		}
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
	// Handlers have drained, so no request still uses a provider or database connection.
	for _, connection := range runtime.providers {
		if err := connection.Close(); err != nil {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("close provider connection: %w", err),
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
	if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

var _ grpcServer = (*grpcgo.Server)(nil)
