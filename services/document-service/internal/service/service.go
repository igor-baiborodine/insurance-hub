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

	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/igor-baiborodine/insurance-hub/libs/servicekit/health"

	documentv1 "github.com/igor-baiborodine/insurance-hub/services/document-service/gen/document/v1"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/application"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/config"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/gateway"
	transport "github.com/igor-baiborodine/insurance-hub/services/document-service/internal/grpc"
	documentkafka "github.com/igor-baiborodine/insurance-hub/services/document-service/internal/kafka"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/objectstore"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/postgres"
	"github.com/igor-baiborodine/insurance-hub/services/document-service/internal/telemetry"
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

type managedDependency interface {
	Check(context.Context) error
	Close()
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
	newDatabase    func(context.Context, config.Database, int, int) (application.MetadataReader, managedDependency, error)
	newObjectStore func(config.ObjectStore, int) (application.ContentReader, managedDependency, error)
	newLookup      func(application.MetadataReader, application.ContentReader, int, int) (*application.FindDocuments, error)
	newGeneration  func(context.Context, config.Events, int) (generationRuntime, error)
	newWorker      func(documentkafka.Settings, generationRuntime, *slog.Logger) (eventWorker, error)
	newGRPCServer  func(*slog.Logger, trace.TracerProvider, propagation.TextMapPropagator, transport.Settings, transport.FindDocumentsExecutor) (grpcServer, error)
	newConnection  func(string, telemetryProvider, config.Config) (gatewayConnection, error)
	newGateway     func(documentv1.DocumentServiceClient, gateway.Settings) (http.Handler, error)
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
		newDatabase:    newDatabaseDependency,
		newObjectStore: newObjectStoreDependency,
		newLookup:      application.NewFindDocuments,
		newGeneration:  newProductionGeneration,
		newWorker: func(
			settings documentkafka.Settings,
			generation generationRuntime,
			logger *slog.Logger,
		) (eventWorker, error) {
			return documentkafka.NewWorker(settings, generation, logger)
		},
		newGRPCServer: func(logger *slog.Logger, tracer trace.TracerProvider, propagator propagation.TextMapPropagator, settings transport.Settings, find transport.FindDocumentsExecutor) (grpcServer, error) {
			return transport.NewServer(logger, tracer, propagator, settings, find)
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

type databaseDependency struct{ pool *pgxpool.Pool }

func (dependency *databaseDependency) Check(ctx context.Context) error {
	return postgres.CheckReadAccess(ctx, dependency.pool)
}

func (dependency *databaseDependency) Close() { dependency.pool.Close() }

func newDatabaseDependency(
	ctx context.Context,
	settings config.Database,
	maximumDocumentBytes int,
	maximumResponseBytes int,
) (application.MetadataReader, managedDependency, error) {
	pool, err := postgres.OpenPool(ctx, settings)
	if err != nil {
		return nil, nil, err
	}
	reader, err := postgres.NewReader(
		pool,
		settings.AcquireTimeout,
		settings.QueryTimeout,
		maximumDocumentBytes,
		maximumResponseBytes,
	)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return reader, &databaseDependency{pool: pool}, nil
}

type objectStoreDependency struct{ reader *objectstore.Reader }

func (dependency *objectStoreDependency) Check(ctx context.Context) error {
	return dependency.reader.CheckReadAccess(ctx)
}

func (dependency *objectStoreDependency) Close() { dependency.reader.Close() }

func newObjectStoreDependency(
	settings config.ObjectStore,
	maximumDocumentBytes int,
) (application.ContentReader, managedDependency, error) {
	reader, err := objectstore.NewReader(settings, maximumDocumentBytes)
	if err != nil {
		return nil, nil, err
	}
	return reader, &objectStoreDependency{reader: reader}, nil
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
	database           managedDependency
	objectStore        managedDependency
	grpcServer         grpcServer
	grpcListener       net.Listener
	connection         gatewayConnection
	businessServer     httpServer
	businessListener   net.Listener
	managementServer   httpServer
	managementListener net.Listener
	tracker            *handlerTracker
	generation         generationRuntime
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
	if settings.StartupTimeout <= 0 {
		return errors.New("run service: startup timeout must be positive")
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
	metadataReader, database, err := deps.newDatabase(
		startupCtx,
		settings.Database,
		settings.MaximumDocumentBytes,
		settings.MaximumResponseBytes,
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize Document database: %w", err))
	}
	runtime.database = database
	if err := runtime.database.Check(startupCtx); err != nil {
		return fail(fmt.Errorf("run service: verify Document database: %w", err))
	}
	contentReader, objectStore, err := deps.newObjectStore(
		settings.ObjectStore,
		settings.MaximumDocumentBytes,
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize Document object storage: %w", err))
	}
	runtime.objectStore = objectStore
	if err := runtime.objectStore.Check(startupCtx); err != nil {
		return fail(fmt.Errorf("run service: verify Document object storage: %w", err))
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
			startupCtx,
			settings.Events,
			settings.MaximumDocumentBytes,
			logger,
			deps,
			eventObserver,
			provider.HTTPPropagator(),
		); err != nil {
			return fail(fmt.Errorf("run service: %w", err))
		}
	}
	lookupObserver, err := telemetry.NewLookupObserver(logger, provider.MeterProvider())
	if err != nil {
		return fail(err)
	}
	find, err := deps.newLookup(
		newObservedMetadataReader(metadataReader, dependencyObserver),
		newObservedContentReader(contentReader, dependencyObserver),
		settings.MaximumResponseBytes,
		settings.MaximumConcurrentLookups,
	)
	if err != nil {
		return fail(fmt.Errorf("run service: initialize document lookup: %w", err))
	}
	runtime.grpcServer, err = deps.newGRPCServer(
		logger, provider.TracerProvider(), provider.GRPCPropagator(),
		transport.Settings{
			RequestTimeout:       settings.RequestTimeout,
			MaxReceiveBytes:      settings.MaxReceiveBytes,
			MaxSendBytes:         settings.MaxSendBytes,
			MaximumResponseBytes: settings.MaximumResponseBytes,
			Observer:             observer,
			LookupObserver:       lookupObserver,
			MeterProvider:        provider.MeterProvider(),
		}, find,
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
	readyContext, cancelReady := context.WithTimeout(startupCtx, settings.ProbeTimeout)
	err = waitForConnection(readyContext, runtime.connection)
	if err == nil {
		err = runtime.database.Check(readyContext)
	}
	if err == nil {
		err = runtime.objectStore.Check(readyContext)
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
		return fail(fmt.Errorf("run service: gateway connection not ready: %w", err))
	}
	state, err := health.NewState(settings.ProbeTimeout, func(probeCtx context.Context) error {
		if runtime.connection.GetState() != connectivity.Ready {
			return errors.New("gateway connection is not ready")
		}
		if err := runtime.database.Check(probeCtx); err != nil {
			return err
		}
		if err := runtime.objectStore.Check(probeCtx); err != nil {
			return err
		}
		return runtime.checkWorker(probeCtx)
	})
	if err != nil {
		return fail(fmt.Errorf("run service: %w", err))
	}
	runtime.state = state
	gatewaySettings := gateway.Settings{
		RequestTimeout:       settings.RequestTimeout,
		MaximumResponseBytes: settings.HTTPMaxResponseBytes,
		TracerProvider:       provider.TracerProvider(),
		MeterProvider:        provider.MeterProvider(),
		Propagator:           provider.HTTPPropagator(),
		Observer:             observer,
	}
	handler, err := deps.newGateway(
		documentv1.NewDocumentServiceClient(runtime.connection),
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
	ctx context.Context,
	settings config.Events,
	maximumDocumentBytes int,
	logger *slog.Logger,
	deps dependencies,
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
	runtime.generation, err = deps.newGeneration(ctx, settings, maximumDocumentBytes)
	if err != nil {
		return err
	}
	runtime.worker, err = deps.newWorker(documentkafka.Settings{
		Brokers: settings.Brokers, Group: settings.Group,
		Topic: settings.RegisteredTopic, ClientID: "policy-registered-document-listener",
		CACert: caCertificate, EventType: "registered", Propagator: propagator,
		Observe: func(ctx context.Context, eventType string) (
			context.Context,
			func(context.Context, string, error),
		) {
			observedCtx, observation := eventObserver.Start(ctx, eventType)
			return observedCtx, observation.Finish
		},
		CommitFailure: eventObserver.RecordCommitFailure,
	}, runtime.generation, logger)
	return err
}

func (runtime *runningService) checkWorker(ctx context.Context) error {
	if runtime.worker == nil {
		return nil
	}
	if runtime.generation == nil {
		return errors.New("document generation is unavailable")
	}
	if err := runtime.generation.Check(ctx); err != nil {
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
	if runtime.objectStore != nil {
		runtime.objectStore.Close()
	}
	if runtime.database != nil {
		runtime.database.Close()
	}
	if runtime.generation != nil {
		runtime.generation.Close()
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
