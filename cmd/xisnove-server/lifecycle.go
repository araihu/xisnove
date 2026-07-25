package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

var (
	errLifecycleNotReady        = errors.New("server is shutting down")
	errLifecycleAdmissionClosed = errors.New("new work is not accepted")
)

type lifecyclePhase uint8

const (
	lifecycleAccepting lifecyclePhase = iota
	lifecycleStoppingClaims
	lifecycleDraining
	lifecycleClosing
	lifecycleClosed
)

func (phase lifecyclePhase) String() string {
	switch phase {
	case lifecycleAccepting:
		return "accepting"
	case lifecycleStoppingClaims:
		return "stopping_claims"
	case lifecycleDraining:
		return "draining"
	case lifecycleClosing:
		return "closing"
	case lifecycleClosed:
		return "closed"
	default:
		return "unknown"
	}
}

// serverLifecycle coordinates ownership of new work and ordered shutdown.
// Callers use ClaimContext to stop claim loops and Admit before a claim or
// long-poll can create in-flight work. Admitted work uses the context returned
// by Admit, which preserves caller cancellation, and must call its release
// function (extra calls are safe).
type serverLifecycle struct {
	mu        sync.RWMutex
	phase     lifecyclePhase
	accepting bool
	inflight  sync.WaitGroup

	claimsCtx    context.Context
	cancelClaims context.CancelFunc
	workCtx      context.Context
	cancelWork   context.CancelFunc

	stopOnce  sync.Once
	forceOnce sync.Once
	forced    chan struct{}
	startOnce sync.Once
	done      chan struct{}

	resultMu sync.Mutex
	result   error
}

func newServerLifecycle() *serverLifecycle {
	claimsCtx, cancelClaims := context.WithCancel(context.Background())
	workCtx, cancelWork := context.WithCancel(context.Background())
	return &serverLifecycle{
		phase: lifecycleAccepting, accepting: true,
		claimsCtx: claimsCtx, cancelClaims: cancelClaims,
		workCtx: workCtx, cancelWork: cancelWork,
		forced: make(chan struct{}), done: make(chan struct{}),
	}
}

func (l *serverLifecycle) Phase() lifecyclePhase {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.phase
}

func (l *serverLifecycle) ClaimContext() context.Context { return l.claimsCtx }

// Ready checks lifecycle state both before and after the backing probe. The
// probe never holds the lifecycle lock, so a slow or hung dependency cannot
// prevent shutdown or forced cancellation from advancing.
func (l *serverLifecycle) Ready(ctx context.Context, probe func(context.Context) error) error {
	l.mu.RLock()
	accepting, phase := l.accepting, l.phase
	l.mu.RUnlock()
	if !accepting {
		return fmt.Errorf("%w: phase=%s", errLifecycleNotReady, phase)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if probe != nil {
		if err := probe(ctx); err != nil {
			return err
		}
	}
	l.mu.RLock()
	accepting, phase = l.accepting, l.phase
	l.mu.RUnlock()
	if !accepting {
		return fmt.Errorf("%w: phase=%s", errLifecycleNotReady, phase)
	}
	return nil
}

// Admit reserves a unit of work which shutdown will drain. Admission is
// rejected once readiness has failed, so Wait cannot race with a later Add.
func (l *serverLifecycle) Admit(parent context.Context) (context.Context, func(), error) {
	if parent == nil {
		panic("nil admission context")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.accepting {
		return nil, nil, fmt.Errorf("%w: phase=%s", errLifecycleAdmissionClosed, l.phase)
	}
	l.inflight.Add(1)
	ctx, cancel := context.WithCancel(parent)
	stopWorkCancellation := context.AfterFunc(l.workCtx, cancel)
	var once sync.Once
	release := func() {
		once.Do(func() {
			stopWorkCancellation()
			cancel()
			l.inflight.Done()
		})
	}
	return ctx, release, nil
}

// AdmitClaim tracks request-scoped claim work and also cancels its context as
// soon as graceful shutdown stops claims. This ends existing long-polls before
// the general in-flight drain while ordinary admitted work remains drainable.
func (l *serverLifecycle) AdmitClaim(parent context.Context) (context.Context, func(), error) {
	ctx, releaseWork, err := l.Admit(parent)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	stopClaimCancellation := context.AfterFunc(l.claimsCtx, cancel)
	var once sync.Once
	release := func() {
		once.Do(func() {
			stopClaimCancellation()
			cancel()
			releaseWork()
		})
	}
	return ctx, release, nil
}

// Force stops accepting and claiming, cancels admitted work, and causes an
// active Shutdown to skip the remainder of its drain. Shutdown's context must
// carry a deadline so even a close hook which does not return remains bounded.
func (l *serverLifecycle) Force() {
	l.stopClaims()
	l.forceOnce.Do(func() {
		close(l.forced)
		l.cancelWork()
	})
}

// Shutdown executes the lifecycle once. It rejects new admission, stops claim
// loops, drains admitted work, and invokes closeFn. Concurrent callers wait for
// that same shutdown, subject to their own contexts.
func (l *serverLifecycle) Shutdown(
	ctx context.Context,
	closeFn func(context.Context) error,
) error {
	if ctx == nil {
		panic("nil shutdown context")
	}
	l.startOnce.Do(func() { go l.runShutdown(ctx, closeFn) })
	select {
	case <-l.done:
		l.resultMu.Lock()
		defer l.resultMu.Unlock()
		return l.result
	case <-ctx.Done():
		l.Force()
		<-l.done
		l.resultMu.Lock()
		defer l.resultMu.Unlock()
		return errors.Join(ctx.Err(), l.result)
	}
}

func (l *serverLifecycle) runShutdown(ctx context.Context, closeFn func(context.Context) error) {
	defer close(l.done)
	l.stopClaims()

	drained := make(chan struct{})
	go func() {
		l.inflight.Wait()
		close(drained)
	}()

	var shutdownErr error
	select {
	case <-drained:
	case <-l.forced:
		shutdownErr = errors.New("shutdown forced before in-flight work drained")
	case <-ctx.Done():
		l.cancelWork()
		shutdownErr = ctx.Err()
	}

	l.setPhase(lifecycleClosing)
	if closeFn != nil {
		closed := make(chan error, 1)
		closeStarted := make(chan struct{})
		go func() {
			close(closeStarted)
			closed <- closeFn(ctx)
		}()
		<-closeStarted
		select {
		case err := <-closed:
			shutdownErr = errors.Join(shutdownErr, err)
		case <-l.forced:
			shutdownErr = errors.Join(shutdownErr, errors.New("close forced"))
			select {
			case err := <-closed:
				shutdownErr = errors.Join(shutdownErr, err)
			case <-ctx.Done():
				shutdownErr = errors.Join(shutdownErr, ctx.Err())
			}
		case <-ctx.Done():
			shutdownErr = errors.Join(shutdownErr, ctx.Err())
		}
	}
	l.cancelWork()
	l.setPhase(lifecycleClosed)

	l.resultMu.Lock()
	l.result = shutdownErr
	l.resultMu.Unlock()
}

func (l *serverLifecycle) stopClaims() {
	l.stopOnce.Do(func() {
		l.mu.Lock()
		// Readiness and admission fail while the claim context is still live.
		l.accepting = false
		l.phase = lifecycleStoppingClaims
		l.mu.Unlock()

		l.cancelClaims()
		l.setPhase(lifecycleDraining)
	})
}

func (l *serverLifecycle) setPhase(phase lifecyclePhase) {
	l.mu.Lock()
	if phase > l.phase {
		l.phase = phase
	}
	l.mu.Unlock()
}
