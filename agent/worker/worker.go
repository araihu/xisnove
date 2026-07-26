package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

type Executor interface {
	Execute(context.Context, controlplane.ProbeWork) controlplane.ProbeResultInput
}

type Worker struct {
	Client               *controlplane.ClientWithResponses
	Credential           func() (string, error)
	Executor             Executor
	Capabilities         []controlplane.AgentCapability
	Version              string
	CredentialGeneration int64

	resultsOnce sync.Once
	results     chan controlplane.ProbeResultInput
}

func (w *Worker) RunOnce(ctx context.Context) error {
	w.resultsOnce.Do(func() {
		w.results = make(chan controlplane.ProbeResultInput, 100)
	})
	capabilities := w.enabledCapabilities()
	probeCapabilities := enabledProbeCapabilities(capabilities)
	generation := w.CredentialGeneration
	if generation <= 0 {
		generation = 1
	}
	editor, err := w.bearerEditor()
	if err != nil {
		return err
	}
	heartbeat, err := w.Client.HeartbeatAgentWithResponse(
		ctx,
		controlplane.AgentHeartbeat{
			Version:              w.Version,
			CredentialGeneration: generation,
			Capabilities:         capabilities,
		},
		editor,
	)
	if err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	if heartbeat.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("send heartbeat: HTTP %d", heartbeat.StatusCode())
	}
	if len(probeCapabilities) == 0 {
		return nil
	}

	for len(w.results) < cap(w.results) {
		editor, err = w.bearerEditor()
		if err != nil {
			return err
		}
		waitSeconds := int32(0)
		if len(w.results) == 0 {
			waitSeconds = 30
		}
		lease, err := w.Client.LeaseAgentWorkWithResponse(
			ctx,
			controlplane.LeaseWorkRequest{
				WaitSeconds: waitSeconds, Capabilities: probeCapabilities,
			},
			editor,
		)
		if err != nil {
			return fmt.Errorf("lease probe work: %w", err)
		}
		if lease.StatusCode() == http.StatusNoContent {
			break
		}
		if lease.StatusCode() != http.StatusOK || lease.JSON200 == nil {
			return fmt.Errorf("lease probe work: HTTP %d", lease.StatusCode())
		}
		result := w.Executor.Execute(ctx, *lease.JSON200)
		if result.ResultId == uuid.Nil {
			result.ResultId = uuid.New()
		}
		result.RunId = lease.JSON200.RunId
		result.LeaseToken = lease.JSON200.LeaseToken
		select {
		case <-ctx.Done():
			return ctx.Err()
		case w.results <- result:
		}
	}
	if len(w.results) == 0 {
		return nil
	}
	batch := make([]controlplane.ProbeResultInput, 0, min(len(w.results), 100))
	for len(batch) < cap(batch) {
		batch = append(batch, <-w.results)
	}
	return w.uploadUntilAcknowledged(ctx, batch)
}

func (w *Worker) uploadUntilAcknowledged(
	ctx context.Context,
	results []controlplane.ProbeResultInput,
) error {
	backoff := 100 * time.Millisecond
	for {
		editor, err := w.bearerEditor()
		if err != nil {
			return err
		}
		response, err := w.Client.UploadProbeResultsWithResponse(
			ctx,
			controlplane.ProbeResultBatch{
				Results: results,
			},
			editor,
		)
		if err == nil && response.StatusCode() == http.StatusOK && response.JSON200 != nil {
			unacknowledged := unacknowledgedResults(
				results,
				response.JSON200.Acknowledgements,
			)
			if len(unacknowledged) == 0 {
				return nil
			}
			w.requeue(unacknowledged)
			return errors.New("upload probe result: acknowledgement missing")
		}
		if err == nil &&
			response.StatusCode() >= http.StatusBadRequest &&
			response.StatusCode() < http.StatusInternalServerError {
			w.requeue(results)
			return fmt.Errorf("upload probe result: HTTP %d", response.StatusCode())
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			w.requeue(results)
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func unacknowledgedResults(
	results []controlplane.ProbeResultInput,
	acknowledgements []controlplane.ProbeResultAcknowledgement,
) []controlplane.ProbeResultInput {
	acknowledged := make(map[uuid.UUID]struct{}, len(acknowledgements))
	for _, acknowledgement := range acknowledgements {
		if acknowledgement.Status == controlplane.Accepted ||
			acknowledgement.Status == controlplane.Duplicate {
			acknowledged[acknowledgement.ResultId] = struct{}{}
		}
	}
	pending := make([]controlplane.ProbeResultInput, 0)
	for _, result := range results {
		if _, ok := acknowledged[result.ResultId]; !ok {
			pending = append(pending, result)
		}
	}
	return pending
}

func (w *Worker) requeue(results []controlplane.ProbeResultInput) {
	for _, result := range results {
		w.results <- result
	}
}

func (w *Worker) enabledCapabilities() []controlplane.AgentCapability {
	if len(w.Capabilities) == 0 {
		return []controlplane.AgentCapability{controlplane.AgentCapabilityHttp}
	}
	return append([]controlplane.AgentCapability(nil), w.Capabilities...)
}

func enabledProbeCapabilities(capabilities []controlplane.AgentCapability) []controlplane.AgentCapability {
	probes := make([]controlplane.AgentCapability, 0, len(capabilities))
	for _, capability := range capabilities {
		switch capability {
		case controlplane.AgentCapabilityHttp, controlplane.AgentCapabilityTcp, controlplane.AgentCapabilityDns:
			probes = append(probes, capability)
		}
	}
	return probes
}

func (w *Worker) bearerEditor() (controlplane.RequestEditorFn, error) {
	credential, err := w.Credential()
	if err != nil {
		return nil, fmt.Errorf("read agent credential: %w", err)
	}
	if credential == "" {
		return nil, errors.New("read agent credential: empty credential")
	}
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+credential)
		return nil
	}, nil
}
