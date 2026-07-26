package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/araihu/xisnove/agent/internal/controlplane"
)

var ErrBatchTooLarge = errors.New("discovery snapshot exceeds 500 candidates")

const (
	MaxBatchSize   = 500
	maxRequestSize = 100
)

type Batch struct {
	ID         string
	Candidates []controlplane.DiscoveryCandidateInput
}

type Producer interface {
	Snapshot(context.Context) (Batch, error)
}
type Publisher interface {
	Publish(context.Context, Batch) error
}

type Runner struct {
	Producer  Producer
	Publisher Publisher
}

func (runner Runner) RunOnce(ctx context.Context) error {
	if runner.Producer == nil || runner.Publisher == nil {
		return errors.New("discovery runner is not configured")
	}
	batch, err := runner.Producer.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("produce discovery snapshot: %w", err)
	}
	if len(batch.Candidates) == 0 {
		return nil
	}
	if batch.ID == "" {
		return errors.New("produce discovery snapshot: empty batch id")
	}
	if len(batch.Candidates) > MaxBatchSize {
		return ErrBatchTooLarge
	}
	if err := runner.Publisher.Publish(ctx, batch); err != nil {
		return fmt.Errorf("publish discovery snapshot: %w", err)
	}
	return nil
}

type APIPublisher struct {
	Client     *controlplane.ClientWithResponses
	Credential func() (string, error)
}

func (publisher APIPublisher) Publish(ctx context.Context, batch Batch) error {
	if publisher.Client == nil || publisher.Credential == nil {
		return errors.New("discovery API publisher is not configured")
	}
	if len(batch.Candidates) == 0 || len(batch.Candidates) > MaxBatchSize || batch.ID == "" {
		return ErrBatchTooLarge
	}
	credential, err := publisher.Credential()
	if err != nil {
		return fmt.Errorf("read agent credential: %w", err)
	}
	if credential == "" {
		return errors.New("read agent credential: empty credential")
	}
	editor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+credential)
		return nil
	}
	for offset := 0; offset < len(batch.Candidates); offset += maxRequestSize {
		end := min(offset+maxRequestSize, len(batch.Candidates))
		key := controlplane.IdempotencyKey(fmt.Sprintf("%s/%d", batch.ID, offset/maxRequestSize))
		response, err := publisher.Client.UpsertDiscoveryCandidatesWithResponse(ctx,
			&controlplane.UpsertDiscoveryCandidatesParams{IdempotencyKey: &key},
			controlplane.DiscoveryCandidateBatch{Candidates: batch.Candidates[offset:end]}, editor)
		if err != nil {
			return err
		}
		if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
			return fmt.Errorf("discovery API returned HTTP %d", response.StatusCode())
		}
	}
	return nil
}
