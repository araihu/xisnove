package probe_test

import (
	"context"
	"testing"

	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/araihu/xisnove/agent/probe"
)

func TestDispatcherRoutesEachProbeVariant(t *testing.T) {
	dispatcher := probe.NewDispatcher(
		markerExecutor("http"),
		markerExecutor("tcp"),
		markerExecutor("dns"),
	)
	for _, test := range []struct {
		kind string
		work controlplane.ProbeWork
	}{
		{"http", testWork("https://example.com", 200, "")},
		{"tcp", tcpWork("127.0.0.1:443", nil, nil, 1000, nil)},
		{"dns", dnsWork("1.1.1.1:53", "example.com", "A", nil)},
	} {
		result := dispatcher.Execute(context.Background(), test.work)
		if result.DiagnosticSample != test.kind {
			t.Fatalf("%s result = %#v", test.kind, result)
		}
	}
}

type markerExecutor string

func (e markerExecutor) Execute(
	context.Context,
	controlplane.ProbeWork,
) controlplane.ProbeResultInput {
	return controlplane.ProbeResultInput{DiagnosticSample: string(e)}
}
