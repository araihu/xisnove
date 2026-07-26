package discovery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/internal/controlplane"
)

var ErrBatchTooLarge = errors.New("discovery snapshot exceeds 500 candidates")
var ErrPartialAcknowledgement = errors.New("discovery API acknowledged only part of the submitted chunk")

const (
	MaxBatchSize = 500
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

type LoopConfig struct {
	Enabled    bool
	Cadence    time.Duration
	MinBackoff time.Duration
	MaxBackoff time.Duration
	Wait       func(context.Context, time.Duration) error
	OnError    func(error)
}

// Run publishes discovery snapshots on an independently enabled cadence. A
// failed cycle is retried with bounded exponential backoff; successful cycles
// return to the configured cadence.
func (runner Runner) Run(ctx context.Context, config LoopConfig) error {
	if !config.Enabled {
		return nil
	}
	if config.Cadence <= 0 || config.MinBackoff <= 0 || config.MaxBackoff < config.MinBackoff {
		return errors.New("discovery loop requires a positive cadence and bounded backoff")
	}
	wait := config.Wait
	if wait == nil {
		wait = waitFor
	}
	backoff := config.MinBackoff
	for ctx.Err() == nil {
		delay := config.Cadence
		if err := runner.RunOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if config.OnError != nil {
				config.OnError(err)
			}
			delay = backoff
			backoff = min(backoff*2, config.MaxBackoff)
		} else {
			backoff = config.MinBackoff
		}
		if err := wait(ctx, delay); err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) {
				return nil
			}
			return fmt.Errorf("wait for discovery cycle: %w", err)
		}
	}
	return nil
}

func waitFor(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	Client      *controlplane.ClientWithResponses
	Credentials credentials.Provider
}

func (publisher APIPublisher) Publish(ctx context.Context, batch Batch) error {
	if publisher.Client == nil || publisher.Credentials == nil {
		return errors.New("discovery API publisher is not configured")
	}
	if len(batch.Candidates) == 0 || len(batch.Candidates) > MaxBatchSize || batch.ID == "" {
		return ErrBatchTooLarge
	}
	bundle, err := publisher.Credentials.Current(ctx)
	if err != nil {
		return fmt.Errorf("read agent credential: %w", err)
	}
	if bundle.Credential == "" || bundle.Generation <= 0 {
		return errors.New("read agent credential: invalid bundle")
	}
	editor := func(_ context.Context, request *http.Request) error {
		request.Header.Set("Authorization", "Bearer "+bundle.Credential)
		return nil
	}
	key := controlplane.IdempotencyKey(batch.ID)
	response, err := publisher.Client.UpsertDiscoveryCandidatesWithResponse(ctx,
		&controlplane.UpsertDiscoveryCandidatesParams{IdempotencyKey: &key},
		controlplane.DiscoveryCandidateBatch{Candidates: batch.Candidates}, editor)
	if err != nil {
		return err
	}
	if response.StatusCode() != http.StatusOK || response.JSON200 == nil {
		return fmt.Errorf("discovery API returned HTTP %d", response.StatusCode())
	}
	if int(response.JSON200.Accepted) != len(batch.Candidates) {
		return fmt.Errorf("%w: accepted %d of %d", ErrPartialAcknowledgement, response.JSON200.Accepted, len(batch.Candidates))
	}
	return nil
}
