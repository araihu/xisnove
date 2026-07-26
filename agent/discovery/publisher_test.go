package discovery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/araihu/xisnove/agent/discovery"
	"github.com/araihu/xisnove/agent/internal/controlplane"
)

func TestRunnerPublishesBoundedSnapshot(t *testing.T) {
	producer := &fakeProducer{batch: discovery.Batch{ID: "batch-1", Candidates: []controlplane.DiscoveryCandidateInput{{
		ExternalId: "service/uid-1", Kind: controlplane.DiscoveryCandidateInputKindHttp,
		Name: "api", Target: "https://api.default.svc/health", Labels: map[string]string{"app": "api"},
		ObservedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}}}}
	publisher := &fakePublisher{}
	runner := discovery.Runner{Producer: producer, Publisher: publisher}
	if err := runner.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if producer.calls != 1 || publisher.calls != 1 || publisher.batch.ID != "batch-1" || len(publisher.batch.Candidates) != 1 {
		t.Fatalf("producer=%d publisher=%d batch=%#v", producer.calls, publisher.calls, publisher.batch)
	}
}

func TestRunnerRejectsOversizedSnapshotWithoutPublishing(t *testing.T) {
	producer := &fakeProducer{batch: discovery.Batch{ID: "batch-1", Candidates: make([]controlplane.DiscoveryCandidateInput, 501)}}
	publisher := &fakePublisher{}
	err := (discovery.Runner{Producer: producer, Publisher: publisher}).RunOnce(context.Background())
	if !errors.Is(err, discovery.ErrBatchTooLarge) || publisher.calls != 0 {
		t.Fatalf("error=%v publisher calls=%d", err, publisher.calls)
	}
}

type fakeProducer struct {
	batch discovery.Batch
	err   error
	calls int
}

func (f *fakeProducer) Snapshot(context.Context) (discovery.Batch, error) {
	f.calls++
	return f.batch, f.err
}

type fakePublisher struct {
	batch discovery.Batch
	err   error
	calls int
}

func (f *fakePublisher) Publish(_ context.Context, batch discovery.Batch) error {
	f.calls++
	f.batch = batch
	return f.err
}
