package probe_test

import (
	"context"
	"errors"
	"net/netip"
	"testing"

	"github.com/araihu/xisnove/agent/probe"
)

func TestPolicyAllowsExplicitLoopbackPrefix(t *testing.T) {
	policy := probe.DefaultPolicy()
	policy.AllowedPrivate = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/8"),
	}

	address, err := policy.ValidateTarget(context.Background(), "http://127.0.0.1/health")
	if err != nil {
		t.Fatal(err)
	}
	if address != netip.MustParseAddr("127.0.0.1") {
		t.Fatalf("address = %v", address)
	}
}

func TestPolicyAlwaysDeniesCloudMetadataAddress(t *testing.T) {
	policy := probe.DefaultPolicy()
	policy.AllowedPrivate = []netip.Prefix{
		netip.MustParsePrefix("169.254.0.0/16"),
	}

	_, err := policy.ValidateTarget(
		context.Background(),
		"http://169.254.169.254/latest/meta-data",
	)
	if !errors.Is(err, probe.ErrTargetDenied) {
		t.Fatalf("error = %v", err)
	}
}

func TestPolicyRejectsNonHTTPURL(t *testing.T) {
	_, err := probe.DefaultPolicy().ValidateTarget(
		context.Background(),
		"file:///etc/passwd",
	)
	if !errors.Is(err, probe.ErrTargetDenied) {
		t.Fatalf("error = %v", err)
	}
}
