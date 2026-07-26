package kubernetes

import (
	"context"
	"errors"
	"time"

	"github.com/araihu/xisnove/agent/discovery"
	"k8s.io/client-go/tools/cache"
)

// Watcher consumes a client-go informer through a one-slot coalescing queue.
// Each observation is explicitly partial: an informer event, reset, or stop
// can never make the control plane infer that unseen targets are absent.
type Watcher struct {
	Informer cache.SharedIndexInformer
	Source   Source
	Publish  discovery.Publisher
	Relists  <-chan struct{}
	// RelistRequests is shared by every scoped watcher. Only the aggregate
	// coordinator consuming it may publish a complete observation.
	RelistRequests chan<- struct{}
}

// RelistCoordinator coalesces all scoped relist signals and publishes the
// complete, full namespace/resource inventory from Source.
type RelistCoordinator struct {
	Source   Source
	Publish  discovery.Publisher
	Requests <-chan struct{}
}

func (coordinator RelistCoordinator) Run(ctx context.Context) error {
	if coordinator.Publish == nil || coordinator.Requests == nil {
		return errors.New("Kubernetes relist coordinator is not configured")
	}
	runner := discovery.Runner{Producer: coordinator.Source, Publisher: coordinator.Publish}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-coordinator.Requests:
			if err := runner.RunUntilSuccess(ctx, discovery.LoopConfig{MinBackoff: time.Second, MaxBackoff: time.Minute}); err != nil {
				return err
			}
		}
	}
}

func (watcher Watcher) Run(ctx context.Context) error {
	if watcher.Informer == nil || watcher.Publish == nil {
		return errors.New("Kubernetes discovery watcher is not configured")
	}
	updates := make(chan struct{}, 1)
	if _, err := watcher.Informer.AddEventHandler(cache.ResourceEventHandlerDetailedFuncs{
		AddFunc: func(_ any, initial bool) {
			if !initial {
				enqueue(updates)
			}
		},
		UpdateFunc: func(any, any) { enqueue(updates) },
		DeleteFunc: func(any) { enqueue(updates) },
	}); err != nil {
		return err
	}
	go watcher.Informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), watcher.Informer.HasSynced) {
		if err := ctx.Err(); err != nil {
			return nil
		}
		return errors.New("Kubernetes discovery informer did not sync")
	}
	drain(watcher.Relists)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-watcher.Relists:
			requestRelist(watcher.RelistRequests)
		case <-updates:
			select {
			case <-watcher.Relists:
				requestRelist(watcher.RelistRequests)
			default:
			}
			if err := watcher.publish(ctx); err != nil {
				return err
			}
		}
	}
}

func (watcher Watcher) publish(ctx context.Context) error {
	batches, err := watcher.Source.Snapshots(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	for _, batch := range batches {
		batch.Complete = false
		if len(batch.Candidates) == 0 {
			continue
		}
		if err := watcher.Publish.Publish(ctx, batch); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
	}
	return nil
}

func requestRelist(requests chan<- struct{}) {
	if requests == nil {
		return
	}
	select {
	case requests <- struct{}{}:
	default:
	}
}

func enqueue(queue chan<- struct{}) {
	select {
	case queue <- struct{}{}:
	default:
	}
}

func drain(events <-chan struct{}) {
	for events != nil {
		select {
		case <-events:
		default:
			return
		}
	}
}
