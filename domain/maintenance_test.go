package domain_test

import (
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
)

func TestMaintenanceIntervalUsesHalfOpenRangeAndSupportsIndefiniteEnd(t *testing.T) {
	start := time.Date(2026, 7, 25, 12, 0, 0, 0, time.FixedZone("test", -3*60*60))
	end := start.Add(time.Hour)
	interval, err := domain.NewMaintenanceInterval("maintenance-1", "monitor-1", start, &end, " upgrade ")
	if err != nil {
		t.Fatal(err)
	}
	if interval.Reason != "upgrade" || interval.StartsAt.Location() != time.UTC || interval.EndsAt.Location() != time.UTC {
		t.Fatalf("interval = %#v", interval)
	}
	if interval.ActiveAt(start.Add(-time.Nanosecond)) || !interval.ActiveAt(start) || !interval.ActiveAt(end.Add(-time.Nanosecond)) || interval.ActiveAt(end) {
		t.Fatal("one-off interval did not use [start,end) semantics")
	}

	indefinite, err := domain.NewMaintenanceInterval("maintenance-2", "monitor-1", start, nil, "incident response")
	if err != nil {
		t.Fatal(err)
	}
	if !indefinite.ActiveAt(start.Add(365 * 24 * time.Hour)) {
		t.Fatal("indefinite interval stopped")
	}
}

func TestMaintenanceIntervalRejectsInvalidIdentityAndRange(t *testing.T) {
	start := time.Now()
	before := start.Add(-time.Second)
	for name, values := range map[string]struct {
		id, monitor string
		end         *time.Time
	}{
		"empty ID":      {monitor: "monitor-1"},
		"empty monitor": {id: "maintenance-1"},
		"end at start":  {id: "maintenance-1", monitor: "monitor-1", end: &start},
		"end before":    {id: "maintenance-1", monitor: "monitor-1", end: &before},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := domain.NewMaintenanceInterval(domain.MaintenanceID(values.id), domain.MonitorID(values.monitor), start, values.end, "reason")
			if !errors.Is(err, domain.ErrInvalidMaintenance) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestMaintenanceEndedDecisionEmitsOnlyForUnhealthyState(t *testing.T) {
	for _, state := range []domain.HealthState{domain.HealthDown, domain.HealthDegraded, domain.HealthUnknown} {
		if !domain.ShouldNotifyAfterMaintenance(state, false) {
			t.Fatalf("state %q did not emit", state)
		}
	}
	for _, tc := range []struct {
		state   domain.HealthState
		emitted bool
	}{{domain.HealthUp, false}, {domain.HealthPending, false}, {domain.HealthDown, true}} {
		if domain.ShouldNotifyAfterMaintenance(tc.state, tc.emitted) {
			t.Fatalf("state %q emitted=%v unexpectedly emitted", tc.state, tc.emitted)
		}
	}
}
