package controller

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestAgentReconcileMaterializesCredentialAndHardenedWorkload(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	agent := validAgent("edge")
	credential := []byte("initial-agent-token")
	remote := &fakeControlPlane{applyAgent: func(_ context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		if request.Owner.Key != "monitoring.xisnove.io/Agent/default/edge/agent-uid" {
			t.Fatalf("owner key = %q", request.Owner.Key)
		}
		return controlplane.AgentState{
			ExternalID:                    "agent-remote-1",
			Credential:                    &controlplane.IssuedCredential{Value: credential, Generation: 1},
			HeartbeatCredentialGeneration: 1,
			LastHeartbeatAt:               time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC),
		}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	reconciler := &AgentReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://xisnove.example.test", DefaultAgentImage: "ghcr.io/araihu/xisnove-agent:test"}

	reconcileAgent(t, reconciler, agent)

	secret := &corev1.Secret{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "edge-credential", Namespace: "default"}, secret); err != nil {
		t.Fatal(err)
	}
	if string(secret.Data[CredentialKey]) != string(credential) {
		t.Fatalf("credential = %q", secret.Data[CredentialKey])
	}
	if _, found := secret.Data[PreviousCredentialKey]; found {
		t.Fatal("new credential Secret unexpectedly has a previous credential")
	}
	if !metav1.IsControlledBy(secret, agent) {
		t.Fatalf("Secret owner references = %#v", secret.OwnerReferences)
	}

	deployment := &appsv1.Deployment{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: "edge", Namespace: "default"}, deployment); err != nil {
		t.Fatal(err)
	}
	pod := deployment.Spec.Template.Spec
	if pod.ServiceAccountName != "edge-discovery" || pod.AutomountServiceAccountToken == nil || !*pod.AutomountServiceAccountToken {
		t.Fatalf("unexpected ServiceAccount configuration: %#v", pod)
	}
	container := pod.Containers[0]
	if container.Image != "ghcr.io/araihu/xisnove-agent:test" {
		t.Fatalf("image = %q", container.Image)
	}
	if container.SecurityContext == nil || container.SecurityContext.ReadOnlyRootFilesystem == nil || !*container.SecurityContext.ReadOnlyRootFilesystem {
		t.Fatalf("container security context = %#v", container.SecurityContext)
	}
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsNonRoot == nil || !*pod.SecurityContext.RunAsNonRoot {
		t.Fatalf("pod security context = %#v", pod.SecurityContext)
	}

	got := &monitoringv1alpha1.Agent{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ExternalID != "agent-remote-1" || got.Status.CredentialGeneration != 1 {
		t.Fatalf("status = %#v", got.Status)
	}
	statusJSON, err := json.Marshal(got.Status)
	if err != nil {
		t.Fatal(err)
	}
	if containsBytes(statusJSON, credential) {
		t.Fatalf("status leaked credential: %s", statusJSON)
	}
	assertCondition(t, got.Status.Conditions, ConditionRegistered, metav1.ConditionTrue, "Registered")
	assertCondition(t, got.Status.Conditions, ConditionWorkloadReady, metav1.ConditionTrue, "DeploymentApplied")
	resourceVersion := got.ResourceVersion
	reconcileAgent(t, reconciler, agent)
	if err := kube.Get(context.Background(), types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if got.ResourceVersion != resourceVersion {
		t.Fatalf("unchanged Agent reconciliation wrote status: resourceVersion %s -> %s", resourceVersion, got.ResourceVersion)
	}
}

func TestAgentRotationKeepsOverlapUntilHeartbeatThenRevokesOldGeneration(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	agent := validAgent("rotating")
	agent.Finalizers = []string{AgentFinalizer}
	agent.Status.ExternalID = "agent-remote-1"
	agent.Status.CredentialGeneration = 1
	agent.Spec.CredentialRotation.RequestedGeneration = 2
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "rotating-credential", Namespace: "default", Annotations: map[string]string{CredentialGenerationAnnotation: "1"}},
		Data:       map[string][]byte{CredentialKey: []byte("old-token")},
	}
	if err := controllerutil.SetControllerReference(agent, secret, scheme); err != nil {
		t.Fatal(err)
	}
	heartbeatGeneration := int64(1)
	issued := 0
	revoked := int64(0)
	remote := &fakeControlPlane{
		applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
			return controlplane.AgentState{ExternalID: "agent-remote-1", HeartbeatCredentialGeneration: heartbeatGeneration}, nil
		},
		issueCredential: func(_ context.Context, request controlplane.IssueAgentCredentialRequest) (controlplane.IssuedCredential, error) {
			issued++
			if request.RequestedGeneration != 2 || request.IdempotencyKey == "" {
				t.Fatalf("unsafe rotation request = %#v", request)
			}
			return controlplane.IssuedCredential{Value: []byte("new-token"), Generation: 2}, nil
		},
		revokeCredential: func(_ context.Context, request controlplane.RevokeAgentCredentialRequest) error {
			revoked = request.Generation
			return nil
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret).Build()
	reconciler := &AgentReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://xisnove.example.test", DefaultAgentImage: "agent:test"}

	result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}})
	if err != nil {
		t.Fatal(err)
	}
	if result.RequeueAfter <= 0 {
		t.Fatal("rotation awaiting heartbeat did not schedule a fresh observation")
	}
	rotated := &corev1.Secret{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, rotated); err != nil {
		t.Fatal(err)
	}
	if string(rotated.Data[CredentialKey]) != "new-token" || string(rotated.Data[PreviousCredentialKey]) != "old-token" {
		t.Fatalf("overlap Secret data = %#v", rotated.Data)
	}
	if revoked != 0 {
		t.Fatalf("old generation revoked before heartbeat: %d", revoked)
	}
	if issued != 1 {
		t.Fatalf("credential issuances = %d, want 1", issued)
	}

	heartbeatGeneration = 2
	reconcileAgent(t, reconciler, agent)
	completed := &corev1.Secret{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, completed); err != nil {
		t.Fatal(err)
	}
	if _, found := completed.Data[PreviousCredentialKey]; found {
		t.Fatal("previous credential retained after confirmed heartbeat")
	}
	if revoked != 1 {
		t.Fatalf("revoked generation = %d, want 1", revoked)
	}
	if issued != 1 {
		t.Fatalf("credential reissued during retry: %d", issued)
	}
	got := &monitoringv1alpha1.Agent{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	if got.Status.RotationPhase != monitoringv1alpha1.RotationPhaseComplete || got.Status.PreviousCredentialGeneration != nil {
		t.Fatalf("rotation status = %#v", got.Status)
	}
}

func TestAgentRefusesToAdoptAnUnownedCredentialSecret(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	agent := validAgent("safe")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "safe-credential", Namespace: "default", Annotations: map[string]string{CredentialGenerationAnnotation: "1"}},
		Data:       map[string][]byte{CredentialKey: []byte("somebody-elses-token")},
	}
	remoteCalled := false
	remote := &fakeControlPlane{applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		remoteCalled = true
		return controlplane.AgentState{}, errors.New("must not be called")
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret).Build()
	reconciler := &AgentReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://xisnove.example.test", DefaultAgentImage: "agent:test"}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}})
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("reconcile error = %v, want unowned Secret refusal", err)
	}
	if remoteCalled {
		t.Fatal("remote Agent state was mutated before Secret ownership was validated")
	}
	got := &corev1.Secret{}
	if getErr := kube.Get(context.Background(), types.NamespacedName{Name: secret.Name, Namespace: secret.Namespace}, got); getErr != nil {
		t.Fatal(getErr)
	}
	if metav1.IsControlledBy(got, agent) {
		t.Fatal("operator adopted an existing unowned Secret")
	}
}

func TestAgentRefusesToAdoptAnUnownedDeployment(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	agent := validAgent("occupied")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "occupied-credential", Namespace: "default", Annotations: map[string]string{CredentialGenerationAnnotation: "1"}},
		Data:       map[string][]byte{CredentialKey: []byte("owned-token")},
	}
	if err := controllerutil.SetControllerReference(agent, secret, scheme); err != nil {
		t.Fatal(err)
	}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "occupied", Namespace: "default"}}
	remote := &fakeControlPlane{applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		return controlplane.AgentState{ExternalID: "agent-remote-1"}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret, deployment).Build()
	reconciler := &AgentReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://xisnove.example.test", DefaultAgentImage: "agent:test"}

	_, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}})
	if err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("reconcile error = %v, want unowned Deployment refusal", err)
	}
	got := &appsv1.Deployment{}
	if getErr := kube.Get(context.Background(), types.NamespacedName{Name: deployment.Name, Namespace: deployment.Namespace}, got); getErr != nil {
		t.Fatal(getErr)
	}
	if metav1.IsControlledBy(got, agent) {
		t.Fatal("operator adopted an existing unowned Deployment")
	}
}

func TestAgentReportsStaleDiscoveryAsDegradedWithoutCreatingAlertState(t *testing.T) {
	t.Parallel()

	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	agent := validAgent("stale")
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "stale-credential", Namespace: "default", Annotations: map[string]string{CredentialGenerationAnnotation: "1"}},
		Data:       map[string][]byte{CredentialKey: []byte("owned-token")},
	}
	if err := controllerutil.SetControllerReference(agent, secret, scheme); err != nil {
		t.Fatal(err)
	}
	remote := &fakeControlPlane{applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		return controlplane.AgentState{
			ExternalID:                    "agent-remote-1",
			HeartbeatCredentialGeneration: 1,
			LastHeartbeatAt:               now,
			LastDiscoverySyncAt:           now.Add(-10 * time.Minute),
		}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret).Build()
	reconciler := &AgentReconciler{Client: kube, Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://xisnove.example.test", DefaultAgentImage: "agent:test", Now: func() time.Time { return now }}

	reconcileAgent(t, reconciler, agent)
	got := &monitoringv1alpha1.Agent{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}, got); err != nil {
		t.Fatal(err)
	}
	assertCondition(t, got.Status.Conditions, ConditionDiscoveryFresh, metav1.ConditionFalse, "Stale")
	assertCondition(t, got.Status.Conditions, ConditionReady, metav1.ConditionFalse, "DiscoveryStale")
	assertCondition(t, got.Status.Conditions, ConditionDegraded, metav1.ConditionTrue, "DiscoveryStale")
}

func validAgent(name string) *monitoringv1alpha1.Agent {
	return &monitoringv1alpha1.Agent{
		TypeMeta:   metav1.TypeMeta{APIVersion: monitoringv1alpha1.GroupVersion.String(), Kind: "Agent"},
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("agent-uid")},
		Spec: monitoringv1alpha1.AgentSpec{
			LocationID:          "11111111-1111-1111-1111-111111111111",
			Capabilities:        []monitoringv1alpha1.AgentCapability{monitoringv1alpha1.AgentCapabilityHTTP, monitoringv1alpha1.AgentCapabilityKubernetesDiscovery},
			CredentialSecretRef: monitoringv1alpha1.SecretKeyReference{Name: name + "-credential", Key: CredentialKey},
			CredentialRotation:  monitoringv1alpha1.AgentCredentialRotationSpec{RequestedGeneration: 1},
			Discovery: monitoringv1alpha1.AgentDiscoverySpec{
				Namespaces:        []string{"default"},
				Resources:         []monitoringv1alpha1.DiscoveryResource{monitoringv1alpha1.DiscoveryResourceService, monitoringv1alpha1.DiscoveryResourceIngress, monitoringv1alpha1.DiscoveryResourceHTTPRoute, monitoringv1alpha1.DiscoveryResourceGRPCRoute},
				StaleAfterSeconds: 300,
			},
			Workload: monitoringv1alpha1.AgentWorkloadSpec{ServiceAccountName: name + "-discovery"},
		},
	}
}

func reconcileAgent(t *testing.T, reconciler *AgentReconciler, agent *monitoringv1alpha1.Agent) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}}); err != nil {
		t.Fatal(err)
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(haystack) < len(needle) {
		return false
	}
	for offset := 0; offset <= len(haystack)-len(needle); offset++ {
		match := true
		for index := range needle {
			if haystack[offset+index] != needle[index] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
