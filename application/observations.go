package application

import (
	"strings"
	"time"

	"github.com/araihu/xisnove/domain"
)

// ResultObservation describes one durably accepted or idempotently ignored
// probe submission. Duplicate observations deliberately omit command outcome
// and latency because the submitted values were not persisted.
type ResultObservation struct {
	Status   ResultStatus
	Outcome  ProbeOutcome
	Latency  time.Duration
	TimedOut bool
}

// MonitorTransitionObservation describes a committed aggregate health change.
type MonitorTransitionObservation struct {
	From domain.HealthState
	To   domain.HealthState
}

type LeaseOutcome string

const (
	LeaseClaimed LeaseOutcome = "claimed"
	LeaseNoWork  LeaseOutcome = "no_work"
	// LeaseExpired means a previously expired probe lease was reclaimed.
	LeaseExpired LeaseOutcome = "expired"
)

// LeaseObservation describes the terminal outcome of one LeaseProbe call.
type LeaseObservation struct {
	Outcome LeaseOutcome
}

type DeliveryAttemptOutcome string

const (
	DeliveryAttemptDelivered        DeliveryAttemptOutcome = "delivered"
	DeliveryAttemptTransientFailure DeliveryAttemptOutcome = "transient_failure"
	DeliveryAttemptPermanentFailure DeliveryAttemptOutcome = "permanent_failure"
)

type DeliveryFinalOutcome string

const (
	DeliveryFinalDelivered DeliveryFinalOutcome = "delivered"
	DeliveryFinalRetry     DeliveryFinalOutcome = "retry"
	DeliveryFinalPermanent DeliveryFinalOutcome = "permanent"
)

type DeliveryDiagnosticClass string

const (
	DeliveryDiagnosticNone      DeliveryDiagnosticClass = "none"
	DeliveryDiagnosticTimeout   DeliveryDiagnosticClass = "timeout"
	DeliveryDiagnosticTransport DeliveryDiagnosticClass = "transport"
	DeliveryDiagnosticProvider  DeliveryDiagnosticClass = "provider"
	DeliveryDiagnosticPolicy    DeliveryDiagnosticClass = "policy"
	DeliveryDiagnosticInternal  DeliveryDiagnosticClass = "internal"
)

// DeliveryObservation contains only bounded outcomes. Provider diagnostics,
// receipts, decrypted configuration, rendered content, and identifiers are
// intentionally excluded. Observers may be called concurrently.
type DeliveryObservation struct {
	AttemptOutcome  DeliveryAttemptOutcome
	FinalOutcome    DeliveryFinalOutcome
	DiagnosticClass DeliveryDiagnosticClass
}

func deliveryDiagnosticClass(errorClass string) DeliveryDiagnosticClass {
	switch {
	case errorClass == "":
		return DeliveryDiagnosticNone
	case errorClass == "deadline_exceeded" || errorClass == "context_canceled":
		return DeliveryDiagnosticTimeout
	case strings.HasPrefix(errorClass, "provider_"):
		return DeliveryDiagnosticProvider
	case strings.HasPrefix(errorClass, "transport_"):
		return DeliveryDiagnosticTransport
	case errorClass == "channel_disabled" ||
		errorClass == "channel_kind_changed" ||
		errorClass == "configuration_invalid" ||
		errorClass == "configuration_unavailable" ||
		errorClass == "scheme_not_reviewed" ||
		errorClass == "template_invalid":
		return DeliveryDiagnosticPolicy
	default:
		return DeliveryDiagnosticInternal
	}
}
