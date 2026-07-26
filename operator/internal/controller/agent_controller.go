package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

const (
	CredentialKey                          = "credential"
	NextCredentialKey                      = "credential.next"
	PreviousCredentialKey                  = "credential.previous"
	CredentialGenerationAnnotation         = "monitoring.xisnove.io/credential-generation"
	PreviousCredentialGenerationAnnotation = "monitoring.xisnove.io/previous-credential-generation"
	credentialMountPath                    = "/var/run/xisnove"
)

type credentialBundle struct {
	Credential string `json:"credential"`
	Generation int64  `json:"generation"`
}

type AgentReconciler struct {
	client.Client
	// APIReader performs exact Secret reads without starting a namespace-wide
	// Secret informer. This lets the operator RBAC omit list/watch on Secrets.
	APIReader           client.Reader
	Scheme              *runtime.Scheme
	ControlPlane        controlplane.Client
	ControlPlaneURL     string
	DefaultAgentImage   string
	PollInterval        time.Duration
	HeartbeatStaleAfter time.Duration
	Now                 func() time.Time
}

func (r *AgentReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	agent := &monitoringv1alpha1.Agent{}
	if err := r.Get(ctx, request.NamespacedName, agent); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if !agent.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, agent)
	}
	if !controllerutil.ContainsFinalizer(agent, AgentFinalizer) {
		controllerutil.AddFinalizer(agent, AgentFinalizer)
		if err := r.Update(ctx, agent); err != nil {
			return ctrl.Result{}, err
		}
	}

	previousStatus := agent.Status.DeepCopy()
	if credentialKey(agent) == NextCredentialKey || credentialKey(agent) == PreviousCredentialKey {
		return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("credential Secret key is reserved for controller lifecycle state"))
	}
	secret, err := r.readCredentialSecret(ctx, agent)
	if err != nil {
		return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
	}
	if secret == nil {
		secret, err = r.stageCredentialSecret(ctx, agent, 1)
		if err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
	}

	current, next, err := credentialState(secret, credentialKey(agent))
	if err != nil {
		return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
	}
	state := controlplane.AgentState{}
	// The control-plane apply endpoint deliberately accepts only generation one.
	// Keep that first bundle unmounted during overlap so retries can observe the
	// replacement heartbeat, but never regenerate it after a write crash.
	initial := initialBundle(current, secret)
	if agent.Status.ExternalID == "" || agent.Status.ObservedGeneration < agent.Generation {
		credential := []byte(nil)
		if initial != nil {
			credential = []byte(initial.Credential)
		}
		state, err = r.ControlPlane.ApplyAgent(ctx, controlplane.ApplyAgentRequest{
			Owner:             ownerFor(agent, "Agent"),
			ExternalID:        agent.Status.ExternalID,
			Name:              agent.Name,
			Spec:              *agent.Spec.DeepCopy(),
			InitialCredential: credential,
			IdempotencyKey:    applyIdempotencyKey(agent, initial != nil),
		})
		if err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
		if state.ExternalID == "" || state.CredentialGeneration < 1 {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("control plane returned an invalid Agent state"))
		}
		agent.Status.ExternalID = state.ExternalID
	} else if agent.Status.ExternalID != "" {
		state, err = r.ControlPlane.ObserveAgent(ctx, controlplane.ObserveAgentRequest{Owner: ownerFor(agent, "Agent"), ExternalID: agent.Status.ExternalID})
		if err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
		if state.ExternalID != agent.Status.ExternalID {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("control plane observation returned a different Agent identifier"))
		}
	}

	if current == nil && next != nil {
		if next.Generation != 1 || state.ExternalID == "" {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("initial credential staging did not produce a registered Agent"))
		}
		if err := r.promoteInitialCredential(ctx, secret, agent, *next); err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
		current = next
		next = nil
	}
	if current == nil {
		return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("operator-owned credential Secret has no current credential bundle"))
	}

	desiredGeneration := agent.Spec.CredentialRotation.RequestedGeneration
	if desiredGeneration < 1 {
		desiredGeneration = 1
	}
	if next != nil {
		if next.Generation != desiredGeneration || next.Generation <= current.Generation {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("credential Secret has an invalid staged replacement"))
		}
		if agent.Status.ExternalID == "" {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("cannot register a replacement credential without an Agent identifier"))
		}
		if err := r.putAndPromoteCredential(ctx, agent, secret, *current, *next); err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
		current, next = next, nil
	} else if desiredGeneration > current.Generation {
		if previousCredentialGeneration(secret) > 0 {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("cannot rotate credentials while the previous generation awaits a heartbeat"))
		}
		secret, err = r.stageCredentialSecret(ctx, agent, desiredGeneration)
		if err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
		_, staged, stateErr := credentialState(secret, credentialKey(agent))
		if stateErr != nil || staged == nil {
			if stateErr == nil {
				stateErr = errors.New("credential Secret staging did not retain the replacement")
			}
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, stateErr)
		}
		if agent.Status.ExternalID == "" {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, errors.New("cannot register a replacement credential without an Agent identifier"))
		}
		if err := r.putAndPromoteCredential(ctx, agent, secret, *current, *staged); err != nil {
			return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
		}
		current = staged
	}

	if previous := previousCredentialGeneration(secret); previous > 0 {
		agent.Status.PreviousCredentialGeneration = &previous
		agent.Status.RotationPhase = monitoringv1alpha1.RotationPhaseAwaitingHeartbeat
		if state.PresentedCredentialGeneration >= current.Generation && !state.LastHeartbeatAt.IsZero() {
			if err := r.ControlPlane.RevokeAgentCredential(ctx, controlplane.RevokeAgentCredentialRequest{
				Owner: ownerFor(agent, "Agent"), ExternalID: agent.Status.ExternalID, Generation: previous,
				IdempotencyKey: credentialIdempotencyKey(agent, previous, "revoke"),
			}); err != nil {
				return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
			}
			if err := r.finishCredentialOverlap(ctx, secret); err != nil {
				return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
			}
			agent.Status.PreviousCredentialGeneration = nil
			agent.Status.RotationPhase = monitoringv1alpha1.RotationPhaseComplete
		}
	} else if current.Generation > 1 {
		agent.Status.RotationPhase = monitoringv1alpha1.RotationPhaseComplete
	}
	agent.Status.CredentialGeneration = current.Generation

	deployment, err := r.applyAgentDeployment(ctx, agent)
	if err != nil {
		return ctrl.Result{}, r.recordAgentFailure(ctx, agent, err)
	}
	r.recordAgentSuccess(agent, state, deployment)
	if !equality.Semantic.DeepEqual(previousStatus, &agent.Status) {
		if err := r.Status().Update(ctx, agent); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: r.pollInterval()}, nil
}

func (r *AgentReconciler) finalize(ctx context.Context, agent *monitoringv1alpha1.Agent) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(agent, AgentFinalizer) {
		return ctrl.Result{}, nil
	}
	if !isForceDelete(agent) {
		err := r.ControlPlane.DeleteAgent(ctx, controlplane.DeleteRemoteObjectRequest{
			Owner: ownerFor(agent, "Agent"), ExternalID: agent.Status.ExternalID, IdempotencyKey: deleteIdempotencyKey(agent),
		})
		if err = ignoreRemoteNotFound(err); err != nil {
			return ctrl.Result{}, safeError(err)
		}
	}
	controllerutil.RemoveFinalizer(agent, AgentFinalizer)
	if err := r.Update(ctx, agent); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *AgentReconciler) readCredentialSecret(ctx context.Context, agent *monitoringv1alpha1.Agent) (*corev1.Secret, error) {
	secret := &corev1.Secret{}
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	err := reader.Get(ctx, client.ObjectKey{Namespace: agent.Namespace, Name: agent.Spec.CredentialSecretRef.Name}, secret)
	if apierrors.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read operator-owned credential Secret: %w", err)
	}
	if !metav1.IsControlledBy(secret, agent) {
		return nil, fmt.Errorf("credential Secret %s/%s is not owned by Agent %s", secret.Namespace, secret.Name, agent.Name)
	}
	return secret, nil
}

func (r *AgentReconciler) stageCredentialSecret(ctx context.Context, agent *monitoringv1alpha1.Agent, generation int64) (*corev1.Secret, error) {
	if generation < 1 {
		return nil, errors.New("credential generation must be positive")
	}
	credential, err := newCredential()
	if err != nil {
		return nil, err
	}
	bundle, err := marshalCredentialBundle(credentialBundle{Credential: credential, Generation: generation})
	if err != nil {
		return nil, err
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: agent.Spec.CredentialSecretRef.Name, Namespace: agent.Namespace},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{NextCredentialKey: bundle},
	}
	if err := controllerutil.SetControllerReference(agent, secret, r.Scheme); err != nil {
		return nil, err
	}
	if err := r.Create(ctx, secret); err != nil {
		if !apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("pre-stage operator-owned credential Secret: %w", err)
		}
		existing, readErr := r.readCredentialSecret(ctx, agent)
		if readErr != nil {
			return nil, readErr
		}
		if existing == nil {
			return nil, errors.New("credential Secret disappeared while staging")
		}
		if len(existing.Data[NextCredentialKey]) != 0 {
			return existing, nil
		}
		if existing.Data == nil {
			existing.Data = map[string][]byte{}
		}
		existing.Data[NextCredentialKey] = bundle
		if err := r.Update(ctx, existing); err != nil {
			return nil, fmt.Errorf("pre-stage replacement credential bundle: %w", err)
		}
		return existing, nil
	}
	return secret, nil
}

func (r *AgentReconciler) promoteInitialCredential(ctx context.Context, secret *corev1.Secret, agent *monitoringv1alpha1.Agent, next credentialBundle) error {
	key := credentialKey(agent)
	secret.Data[key] = append([]byte(nil), secret.Data[NextCredentialKey]...)
	delete(secret.Data, NextCredentialKey)
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[CredentialGenerationAnnotation] = strconv.FormatInt(next.Generation, 10)
	if err := r.Update(ctx, secret); err != nil {
		return fmt.Errorf("promote initial credential bundle: %w", err)
	}
	return nil
}

func (r *AgentReconciler) putAndPromoteCredential(ctx context.Context, agent *monitoringv1alpha1.Agent, secret *corev1.Secret, current, next credentialBundle) error {
	if err := r.ControlPlane.PutAgentCredential(ctx, controlplane.PutAgentCredentialRequest{
		Owner: ownerFor(agent, "Agent"), ExternalID: agent.Status.ExternalID, Generation: next.Generation,
		Credential: []byte(next.Credential), IdempotencyKey: credentialIdempotencyKey(agent, next.Generation, "put"),
	}); err != nil {
		return err
	}
	key := credentialKey(agent)
	secret.Data[PreviousCredentialKey] = append([]byte(nil), secret.Data[key]...)
	secret.Data[key] = append([]byte(nil), secret.Data[NextCredentialKey]...)
	delete(secret.Data, NextCredentialKey)
	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[CredentialGenerationAnnotation] = strconv.FormatInt(next.Generation, 10)
	secret.Annotations[PreviousCredentialGenerationAnnotation] = strconv.FormatInt(current.Generation, 10)
	if err := r.Update(ctx, secret); err != nil {
		return fmt.Errorf("promote replacement credential bundle: %w", err)
	}
	return nil
}

func (r *AgentReconciler) finishCredentialOverlap(ctx context.Context, secret *corev1.Secret) error {
	delete(secret.Data, PreviousCredentialKey)
	delete(secret.Annotations, PreviousCredentialGenerationAnnotation)
	if err := r.Update(ctx, secret); err != nil {
		return fmt.Errorf("remove retired credential from Secret: %w", err)
	}
	return nil
}

func (r *AgentReconciler) applyAgentDeployment(ctx context.Context, agent *monitoringv1alpha1.Agent) (*appsv1.Deployment, error) {
	deployment := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: agent.Name, Namespace: agent.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, deployment, func() error {
		if deployment.ResourceVersion != "" && !metav1.IsControlledBy(deployment, agent) {
			return fmt.Errorf("Deployment %s/%s is not owned by Agent %s", deployment.Namespace, deployment.Name, agent.Name)
		}
		if err := controllerutil.SetControllerReference(agent, deployment, r.Scheme); err != nil {
			return err
		}
		labels := map[string]string{"app.kubernetes.io/name": "xisnove-agent", "app.kubernetes.io/instance": agent.Name, "monitoring.xisnove.io/agent": agent.Name}
		replicas := int32(1)
		if agent.Spec.Workload.Replicas != nil {
			replicas = *agent.Spec.Workload.Replicas
		}
		image := agent.Spec.Workload.Image
		if image == "" {
			image = r.DefaultAgentImage
		}
		automount, runAsNonRoot, readOnlyRoot, allowPrivilegeEscalation := true, true, true, false
		seccomp := corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
		deployment.Labels = cloneStringMap(labels)
		deployment.Spec = appsv1.DeploymentSpec{Replicas: &replicas, Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}, Selector: &metav1.LabelSelector{MatchLabels: cloneStringMap(labels)}, Template: corev1.PodTemplateSpec{ObjectMeta: metav1.ObjectMeta{Labels: cloneStringMap(labels)}, Spec: corev1.PodSpec{
			ServiceAccountName: agent.Spec.Workload.ServiceAccountName, AutomountServiceAccountToken: &automount,
			SecurityContext: &corev1.PodSecurityContext{RunAsNonRoot: &runAsNonRoot, SeccompProfile: &seccomp}, NodeSelector: cloneStringMap(agent.Spec.Workload.NodeSelector), Tolerations: append([]corev1.Toleration(nil), agent.Spec.Workload.Tolerations...), Affinity: agent.Spec.Workload.Affinity.DeepCopy(),
			Containers: []corev1.Container{{Name: "agent", Image: image, ImagePullPolicy: corev1.PullIfNotPresent, Env: []corev1.EnvVar{{Name: "XISNOVE_URL", Value: r.ControlPlaneURL}, {Name: "XISNOVE_AGENT_ID", Value: agent.Status.ExternalID}, {Name: "XISNOVE_AGENT_CREDENTIAL_FILE", Value: credentialMountPath + "/" + credentialKey(agent)}, {Name: "XISNOVE_AGENT_CAPABILITIES", Value: joinCapabilities(agent.Spec.Capabilities)}, {Name: "XISNOVE_DISCOVERY_NAMESPACES", Value: joinNamespaces(agent)}, {Name: "XISNOVE_DISCOVERY_RESOURCES", Value: joinDiscoveryResources(agent.Spec.Discovery.Resources)}}, Resources: *agent.Spec.Workload.Resources.DeepCopy(), SecurityContext: &corev1.SecurityContext{ReadOnlyRootFilesystem: &readOnlyRoot, AllowPrivilegeEscalation: &allowPrivilegeEscalation, Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}}}, VolumeMounts: []corev1.VolumeMount{{Name: "credential", MountPath: credentialMountPath, ReadOnly: true}}}},
			Volumes:    []corev1.Volume{{Name: "credential", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: agent.Spec.CredentialSecretRef.Name, Items: []corev1.KeyToPath{{Key: credentialKey(agent), Path: credentialKey(agent)}}}}}},
		}}}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("apply Agent Deployment: %w", err)
	}
	return deployment, nil
}

func (r *AgentReconciler) recordAgentSuccess(agent *monitoringv1alpha1.Agent, state controlplane.AgentState, deployment *appsv1.Deployment) {
	agent.Status.ObservedGeneration = agent.Generation
	if !state.LastHeartbeatAt.IsZero() {
		value := metav1.NewTime(state.LastHeartbeatAt)
		agent.Status.LastHeartbeatTime = &value
	}
	if !state.LastDiscoverySyncAt.IsZero() {
		value := metav1.NewTime(state.LastDiscoverySyncAt)
		agent.Status.LastDiscoverySyncTime = &value
	}
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionRegistered, metav1.ConditionTrue, "Registered", "The operator-owned Agent identity is registered")
	workloadReady := r.recordWorkloadCondition(agent, deployment)
	heartbeatReady, heartbeatStale := r.recordHeartbeatCondition(agent)
	discoveryReady, discoveryStale := r.recordDiscoveryCondition(agent)
	ready, reason, message := metav1.ConditionTrue, "Reconciled", "Registration and workload desired state are synchronized"
	if !workloadReady {
		ready, reason, message = metav1.ConditionFalse, "WorkloadUnavailable", "The Agent Deployment is not available"
	} else if !heartbeatReady {
		ready, reason, message = metav1.ConditionFalse, "HeartbeatStale", "The Agent heartbeat is missing or stale"
	} else if discoveryStale {
		ready, reason, message = metav1.ConditionFalse, "DiscoveryStale", "The Agent heartbeat is current but its discovery catalog is stale"
	} else if !discoveryReady {
		ready, reason, message = metav1.ConditionFalse, "AwaitingDiscovery", "The Agent heartbeat is current; waiting for a complete discovery snapshot"
	}
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionReady, ready, reason, message)
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionSynced, metav1.ConditionTrue, "Applied", "Desired state is synchronized through the control-plane client")
	if heartbeatStale {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionDegraded, metav1.ConditionTrue, "HeartbeatStale", "The Agent heartbeat is missing or stale")
	} else if discoveryStale {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionDegraded, metav1.ConditionTrue, "DiscoveryStale", "The last complete discovery catalog snapshot is stale")
	} else {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionDegraded, metav1.ConditionFalse, "Healthy", "No reconciliation error is active")
	}
}

func (r *AgentReconciler) recordWorkloadCondition(agent *monitoringv1alpha1.Agent, deployment *appsv1.Deployment) bool {
	if deployment == nil || deployment.Status.ObservedGeneration < deployment.Generation {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionWorkloadReady, metav1.ConditionUnknown, "Progressing", "Waiting for the Deployment generation to be observed")
		return false
	}
	replicas := int32(1)
	if agent.Spec.Workload.Replicas != nil {
		replicas = *agent.Spec.Workload.Replicas
	}
	if deployment.Status.AvailableReplicas < replicas {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionWorkloadReady, metav1.ConditionFalse, "Unavailable", "The Deployment has insufficient available replicas")
		return false
	}
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionWorkloadReady, metav1.ConditionTrue, "Available", "The Deployment observed the desired generation and replicas are available")
	return true
}

func (r *AgentReconciler) recordHeartbeatCondition(agent *monitoringv1alpha1.Agent) (bool, bool) {
	if agent.Status.LastHeartbeatTime == nil {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionHeartbeat, metav1.ConditionFalse, "AwaitingHeartbeat", "The Agent has not reported a heartbeat yet")
		return false, true
	}
	if r.now().Sub(agent.Status.LastHeartbeatTime.Time) > r.heartbeatStaleAfter() {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionHeartbeat, metav1.ConditionFalse, "Stale", "The Agent heartbeat is stale")
		return false, true
	}
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionHeartbeat, metav1.ConditionTrue, "Observed", "The control plane has observed a recent Agent heartbeat")
	return true, false
}

func (r *AgentReconciler) recordDiscoveryCondition(agent *monitoringv1alpha1.Agent) (bool, bool) {
	if !hasDiscoveryCapability(agent.Spec.Capabilities) {
		return true, false
	}
	if agent.Status.LastDiscoverySyncTime == nil {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionDiscoveryFresh, metav1.ConditionFalse, "AwaitingCatalog", "No complete discovery catalog snapshot has been observed")
		return false, false
	}
	staleAfter := time.Duration(agent.Spec.Discovery.StaleAfterSeconds) * time.Second
	if staleAfter == 0 {
		staleAfter = 5 * time.Minute
	}
	if r.now().Sub(agent.Status.LastDiscoverySyncTime.Time) > staleAfter {
		setCondition(&agent.Status.Conditions, agent.Generation, ConditionDiscoveryFresh, metav1.ConditionFalse, "Stale", "The last complete discovery catalog snapshot is stale")
		return false, true
	}
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionDiscoveryFresh, metav1.ConditionTrue, "Fresh", "The discovery catalog has a recent complete snapshot")
	return true, false
}

func (r *AgentReconciler) recordAgentFailure(ctx context.Context, agent *monitoringv1alpha1.Agent, reconcileErr error) error {
	safe := safeError(reconcileErr)
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionReady, metav1.ConditionFalse, "ReconcileFailed", safe.Error())
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionSynced, metav1.ConditionFalse, "ApplyFailed", safe.Error())
	setCondition(&agent.Status.Conditions, agent.Generation, ConditionDegraded, metav1.ConditionTrue, "ReconcileFailed", safe.Error())
	if err := r.Status().Update(ctx, agent); err != nil {
		return safeError(fmt.Errorf("record Agent status after %v: %w", safe, err))
	}
	return safe
}

func (r *AgentReconciler) SetupWithManager(manager ctrl.Manager) error {
	if r.APIReader == nil {
		r.APIReader = manager.GetAPIReader()
	}
	return ctrl.NewControllerManagedBy(manager).For(&monitoringv1alpha1.Agent{}).Owns(&appsv1.Deployment{}).Complete(r)
}
func (r *AgentReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}
func (r *AgentReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 30 * time.Second
}
func (r *AgentReconciler) heartbeatStaleAfter() time.Duration {
	if r.HeartbeatStaleAfter > 0 {
		return r.HeartbeatStaleAfter
	}
	return 5 * time.Minute
}
func credentialKey(agent *monitoringv1alpha1.Agent) string {
	if agent.Spec.CredentialSecretRef.Key == "" {
		return CredentialKey
	}
	return agent.Spec.CredentialSecretRef.Key
}
func previousCredentialGeneration(secret *corev1.Secret) int64 {
	if secret == nil || len(secret.Data[PreviousCredentialKey]) == 0 {
		return 0
	}
	value, err := strconv.ParseInt(secret.Annotations[PreviousCredentialGenerationAnnotation], 10, 64)
	if err != nil || value < 1 {
		return 0
	}
	return value
}
func initialBundle(current *credentialBundle, secret *corev1.Secret) *credentialBundle {
	if current != nil && current.Generation == 1 {
		return current
	}
	next, err := parseCredentialBundle(secret.Data[NextCredentialKey])
	if err == nil && next.Generation == 1 {
		return &next
	}
	previous, err := parseCredentialBundle(secret.Data[PreviousCredentialKey])
	if err == nil && previous.Generation == 1 {
		return &previous
	}
	return nil
}
func credentialState(secret *corev1.Secret, key string) (*credentialBundle, *credentialBundle, error) {
	current, err := parseOptionalCredentialBundle(secret.Data[key])
	if err != nil {
		return nil, nil, fmt.Errorf("read current credential bundle: %w", err)
	}
	next, err := parseOptionalCredentialBundle(secret.Data[NextCredentialKey])
	if err != nil {
		return nil, nil, fmt.Errorf("read staged credential bundle: %w", err)
	}
	return current, next, nil
}
func parseOptionalCredentialBundle(value []byte) (*credentialBundle, error) {
	if len(value) == 0 {
		return nil, nil
	}
	bundle, err := parseCredentialBundle(value)
	if err != nil {
		return nil, err
	}
	return &bundle, nil
}
func parseCredentialBundle(value []byte) (credentialBundle, error) {
	var bundle credentialBundle
	if err := json.Unmarshal(value, &bundle); err != nil || strings.TrimSpace(bundle.Credential) == "" || bundle.Generation < 1 {
		return credentialBundle{}, errors.New("credential bundle is invalid")
	}
	return bundle, nil
}
func marshalCredentialBundle(bundle credentialBundle) ([]byte, error) { return json.Marshal(bundle) }
func newCredential() (string, error) {
	value := make([]byte, 32)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate credential: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}
func applyIdempotencyKey(agent *monitoringv1alpha1.Agent, bootstrap bool) string {
	mode := "reconcile"
	if bootstrap {
		mode = "bootstrap"
	}
	return boundedIdempotencyKey("agent-apply-"+mode, ownerFor(agent, "Agent"), agent.Generation, mode)
}
func credentialIdempotencyKey(agent *monitoringv1alpha1.Agent, generation int64, action string) string {
	owner := ownerFor(agent, "Agent")
	return boundedIdempotencyKey("agent-credential", owner, generation, action)
}
func deleteIdempotencyKey(agent *monitoringv1alpha1.Agent) string {
	owner := ownerFor(agent, "Agent")
	return boundedIdempotencyKey("agent-delete", owner, 0, "delete")
}
func joinCapabilities(capabilities []monitoringv1alpha1.AgentCapability) string {
	values := make([]string, len(capabilities))
	for index, capability := range capabilities {
		values[index] = string(capability)
	}
	return strings.Join(values, ",")
}
func joinDiscoveryResources(resources []monitoringv1alpha1.DiscoveryResource) string {
	values := make([]string, len(resources))
	for index, resource := range resources {
		values[index] = string(resource)
	}
	return strings.Join(values, ",")
}
func joinNamespaces(agent *monitoringv1alpha1.Agent) string {
	if len(agent.Spec.Discovery.Namespaces) == 0 {
		return agent.Namespace
	}
	return strings.Join(agent.Spec.Discovery.Namespaces, ",")
}
func hasDiscoveryCapability(capabilities []monitoringv1alpha1.AgentCapability) bool {
	for _, capability := range capabilities {
		if capability == monitoringv1alpha1.AgentCapabilityKubernetesDiscovery || capability == monitoringv1alpha1.AgentCapabilityKubernetesWatch {
			return true
		}
	}
	return false
}
func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
