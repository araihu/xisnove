package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/araihu/xisnove/internal/adapters/migration"
)

type runtimeProcessLease struct {
	handle   *migration.ProcessLeaseHandle
	failures chan error

	mu  sync.RWMutex
	err error
}

func startRuntimeProcessLease(
	ctx context.Context,
	store migration.ProcessLeaseStore,
	lease migration.ProcessLease,
	heartbeatInterval time.Duration,
) (*runtimeProcessLease, error) {
	handle, err := migration.StartProcessLease(ctx, store, lease, heartbeatInterval)
	if err != nil {
		return nil, fmt.Errorf("start process version lease: %w", err)
	}
	runtimeLease := &runtimeProcessLease{handle: handle, failures: make(chan error, 1)}
	go runtimeLease.observe()
	return runtimeLease, nil
}

func (l *runtimeProcessLease) observe() {
	err, ok := <-l.handle.Errors()
	if !ok || err == nil {
		return
	}
	l.mu.Lock()
	l.err = err
	l.mu.Unlock()
	l.failures <- err
}

func (l *runtimeProcessLease) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.err != nil {
		return errors.Join(errors.New("process version lease is not ready"), l.err)
	}
	return nil
}

func (l *runtimeProcessLease) Failures() <-chan error { return l.failures }

func (l *runtimeProcessLease) Stop(ctx context.Context) error {
	if l == nil {
		return nil
	}
	return l.handle.Stop(ctx)
}

func runtimeProcessVersion(version string) string {
	if version = strings.TrimSpace(version); version != "" {
		return version
	}
	return "dev"
}
