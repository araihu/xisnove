package main

import (
	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/domain"
	"github.com/araihu/xisnove/internal/adapters/observability"
)

func resultObserver(metrics *observability.Metrics) func(application.ResultObservation) {
	return func(observation application.ResultObservation) {
		if observation.Status == application.ResultDuplicate {
			metrics.ObserveDuplicate(observability.DuplicateResult)
			return
		}
		outcome := observability.ProbeFailure
		if observation.TimedOut {
			outcome = observability.ProbeTimeout
		} else if observation.Outcome == application.ProbePassed {
			outcome = observability.ProbeSuccess
		}
		metrics.ObserveProbe(outcome, observation.Latency)
	}
}

func transitionObserver(metrics *observability.Metrics) func(application.MonitorTransitionObservation) {
	return func(observation application.MonitorTransitionObservation) {
		metrics.ObserveTransition(metricMonitorState(observation.From), metricMonitorState(observation.To))
	}
}

func leaseObserver(metrics *observability.Metrics) func(application.LeaseObservation) {
	return func(observation application.LeaseObservation) {
		outcome := observability.LeaseAvailable
		switch observation.Outcome {
		case application.LeaseClaimed:
			outcome = observability.LeaseClaimed
		case application.LeaseExpired:
			outcome = observability.LeaseExpired
		}
		metrics.ObserveLeaseEvent(outcome)
	}
}

func deliveryObserver(metrics *observability.Metrics) func(application.DeliveryObservation) {
	return func(observation application.DeliveryObservation) {
		outcome := observability.AttemptPermanent
		switch observation.AttemptOutcome {
		case application.DeliveryAttemptDelivered:
			outcome = observability.AttemptDelivered
		case application.DeliveryAttemptTransientFailure:
			outcome = observability.AttemptRetry
		}
		class := observability.DiagnosticInternal
		switch observation.DiagnosticClass {
		case application.DeliveryDiagnosticNone:
			class = observability.DiagnosticNone
		case application.DeliveryDiagnosticTimeout:
			class = observability.DiagnosticTimeout
		case application.DeliveryDiagnosticTransport:
			class = observability.DiagnosticTransport
		case application.DeliveryDiagnosticProvider:
			class = observability.DiagnosticProvider
		case application.DeliveryDiagnosticPolicy:
			class = observability.DiagnosticPolicy
		}
		metrics.ObserveAttempt(outcome, class)
	}
}

func metricMonitorState(state domain.HealthState) observability.MonitorState {
	switch state {
	case domain.HealthUp:
		return observability.MonitorPassing
	case domain.HealthDown, domain.HealthDegraded:
		return observability.MonitorFailing
	default:
		return observability.MonitorUnknown
	}
}
