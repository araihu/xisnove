package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	xisclock "github.com/araihu/xisnove/internal/adapters/clock"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
	"github.com/araihu/xisnove/internal/adapters/database"
	"github.com/araihu/xisnove/internal/adapters/httpapi"
	"github.com/araihu/xisnove/internal/adapters/ids"
	"github.com/araihu/xisnove/internal/application"
)

func serveCommand(parent context.Context, args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	databaseFlags := addDatabaseFlags(flags)
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
	handle, err := database.Open(parent, config)
	if err != nil {
		return err
	}
	defer handle.Close()
	if err := handle.Ready(parent); err != nil {
		return fmt.Errorf("database is not ready; run db migrate: %w", err)
	}

	store := handle.Store
	const leaseDuration = 45 * time.Second
	tokens := xiscrypto.NewProductionTokenIssuer()
	auth := application.NewAuthService(application.AuthServiceConfig{
		Store: store, Passwords: xiscrypto.NewProductionPasswordHasher(),
		Tokens: tokens, SessionDuration: 24 * time.Hour,
		Now: xisclock.Now, NewID: ids.NewUUID,
	})
	agents := application.NewAgentService(application.AgentServiceConfig{
		Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
	})
	handler, err := httpapi.NewHandler(httpapi.HandlerConfig{
		Server: httpapi.NewServer(httpapi.ServerConfig{
			Auth: auth,
			Configuration: application.NewConfigurationService(
				store, xisclock.Now, ids.NewUUID,
			),
			Agents: agents,
			Lease: application.NewLeaseService(application.LeaseServiceConfig{
				Store: store, Tokens: tokens, LeaseDuration: leaseDuration,
			}),
			Results: application.NewResultService(application.ResultServiceConfig{
				Store: store, Tokens: tokens, Now: xisclock.Now, NewID: ids.NewUUID,
				LeaseDuration: leaseDuration,
			}),
			Health: application.NewHealthService(store),
		}),
		Ready: func(ctx context.Context) error { return handle.Ready(ctx) },
	})
	if err != nil {
		return err
	}
	scheduler := application.NewScheduler(store, ids.NewUUID)
	staleness := application.NewStalenessService(store, ids.NewUUID)
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	var loops sync.WaitGroup
	loops.Add(2)
	go func() {
		defer loops.Done()
		runScheduler(ctx, scheduler)
	}()
	go func() {
		defer loops.Done()
		runStaleness(ctx, staleness)
	}()
	defer func() {
		stop()
		loops.Wait()
	}()

	server := &http.Server{
		Addr: *listen, Handler: handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       35 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	errs := make(chan error, 1)
	go func() { errs <- server.ListenAndServe() }()
	select {
	case <-ctx.Done():
	case err := <-errs:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}

func runStaleness(ctx context.Context, staleness *application.StalenessService) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := staleness.MarkDue(ctx, 100); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "staleness tick failed", "error_class", "mark_due")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func runScheduler(ctx context.Context, scheduler *application.Scheduler) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if _, err := scheduler.EnqueueDue(ctx, 100); err != nil && ctx.Err() == nil {
			slog.ErrorContext(ctx, "scheduler tick failed", "error_class", "enqueue_due")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
