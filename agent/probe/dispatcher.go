package probe

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

type Executor interface {
	Execute(context.Context, controlplane.ProbeWork) controlplane.ProbeResultInput
}

type Dispatcher struct {
	http Executor
	tcp  Executor
	dns  Executor
}

func NewDispatcher(httpExecutor, tcpExecutor, dnsExecutor Executor) *Dispatcher {
	return &Dispatcher{
		http: httpExecutor,
		tcp:  tcpExecutor,
		dns:  dnsExecutor,
	}
}

func (d *Dispatcher) Execute(
	ctx context.Context,
	work controlplane.ProbeWork,
) controlplane.ProbeResultInput {
	kind, err := work.Probe.Discriminator()
	if err == nil {
		switch kind {
		case "http":
			if d.http != nil {
				return d.http.Execute(ctx, work)
			}
		case "tcp":
			if d.tcp != nil {
				return d.tcp.Execute(ctx, work)
			}
		case "dns":
			if d.dns != nil {
				return d.dns.Execute(ctx, work)
			}
		}
	}
	now := time.Now().UTC()
	return controlplane.ProbeResultInput{
		ResultId: uuid.New(), RunId: work.RunId, LeaseToken: work.LeaseToken,
		StartedAt: now, FinishedAt: now, Outcome: controlplane.Failed,
		ErrorCode:        controlplane.ProtocolError,
		DiagnosticSample: "unsupported or disabled probe variant",
	}
}
