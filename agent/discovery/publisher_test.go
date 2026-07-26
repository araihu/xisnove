package discovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/araihu/xisnove/agent/discovery"
	"github.com/araihu/xisnove/agent/internal/controlplane"
)

func TestRunnerPublishesBoundedSnapshot(t *testing.T) {
	producer := &fakeProducer{batch: discovery.Batch{ID: "batch-1", Candidates: []controlplane.DiscoveryCandidateInput{{
		SourceKind: "service", SourceUid: "uid-1", Protocol: controlplane.DiscoveryCandidateInputProtocolHttp,
		Namespace: "default", Name: "api", Target: "https://api.default.svc/health",
		NetworkPerspective: "cluster-a", Present: true, Labels: map[string]string{"app": "api"},
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

func TestRunnerLoopIsSeparatelyEnabledAndUsesBoundedBackoff(t *testing.T) {
	producer := &fakeProducer{batch: discovery.Batch{ID: "batch-1", Candidates: []controlplane.DiscoveryCandidateInput{{
		SourceKind: "service", SourceUid: "uid-1", Protocol: controlplane.DiscoveryCandidateInputProtocolHttp,
		Namespace: "default", Name: "api", Target: "https://api.default.svc/health",
		NetworkPerspective: "cluster-a", Present: true, Labels: map[string]string{},
		ObservedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}}}}
	publisher := &fakePublisher{errs: []error{errors.New("first"), errors.New("second"), nil}}
	runner := discovery.Runner{Producer: producer, Publisher: publisher}

	if err := runner.Run(context.Background(), discovery.LoopConfig{}); err != nil || producer.calls != 0 {
		t.Fatalf("disabled loop = %v, producer calls=%d", err, producer.calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var waits []time.Duration
	var observed []error
	err := runner.Run(ctx, discovery.LoopConfig{
		Enabled: true, Cadence: 30 * time.Second, MinBackoff: time.Second, MaxBackoff: 2 * time.Second,
		OnError: func(err error) { observed = append(observed, err) },
		Wait: func(_ context.Context, delay time.Duration) error {
			waits = append(waits, delay)
			if len(waits) == 3 {
				cancel()
				return context.Canceled
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	wantWaits := []time.Duration{time.Second, 2 * time.Second, 30 * time.Second}
	if len(observed) != 2 || len(waits) != len(wantWaits) {
		t.Fatalf("observed=%v waits=%v", observed, waits)
	}
	for index := range wantWaits {
		if waits[index] != wantWaits[index] {
			t.Fatalf("waits=%v, want %v", waits, wantWaits)
		}
	}
}

func TestAPIPublisherFailsClosedOnPartialAcknowledgement(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/agent/discovery-candidates:batch" || request.Header.Get("Authorization") != "Bearer credential" {
			t.Errorf("request path=%q authorization=%q", request.URL.Path, request.Header.Get("Authorization"))
		}
		if request.Header.Get("Idempotency-Key") != "batch-1" {
			t.Errorf("idempotency key=%q", request.Header.Get("Idempotency-Key"))
		}
		var body controlplane.DiscoveryCandidateBatch
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		requests++
		accepted := len(body.Candidates) - 1
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(controlplane.DiscoveryCandidateBatchAcknowledgement{Accepted: int32(accepted)})
	}))
	defer server.Close()
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	candidates := make([]controlplane.DiscoveryCandidateInput, 101)
	for index := range candidates {
		candidates[index] = controlplane.DiscoveryCandidateInput{
			SourceKind: "service", SourceUid: "uid", Namespace: "default", Name: "api",
			Labels: map[string]string{}, Protocol: controlplane.DiscoveryCandidateInputProtocolHttp,
			Target: "https://api.default.svc/health", NetworkPerspective: "cluster-a", Present: true,
			ObservedAt: time.Now().UTC(),
		}
	}
	err = (discovery.APIPublisher{Client: client, Credential: func() (string, error) { return "credential", nil }}).Publish(context.Background(), discovery.Batch{ID: "batch-1", Candidates: candidates})
	if !errors.Is(err, discovery.ErrPartialAcknowledgement) || requests != 1 {
		t.Fatalf("error=%v requests=%d", err, requests)
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
	errs  []error
	calls int
}

func (f *fakePublisher) Publish(_ context.Context, batch discovery.Batch) error {
	f.calls++
	f.batch = batch
	if len(f.errs) >= f.calls {
		return f.errs[f.calls-1]
	}
	return f.err
}
