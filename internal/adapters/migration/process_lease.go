package migration

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type ProcessLeaseStore interface {
	AcquireProcessLease(context.Context, ProcessLease) error
	RenewProcessLease(context.Context, ProcessLease) error
	ReleaseProcessLease(context.Context, string, string) error
}

type ProcessLeaseStoreFuncs struct {
	Acquire func(context.Context, ProcessLease) error
	Renew   func(context.Context, ProcessLease) error
	Release func(context.Context, string, string) error
}

func (s ProcessLeaseStoreFuncs) RenewProcessLease(ctx context.Context, lease ProcessLease) error {
	if s.Renew == nil {
		return fmt.Errorf("process lease renew function is required")
	}
	return s.Renew(ctx, lease)
}

func (s ProcessLeaseStoreFuncs) AcquireProcessLease(ctx context.Context, lease ProcessLease) error {
	if s.Acquire == nil {
		return fmt.Errorf("process lease acquire function is required")
	}
	return s.Acquire(ctx, lease)
}

func (s ProcessLeaseStoreFuncs) ReleaseProcessLease(ctx context.Context, installationID, processID string) error {
	if s.Release == nil {
		return fmt.Errorf("process lease release function is required")
	}
	return s.Release(ctx, installationID, processID)
}

type ProcessLeaseHandle struct {
	cancel      context.CancelFunc
	done        chan struct{}
	errors      chan error
	cancelOnce  sync.Once
	releaseOnce sync.Once
	releaseErr  error
	store       ProcessLeaseStore
	lease       ProcessLease
}

func StartProcessLease(ctx context.Context, store ProcessLeaseStore, lease ProcessLease, heartbeatInterval time.Duration) (*ProcessLeaseHandle, error) {
	if store == nil {
		return nil, fmt.Errorf("process lease store is required")
	}
	if err := lease.Validate(); err != nil {
		return nil, err
	}
	if heartbeatInterval <= 0 || heartbeatInterval >= lease.TTL {
		return nil, fmt.Errorf("process lease heartbeat must be positive and less than TTL")
	}
	if err := store.AcquireProcessLease(ctx, lease); err != nil {
		return nil, err
	}
	heartbeatCtx, cancel := context.WithCancel(ctx)
	handle := &ProcessLeaseHandle{cancel: cancel, done: make(chan struct{}), errors: make(chan error, 1), store: store, lease: lease}
	go handle.heartbeat(heartbeatCtx, heartbeatInterval)
	return handle, nil
}

func (h *ProcessLeaseHandle) heartbeat(ctx context.Context, interval time.Duration) {
	defer close(h.done)
	defer close(h.errors)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.store.RenewProcessLease(ctx, h.lease); err != nil {
				select {
				case h.errors <- fmt.Errorf("heartbeat process version lease: %w", err):
				default:
				}
				h.cancel()
				return
			}
		}
	}
}

func (h *ProcessLeaseHandle) Errors() <-chan error { return h.errors }

func (h *ProcessLeaseHandle) Stop(ctx context.Context) error {
	if h == nil {
		return nil
	}
	h.cancelOnce.Do(h.cancel)
	select {
	case <-h.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	h.releaseOnce.Do(func() { h.releaseErr = h.store.ReleaseProcessLease(ctx, h.lease.InstallationID, h.lease.ProcessID) })
	return h.releaseErr
}
