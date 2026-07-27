package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/adapters/migration"
)

type processLeaseStoreStub struct {
	mu       sync.Mutex
	acquires int
	released bool
	failAt   int
}

func (s *processLeaseStoreStub) AcquireProcessLease(context.Context, migration.ProcessLease) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.acquires++
	if s.failAt > 0 && s.acquires >= s.failAt {
		return errors.New("lease backend unavailable")
	}
	return nil
}

func (s *processLeaseStoreStub) ReleaseProcessLease(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.released = true
	return nil
}

func TestRuntimeProcessLeaseFailureClearsReadinessAndSignalsShutdown(t *testing.T) {
	store := &processLeaseStoreStub{failAt: 2}
	lease, err := startRuntimeProcessLease(context.Background(), store, migration.ProcessLease{
		InstallationID: "home", ProcessID: "server-1", ProcessVersion: "1.2.3",
		Readable: migration.SchemaInterval{Minimum: 10, Maximum: 11}, TTL: 100 * time.Millisecond,
	}, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := lease.Stop(ctx); err != nil {
			t.Errorf("Stop() error = %v", err)
		}
	})
	if err := lease.Ready(context.Background()); err != nil {
		t.Fatalf("initial Ready() error = %v", err)
	}
	select {
	case err := <-lease.Failures():
		if err == nil {
			t.Fatal("failure signal is nil")
		}
	case <-time.After(time.Second):
		t.Fatal("lease failure was not signaled")
	}
	if err := lease.Ready(context.Background()); err == nil {
		t.Fatal("Ready() error = nil after heartbeat failure")
	}
}

func TestRuntimeProcessLeaseStopReleasesLease(t *testing.T) {
	store := &processLeaseStoreStub{}
	lease, err := startRuntimeProcessLease(context.Background(), store, migration.ProcessLease{
		InstallationID: "home", ProcessID: "server-1", ProcessVersion: "dev",
		Readable: migration.SchemaInterval{Minimum: 10, Maximum: 11}, TTL: time.Second,
	}, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lease.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if !store.released {
		t.Fatal("process lease was not released")
	}
}

func TestRuntimeProcessVersionDefaultsForDevelopmentBuild(t *testing.T) {
	if got := runtimeProcessVersion(""); got != "dev" {
		t.Fatalf("runtimeProcessVersion() = %q, want dev", got)
	}
	if got := runtimeProcessVersion(" 1.2.3 "); got != "1.2.3" {
		t.Fatalf("runtimeProcessVersion() = %q, want 1.2.3", got)
	}
}
