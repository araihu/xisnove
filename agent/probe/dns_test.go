package probe_test

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/miekg/dns"

	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
)

func TestDNSExecutorNormalizesSupportedRecords(t *testing.T) {
	resolver, _ := serveDNS(t)
	tests := []struct {
		recordType string
		expected   string
	}{
		{"A", "192.0.2.10"},
		{"AAAA", "2001:db8::1"},
		{"CNAME", "target.test"},
		{"MX", "10 mail.test"},
		{"NS", "ns1.test"},
		{"TXT", "hello world"},
		{"SRV", "10 20 443 service.test"},
	}
	executor := probe.NewDNSExecutor(dnsLoopbackPolicy())
	for _, test := range tests {
		t.Run(test.recordType, func(t *testing.T) {
			result := executor.Execute(
				context.Background(),
				dnsWork(resolver, "records.test", test.recordType, []string{test.expected}),
			)
			if result.Outcome != controlplane.Passed ||
				result.ErrorCode != controlplane.Empty ||
				result.ObservedValues == nil ||
				!slices.Contains(*result.ObservedValues, test.expected) {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}

func TestDNSExecutorRetriesTruncatedUDPOverTCP(t *testing.T) {
	resolver, tcpRequests := serveDNS(t)
	result := probe.NewDNSExecutor(dnsLoopbackPolicy()).Execute(
		context.Background(),
		dnsWork(resolver, "truncated.test", "A", []string{"192.0.2.10"}),
	)
	if result.Outcome != controlplane.Passed || tcpRequests.Load() != 1 {
		t.Fatalf("result = %#v tcpRequests=%d", result, tcpRequests.Load())
	}
}

func TestDNSExecutorReportsSortedMismatch(t *testing.T) {
	resolver, _ := serveDNS(t)
	result := probe.NewDNSExecutor(dnsLoopbackPolicy()).Execute(
		context.Background(),
		dnsWork(resolver, "records.test", "A", []string{"192.0.2.99"}),
	)
	if result.ErrorCode != controlplane.DnsMismatch ||
		result.ObservedValues == nil ||
		len(*result.ObservedValues) != 1 {
		t.Fatalf("result = %#v", result)
	}
}

func TestDNSExecutorDeniesUnlistedPrivateResolver(t *testing.T) {
	resolver, _ := serveDNS(t)
	result := probe.NewDNSExecutor(probe.DefaultPolicy()).Execute(
		context.Background(),
		dnsWork(resolver, "records.test", "A", nil),
	)
	if result.ErrorCode != controlplane.TargetDenied {
		t.Fatalf("result = %#v", result)
	}
}

func dnsLoopbackPolicy() probe.Policy {
	policy := probe.DefaultPolicy()
	policy.AllowedPrivate = []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
	return policy
}

func dnsWork(
	resolver string,
	name string,
	recordType string,
	expected []string,
) controlplane.ProbeWork {
	var definition controlplane.ProbeDefinition
	if err := definition.FromDNSProbeDefinition(controlplane.DNSProbeDefinition{
		Resolver: resolver, Name: name,
		RecordType:     controlplane.DNSProbeDefinitionRecordType(recordType),
		ExpectedValues: append([]string{}, expected...),
	}); err != nil {
		panic(err)
	}
	return controlplane.ProbeWork{
		RunId:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		MonitorId:  uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		LeaseToken: "lease-token", ScheduledFor: time.Now().UTC(),
		TimeoutMillis: 1000, Probe: definition,
	}
}

func serveDNS(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := udp.LocalAddr().(*net.UDPAddr).Port
	tcp, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		_ = udp.Close()
		t.Fatal(err)
	}
	var tcpRequests atomic.Int32
	handler := dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		question := request.Question[0]
		if question.Name == "truncated.test." &&
			writer.LocalAddr().Network() == "udp" {
			response.Truncated = true
			_ = writer.WriteMsg(response)
			return
		}
		if writer.LocalAddr().Network() == "tcp" {
			tcpRequests.Add(1)
		}
		var raw string
		switch question.Qtype {
		case dns.TypeA:
			raw = question.Name + " 60 IN A 192.0.2.10"
		case dns.TypeAAAA:
			raw = question.Name + " 60 IN AAAA 2001:db8::1"
		case dns.TypeCNAME:
			raw = question.Name + " 60 IN CNAME target.test."
		case dns.TypeMX:
			raw = question.Name + " 60 IN MX 10 mail.test."
		case dns.TypeNS:
			raw = question.Name + " 60 IN NS ns1.test."
		case dns.TypeTXT:
			raw = question.Name + ` 60 IN TXT "hello " "world"`
		case dns.TypeSRV:
			raw = question.Name + " 60 IN SRV 10 20 443 service.test."
		}
		record, parseErr := dns.NewRR(raw)
		if parseErr == nil {
			response.Answer = []dns.RR{record}
		}
		_ = writer.WriteMsg(response)
	})
	udpServer := &dns.Server{PacketConn: udp, Handler: handler}
	tcpServer := &dns.Server{Listener: tcp, Handler: handler}
	go func() { _ = udpServer.ActivateAndServe() }()
	go func() { _ = tcpServer.ActivateAndServe() }()
	t.Cleanup(func() {
		_ = udpServer.Shutdown()
		_ = tcpServer.Shutdown()
	})
	return fmt.Sprintf("127.0.0.1:%d", port), &tcpRequests
}
