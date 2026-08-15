package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestParseOptionsSupportsPositionalTypeBeforeFlags(t *testing.T) {
	config, err := parseOptions([]string{"http", "--listen", "127.0.0.1:18080", "--body", "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if config.kind != serviceHTTP || config.listen != "127.0.0.1:18080" || config.body != "hello" {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseOptionsSupportsFlagsBeforePositionalType(t *testing.T) {
	config, err := parseOptions([]string{"--listen", "127.0.0.1:19090", "tcp"})
	if err != nil {
		t.Fatal(err)
	}
	if config.kind != serviceTCP || config.listen != "127.0.0.1:19090" {
		t.Fatalf("config = %#v", config)
	}
}

func TestParseOptionsDefaultsEachServiceListenAddress(t *testing.T) {
	for _, test := range []struct {
		kind   string
		listen string
	}{
		{serviceHTTP, defaultHTTPListen},
		{serviceTCP, defaultTCPListen},
		{serviceDNS, defaultDNSListen},
	} {
		t.Run(test.kind, func(t *testing.T) {
			config, err := parseOptions([]string{test.kind})
			if err != nil {
				t.Fatal(err)
			}
			if config.listen != test.listen {
				t.Fatalf("listen = %q, want %q", config.listen, test.listen)
			}
		})
	}
}

func TestParseOptionsRejectsUnsupportedOrAmbiguousTypes(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"smtp"},
		{"http", "--type", "tcp"},
		{"http", "extra"},
	} {
		if _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) succeeded", args)
		}
	}
}

func TestHTTPHandlerServesHealthAndConfiguredBody(t *testing.T) {
	handler := newHTTPHandler("fixture body\n")
	for _, test := range []struct {
		path string
		want string
	}{
		{"/healthz", "ok\n"},
		{"/", "fixture body\n"},
	} {
		t.Run(test.path, func(t *testing.T) {
			record := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "http://service.test"+test.path, nil)
			handler.ServeHTTP(record, request)
			if record.Code != http.StatusOK || record.Body.String() != test.want {
				t.Fatalf("status=%d body=%q", record.Code, record.Body.String())
			}
		})
	}
}

func TestTCPServiceWritesConfiguredResponse(t *testing.T) {
	client, server := net.Pipe()
	done := make(chan struct{})
	go func() {
		serveTCPConnection(server, []byte("PONG"))
		close(done)
	}()
	defer client.Close()

	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PONG" {
		t.Fatalf("response = %q", got)
	}
	<-done
}

func TestBuildDNSRecordSupportsInitialRecordTypes(t *testing.T) {
	for _, test := range []struct {
		recordType string
		value      string
		wantType   uint16
	}{
		{"A", "192.0.2.10", dns.TypeA},
		{"AAAA", "2001:db8::10", dns.TypeAAAA},
		{"CNAME", "target.test.", dns.TypeCNAME},
		{"MX", "10 mail.test.", dns.TypeMX},
		{"NS", "ns.test.", dns.TypeNS},
		{"SRV", "10 20 443 service.test.", dns.TypeSRV},
		{"TXT", "hello world", dns.TypeTXT},
	} {
		t.Run(test.recordType, func(t *testing.T) {
			record, err := buildDNSRecord(options{
				name:        "records.test",
				recordType:  test.recordType,
				recordValue: test.value,
			})
			if err != nil {
				t.Fatal(err)
			}
			if record.Header().Rrtype != test.wantType || !strings.HasPrefix(record.Header().Name, "records.test.") {
				t.Fatalf("record = %s", record)
			}
		})
	}
}

func TestBuildDNSRecordRejectsTypesOutsideInitialScope(t *testing.T) {
	if _, err := buildDNSRecord(options{
		name:        "records.test",
		recordType:  "SOA",
		recordValue: "ns.test. hostmaster.test. 1 2 3 4 5",
	}); err == nil {
		t.Fatal("SOA record unexpectedly accepted")
	}
}

func TestDNSHandlerReturnsConfiguredRecordAndNXDomainForOtherNames(t *testing.T) {
	record, err := buildDNSRecord(options{name: "service.test", recordType: "A", recordValue: "192.0.2.10"})
	if err != nil {
		t.Fatal(err)
	}
	handler := newDNSHandler("service.test", record)

	writer := &captureDNSWriter{}
	request := new(dns.Msg)
	request.SetQuestion("service.test.", dns.TypeA)
	handler.ServeDNS(writer, request)
	if writer.message == nil || len(writer.message.Answer) != 1 || writer.message.Answer[0].Header().Rrtype != dns.TypeA {
		t.Fatalf("response = %#v", writer.message)
	}

	writer = &captureDNSWriter{}
	request.SetQuestion("other.test.", dns.TypeA)
	handler.ServeDNS(writer, request)
	if writer.message == nil || writer.message.Rcode != dns.RcodeNameError {
		t.Fatalf("response = %#v", writer.message)
	}
}

type captureDNSWriter struct {
	message *dns.Msg
}

func (writer *captureDNSWriter) LocalAddr() net.Addr  { return captureDNSAddr("local") }
func (writer *captureDNSWriter) RemoteAddr() net.Addr { return captureDNSAddr("remote") }
func (writer *captureDNSWriter) WriteMsg(message *dns.Msg) error {
	writer.message = message
	return nil
}
func (writer *captureDNSWriter) Write(payload []byte) (int, error) { return len(payload), nil }
func (writer *captureDNSWriter) Close() error                      { return nil }
func (writer *captureDNSWriter) TsigStatus() error                 { return nil }
func (writer *captureDNSWriter) TsigTimersOnly(bool)               {}
func (writer *captureDNSWriter) Hijack()                           {}

type captureDNSAddr string

func (address captureDNSAddr) Network() string { return "capture" }
func (address captureDNSAddr) String() string  { return string(address) }
