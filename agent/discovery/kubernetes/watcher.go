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
}

func (watcher Watcher) Run(ctx context.Context) error {
	if watcher.Informer == nil || watcher.Publish == nil {
		return errors.New("Kubernetes discovery watcher is not configured")
	}
	updates := make(chan struct{}, 1)
	// A listener is required before Run so the shared informer starts its
	// ListWatch. It intentionally ignores the initial list; that list is
	// published by Source as the only complete observation.
	if _, err := watcher.Informer.AddEventHandler(cache.ResourceEventHandlerFuncs{}); err != nil {
		return err
	}
	go watcher.Informer.Run(ctx.Done())
	if !cache.WaitForCacheSync(ctx.Done(), watcher.Informer.HasSynced) {
		if err := ctx.Err(); err != nil {
			return nil
		}
		return errors.New("Kubernetes discovery informer did not sync")
	}
	_, err := watcher.Informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { enqueue(updates) },
		UpdateFunc: func(any, any) { enqueue(updates) },
		DeleteFunc: func(any) { enqueue(updates) },
	})
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-updates:
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
		}
	}
}

func enqueue(queue chan<- struct{}) {
	select {
	case queue <- struct{}{}:
	default:
	}
}
