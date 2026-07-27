package migration

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingLeaseStore struct {
	mu       sync.Mutex
	acquired int
	released int
}

func (s *recordingLeaseStore) AcquireProcessLease(context.Context, ProcessLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquired++
	return nil
}
func (s *recordingLeaseStore) ReleaseProcessLease(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released++
	return nil
}

func TestProcessLeaseHeartbeatsAndReleasesEagerly(t *testing.T) {
	store := &recordingLeaseStore{}
	lease := ProcessLease{InstallationID: "home", ProcessID: "server", ProcessVersion: "v1", Readable: SchemaInterval{Minimum: 10, Maximum: 11}, TTL: 100 * time.Millisecond}
	handle, err := StartProcessLease(context.Background(), store, lease, 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(55 * time.Millisecond)
	stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := handle.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.acquired < 3 || store.released != 1 {
		t.Fatalf("acquired=%d released=%d", store.acquired, store.released)
	}
}
