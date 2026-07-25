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
	Execute(context.Context, controlplane.HTTPWork) controlplane.ProbeResultInput
}

type Worker struct {
	Client               *controlplane.ClientWithResponses
	Credential           func() (string, error)
	Executor             Executor
	Version              string
	CredentialGeneration int64

	resultsOnce sync.Once
	results     chan controlplane.ProbeResultInput
}

func (w *Worker) RunOnce(ctx context.Context) error {
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
			Capabilities: []controlplane.AgentCapability{
				controlplane.AgentCapabilityHttp,
			},
		},
		editor,
	)
	if err != nil {
		return fmt.Errorf("send heartbeat: %w", err)
	}
	if heartbeat.StatusCode() != http.StatusNoContent {
		return fmt.Errorf("send heartbeat: HTTP %d", heartbeat.StatusCode())
	}

	editor, err = w.bearerEditor()
	if err != nil {
		return err
	}
	lease, err := w.Client.LeaseAgentWorkWithResponse(
		ctx,
		controlplane.LeaseWorkRequest{
			WaitSeconds: 30,
			Capabilities: []controlplane.AgentCapability{
				controlplane.AgentCapabilityHttp,
			},
		},
		editor,
	)
	if err != nil {
		return fmt.Errorf("lease HTTP work: %w", err)
	}
	if lease.StatusCode() == http.StatusNoContent {
		return nil
	}
	if lease.StatusCode() != http.StatusOK || lease.JSON200 == nil {
		return fmt.Errorf("lease HTTP work: HTTP %d", lease.StatusCode())
	}

	result := w.Executor.Execute(ctx, *lease.JSON200)
	if result.ResultId == uuid.Nil {
		result.ResultId = uuid.New()
	}
	result.RunId = lease.JSON200.RunId
	result.LeaseToken = lease.JSON200.LeaseToken
	w.resultsOnce.Do(func() {
		w.results = make(chan controlplane.ProbeResultInput, 100)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case w.results <- result:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case pending := <-w.results:
		return w.uploadUntilAcknowledged(ctx, pending)
	}
}

func (w *Worker) uploadUntilAcknowledged(
	ctx context.Context,
	result controlplane.ProbeResultInput,
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
				Results: []controlplane.ProbeResultInput{result},
			},
			editor,
		)
		if err == nil && response.StatusCode() == http.StatusOK && response.JSON200 != nil {
			if acknowledged(response.JSON200.Acknowledgements, result.ResultId) {
				return nil
			}
			return errors.New("upload probe result: acknowledgement missing")
		}
		if err == nil &&
			response.StatusCode() >= http.StatusBadRequest &&
			response.StatusCode() < http.StatusInternalServerError {
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
			return ctx.Err()
		case <-timer.C:
		}
		backoff = min(backoff*2, 5*time.Second)
	}
}

func acknowledged(
	acknowledgements []controlplane.ProbeResultAcknowledgement,
	resultID uuid.UUID,
) bool {
	for _, acknowledgement := range acknowledgements {
		if acknowledgement.ResultId == resultID &&
			(acknowledgement.Status == controlplane.Accepted ||
				acknowledgement.Status == controlplane.Duplicate) {
			return true
		}
	}
	return false
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
