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
	networkingv1 "k8s.io/api/networking/v1"
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

func TestScopedWatcherRelistCompletesTheFullMultiScopeInventory(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("service-uid")}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}}
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: types.UID("ingress-uid")}, Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{Host: "web.example.test", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/"}}}}}}}}
	client := fake.NewSimpleClientset(service, ingress)
	aggregate := kubernetes.Source{Core: client.CoreV1(), Networking: client.NetworkingV1(), Namespaces: []string{"payments", "default"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices, kubernetes.ResourceIngresses}, Perspective: "kubernetes:homelab"}
	scoped := aggregate
	scoped.Namespaces = []string{"payments"}
	scoped.Resources = []kubernetes.Resource{kubernetes.ResourceServices}
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
	initial, err := aggregate.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	published <- initial // The command publishes this before it starts watchers.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	relistRequests := make(chan struct{}, 1)
	go func() {
		done <- (kubernetes.RelistCoordinator{Source: aggregate, Publish: publishFunc(func(_ context.Context, batch discovery.Batch) error { published <- batch; return nil }), Requests: relistRequests}).Run(ctx)
	}()
	go func() {
		done <- (kubernetes.Watcher{Informer: informer, Source: scoped, Relists: relists, RelistRequests: relistRequests, Publish: publishFunc(func(_ context.Context, batch discovery.Batch) error { published <- batch; return nil })}).Run(ctx)
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
	deadline := time.After(3 * time.Second)
	for {
		select {
		case batch := <-published:
			if batch.Complete {
				if len(batch.Candidates) != 2 {
					t.Fatalf("aggregate relist batch = %#v", batch)
				}
				seen := map[string]bool{}
				for _, candidate := range batch.Candidates {
					seen[candidate.Namespace+"/"+candidate.SourceKind] = true
				}
				if !seen["payments/service"] || !seen["default/ingress"] {
					t.Fatalf("aggregate relist lost a configured scope: %#v", batch.Candidates)
				}
				goto complete
			}
		case <-deadline:
			t.Fatalf("successful relist did not publish the complete aggregate snapshot (lists=%d)", lists.Load())
		}
	}

complete:
	cancel()
	for range 2 {
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("watcher/coordinator did not stop")
		}
	}
}

type publishFunc func(context.Context, discovery.Batch) error

func (publish publishFunc) Publish(ctx context.Context, batch discovery.Batch) error {
	return publish(ctx, batch)
}
