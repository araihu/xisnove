package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:validation:Enum=http;tcp;dns;kubernetes-discovery;kubernetes-watch
type AgentCapability string

const (
	AgentCapabilityHTTP                AgentCapability = "http"
	AgentCapabilityTCP                 AgentCapability = "tcp"
	AgentCapabilityDNS                 AgentCapability = "dns"
	AgentCapabilityKubernetesDiscovery AgentCapability = "kubernetes-discovery"
	AgentCapabilityKubernetesWatch     AgentCapability = "kubernetes-watch"
)

// +kubebuilder:validation:Enum=services;endpointSlices;ingresses;gateways;httpRoutes;grpcRoutes
type DiscoveryResource string

const (
	DiscoveryResourceService       DiscoveryResource = "services"
	DiscoveryResourceEndpointSlice DiscoveryResource = "endpointSlices"
	DiscoveryResourceIngress       DiscoveryResource = "ingresses"
	DiscoveryResourceGateway       DiscoveryResource = "gateways"
	DiscoveryResourceHTTPRoute     DiscoveryResource = "httpRoutes"
	DiscoveryResourceGRPCRoute     DiscoveryResource = "grpcRoutes"
)

// +kubebuilder:validation:XValidation:rule="!has(self.key) || self.key != 'credential.previous'",message="credential.previous is reserved for overlap-safe rotation"
type SecretKeyReference struct {
	// Name is a Secret in the Agent namespace. The operator owns this Secret.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:default=credential
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+$`
	Key string `json:"key,omitempty"`
}

type AgentCredentialRotationSpec struct {
	// RequestedGeneration is advanced explicitly by an administrator. Scheduled rotation is not performed in v1.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	RequestedGeneration int64 `json:"requestedGeneration,omitempty"`
}

type AgentDiscoverySpec struct {
	// Namespaces is the catalog scope. An empty list means the Agent namespace only.
	// +kubebuilder:validation:MaxItems=64
	// +listType=set
	Namespaces []string `json:"namespaces,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	Resources []DiscoveryResource `json:"resources,omitempty"`
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=30
	// +kubebuilder:validation:Maximum=86400
	StaleAfterSeconds int32 `json:"staleAfterSeconds,omitempty"`
}

type AgentWorkloadSpec struct {
	// Image overrides the operator default Agent image.
	Image string `json:"image,omitempty"`
	// ServiceAccountName must refer to a chart- or administrator-owned read-only discovery ServiceAccount.
	// +kubebuilder:validation:MinLength=1
	ServiceAccountName string `json:"serviceAccountName"`
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	Replicas     *int32                      `json:"replicas,omitempty"`
	Resources    corev1.ResourceRequirements `json:"resources,omitempty"`
	NodeSelector map[string]string           `json:"nodeSelector,omitempty"`
	Tolerations  []corev1.Toleration         `json:"tolerations,omitempty"`
	Affinity     *corev1.Affinity            `json:"affinity,omitempty"`
}

type AgentSpec struct {
	// LocationID is the immutable public control-plane location identifier.
	// +kubebuilder:validation:Format=uuid
	LocationID string `json:"locationID"`
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	Capabilities        []AgentCapability           `json:"capabilities"`
	CredentialSecretRef SecretKeyReference          `json:"credentialSecretRef"`
	CredentialRotation  AgentCredentialRotationSpec `json:"credentialRotation,omitempty"`
	Discovery           AgentDiscoverySpec          `json:"discovery,omitempty"`
	Workload            AgentWorkloadSpec           `json:"workload"`
}

type CredentialRotationPhase string

const (
	RotationPhaseNone              CredentialRotationPhase = ""
	RotationPhaseAwaitingHeartbeat CredentialRotationPhase = "AwaitingHeartbeat"
	RotationPhaseComplete          CredentialRotationPhase = "Complete"
)

type AgentStatus struct {
	ObservedGeneration int64  `json:"observedGeneration,omitempty"`
	ExternalID         string `json:"externalID,omitempty"`
	// CredentialGeneration is safe metadata. Credential material is never copied to status.
	CredentialGeneration         int64                   `json:"credentialGeneration,omitempty"`
	PreviousCredentialGeneration *int64                  `json:"previousCredentialGeneration,omitempty"`
	RotationPhase                CredentialRotationPhase `json:"rotationPhase,omitempty"`
	LastHeartbeatTime            *metav1.Time            `json:"lastHeartbeatTime,omitempty"`
	LastDiscoverySyncTime        *metav1.Time            `json:"lastDiscoverySyncTime,omitempty"`
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=xagent
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Registered",type=string,JSONPath=`.status.conditions[?(@.type=='Registered')].status`
// +kubebuilder:printcolumn:name="External ID",type=string,JSONPath=`.status.externalID`
// +kubebuilder:printcolumn:name="Credential",type=integer,JSONPath=`.status.credentialGeneration`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Agent struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              AgentSpec   `json:"spec"`
	Status            AgentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type AgentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Agent `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Agent{}, &AgentList{})
}
