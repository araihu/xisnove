package controller

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

func TestAgentCredentialStagesLocallyBeforeRemoteApply(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("stage")
	var firstCredential string
	attempts := 0
	remote := &fakeControlPlane{applyAgent: func(_ context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		attempts++
		if request.Owner.Key != "monitoring.xisnove.io/Agent/default/stage" || request.Owner.UID != "agent-uid" {
			t.Fatalf("owner = %#v", request.Owner)
		}
		if attempts == 1 {
			firstCredential = string(request.InitialCredential)
			return controlplane.AgentState{}, errors.New("temporary remote failure")
		}
		if got := string(request.InitialCredential); got != firstCredential {
			t.Fatalf("retry credential changed: %q != %q", got, firstCredential)
		}
		return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 1}, nil
	}, observeAgent: func(_ context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
		return controlplane.AgentState{ExternalID: request.ExternalID, CredentialGeneration: 1}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	r := testAgentReconciler(kube, scheme, remote)

	if _, err := r.Reconcile(context.Background(), requestFor(agent)); err == nil {
		t.Fatal("first reconcile unexpectedly succeeded")
	}
	staged := getCredentialSecret(t, kube, agent)
	if _, current, err := credentialState(staged, CredentialKey); err != nil || current == nil {
		t.Fatalf("staged Secret = %#v, %v", staged.Data, err)
	}
	if _, found := staged.Data[CredentialKey]; found {
		t.Fatal("pre-staged credential was mounted as current")
	}

	reconcileAgent(t, r, agent)
	promoted := getCredentialSecret(t, kube, agent)
	current, next, err := credentialState(promoted, CredentialKey)
	if err != nil || current == nil || next != nil {
		t.Fatalf("promoted credential state current=%#v next=%#v err=%v", current, next, err)
	}
	if current.Credential != firstCredential || current.Generation != 1 {
		t.Fatalf("promoted bundle = %#v", current)
	}
	if attempts != 2 {
		t.Fatalf("apply attempts = %d, want 2", attempts)
	}
}

func TestAgentCredentialRotationPromotesOnlyAfterPutAndRevokesAfterHeartbeat(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	agent := validAgent("rotate")
	heartbeatGeneration := int64(1)
	puts := 0
	revokes := 0
	remote := &fakeControlPlane{
		applyAgent: func(_ context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
			return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 2, PresentedCredentialGeneration: heartbeatGeneration, LastHeartbeatAt: now, LastDiscoverySyncAt: now}, nil
		},
		observeAgent: func(_ context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
			return controlplane.AgentState{ExternalID: request.ExternalID, CredentialGeneration: 2, PresentedCredentialGeneration: heartbeatGeneration, LastHeartbeatAt: now, LastDiscoverySyncAt: now}, nil
		},
		putCredential: func(_ context.Context, request controlplane.PutAgentCredentialRequest) error {
			puts++
			if request.Generation != 2 || len(request.Credential) < 32 || request.Owner.UID != "agent-uid" {
				t.Fatalf("put = %#v", request)
			}
			return nil
		},
		revokeCredential: func(_ context.Context, request controlplane.RevokeAgentCredentialRequest) error {
			revokes++
			if request.Generation != 1 || request.Owner.UID != "agent-uid" {
				t.Fatalf("revoke = %#v", request)
			}
			return nil
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	r := testAgentReconciler(kube, scheme, remote)
	r.Now = func() time.Time { return now }
	reconcileAgent(t, r, agent)

	stored := &monitoringv1alpha1.Agent{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}, stored); err != nil {
		t.Fatal(err)
	}
	stored.Spec.CredentialRotation.RequestedGeneration = 2
	if err := kube.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	reconcileAgent(t, r, agent)
	rotated := getCredentialSecret(t, kube, agent)
	current, next, err := credentialState(rotated, CredentialKey)
	if err != nil || current == nil || next != nil || current.Generation != 2 || previousCredentialGeneration(rotated) != 1 {
		t.Fatalf("rotation state current=%#v next=%#v previous=%d err=%v", current, next, previousCredentialGeneration(rotated), err)
	}
	if puts != 1 || revokes != 0 {
		t.Fatalf("puts=%d revokes=%d, want 1,0", puts, revokes)
	}
	if _, found := rotated.Data[NextCredentialKey]; found {
		t.Fatal("next credential remained after successful put")
	}

	heartbeatGeneration = 2
	reconcileAgent(t, r, agent)
	completed := getCredentialSecret(t, kube, agent)
	if _, found := completed.Data[PreviousCredentialKey]; found {
		t.Fatal("previous credential remained after replacement heartbeat and revoke")
	}
	if revokes != 1 {
		t.Fatalf("revokes=%d, want 1", revokes)
	}
}

func TestAgentCredentialPutFailureRetainsUnmountedNextForRetry(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("retry-put")
	putAttempts := 0
	var staged string
	remote := &fakeControlPlane{
		applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
			return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 1}, nil
		},
		observeAgent: func(_ context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
			return controlplane.AgentState{ExternalID: request.ExternalID, CredentialGeneration: 1}, nil
		},
		putCredential: func(_ context.Context, request controlplane.PutAgentCredentialRequest) error {
			putAttempts++
			if putAttempts == 1 {
				staged = string(request.Credential)
				return errors.New("put interrupted")
			}
			if string(request.Credential) != staged {
				t.Fatalf("retry credential changed")
			}
			return nil
		},
	}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	r := testAgentReconciler(kube, scheme, remote)
	reconcileAgent(t, r, agent)
	stored := &monitoringv1alpha1.Agent{}
	if err := kube.Get(context.Background(), types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}, stored); err != nil {
		t.Fatal(err)
	}
	stored.Spec.CredentialRotation.RequestedGeneration = 2
	if err := kube.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), requestFor(agent)); err == nil {
		t.Fatal("put failure unexpectedly succeeded")
	}
	pending := getCredentialSecret(t, kube, agent)
	current, next, err := credentialState(pending, CredentialKey)
	if err != nil || current == nil || next == nil || current.Generation != 1 || next.Generation != 2 {
		t.Fatalf("pending state current=%#v next=%#v err=%v", current, next, err)
	}
	reconcileAgent(t, r, agent)
	if putAttempts != 2 {
		t.Fatalf("put attempts=%d, want 2", putAttempts)
	}
}

func TestAgentCredentialApplyCrashRetainsNextUntilPromotionRetries(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("apply-crash")
	applyCalls := 0
	var credential string
	remote := &fakeControlPlane{applyAgent: func(_ context.Context, request controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		applyCalls++
		if applyCalls == 1 {
			credential = string(request.InitialCredential)
		} else if string(request.InitialCredential) != credential {
			t.Fatal("apply retry generated a different credential")
		}
		return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 1}, nil
	}, observeAgent: func(_ context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
		return controlplane.AgentState{ExternalID: request.ExternalID, CredentialGeneration: 1}, nil
	}}
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	flaky := &failSecretUpdateClient{Client: base, failures: 1}
	r := testAgentReconciler(flaky, scheme, remote)
	r.APIReader = base
	if _, err := r.Reconcile(context.Background(), requestFor(agent)); err == nil {
		t.Fatal("promotion write crash unexpectedly succeeded")
	}
	staged := getCredentialSecret(t, base, agent)
	if _, next, err := credentialState(staged, CredentialKey); err != nil || next == nil {
		t.Fatalf("next after apply crash = %#v, %v", staged.Data, err)
	}
	reconcileAgent(t, r, agent)
	promoted := getCredentialSecret(t, base, agent)
	current, next, err := credentialState(promoted, CredentialKey)
	if err != nil || current == nil || next != nil || current.Credential != credential {
		t.Fatalf("promotion retry current=%#v next=%#v err=%v", current, next, err)
	}
}

func TestAgentCredentialRevokeCrashKeepsPreviousForIdempotentRetry(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	agent := validAgent("revoke-crash")
	heartbeatGeneration := int64(1)
	revokes := 0
	observes := 0
	remote := &fakeControlPlane{
		applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
			return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 2, PresentedCredentialGeneration: heartbeatGeneration, LastHeartbeatAt: now}, nil
		},
		putCredential: func(context.Context, controlplane.PutAgentCredentialRequest) error { return nil },
		observeAgent: func(_ context.Context, request controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
			observes++
			return controlplane.AgentState{ExternalID: request.ExternalID, CredentialGeneration: 2, PresentedCredentialGeneration: heartbeatGeneration, LastHeartbeatAt: now}, nil
		},
		revokeCredential: func(context.Context, controlplane.RevokeAgentCredentialRequest) error { revokes++; return nil },
	}
	base := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	flaky := &failSecretUpdateClient{Client: base}
	r := testAgentReconciler(flaky, scheme, remote)
	r.APIReader, r.Now = base, func() time.Time { return now }
	reconcileAgent(t, r, agent)
	stored := &monitoringv1alpha1.Agent{}
	if err := base.Get(context.Background(), types.NamespacedName{Namespace: agent.Namespace, Name: agent.Name}, stored); err != nil {
		t.Fatal(err)
	}
	stored.Spec.CredentialRotation.RequestedGeneration = 2
	if err := base.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	reconcileAgent(t, r, agent)
	heartbeatGeneration = 2
	flaky.failures = 1
	if _, err := r.Reconcile(context.Background(), requestFor(agent)); err == nil {
		t.Fatal("post-revoke Secret write crash unexpectedly succeeded")
	}
	pending := getCredentialSecret(t, base, agent)
	if _, found := pending.Data[PreviousCredentialKey]; !found {
		t.Fatal("previous credential was lost after revoke write crash")
	}
	reconcileAgent(t, r, agent)
	completed := getCredentialSecret(t, base, agent)
	if _, found := completed.Data[PreviousCredentialKey]; found {
		t.Fatal("previous credential remained after idempotent revoke retry")
	}
	if revokes != 2 {
		t.Fatalf("revokes=%d, want 2", revokes)
	}
	reconcileAgent(t, r, agent)
	if observes < 1 {
		t.Fatalf("observes=%d, want steady observations", observes)
	}
}

func TestAgentRefusesCredentialSecretOwnedByPriorUID(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("orphan")
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "orphan-credential", Namespace: "default", OwnerReferences: []metav1.OwnerReference{{APIVersion: monitoringv1alpha1.GroupVersion.String(), Kind: "Agent", Name: "orphan", UID: "old-agent-uid", Controller: boolPointer(true)}}}, Data: map[string][]byte{CredentialKey: []byte(`{"credential":"credential-00000000000000000000000000000001","generation":1}`)}}
	remoteCalled := false
	remote := &fakeControlPlane{applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		remoteCalled = true
		return controlplane.AgentState{}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret).Build()
	r := testAgentReconciler(kube, scheme, remote)
	if _, err := r.Reconcile(context.Background(), requestFor(agent)); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("reconcile error=%v", err)
	}
	if remoteCalled {
		t.Fatal("remote state changed before ownership refusal")
	}
}

func TestAgentFinalizerUsesOwnerOnlyRecovery(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("delete")
	now := metav1.Now()
	agent.DeletionTimestamp = &now
	agent.Finalizers = []string{AgentFinalizer}
	called := false
	remote := &fakeControlPlane{deleteAgent: func(_ context.Context, request controlplane.DeleteRemoteObjectRequest) error {
		called = true
		if request.ExternalID != "" || request.Owner.Key != "monitoring.xisnove.io/Agent/default/delete" || request.Owner.UID != "agent-uid" {
			t.Fatalf("delete = %#v", request)
		}
		return controlplane.ErrNotFound
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent).Build()
	r := testAgentReconciler(kube, scheme, remote)
	reconcileAgent(t, r, agent)
	if !called {
		t.Fatal("finalizer skipped owner-only remote recovery")
	}
}

func TestAgentConditionsUseDeploymentAndFreshnessObservations(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	agent := validAgent("observed")
	agent.Generation = 3
	r := &AgentReconciler{Now: func() time.Time { return now }, HeartbeatStaleAfter: time.Minute}
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Generation: 4}, Status: appsv1.DeploymentStatus{ObservedGeneration: 3}}
	r.recordAgentSuccess(agent, controlplane.AgentState{}, deployment)
	assertCondition(t, agent.Status.Conditions, ConditionWorkloadReady, metav1.ConditionUnknown, "Progressing")
	assertCondition(t, agent.Status.Conditions, ConditionHeartbeat, metav1.ConditionFalse, "AwaitingHeartbeat")

	heartbeat := now.Add(-2 * time.Minute)
	discovery := now.Add(-10 * time.Minute)
	agent.Status.LastHeartbeatTime = &metav1.Time{Time: heartbeat}
	agent.Status.LastDiscoverySyncTime = &metav1.Time{Time: discovery}
	deployment.Status.ObservedGeneration = 4
	deployment.Status.AvailableReplicas = 1
	r.recordAgentSuccess(agent, controlplane.AgentState{}, deployment)
	assertCondition(t, agent.Status.Conditions, ConditionWorkloadReady, metav1.ConditionTrue, "Available")
	assertCondition(t, agent.Status.Conditions, ConditionHeartbeat, metav1.ConditionFalse, "Stale")
	assertCondition(t, agent.Status.Conditions, ConditionDiscoveryFresh, metav1.ConditionFalse, "Stale")
	assertCondition(t, agent.Status.Conditions, ConditionDegraded, metav1.ConditionTrue, "HeartbeatStale")
}

func TestAgentDeploymentUsesNamedObservabilityPortAndBoundedProbes(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("observed-runtime")
	kube := fake.NewClientBuilder().WithScheme(scheme).WithObjects(agent).Build()
	reconciler := testAgentReconciler(kube, scheme, &fakeControlPlane{})

	deployment, err := reconciler.applyAgentDeployment(context.Background(), agent)
	if err != nil {
		t.Fatal(err)
	}
	container := deployment.Spec.Template.Spec.Containers[0]
	if len(container.Ports) != 1 || container.Ports[0].Name != "observability" || container.Ports[0].ContainerPort != 9090 {
		t.Fatalf("Agent ports = %#v", container.Ports)
	}
	if !hasEnv(container.Env, "XISNOVE_AGENT_OBSERVABILITY_ADDRESS", "0.0.0.0:9090") {
		t.Fatalf("Agent env = %#v", container.Env)
	}
	if container.LivenessProbe == nil || container.ReadinessProbe == nil {
		t.Fatalf("Agent probes = live %#v ready %#v", container.LivenessProbe, container.ReadinessProbe)
	}
	if container.LivenessProbe.HTTPGet.Path != "/livez" || container.ReadinessProbe.HTTPGet.Path != "/readyz" {
		t.Fatalf("Agent probe paths = live %q ready %q", container.LivenessProbe.HTTPGet.Path, container.ReadinessProbe.HTTPGet.Path)
	}
	if container.LivenessProbe.HTTPGet.Port.StrVal != "observability" || container.ReadinessProbe.HTTPGet.Port.StrVal != "observability" {
		t.Fatalf("Agent probe ports = live %v ready %v", container.LivenessProbe.HTTPGet.Port, container.ReadinessProbe.HTTPGet.Port)
	}
	if deployment.Spec.Template.Spec.TerminationGracePeriodSeconds == nil || *deployment.Spec.Template.Spec.TerminationGracePeriodSeconds != 15 {
		t.Fatalf("termination grace = %v", deployment.Spec.Template.Spec.TerminationGracePeriodSeconds)
	}
}

func hasEnv(values []corev1.EnvVar, name, want string) bool {
	for _, value := range values {
		if value.Name == name && value.Value == want {
			return true
		}
	}
	return false
}

func TestConditionMessagesAndCountAreBounded(t *testing.T) {
	t.Parallel()
	conditions := make([]metav1.Condition, 8)
	for index := range conditions {
		conditions[index] = metav1.Condition{Type: "Old" + string(rune('A'+index))}
	}
	setCondition(&conditions, 1, "New", metav1.ConditionTrue, "Ready", strings.Repeat("x", 400))
	if len(conditions) != 8 {
		t.Fatalf("conditions=%d, want 8", len(conditions))
	}
	newCondition := findCondition(conditions, "New")
	if newCondition == nil || len(newCondition.Message) != MaxConditionMessageLength {
		t.Fatalf("new condition=%#v", newCondition)
	}
}

func TestBoundMessagePreservesUTF8(t *testing.T) {
	t.Parallel()
	message := strings.Repeat("😀", 100)
	got := boundMessage(message)
	if len(got) > MaxConditionMessageLength || !utf8.ValidString(got) {
		t.Fatalf("bounded message bytes=%d valid=%t", len(got), utf8.ValidString(got))
	}
}

func TestApplyIdempotencyKeyTracksGenerationAndIsBounded(t *testing.T) {
	t.Parallel()
	agent := validAgent(strings.Repeat("a", 253))
	agent.Namespace = strings.Repeat("b", 63)
	agent.Generation = 1
	first := applyIdempotencyKey(agent, false)
	agent.Generation = 2
	second := applyIdempotencyKey(agent, false)
	if first == second || len(first) > 200 || len(second) > 200 {
		t.Fatalf("keys first=%q second=%q", first, second)
	}
	if bootstrap := applyIdempotencyKey(agent, true); bootstrap == second {
		t.Fatal("bootstrap and credential-free reconcile keys collided")
	}
}

func TestAgentFailedPostBootstrapApplyKeepsObservedGenerationForRetry(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("retry-apply")
	agent.Generation, agent.Status.ObservedGeneration, agent.Status.ExternalID = 2, 1, "agent-remote-1"
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: agent.Spec.CredentialSecretRef.Name, Namespace: agent.Namespace}, Data: map[string][]byte{CredentialKey: []byte(`{"credential":"credential-00000000000000000000000000000002","generation":2}`)}}
	if err := controllerutil.SetControllerReference(agent, secret, scheme); err != nil {
		t.Fatal(err)
	}
	attempts, observes := 0, 0
	remote := &fakeControlPlane{applyAgent: func(context.Context, controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		attempts++
		if attempts == 1 {
			return controlplane.AgentState{}, errors.New("temporary apply failure")
		}
		return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 2}, nil
	}, observeAgent: func(context.Context, controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
		observes++
		return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 2}, nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret).Build()
	r := testAgentReconciler(kube, scheme, remote)
	if _, err := r.Reconcile(context.Background(), requestFor(agent)); err == nil {
		t.Fatal("first apply unexpectedly succeeded")
	}
	stored := &monitoringv1alpha1.Agent{}
	if err := kube.Get(context.Background(), requestFor(agent).NamespacedName, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.ObservedGeneration != 1 {
		t.Fatalf("observed generation=%d, want 1", stored.Status.ObservedGeneration)
	}
	reconcileAgent(t, r, agent)
	reconcileAgent(t, r, agent)
	if attempts != 2 || observes != 1 {
		t.Fatalf("apply=%d observe=%d", attempts, observes)
	}
}

func TestAgentPostBootstrapSpecUpdateAndGenerationThreeConverge(t *testing.T) {
	t.Parallel()
	scheme := agentScheme(t)
	agent := validAgent("completed")
	agent.Generation, agent.Status.ObservedGeneration, agent.Status.ExternalID, agent.Status.CredentialGeneration = 2, 2, "agent-remote-1", 2
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: agent.Spec.CredentialSecretRef.Name, Namespace: agent.Namespace}, Data: map[string][]byte{CredentialKey: []byte(`{"credential":"credential-00000000000000000000000000000002","generation":2}`)}}
	if err := controllerutil.SetControllerReference(agent, secret, scheme); err != nil {
		t.Fatal(err)
	}
	applies, observes, puts, revokes := 0, 0, 0, 0
	presented := int64(2)
	remote := &fakeControlPlane{applyAgent: func(_ context.Context, req controlplane.ApplyAgentRequest) (controlplane.AgentState, error) {
		applies++
		if len(req.InitialCredential) != 0 || len(req.Spec.Capabilities) != 1 || req.Spec.Capabilities[0] != monitoringv1alpha1.AgentCapabilityTCP || req.IdempotencyKey == "" || len(req.IdempotencyKey) > 200 {
			t.Fatalf("apply=%#v", req)
		}
		return controlplane.AgentState{ExternalID: "agent-remote-1", CredentialGeneration: 2, PresentedCredentialGeneration: presented}, nil
	}, observeAgent: func(_ context.Context, req controlplane.ObserveAgentRequest) (controlplane.AgentState, error) {
		observes++
		return controlplane.AgentState{ExternalID: req.ExternalID, CredentialGeneration: 3, PresentedCredentialGeneration: presented, LastHeartbeatAt: time.Now()}, nil
	}, putCredential: func(_ context.Context, req controlplane.PutAgentCredentialRequest) error {
		puts++
		if req.Generation != 3 || len(req.Credential) == 0 {
			t.Fatalf("put=%#v", req)
		}
		return nil
	}, revokeCredential: func(_ context.Context, req controlplane.RevokeAgentCredentialRequest) error {
		revokes++
		if req.Generation != 2 {
			t.Fatalf("revoke=%#v", req)
		}
		return nil
	}}
	kube := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&monitoringv1alpha1.Agent{}).WithObjects(agent, secret).Build()
	r := testAgentReconciler(kube, scheme, remote)
	stored := &monitoringv1alpha1.Agent{}
	_ = kube.Get(context.Background(), requestFor(agent).NamespacedName, stored)
	stored.Generation = 3
	stored.Spec.Capabilities = []monitoringv1alpha1.AgentCapability{monitoringv1alpha1.AgentCapabilityTCP}
	if err := kube.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	reconcileAgent(t, r, agent)
	if applies != 1 {
		t.Fatalf("applies=%d", applies)
	}
	if err := kube.Get(context.Background(), requestFor(agent).NamespacedName, stored); err != nil {
		t.Fatal(err)
	}
	if stored.Status.ObservedGeneration != 3 {
		t.Fatalf("observed generation=%d, want 3", stored.Status.ObservedGeneration)
	}
	assertCondition(t, stored.Status.Conditions, ConditionSynced, metav1.ConditionTrue, "Applied")
	stored.Spec.CredentialRotation.RequestedGeneration = 3
	stored.Generation = 4
	if err := kube.Update(context.Background(), stored); err != nil {
		t.Fatal(err)
	}
	reconcileAgent(t, r, agent)
	presented = 3
	reconcileAgent(t, r, agent)
	got := getCredentialSecret(t, kube, agent)
	current, next, err := credentialState(got, CredentialKey)
	if err != nil || current.Generation != 3 || next != nil || previousCredentialGeneration(got) != 0 || puts != 1 || revokes != 1 || observes < 1 {
		t.Fatalf("state current=%#v next=%#v puts=%d revokes=%d observes=%d", current, next, puts, revokes, observes)
	}
}

func agentScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := testScheme(t)
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	return scheme
}

func testAgentReconciler(kube client.Client, scheme *runtime.Scheme, remote controlplane.Client) *AgentReconciler {
	return &AgentReconciler{Client: kube, APIReader: kube, Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://xisnove.example.test", DefaultAgentImage: "agent:test"}
}

func validAgent(name string) *monitoringv1alpha1.Agent {
	return &monitoringv1alpha1.Agent{TypeMeta: metav1.TypeMeta{APIVersion: monitoringv1alpha1.GroupVersion.String(), Kind: "Agent"}, ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", UID: types.UID("agent-uid")}, Spec: monitoringv1alpha1.AgentSpec{LocationID: "11111111-1111-1111-1111-111111111111", Capabilities: []monitoringv1alpha1.AgentCapability{monitoringv1alpha1.AgentCapabilityHTTP, monitoringv1alpha1.AgentCapabilityKubernetesDiscovery}, CredentialSecretRef: monitoringv1alpha1.SecretKeyReference{Name: name + "-credential", Key: CredentialKey}, CredentialRotation: monitoringv1alpha1.AgentCredentialRotationSpec{RequestedGeneration: 1}, Discovery: monitoringv1alpha1.AgentDiscoverySpec{Namespaces: []string{"default"}, Resources: []monitoringv1alpha1.DiscoveryResource{monitoringv1alpha1.DiscoveryResourceService}, StaleAfterSeconds: 300}, Workload: monitoringv1alpha1.AgentWorkloadSpec{ServiceAccountName: name + "-discovery"}}}
}

func reconcileAgent(t *testing.T, reconciler *AgentReconciler, agent *monitoringv1alpha1.Agent) {
	t.Helper()
	if _, err := reconciler.Reconcile(context.Background(), requestFor(agent)); err != nil {
		t.Fatal(err)
	}
}
func requestFor(agent *monitoringv1alpha1.Agent) ctrl.Request {
	return ctrl.Request{NamespacedName: types.NamespacedName{Name: agent.Name, Namespace: agent.Namespace}}
}
func getCredentialSecret(t *testing.T, kube client.Client, agent *monitoringv1alpha1.Agent) *corev1.Secret {
	t.Helper()
	secret := &corev1.Secret{}
	if err := kube.Get(context.Background(), types.NamespacedName{Name: agent.Spec.CredentialSecretRef.Name, Namespace: agent.Namespace}, secret); err != nil {
		t.Fatal(err)
	}
	return secret
}
func boolPointer(value bool) *bool { return &value }

type failSecretUpdateClient struct {
	client.Client
	failures int
}

func (c *failSecretUpdateClient) Update(ctx context.Context, object client.Object, options ...client.UpdateOption) error {
	if _, isSecret := object.(*corev1.Secret); isSecret && c.failures > 0 {
		c.failures--
		return errors.New("simulated Secret update crash")
	}
	return c.Client.Update(ctx, object, options...)
}
