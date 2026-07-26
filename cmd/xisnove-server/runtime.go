package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/internal/adapters/observability"
)

func newOperatorService(store application.UnitOfWork, credentials application.CredentialHasher) application.OperatorService {
	return application.OperatorService{Store: store, Credentials: credentials}
}

func runLifecycleLoop(
	claims context.Context,
	lifecycle *serverLifecycle,
	tracing *observability.Tracing,
	interval time.Duration,
	operation string,
	cycle func(context.Context) error,
	onResult func(context.Context, error),
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-claims.Done():
			return
		default:
		}
		workCtx, release, err := lifecycle.Admit(context.Background())
		if err != nil {
			return
		}
		workCtx, span := tracing.StartWorker(workCtx, "github.com/araihu/xisnove/workers", operation)
		err = cycle(workCtx)
		if onResult != nil {
			onResult(workCtx, err)
		}
		span.End()
		release()
		select {
		case <-claims.Done():
			return
		case <-ticker.C:
		}
	}
}

func runDatabaseMetrics(ctx context.Context, db *sql.DB, metrics *observability.Metrics) {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		stats := db.Stats()
		metrics.SetPool(observability.PoolDatabase, observability.PoolIdle, float64(stats.Idle))
		metrics.SetPool(observability.PoolDatabase, observability.PoolInUse, float64(stats.InUse))
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func logCycleResult(message, class string) func(context.Context, error) {
	return func(ctx context.Context, err error) {
		if err != nil && !errors.Is(err, context.Canceled) {
			slog.ErrorContext(ctx, message, "error_class", class, "error", err)
		}
	}
}
