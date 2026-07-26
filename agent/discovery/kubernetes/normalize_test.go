package kubernetes_test

import (
	"testing"

	kubernetes "github.com/araihu/xisnove/agent/discovery/kubernetes"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

func TestNormalizeResourcesProducesDeterministicProbeCandidates(t *testing.T) {
	t.Parallel()
	appProtocol := "http"
	service := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("service-uid"), Labels: map[string]string{"app": "api"}}, Spec: corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080, AppProtocol: &appProtocol}}}}
	endpointPort := int32(8443)
	slice := &discoveryv1.EndpointSlice{ObjectMeta: metav1.ObjectMeta{Name: "api-abc", Namespace: "payments", UID: types.UID("slice-uid")}, AddressType: discoveryv1.AddressTypeIPv6, Ports: []discoveryv1.EndpointPort{{Port: &endpointPort}}, Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"2001:db8::10"}}}}
	pathType := networkingv1.PathTypePrefix
	ingress := &networkingv1.Ingress{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "payments", UID: types.UID("ingress-uid")}, Spec: networkingv1.IngressSpec{TLS: []networkingv1.IngressTLS{{Hosts: []string{"api.example.test"}}}, Rules: []networkingv1.IngressRule{{Host: "api.example.test", IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{Path: "/health", PathType: &pathType}}}}}}}}
	gateway := resource("Gateway", "gateway-uid", map[string]any{"listeners": []any{map[string]any{"hostname": "api.example.test", "protocol": "HTTP", "port": int64(80)}, map[string]any{"hostname": "api.example.test", "protocol": "HTTPS", "port": int64(443)}}})
	route := resource("HTTPRoute", "route-uid", map[string]any{"hostnames": []any{"route.example.test"}, "rules": []any{map[string]any{"matches": []any{map[string]any{"path": map[string]any{"value": "/ready"}}}}}})

	tests := []struct {
		name string
		got  []kubernetes.Candidate
		want []string
	}{
		{"service", kubernetes.NormalizeService(service), []string{"http://api.payments.svc:8080"}},
		{"endpoint slice", kubernetes.NormalizeEndpointSlice(slice), []string{"tcp://[2001:db8::10]:8443"}},
		{"ingress", kubernetes.NormalizeIngress(ingress), []string{"https://api.example.test/health"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if len(tt.got) != len(tt.want) {
				t.Fatalf("candidates = %#v", tt.got)
			}
			for i, candidate := range tt.got {
				if candidate.Target != tt.want[i] || candidate.Source.UID == "" || candidate.Key == "" {
					t.Fatalf("candidate[%d] = %#v", i, candidate)
				}
			}
		})
	}
	for _, tt := range []struct {
		name string
		item *unstructured.Unstructured
		want []string
	}{{"gateway", gateway, []string{"http://api.example.test:80", "https://api.example.test:443"}}, {"HTTPRoute", route, []string{"https://route.example.test/ready"}}} {
		t.Run(tt.name, func(t *testing.T) {
			got, diagnostics := kubernetes.NormalizeGatewayResource(tt.item)
			if len(diagnostics) != 0 || len(got) != len(tt.want) {
				t.Fatalf("candidates=%#v diagnostics=%#v", got, diagnostics)
			}
			for i, candidate := range got {
				if candidate.Target != tt.want[i] || candidate.Source.Namespace != "payments" || candidate.Source.Name != "api" {
					t.Fatalf("candidate[%d] = %#v", i, candidate)
				}
			}
		})
	}
}

func TestNormalizeGRPCRouteReportsUnsupportedWithoutCandidate(t *testing.T) {
	t.Parallel()
	candidates, diagnostics := kubernetes.NormalizeGatewayResource(resource("GRPCRoute", "grpc-uid", map[string]any{"hostnames": []any{"grpc.example.test"}}))
	if len(candidates) != 0 || len(diagnostics) != 1 || diagnostics[0].Code != "unsupported-grpc-route" {
		t.Fatalf("candidates=%#v diagnostics=%#v", candidates, diagnostics)
	}
}

func resource(kind, uid string, spec map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "gateway.networking.k8s.io/v1", "kind": kind, "metadata": map[string]any{"name": "api", "namespace": "payments", "uid": uid}, "spec": spec}}
}
