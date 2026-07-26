package kubernetes_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	kubernetes "github.com/araihu/xisnove/agent/discovery/kubernetes"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestSourceListsConfiguredResourcesAsOneCompleteSnapshotIncludingEmpty(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset(&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("service-uid")}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}})
	source := kubernetes.Source{Core: client.CoreV1(), Namespaces: []string{"payments"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices}, Perspective: "kubernetes:homelab", Now: func() time.Time { return now }}
	batch, err := source.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Complete || !batch.CompletedAt.Equal(now) || len(batch.Candidates) != 1 || batch.Candidates[0].NetworkPerspective != "kubernetes:homelab" {
		t.Fatalf("batch = %#v", batch)
	}

	empty := kubernetes.Source{Core: fake.NewSimpleClientset().CoreV1(), Namespaces: []string{"payments"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices}, Perspective: "kubernetes:homelab", Now: func() time.Time { return now }}
	batch, err = empty.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !batch.Complete || len(batch.Candidates) != 0 || !batch.CompletedAt.Equal(now) {
		t.Fatalf("empty batch = %#v", batch)
	}
}

func TestSourceNeverCompletesAfterListFailureOrCancellation(t *testing.T) {
	t.Parallel()
	client := fake.NewSimpleClientset()
	denied := errors.New("forbidden")
	client.PrependReactor("list", "services", func(_ k8stesting.Action) (bool, runtime.Object, error) { return true, nil, denied })
	source := kubernetes.Source{Core: client.CoreV1(), Namespaces: []string{"payments"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices}, Perspective: "kubernetes:homelab"}
	if _, err := source.Snapshot(context.Background()); !errors.Is(err, denied) {
		t.Fatalf("error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := source.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
}

func TestSourceSplitsOversizedInventoryIntoPartialBatchesAndDiagnosesIt(t *testing.T) {
	t.Parallel()
	objects := make([]runtime.Object, 0, 501)
	for i := 0; i < 501; i++ {
		objects = append(objects, &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("api-%d", i), Namespace: "payments", UID: types.UID(fmt.Sprintf("uid-%d", i))}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Port: 8080}}}})
	}
	var diagnostics []kubernetes.Diagnostic
	source := kubernetes.Source{Core: fake.NewSimpleClientset(objects...).CoreV1(), Namespaces: []string{"payments"}, Resources: []kubernetes.Resource{kubernetes.ResourceServices}, Perspective: "kubernetes:homelab", OnDiagnostic: func(diagnostic kubernetes.Diagnostic) { diagnostics = append(diagnostics, diagnostic) }}
	batches, err := source.Snapshots(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 2 || batches[0].Complete || batches[1].Complete || len(batches[0].Candidates) != 500 || len(batches[1].Candidates) != 1 {
		t.Fatalf("batches = %#v", batches)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != "inventory-exceeds-batch-limit" {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}
