package kubernetes_test

import (
	"context"
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

type publishFunc func(context.Context, discovery.Batch) error

func (publish publishFunc) Publish(ctx context.Context, batch discovery.Batch) error {
	return publish(ctx, batch)
}
