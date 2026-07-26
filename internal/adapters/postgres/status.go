package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/araihu/xisnove/application/port"
	dbpostgres "github.com/araihu/xisnove/db/generated/postgres"
	"github.com/araihu/xisnove/domain"
)

type publicStatusUnitOfWork struct {
	db *sql.DB
}

func NewPublicStatusUnitOfWork(db *sql.DB) port.PublicStatusUnitOfWork {
	return &publicStatusUnitOfWork{db: db}
}

func (u *publicStatusUnitOfWork) View(
	ctx context.Context,
	fn func(context.Context, port.PublicStatusRepositories) error,
) error {
	tx, err := u.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return fmt.Errorf("begin public status view: %w", classifyTransactionError(err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	queries := dbpostgres.New(u.db).WithTx(tx)
	if err := fn(ctx, port.PublicStatusRepositories{
		Status:    &publicStatusRepository{queries: queries},
		Retention: &retentionRepository{queries: queries},
	}); err != nil {
		return classifyTransactionError(err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit public status view: %w", classifyTransactionError(err))
	}
	committed = true
	return nil
}

type publicStatusRepository struct {
	queries *dbpostgres.Queries
}

func (r *publicStatusRepository) ListMonitors(ctx context.Context) ([]port.PublicMonitorProjection, error) {
	rows, err := r.queries.ListPublicStatusMonitors(ctx)
	if err != nil {
		return nil, repositoryError("list public status monitors", err)
	}
	result := make([]port.PublicMonitorProjection, 0, len(rows))
	for _, row := range rows {
		mapped, err := mapPublicStatusMonitor(row)
		if err != nil {
			return nil, err
		}
		result = append(result, mapped)
	}
	return result, nil
}

func mapPublicStatusMonitor(row dbpostgres.ListPublicStatusMonitorsRow) (port.PublicMonitorProjection, error) {
	state := domain.HealthState(row.HealthState)
	if !validPublicHealthState(state) {
		return port.PublicMonitorProjection{}, fmt.Errorf("map public status monitor: invalid health state")
	}
	transition := optionalPostgresTime(row.HealthLastTransitionAt)
	projection := port.PublicMonitorProjection{
		ID: domain.MonitorID(row.ID), Name: row.Name, Description: row.Description,
		DisplayOrder: row.DisplayOrder, State: state, LastTransitionAt: transition,
	}
	if row.IncidentID.Valid {
		if !row.IncidentState.Valid || !row.IncidentSeverity.Valid ||
			!row.IncidentOpenedAt.Valid || !row.IncidentLastTransitionAt.Valid {
			return port.PublicMonitorProjection{}, fmt.Errorf("map public status incident: incomplete row")
		}
		projection.ActiveIncident = &domain.Incident{
			ID: domain.IncidentID(row.IncidentID.String), MonitorID: domain.MonitorID(row.ID),
			State:            domain.HealthState(row.IncidentState.String),
			Severity:         domain.IncidentSeverity(row.IncidentSeverity.String),
			OpenedAt:         row.IncidentOpenedAt.Time.UTC(),
			LastTransitionAt: row.IncidentLastTransitionAt.Time.UTC(),
		}
	}
	return projection, nil
}

func optionalPostgresTime(value sql.NullTime) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func validPublicHealthState(state domain.HealthState) bool {
	switch state {
	case domain.HealthPending, domain.HealthUp, domain.HealthDown, domain.HealthDegraded, domain.HealthUnknown:
		return true
	default:
		return false
	}
}
