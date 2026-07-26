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
		"--set", "controlPlane.provisioningSecret.name=xisnove-provisioner",
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

	agent := findObject(t, objects, "Agent", "edge-xisnove-edge")
	serviceAccount, _, _ := unstructured.NestedString(agent.Object, "spec", "workload", "serviceAccountName")
	if serviceAccount != "edge-xisnove-edge-discovery" {
		t.Fatalf("Agent ServiceAccount = %q", serviceAccount)
	}
	secretName, _, _ := unstructured.NestedString(agent.Object, "spec", "credentialSecretRef", "name")
	if secretName == "" {
		t.Fatal("Agent has no credential Secret destination")
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
