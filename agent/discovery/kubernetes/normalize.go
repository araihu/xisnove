// Package kubernetes turns read-only Kubernetes objects into stable Agent
// discovery candidates. It never creates monitors or mutates Kubernetes.
package kubernetes

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type SourceReference struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
	UID        string
}

type Candidate struct {
	Key      string
	Source   SourceReference
	Protocol string
	Target   string
	Labels   map[string]string
}

// Diagnostic is deliberately bounded by callers before publication. It is
// local operational feedback, not a probe target or an absence assertion.
type Diagnostic struct {
	Code    string
	Message string
	Source  SourceReference
}

func NormalizeService(service *corev1.Service) []Candidate {
	if service == nil || service.Spec.Type == corev1.ServiceTypeExternalName {
		return nil
	}
	source := sourceFor("v1", "Service", service.Namespace, service.Name, string(service.UID))
	host := service.Name + "." + service.Namespace + ".svc"
	result := make([]Candidate, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		if port.Port <= 0 || (port.Protocol != "" && port.Protocol != corev1.ProtocolTCP) {
			continue
		}
		protocol := "tcp"
		if (port.AppProtocol != nil && strings.HasPrefix(strings.ToLower(*port.AppProtocol), "http")) || strings.HasPrefix(strings.ToLower(port.Name), "http") {
			protocol = "http"
		}
		result = append(result, candidateFor(source, protocol, protocol+"://"+net.JoinHostPort(host, strconv.Itoa(int(port.Port))), service.Labels))
	}
	return sorted(result)
}

func NormalizeEndpointSlice(slice *discoveryv1.EndpointSlice) []Candidate {
	if slice == nil {
		return nil
	}
	source := sourceFor(discoveryv1.SchemeGroupVersion.String(), "EndpointSlice", slice.Namespace, slice.Name, string(slice.UID))
	var result []Candidate
	for _, endpoint := range slice.Endpoints {
		for _, address := range endpoint.Addresses {
			for _, port := range slice.Ports {
				if port.Port == nil || *port.Port <= 0 || (port.Protocol != nil && *port.Protocol != corev1.ProtocolTCP) {
					continue
				}
				result = append(result, candidateFor(source, "tcp", "tcp://"+net.JoinHostPort(address, strconv.Itoa(int(*port.Port))), slice.Labels))
			}
		}
	}
	return sorted(result)
}

func NormalizeIngress(ingress *networkingv1.Ingress) []Candidate {
	if ingress == nil {
		return nil
	}
	tlsHosts := make(map[string]struct{})
	for _, tls := range ingress.Spec.TLS {
		for _, host := range tls.Hosts {
			tlsHosts[host] = struct{}{}
		}
	}
	source := sourceFor(networkingv1.SchemeGroupVersion.String(), "Ingress", ingress.Namespace, ingress.Name, string(ingress.UID))
	var result []Candidate
	for _, rule := range ingress.Spec.Rules {
		if rule.Host == "" || rule.HTTP == nil {
			continue
		}
		scheme := "http"
		if _, ok := tlsHosts[rule.Host]; ok {
			scheme = "https"
		}
		for _, path := range rule.HTTP.Paths {
			result = append(result, candidateFor(source, "http", (&url.URL{Scheme: scheme, Host: rule.Host, Path: normalizedPath(path.Path)}).String(), ingress.Labels))
		}
	}
	return sorted(result)
}

// NormalizeGatewayResource accepts unstructured optional Gateway API resources
// so clusters without the CRDs remain supported.
func NormalizeGatewayResource(resource *unstructured.Unstructured) ([]Candidate, []Diagnostic) {
	if resource == nil {
		return nil, nil
	}
	source := sourceFor(resource.GetAPIVersion(), resource.GetKind(), resource.GetNamespace(), resource.GetName(), string(resource.GetUID()))
	switch resource.GetKind() {
	case "Gateway":
		return normalizeGateway(source, resource), nil
	case "HTTPRoute":
		return normalizeHTTPRoute(source, resource), nil
	case "GRPCRoute":
		return nil, []Diagnostic{{Code: "unsupported-grpc-route", Message: "GRPCRoute discovery is not supported by Agent v1", Source: source}}
	default:
		return nil, []Diagnostic{{Code: "unsupported-gateway-resource", Message: fmt.Sprintf("unsupported Gateway API kind %q", resource.GetKind()), Source: source}}
	}
}

func normalizeGateway(source SourceReference, resource *unstructured.Unstructured) []Candidate {
	listeners, found, err := unstructured.NestedSlice(resource.Object, "spec", "listeners")
	if err != nil || !found {
		return nil
	}
	var result []Candidate
	for _, raw := range listeners {
		listener, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		host, _, _ := unstructured.NestedString(listener, "hostname")
		listenerProtocol, _, _ := unstructured.NestedString(listener, "protocol")
		port, _, _ := unstructured.NestedInt64(listener, "port")
		if host == "" || port <= 0 || port > 65535 {
			continue
		}
		protocol, scheme, ok := gatewayProtocol(listenerProtocol)
		if !ok {
			continue
		}
		result = append(result, candidateFor(source, protocol, scheme+"://"+net.JoinHostPort(host, strconv.FormatInt(port, 10)), resource.GetLabels()))
	}
	return sorted(result)
}

func gatewayProtocol(value string) (protocol, scheme string, ok bool) {
	switch strings.ToLower(value) {
	case "http":
		return "http", "http", true
	case "https", "tls":
		return "http", "https", true
	case "tcp":
		return "tcp", "tcp", true
	default:
		return "", "", false
	}
}

func normalizeHTTPRoute(source SourceReference, resource *unstructured.Unstructured) []Candidate {
	hostnames, found, err := unstructured.NestedStringSlice(resource.Object, "spec", "hostnames")
	if err != nil || !found {
		return nil
	}
	paths := httpRoutePaths(resource)
	var result []Candidate
	for _, hostname := range hostnames {
		for _, path := range paths {
			result = append(result, candidateFor(source, "http", (&url.URL{Scheme: "https", Host: hostname, Path: normalizedPath(path)}).String(), resource.GetLabels()))
		}
	}
	return sorted(result)
}

func httpRoutePaths(resource *unstructured.Unstructured) []string {
	rules, _, _ := unstructured.NestedSlice(resource.Object, "spec", "rules")
	paths := map[string]struct{}{}
	for _, rawRule := range rules {
		rule, ok := rawRule.(map[string]any)
		if !ok {
			continue
		}
		matches, _, _ := unstructured.NestedSlice(rule, "matches")
		for _, rawMatch := range matches {
			match, ok := rawMatch.(map[string]any)
			if !ok {
				continue
			}
			path, _, _ := unstructured.NestedString(match, "path", "value")
			if path != "" {
				paths[normalizedPath(path)] = struct{}{}
			}
		}
	}
	if len(paths) == 0 {
		return []string{"/"}
	}
	result := make([]string, 0, len(paths))
	for path := range paths {
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func sourceFor(apiVersion, kind, namespace, name, uid string) SourceReference {
	return SourceReference{APIVersion: apiVersion, Kind: kind, Namespace: namespace, Name: name, UID: uid}
}

func candidateFor(source SourceReference, protocol, target string, labels map[string]string) Candidate {
	return Candidate{Key: source.UID + "/" + protocol + "/" + target, Source: source, Protocol: protocol, Target: target, Labels: cloneLabels(labels)}
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(labels))
	for key, value := range labels {
		result[key] = value
	}
	return result
}

func normalizedPath(path string) string {
	if path == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func sorted(candidates []Candidate) []Candidate {
	sort.Slice(candidates, func(left, right int) bool { return candidates[left].Key < candidates[right].Key })
	return candidates
}
