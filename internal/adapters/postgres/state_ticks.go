package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	port "github.com/araihu/xisnove/application/port"
	dbpostgres "github.com/araihu/xisnove/db/generated/postgres"
	"github.com/araihu/xisnove/domain"
)

const (
	defaultStateTickHistoryLimit = 4096
	maxStateTickHistoryLimit     = 10000
)

type stateTickRepository struct {
	queries *dbpostgres.Queries
}

func (r *stateTickRepository) AppendStateTick(ctx context.Context, tick domain.StateTick) (bool, error) {
	if err := tick.Validate(); err != nil {
		return false, fmt.Errorf("append state tick: %w", err)
	}
	occurredAtUnixNanos, err := stateTickUnixNanos(tick.OccurredAt)
	if err != nil {
		return false, fmt.Errorf("append state tick: %w", err)
	}
	count, err := r.queries.AppendStateTick(ctx, dbpostgres.AppendStateTickParams{
		ID:                  tick.ID,
		MonitorID:           string(tick.MonitorID),
		LocationID:          nullableStateTickLocationID(tick.LocationID),
		Lifecycle:           string(tick.Lifecycle),
		Health:              string(tick.Health),
		ReasonCode:          string(tick.ReasonCode),
		ActionID:            tick.ActionID,
		UserActionID:        nullableStateTickPointer(tick.UserActionID),
		ActorKind:           string(tick.Actor.Kind),
		ActorID:             nullableStateTickID(tick.Actor.ID),
		OccurredAt:          formatTime(tick.OccurredAt),
		OccurredAtUnixNanos: occurredAtUnixNanos,
		ObservationID:       nullableStateTickPointer(tick.ObservationID),
		CausalTickID:        nullableStateTickPointer(tick.CausalTickID),
		CausalDependencyID:  nullableStateTickPointer(tick.CausalDependencyID),
	})
	if err != nil {
		return false, repositoryError("append state tick", err)
	}
	return count == 1, nil
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
	startsAtUnixNanos, err := stateTickUnixNanos(start)
	if err != nil {
		return nil, fmt.Errorf("list state ticks: %w", err)
	}
	endsAtUnixNanos, err := stateTickUnixNanos(end)
	if err != nil {
		return nil, fmt.Errorf("list state ticks: %w", err)
	}
	records, err := r.queries.ListStateTicks(ctx, dbpostgres.ListStateTicksParams{
		MonitorID: string(monitorID),
		StartsAt:  startsAtUnixNanos,
		EndsAt:    endsAtUnixNanos,
		RowLimit:  int32(normalizeStateTickHistoryLimit(limit)),
	})
	if err != nil {
		return nil, repositoryError("list state ticks", err)
	}
	ticks := make([]domain.StateTick, 0, len(records))
	// Storage selects newest rows first so a bounded query retains the latest
	// history. Expose the repository contract chronologically.
	for index := len(records) - 1; index >= 0; index-- {
		record := records[index]
		occurredAt := stateTickTimeFromUnixNanos(record.OccurredAtUnixNanos)
		if record.MonitorID != string(monitorID) || occurredAt.Before(start) || !occurredAt.Before(end) {
			return nil, errors.New("list state ticks: query returned a row outside the requested scope")
		}
		tick, err := mapPostgresStateTick(record, occurredAt)
		if err != nil {
			return nil, err
		}
		ticks = append(ticks, tick)
	}
	return ticks, nil
}

func stateTickUnixNanos(value time.Time) (int64, error) {
	const (
		maxUnixSeconds        int64 = 9223372036
		maxUnixNanosRemainder int64 = 854775807
	)
	value = value.UTC()
	seconds := value.Unix()
	nanos := int64(value.Nanosecond())
	if seconds < -maxUnixSeconds || seconds > maxUnixSeconds ||
		(seconds == maxUnixSeconds && nanos > maxUnixNanosRemainder) {
		return 0, errors.New("timestamp is outside the PostgreSQL nanosecond range")
	}
	return seconds*1_000_000_000 + nanos, nil
}

func stateTickTimeFromUnixNanos(value int64) time.Time {
	return time.Unix(0, value).UTC()
}

func mapPostgresStateTick(record dbpostgres.StateTick, occurredAt time.Time) (domain.StateTick, error) {
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
		UserActionID:       nullablePostgresStateTickString(record.UserActionID),
		Actor:              domain.StateTickActor{Kind: domain.StateTickActorKind(record.ActorKind), ID: nullablePostgresStateTickValue(record.ActorID)},
		OccurredAt:         occurredAt,
		ObservationID:      nullablePostgresStateTickString(record.ObservationID),
		CausalTickID:       nullablePostgresStateTickString(record.CausalTickID),
		CausalDependencyID: nullablePostgresStateTickString(record.CausalDependencyID),
	})
	if err != nil {
		return domain.StateTick{}, fmt.Errorf("map state tick %q: %w", record.ID, err)
	}
	return tick, nil
}

func nullableStateTickLocationID(value *domain.LocationID) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return nullableStateTickID(string(*value))
}

func nullableStateTickPointer(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return nullableStateTickID(*value)
}

func nullableStateTickID(value string) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: value, Valid: true}
}

func nullablePostgresStateTickString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func nullablePostgresStateTickValue(value sql.NullString) string {
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
var _ port.StateTickWriter = (*stateTickRepository)(nil)
