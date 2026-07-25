// Package egress provides a DNS- and redirect-aware outbound HTTP policy for
// notification transports.
package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// ErrTargetDenied reports an address or URL rejected by outbound policy.
var ErrTargetDenied = errors.New("egress target denied")

// Resolver is the narrow DNS boundary used for rebinding-safe tests and dials.
type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

// DialContextFunc matches net.Dialer's context-aware dialing method.
type DialContextFunc func(context.Context, string, string) (net.Conn, error)

// Config defines explicit homelab allow ranges, additional deny ranges such as
// Kubernetes service networks, and injectable network primitives.
type Config struct {
	AllowedCIDRs []string
	DeniedCIDRs  []string
	Resolver     Resolver
	DialContext  DialContextFunc
	RoundTripper http.RoundTripper
}

// Policy validates every resolution, dial, request, and redirect target.
type Policy struct {
	allowed      []netip.Prefix
	denied       []netip.Prefix
	resolver     Resolver
	dialContext  DialContextFunc
	roundTripper http.RoundTripper
}

// NewPolicy constructs a default-deny policy for unsafe address classes.
func NewPolicy(config Config) (*Policy, error) {
	allowed, err := parsePrefixes(config.AllowedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse allowed egress CIDRs: %w", err)
	}
	denied, err := parsePrefixes(config.DeniedCIDRs)
	if err != nil {
		return nil, fmt.Errorf("parse denied egress CIDRs: %w", err)
	}
	resolver := config.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	dialContext := config.DialContext
	if dialContext == nil {
		dialer := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		dialContext = dialer.DialContext
	}
	return &Policy{
		allowed: allowed, denied: denied, resolver: resolver,
		dialContext: dialContext, roundTripper: config.RoundTripper,
	}, nil
}

// ValidateURL resolves and validates every address returned for an HTTP target.
func (p *Policy) ValidateURL(ctx context.Context, target *url.URL) error {
	if target == nil || (target.Scheme != "http" && target.Scheme != "https") || target.Hostname() == "" || target.User != nil {
		return fmt.Errorf("%w: only absolute HTTP(S) URLs without userinfo are allowed", ErrTargetDenied)
	}
	addresses, err := p.resolve(ctx, target.Hostname())
	if err != nil {
		return err
	}
	for _, address := range addresses {
		if err := p.validateAddress(address); err != nil {
			return err
		}
	}
	return nil
}

// DialContext re-resolves, revalidates, and connects to the validated IP rather
// than asking a downstream dialer to resolve the hostname again.
func (p *Policy) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("validate egress dial address: %w", err)
	}
	addresses, err := p.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, resolved := range addresses {
		if err := p.validateAddress(resolved); err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve egress target: no addresses")
	}
	return p.dialContext(ctx, network, net.JoinHostPort(addresses[0].String(), port))
}

// HTTPClient constructs a timeout-bound client that applies the policy to the
// initial request and every redirect.
func (p *Policy) HTTPClient(timeout time.Duration) *http.Client {
	base := p.roundTripper
	if base == nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		transport.DialContext = p.DialContext
		transport.MaxIdleConns = 100
		transport.MaxIdleConnsPerHost = 10
		transport.IdleConnTimeout = 90 * time.Second
		transport.TLSHandshakeTimeout = 10 * time.Second
		base = transport
	}
	client := &http.Client{Transport: policyRoundTripper{policy: p, next: base}, Timeout: timeout}
	client.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return errors.New("redirect limit exceeded")
		}
		if err := p.ValidateURL(request.Context(), request.URL); err != nil {
			return fmt.Errorf("redirect target rejected: %w", err)
		}
		return nil
	}
	return client
}

func (p *Policy) resolve(ctx context.Context, host string) ([]netip.Addr, error) {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if literal, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{literal.Unmap()}, nil
	}
	addresses, err := p.resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("resolve egress target: %w", err)
	}
	if len(addresses) == 0 {
		return nil, errors.New("resolve egress target: no addresses")
	}
	result := make([]netip.Addr, len(addresses))
	for index := range addresses {
		result[index] = addresses[index].Unmap()
	}
	return result, nil
}

func (p *Policy) validateAddress(address netip.Addr) error {
	address = address.Unmap()
	for _, prefix := range p.denied {
		if prefix.Contains(address) {
			return fmt.Errorf("%w: address is in a configured deny range", ErrTargetDenied)
		}
	}
	for _, prefix := range p.allowed {
		if prefix.Contains(address) {
			return nil
		}
	}
	if address.IsUnspecified() || address.IsLoopback() || address.IsMulticast() ||
		address.IsPrivate() || address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		netip.MustParsePrefix("100.64.0.0/10").Contains(address) {
		return fmt.Errorf("%w: address class is blocked", ErrTargetDenied)
	}
	return nil
}

func parsePrefixes(values []string) ([]netip.Prefix, error) {
	prefixes := make([]netip.Prefix, len(values))
	for index, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, err
		}
		prefixes[index] = prefix.Masked()
	}
	return prefixes, nil
}

type policyRoundTripper struct {
	policy *Policy
	next   http.RoundTripper
}

func (t policyRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := t.policy.ValidateURL(request.Context(), request.URL); err != nil {
		return nil, err
	}
	return t.next.RoundTrip(request)
}
