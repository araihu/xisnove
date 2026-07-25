package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"
)

var (
	ErrTargetDenied = errors.New("target denied")
	ErrDNS          = errors.New("DNS resolution failed")
)

type Resolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

type Policy struct {
	AllowedPrivate   []netip.Prefix
	MaxResponseBytes int64
	MaxRedirects     int
	Resolver         Resolver
}

func DefaultPolicy() Policy {
	return Policy{
		MaxResponseBytes: 64 << 10,
		MaxRedirects:     3,
		Resolver:         net.DefaultResolver,
	}
}

func (p Policy) ValidateTarget(ctx context.Context, rawURL string) (netip.Addr, error) {
	target, err := url.Parse(rawURL)
	if err != nil {
		return netip.Addr{}, fmt.Errorf("%w: malformed URL", ErrTargetDenied)
	}
	if err := validateURL(target); err != nil {
		return netip.Addr{}, err
	}
	addresses, err := p.resolveHost(ctx, target.Hostname())
	if err != nil {
		return netip.Addr{}, err
	}
	return addresses[0], nil
}

func (p Policy) withDefaults() Policy {
	if p.MaxResponseBytes <= 0 {
		p.MaxResponseBytes = 64 << 10
	}
	if p.MaxRedirects <= 0 {
		p.MaxRedirects = 3
	}
	if p.Resolver == nil {
		p.Resolver = net.DefaultResolver
	}
	return p
}

func (p Policy) resolveHost(ctx context.Context, host string) ([]netip.Addr, error) {
	p = p.withDefaults()
	host = strings.TrimSuffix(host, ".")
	if literal, err := netip.ParseAddr(host); err == nil {
		literal = literal.Unmap()
		if err := p.validateAddress(literal); err != nil {
			return nil, err
		}
		return []netip.Addr{literal}, nil
	}

	addresses, err := p.Resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrDNS, err)
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("%w: no addresses", ErrDNS)
	}
	validated := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if err := p.validateAddress(address); err != nil {
			return nil, err
		}
		validated = append(validated, address)
	}
	return validated, nil
}

func (p Policy) validateAddress(address netip.Addr) error {
	metadata := netip.MustParseAddr("169.254.169.254")
	if !address.IsValid() ||
		address == metadata ||
		address.IsUnspecified() ||
		address.IsMulticast() ||
		(address.Is6() && address.IsLinkLocalUnicast()) {
		return fmt.Errorf("%w: prohibited address", ErrTargetDenied)
	}
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() {
		for _, prefix := range p.AllowedPrivate {
			if prefix.Contains(address) {
				return nil
			}
		}
		return fmt.Errorf("%w: private address", ErrTargetDenied)
	}
	if !address.IsGlobalUnicast() {
		return fmt.Errorf("%w: non-global address", ErrTargetDenied)
	}
	return nil
}

func validateURL(target *url.URL) error {
	if target == nil ||
		(target.Scheme != "http" && target.Scheme != "https") ||
		target.Hostname() == "" {
		return fmt.Errorf("%w: only http and https targets are allowed", ErrTargetDenied)
	}
	return nil
}
