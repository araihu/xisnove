package manifest

import (
	"bytes"
	"io"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/util/yaml"
)

func TestEdgeChartRendersReadOnlyDiscoveryAndScopedOperatorRBAC(t *testing.T) {
	t.Parallel()

	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate chart test")
	}
	chart := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../charts/xisnove-edge"))
	command := exec.Command(helm, "template", "edge", chart,
		"--namespace", "monitoring",
		"--set", "controlPlane.url=https://xisnove.example.test",
		"--set", "controlPlane.existingSecret.name=xisnove-provisioner",
		"--set", "agent.enabled=true",
		"--set", "agent.locationID=11111111-1111-1111-1111-111111111111",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, output)
	}

	objects := decodeObjects(t, output)
	discoveryRole := findObject(t, objects, "ClusterRole", "edge-xisnove-edge-discovery")
	rules, found, err := unstructured.NestedSlice(discoveryRole.Object, "rules")
	if err != nil || !found {
		t.Fatalf("discovery rules: found=%v err=%v", found, err)
	}
	wantResources := []string{"endpointslices", "gateways", "grpcroutes", "httproutes", "ingresses", "services"}
	var gotResources []string
	for _, rawRule := range rules {
		rule := rawRule.(map[string]any)
		verbs, _, _ := unstructured.NestedStringSlice(rule, "verbs")
		for _, verb := range verbs {
			if verb != "get" && verb != "list" && verb != "watch" {
				t.Fatalf("discovery ClusterRole grants mutation verb %q", verb)
			}
		}
		resources, _, _ := unstructured.NestedStringSlice(rule, "resources")
		for _, resource := range resources {
			if resource == "secrets" || resource == "secrets/status" {
				t.Fatal("discovery ClusterRole grants Secret access")
			}
			gotResources = append(gotResources, resource)
		}
	}
	sort.Strings(gotResources)
	if !equalStrings(gotResources, wantResources) {
		t.Fatalf("discovery resources = %#v, want %#v", gotResources, wantResources)
	}

	operatorRole := findObject(t, objects, "Role", "edge-xisnove-edge-operator")
	operatorRules, _, _ := unstructured.NestedSlice(operatorRole.Object, "rules")
	for _, rawRule := range operatorRules {
		rule := rawRule.(map[string]any)
		resources, _, _ := unstructured.NestedStringSlice(rule, "resources")
		verbs, _, _ := unstructured.NestedStringSlice(rule, "verbs")
		for _, resource := range resources {
			if resource == "*" {
				t.Fatal("operator Role uses a resource wildcard")
			}
			if resource == "secrets" && contains(verbs, "deletecollection") {
				t.Fatal("operator Role can bulk-delete Secrets")
			}
			if resource == "secrets" && (contains(verbs, "list") || contains(verbs, "watch")) {
				t.Fatal("operator Role can enumerate unrelated Secrets")
			}
		}
	}

	leaseRole := findObject(t, objects, "Role", "edge-xisnove-edge-leader-election")
	if leaseRole.GetNamespace() != "monitoring" {
		t.Fatalf("leader-election Role namespace = %q", leaseRole.GetNamespace())
	}
	leaseRules, _, _ := unstructured.NestedSlice(leaseRole.Object, "rules")
	if len(leaseRules) != 1 {
		t.Fatalf("leader-election rules = %d", len(leaseRules))
	}
	leaseRule := leaseRules[0].(map[string]any)
	leaseResources, _, _ := unstructured.NestedStringSlice(leaseRule, "resources")
	leaseVerbs, _, _ := unstructured.NestedStringSlice(leaseRule, "verbs")
	if !equalStrings(leaseResources, []string{"leases"}) || !equalStrings(leaseVerbs, []string{"create", "get", "patch", "update"}) {
		t.Fatalf("leader-election rule resources=%v verbs=%v", leaseResources, leaseVerbs)
	}

	deployment := findObject(t, objects, "Deployment", "edge-xisnove-edge-operator")
	containers, _, _ := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "containers")
	operatorContainer := containers[0].(map[string]any)
	args, _, _ := unstructured.NestedStringSlice(operatorContainer, "args")
	for _, requiredArg := range []string{"--metrics-bind-address=:8080", "--health-probe-bind-address=:8081", "--leader-elect=true", "--poll-interval=30s", "--heartbeat-stale-after=5m"} {
		if !contains(args, requiredArg) {
			t.Fatalf("operator args %v do not include %q", args, requiredArg)
		}
	}
	readyPath, _, _ := unstructured.NestedString(operatorContainer, "readinessProbe", "httpGet", "path")
	healthPath, _, _ := unstructured.NestedString(operatorContainer, "livenessProbe", "httpGet", "path")
	if readyPath != "/readyz" || healthPath != "/healthz" {
		t.Fatalf("probe paths = ready %q health %q", readyPath, healthPath)
	}
	volumes, _, _ := unstructured.NestedSlice(deployment.Object, "spec", "template", "spec", "volumes")
	secretName, _, _ := unstructured.NestedString(volumes[0].(map[string]any), "secret", "secretName")
	items, _, _ := unstructured.NestedSlice(volumes[0].(map[string]any), "secret", "items")
	if secretName != "xisnove-provisioner" || len(items) != 1 {
		t.Fatalf("operator credential volume secret=%q items=%v", secretName, items)
	}
	item := items[0].(map[string]any)
	if item["key"] != "token" || item["path"] != "credential" {
		t.Fatalf("operator credential item = %#v", item)
	}

	agent := findObject(t, objects, "Agent", "edge-xisnove-edge")
	serviceAccount, _, _ := unstructured.NestedString(agent.Object, "spec", "workload", "serviceAccountName")
	if serviceAccount != "edge-xisnove-edge-discovery" {
		t.Fatalf("Agent ServiceAccount = %q", serviceAccount)
	}
	agentSecretName, _, _ := unstructured.NestedString(agent.Object, "spec", "credentialSecretRef", "name")
	agentSecretKey, _, _ := unstructured.NestedString(agent.Object, "spec", "credentialSecretRef", "key")
	if agentSecretName == "" {
		t.Fatal("Agent has no credential Secret destination")
	}
	if agentSecretKey != "credential" {
		t.Fatalf("Agent credential bundle key = %q", agentSecretKey)
	}
	discoveryStaleAfter, _, _ := unstructured.NestedInt64(agent.Object, "spec", "discovery", "staleAfterSeconds")
	if discoveryStaleAfter != 300 {
		t.Fatalf("Agent discovery stale threshold = %d", discoveryStaleAfter)
	}
}

func TestEdgeChartRequiresExistingProvisioningSecret(t *testing.T) {
	t.Parallel()

	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate chart test")
	}
	chart := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../charts/xisnove-edge"))
	command := exec.Command(helm, "template", "edge", chart,
		"--namespace", "monitoring",
		"--set", "controlPlane.url=https://xisnove.example.test",
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("helm template unexpectedly accepted a missing existingSecret:\n%s", output)
	}
	if !bytes.Contains(output, []byte("controlPlane.existingSecret.name is required")) {
		t.Fatalf("helm template error does not identify the required existingSecret:\n%s", output)
	}
}

func decodeObjects(t *testing.T, manifest []byte) []*unstructured.Unstructured {
	t.Helper()
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	var result []*unstructured.Unstructured
	for {
		object := &unstructured.Unstructured{}
		err := decoder.Decode(object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if object.GetKind() != "" {
			result = append(result, object)
		}
	}
	return result
}

func findObject(t *testing.T, objects []*unstructured.Unstructured, kind, name string) *unstructured.Unstructured {
	t.Helper()
	for _, object := range objects {
		if object.GetKind() == kind && object.GetName() == name {
			return object
		}
	}
	t.Fatalf("%s %s not found", kind, name)
	return nil
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
