// Package controlplane defines the narrow, contract-independent boundary used
// by the Kubernetes controllers. A generated SDK adapter is intentionally not
// implemented until the public API contract is frozen.
package controlplane

import (
	"context"
	"errors"
	"time"

	monitoringv1alpha1 "github.com/araihu/xisnove/operator/api/v1alpha1"
)

var (
	ErrNotFound           = errors.New("control-plane object not found")
	ErrOwnershipConflict  = errors.New("control-plane ownership conflict")
	ErrCredentialConflict = errors.New("control-plane credential conflict")
)

type OwnerReference struct {
	// Key is stable across retries. UID distinguishes a recreated Kubernetes
	// resource from the object it replaced.
	Key string
	UID string
}

type ApplyMonitorRequest struct {
	Owner          OwnerReference
	ExternalID     string
	Name           string
	Spec           monitoringv1alpha1.MonitorSpec
	IdempotencyKey string
}

type MonitorState struct {
	ExternalID             string
	AggregateHealth        string
	HealthLastTransitionAt time.Time
}

type DeleteRemoteObjectRequest struct {
	Owner OwnerReference
	// ExternalID may be empty when a remote apply succeeded but the Kubernetes
	// status write was lost. Implementations must then resolve strictly by Owner.
	ExternalID     string
	IdempotencyKey string
}

type ApplyAgentRequest struct {
	Owner      OwnerReference
	ExternalID string
	Name       string
	Spec       monitoringv1alpha1.AgentSpec
	// InitialCredential is write-only Secret material. The adapter sends it only
	// with generation 1; it must never be logged or copied to status.
	InitialCredential []byte
	IdempotencyKey    string
}

type AgentState struct {
	ExternalID                    string
	CredentialGeneration          int64
	PresentedCredentialGeneration int64
	LastHeartbeatAt               time.Time
	LastDiscoverySyncAt           time.Time
}

type ObserveAgentRequest struct {
	Owner      OwnerReference
	ExternalID string
}

type PutAgentCredentialRequest struct {
	Owner      OwnerReference
	ExternalID string
	Generation int64
	// Credential is write-only Secret material and must never be logged or copied to status.
	Credential     []byte
	IdempotencyKey string
}

type RevokeAgentCredentialRequest struct {
	Owner          OwnerReference
	ExternalID     string
	Generation     int64
	IdempotencyKey string
}

type Client interface {
	ApplyMonitor(context.Context, ApplyMonitorRequest) (MonitorState, error)
	DeleteMonitor(context.Context, DeleteRemoteObjectRequest) error
	ApplyAgent(context.Context, ApplyAgentRequest) (AgentState, error)
	ObserveAgent(context.Context, ObserveAgentRequest) (AgentState, error)
	PutAgentCredential(context.Context, PutAgentCredentialRequest) error
	RevokeAgentCredential(context.Context, RevokeAgentCredentialRequest) error
	DeleteAgent(context.Context, DeleteRemoteObjectRequest) error
}
