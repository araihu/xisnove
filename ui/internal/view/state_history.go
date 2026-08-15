package view

import (
	"sort"
	"strings"
	"time"

	"github.com/araihu/xisnove/sdk"
)

const (
	stateHistoryLookback = 3 * time.Hour
	stateHistoryMaxTicks = 10000
)

// stateHistoryFor returns the selected monitor's immutable StateTick stream
// after applying the UI's same bounded window as the availability chart.
// The BFF remains responsible for fetching it; this guard prevents a stale or
// over-wide response from expanding the drawer surface.
func stateHistoryFor(data MonitorList, monitor sdk.Monitor) sdk.MonitorStateHistory {
	history, ok := data.StateHistory[monitor.Id.String()]
	if !ok {
		return sdk.MonitorStateHistory{}
	}
	return boundedStateHistory(history)
}

func stateHistoryErrorFor(data MonitorList, monitor sdk.Monitor) string {
	return strings.TrimSpace(data.StateHistoryErrors[monitor.Id.String()])
}

func stateHistoryStatus(history sdk.MonitorStateHistory, historyError string) string {
	if strings.TrimSpace(historyError) != "" {
		return "error"
	}
	if len(history.Ticks) == 0 {
		return "empty"
	}
	return "ready"
}

func boundedStateHistory(history sdk.MonitorStateHistory) sdk.MonitorStateHistory {
	if len(history.Ticks) == 0 {
		return history
	}

	end := history.EndsAt.UTC()
	derivedEnd := end.IsZero() && history.GeneratedAt.IsZero()
	if end.IsZero() {
		end = history.GeneratedAt.UTC()
	}
	if end.IsZero() {
		end = history.Ticks[0].OccurredAt.UTC()
		for _, tick := range history.Ticks[1:] {
			if tick.OccurredAt.After(end) {
				end = tick.OccurredAt.UTC()
			}
		}
	}
	if end.IsZero() {
		end = time.Now().UTC()
	}
	if derivedEnd {
		// A hand-built/fake response may omit its envelope. Keep the newest
		// tick visible while retaining the half-open comparison below.
		end = end.Add(time.Nanosecond)
	}

	start := end.Add(-stateHistoryLookback)
	if candidate := history.StartsAt.UTC(); candidate.After(start) && candidate.Before(end) {
		start = candidate
	}

	ticks := make([]sdk.MonitorStateTick, 0, len(history.Ticks))
	for _, tick := range history.Ticks {
		occurredAt := tick.OccurredAt.UTC()
		if occurredAt.Before(start) || !occurredAt.Before(end) {
			continue
		}
		tick.OccurredAt = occurredAt
		ticks = append(ticks, tick)
	}
	sort.SliceStable(ticks, func(i, j int) bool {
		return ticks[i].OccurredAt.Before(ticks[j].OccurredAt)
	})
	if len(ticks) > stateHistoryMaxTicks {
		ticks = ticks[len(ticks)-stateHistoryMaxTicks:]
	}
	history.StartsAt = start
	history.EndsAt = end
	history.Ticks = ticks
	return history
}

func latestStateTick(history sdk.MonitorStateHistory) (sdk.MonitorStateTick, bool) {
	if len(history.Ticks) == 0 {
		return sdk.MonitorStateTick{}, false
	}
	return history.Ticks[len(history.Ticks)-1], true
}

func monitorLifecycle(monitor sdk.Monitor, history sdk.MonitorStateHistory) sdk.MonitorLifecycle {
	if tick, ok := latestStateTick(history); ok && tick.Lifecycle != "" {
		return tick.Lifecycle
	}
	if !monitor.Enabled {
		return sdk.Disabled
	}
	return sdk.Active
}

func lifecycleLabel(lifecycle sdk.MonitorLifecycle) string {
	switch lifecycle {
	case sdk.Active:
		return "Active"
	case sdk.Paused:
		return "Paused"
	case sdk.Disabled:
		return "Disabled"
	default:
		return "Unknown"
	}
}

func healthStateValue(state sdk.HealthState) sdk.HealthState {
	if state == "" {
		return sdk.Unknown
	}
	return state
}

func healthStateLabel(state sdk.HealthState) string {
	return strings.ToUpper(string(healthStateValue(state)))
}

func stateTickHealth(state sdk.HealthState) sdk.HealthState {
	return healthStateValue(state)
}

func stateTickProvenance(tick sdk.MonitorStateTick) string {
	actor := strings.ToLower(strings.TrimSpace(string(tick.Actor.Kind)))
	if actor == "" {
		actor = "unknown actor"
	}
	parts := []string{"Actor: " + actor}
	if tick.ActionId != (sdk.MonitorID{}) {
		parts = append(parts, "Action recorded")
	}
	if tick.Actor.Id != nil {
		parts = append(parts, "Actor identity recorded")
	}
	if tick.UserActionId != nil {
		parts = append(parts, "User action linked")
	}
	if tick.ObservationId != nil {
		parts = append(parts, "Observation linked")
	}
	if tick.CausalTickId != nil {
		parts = append(parts, "Causal state tick linked")
	}
	if tick.CausalDependencyId != nil {
		parts = append(parts, "Causal dependency linked")
	}
	return strings.Join(parts, " · ")
}

func stateTickTime(tick sdk.MonitorStateTick) string {
	return formattedTime(tick.OccurredAt)
}
