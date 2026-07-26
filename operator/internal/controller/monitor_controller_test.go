package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestMonitorReconcileUsesStableOwnershipAndBoundedStatus(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	monitor := validMonitor("payments")
	monitor.Generation = 7
	var requests []controlplane.ApplyMonitorRequest
	remote := &fakeControlPlane{
		applyMonitor: func(_ context.Context, request controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
			requests = append(requests, request)
			return controlplane.MonitorState{
				ExternalID:             "monitor-remote-1",
				AggregateHealth:        "up",
				HealthLastTransitionAt: time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
			}, nil
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Monitor{}).WithObjects(monitor).Build()
	reconciler := &MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote}

	reconcileMonitor(t, reconciler, monitor)
	afterFirst := &monitoringv1alpha1.Monitor{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}, afterFirst); err != nil {
		t.Fatal(err)
	}
	reconcileMonitor(t, reconciler, monitor)

	if len(requests) != 2 {
		t.Fatalf("apply calls = %d, want 2", len(requests))
	}
	wantOwner := "monitoring.xisnove.io/Monitor/default/payments"
	for index, request := range requests {
		if request.Owner.Key != wantOwner {
			t.Fatalf("request[%d] owner = %q, want %q", index, request.Owner.Key, wantOwner)
		}
		if request.Owner.UID != "monitor-uid" {
			t.Fatalf("request[%d] owner UID = %q, want monitor-uid", index, request.Owner.UID)
		}
		if request.IdempotencyKey == "" || len(request.IdempotencyKey) > 200 {
			t.Fatalf("request[%d] idempotency key=%q", index, request.IdempotencyKey)
		}
	}
	if requests[1].ExternalID != "monitor-remote-1" {
		t.Fatalf("second external ID = %q, want stable remote ID", requests[1].ExternalID)
	}

	got := &monitoringv1alpha1.Monitor{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if !containsString(got.Finalizers, MonitorFinalizer) {
		t.Fatalf("finalizers = %#v, want %q", got.Finalizers, MonitorFinalizer)
	}
	if got.Status.ObservedGeneration != 7 || got.Status.ExternalID != "monitor-remote-1" {
		t.Fatalf("status = %#v", got.Status)
	}
	if got.ResourceVersion != afterFirst.ResourceVersion {
		t.Fatalf("unchanged reconciliation wrote status: resourceVersion %s -> %s", afterFirst.ResourceVersion, got.ResourceVersion)
	}
	assertCondition(t, got.Status.Conditions, ConditionReady, metav1.ConditionTrue, "Reconciled")
	assertCondition(t, got.Status.Conditions, ConditionSynced, metav1.ConditionTrue, "Applied")
	assertCondition(t, got.Status.Conditions, ConditionDegraded, metav1.ConditionFalse, "Healthy")
}

func TestMonitorHealthConditionTruthTable(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		state string
		want  metav1.ConditionStatus
	}{
		{state: "pending", want: metav1.ConditionUnknown},
		{state: "unknown", want: metav1.ConditionUnknown},
		{state: "up", want: metav1.ConditionFalse},
		{state: "down", want: metav1.ConditionTrue},
		{state: "degraded", want: metav1.ConditionTrue},
	} {
		test := test
		t.Run(test.state, func(t *testing.T) {
			got, _, _ := monitorDegradedCondition(test.state)
			if got != test.want {
				t.Fatalf("Degraded(%q) = %s, want %s", test.state, got, test.want)
			}
		})
	}
}

func TestMonitorDeletionKeepsFinalizerWhenOwnershipCannotBeProven(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	monitor := validMonitor("owned")
	now := metav1.Now()
	monitor.DeletionTimestamp = &now
	monitor.Finalizers = []string{MonitorFinalizer}
	monitor.Status.ExternalID = "monitor-remote-1"
	remote := &fakeControlPlane{
		deleteMonitor: func(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
			if request.Owner.Key == "" || request.ExternalID == "" {
				t.Fatalf("unsafe delete request = %#v", request)
			}
			return controlplane.ErrOwnershipConflict
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Monitor{}).WithObjects(monitor).Build()
	reconciler := &MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}})
	if !errors.Is(err, controlplane.ErrOwnershipConflict) {
		t.Fatalf("reconcile error = %v, want ownership conflict", err)
	}

	got := &monitoringv1alpha1.Monitor{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if !containsString(got.Finalizers, MonitorFinalizer) {
		t.Fatalf("finalizer removed after unsafe delete: %#v", got.Finalizers)
	}
}

func TestMonitorForceRemovalDoesNotDeleteRemoteObject(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	monitor := validMonitor("abandoned")
	now := metav1.Now()
	monitor.DeletionTimestamp = &now
	monitor.Finalizers = []string{MonitorFinalizer}
	monitor.Annotations = map[string]string{ForceDeleteAnnotation: "true"}
	deleted := false
	remote := &fakeControlPlane{deleteMonitor: func(context.Context, controlplane.DeleteRemoteObjectRequest) error {
		deleted = true
		return nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Monitor{}).WithObjects(monitor).Build()
	reconciler := &MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote}

	reconcileMonitor(t, reconciler, monitor)
	if deleted {
		t.Fatal("force removal attempted a remote delete")
	}
}

func TestMonitorDeletionUsesStableOwnerWhenStatusWriteLostExternalID(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	monitor := validMonitor("status-lost")
	now := metav1.Now()
	monitor.DeletionTimestamp = &now
	monitor.Finalizers = []string{MonitorFinalizer}
	called := false
	remote := &fakeControlPlane{deleteMonitor: func(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
		called = true
		if request.ExternalID != "" || request.Owner.Key == "" {
			t.Fatalf("owner-only delete request = %#v", request)
		}
		return controlplane.ErrNotFound
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Monitor{}).WithObjects(monitor).Build()
	reconciler := &MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote}

	reconcileMonitor(t, reconciler, monitor)
	if !called {
		t.Fatal("finalizer removed without checking for an owner-keyed remote object")
	}
}

func TestMonitorErrorConditionIsBoundedAndRedacted(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	monitor := validMonitor("broken")
	remote := &fakeControlPlane{applyMonitor: func(context.Context, controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
		return controlplane.MonitorState{}, errors.New("Authorization: Bearer super-secret-token " + strings.Repeat("x", 600))
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Monitor{}).WithObjects(monitor).Build()
	reconciler := &MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}})
	if err == nil {
		t.Fatal("reconcile error = nil, want failure")
	}
	if strings.Contains(err.Error(), "super-secret-token") {
		t.Fatalf("returned error leaked token: %v", err)
	}

	got := &monitoringv1alpha1.Monitor{}
	if getErr := kube.Get(context.Background(), types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}, got); getErr != nil {
		t.Fatal(getErr)
	}
	degraded := findCondition(got.Status.Conditions, ConditionDegraded)
	if degraded == nil {
		t.Fatal("missing Degraded condition")
	}
	if len(degraded.Message) > MaxConditionMessageLength || strings.Contains(degraded.Message, "super-secret-token") {
		t.Fatalf("unsafe condition message (%d bytes): %q", len(degraded.Message), degraded.Message)
	}
}

func TestMonitorReconcileSchedulesFreshHealthObservation(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	monitor := validMonitor("polling")
	remote := &fakeControlPlane{applyMonitor: func(context.Context, controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
		return controlplane.MonitorState{ExternalID: "monitor-remote-1", AggregateHealth: "up"}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Monitor{}).WithObjects(monitor).Build()
	reconciler := &MonitorReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, PollInterval: 45 * time.Second}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter != 45*time.Second {
		t.Fatalf("requeue = %s, want 45s", result.RequeueAfter)
	}
}

func validMonitor(name string) *monitoringv1alpha1.Monitor {
	return &monitoringv1alpha1.Monitor{
		TypeMeta:   metav1.TypeMeta{APIVersion: monitoringv1alpha1.GroupVersion.String(), Kind: "Monitor"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("monitor-uid")},
		Spec: monitoringv1alpha1.MonitorSpec{
			LocationID:        "11111111-1111-1111-1111-111111111111",
			IntervalSeconds:   30,
			TimeoutMillis:     5000,
			FailureThreshold:  3,
			RecoveryThreshold: 2,
			RequiredLocation:  true,
			Probe: monitoringv1alpha1.MonitorProbeSpec{
				Kind: "http",
				HTTP: &monitoringv1alpha1.HTTPProbeSpec{URL: "https://example.test/health", Method: "GET", ExpectedStatus: []monitoringv1alpha1.StatusRange{{Minimum: 200, Maximum: 299}}},
			},
		},
	}
}

func reconcileMonitor(t *testing.T, reconciler *MonitorReconciler, monitor *monitoringv1alpha1.Monitor) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: monitor.Name, Namespace: monitor.Namespace}}); err != nil {
		t.Fatal(err)
	}
}

func testScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := monitoringv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func assertCondition(t *testing.T, conditions []metav1.Condition, conditionType string, status metav1.ConditionStatus, reason string) {
	t.Helper()
	condition := findCondition(conditions, conditionType)
	if condition == nil {
		t.Fatalf("condition %s not found in %#v", conditionType, conditions)
	}
	if condition.Status != status || condition.Reason != reason {
		t.Fatalf("condition %s = %#v, want status=%s reason=%s", conditionType, condition, status, reason)
	}
}

func findCondition(conditions []metav1.Condition, conditionType string) *metav1.Condition {
	for index := range conditions {
		if conditions[index].Type == conditionType {
			return &conditions[index]
		}
	}
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

type fakeControlPlane struct {
	applyMonitor     func(context.Context, controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error)
	deleteMonitor    func(context.Context, controlplane.DeleteRemoteObjectRequest) error
	applyAgent       func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error)
	observeAgent     func(context.Context, controlplane.ObserveAgentRequest) (controlplane.AgentState, error)
	putCredential    func(context.Context, controlplane.PutAgentCredentialRequest) error
	revokeCredential func(context.Context, controlplane.RevokeAgentCredentialRequest) error
	deleteAgent      func(context.Context, controlplane.DeleteRemoteObjectRequest) error
}

func (f *fakeControlPlane) ObserveAgent(ctx context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
	if f.observeAgent == nil {
		return controlplane.AgentState{}, errors.New("unexpected ObserveAgent call")
	}
	return f.observeAgent(ctx, request)
}

func (f *fakeControlPlane) ApplyMonitor(ctx context.Context, request controlplane.ApplyMonitorRequest) (controlplane.MonitorState, error) {
	if f.applyMonitor == nil {
		return controlplane.MonitorState{}, errors.New("unexpected ApplyMonitor call")
	}
	return f.applyMonitor(ctx, request)
}

func (f *fakeControlPlane) DeleteMonitor(ctx context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	if f.deleteMonitor == nil {
		return errors.New("unexpected DeleteMonitor call")
	}
	return f.deleteMonitor(ctx, request)
}

func (f *fakeControlPlane) ApplyAgent(ctx context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
	if f.applyAgent == nil {
		return controlplane.AgentState{}, errors.New("unexpected ApplyAgent call")
	}
	return f.applyAgent(ctx, request)
}

func (f *fakeControlPlane) PutAgentCredential(ctx context.Context, request controlplane.PutAgentCredentialRequest) error {
	if f.putCredential == nil {
		return errors.New("unexpected PutAgentCredential call")
	}
	return f.putCredential(ctx, request)
}

func (f *fakeControlPlane) RevokeAgentCredential(ctx context.Context, request controlplane.RevokeAgentCredentialRequest) error {
	if f.revokeCredential == nil {
		return errors.New("unexpected RevokeAgentCredential call")
	}
	return f.revokeCredential(ctx, request)
}

func (f *fakeControlPlane) DeleteAgent(ctx context.Context, request controlplane.DeleteRemoteObjectRequest) error {
	if f.deleteAgent == nil {
		return errors.New("unexpected DeleteAgent call")
	}
	return f.deleteAgent(ctx, request)
}
