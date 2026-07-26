package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLifecycleShutdownRejectsAdmissionThenDrainsBeforeClose(t *testing.T) {
	lifecycle := newServerLifecycle()
	workCtx, release, err := lifecycle.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}

	closed := make(chan struct{})
	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		shutdownDone <- lifecycle.Shutdown(ctx, func(context.Context) error {
			close(closed)
			return nil
		})
	}()

	select {
	case <-lifecycle.ClaimContext().Done():
	case <-time.After(time.Second):
		t.Fatal("claim context was not canceled")
	}
	if err := lifecycle.Ready(context.Background(), nil); !errors.Is(err, errLifecycleNotReady) {
		t.Fatalf("Ready() error = %v, want lifecycle not ready", err)
	}
	if _, _, err := lifecycle.Admit(context.Background()); !errors.Is(err, errLifecycleAdmissionClosed) {
		t.Fatalf("Admit() error = %v, want admission closed", err)
	}
	select {
	case <-workCtx.Done():
		t.Fatal("admitted work canceled during graceful drain")
	case <-closed:
		t.Fatal("resources closed before admitted work drained")
	default:
	}

	release()
	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown did not finish after work drained")
	}
	if lifecycle.Phase() != lifecycleClosed {
		t.Fatalf("Phase() = %s, want closed", lifecycle.Phase())
	}
}

func TestLifecycleShutdownCancelsExistingClaimLongPollBeforeDrain(t *testing.T) {
	lifecycle := newServerLifecycle()
	claimCtx, releaseClaim, err := lifecycle.AdmitClaim(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseClaim()
	ordinaryCtx, releaseOrdinary, err := lifecycle.Admit(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer releaseOrdinary()
	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownDone <- lifecycle.Shutdown(ctx, nil) }()
	select {
	case <-claimCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("claim long-poll context was not canceled")
	}
	select {
	case <-ordinaryCtx.Done():
		t.Fatal("ordinary in-flight work was canceled during graceful drain")
	default:
	}
	releaseClaim()
	releaseOrdinary()
	if err := <-shutdownDone; err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleBlockedReadinessDoesNotBlockShutdown(t *testing.T) {
	lifecycle := newServerLifecycle()
	probeStarted := make(chan struct{})
	allowProbe := make(chan struct{})
	readyDone := make(chan error, 1)
	go func() {
		readyDone <- lifecycle.Ready(context.Background(), func(context.Context) error {
			close(probeStarted)
			<-allowProbe
			return nil
		})
	}()
	<-probeStarted

	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownDone <- lifecycle.Shutdown(ctx, nil) }()
	select {
	case <-lifecycle.ClaimContext().Done():
	case <-time.After(time.Second):
		t.Fatal("blocked readiness probe prevented claims from stopping")
	}
	close(allowProbe)
	if err := <-readyDone; !errors.Is(err, errLifecycleNotReady) {
		t.Fatalf("in-progress Ready() error = %v, want lifecycle not ready", err)
	}

	if err := lifecycle.Ready(context.Background(), nil); !errors.Is(err, errLifecycleNotReady) {
		t.Fatalf("Ready() after claims stopped = %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestLifecycleForceCancelsWorkAndBoundsDrain(t *testing.T) {
	lifecycle := newServerLifecycle()
	workCtx, release, err := lifecycle.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	defer release()

	closeCalled := make(chan struct{})
	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() {
		shutdownDone <- lifecycle.Shutdown(ctx, func(context.Context) error {
			close(closeCalled)
			return nil
		})
	}()
	<-lifecycle.ClaimContext().Done()
	lifecycle.Force()

	select {
	case <-workCtx.Done():
	default:
		t.Fatal("Force returned before canceling admitted work")
	}
	select {
	case err := <-shutdownDone:
		if err == nil {
			t.Fatal("forced Shutdown() returned nil")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Force did not bound the drain")
	}
	select {
	case <-closeCalled:
	default:
		t.Fatal("close hook was not called after force")
	}
}

func TestLifecycleConcurrentForceNeverClosesBeforeClaimsStop(t *testing.T) {
	for range 100 {
		lifecycle := newServerLifecycle()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		start := make(chan struct{})
		closeState := make(chan error, 1)
		shutdownDone := make(chan error, 1)
		forceDone := make(chan struct{})
		go func() {
			<-start
			shutdownDone <- lifecycle.Shutdown(ctx, func(context.Context) error {
				if lifecycle.ClaimContext().Err() == nil {
					closeState <- errors.New("close started before claims stopped")
					return nil
				}
				if err := lifecycle.Ready(context.Background(), nil); !errors.Is(err, errLifecycleNotReady) {
					closeState <- errors.New("close started while lifecycle was ready")
					return nil
				}
				closeState <- nil
				return nil
			})
		}()
		go func() {
			defer close(forceDone)
			<-start
			lifecycle.Force()
		}()
		close(start)
		<-shutdownDone
		<-forceDone
		cancel()
		if err := <-closeState; err != nil {
			t.Fatal(err)
		}
	}
}

func TestLifecycleShutdownDeadlineCancelsWorkAndBoundsClose(t *testing.T) {
	lifecycle := newServerLifecycle()
	workCtx, release, err := lifecycle.Admit(context.Background())
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	defer release()

	closeStarted := make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err = lifecycle.Shutdown(ctx, func(context.Context) error {
		close(closeStarted)
		select {}
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want deadline exceeded", err)
	}
	select {
	case <-workCtx.Done():
	default:
		t.Fatal("deadline did not cancel admitted work")
	}
	select {
	case <-closeStarted:
	case <-time.After(time.Second):
		t.Fatal("close hook was not attempted")
	}
}

func TestLifecycleShutdownCanceledContextSynchronouslyCancelsAdmittedWork(t *testing.T) {
	for range 100 {
		lifecycle := newServerLifecycle()
		workCtx, release, err := lifecycle.Admit(context.Background())
		if err != nil {
			t.Fatalf("Admit() error = %v", err)
		}

		shutdownCtx, cancel := context.WithCancel(context.Background())
		cancel()
		err = lifecycle.Shutdown(shutdownCtx, nil)
		release()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Shutdown() error = %v, want context canceled", err)
		}
		select {
		case <-workCtx.Done():
		default:
			t.Fatal("Shutdown returned before canceling admitted work")
		}
	}
}

func TestLifecycleConcurrentAdmissionAndShutdownIsRaceSafe(t *testing.T) {
	lifecycle := newServerLifecycle()
	var admitted atomic.Int64
	var rejected atomic.Int64
	_, release, err := lifecycle.Admit(context.Background())
	if err != nil {
		t.Fatalf("initial Admit() error = %v", err)
	}
	admitted.Add(1)
	release()
	var workers sync.WaitGroup
	start := make(chan struct{})
	for range 32 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for range 100 {
				_, release, err := lifecycle.Admit(context.Background())
				if errors.Is(err, errLifecycleAdmissionClosed) {
					rejected.Add(1)
					return
				}
				if err != nil {
					t.Errorf("Admit() error = %v", err)
					return
				}
				admitted.Add(1)
				release()
				release()
			}
		}()
	}
	close(start)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := lifecycle.Shutdown(ctx, nil); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	workers.Wait()
	if _, _, err := lifecycle.Admit(context.Background()); errors.Is(err, errLifecycleAdmissionClosed) {
		rejected.Add(1)
	} else {
		t.Fatalf("post-shutdown Admit() error = %v, want admission closed", err)
	}
	if admitted.Load() == 0 || rejected.Load() == 0 {
		t.Fatalf("admitted=%d rejected=%d, want both paths", admitted.Load(), rejected.Load())
	}
}

func TestLifecycleReadyHonorsCanceledContext(t *testing.T) {
	lifecycle := newServerLifecycle()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var called atomic.Bool
	err := lifecycle.Ready(ctx, func(context.Context) error {
		called.Store(true)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Ready() error = %v, want context canceled", err)
	}
	if called.Load() {
		t.Fatal("readiness probe called with canceled context")
	}
}

func TestLifecycleAdmissionPreservesCallerCancellation(t *testing.T) {
	lifecycle := newServerLifecycle()
	parent, cancel := context.WithCancel(context.Background())
	workCtx, release, err := lifecycle.Admit(parent)
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	defer release()
	cancel()
	select {
	case <-workCtx.Done():
		if !errors.Is(workCtx.Err(), context.Canceled) {
			t.Fatalf("work context error = %v", workCtx.Err())
		}
	case <-time.After(time.Second):
		t.Fatal("caller cancellation did not reach admitted work")
	}
}
