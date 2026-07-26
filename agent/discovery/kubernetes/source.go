package kubernetes

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/araihu/xisnove/agent/discovery"
	"github.com/araihu/xisnove/agent/internal/controlplane"
	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/dynamic"
	coretyped "k8s.io/client-go/kubernetes/typed/core/v1"
	discoverytyped "k8s.io/client-go/kubernetes/typed/discovery/v1"
	networkingtyped "k8s.io/client-go/kubernetes/typed/networking/v1"
	"k8s.io/client-go/tools/cache"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"
)

type Resource string

const (
	ResourceServices       Resource = "services"
	ResourceEndpointSlices Resource = "endpointSlices"
	ResourceIngresses      Resource = "ingresses"
	ResourceGateways       Resource = "gateways"
	ResourceHTTPRoutes     Resource = "httpRoutes"
	ResourceGRPCRoutes     Resource = "grpcRoutes"
)

var gatewayResources = map[Resource]schema.GroupVersionResource{
	ResourceGateways:   {Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Resource: "gateways"},
	ResourceHTTPRoutes: {Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Resource: "httproutes"},
	ResourceGRPCRoutes: {Group: gatewayv1.GroupVersion.Group, Version: gatewayv1.GroupVersion.Version, Resource: "grpcroutes"},
}

// Source lists only the configured read-only resource kinds. The narrow typed
// client interfaces make this usable with the client-go fake client in tests;
// optional Gateway resources use the dynamic client to avoid requiring CRDs.
type Source struct {
	Core         coretyped.CoreV1Interface
	Discovery    discoverytyped.DiscoveryV1Interface
	Networking   networkingtyped.NetworkingV1Interface
	Dynamic      dynamic.Interface
	Namespaces   []string
	Resources    []Resource
	Perspective  string
	Now          func() time.Time
	OnDiagnostic func(Diagnostic)
}

// Watchers creates one informer-backed, read-only watcher for each configured
// resource scope. A watcher always republishes a partial observation.
func (source Source) Watchers(publisher discovery.Publisher) ([]Watcher, error) {
	if publisher == nil {
		return nil, errors.New("Kubernetes discovery watcher publisher is not configured")
	}
	if source.Perspective == "" || len(source.Namespaces) == 0 || len(source.Resources) == 0 {
		return nil, errors.New("Kubernetes discovery source is not configured")
	}
	result := make([]Watcher, 0, len(source.Namespaces)*len(source.Resources))
	for _, namespace := range source.Namespaces {
		for _, resource := range source.Resources {
			relists := make(chan struct{}, 1)
			listWatch, object, err := source.listWatch(namespace, resource, relists)
			if err != nil {
				return nil, err
			}
			listWatch = cache.ToListWatcherWithWatchListSemantics(listWatch.(*cache.ListWatch), watchListUnsupported{})
			watchSource := source
			watchSource.Namespaces = []string{namespace}
			watchSource.Resources = []Resource{resource}
			result = append(result, Watcher{Informer: cache.NewSharedIndexInformer(listWatch, object, 0, cache.Indexers{}), Source: watchSource, Publish: publisher, Relists: relists})
		}
	}
	return result, nil
}

func (source Source) listWatch(namespace string, resource Resource, relists chan<- struct{}) (cache.ListerWatcher, runtime.Object, error) {
	switch resource {
	case ResourceServices:
		if source.Core == nil {
			return nil, nil, errors.New("Kubernetes core client is not configured")
		}
		return &cache.ListWatch{ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			items, err := source.Core.Services(namespace).List(ctx, options)
			if err == nil {
				signal(relists)
			}
			return items, err
		}, WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return source.Core.Services(namespace).Watch(ctx, options)
		}}, &corev1.Service{}, nil
	case ResourceEndpointSlices:
		if source.Discovery == nil {
			return nil, nil, errors.New("Kubernetes discovery client is not configured")
		}
		return &cache.ListWatch{ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			items, err := source.Discovery.EndpointSlices(namespace).List(ctx, options)
			if err == nil {
				signal(relists)
			}
			return items, err
		}, WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return source.Discovery.EndpointSlices(namespace).Watch(ctx, options)
		}}, &discoveryv1.EndpointSlice{}, nil
	case ResourceIngresses:
		if source.Networking == nil {
			return nil, nil, errors.New("Kubernetes networking client is not configured")
		}
		return &cache.ListWatch{ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			items, err := source.Networking.Ingresses(namespace).List(ctx, options)
			if err == nil {
				signal(relists)
			}
			return items, err
		}, WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return source.Networking.Ingresses(namespace).Watch(ctx, options)
		}}, &networkingv1.Ingress{}, nil
	case ResourceGateways, ResourceHTTPRoutes, ResourceGRPCRoutes:
		if source.Dynamic == nil {
			return nil, nil, errors.New("Kubernetes dynamic client is not configured")
		}
		client := source.Dynamic.Resource(gatewayResources[resource]).Namespace(namespace)
		return &cache.ListWatch{ListWithContextFunc: func(ctx context.Context, options metav1.ListOptions) (runtime.Object, error) {
			items, err := client.List(ctx, options)
			if err == nil {
				signal(relists)
			}
			return items, err
		}, WatchFuncWithContext: func(ctx context.Context, options metav1.ListOptions) (watch.Interface, error) {
			return client.Watch(ctx, options)
		}}, &unstructured.Unstructured{}, nil
	default:
		return nil, nil, fmt.Errorf("unsupported Kubernetes discovery resource %q", resource)
	}
}

func signal(events chan<- struct{}) {
	select {
	case events <- struct{}{}:
	default:
	}
}

type watchListUnsupported struct{}

func (watchListUnsupported) IsWatchListSemanticsUnSupported() bool { return true }

func (source Source) Snapshot(ctx context.Context) (discovery.Batch, error) {
	batch, err := source.collect(ctx)
	if err != nil {
		return discovery.Batch{}, err
	}
	if len(batch.Candidates) > discovery.MaxBatchSize {
		return discovery.Batch{}, discovery.ErrBatchTooLarge
	}
	return batch, nil
}

// Snapshots satisfies discovery.MultiProducer. An oversized inventory is
// emitted as bounded partial batches: none is an absence assertion.
func (source Source) Snapshots(ctx context.Context) ([]discovery.Batch, error) {
	batch, err := source.collect(ctx)
	if err != nil {
		return nil, err
	}
	if len(batch.Candidates) <= discovery.MaxBatchSize {
		return []discovery.Batch{batch}, nil
	}
	source.diagnostic(Diagnostic{Code: "inventory-exceeds-batch-limit", Message: fmt.Sprintf("Kubernetes inventory has %d candidates; publishing partial batches without absence claims", len(batch.Candidates))})
	result := make([]discovery.Batch, 0, (len(batch.Candidates)+discovery.MaxBatchSize-1)/discovery.MaxBatchSize)
	for start := 0; start < len(batch.Candidates); start += discovery.MaxBatchSize {
		end := min(start+discovery.MaxBatchSize, len(batch.Candidates))
		result = append(result, discovery.Batch{ID: fmt.Sprintf("%s-chunk-%d", batch.ID, len(result)+1), Candidates: append([]controlplane.DiscoveryCandidateInput(nil), batch.Candidates[start:end]...), Complete: false, CompletedAt: batch.CompletedAt})
	}
	return result, nil
}

func (source Source) collect(ctx context.Context) (discovery.Batch, error) {
	if err := ctx.Err(); err != nil {
		return discovery.Batch{}, err
	}
	if source.Perspective == "" || len(source.Namespaces) == 0 || len(source.Resources) == 0 {
		return discovery.Batch{}, errors.New("Kubernetes discovery source is not configured")
	}
	now := time.Now().UTC()
	if source.Now != nil {
		now = source.Now().UTC()
	}
	var candidates []Candidate
	for _, namespace := range source.Namespaces {
		for _, resource := range source.Resources {
			listed, diagnostics, err := source.list(ctx, namespace, resource)
			if err != nil {
				return discovery.Batch{}, fmt.Errorf("list %s in namespace %q: %w", resource, namespace, err)
			}
			for _, diagnostic := range diagnostics {
				source.diagnostic(diagnostic)
			}
			candidates = append(candidates, listed...)
		}
	}
	if err := ctx.Err(); err != nil {
		return discovery.Batch{}, err
	}
	inputs := candidateInputs(candidates, source.Perspective, now)
	return discovery.Batch{ID: "kubernetes-" + uuid.NewString(), Candidates: inputs, Complete: true, CompletedAt: now}, nil
}

func (source Source) list(ctx context.Context, namespace string, resource Resource) ([]Candidate, []Diagnostic, error) {
	switch resource {
	case ResourceServices:
		if source.Core == nil {
			return nil, nil, errors.New("Kubernetes core client is not configured")
		}
		items, err := source.Core.Services(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil, err
		}
		result := make([]Candidate, 0)
		for i := range items.Items {
			result = append(result, NormalizeService(&items.Items[i])...)
		}
		return result, nil, nil
	case ResourceEndpointSlices:
		if source.Discovery == nil {
			return nil, nil, errors.New("Kubernetes discovery client is not configured")
		}
		items, err := source.Discovery.EndpointSlices(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil, err
		}
		result := make([]Candidate, 0)
		for i := range items.Items {
			result = append(result, NormalizeEndpointSlice(&items.Items[i])...)
		}
		return result, nil, nil
	case ResourceIngresses:
		if source.Networking == nil {
			return nil, nil, errors.New("Kubernetes networking client is not configured")
		}
		items, err := source.Networking.Ingresses(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil, err
		}
		result := make([]Candidate, 0)
		for i := range items.Items {
			result = append(result, NormalizeIngress(&items.Items[i])...)
		}
		return result, nil, nil
	case ResourceGateways, ResourceHTTPRoutes, ResourceGRPCRoutes:
		if source.Dynamic == nil {
			return nil, nil, errors.New("Kubernetes dynamic client is not configured")
		}
		items, err := source.Dynamic.Resource(gatewayResources[resource]).Namespace(namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, nil, err
		}
		result := make([]Candidate, 0)
		var diagnostics []Diagnostic
		for i := range items.Items {
			candidates, found := NormalizeGatewayResource(&items.Items[i])
			result, diagnostics = append(result, candidates...), append(diagnostics, found...)
		}
		return result, diagnostics, nil
	default:
		return nil, nil, fmt.Errorf("unsupported Kubernetes discovery resource %q", resource)
	}
}

func (source Source) diagnostic(diagnostic Diagnostic) {
	if source.OnDiagnostic != nil {
		source.OnDiagnostic(diagnostic)
	}
}

func candidateInputs(candidates []Candidate, perspective string, observedAt time.Time) []controlplane.DiscoveryCandidateInput {
	byKey := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		byKey[candidate.Key] = candidate
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]controlplane.DiscoveryCandidateInput, 0, len(keys))
	for _, key := range keys {
		candidate := byKey[key]
		protocol, ok := controlplaneProtocol(candidate.Protocol)
		if !ok || candidate.Source.UID == "" {
			continue
		}
		result = append(result, controlplane.DiscoveryCandidateInput{SourceKind: strings.ToLower(candidate.Source.Kind), SourceUid: candidate.Source.UID, Namespace: candidate.Source.Namespace, Name: candidate.Source.Name, Labels: boundedLabels(candidate.Labels), Protocol: protocol, Target: candidate.Target, NetworkPerspective: perspective, Present: true, ObservedAt: observedAt})
	}
	return result
}

func controlplaneProtocol(value string) (controlplane.DiscoveryCandidateInputProtocol, bool) {
	switch value {
	case "http":
		return controlplane.DiscoveryCandidateInputProtocolHttp, true
	case "tcp":
		return controlplane.DiscoveryCandidateInputProtocolTcp, true
	case "dns":
		return controlplane.DiscoveryCandidateInputProtocolDns, true
	default:
		return "", false
	}
}

func boundedLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > 64 {
		keys = keys[:64]
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		result[key] = labels[key]
	}
	return result
}
