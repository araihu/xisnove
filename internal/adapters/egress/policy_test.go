package egress_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/adapters/egress"
)

func TestPolicyBlocksUnsafeAddressClasses(t *testing.T) {
	policy := newPolicy(t, nil, nil)
	for _, address := range []string{
		"127.0.0.1", "0.0.0.0", "169.254.1.1", "224.0.0.1",
		"10.0.0.1", "172.16.0.1", "192.168.1.1", "100.64.0.1",
		"::1", "::", "fe80::1", "ff02::1", "fd00::1",
	} {
		t.Run(address, func(t *testing.T) {
			host := address
			if strings.Contains(address, ":") {
				host = "[" + address + "]"
			}
			if err := policy.ValidateURL(context.Background(), mustURL(t, "https://"+host+"/hook")); err == nil {
				t.Fatal("unsafe address was allowed")
			}
		})
	}
}

func TestPolicySupportsExplicitHomelabAllowButKeepsConfiguredDenies(t *testing.T) {
	policy, err := egress.NewPolicy(egress.Config{
		AllowedCIDRs: []string{"192.168.1.0/24", "10.0.0.0/8"},
		DeniedCIDRs:  []string{"10.96.0.0/12"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), mustURL(t, "https://192.168.1.10/hook")); err != nil {
		t.Fatalf("explicit homelab allow = %v", err)
	}
	if err := policy.ValidateURL(context.Background(), mustURL(t, "https://10.96.0.10/hook")); err == nil {
		t.Fatal("configured Kubernetes service range was allowed")
	}
}

func TestPolicyRejectsDNSRebindingAndDialsValidatedAddress(t *testing.T) {
	resolver := staticResolver{values: map[string][]netip.Addr{
		"mixed.example": {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")},
		"safe.example":  {netip.MustParseAddr("8.8.4.4")},
	}}
	var dialed string
	policy, err := egress.NewPolicy(egress.Config{
		Resolver: resolver,
		DialContext: func(_ context.Context, _, address string) (net.Conn, error) {
			dialed = address
			return nil, errors.New("stop after address assertion")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := policy.ValidateURL(context.Background(), mustURL(t, "https://mixed.example/hook")); err == nil {
		t.Fatal("mixed public/private DNS answer was allowed")
	}
	if _, err := policy.DialContext(context.Background(), "tcp", "safe.example:443"); err == nil {
		t.Fatal("dial unexpectedly succeeded")
	}
	if dialed != "8.8.4.4:443" {
		t.Fatalf("dialed address = %q", dialed)
	}
}

func TestPolicyRevalidatesRedirects(t *testing.T) {
	policy, err := egress.NewPolicy(egress.Config{
		Resolver: staticResolver{values: map[string][]netip.Addr{"public.example": {netip.MustParseAddr("8.8.8.8")}}},
		RoundTripper: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Hostname() == "public.example" {
				return &http.Response{
					StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"http://127.0.0.1/private"}},
					Body: io.NopCloser(strings.NewReader("")), Request: request,
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, _ := http.NewRequest(http.MethodPost, "https://public.example/hook", nil)
	response, err := policy.HTTPClient(time.Second).Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "redirect") {
		t.Fatalf("redirect error = %v", err)
	}
}

func TestPolicyHonorsCanceledResolution(t *testing.T) {
	policy, err := egress.NewPolicy(egress.Config{Resolver: blockingResolver{}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := policy.ValidateURL(ctx, mustURL(t, "https://blocked.example/hook")); !errors.Is(err, context.Canceled) {
		t.Fatalf("ValidateURL() error = %v", err)
	}
}

type staticResolver struct{ values map[string][]netip.Addr }

func (r staticResolver) LookupNetIP(_ context.Context, _ string, host string) ([]netip.Addr, error) {
	return append([]netip.Addr(nil), r.values[host]...), nil
}

type blockingResolver struct{}

func (blockingResolver) LookupNetIP(ctx context.Context, _ string, _ string) ([]netip.Addr, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func newPolicy(t *testing.T, allowed, denied []string) *egress.Policy {
	t.Helper()
	policy, err := egress.NewPolicy(egress.Config{AllowedCIDRs: allowed, DeniedCIDRs: denied})
	if err != nil {
		t.Fatal(err)
	}
	return policy
}

func mustURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
