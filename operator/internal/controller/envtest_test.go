package controller

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controller/testdata"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

const envtestTimeout = 15 * time.Second

func TestEnvtestFakeControlPlaneIdempotency(t *testing.T) {
	ctx := context.Background()
	remote := testdata.NewFakeControlPlane()
	monitorOwner := controlplane.OwnerReference{Key: "monitoring.xisnove.io/Monitor/default/idempotent", UID: "monitor-uid"}
	monitorRequest := controlplane.ApplyMonitorRequest{Owner: monitorOwner, Name: "idempotent", Spec: validMonitor("idempotent").Spec, IdempotencyKey: "monitor-apply-1"}
	monitorState, err := remote.ApplyMonitor(ctx, monitorRequest)
	must(t, err)
	boundMonitorReplay := monitorRequest
	boundMonitorReplay.ExternalID = monitorState.ExternalID
	replayedMonitor, err := remote.ApplyMonitor(ctx, boundMonitorReplay)
	must(t, err)
	if replayedMonitor.ExternalID != monitorState.ExternalID {
		t.Fatal("identical Monitor replay changed the remote identity")
	}
	changedMonitor := monitorRequest
	changedMonitor.Name = "changed"
	if _, err := remote.ApplyMonitor(ctx, changedMonitor); err == nil {
		t.Fatal("changed Monitor request reused an idempotency key")
	}
	if _, err := remote.ApplyMonitor(ctx, controlplane.ApplyMonitorRequest{Owner: monitorOwner, Name: "missing-key", Spec: monitorRequest.Spec}); err == nil {
		t.Fatal("Monitor mutation accepted an empty idempotency key")
	}
	monitorDelete := controlplane.DeleteRemoteObjectRequest{Owner: monitorOwner, ExternalID: monitorState.ExternalID, IdempotencyKey: "monitor-delete-1"}
	must(t, remote.DeleteMonitor(ctx, monitorDelete))
	must(t, remote.DeleteMonitor(ctx, monitorDelete))
	changedMonitorDelete := monitorDelete
	changedMonitorDelete.ExternalID = "different"
	if err := remote.DeleteMonitor(ctx, changedMonitorDelete); err == nil {
		t.Fatal("changed Monitor delete reused an idempotency key")
	}

	agentOwner := controlplane.OwnerReference{Key: "monitoring.xisnove.io/Agent/default/idempotent", UID: "agent-uid"}
	agentRequest := controlplane.ApplyAgentRequest{Owner: agentOwner, Name: "idempotent", Spec: validAgent("idempotent").Spec, InitialCredential: []byte("credential-one"), IdempotencyKey: "agent-apply-1"}
	agentState, err := remote.ApplyAgent(ctx, agentRequest)
	must(t, err)
	boundAgentReplay := agentRequest
	boundAgentReplay.ExternalID = agentState.ExternalID
	replayedAgent, err := remote.ApplyAgent(ctx, boundAgentReplay)
	must(t, err)
	if replayedAgent.ExternalID != agentState.ExternalID {
		t.Fatal("identical Agent replay changed the remote identity")
	}
	changedAgent := agentRequest
	changedAgent.InitialCredential = []byte("credential-two")
	if _, err := remote.ApplyAgent(ctx, changedAgent); err == nil {
		t.Fatal("changed Agent credential reused an idempotency key")
	}

	put := controlplane.PutAgentCredentialRequest{Owner: agentOwner, ExternalID: agentState.ExternalID, Generation: 2, Credential: []byte("credential-two"), IdempotencyKey: "agent-put-2"}
	must(t, remote.PutAgentCredential(ctx, put))
	must(t, remote.PutAgentCredential(ctx, put))
	changedPut := put
	changedPut.Credential = []byte("different")
	if err := remote.PutAgentCredential(ctx, changedPut); err == nil {
		t.Fatal("changed credential PUT reused an idempotency key")
	}
	remote.SetAgentObservation(agentOwner.Key, 2, time.Now(), time.Time{})
	revoke := controlplane.RevokeAgentCredentialRequest{Owner: agentOwner, ExternalID: agentState.ExternalID, Generation: 1, IdempotencyKey: "agent-revoke-1"}
	must(t, remote.RevokeAgentCredential(ctx, revoke))
	must(t, remote.RevokeAgentCredential(ctx, revoke))
	changedRevoke := revoke
	changedRevoke.Generation = 2
	if err := remote.RevokeAgentCredential(ctx, changedRevoke); err == nil {
		t.Fatal("changed credential revoke reused an idempotency key")
	}
	agentDelete := controlplane.DeleteRemoteObjectRequest{Owner: agentOwner, ExternalID: agentState.ExternalID, IdempotencyKey: "agent-delete-1"}
	must(t, remote.DeleteAgent(ctx, agentDelete))
	must(t, remote.DeleteAgent(ctx, agentDelete))
	changedAgentDelete := agentDelete
	changedAgentDelete.ExternalID = "different"
	if err := remote.DeleteAgent(ctx, changedAgentDelete); err == nil {
		t.Fatal("changed Agent delete reused an idempotency key")
	}
}

func TestEnvtestControllerJourneys(t *testing.T) {
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		t.Skip("KUBEBUILDER_ASSETS is set by make -C operator envtest")
	}
	testEnvironment := &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "..", "charts", "xisnove-edge", "crds")},
		ErrorIfCRDPathMissing: true,
	}
	config, err := testEnvironment.Start()
	if err != nil {
		t.Fatalf("start envtest: %v", err)
	}
	t.Cleanup(func() {
		if err := testEnvironment.Stop(); err != nil {
			t.Errorf("stop envtest: %v", err)
		}
	})

	scheme := runtime.NewScheme()
	must(t, corev1.AddToScheme(scheme))
	must(t, appsv1.AddToScheme(scheme))
	must(t, monitoringv1alpha1.AddToScheme(scheme))
	manager, err := ctrl.NewManager(config, ctrl.Options{
		Scheme: scheme, Metrics: metricsserver.Options{BindAddress: "0"}, HealthProbeBindAddress: "0", LeaderElection: false,
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}
	remote := testdata.NewFakeControlPlane()
	now := time.Date(2026, time.July, 26, 12, 0, 0, 0, time.UTC)
	monitorController := &MonitorReconciler{Client: manager.GetClient(), Scheme: scheme, ControlPlane: remote, PollInterval: 40 * time.Millisecond}
	agentController := &AgentReconciler{Client: manager.GetClient(), APIReader: manager.GetAPIReader(), Scheme: scheme, ControlPlane: remote, ControlPlaneURL: "https://control.example.test", DefaultAgentImage: "agent:test", PollInterval: 40 * time.Millisecond, HeartbeatStaleAfter: 2 * time.Minute, Now: func() time.Time { return now }}
	must(t, monitorController.SetupWithManager(manager))
	must(t, agentController.SetupWithManager(manager))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- manager.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("manager stopped: %v", err)
			}
		case <-time.After(envtestTimeout):
			t.Error("manager did not stop")
		}
	})
	startupContext, cancelStartup := context.WithTimeout(ctx, envtestTimeout)
	defer cancelStartup()
	if !manager.GetCache().WaitForCacheSync(startupContext) {
		t.Fatal("manager cache did not synchronize")
	}
	cancelStartup()
	kube := manager.GetClient()
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "xisnove-envtest-"}}
	must(t, kube.Create(ctx, namespace))

	t.Run("monitor lifecycle, replay, health, and conflicts", func(t *testing.T) {
		monitor := envtestMonitor(namespace.Name, "payments")
		must(t, kube.Create(ctx, monitor))
		key := ownerKey("Monitor", namespace.Name, monitor.Name)
		var firstID string
		eventually(t, func() bool {
			stored, ok := loadMonitor(ctx, kube, monitor.Name, namespace.Name)
			if !ok {
				return false
			}
			firstID = stored.Status.ExternalID
			return stored.Status.ObservedGeneration == stored.Generation && firstID != "" && stored.Status.Health.State == "pending" && conditionIs(stored.Status.Conditions, ConditionDegraded, metav1.ConditionUnknown, "HealthUnknown") && hasFinalizer(stored.Finalizers, MonitorFinalizer)
		})
		first, ok := remote.Monitor(key)
		if !ok || first.Owner.UID == "" || first.Applies < 1 {
			t.Fatalf("monitor was not materialized safely: %#v", first)
		}

		updateMonitor(t, ctx, kube, monitor.Name, namespace.Name, func(value *monitoringv1alpha1.Monitor) { value.Spec.Description = "updated" })
		eventually(t, func() bool {
			stored := getMonitor(t, ctx, kube, monitor.Name, namespace.Name)
			snapshot, ok := remote.Monitor(key)
			return ok && stored.Status.ObservedGeneration == stored.Generation && snapshot.Spec.Description == "updated" && snapshot.State.ExternalID == firstID
		})

		// Simulate a crash after the durable remote apply but before the status
		// write. Reconciliation must recover the same remote object.
		updateMonitorStatus(t, ctx, kube, monitor.Name, namespace.Name, func(value *monitoringv1alpha1.Monitor) { value.Status = monitoringv1alpha1.MonitorStatus{} })
		eventually(t, func() bool {
			return getMonitor(t, ctx, kube, monitor.Name, namespace.Name).Status.ExternalID == firstID
		})
		if snapshot, _ := remote.Monitor(key); snapshot.State.ExternalID != firstID {
			t.Fatal("lost-status replay created a second monitor")
		}

		remote.SetMonitorHealth(key, "up", now)
		eventually(t, func() bool {
			stored := getMonitor(t, ctx, kube, monitor.Name, namespace.Name)
			return stored.Status.Health.State == "up" && conditionIs(stored.Status.Conditions, ConditionDegraded, metav1.ConditionFalse, "Healthy")
		})

		remote.SetMonitorFailure(errors.New("Authorization: Bearer should-never-appear " + strings.Repeat("x", 600)))
		updateMonitor(t, ctx, kube, monitor.Name, namespace.Name, func(value *monitoringv1alpha1.Monitor) { value.Spec.Description = "force bounded error" })
		eventually(t, func() bool {
			stored := getMonitor(t, ctx, kube, monitor.Name, namespace.Name)
			condition := findCondition(stored.Status.Conditions, ConditionDegraded)
			return condition != nil && condition.Reason == "ReconcileFailed" && len(condition.Message) <= MaxConditionMessageLength && !strings.Contains(condition.Message, "should-never-appear") && len(stored.Status.Conditions) <= 8
		})
		remote.SetMonitorFailure(nil)

		adversarialSpecStatusUpdates(t, ctx, kube, monitor.Name, namespace.Name)
		eventually(t, func() bool {
			stored := getMonitor(t, ctx, kube, monitor.Name, namespace.Name)
			snapshot, ok := remote.Monitor(key)
			return ok && stored.Status.ObservedGeneration == stored.Generation && snapshot.Spec.Description == "generation-7" && len(stored.Status.Conditions) <= 8
		})

		must(t, kube.Delete(ctx, getMonitor(t, ctx, kube, monitor.Name, namespace.Name)))
		eventually(t, func() bool {
			probe := &monitoringv1alpha1.Monitor{}
			err := kube.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: monitor.Name}, probe)
			_, remoteExists := remote.Monitor(key)
			return apierrors.IsNotFound(err) && !remoteExists
		})
	})

	t.Run("recreated UID cannot claim orphan", func(t *testing.T) {
		name := "orphan"
		key := ownerKey("Monitor", namespace.Name, name)
		remote.SeedMonitor(controlplane.OwnerReference{Key: key, UID: "deleted-uid"}, "orphan-remote")
		monitor := envtestMonitor(namespace.Name, name)
		must(t, kube.Create(ctx, monitor))
		eventually(t, func() bool {
			stored, ok := loadMonitor(ctx, kube, name, namespace.Name)
			if !ok {
				return false
			}
			condition := findCondition(stored.Status.Conditions, ConditionDegraded)
			return condition != nil && condition.Reason == "ReconcileFailed" && stored.UID != types.UID("deleted-uid")
		})
		snapshot, _ := remote.Monitor(key)
		if snapshot.Owner.UID != "deleted-uid" || snapshot.State.ExternalID != "orphan-remote" {
			t.Fatalf("orphan ownership changed: %#v", snapshot)
		}
		updateMonitor(t, ctx, kube, name, namespace.Name, func(value *monitoringv1alpha1.Monitor) {
			if value.Annotations == nil {
				value.Annotations = map[string]string{}
			}
			value.Annotations[ForceDeleteAnnotation] = "true"
		})
		must(t, kube.Delete(ctx, getMonitor(t, ctx, kube, name, namespace.Name)))
		eventually(t, func() bool {
			return apierrors.IsNotFound(kube.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: name}, &monitoringv1alpha1.Monitor{}))
		})
	})

	t.Run("agent refuses a non-owned credential Secret", func(t *testing.T) {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "foreign-credential", Namespace: namespace.Name}, Type: corev1.SecretTypeOpaque, Data: map[string][]byte{"unrelated": []byte("preserved")}}
		must(t, kube.Create(ctx, secret))
		agent := envtestAgent(namespace.Name, "foreign")
		agent.Spec.CredentialSecretRef.Name = secret.Name
		must(t, kube.Create(ctx, agent))
		eventually(t, func() bool {
			stored, ok := loadAgent(ctx, kube, agent.Name, namespace.Name)
			if !ok {
				return false
			}
			condition := findCondition(stored.Status.Conditions, ConditionDegraded)
			return condition != nil && condition.Reason == "ReconcileFailed" && strings.Contains(condition.Message, "not owned")
		})
		unchanged := getSecret(t, ctx, kube, secret.Name, namespace.Name)
		if string(unchanged.Data["unrelated"]) != "preserved" || len(unchanged.OwnerReferences) != 0 {
			t.Fatal("controller modified the non-owned Secret")
		}
		updateAgent(t, ctx, kube, agent.Name, namespace.Name, func(value *monitoringv1alpha1.Agent) {
			if value.Annotations == nil {
				value.Annotations = map[string]string{}
			}
			value.Annotations[ForceDeleteAnnotation] = "true"
		})
		must(t, kube.Delete(ctx, getAgent(t, ctx, kube, agent.Name, namespace.Name)))
		eventually(t, func() bool {
			return apierrors.IsNotFound(kube.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: agent.Name}, &monitoringv1alpha1.Agent{}))
		})
	})

	t.Run("recreated Agent UID cannot claim or delete orphan", func(t *testing.T) {
		name := "agent-orphan"
		key := ownerKey("Agent", namespace.Name, name)
		remote.SeedAgent(controlplane.OwnerReference{Key: key, UID: "deleted-agent-uid"}, "agent-orphan-remote")
		agent := envtestAgent(namespace.Name, name)
		must(t, kube.Create(ctx, agent))
		eventually(t, func() bool {
			stored, ok := loadAgent(ctx, kube, name, namespace.Name)
			if !ok {
				return false
			}
			condition := findCondition(stored.Status.Conditions, ConditionDegraded)
			return condition != nil && condition.Reason == "ReconcileFailed" && stored.UID != types.UID("deleted-agent-uid")
		})
		snapshot, _ := remote.Agent(key)
		if snapshot.Owner.UID != "deleted-agent-uid" || snapshot.State.ExternalID != "agent-orphan-remote" {
			t.Fatalf("orphan Agent ownership changed: %#v", snapshot)
		}

		must(t, kube.Delete(ctx, getAgent(t, ctx, kube, name, namespace.Name)))
		eventually(t, func() bool {
			stored, ok := loadAgent(ctx, kube, name, namespace.Name)
			if !ok {
				return false
			}
			orphan, remoteExists := remote.Agent(key)
			return !stored.DeletionTimestamp.IsZero() && hasFinalizer(stored.Finalizers, AgentFinalizer) && remoteExists && orphan.Owner.UID == "deleted-agent-uid"
		})
		updateAgent(t, ctx, kube, name, namespace.Name, func(value *monitoringv1alpha1.Agent) {
			if value.Annotations == nil {
				value.Annotations = map[string]string{}
			}
			value.Annotations[ForceDeleteAnnotation] = "true"
		})
		eventually(t, func() bool {
			return apierrors.IsNotFound(kube.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: name}, &monitoringv1alpha1.Agent{}))
		})
		if orphan, ok := remote.Agent(key); !ok || orphan.Owner.UID != "deleted-agent-uid" {
			t.Fatal("forced Kubernetes cleanup mutated the orphan Agent")
		}
	})

	t.Run("agent materialization, availability, freshness, rotation, replay, and deletion", func(t *testing.T) {
		agent := envtestAgent(namespace.Name, "edge")
		must(t, kube.Create(ctx, agent))
		key := ownerKey("Agent", namespace.Name, agent.Name)
		eventually(t, func() bool {
			stored, ok := loadAgent(ctx, kube, agent.Name, namespace.Name)
			if !ok {
				return false
			}
			return stored.Status.ExternalID != "" && stored.Status.CredentialGeneration == 1 && hasFinalizer(stored.Finalizers, AgentFinalizer)
		})
		secret := getSecret(t, ctx, kube, agent.Spec.CredentialSecretRef.Name, namespace.Name)
		firstDigest := sha256.Sum256(secret.Data[CredentialKey])
		if len(secret.Data[NextCredentialKey]) != 0 || len(secret.Data[PreviousCredentialKey]) != 0 {
			t.Fatal("initial Secret exposed overlap keys")
		}
		initialSnapshot, ok := remote.Agent(key)
		if !ok || len(initialSnapshot.CredentialGenerations) != 1 {
			t.Fatalf("unexpected remote Agent state: %#v", initialSnapshot)
		}

		// Lost status must reuse the current bundle; neither identity nor token may change.
		updateAgentStatus(t, ctx, kube, agent.Name, namespace.Name, func(value *monitoringv1alpha1.Agent) { value.Status = monitoringv1alpha1.AgentStatus{} })
		eventually(t, func() bool {
			return getAgent(t, ctx, kube, agent.Name, namespace.Name).Status.ExternalID == initialSnapshot.State.ExternalID
		})
		secret = getSecret(t, ctx, kube, agent.Spec.CredentialSecretRef.Name, namespace.Name)
		if sha256.Sum256(secret.Data[CredentialKey]) != firstDigest {
			t.Fatal("lost-status replay changed the current credential")
		}
		if snapshot, _ := remote.Agent(key); snapshot.State.ExternalID != initialSnapshot.State.ExternalID || len(snapshot.CredentialGenerations) != 1 {
			t.Fatalf("lost-status replay duplicated remote state: %#v", snapshot)
		}

		updateAgent(t, ctx, kube, agent.Name, namespace.Name, func(value *monitoringv1alpha1.Agent) { value.Spec.CredentialRotation.RequestedGeneration = 2 })
		eventually(t, func() bool {
			secret := getSecret(t, ctx, kube, agent.Spec.CredentialSecretRef.Name, namespace.Name)
			return secret.Annotations[CredentialGenerationAnnotation] == "2" && secret.Annotations[PreviousCredentialGenerationAnnotation] == "1" && len(secret.Data[PreviousCredentialKey]) != 0 && len(secret.Data[NextCredentialKey]) == 0
		})
		remote.SetAgentObservation(key, 1, now, now.Add(-10*time.Minute))
		eventually(t, func() bool {
			stored := getAgent(t, ctx, kube, agent.Name, namespace.Name)
			return conditionIs(stored.Status.Conditions, ConditionDiscoveryFresh, metav1.ConditionFalse, "Stale") && conditionIs(stored.Status.Conditions, ConditionDegraded, metav1.ConditionTrue, "DiscoveryStale")
		})

		deployment := getDeployment(t, ctx, kube, agent.Name, namespace.Name)
		deployment.Status.ObservedGeneration = deployment.Generation
		deployment.Status.Replicas = 1
		deployment.Status.ReadyReplicas = 1
		deployment.Status.AvailableReplicas = 1
		must(t, kube.Status().Update(ctx, deployment))
		remote.SetAgentObservation(key, 2, now, now)
		eventually(t, func() bool {
			stored := getAgent(t, ctx, kube, agent.Name, namespace.Name)
			secret := getSecret(t, ctx, kube, agent.Spec.CredentialSecretRef.Name, namespace.Name)
			return stored.Status.RotationPhase == monitoringv1alpha1.RotationPhaseComplete && stored.Status.PreviousCredentialGeneration == nil && len(secret.Data[PreviousCredentialKey]) == 0 && conditionIs(stored.Status.Conditions, ConditionWorkloadReady, metav1.ConditionTrue, "Available") && conditionIs(stored.Status.Conditions, ConditionDiscoveryFresh, metav1.ConditionTrue, "Fresh")
		})
		snapshot, _ := remote.Agent(key)
		if len(snapshot.CredentialGenerations) != 1 || snapshot.CredentialGenerations[0] != 2 || len(snapshot.Revokes) == 0 || snapshot.Revokes[len(snapshot.Revokes)-1] != 1 {
			t.Fatalf("rotation did not converge: %#v", snapshot)
		}

		remote.SetAgentObservation(key, 2, now.Add(-3*time.Minute), now)
		eventually(t, func() bool {
			return conditionIs(getAgent(t, ctx, kube, agent.Name, namespace.Name).Status.Conditions, ConditionDegraded, metav1.ConditionTrue, "HeartbeatStale")
		})
		stored := getAgent(t, ctx, kube, agent.Name, namespace.Name)
		if len(stored.Status.Conditions) > 8 {
			t.Fatalf("conditions = %d, want <= 8", len(stored.Status.Conditions))
		}
		for _, condition := range stored.Status.Conditions {
			if len(condition.Message) > MaxConditionMessageLength {
				t.Fatalf("condition message length = %d", len(condition.Message))
			}
		}

		must(t, kube.Delete(ctx, stored))
		eventually(t, func() bool {
			err := kube.Get(ctx, types.NamespacedName{Namespace: namespace.Name, Name: agent.Name}, &monitoringv1alpha1.Agent{})
			_, remoteExists := remote.Agent(key)
			return apierrors.IsNotFound(err) && !remoteExists
		})
	})
}

func adversarialSpecStatusUpdates(t *testing.T, ctx context.Context, kube client.Client, name, namespace string) {
	t.Helper()
	var wait sync.WaitGroup
	errorsFound := make(chan error, 2)
	wait.Add(2)
	go func() {
		defer wait.Done()
		for index := 0; index < 8; index++ {
			if err := retryConflict(ctx, func() error {
				value := &monitoringv1alpha1.Monitor{}
				if err := kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, value); err != nil {
					return err
				}
				value.Spec.Description = fmt.Sprintf("generation-%d", index)
				return kube.Update(ctx, value)
			}); err != nil {
				errorsFound <- err
				return
			}
		}
	}()
	go func() {
		defer wait.Done()
		for range 16 {
			if err := retryConflict(ctx, func() error {
				value := &monitoringv1alpha1.Monitor{}
				if err := kube.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, value); err != nil {
					return err
				}
				value.Status.Conditions = make([]metav1.Condition, 8)
				for conditionIndex := range value.Status.Conditions {
					value.Status.Conditions[conditionIndex] = metav1.Condition{Type: fmt.Sprintf("Adversarial%d", conditionIndex), Status: metav1.ConditionUnknown, Reason: "Injected", Message: strings.Repeat("z", 256), LastTransitionTime: metav1.Now(), ObservedGeneration: value.Generation}
				}
				return kube.Status().Update(ctx, value)
			}); err != nil {
				errorsFound <- err
				return
			}
		}
	}()
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatalf("adversarial writer: %v", err)
	}
}

func retryConflict(ctx context.Context, operation func() error) error {
	deadline := time.NewTimer(envtestTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		err := operation()
		if err == nil {
			return nil
		}
		if !apierrors.IsConflict(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return err
		case <-ticker.C:
		}
	}
}

func eventually(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.NewTimer(envtestTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if predicate() {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("condition did not converge before timeout")
		case <-ticker.C:
		}
	}
}

func envtestMonitor(namespace, name string) *monitoringv1alpha1.Monitor {
	value := validMonitor(name)
	value.Namespace, value.UID = namespace, ""
	return value
}

func envtestAgent(namespace, name string) *monitoringv1alpha1.Agent {
	value := validAgent(name)
	value.Namespace, value.UID = namespace, ""
	value.Spec.Discovery.StaleAfterSeconds = 120
	return value
}

func ownerKey(kind, namespace, name string) string {
	return fmt.Sprintf("monitoring.xisnove.io/%s/%s/%s", kind, namespace, name)
}
func hasFinalizer(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func conditionIs(values []metav1.Condition, kind string, status metav1.ConditionStatus, reason string) bool {
	condition := findCondition(values, kind)
	return condition != nil && condition.Status == status && condition.Reason == reason
}
func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func getMonitor(t *testing.T, ctx context.Context, kube client.Client, name, namespace string) *monitoringv1alpha1.Monitor {
	t.Helper()
	value := &monitoringv1alpha1.Monitor{}
	must(t, kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value))
	return value
}

func loadMonitor(ctx context.Context, kube client.Client, name, namespace string) (*monitoringv1alpha1.Monitor, bool) {
	value := &monitoringv1alpha1.Monitor{}
	if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value); err != nil {
		return nil, false
	}
	return value, true
}
func getAgent(t *testing.T, ctx context.Context, kube client.Client, name, namespace string) *monitoringv1alpha1.Agent {
	t.Helper()
	value := &monitoringv1alpha1.Agent{}
	must(t, kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value))
	return value
}

func loadAgent(ctx context.Context, kube client.Client, name, namespace string) (*monitoringv1alpha1.Agent, bool) {
	value := &monitoringv1alpha1.Agent{}
	if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value); err != nil {
		return nil, false
	}
	return value, true
}
func getSecret(t *testing.T, ctx context.Context, kube client.Client, name, namespace string) *corev1.Secret {
	t.Helper()
	value := &corev1.Secret{}
	must(t, kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value))
	return value
}
func getDeployment(t *testing.T, ctx context.Context, kube client.Client, name, namespace string) *appsv1.Deployment {
	t.Helper()
	value := &appsv1.Deployment{}
	must(t, kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value))
	return value
}

func updateMonitor(t *testing.T, ctx context.Context, kube client.Client, name, namespace string, mutate func(*monitoringv1alpha1.Monitor)) {
	t.Helper()
	must(t, retryConflict(ctx, func() error {
		value := &monitoringv1alpha1.Monitor{}
		if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value); err != nil {
			return err
		}
		mutate(value)
		return kube.Update(ctx, value)
	}))
}
func updateMonitorStatus(t *testing.T, ctx context.Context, kube client.Client, name, namespace string, mutate func(*monitoringv1alpha1.Monitor)) {
	t.Helper()
	must(t, retryConflict(ctx, func() error {
		value := &monitoringv1alpha1.Monitor{}
		if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value); err != nil {
			return err
		}
		mutate(value)
		return kube.Status().Update(ctx, value)
	}))
}
func updateAgent(t *testing.T, ctx context.Context, kube client.Client, name, namespace string, mutate func(*monitoringv1alpha1.Agent)) {
	t.Helper()
	must(t, retryConflict(ctx, func() error {
		value := &monitoringv1alpha1.Agent{}
		if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value); err != nil {
			return err
		}
		mutate(value)
		return kube.Update(ctx, value)
	}))
}
func updateAgentStatus(t *testing.T, ctx context.Context, kube client.Client, name, namespace string, mutate func(*monitoringv1alpha1.Agent)) {
	t.Helper()
	must(t, retryConflict(ctx, func() error {
		value := &monitoringv1alpha1.Agent{}
		if err := kube.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, value); err != nil {
			return err
		}
		mutate(value)
		return kube.Status().Update(ctx, value)
	}))
}
