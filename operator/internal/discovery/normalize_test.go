package discovery

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestNormalizeKubernetesResourcesProducesStableCatalogCandidates(t *testing.T) {
	t.Parallel()

	appProtocol := "http"
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("service-uid"), Labels: map[string]string{"app": "api"}},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080, AppProtocol: &appProtocol}}},
	}
	ingressPath := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("ingress-uid")},
		Spec: networkingv1.IngressSpec{
			TLS:   []networkingv1.IngressTLS{{Hosts: []string{"api.example.test"}}},
			Rules: []networkingv1.IngressRule{{Host: "api.example.test", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/health", PathType: &ingressPath}}}}}},
		},
	}
	httpRoute := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gateway.networking.k8s.io/v1", "kind": "HTTPRoute",
		"metadata": map[string]any{"name": "api", "namespace": "payments", "uid": "route-uid"},
		"spec":     map[string]any{"hostnames": []any{"route.example.test"}, "rules": []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"value": "/ready"}}}}}},
	}}

	serviceCandidates := NormalizeService(service)
	if len(serviceCandidates) != 1 || serviceCandidates[0].Target != "http://api.payments.svc:8080" {
		t.Fatalf("service candidates = %#v", serviceCandidates)
	}
	if serviceCandidates[0].Key != "service-uid/http/http://api.payments.svc:8080" {
		t.Fatalf("stable service key = %q", serviceCandidates[0].Key)
	}
	serviceCandidates = WithPerspective(serviceCandidates, "kubernetes:homelab")
	if serviceCandidates[0].Perspective != "kubernetes:homelab" {
		t.Fatalf("perspective = %q", serviceCandidates[0].Perspective)
	}
	ingressCandidates := NormalizeIngress(ingress)
	if len(ingressCandidates) != 1 || ingressCandidates[0].Target != "https://api.example.test/health" {
		t.Fatalf("ingress candidates = %#v", ingressCandidates)
	}
	routeCandidates, err := NormalizeGatewayResource(httpRoute)
	if err != nil {
		t.Fatal(err)
	}
	if len(routeCandidates) != 1 || routeCandidates[0].Target != "https://route.example.test/ready" {
		t.Fatalf("route candidates = %#v", routeCandidates)
	}
	for _, candidate := range append(append(serviceCandidates, ingressCandidates...), routeCandidates...) {
		if candidate.Source.UID == "" || candidate.Stale {
			t.Fatalf("invalid active catalog candidate = %#v", candidate)
		}
	}
}

func TestNormalizeEndpointSliceHandlesIPv6WithoutLosingSourceIdentity(t *testing.T) {
	t.Parallel()

	portName := "https"
	port := int32(8443)
	slice := &discoveryv1.EndpointSlice{
		ObjectMeta:  metav1.ObjectMeta{Name: "api-abc", Namespace: "payments", UID: types.UID("slice-uid")},
		AddressType: discoveryv1.AddressTypeIPv6,
		Ports:       []discoveryv1.EndpointPort{{Name: &portName, Port: &port}},
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"2001:db8::10"}}},
	}

	candidates := NormalizeEndpointSlice(slice)
	if len(candidates) != 1 || candidates[0].Target != "tcp://[2001:db8::10]:8443" {
		t.Fatalf("EndpointSlice candidates = %#v", candidates)
	}
	if candidates[0].Source.UID != "slice-uid" {
		t.Fatalf("source = %#v", candidates[0].Source)
	}
}

func TestReconcileStalenessMarksMissingCandidatesWithoutDeletingCatalogRecords(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	previous := []Candidate{
		{Key: "gone", Target: "https://gone.example.test", Source: SourceReference{UID: "gone-uid"}},
		{Key: "kept", Target: "https://kept.example.test", Source: SourceReference{UID: "kept-uid"}},
	}
	current := []Candidate{{Key: "kept", Target: "https://kept.example.test", Source: SourceReference{UID: "kept-uid"}}}

	reconciled := ReconcileSnapshot(previous, Snapshot{Candidates: current, ObservedAt: now, Complete: true})
	if len(reconciled) != 2 {
		t.Fatalf("records = %d, want 2", len(reconciled))
	}
	byKey := map[string]Candidate{}
	for _, candidate := range reconciled {
		byKey[candidate.Key] = candidate
	}
	if !byKey["gone"].Stale || byKey["gone"].StaleSince == nil || !byKey["gone"].StaleSince.Equal(now) {
		t.Fatalf("missing candidate was not retained as stale: %#v", byKey["gone"])
	}
	if byKey["kept"].Stale || byKey["kept"].StaleSince != nil {
		t.Fatalf("current candidate marked stale: %#v", byKey["kept"])
	}
}

func TestIncompleteSnapshotNeverMarksMissingCandidatesStale(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 25, 12, 0, 0, 0, time.UTC)
	previous := []Candidate{
		{Key: "temporarily-unseen", Target: "https://api.example.test", Source: SourceReference{UID: "api-uid"}},
	}

	reconciled := ReconcileSnapshot(previous, Snapshot{ObservedAt: now, Complete: false})
	if len(reconciled) != 1 || reconciled[0].Stale || reconciled[0].StaleSince != nil {
		t.Fatalf("partial observation changed freshness: %#v", reconciled)
	}
}
