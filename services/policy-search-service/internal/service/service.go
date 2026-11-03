// Package service owns construction, startup, serving, and bounded shutdown.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

	policysearchv1 "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/gen/policysearch/v1"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/grpc"
	policykafka "github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/policy-search-service/internal/telemetry"
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

type eventWorker interface {
	Check(context.Context) error
	Run(context.Context) error
	Shutdown(context.Context) error
}

type dependencies struct {
	listen         func(string, string) (net.Listener, error)
	newTelemetry   func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	newQuery       func(config.Elasticsearch, *telemetry.DependencyObserver) (queryRuntime, error)
	newProjection  func(config.Elasticsearch, *telemetry.DependencyObserver) (projectionRuntime, error)
	newWorker      func(policykafka.Settings, projectionRuntime, *slog.Logger) (eventWorker, error)
	newGRPCServer  func(*slog.Logger, trace.TracerProvider, propagation.TextMapPropagator, transport.Settings, transport.SearchPolicies) (grpcServer, error)
	newConnection  func(string, telemetryProvider, config.Config) (gatewayConnection, error)
	newGateway     func(policysearchv1.PolicySearchServiceClient, gateway.Settings) (http.Handler, error)
	newUnavailable func(gateway.Settings) (http.Handler, error)
	newBusiness    func(http.Handler, config.Config) httpServer
	newManagement  func(*health.State) httpServer
}

func productionDependencies() dependencies {
	return dependencies{
		listen: net.Listen,
		newTelemetry: func(ctx context.Context, name string, settings config.Telemetry) (telemetryProvider, error) {
			return telemetry.New(ctx, name, settings)
		},
		newQuery:      newProductionQuery,
		newProjection: newProductionProjection,
		newWorker: func(settings policykafka.Settings, projection projectionRuntime, logger *slog.Logger) (eventWorker, error) {
			return policykafka.NewWorker(settings, projection, logger)
		},
		newGRPCServer: func(logger *slog.Logger, tracer trace.TracerProvider, propagator propagation.TextMapPropagator, settings transport.Settings, search transport.SearchPolicies) (grpcServer, error) {
			return transport.NewServer(logger, tracer, propagator, settings, search)
		},
		newConnection:  newGatewayConnection,
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

type runningService struct {
	state              *health.State
	provider           telemetryProvider
	query              queryRuntime
	grpcServer         grpcServer
	grpcListener       net.Listener
	connection         gatewayConnection
	businessServer     httpServer
	businessListener   net.Listener
	managementServer   httpServer
	managementListener net.Listener
	tracker            *handlerTracker
	projection         projectionRuntime
	workers            []eventWorker
	workerCancel       context.CancelFunc
	workerResults      chan error
	workerWG           sync.WaitGroup
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
	dependencyObserver, err := telemetry.NewDependencyObserver(
		logger,
		provider.MeterProvider(),
		provider.TracerProvider(),
	)
	if err != nil {
		return fail(err)
	}
	eventObserver, err := telemetry.NewEventObserver(
		logger,
		provider.MeterProvider(),
		provider.TracerProvider(),
		settings.Events.Enabled,
	)
	if err != nil {
		return fail(err)
	}
	runtime.query, err = deps.newQuery(settings.Elasticsearch, dependencyObserver)
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	if settings.Events.Enabled {
		if err := runtime.configureWorkers(
			settings.Events,
			logger,
			deps,
			dependencyObserver,
			eventObserver,
			provider.HTTPPropagator(),
		); err != nil {
			return fail(fmt.Errorf("run service: %w", err))
		}
	}
	runtime.grpcServer, err = deps.newGRPCServer(
		logger, provider.TracerProvider(), provider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.MaxReceiveBytes,
			MaxSendBytes:    settings.MaxSendBytes,
			Observer:        observer,
			MeterProvider:   provider.MeterProvider(),
		}, runtime.query.SearchPolicies,
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
	dependencyContext, cancelDependency := context.WithTimeout(ctx, settings.ProbeTimeout)
	err = runtime.query.Check(dependencyContext)
	cancelDependency()
	if err != nil {
		return fail(fmt.Errorf("run service: Elasticsearch not ready: %w", err))
	}
	if runtime.projection != nil {
		projectionContext, cancelProjection := context.WithTimeout(
			ctx,
			settings.ProbeTimeout,
		)
		err = runtime.projection.Check(projectionContext)
		cancelProjection()
		if err != nil {
			return fail(
				fmt.Errorf(
					"run service: projection Elasticsearch not ready: %w",
					err,
				),
			)
		}
	}
	runtime.startWorkers(ctx)
	workerCheckContext, cancelWorkerCheck := context.WithTimeout(ctx, settings.ProbeTimeout)
	err = runtime.checkWorkers(workerCheckContext)
	cancelWorkerCheck()
	if err != nil {
		logger.ErrorContext(
			context.Background(),
			"event worker startup failed",
			slog.String("reason", "dependency_check_failed"),
		)
		return fail(fmt.Errorf("run service: event workers not ready: %w", err))
	}
	if len(runtime.workers) > 0 {
		logger.InfoContext(ctx, "event workers ready")
	}
	state, err := health.NewState(
		settings.ProbeTimeout,
		func(checkContext context.Context) error {
			if runtime.connection.GetState() != connectivity.Ready {
				return errors.New("gateway connection is not ready")
			}
			if err := runtime.query.Check(checkContext); err != nil {
				return err
			}
			return runtime.checkWorkers(checkContext)
		},
	)
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
		policysearchv1.NewPolicySearchServiceClient(runtime.connection),
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
	case err := <-runtime.workerResults:
		if ctx.Err() == nil {
			if err == nil {
				err = errors.New("stopped unexpectedly")
			}
			serveErr = fmt.Errorf("event worker stopped: %w", err)
		}
	}
	return errors.Join(
		serveErr,
		runtime.shutdown(settings.ShutdownTimeout, settings.ExporterTimeout),
	)
}

func (runtime *runningService) configureWorkers(
	settings config.Events,
	logger *slog.Logger,
	deps dependencies,
	dependencyObserver *telemetry.DependencyObserver,
	eventObserver *telemetry.EventObserver,
	propagator propagation.TextMapPropagator,
) error {
	caCertificate, err := os.ReadFile(settings.CAFile)
	if err != nil {
		return errors.New("read Kafka CA file")
	}
	if len(bytes.TrimSpace(caCertificate)) == 0 {
		return errors.New("read Kafka CA file: empty certificate")
	}
	runtime.projection, err = deps.newProjection(settings.Writer, dependencyObserver)
	if err != nil {
		return err
	}
	for _, workerSettings := range []policykafka.Settings{
		{Brokers: settings.Brokers, Group: settings.Group, Topic: settings.RegisteredTopic, ClientID: "policy-registered-listener", CACert: caCertificate, EventType: "registered"},
		{Brokers: settings.Brokers, Group: settings.Group, Topic: settings.TerminatedTopic, ClientID: "policy-terminated-listener", CACert: caCertificate, EventType: "terminated"},
	} {
		workerSettings.Propagator = propagator
		workerSettings.Observe = func(ctx context.Context, eventType string) (context.Context, func(context.Context, string, error)) {
			observedCtx, observation := eventObserver.Start(ctx, eventType)
			return observedCtx, observation.Finish
		}
		worker, workerErr := deps.newWorker(workerSettings, runtime.projection, logger)
		if workerErr != nil {
			return workerErr
		}
		runtime.workers = append(runtime.workers, worker)
	}
	return nil
}

func (runtime *runningService) checkWorkers(ctx context.Context) error {
	if len(runtime.workers) == 0 {
		return nil
	}
	if runtime.projection == nil {
		return errors.New("projection is unavailable")
	}
	if err := runtime.projection.Check(ctx); err != nil {
		return err
	}
	for _, worker := range runtime.workers {
		if err := worker.Check(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (runtime *runningService) startWorkers(parent context.Context) {
	if len(runtime.workers) == 0 {
		return
	}
	workerCtx, cancel := context.WithCancel(parent)
	runtime.workerCancel = cancel
	runtime.workerResults = make(chan error, len(runtime.workers))
	for _, worker := range runtime.workers {
		runtime.workerWG.Add(1)
		go func(item eventWorker) {
			defer runtime.workerWG.Done()
			runtime.workerResults <- item.Run(workerCtx)
		}(worker)
	}
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
	if runtime.workerCancel != nil {
		runtime.workerCancel()
		workersStopped := make(chan struct{})
		go func() { runtime.workerWG.Wait(); close(workersStopped) }()
		select {
		case <-workersStopped:
		case <-graceful.Done():
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("stop event workers: %w", graceful.Err()),
			)
		}
	}
	for _, worker := range runtime.workers {
		if err := worker.Shutdown(graceful); err != nil {
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("shutdown event worker: %w", err),
			)
		}
	}

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
	if runtime.query != nil {
		runtime.query.CloseIdleConnections()
	}
	if runtime.projection != nil {
		runtime.projection.CloseIdleConnections()
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
