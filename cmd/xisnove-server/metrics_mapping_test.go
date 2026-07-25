package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/observability"
)

func TestApplicationObservationsMapToBoundedMetrics(t *testing.T) {
	metrics := observability.NewMetrics()
	resultObserver(metrics)(application.ResultObservation{Status: application.ResultAccepted, Outcome: application.ProbePassed, Latency: time.Millisecond})
	resultObserver(metrics)(application.ResultObservation{Status: application.ResultDuplicate})
	transitionObserver(metrics)(application.MonitorTransitionObservation{From: domain.HealthUnknown, To: domain.HealthDown})
	leaseObserver(metrics)(application.LeaseObservation{Outcome: application.LeaseExpired})
	deliveryObserver(metrics)(application.DeliveryObservation{
		AttemptOutcome:  application.DeliveryAttemptTransientFailure,
		FinalOutcome:    application.DeliveryFinalRetry,
		DiagnosticClass: application.DeliveryDiagnosticProvider,
	})
	response := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/metrics", nil))
	body := response.Body.String()
	for _, sample := range []string{
		`xisnove_probe_duration_seconds_count{outcome="success"} 1`,
		`xisnove_duplicates_total{kind="result"} 1`,
		`xisnove_monitor_transitions_total{from="unknown",to="failing"} 1`,
		`xisnove_lease_events_total{outcome="expired"} 1`,
		`xisnove_notification_attempts_total{diagnostic_class="provider",outcome="retry"} 1`,
	} {
		if !strings.Contains(body, sample) {
			t.Errorf("metrics output missing %q", sample)
		}
	}
}
