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
	ErrNotFound          = errors.New("control-plane object not found")
	ErrOwnershipConflict = errors.New("control-plane ownership conflict")
)

type OwnerReference struct {
	// Key is stable across retries and includes the Kubernetes UID so a
	// recreated resource cannot take ownership of an older remote object.
	Key string
}

type ApplyMonitorRequest struct {
	Owner      OwnerReference
	ExternalID string
	Name       string
	Spec       monitoringv1alpha1.MonitorSpec
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
	ExternalID string
}

type ApplyAgentRequest struct {
	Owner                OwnerReference
	ExternalID           string
	Name                 string
	Spec                 monitoringv1alpha1.AgentSpec
	NeedsCredential      bool
	CredentialGeneration int64
	IdempotencyKey       string
}

type IssuedCredential struct {
	// Value is write-only Secret material and must never be logged or copied to status.
	Value      []byte
	Generation int64
}

type AgentState struct {
	ExternalID                    string
	Credential                    *IssuedCredential
	HeartbeatCredentialGeneration int64
	LastHeartbeatAt               time.Time
	LastDiscoverySyncAt           time.Time
}

type IssueAgentCredentialRequest struct {
	Owner               OwnerReference
	ExternalID          string
	RequestedGeneration int64
	IdempotencyKey      string
}

type RevokeAgentCredentialRequest struct {
	Owner      OwnerReference
	ExternalID string
	Generation int64
}

type Client interface {
	ApplyMonitor(context.Context, ApplyMonitorRequest) (MonitorState, error)
	DeleteMonitor(context.Context, DeleteRemoteObjectRequest) error
	ApplyAgent(context.Context, ApplyAgentRequest) (AgentState, error)
	IssueAgentCredential(context.Context, IssueAgentCredentialRequest) (IssuedCredential, error)
	RevokeAgentCredential(context.Context, RevokeAgentCredentialRequest) error
	DeleteAgent(context.Context, DeleteRemoteObjectRequest) error
}
