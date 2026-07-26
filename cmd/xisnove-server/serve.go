package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/araihu/xisnove/application"
	xisclock "github.com/araihu/xisnove/internal/adapters/clock"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	"github.com/araihu/xisnove/internal/adapters/observability"
)

func serveCommand(parent context.Context, args []string) (returnErr error) {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	databaseFlags := addDatabaseFlags(flags)
	keyFlags := addNotificationKeyFlags(flags, os.Getenv)
	cursorKeyFlags := addCursorSigningKeyFlags(flags, os.Getenv)
	notificationWorkerFlags := addNotificationWorkerFlags(flags)
	retentionWorkerFlags := addRetentionWorkerFlags(flags)
	observabilityFlags := addObservabilityFlags(flags)
	listen := flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
	replicas := flags.Int("replicas", 1, "expected number of server replicas")
	if err := flags.Parse(args); err != nil {
		return err
	}
	config, err := databaseFlags.config()
	if err != nil {
		return err
	}
	if err := validateReplicaCount(config.Profile, *replicas); err != nil {
		return err
	}
	slog.SetDefault(observability.NewJSONLogger(os.Stdout, observability.LogConfig{
		SensitiveValues: []string{config.URL, config.AuthToken},
	}))
	tracing, err := observabilityFlags.tracing(parent)
	if err != nil {
		return fmt.Errorf("configure tracing: %w", err)
	}
	traceClosed := false
	databaseClosed := false
	var handle *database.Handle
	defer func() {
		if !traceClosed {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			returnErr = errors.Join(returnErr, tracing.Shutdown(ctx))
			cancel()
		}
		if handle != nil && !databaseClosed {
			returnErr = errors.Join(returnErr, handle.Close())
		}
	}()
	tracing.InstallGlobal()
	sealer, err := keyFlags.load()
	if err != nil {
		return err
	}
	cursors, err := cursorKeyFlags.load(parent)
	if err != nil {
		return err
	}
	handle, err = database.Open(parent, config)
	if err != nil {
		return err
	}
	if err := handle.Ready(parent); err != nil {
		return fmt.Errorf("database is not ready; run db migrate: %w", err)
	}
	if err := validateNotificationKeyring(parent, handle.Store, sealer); err != nil {
		return fmt.Errorf("notification keyring is not ready: %w", err)
	}
	logNotificationKeyring(sealer)

	store := handle.Store
	metrics := observability.NewMetrics()
	var schemaVersion int64
	if err := handle.DB.QueryRowContext(parent, "SELECT COALESCE(MAX(version_id), 0) FROM schema_migrations WHERE is_applied").Scan(&schemaVersion); err == nil {
		metrics.SetSchemaVersion(schemaVersion)
	} else {
		slog.Warn("schema version metric unavailable", "error_class", "schema_version_query", "error", err)
	}
	lifecycle := newServerLifecycle()
	const leaseDuration = 45 * time.Second
	tokens := xiscrypto.NewProductionTokenIssuer()
	notificationWorker, err := notificationWorkerFlags.build(
		store, sealer, tokens, ids.NewUUID, ids.NewUUID(), deliveryObserver(metrics),
	)
	if err != nil {
		return fmt.Errorf("configure notification worker: %w", err)
	}
	maintenanceWorker, err := application.NewMaintenanceWorker(application.MaintenanceWorkerConfig{
		Store: store, Tokens: tokens, NewID: ids.NewUUID, Owner: ids.NewUUID(),
		OnError: func(err error) {
			slog.Error("maintenance projection cycle failed", "error_class", "maintenance_cycle", "error", err)
		},
	})
	if err != nil {
		return fmt.Errorf("configure maintenance worker: %w", err)
	}
	retentionWorker, err := retentionWorkerFlags.build(store, tokens, ids.NewUUID, ids.NewUUID())
	if err != nil {
		return fmt.Errorf("configure retention worker: %w", err)
	}
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(),
		Tokens: tokens, SessionDuration: 24 * time.Hour,
		Now: xisclock.Now, NewID: ids.NewUUID,
	})
	apiTokens := application.NewAPITokenService(application.APITokenServiceConfig{
		Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	management := application.NewManagementService(application.ManagementServiceConfig{
		Store: store, Cursors: cursors, Tokens: tokens, NewID: ids.NewUUID,
	})
	publicStatus, err := application.NewPublicStatusService(application.PublicStatusServiceConfig{
		Store: handle.PublicStatusUnitOfWork(), Now: xisclock.Now,
	})
	if err != nil {
		return fmt.Errorf("configure public status: %w", err)
	}
	discovery := application.NewDiscoveryService(application.DiscoveryServiceConfig{
		Store: handle.DiscoveryUnitOfWork(), NewCandidateID: ids.NewUUID,
		NewMonitorID: ids.NewUUID, Now: xisclock.Now, Cursors: cursors,
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: httpapi.NewServer(httpapi.ServerConfig{
			Auth: auth, APITokens: apiTokens,
			Configuration: application.NewConfigurationService(
				store, xisclock.Now, ids.NewUUID,
			),
			Agents:       agents,
			Management:   management,
			PublicStatus: publicStatus,
			Discovery:    discovery,
			Lease: application.NewLeaseService(application.LeaseServiceConfig{
				Store: store, Tokens: tokens, LeaseDuration: leaseDuration,
				ObserveLease: leaseObserver(metrics),
			}),
			Results: application.NewResultService(application.ResultServiceConfig{
				Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
				LeaseDuration:            leaseDuration,
				ObserveResult:            resultObserver(metrics),
				ObserveMonitorTransition: transitionObserver(metrics),
			}),
			Health: application.NewHealthService(store),
			Notifications: application.NewNotificationAdminService(
				application.NotificationAdminServiceConfig{
					Store: store, Sealer: sealer, Now: xisclock.Now, NewID: ids.NewUUID,
				},
			),
		}),
		Ready:   func(ctx context.Context) error { return lifecycle.Ready(ctx, handle.Ready) },
		Metrics: metrics.Handler(), AdmitWork: lifecycle.AdmitClaim,
	})
	if err != nil {
		return err
	}
	handler = tracing.HTTPMiddleware(handler)
	scheduler := application.NewScheduler(store, ids.NewUUID)
	staleness := application.NewStalenessServiceWithObserver(store, ids.NewUUID, transitionObserver(metrics))
	claims := lifecycle.ClaimContext()
	var loops sync.WaitGroup
	loops.Add(3)
	go func() {
		defer loops.Done()
		runLifecycleLoop(claims, lifecycle, tracing, time.Second, "scheduler.cycle", func(ctx context.Context) error {
			_, err := scheduler.EnqueueDue(ctx, 100)
			return err
		}, func(ctx context.Context, err error) {
			outcome := observability.CycleSuccess
			if err != nil {
				outcome = observability.CycleFailure
			}
			metrics.ObserveSchedulerCycle(outcome)
			logCycleResult("scheduler tick failed", "enqueue_due")(ctx, err)
		})
	}()
	go func() {
		defer loops.Done()
		runLifecycleLoop(claims, lifecycle, tracing, time.Second, "staleness.cycle", func(ctx context.Context) error {
			_, err := staleness.MarkDue(ctx, 100)
			return err
		}, logCycleResult("staleness tick failed", "mark_due"))
	}()
	go func() {
		defer loops.Done()
		runDatabaseMetrics(claims, handle.DB, metrics)
	}()
	if notificationWorker != nil {
		loops.Add(1)
		go func() {
			defer loops.Done()
			runLifecycleLoop(claims, lifecycle, tracing, notificationWorkerFlags.pollInterval, "notification.delivery", func(ctx context.Context) error {
				_, err := notificationWorker.RunOnce(ctx)
				return err
			}, logCycleResult("notification delivery cycle failed", "delivery_cycle"))
		}()
	}
	loops.Add(1)
	go func() {
		defer loops.Done()
		runLifecycleLoop(claims, lifecycle, tracing, time.Second, "maintenance.projection", func(ctx context.Context) error {
			_, err := maintenanceWorker.RunOnce(ctx)
			return err
		}, logCycleResult("maintenance projection cycle failed", "maintenance_cycle"))
	}()
	loops.Add(1)
	go func() {
		defer loops.Done()
		runLifecycleLoop(claims, lifecycle, tracing, retentionWorkerFlags.pollInterval, "retention.cycle", func(ctx context.Context) error {
			_, err := retentionWorker.RunOnce(ctx)
			return err
		}, logCycleResult("retention cycle failed", "retention_cycle"))
	}()

	server := &http.Server{
		Addr: *listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	var serveErr error
	select {
	case <-parent.Done():
		serveErr = parent.Err()
	case <-signals:
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			serveErr = err
		}
	}
	forcedWatchDone := make(chan struct{})
	go func() {
		select {
		case <-signals:
			lifecycle.Force()
			_ = server.Close()
		case <-forcedWatchDone:
		}
	}()
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	shutdownErr := lifecycle.Shutdown(shutdown, func(ctx context.Context) error {
		httpErr := server.Shutdown(ctx)
		loops.Wait()
		return httpErr
	})
	close(forcedWatchDone)
	traceContext, cancelTrace := context.WithTimeout(context.Background(), 10*time.Second)
	traceErr := tracing.Shutdown(traceContext)
	cancelTrace()
	traceClosed = true
	databaseErr := handle.Close()
	databaseClosed = true
	return errors.Join(serveErr, shutdownErr, traceErr, databaseErr)
}
