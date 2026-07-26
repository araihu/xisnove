package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// StatusRange is an inclusive HTTP status range.
type StatusRange struct {
	// +kubebuilder:validation:Minimum=100
	// +kubebuilder:validation:Maximum=599
	Minimum int32 `json:"minimum"`
	// +kubebuilder:validation:Minimum=100
	// +kubebuilder:validation:Maximum=599
	Maximum int32 `json:"maximum"`
}

type HTTPProbeSpec struct {
	// +kubebuilder:default=GET
	// +kubebuilder:validation:Enum=GET;HEAD;POST;PUT;PATCH;DELETE;OPTIONS
	Method string `json:"method,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=4096
	URL string `json:"url"`
	// +kubebuilder:validation:MaxProperties=100
	Headers map[string]string `json:"headers,omitempty"`
	// +kubebuilder:validation:MaxLength=5464
	Body string `json:"body,omitempty"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=20
	ExpectedStatus []StatusRange `json:"expectedStatus"`
	// +kubebuilder:validation:MaxItems=20
	BodyContains []string `json:"bodyContains,omitempty"`
	// +kubebuilder:validation:MaxItems=20
	BodyDoesNotContain []string `json:"bodyDoesNotContain,omitempty"`
	FollowRedirects    bool     `json:"followRedirects,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=31536000
	TLSMinimumRemainingSeconds *int64 `json:"tlsMinimumRemainingSeconds,omitempty"`
}

type TCPProbeSpec struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Host string `json:"host"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
	// +kubebuilder:validation:MaxLength=5464
	Send string `json:"send,omitempty"`
	// +kubebuilder:validation:MaxLength=5464
	Expect string `json:"expect,omitempty"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=31536000
	TLSMinimumRemainingSeconds *int64 `json:"tlsMinimumRemainingSeconds,omitempty"`
}

type DNSProbeSpec struct {
	// +kubebuilder:validation:MaxLength=261
	Resolver string `json:"resolver,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:Enum=A;AAAA;CNAME;MX;NS;TXT;SRV
	RecordType string `json:"recordType"`
	// +kubebuilder:validation:MaxItems=20
	ExpectedValues []string `json:"expectedValues,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="(self.kind == 'http' && has(self.http) && !has(self.tcp) && !has(self.dns)) || (self.kind == 'tcp' && has(self.tcp) && !has(self.http) && !has(self.dns)) || (self.kind == 'dns' && has(self.dns) && !has(self.http) && !has(self.tcp))",message="exactly one probe configuration must match kind"
type MonitorProbeSpec struct {
	// +kubebuilder:validation:Enum=http;tcp;dns
	Kind string         `json:"kind"`
	HTTP *HTTPProbeSpec `json:"http,omitempty"`
	TCP  *TCPProbeSpec  `json:"tcp,omitempty"`
	DNS  *DNSProbeSpec  `json:"dns,omitempty"`
}

type MonitorSpec struct {
	// Name defaults to metadata.name when omitted.
	// +kubebuilder:validation:MaxLength=200
	Name string `json:"name,omitempty"`
	// +kubebuilder:validation:MaxLength=2048
	Description string `json:"description,omitempty"`
	// +kubebuilder:validation:MaxProperties=64
	Labels map[string]string `json:"labels,omitempty"`
	// +kubebuilder:validation:Minimum=0
	DisplayOrder int32 `json:"displayOrder,omitempty"`
	Public       bool  `json:"public,omitempty"`
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=86400
	IntervalSeconds int32 `json:"intervalSeconds"`
	// +kubebuilder:validation:Minimum=100
	// +kubebuilder:validation:Maximum=120000
	TimeoutMillis int32 `json:"timeoutMillis"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	FailureThreshold int32 `json:"failureThreshold"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	RecoveryThreshold int32 `json:"recoveryThreshold"`
	// LocationID is the immutable public control-plane location identifier.
	// +kubebuilder:validation:Format=uuid
	LocationID       string           `json:"locationID"`
	RequiredLocation bool             `json:"requiredLocation"`
	Probe            MonitorProbeSpec `json:"probe"`
}

type MonitorHealthStatus struct {
	// +kubebuilder:validation:Enum=unknown;up;degraded;down
	State              string       `json:"state,omitempty"`
	LastTransitionTime *metav1.Time `json:"lastTransitionTime,omitempty"`
}

type MonitorStatus struct {
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// ExternalID is the identifier of the operator-owned control-plane object.
	ExternalID string              `json:"externalID,omitempty"`
	Health     MonitorHealthStatus `json:"health,omitempty"`
	// Conditions contains only bounded current state; incident history remains in the control plane.
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=8
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=xmon
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=='Ready')].status`
// +kubebuilder:printcolumn:name="Health",type=string,JSONPath=`.status.health.state`
// +kubebuilder:printcolumn:name="External ID",type=string,JSONPath=`.status.externalID`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type Monitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MonitorSpec   `json:"spec"`
	Status            MonitorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type MonitorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Monitor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Monitor{}, &MonitorList{})
}
