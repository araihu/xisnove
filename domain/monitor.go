package domain

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
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
	maxProbePayloadBytes       = 4 << 10
	maxDNSExpectedValues       = 20
	maxMonitorDescriptionBytes = 2 << 10
	maxMonitorLabels           = 64
)

var (
	labelNamePattern       = regexp.MustCompile(`^[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?$`)
	labelPrefixPartPattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
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
	Description       string
	Labels            map[string]string
	DisplayOrder      int32
	Public            bool
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
	Description       string
	Labels            map[string]string
	DisplayOrder      int32
	Public            bool
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
	Description       string
	Labels            map[string]string
	DisplayOrder      int32
	Public            bool
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
	Description       string
	Labels            map[string]string
	DisplayOrder      int32
	Public            bool
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
			p.Description,
			p.Labels,
			p.DisplayOrder,
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
		p.Description,
		p.Labels,
		p.DisplayOrder,
		p.Public,
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
		p.Description,
		p.Labels,
		p.DisplayOrder,
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
		p.Description,
		p.Labels,
		p.DisplayOrder,
		p.Public,
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
		p.Description,
		p.Labels,
		p.DisplayOrder,
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
		p.Description,
		p.Labels,
		p.DisplayOrder,
		p.Public,
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

func (m Monitor) MetadataLabels() map[string]string {
	return cloneStringMap(m.Labels)
}

func newMonitor(
	id MonitorID,
	name string,
	description string,
	labels map[string]string,
	displayOrder int32,
	public bool,
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
		Description:       strings.TrimSpace(description),
		Labels:            cloneStringMap(labels),
		DisplayOrder:      displayOrder,
		Public:            public,
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
	description string,
	labels map[string]string,
	displayOrder int32,
	interval time.Duration,
	timeout time.Duration,
	failureThreshold uint16,
	recoveryThreshold uint16,
) bool {
	return id != "" &&
		strings.TrimSpace(name) != "" &&
		len(strings.TrimSpace(description)) <= maxMonitorDescriptionBytes &&
		displayOrder >= 0 &&
		validMonitorLabels(labels) &&
		interval > 0 &&
		timeout > 0 &&
		timeout < interval &&
		failureThreshold > 0 &&
		recoveryThreshold > 0
}

func validMonitorLabels(labels map[string]string) bool {
	if len(labels) > maxMonitorLabels {
		return false
	}
	for key, value := range labels {
		if !validLabelKey(key) || (value != "" && !labelNamePattern.MatchString(value)) {
			return false
		}
	}
	return true
}

func validLabelKey(key string) bool {
	prefix, name, hasPrefix := strings.Cut(key, "/")
	if !hasPrefix {
		return labelNamePattern.MatchString(key)
	}
	if prefix == "" || name == "" || len(prefix) > 253 || !labelNamePattern.MatchString(name) {
		return false
	}
	for _, part := range strings.Split(prefix, ".") {
		if !labelPrefixPartPattern.MatchString(part) {
			return false
		}
	}
	return true
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
