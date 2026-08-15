-- +goose Up
CREATE TABLE state_ticks (
    id TEXT PRIMARY KEY,
    monitor_id TEXT NOT NULL REFERENCES monitors(id),
    location_id TEXT REFERENCES locations(id) ON DELETE SET NULL,
    lifecycle TEXT NOT NULL CHECK (lifecycle IN ('active', 'paused', 'disabled')),
    health TEXT NOT NULL CHECK (health IN ('pending', 'up', 'down', 'degraded', 'unknown')),
    reason_code TEXT NOT NULL CHECK (reason_code IN (
        'initial', 'probe_success', 'probe_failure', 'probe_timeout',
        'stale_observation', 'agent_disconnected', 'dependency_unknown',
        'dependency_paused', 'monitor_paused', 'location_paused',
        'paused_by_user', 'resumed_by_user', 'maintenance'
    )),
    action_id TEXT NOT NULL,
    user_action_id TEXT,
    actor_kind TEXT NOT NULL CHECK (actor_kind IN ('user', 'system', 'agent')),
    actor_id TEXT,
    occurred_at TEXT NOT NULL,
    observation_id TEXT,
    causal_tick_id TEXT REFERENCES state_ticks(id),
    causal_dependency_id TEXT,
    CHECK (causal_tick_id IS NULL OR causal_tick_id <> id)
);

CREATE INDEX state_ticks_monitor_occurred_at
    ON state_ticks(monitor_id, occurred_at, id);
CREATE INDEX state_ticks_occurred_at
    ON state_ticks(occurred_at, id);

-- +goose Down
DROP INDEX state_ticks_occurred_at;
DROP INDEX state_ticks_monitor_occurred_at;
DROP TABLE state_ticks;
