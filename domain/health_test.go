package domain_test

import (
	"testing"
	"time"

	"github.com/araihu/xisnove/domain"
)

func TestApplyProbeUsesFailureAndRecoveryThresholds(t *testing.T) {
	at := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	health := domain.LocationHealth{State: domain.HealthPending}
	for i := 0; i < 2; i++ {
		health = domain.ApplyProbe(
			health,
			domain.ProbeObservation{Passed: false, At: at.Add(time.Duration(i) * time.Minute)},
			domain.Thresholds{Failures: 3, Recoveries: 2},
		)
		if health.State != domain.HealthPending {
			t.Fatalf("failure %d state = %s", i+1, health.State)
		}
	}

	health = domain.ApplyProbe(
		health,
		domain.ProbeObservation{Passed: false, At: at.Add(2 * time.Minute)},
		domain.Thresholds{Failures: 3, Recoveries: 2},
	)
	if health.State != domain.HealthDown {
		t.Fatalf("state = %s", health.State)
	}
	if !health.LastTransitionAt.Equal(at.Add(2 * time.Minute)) {
		t.Fatalf("LastTransitionAt = %v", health.LastTransitionAt)
	}

	health = domain.ApplyProbe(
		health,
		domain.ProbeObservation{Passed: true, At: at.Add(3 * time.Minute)},
		domain.Thresholds{Failures: 3, Recoveries: 2},
	)
	if health.State != domain.HealthDown {
		t.Fatalf("recovered too early: %s", health.State)
	}
	if health.ConsecutiveFailures != 0 || health.ConsecutiveSuccesses != 1 {
		t.Fatalf(
			"counters = failures:%d successes:%d",
			health.ConsecutiveFailures,
			health.ConsecutiveSuccesses,
		)
	}

	health = domain.ApplyProbe(
		health,
		domain.ProbeObservation{Passed: true, At: at.Add(4 * time.Minute)},
		domain.Thresholds{Failures: 3, Recoveries: 2},
	)
	if health.State != domain.HealthUp {
		t.Fatalf("state = %s", health.State)
	}
	if !health.LastObservedAt.Equal(at.Add(4 * time.Minute)) {
		t.Fatalf("LastObservedAt = %v", health.LastObservedAt)
	}
}

func TestRollupRequiredTruthTable(t *testing.T) {
	tests := []struct {
		name   string
		states []domain.HealthState
		want   domain.HealthState
	}{
		{"missing", nil, domain.HealthUnknown},
		{"unknown wins", []domain.HealthState{domain.HealthUp, domain.HealthUnknown}, domain.HealthUnknown},
		{"pending is unknown", []domain.HealthState{domain.HealthPending}, domain.HealthUnknown},
		{"all up", []domain.HealthState{domain.HealthUp, domain.HealthUp}, domain.HealthUp},
		{"all down", []domain.HealthState{domain.HealthDown, domain.HealthDown}, domain.HealthDown},
		{"mixed", []domain.HealthState{domain.HealthUp, domain.HealthDown}, domain.HealthDegraded},
		{"degraded propagates", []domain.HealthState{domain.HealthUp, domain.HealthDegraded}, domain.HealthDegraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			health := make([]domain.LocationHealth, len(tt.states))
			for i, state := range tt.states {
				health[i].State = state
			}
			if got := domain.RollupRequired(health); got != tt.want {
				t.Fatalf("got %s want %s", got, tt.want)
			}
		})
	}
}
