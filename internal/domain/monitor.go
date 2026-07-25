package domain

import (
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

var ErrInvalidMonitor = errors.New("invalid monitor")

type MonitorKind string

const (
	MonitorKindHTTP MonitorKind = "http"
	MonitorKindTCP  MonitorKind = "tcp"
	MonitorKindDNS  MonitorKind = "dns"
)

const (
	maxProbePayloadBytes = 4 << 10
	maxDNSExpectedValues = 20
)

type StatusRange struct {
	Min int
	Max int
}

type HTTPProbe struct {
	Method             string
	URL                string
	Headers            map[string]string
	Body               []byte
	ExpectedStatus     []StatusRange
	BodyContains       []string
	BodyDoesNotContain []string
	FollowRedirects    bool
	TLS                *TLSExpectation
}

type TLSExpectation struct {
	MinimumRemaining time.Duration
}

type TCPProbe struct {
	Host   string
	Port   uint16
	Send   []byte
	Expect []byte
	TLS    *TLSExpectation
}

type DNSProbe struct {
	Resolver       string
	Name           string
	RecordType     string
	ExpectedValues []string
}

type ProbeDefinition struct {
	Kind MonitorKind
	HTTP HTTPProbe
	TCP  TCPProbe
	DNS  DNSProbe
}

type NewHTTPMonitorParams struct {
	ID                MonitorID
	Name              string
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	HTTP              HTTPProbe
	CreatedAt         time.Time
}

type NewTCPMonitorParams struct {
	ID                MonitorID
	Name              string
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	TCP               TCPProbe
	CreatedAt         time.Time
}

type NewDNSMonitorParams struct {
	ID                MonitorID
	Name              string
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	DNS               DNSProbe
	CreatedAt         time.Time
}

type Monitor struct {
	ID                MonitorID
	Name              string
	Kind              MonitorKind
	Interval          time.Duration
	Timeout           time.Duration
	FailureThreshold  uint16
	RecoveryThreshold uint16
	HTTP              HTTPProbe
	TCP               TCPProbe
	DNS               DNSProbe
	Enabled           bool
	NextRunAt         time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func NewHTTPMonitor(p NewHTTPMonitorParams) (Monitor, error) {
	parsed, err := url.ParseRequestURI(p.HTTP.URL)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		!validMonitorFields(
			p.ID,
			p.Name,
			p.Interval,
			p.Timeout,
			p.FailureThreshold,
			p.RecoveryThreshold,
		) ||
		len(p.HTTP.Body) > maxProbePayloadBytes ||
		!validTLSExpectation(p.HTTP.TLS) {
		return Monitor{}, ErrInvalidMonitor
	}

	if p.HTTP.Method == "" {
		p.HTTP.Method = http.MethodGet
	}
	for _, status := range p.HTTP.ExpectedStatus {
		if status.Min < 100 || status.Max > 599 || status.Min > status.Max {
			return Monitor{}, ErrInvalidMonitor
		}
	}
	if !validHTTPAssertions(p.HTTP) {
		return Monitor{}, ErrInvalidMonitor
	}

	monitor := newMonitor(
		p.ID,
		p.Name,
		MonitorKindHTTP,
		p.Interval,
		p.Timeout,
		p.FailureThreshold,
		p.RecoveryThreshold,
		p.CreatedAt,
	)
	monitor.HTTP = cloneHTTPProbe(p.HTTP)
	return monitor, nil
}

func NewTCPMonitor(p NewTCPMonitorParams) (Monitor, error) {
	p.TCP.Host = normalizeDNSName(p.TCP.Host)
	if !validMonitorFields(
		p.ID,
		p.Name,
		p.Interval,
		p.Timeout,
		p.FailureThreshold,
		p.RecoveryThreshold,
	) ||
		p.TCP.Host == "" ||
		p.TCP.Port == 0 ||
		len(p.TCP.Send) > maxProbePayloadBytes ||
		len(p.TCP.Expect) > maxProbePayloadBytes ||
		!validTLSExpectation(p.TCP.TLS) {
		return Monitor{}, ErrInvalidMonitor
	}
	monitor := newMonitor(
		p.ID,
		p.Name,
		MonitorKindTCP,
		p.Interval,
		p.Timeout,
		p.FailureThreshold,
		p.RecoveryThreshold,
		p.CreatedAt,
	)
	monitor.TCP = cloneTCPProbe(p.TCP)
	return monitor, nil
}

func NewDNSMonitor(p NewDNSMonitorParams) (Monitor, error) {
	p.DNS.Name = normalizeDNSName(p.DNS.Name)
	p.DNS.Resolver = strings.TrimSpace(p.DNS.Resolver)
	p.DNS.RecordType = strings.ToUpper(strings.TrimSpace(p.DNS.RecordType))
	if !validMonitorFields(
		p.ID,
		p.Name,
		p.Interval,
		p.Timeout,
		p.FailureThreshold,
		p.RecoveryThreshold,
	) ||
		p.DNS.Name == "" ||
		len(p.DNS.Name) > 253 ||
		!validDNSRecordType(p.DNS.RecordType) ||
		len(p.DNS.ExpectedValues) > maxDNSExpectedValues {
		return Monitor{}, ErrInvalidMonitor
	}
	for _, value := range p.DNS.ExpectedValues {
		if strings.TrimSpace(value) == "" {
			return Monitor{}, ErrInvalidMonitor
		}
	}
	monitor := newMonitor(
		p.ID,
		p.Name,
		MonitorKindDNS,
		p.Interval,
		p.Timeout,
		p.FailureThreshold,
		p.RecoveryThreshold,
		p.CreatedAt,
	)
	monitor.DNS = cloneDNSProbe(p.DNS)
	return monitor, nil
}

func (m Monitor) Probe() ProbeDefinition {
	return ProbeDefinition{
		Kind: m.Kind,
		HTTP: cloneHTTPProbe(m.HTTP),
		TCP:  cloneTCPProbe(m.TCP),
		DNS:  cloneDNSProbe(m.DNS),
	}
}

func newMonitor(
	id MonitorID,
	name string,
	kind MonitorKind,
	interval time.Duration,
	timeout time.Duration,
	failureThreshold uint16,
	recoveryThreshold uint16,
	createdAt time.Time,
) Monitor {
	createdAt = createdAt.UTC()
	return Monitor{
		ID:                id,
		Name:              strings.TrimSpace(name),
		Kind:              kind,
		Interval:          interval,
		Timeout:           timeout,
		FailureThreshold:  failureThreshold,
		RecoveryThreshold: recoveryThreshold,
		Enabled:           true,
		NextRunAt:         createdAt,
		CreatedAt:         createdAt,
		UpdatedAt:         createdAt,
	}
}

func validMonitorFields(
	id MonitorID,
	name string,
	interval time.Duration,
	timeout time.Duration,
	failureThreshold uint16,
	recoveryThreshold uint16,
) bool {
	return id != "" &&
		strings.TrimSpace(name) != "" &&
		interval > 0 &&
		timeout > 0 &&
		timeout < interval &&
		failureThreshold > 0 &&
		recoveryThreshold > 0
}

func validTLSExpectation(expectation *TLSExpectation) bool {
	return expectation == nil || expectation.MinimumRemaining > 0
}

func validHTTPAssertions(probe HTTPProbe) bool {
	if len(probe.Headers) > 100 {
		return false
	}
	for name, value := range probe.Headers {
		if strings.TrimSpace(name) == "" || len(name) > 256 || len(value) > maxProbePayloadBytes {
			return false
		}
	}
	for _, values := range [][]string{probe.BodyContains, probe.BodyDoesNotContain} {
		if len(values) > 20 {
			return false
		}
		for _, value := range values {
			if value == "" || len(value) > maxProbePayloadBytes {
				return false
			}
		}
	}
	return true
}

func validDNSRecordType(recordType string) bool {
	switch recordType {
	case "A", "AAAA", "CNAME", "MX", "NS", "TXT", "SRV":
		return true
	default:
		return false
	}
}

func normalizeDNSName(value string) string {
	return strings.TrimSuffix(strings.TrimSpace(value), ".")
}

func cloneHTTPProbe(probe HTTPProbe) HTTPProbe {
	probe.Headers = cloneStringMap(probe.Headers)
	probe.Body = slices.Clone(probe.Body)
	probe.ExpectedStatus = slices.Clone(probe.ExpectedStatus)
	probe.BodyContains = slices.Clone(probe.BodyContains)
	probe.BodyDoesNotContain = slices.Clone(probe.BodyDoesNotContain)
	probe.TLS = cloneTLSExpectation(probe.TLS)
	return probe
}

func cloneTCPProbe(probe TCPProbe) TCPProbe {
	probe.Send = slices.Clone(probe.Send)
	probe.Expect = slices.Clone(probe.Expect)
	probe.TLS = cloneTLSExpectation(probe.TLS)
	return probe
}

func cloneDNSProbe(probe DNSProbe) DNSProbe {
	probe.ExpectedValues = slices.Clone(probe.ExpectedValues)
	return probe
}

func cloneTLSExpectation(expectation *TLSExpectation) *TLSExpectation {
	if expectation == nil {
		return nil
	}
	cloned := *expectation
	return &cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
