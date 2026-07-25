package application

import (
	"context"
	"regexp"
	"strings"

	"github.com/araihu/xisnove/domain"
)

// MaxTransportDiagnosticBytes bounds operator-visible transport details.
const MaxTransportDiagnosticBytes = 1024

// TransportOutcome is the stable delivery classification consumed by the
// dispatcher.
type TransportOutcome string

const (
	TransportDelivered        TransportOutcome = "delivered"
	TransportTransientFailure TransportOutcome = "transient-failure"
	TransportPermanentFailure TransportOutcome = "permanent-failure"
)

// TransportDelivery contains one call-local, decrypted delivery request.
type TransportDelivery struct {
	DeliveryID    domain.NotificationDeliveryID
	ChannelID     domain.NotificationChannelID
	ChannelKind   domain.NotificationChannelKind
	Configuration []byte
	Title         string
	Message       string
}

// TransportResult describes a provider attempt without exposing provider
// errors or secret material directly.
type TransportResult struct {
	Outcome         TransportOutcome
	ErrorClass      string
	Diagnostic      string
	ProviderReceipt string
}

// NotificationTransport is the provider-neutral delivery boundary. Adapters
// must honor context cancellation and must not retain Configuration, Title, or
// Message after Send returns.
type NotificationTransport interface {
	Send(context.Context, TransportDelivery) TransportResult
}

var (
	transportClassPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	transportURLPattern    = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s]+`)
	transportHeaderPattern = regexp.MustCompile(`(?i)(authorization|proxy-authorization|x-api-key|api-key)[=:][^\r\n]+`)
)

// NewTransportResult normalizes classifications and scrubs all diagnostics.
func NewTransportResult(
	outcome TransportOutcome,
	errorClass string,
	diagnostic string,
	providerReceipt string,
	sensitive ...string,
) TransportResult {
	if outcome != TransportDelivered && outcome != TransportTransientFailure && outcome != TransportPermanentFailure {
		outcome = TransportPermanentFailure
		errorClass = "transport_invalid_result"
	}
	if outcome != TransportDelivered && !transportClassPattern.MatchString(errorClass) {
		outcome = TransportPermanentFailure
		errorClass = "transport_invalid_result"
	}
	if outcome == TransportDelivered {
		errorClass = ""
		diagnostic = ""
	}
	return TransportResult{
		Outcome: outcome, ErrorClass: errorClass,
		Diagnostic:      ScrubTransportDiagnostic(diagnostic, sensitive...),
		ProviderReceipt: ScrubTransportDiagnostic(providerReceipt, sensitive...),
	}
}

// ScrubTransportDiagnostic removes explicit secrets, URLs, and credential
// headers before bounding the result.
func ScrubTransportDiagnostic(value string, sensitive ...string) string {
	for _, secret := range sensitive {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "<redacted>")
		}
	}
	value = transportURLPattern.ReplaceAllString(value, "<redacted-url>")
	value = transportHeaderPattern.ReplaceAllString(value, "$1=<redacted>")
	return boundTransportText(value)
}

func boundTransportText(value string) string {
	value = strings.ToValidUTF8(value, "?")
	if len(value) > MaxTransportDiagnosticBytes {
		value = value[:MaxTransportDiagnosticBytes]
		value = strings.ToValidUTF8(value, "?")
	}
	return value
}
