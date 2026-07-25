package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

const (
	maxDNSObservedValues = 20
	maxDNSObservedBytes  = 4 << 10
)

type DNSExecutor struct {
	policy Policy
}

func NewDNSExecutor(policy Policy) *DNSExecutor {
	return &DNSExecutor{policy: policy.withDefaults()}
}

func (e *DNSExecutor) Execute(
	ctx context.Context,
	work controlplane.ProbeWork,
) controlplane.ProbeResultInput {
	startedAt := time.Now().UTC()
	result := controlplane.ProbeResultInput{
		ResultId: uuid.New(), RunId: work.RunId, LeaseToken: work.LeaseToken,
		StartedAt: startedAt, Outcome: controlplane.Failed,
		ErrorCode: controlplane.ProtocolError,
	}
	timings := controlplane.ProtocolTimings{}
	result.ProtocolTimings = &timings
	finish := func() controlplane.ProbeResultInput {
		result.FinishedAt = time.Now().UTC()
		result.LatencyMillis = max(0, result.FinishedAt.Sub(startedAt).Milliseconds())
		return result
	}
	if work.TimeoutMillis <= 0 {
		result.DiagnosticSample = "invalid timeout"
		return finish()
	}
	kind, err := work.Probe.Discriminator()
	if err != nil || kind != "dns" {
		result.DiagnosticSample = "invalid DNS probe"
		return finish()
	}
	definition, err := work.Probe.AsDNSProbeDefinition()
	recordType, ok := dns.StringToType[string(definition.RecordType)]
	if err != nil || !ok || strings.TrimSpace(definition.Name) == "" {
		result.DiagnosticSample = "invalid DNS probe"
		return finish()
	}

	timeoutCtx, cancel := context.WithTimeout(
		ctx,
		time.Duration(work.TimeoutMillis)*time.Millisecond,
	)
	defer cancel()
	resolverHost, resolverPort, err := resolverAddress(definition.Resolver)
	if err != nil {
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	resolverAddresses, err := e.policy.resolveHost(timeoutCtx, resolverHost)
	if err != nil {
		result.ErrorCode = classifyNetworkError(err)
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	pinnedResolver := net.JoinHostPort(
		resolverAddresses[0].String(),
		strconv.Itoa(int(resolverPort)),
	)

	message := new(dns.Msg)
	message.SetQuestion(dns.Fqdn(definition.Name), recordType)
	dnsStarted := time.Now()
	response, err := exchangeDNS(timeoutCtx, "udp", pinnedResolver, message)
	if err == nil && response.Truncated {
		response, err = exchangeDNS(timeoutCtx, "tcp", pinnedResolver, message)
	}
	dnsMillis := max(int64(0), time.Since(dnsStarted).Milliseconds())
	timings.DnsMillis = &dnsMillis
	if err != nil {
		result.ErrorCode = dnsErrorCode(err)
		result.DiagnosticSample = diagnostic(err.Error())
		return finish()
	}
	if response.Rcode != dns.RcodeSuccess {
		result.ErrorCode = controlplane.DnsError
		result.DiagnosticSample = dns.RcodeToString[response.Rcode]
		return finish()
	}

	values := canonicalDNSValues(response.Answer, recordType)
	slices.Sort(values)
	values = slices.Compact(values)
	observed := boundDNSValues(values)
	result.ObservedValues = &observed
	expected := canonicalExpectedValues(definition.ExpectedValues, recordType)
	if !containsAll(values, expected) {
		result.ErrorCode = controlplane.DnsMismatch
		result.DiagnosticSample = diagnostic(strings.Join(observed, ", "))
		return finish()
	}
	result.Outcome = controlplane.Passed
	result.ErrorCode = controlplane.Empty
	return finish()
}

func resolverAddress(raw string) (string, uint16, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		config, err := dns.ClientConfigFromFile("/etc/resolv.conf")
		if err != nil || len(config.Servers) == 0 {
			return "", 0, errors.New("system DNS resolver is unavailable")
		}
		port, err := strconv.ParseUint(config.Port, 10, 16)
		if err != nil || port == 0 {
			return "", 0, errors.New("system DNS resolver port is invalid")
		}
		return config.Servers[0], uint16(port), nil
	}
	if host, portText, err := net.SplitHostPort(raw); err == nil {
		port, parseErr := strconv.ParseUint(portText, 10, 16)
		if parseErr != nil || port == 0 {
			return "", 0, errors.New("DNS resolver port is invalid")
		}
		return strings.TrimSuffix(host, "."), uint16(port), nil
	}
	if address, err := netip.ParseAddr(strings.Trim(raw, "[]")); err == nil {
		return address.String(), 53, nil
	}
	if strings.Contains(raw, ":") {
		return "", 0, errors.New("DNS resolver address is invalid")
	}
	return strings.TrimSuffix(raw, "."), 53, nil
}

func exchangeDNS(
	ctx context.Context,
	network string,
	resolver string,
	message *dns.Msg,
) (*dns.Msg, error) {
	client := &dns.Client{Net: network}
	response, _, err := client.ExchangeContext(ctx, message, resolver)
	return response, err
}

func canonicalDNSValues(records []dns.RR, recordType uint16) []string {
	values := make([]string, 0, len(records))
	for _, record := range records {
		switch value := record.(type) {
		case *dns.A:
			if recordType == dns.TypeA {
				values = append(values, value.A.String())
			}
		case *dns.AAAA:
			if recordType == dns.TypeAAAA {
				values = append(values, value.AAAA.String())
			}
		case *dns.CNAME:
			if recordType == dns.TypeCNAME {
				values = append(values, canonicalDNSName(value.Target))
			}
		case *dns.MX:
			if recordType == dns.TypeMX {
				values = append(values, fmt.Sprintf(
					"%d %s", value.Preference, canonicalDNSName(value.Mx),
				))
			}
		case *dns.NS:
			if recordType == dns.TypeNS {
				values = append(values, canonicalDNSName(value.Ns))
			}
		case *dns.TXT:
			if recordType == dns.TypeTXT {
				values = append(values, strings.Join(value.Txt, ""))
			}
		case *dns.SRV:
			if recordType == dns.TypeSRV {
				values = append(values, fmt.Sprintf(
					"%d %d %d %s",
					value.Priority, value.Weight, value.Port,
					canonicalDNSName(value.Target),
				))
			}
		}
	}
	return values
}

func canonicalExpectedValues(values []string, recordType uint16) []string {
	canonical := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		switch recordType {
		case dns.TypeA, dns.TypeAAAA:
			if address, err := netip.ParseAddr(value); err == nil {
				value = address.Unmap().String()
			}
		case dns.TypeCNAME, dns.TypeNS:
			value = canonicalDNSName(value)
		case dns.TypeMX:
			value = canonicalStructuredName(value, 1)
		case dns.TypeSRV:
			value = canonicalStructuredName(value, 3)
		}
		canonical = append(canonical, value)
	}
	slices.Sort(canonical)
	return slices.Compact(canonical)
}

func canonicalStructuredName(value string, nameIndex int) string {
	fields := strings.Fields(value)
	if len(fields) > nameIndex {
		fields[nameIndex] = canonicalDNSName(fields[nameIndex])
		return strings.Join(fields, " ")
	}
	return value
}

func canonicalDNSName(value string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
}

func containsAll(values []string, expected []string) bool {
	for _, wanted := range expected {
		if !slices.Contains(values, wanted) {
			return false
		}
	}
	return true
}

func boundDNSValues(values []string) []string {
	bounded := make([]string, 0, min(len(values), maxDNSObservedValues))
	total := 0
	for _, value := range values {
		if len(bounded) == maxDNSObservedValues ||
			total+len(value) > maxDNSObservedBytes {
			break
		}
		bounded = append(bounded, value)
		total += len(value)
	}
	return bounded
}

func dnsErrorCode(err error) controlplane.ProbeResultInputErrorCode {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return controlplane.Timeout
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return controlplane.Timeout
	}
	return controlplane.DnsError
}
