// Package discovery converts read-only Kubernetes observations into a stable
// candidate catalog. It deliberately has no monitor-creation operation:
// promotion is an explicit control-plane action.
package discovery

import (
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type SourceReference struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace"`
	Name       string `json:"name"`
	UID        string `json:"uid"`
}

type Candidate struct {
	Key         string            `json:"key"`
	Source      SourceReference   `json:"source"`
	Perspective string            `json:"perspective"`
	Protocol    string            `json:"protocol"`
	Target      string            `json:"target"`
	Labels      map[string]string `json:"labels,omitempty"`
	Stale       bool              `json:"stale"`
	StaleSince  *time.Time        `json:"staleSince,omitempty"`
}

// Snapshot is one discovery observation. Complete must be true only after all
// configured list/watch sources succeeded; partial observations can upsert
// candidates but can never make an unseen candidate stale.
type Snapshot struct {
	Candidates []Candidate
	ObservedAt time.Time
	Complete   bool
}

// WithPerspective binds candidates to the network perspective from which the
// target is reachable. Perspective becomes part of the stable catalog key.
func WithPerspective(candidates []Candidate, perspective string) []Candidate {
	result := append([]Candidate(nil), candidates...)
	for index := range result {
		result[index].Perspective = perspective
		result[index].Key = perspective + "/" + result[index].Key
	}
	return result
}

func NormalizeService(service *corev1.Service) []Candidate {
	if service == nil || service.Spec.Type == corev1.ServiceTypeExternalName {
		return nil
	}
	host := service.Name + "." + service.Namespace + ".svc"
	result := make([]Candidate, 0, len(service.Spec.Ports))
	for _, port := range service.Spec.Ports {
		protocol := "tcp"
		if port.AppProtocol != nil && strings.HasPrefix(strings.ToLower(*port.AppProtocol), "http") || strings.HasPrefix(strings.ToLower(port.Name), "http") {
			protocol = "http"
		}
		target := protocol + "://" + net.JoinHostPort(host, strconv.Itoa(int(port.Port)))
		result = append(result, candidateFor(
			SourceReference{APIVersion: "v1", Kind: "Service", Namespace: service.Namespace, Name: service.Name, UID: string(service.UID)},
			protocol, target, service.Labels,
		))
	}
	return sorted(result)
}

func NormalizeEndpointSlice(slice *discoveryv1.EndpointSlice) []Candidate {
	if slice == nil {
		return nil
	}
	source := SourceReference{APIVersion: discoveryv1.SchemeGroupVersion.String(), Kind: "EndpointSlice", Namespace: slice.Namespace, Name: slice.Name, UID: string(slice.UID)}
	var result []Candidate
	for _, endpoint := range slice.Endpoints {
		for _, address := range endpoint.Addresses {
			for _, port := range slice.Ports {
				if port.Port == nil {
					continue
				}
				protocol := "tcp"
				target := protocol + "://" + net.JoinHostPort(address, strconv.Itoa(int(*port.Port)))
				result = append(result, candidateFor(source, protocol, target, slice.Labels))
			}
		}
	}
	return sorted(result)
}

func NormalizeIngress(ingress *networkingv1.Ingress) []Candidate {
	if ingress == nil {
		return nil
	}
	tlsHosts := map[string]struct{}{}
	for _, tls := range ingress.Spec.TLS {
		for _, host := range tls.Hosts {
			tlsHosts[host] = struct{}{}
		}
	}
	source := SourceReference{APIVersion: networkingv1.SchemeGroupVersion.String(), Kind: "Ingress", Namespace: ingress.Namespace, Name: ingress.Name, UID: string(ingress.UID)}
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
			target := (&url.URL{Scheme: scheme, Host: rule.Host, Path: normalizedPath(path.Path)}).String()
			result = append(result, candidateFor(source, "http", target, ingress.Labels))
		}
	}
	return sorted(result)
}

// NormalizeGatewayResource supports Gateway, HTTPRoute, and GRPCRoute through
// unstructured objects, so discovery keeps working when Gateway API CRDs are
// not installed and the operator module does not pin their Go types.
func NormalizeGatewayResource(resource *unstructured.Unstructured) ([]Candidate, error) {
	if resource == nil {
		return nil, nil
	}
	source := SourceReference{APIVersion: resource.GetAPIVersion(), Kind: resource.GetKind(), Namespace: resource.GetNamespace(), Name: resource.GetName(), UID: string(resource.GetUID())}
	switch resource.GetKind() {
	case "Gateway":
		return normalizeGateway(source, resource)
	case "HTTPRoute":
		return normalizeRoute(source, resource, "http")
	case "GRPCRoute":
		return normalizeRoute(source, resource, "grpc")
	default:
		return nil, fmt.Errorf("unsupported Gateway API kind %q", resource.GetKind())
	}
}

func normalizeGateway(source SourceReference, resource *unstructured.Unstructured) ([]Candidate, error) {
	listeners, found, err := unstructured.NestedSlice(resource.Object, "spec", "listeners")
	if err != nil {
		return nil, fmt.Errorf("read Gateway listeners: %w", err)
	}
	if !found {
		return nil, nil
	}
	var result []Candidate
	for _, raw := range listeners {
		listener, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		host, _, _ := unstructured.NestedString(listener, "hostname")
		protocol, _, _ := unstructured.NestedString(listener, "protocol")
		port, _, _ := unstructured.NestedInt64(listener, "port")
		if host == "" || port == 0 {
			continue
		}
		protocol = strings.ToLower(protocol)
		scheme := protocol
		if protocol == "https" || protocol == "tls" {
			scheme = "https"
		} else if protocol == "http" {
			scheme = "http"
		}
		target := scheme + "://" + net.JoinHostPort(host, strconv.FormatInt(port, 10))
		result = append(result, candidateFor(source, protocol, target, resource.GetLabels()))
	}
	return sorted(result), nil
}

func normalizeRoute(source SourceReference, resource *unstructured.Unstructured, protocol string) ([]Candidate, error) {
	hostnames, found, err := unstructured.NestedStringSlice(resource.Object, "spec", "hostnames")
	if err != nil {
		return nil, fmt.Errorf("read %s hostnames: %w", resource.GetKind(), err)
	}
	if !found {
		return nil, nil
	}
	paths := []string{"/"}
	if protocol == "http" {
		paths = httpRoutePaths(resource)
	}
	var result []Candidate
	for _, hostname := range hostnames {
		for _, path := range paths {
			scheme := "https"
			if protocol == "grpc" {
				scheme = "grpcs"
			}
			target := (&url.URL{Scheme: scheme, Host: hostname, Path: normalizedPath(path)}).String()
			result = append(result, candidateFor(source, protocol, target, resource.GetLabels()))
		}
	}
	return sorted(result), nil
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

func ReconcileSnapshot(previous []Candidate, snapshot Snapshot) []Candidate {
	if snapshot.Complete {
		return reconcileStaleness(previous, snapshot.Candidates, snapshot.ObservedAt)
	}
	byKey := make(map[string]Candidate, len(previous)+len(snapshot.Candidates))
	for _, candidate := range previous {
		byKey[candidate.Key] = candidate
	}
	for _, candidate := range snapshot.Candidates {
		candidate.Stale = false
		candidate.StaleSince = nil
		byKey[candidate.Key] = candidate
	}
	result := make([]Candidate, 0, len(byKey))
	for _, candidate := range byKey {
		result = append(result, candidate)
	}
	return sorted(result)
}

func reconcileStaleness(previous, current []Candidate, observedAt time.Time) []Candidate {
	byKey := make(map[string]Candidate, len(previous)+len(current))
	for _, candidate := range previous {
		candidate.Stale = true
		if candidate.StaleSince == nil {
			staleSince := observedAt
			candidate.StaleSince = &staleSince
		}
		byKey[candidate.Key] = candidate
	}
	for _, candidate := range current {
		candidate.Stale = false
		candidate.StaleSince = nil
		byKey[candidate.Key] = candidate
	}
	result := make([]Candidate, 0, len(byKey))
	for _, candidate := range byKey {
		result = append(result, candidate)
	}
	return sorted(result)
}

func candidateFor(source SourceReference, protocol, target string, labels map[string]string) Candidate {
	return Candidate{Key: source.UID + "/" + protocol + "/" + target, Source: source, Protocol: protocol, Target: target, Labels: cloneLabels(labels)}
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
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
