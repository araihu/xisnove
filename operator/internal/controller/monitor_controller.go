package controller

import (
	"context"
	"fmt"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
	"github.com/araihu/xisnove/operator/internal/controlplane"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

type MonitorReconciler struct {
	client.Client
	Scheme       *runtime.Scheme
	ControlPlane controlplane.Client
	PollInterval time.Duration
}

func (r *MonitorReconciler) Reconcile(ctx context.Context, request ctrl.Request) (ctrl.Result, error) {
	monitor := &monitoringv1alpha1.Monitor{}
	if err := r.Get(ctx, request.NamespacedName, monitor); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !monitor.DeletionTimestamp.IsZero() {
		return r.finalize(ctx, monitor)
	}
	if !controllerutil.ContainsFinalizer(monitor, MonitorFinalizer) {
		controllerutil.AddFinalizer(monitor, MonitorFinalizer)
		if err := r.Update(ctx, monitor); err != nil {
			return ctrl.Result{}, err
		}
	}

	name := monitor.Spec.Name
	if name == "" {
		name = monitor.Name
	}
	previousStatus := monitor.Status.DeepCopy()
	state, err := r.ControlPlane.ApplyMonitor(ctx, controlplane.ApplyMonitorRequest{
		Owner:      ownerFor(monitor, "Monitor"),
		ExternalID: monitor.Status.ExternalID,
		Name:       name,
		Spec:       *monitor.Spec.DeepCopy(),
	})
	if err != nil {
		return ctrl.Result{}, r.recordFailure(ctx, monitor, err)
	}
	if state.ExternalID == "" {
		return ctrl.Result{}, r.recordFailure(ctx, monitor, fmt.Errorf("control plane returned an empty monitor identifier"))
	}

	monitor.Status.ObservedGeneration = monitor.Generation
	monitor.Status.ExternalID = state.ExternalID
	monitor.Status.Health.State = state.AggregateHealth
	if !state.HealthLastTransitionAt.IsZero() {
		transition := metav1.NewTime(state.HealthLastTransitionAt)
		monitor.Status.Health.LastTransitionTime = &transition
	}
	setCondition(&monitor.Status.Conditions, monitor.Generation, ConditionReady, metav1.ConditionTrue, "Reconciled", "The operator-owned Monitor is ready")
	setCondition(&monitor.Status.Conditions, monitor.Generation, ConditionSynced, metav1.ConditionTrue, "Applied", "Desired state is synchronized through the control-plane client")
	degraded, reason, message := monitorDegradedCondition(state.AggregateHealth)
	setCondition(&monitor.Status.Conditions, monitor.Generation, ConditionDegraded, degraded, reason, message)
	if !equality.Semantic.DeepEqual(previousStatus, &monitor.Status) {
		if err := r.Status().Update(ctx, monitor); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{RequeueAfter: r.pollInterval()}, nil
}

func monitorDegradedCondition(health string) (metav1.ConditionStatus, string, string) {
	switch health {
	case "up":
		return metav1.ConditionFalse, "Healthy", "The aggregate health is up"
	case "down", "degraded":
		return metav1.ConditionTrue, "Unhealthy", "The aggregate health requires attention"
	default:
		return metav1.ConditionUnknown, "HealthUnknown", "The aggregate health is pending or unknown"
	}
}

func (r *MonitorReconciler) finalize(ctx context.Context, monitor *monitoringv1alpha1.Monitor) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(monitor, MonitorFinalizer) {
		return ctrl.Result{}, nil
	}
	if !isForceDelete(monitor) {
		err := r.ControlPlane.DeleteMonitor(ctx, controlplane.DeleteRemoteObjectRequest{
			Owner:      ownerFor(monitor, "Monitor"),
			ExternalID: monitor.Status.ExternalID,
		})
		if err = ignoreRemoteNotFound(err); err != nil {
			return ctrl.Result{}, safeError(err)
		}
	}
	controllerutil.RemoveFinalizer(monitor, MonitorFinalizer)
	if err := r.Update(ctx, monitor); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *MonitorReconciler) recordFailure(ctx context.Context, monitor *monitoringv1alpha1.Monitor, reconcileErr error) error {
	safe := safeError(reconcileErr)
	setCondition(&monitor.Status.Conditions, monitor.Generation, ConditionReady, metav1.ConditionFalse, "ReconcileFailed", safe.Error())
	setCondition(&monitor.Status.Conditions, monitor.Generation, ConditionSynced, metav1.ConditionFalse, "ApplyFailed", safe.Error())
	setCondition(&monitor.Status.Conditions, monitor.Generation, ConditionDegraded, metav1.ConditionTrue, "ReconcileFailed", safe.Error())
	monitor.Status.ObservedGeneration = monitor.Generation
	if err := r.Status().Update(ctx, monitor); err != nil {
		return safeError(fmt.Errorf("record Monitor status after %v: %w", safe, err))
	}
	return safe
}

func (r *MonitorReconciler) SetupWithManager(manager ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(manager).
		For(&monitoringv1alpha1.Monitor{}).
		Complete(r)
}

func (r *MonitorReconciler) pollInterval() time.Duration {
	if r.PollInterval > 0 {
		return r.PollInterval
	}
	return 30 * time.Second
}
