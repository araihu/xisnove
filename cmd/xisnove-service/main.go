package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/miekg/dns"
)

const (
	serviceHTTP = "http"
	serviceTCP  = "tcp"
	serviceDNS  = "dns"

	defaultHTTPListen = "0.0.0.0:8080"
	defaultTCPListen  = "0.0.0.0:9090"
	defaultDNSListen  = "0.0.0.0:5353"
	defaultDNSName    = "service.test"
	defaultDNSAddress = "192.0.2.10"
	defaultDNSRecord  = "A"
	defaultDNSTTL     = 60
)

var supportedDNSRecordTypes = map[string]uint16{
	"A":     dns.TypeA,
	"AAAA":  dns.TypeAAAA,
	"CNAME": dns.TypeCNAME,
	"MX":    dns.TypeMX,
	"NS":    dns.TypeNS,
	"SRV":   dns.TypeSRV,
	"TXT":   dns.TypeTXT,
}

type options struct {
	kind        string
	listen      string
	body        string
	response    string
	name        string
	recordType  string
	recordValue string
}

func main() {
	config, err := parseOptions(os.Args[1:])
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, config); err != nil {
		slog.Error("service stopped", "error", err)
		os.Exit(1)
	}
}

func parseOptions(args []string) (options, error) {
	set := flag.NewFlagSet("xisnove-service", flag.ContinueOnError)
	set.SetOutput(os.Stderr)
	set.Usage = func() {
		fmt.Fprintln(set.Output(), "Usage: xisnove-service <http|tcp|dns> [flags]")
		set.PrintDefaults()
	}

	var result options
	positionalKind := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positionalKind = args[0]
		args = args[1:]
	}
	var typeFlag string
	set.StringVar(&typeFlag, "type", "", "service type when it is not the first positional argument")
	set.StringVar(&result.listen, "listen", "", "listen address (defaults by service type)")
	set.StringVar(&result.body, "body", "xisnove HTTP service\n", "HTTP response body")
	set.StringVar(&result.response, "response", "PONG\n", "TCP response payload")
	set.StringVar(&result.name, "name", defaultDNSName, "DNS record name")
	set.StringVar(&result.recordType, "record-type", defaultDNSRecord, "DNS record type (A, AAAA, CNAME, MX, NS, SRV, or TXT)")
	set.StringVar(&result.recordValue, "record-value", defaultDNSAddress, "DNS record value")
	if err := set.Parse(args); err != nil {
		return options{}, err
	}

	if set.NArg() != 0 {
		if positionalKind != "" {
			return options{}, fmt.Errorf("unexpected arguments: %v", set.Args())
		}
		positionalKind = set.Arg(0)
		if set.NArg() > 1 {
			return options{}, fmt.Errorf("unexpected arguments: %v", set.Args()[1:])
		}
	}
	if positionalKind != "" && typeFlag != "" {
		return options{}, errors.New("service type must be supplied either positionally or with --type, not both")
	}
	if typeFlag != "" {
		positionalKind = typeFlag
	}
	result.kind = strings.ToLower(strings.TrimSpace(positionalKind))
	switch result.kind {
	case serviceHTTP:
		if result.listen == "" {
			result.listen = defaultHTTPListen
		}
	case serviceTCP:
		if result.listen == "" {
			result.listen = defaultTCPListen
		}
	case serviceDNS:
		if result.listen == "" {
			result.listen = defaultDNSListen
		}
		result.recordType = strings.ToUpper(strings.TrimSpace(result.recordType))
		if _, err := buildDNSRecord(result); err != nil {
			return options{}, err
		}
	default:
		if result.kind == "" {
			return options{}, errors.New("service type is required (http, tcp, or dns)")
		}
		return options{}, fmt.Errorf("unsupported service type %q (want http, tcp, or dns)", result.kind)
	}
	return result, nil
}

func run(ctx context.Context, config options) error {
	switch config.kind {
	case serviceHTTP:
		return runHTTP(ctx, config)
	case serviceTCP:
		return runTCP(ctx, config)
	case serviceDNS:
		return runDNS(ctx, config)
	default:
		return fmt.Errorf("unsupported service type %q", config.kind)
	}
}

func runHTTP(ctx context.Context, config options) error {
	listener, err := net.Listen("tcp", config.listen)
	if err != nil {
		return fmt.Errorf("listen HTTP service on %q: %w", config.listen, err)
	}
	server := &http.Server{
		Handler:           newHTTPHandler(config.body),
		ReadHeaderTimeout: 5 * time.Second,
	}
	result := make(chan error, 1)
	go func() {
		slog.Info("HTTP service listening", "address", listener.Addr().String())
		result <- server.Serve(listener)
	}()

	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownContext); err != nil {
			return err
		}
		err := <-result
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func newHTTPHandler(body string) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if request.URL.Path == "/healthz" {
			_, _ = writer.Write([]byte("ok\n"))
			return
		}
		_, _ = writer.Write([]byte(body))
	})
}

func runTCP(ctx context.Context, config options) error {
	listener, err := net.Listen("tcp", config.listen)
	if err != nil {
		return fmt.Errorf("listen TCP service on %q: %w", config.listen, err)
	}
	result := make(chan error, 1)
	go func() {
		slog.Info("TCP service listening", "address", listener.Addr().String())
		for {
			connection, err := listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					result <- nil
					return
				}
				result <- err
				return
			}
			go serveTCPConnection(connection, []byte(config.response))
		}
	}()

	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		if err := listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			return err
		}
		return <-result
	}
}

func serveTCPConnection(connection net.Conn, response []byte) {
	defer connection.Close()
	_ = connection.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = connection.Write(response)
}

func runDNS(ctx context.Context, config options) error {
	record, err := buildDNSRecord(config)
	if err != nil {
		return err
	}
	udpConnection, err := net.ListenPacket("udp", config.listen)
	if err != nil {
		return fmt.Errorf("listen DNS UDP service on %q: %w", config.listen, err)
	}
	tcpListener, err := net.Listen("tcp", config.listen)
	if err != nil {
		_ = udpConnection.Close()
		return fmt.Errorf("listen DNS TCP service on %q: %w", config.listen, err)
	}

	handler := newDNSHandler(config.name, record)
	udpServer := &dns.Server{PacketConn: udpConnection, Handler: handler}
	tcpServer := &dns.Server{Listener: tcpListener, Handler: handler}
	result := make(chan error, 2)
	go func() { result <- udpServer.ActivateAndServe() }()
	go func() { result <- tcpServer.ActivateAndServe() }()
	slog.Info("DNS service listening", "udp", udpConnection.LocalAddr().String(), "tcp", tcpListener.Addr().String(), "name", dns.Fqdn(config.name), "record", record.String())

	select {
	case err := <-result:
		_ = udpServer.Shutdown()
		_ = tcpServer.Shutdown()
		if err == nil {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownError := errors.Join(
			udpServer.ShutdownContext(shutdownContext),
			tcpServer.ShutdownContext(shutdownContext),
		)
		first := <-result
		second := <-result
		if shutdownError != nil {
			return shutdownError
		}
		return errors.Join(first, second)
	}
}

func newDNSHandler(name string, record dns.RR) dns.Handler {
	canonicalName := strings.ToLower(dns.Fqdn(name))
	return dns.HandlerFunc(func(writer dns.ResponseWriter, request *dns.Msg) {
		response := new(dns.Msg)
		response.SetReply(request)
		response.Authoritative = true
		if len(request.Question) == 0 {
			response.Rcode = dns.RcodeFormatError
			_ = writer.WriteMsg(response)
			return
		}
		question := request.Question[0]
		if strings.ToLower(question.Name) != canonicalName {
			response.Rcode = dns.RcodeNameError
			_ = writer.WriteMsg(response)
			return
		}
		if question.Qtype == dns.TypeANY || question.Qtype == record.Header().Rrtype {
			response.Answer = []dns.RR{record}
		}
		_ = writer.WriteMsg(response)
	})
}

func buildDNSRecord(config options) (dns.RR, error) {
	name := strings.TrimSpace(config.name)
	if name == "" {
		return nil, errors.New("DNS record name cannot be empty")
	}
	recordType := strings.ToUpper(strings.TrimSpace(config.recordType))
	if _, ok := supportedDNSRecordTypes[recordType]; !ok {
		return nil, fmt.Errorf("unsupported DNS record type %q", recordType)
	}
	value := strings.TrimSpace(config.recordValue)
	if value == "" {
		return nil, errors.New("DNS record value cannot be empty")
	}
	if recordType == "TXT" {
		value = strconv.Quote(config.recordValue)
	}
	raw := fmt.Sprintf("%s %d IN %s %s", dns.Fqdn(name), defaultDNSTTL, recordType, value)
	record, err := dns.NewRR(raw)
	if err != nil {
		return nil, fmt.Errorf("parse DNS record: %w", err)
	}
	return record, nil
}
