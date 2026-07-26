package discovery_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/araihu/xisnove/agent/credentials"
	"github.com/araihu/xisnove/agent/discovery"
	"github.com/araihu/xisnove/agent/internal/controlplane"
)

func TestAPIPublisherReloadsCredentialBundleBeforeEachRequest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	writeCredentialBundle(t, path, `{"credential":"first-credential","generation":7}`)
	var authorizations []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		authorizations = append(authorizations, request.Header.Get("Authorization"))
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(controlplane.DiscoveryCandidateBatchAcknowledgement{Accepted: 1})
	}))
	t.Cleanup(server.Close)
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	publisher := discovery.APIPublisher{
		Client:      client,
		Credentials: credentials.FileProvider{Path: path},
	}
	if err := publisher.Publish(context.Background(), testBatch("first")); err != nil {
		t.Fatal(err)
	}
	replacement := filepath.Join(filepath.Dir(path), "replacement.json")
	writeCredentialBundle(t, replacement, `{"credential":"second-credential","generation":8}`)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), testBatch("second")); err != nil {
		t.Fatal(err)
	}
	want := []string{"Bearer first-credential", "Bearer second-credential"}
	if len(authorizations) != len(want) {
		t.Fatalf("authorizations = %#v", authorizations)
	}
	for index := range want {
		if authorizations[index] != want[index] {
			t.Fatalf("authorization[%d] = %q, want %q", index, authorizations[index], want[index])
		}
	}
}

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

func TestRunnerPublishesAnEmptyCompleteSnapshot(t *testing.T) {
	completedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	producer := &fakeProducer{batch: discovery.Batch{ID: "empty", Complete: true, CompletedAt: completedAt}}
	publisher := &fakePublisher{}
	if err := (discovery.Runner{Producer: producer, Publisher: publisher}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if publisher.calls != 1 || !publisher.batch.Complete || !publisher.batch.CompletedAt.Equal(completedAt) || len(publisher.batch.Candidates) != 0 {
		t.Fatalf("published batch = %#v", publisher.batch)
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

func TestRunnerPublishesPartialChunksFromMultiProducer(t *testing.T) {
	producer := multiProducer{batches: []discovery.Batch{{ID: "chunk-1", Candidates: make([]controlplane.DiscoveryCandidateInput, 500)}, {ID: "chunk-2", Candidates: make([]controlplane.DiscoveryCandidateInput, 1)}}}
	publisher := &recordingPublisher{}
	if err := (discovery.Runner{Producer: producer, Publisher: publisher}).RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(publisher.batches) != 2 || publisher.batches[0].Complete || publisher.batches[1].Complete {
		t.Fatalf("published batches = %#v", publisher.batches)
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
	err = (discovery.APIPublisher{Client: client, Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "credential", Generation: 16}}}).Publish(context.Background(), discovery.Batch{ID: "batch-1", Candidates: candidates})
	if !errors.Is(err, discovery.ErrPartialAcknowledgement) || requests != 1 {
		t.Fatalf("error=%v requests=%d", err, requests)
	}
}

func TestAPIPublisherSendsCompletionFields(t *testing.T) {
	completedAt := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body controlplane.DiscoveryCandidateBatch
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if !body.Complete || !body.CompletedAt.Equal(completedAt) || len(body.Candidates) != 0 {
			t.Fatalf("body = %#v", body)
		}
		response.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(response).Encode(controlplane.DiscoveryCandidateBatchAcknowledgement{})
	}))
	t.Cleanup(server.Close)
	client, err := controlplane.NewClientWithResponses(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	publisher := discovery.APIPublisher{Client: client, Credentials: fixedCredentials{bundle: credentials.Bundle{Credential: "credential", Generation: 1}}}
	if err := publisher.Publish(context.Background(), discovery.Batch{ID: "empty", Complete: true, CompletedAt: completedAt}); err != nil {
		t.Fatal(err)
	}
}

type fakeProducer struct {
	batch discovery.Batch
	err   error
	calls int
}

type multiProducer struct {
	batches []discovery.Batch
	err     error
}

func (producer multiProducer) Snapshot(context.Context) (discovery.Batch, error) {
	return discovery.Batch{}, errors.New("Snapshot must not be called for a multi producer")
}
func (producer multiProducer) Snapshots(context.Context) ([]discovery.Batch, error) {
	return producer.batches, producer.err
}

type recordingPublisher struct{ batches []discovery.Batch }

func (publisher *recordingPublisher) Publish(_ context.Context, batch discovery.Batch) error {
	publisher.batches = append(publisher.batches, batch)
	return nil
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

func testBatch(id string) discovery.Batch {
	return discovery.Batch{ID: id, Candidates: []controlplane.DiscoveryCandidateInput{{
		SourceKind: "service", SourceUid: "uid-1", Protocol: controlplane.DiscoveryCandidateInputProtocolHttp,
		Namespace: "default", Name: "api", Target: "https://api.default.svc/health",
		NetworkPerspective: "cluster-a", Present: true, Labels: map[string]string{},
		ObservedAt: time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC),
	}}}
}

func writeCredentialBundle(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

type fixedCredentials struct {
	bundle credentials.Bundle
	err    error
}

func (credentials fixedCredentials) Current(context.Context) (credentials.Bundle, error) {
	return credentials.bundle, credentials.err
}

func (f *fakePublisher) Publish(_ context.Context, batch discovery.Batch) error {
	f.calls++
	f.batch = batch
	if len(f.errs) >= f.calls {
		return f.errs[f.calls-1]
	}
	return f.err
}
