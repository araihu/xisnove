package sqlitecompat

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
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
	tx, err := u.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin public status view: %w", classifyTransactionError(err))
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	queries := dbsqlite.New(u.db).WithTx(tx)
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
	queries *dbsqlite.Queries
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

func mapPublicStatusMonitor(row dbsqlite.ListPublicStatusMonitorsRow) (port.PublicMonitorProjection, error) {
	if row.DisplayOrder < 0 || row.DisplayOrder > math.MaxInt32 {
		return port.PublicMonitorProjection{}, fmt.Errorf("map public status monitor: invalid display order")
	}
	state := domain.HealthState(row.HealthState)
	if !validPublicHealthState(state) {
		return port.PublicMonitorProjection{}, fmt.Errorf("map public status monitor: invalid health state")
	}
	transition, err := parseOptionalSQLiteTime(row.HealthLastTransitionAt)
	if err != nil {
		return port.PublicMonitorProjection{}, fmt.Errorf("map public status health transition: %w", err)
	}
	projection := port.PublicMonitorProjection{
		ID: domain.MonitorID(row.ID), Name: row.Name, Description: row.Description,
		DisplayOrder: int32(row.DisplayOrder), State: state, LastTransitionAt: transition,
	}
	if row.IncidentID.Valid {
		openedAt, err := requiredSQLiteTime(row.IncidentOpenedAt)
		if err != nil {
			return port.PublicMonitorProjection{}, fmt.Errorf("map public status incident opening: %w", err)
		}
		lastTransitionAt, err := requiredSQLiteTime(row.IncidentLastTransitionAt)
		if err != nil {
			return port.PublicMonitorProjection{}, fmt.Errorf("map public status incident transition: %w", err)
		}
		if !row.IncidentState.Valid || !row.IncidentSeverity.Valid {
			return port.PublicMonitorProjection{}, fmt.Errorf("map public status incident: incomplete row")
		}
		projection.ActiveIncident = &domain.Incident{
			ID: domain.IncidentID(row.IncidentID.String), MonitorID: domain.MonitorID(row.ID),
			State:    domain.HealthState(row.IncidentState.String),
			Severity: domain.IncidentSeverity(row.IncidentSeverity.String),
			OpenedAt: openedAt, LastTransitionAt: lastTransitionAt,
		}
	}
	return projection, nil
}

func parseOptionalSQLiteTime(value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, nil
	}
	return parseTime(value.String)
}

func requiredSQLiteTime(value sql.NullString) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, fmt.Errorf("missing time")
	}
	return parseTime(value.String)
}

func validPublicHealthState(state domain.HealthState) bool {
	switch state {
	case domain.HealthPending, domain.HealthUp, domain.HealthDown, domain.HealthDegraded, domain.HealthUnknown:
		return true
	default:
		return false
	}
}
