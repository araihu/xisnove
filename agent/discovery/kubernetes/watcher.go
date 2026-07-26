package kubernetes

import (
	"context"
	"errors"

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
			if err := watcher.publish(ctx, true); err != nil {
				return err
			}
		case <-updates:
			select {
			case <-watcher.Relists:
				if err := watcher.publish(ctx, true); err != nil {
					return err
				}
			default:
				if err := watcher.publish(ctx, false); err != nil {
					return err
				}
			}
		}
	}
}

func (watcher Watcher) publish(ctx context.Context, complete bool) error {
	batches, err := watcher.Source.Snapshots(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	for _, batch := range batches {
		batch.Complete = complete && batch.Complete
		if len(batch.Candidates) == 0 && !batch.Complete {
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
