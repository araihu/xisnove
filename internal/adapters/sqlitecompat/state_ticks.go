package sqlitecompat

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	port "github.com/araihu/xisnove/application/port"
	dbsqlite "github.com/araihu/xisnove/db/generated/sqlite"
	"github.com/araihu/xisnove/domain"
)

const (
	defaultStateTickHistoryLimit = 4096
	maxStateTickHistoryLimit     = 10000
)

type stateTickRepository struct {
	queries *dbsqlite.Queries
}

func (r *stateTickRepository) ListStateTicks(
	ctx context.Context,
	monitorID domain.MonitorID,
	start time.Time,
	end time.Time,
	limit int,
) ([]domain.StateTick, error) {
	start = start.UTC()
	end = end.UTC()
	if end.Before(start) {
		return nil, errors.New("list state ticks: end precedes start")
	}
	records, err := r.queries.ListStateTicks(ctx, dbsqlite.ListStateTicksParams{
		MonitorID: string(monitorID),
		StartsAt:  formatTime(start),
		EndsAt:    formatTime(end),
		RowLimit:  int64(normalizeStateTickHistoryLimit(limit)),
	})
	if err != nil {
		return nil, repositoryError("list state ticks", err)
	}
	ticks := make([]domain.StateTick, 0, len(records))
	for _, record := range records {
		occurredAt, err := parseTime(record.OccurredAt)
		if err != nil {
			return nil, fmt.Errorf("map state tick timestamp: %w", err)
		}
		if record.MonitorID != string(monitorID) || occurredAt.Before(start) || !occurredAt.Before(end) {
			return nil, errors.New("list state ticks: query returned a row outside the requested scope")
		}
		tick, err := mapSQLiteStateTick(record, occurredAt)
		if err != nil {
			return nil, err
		}
		ticks = append(ticks, tick)
	}
	return ticks, nil
}

func mapSQLiteStateTick(record dbsqlite.StateTick, occurredAt time.Time) (domain.StateTick, error) {
	var locationID *domain.LocationID
	if record.LocationID.Valid {
		value := domain.LocationID(record.LocationID.String)
		locationID = &value
	}
	tick, err := domain.NewStateTick(domain.NewStateTickParams{
		ID:                 record.ID,
		MonitorID:          domain.MonitorID(record.MonitorID),
		LocationID:         locationID,
		Lifecycle:          domain.MonitorLifecycle(record.Lifecycle),
		Health:             domain.HealthState(record.Health),
		ReasonCode:         domain.StateTickReasonCode(record.ReasonCode),
		ActionID:           record.ActionID,
		UserActionID:       nullableStateTickString(record.UserActionID),
		Actor:              domain.StateTickActor{Kind: domain.StateTickActorKind(record.ActorKind), ID: nullableStateTickValue(record.ActorID)},
		OccurredAt:         occurredAt,
		ObservationID:      nullableStateTickString(record.ObservationID),
		CausalTickID:       nullableStateTickString(record.CausalTickID),
		CausalDependencyID: nullableStateTickString(record.CausalDependencyID),
	})
	if err != nil {
		return domain.StateTick{}, fmt.Errorf("map state tick %q: %w", record.ID, err)
	}
	return tick, nil
}

func nullableStateTickString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullableStateTickValue(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func normalizeStateTickHistoryLimit(limit int) int {
	if limit <= 0 {
		return defaultStateTickHistoryLimit
	}
	if limit > maxStateTickHistoryLimit+1 {
		return maxStateTickHistoryLimit + 1
	}
	return limit
}

var _ port.StateTickRepository = (*stateTickRepository)(nil)
