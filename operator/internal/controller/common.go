package controller

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/araihu/xisnove/operator/internal/controlplane"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	MonitorFinalizer      = "monitoring.xisnove.io/monitor-finalizer"
	AgentFinalizer        = "monitoring.xisnove.io/agent-finalizer"
	ForceDeleteAnnotation = "monitoring.xisnove.io/force-delete"

	ConditionReady          = "Ready"
	ConditionSynced         = "Synced"
	ConditionDegraded       = "Degraded"
	ConditionRegistered     = "Registered"
	ConditionWorkloadReady  = "Workload"
	ConditionHeartbeat      = "Heartbeat"
	ConditionDiscoveryFresh = "DiscoveryFresh"

	MaxConditionMessageLength = 256
)

var bearerPattern = regexp.MustCompile(`(?i)(authorization\s*:\s*bearer|bearer)\s+[^\s,;]+`)

type redactedError struct {
	cause   error
	message string
}

func (e *redactedError) Error() string { return e.message }
func (e *redactedError) Unwrap() error { return e.cause }

func safeError(err error) error {
	if err == nil {
		return nil
	}
	message := bearerPattern.ReplaceAllString(err.Error(), "Bearer [REDACTED]")
	message = boundMessage(message)
	return &redactedError{cause: err, message: message}
}

func boundMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= MaxConditionMessageLength {
		return message
	}
	const suffix = "..."
	return message[:MaxConditionMessageLength-len(suffix)] + suffix
}

func setCondition(conditions *[]metav1.Condition, generation int64, conditionType string, status metav1.ConditionStatus, reason, message string) {
	apiMeta.SetStatusCondition(conditions, metav1.Condition{
		Type:               conditionType,
		Status:             status,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            boundMessage(message),
	})
	// Conditions are status summaries, not an event stream. Keep the condition
	// just written and discard the oldest unrelated entry if an older object
	// already contained more condition types than this API permits.
	for len(*conditions) > 8 {
		drop := 0
		for index := range *conditions {
			if (*conditions)[index].Type != conditionType {
				drop = index
				break
			}
		}
		*conditions = append((*conditions)[:drop], (*conditions)[drop+1:]...)
	}
}

func ownerFor(object client.Object, kind string) controlplane.OwnerReference {
	return controlplane.OwnerReference{
		Key: fmt.Sprintf("monitoring.xisnove.io/%s/%s/%s", kind, object.GetNamespace(), object.GetName()),
		UID: string(object.GetUID()),
	}
}

func isForceDelete(object client.Object) bool {
	return strings.EqualFold(object.GetAnnotations()[ForceDeleteAnnotation], "true")
}

func ignoreRemoteNotFound(err error) error {
	if errors.Is(err, controlplane.ErrNotFound) {
		return nil
	}
	return err
}
