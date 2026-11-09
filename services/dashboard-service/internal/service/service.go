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

	dashboardv1 "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/gen/dashboard/v1"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/elasticsearch"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/grpc"
	dashboardkafka "github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/dashboard-service/internal/telemetry"
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

type searchDependency interface {
	Check(context.Context) error
	CloseIdleConnections()
}

type salesReader interface {
	application.TotalSalesReader
	application.AgentSalesReader
	application.SalesTrendsReader
}

type eventWorker interface {
	Preflight(context.Context) error
	Check(context.Context) error
	Run(context.Context) error
	Shutdown(context.Context) error
}

type dependencies struct {
	listen         func(string, string) (net.Listener, error)
	newTelemetry   func(context.Context, string, config.Telemetry) (telemetryProvider, error)
	newSearch      func(config.Elasticsearch, *time.Location) (salesReader, searchDependency, error)
	newProjection  func(config.Elasticsearch, *telemetry.DependencyObserver) (projectionRuntime, error)
	newWorker      func(dashboardkafka.Settings, projectionRuntime, *slog.Logger) (eventWorker, error)
	newGRPCServer  func(*slog.Logger, trace.TracerProvider, propagation.TextMapPropagator, transport.Settings, transport.TotalSalesExecutor, transport.AgentSalesExecutor, transport.SalesTrendsExecutor) (grpcServer, error)
	newConnection  func(string, telemetryProvider, config.Config) (gatewayConnection, error)
	newGateway     func(dashboardv1.DashboardServiceClient, gateway.Settings) (http.Handler, error)
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
		newSearch:     newSearchDependency,
		newProjection: newProductionProjection,
		newWorker: func(
			settings dashboardkafka.Settings,
			projection projectionRuntime,
			logger *slog.Logger,
		) (eventWorker, error) {
			return dashboardkafka.NewWorker(settings, projection, logger)
		},
		newGRPCServer: func(logger *slog.Logger, tracer trace.TracerProvider, propagator propagation.TextMapPropagator, settings transport.Settings, totalSales transport.TotalSalesExecutor, agentSales transport.AgentSalesExecutor, salesTrends transport.SalesTrendsExecutor) (grpcServer, error) {
			return transport.NewServer(
				logger,
				tracer,
				propagator,
				settings,
				totalSales,
				agentSales,
				salesTrends,
			)
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

func newSearchDependency(
	settings config.Elasticsearch,
	salesTrendsTimeZone *time.Location,
) (salesReader, searchDependency, error) {
	var caCertificate []byte
	if settings.CAFile != "" {
		var err error
		caCertificate, err = readBoundedFile(settings.CAFile, 1<<20)
		if err != nil {
			return nil, nil, errors.New("load Elasticsearch CA certificate")
		}
	}
	client, err := elasticsearch.New(elasticsearch.Config{
		Endpoint: settings.URL,
		Index:    settings.Index,
		Username: settings.Username,
		Password: settings.Password,
		CACert:   caCertificate,
		Timeout:  settings.Timeout,
	})
	if err != nil {
		return nil, nil, err
	}
	reader, err := elasticsearch.NewSalesReader(client, salesTrendsTimeZone)
	if err != nil {
		client.CloseIdleConnections()
		return nil, nil, err
	}
	return reader, client, nil
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
	search             searchDependency
	grpcServer         grpcServer
	grpcListener       net.Listener
	connection         gatewayConnection
	businessServer     httpServer
	businessListener   net.Listener
	managementServer   httpServer
	managementListener net.Listener
	tracker            *handlerTracker
	projection         projectionRuntime
	worker             eventWorker
	workerCancel       context.CancelFunc
	workerResult       chan error
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
	if settings.Events.Enabled {
		if err := runtime.configureWorker(
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
	reader, search, err := deps.newSearch(settings.Elasticsearch, settings.SalesTrendsTimeZone)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize Elasticsearch: %w", err))
	}
	runtime.search = search
	totalSales, err := application.NewGetTotalSales(
		newObservedTotalSalesReader(reader, dependencyObserver),
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize total-sales use case: %w", err))
	}
	agentSales, err := application.NewGetAgentSales(
		newObservedAgentSalesReader(reader, dependencyObserver),
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize agent-sales use case: %w", err))
	}
	salesTrends, err := application.NewGetSalesTrends(
		newObservedSalesTrendsReader(reader, dependencyObserver),
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize sales-trends use case: %w", err))
	}
	runtime.grpcServer, err = deps.newGRPCServer(
		logger, provider.TracerProvider(), provider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout:  settings.RequestTimeout,
			MaxReceiveBytes: settings.MaxReceiveBytes,
			MaxSendBytes:    settings.MaxSendBytes,
			Observer:        observer,
			MeterProvider:   provider.MeterProvider(),
		}, totalSales, agentSales, salesTrends,
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
	if err == nil {
		err = runtime.search.Check(readyContext)
	}
	if err == nil && runtime.projection != nil {
		err = runtime.projection.Check(readyContext)
	}
	if err == nil && runtime.worker != nil {
		err = runtime.worker.Preflight(readyContext)
	}
	if err == nil {
		runtime.startWorker(ctx)
		err = runtime.checkWorker(readyContext)
	}
	cancelReady()
	if err != nil {
		return fail(fmt.Errorf("run service: dependency not ready: %w", err))
	}
	state, err := health.NewState(settings.ProbeTimeout, func(ctx context.Context) error {
		if runtime.connection.GetState() != connectivity.Ready {
			return errors.New("gateway connection is not ready")
		}
		if err := runtime.search.Check(ctx); err != nil {
			return err
		}
		return runtime.checkWorker(ctx)
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
		dashboardv1.NewDashboardServiceClient(runtime.connection),
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
	case err := <-runtime.workerResult:
		return fail(unexpectedWorkerError(err))
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
	case err := <-runtime.workerResult:
		if ctx.Err() == nil {
			serveErr = unexpectedWorkerError(err)
		}
	}
	return errors.Join(
		serveErr,
		runtime.shutdown(settings.ShutdownTimeout, settings.ExporterTimeout),
	)
}

func (runtime *runningService) configureWorker(
	settings config.Events,
	logger *slog.Logger,
	deps dependencies,
	dependencyObserver *telemetry.DependencyObserver,
	eventObserver *telemetry.EventObserver,
	propagator propagation.TextMapPropagator,
) error {
	caCertificate, err := readBoundedFile(settings.CAFile, 1<<20)
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
	runtime.worker, err = deps.newWorker(dashboardkafka.Settings{
		Brokers: settings.Brokers, Group: settings.Group,
		Topic: settings.RegisteredTopic, ClientID: "policy-registered-dashboard-listener",
		CACert: caCertificate, EventType: "registered", Propagator: propagator,
		Observe: func(ctx context.Context, eventType string) (
			context.Context,
			func(context.Context, string, error),
		) {
			observedCtx, observation := eventObserver.Start(ctx, eventType)
			return observedCtx, observation.Finish
		},
		CommitFailure:            eventObserver.RecordCommitFailure,
		RequireCommittedProgress: settings.Mode == config.EventProcessingCanonical,
	}, runtime.projection, logger)
	return err
}

func (runtime *runningService) checkWorker(ctx context.Context) error {
	if runtime.worker == nil {
		return nil
	}
	if runtime.projection == nil {
		return errors.New("projection is unavailable")
	}
	if err := runtime.projection.Check(ctx); err != nil {
		return err
	}
	return runtime.worker.Check(ctx)
}

func (runtime *runningService) startWorker(parent context.Context) {
	if runtime.worker == nil {
		return
	}
	workerCtx, cancel := context.WithCancel(parent)
	runtime.workerCancel = cancel
	runtime.workerResult = make(chan error, 1)
	runtime.workerWG.Add(1)
	go func() {
		defer runtime.workerWG.Done()
		runtime.workerResult <- runtime.worker.Run(workerCtx)
	}()
}

func unexpectedWorkerError(err error) error {
	if err == nil {
		err = errors.New("stopped unexpectedly")
	}
	return fmt.Errorf("event worker stopped: %w", err)
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
		workerStopped := make(chan struct{})
		go func() { runtime.workerWG.Wait(); close(workerStopped) }()
		select {
		case <-workerStopped:
		case <-graceful.Done():
			shutdownErrors = append(
				shutdownErrors,
				fmt.Errorf("stop event worker: %w", graceful.Err()),
			)
		}
	}
	if runtime.worker != nil {
		if err := runtime.worker.Shutdown(graceful); err != nil {
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
	if runtime.search != nil {
		runtime.search.CloseIdleConnections()
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
