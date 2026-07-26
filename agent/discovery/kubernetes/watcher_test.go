package kubernetes_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/araihu/xisnove/agent/discovery"
	kubernetes "github.com/araihu/xisnove/agent/discovery/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"
)

func TestWatcherCoalescesEventsIntoPartialSnapshotsAndStopsOnCancellation(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("service-uid")}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}}
	client := fake.NewSimpleClientset(service)
	stream := watch.NewRaceFreeFake()
	informer := cache.NewSharedIndexInformer(&cache.ListWatch{ListFunc: func(metav1.ListOptions) (runtime.Object, error) {
		return &corev1.ServiceList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}, Items: []corev1.Service{*service}}, nil
	}, WatchFunc: func(metav1.ListOptions) (watch.Interface, error) { return stream, nil }}, &corev1.Service{}, 0, cache.Indexers{})
	published := make(chan discovery.Batch, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (kubernetes.Watcher{Informer: informer, Source: kubernetes.Source{Core: client.CoreV1(), Namespaces: []string{"payments"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices}, Perspective: "kubernetes:homelab"}, Publish: publishFunc(func(_ context.Context, batch discovery.Batch) error { published <- batch; return nil })}).Run(ctx)
	}()

	stream.Add(service)
	deadline := time.After(2 * time.Second)
	for !informer.HasSynced() {
		select {
		case <-deadline:
			t.Fatal("informer did not sync")
		case <-time.After(time.Millisecond):
		}
	}
	stream.Modify(service)
	select {
	case batch := <-published:
		if batch.Complete || len(batch.Candidates) != 1 {
			t.Fatalf("watch batch = %#v", batch)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch update was not published")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

func TestWatcherIgnoresItsInitialListAndPublishesCompleteAfterExpiredResourceVersionRelist(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("service-uid")}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}}
	source := kubernetes.Source{Core: fake.NewSimpleClientset(service).CoreV1(), Namespaces: []string{"payments"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices}, Perspective: "kubernetes:homelab"}
	firstWatch, secondWatch := watch.NewRaceFreeFake(), watch.NewRaceFreeFake()
	relists := make(chan struct{}, 1)
	var lists atomic.Int32
	informer := cache.NewSharedIndexInformer(&cache.ListWatch{ListFunc: func(metav1.ListOptions) (runtime.Object, error) {
		if lists.Add(1) > 1 {
			relists <- struct{}{}
		}
		return &corev1.ServiceList{ListMeta: metav1.ListMeta{ResourceVersion: "1"}, Items: []corev1.Service{*service}}, nil
	}, WatchFunc: func(metav1.ListOptions) (watch.Interface, error) {
		if lists.Load() == 1 {
			return firstWatch, nil
		}
		return secondWatch, nil
	}}, &corev1.Service{}, 0, cache.Indexers{})
	published := make(chan discovery.Batch, 4)
	initial, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	published <- initial // The command publishes this before it starts watchers.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (kubernetes.Watcher{Informer: informer, Source: source, Relists: relists, Publish: publishFunc(func(_ context.Context, batch discovery.Batch) error { published <- batch; return nil })}).Run(ctx)
	}()
	if batch := <-published; !batch.Complete {
		t.Fatalf("initial batch = %#v", batch)
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case batch := <-published:
		t.Fatalf("watcher published before a watch/relist event: %#v", batch)
	default:
	}
	firstWatch.Error(&metav1.Status{Reason: metav1.StatusReasonExpired, Code: 410, Message: "expired"})
	select {
	case batch := <-published:
		if !batch.Complete || len(batch.Candidates) != 1 {
			t.Fatalf("relist batch = %#v", batch)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("successful relist did not publish a complete snapshot (lists=%d)", lists.Load())
	}
	cancel()
	select {
	case err := <-done:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not stop")
	}
}

type publishFunc func(context.Context, discovery.Batch) error

func (publish publishFunc) Publish(ctx context.Context, batch discovery.Batch) error {
	return publish(ctx, batch)
}
