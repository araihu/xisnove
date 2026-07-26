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
	for _, requiredArg := range []string{"--metrics-bind-address=:8080", "--health-probe-bind-address=:8081", "--leader-elect=true", "--poll-interval=30s", "--heartbeat-stale-after=5m", "--request-timeout=15s", "--graceful-shutdown-timeout=30s"} {
		if !contains(args, requiredArg) {
			t.Fatalf("operator args %v do not include %q", args, requiredArg)
		}
	}
	readyPath, _, _ := unstructured.NestedString(operatorContainer, "readinessProbe", "httpGet", "path")
	healthPath, _, _ := unstructured.NestedString(operatorContainer, "livenessProbe", "httpGet", "path")
	if readyPath != "/readyz" || healthPath != "/healthz" {
		t.Fatalf("probe paths = ready %q health %q", readyPath, healthPath)
	}
	terminationGracePeriod, _, _ := unstructured.NestedInt64(deployment.Object, "spec", "template", "spec", "terminationGracePeriodSeconds")
	if terminationGracePeriod != 45 {
		t.Fatalf("termination grace period = %d", terminationGracePeriod)
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
	if !bytes.Contains(output, []byte("controlPlane.existingSecret.name must be a non-empty string")) {
		t.Fatalf("helm template error does not identify the required existingSecret:\n%s", output)
	}
}

func TestEdgeChartRequiresExistingProvisioningSecretKey(t *testing.T) {
	t.Parallel()

	output, err := renderEdgeChart(t,
		"--skip-schema-validation",
		"--set", "controlPlane.url=https://xisnove.example.test",
		"--set", "controlPlane.existingSecret.name=xisnove-provisioner",
		"--set", "controlPlane.existingSecret.key=",
	)
	if err == nil {
		t.Fatalf("helm template unexpectedly accepted an empty existingSecret key:\n%s", output)
	}
	if !bytes.Contains(output, []byte("controlPlane.existingSecret.key must be a non-empty string")) {
		t.Fatalf("helm template error does not identify the required existingSecret key:\n%s", output)
	}
}

func TestEdgeChartRejectsActiveActiveWithoutLeaderElection(t *testing.T) {
	t.Parallel()

	valid := [][]string{
		{"--set", "operator.replicas=1", "--set", "operator.leaderElection=false"},
		{"--set", "operator.replicas=2", "--set", "operator.leaderElection=true"},
	}
	for _, arguments := range valid {
		arguments = append(requiredChartValues(), arguments...)
		if output, err := renderEdgeChart(t, arguments...); err != nil {
			t.Fatalf("valid manager topology %v failed: %v\n%s", arguments, err, output)
		}
	}

	arguments := append(requiredChartValues(), "--set", "operator.replicas=2", "--set", "operator.leaderElection=false")
	output, err := renderEdgeChart(t, arguments...)
	if err == nil {
		t.Fatalf("helm template accepted two active managers without leader election:\n%s", output)
	}
	if !bytes.Contains(output, []byte("leader election is required when operator.replicas is greater than 1")) {
		t.Fatalf("unexpected active-active validation error:\n%s", output)
	}
}

func TestEdgeChartRejectsUnsafeShutdownBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		graceful  string
		podBudget string
		message   string
	}{
		{name: "equal", graceful: "30", podBudget: "30", message: "operator.terminationGracePeriodSeconds must be greater than operator.gracefulShutdownTimeoutSeconds, and both must be positive"},
		{name: "lower", graceful: "30", podBudget: "29", message: "operator.terminationGracePeriodSeconds must be greater than operator.gracefulShutdownTimeoutSeconds, and both must be positive"},
		{name: "negative graceful", graceful: "-1", podBudget: "45", message: "operator.gracefulShutdownTimeoutSeconds"},
		{name: "negative pod budget", graceful: "30", podBudget: "-1", message: "operator.terminationGracePeriodSeconds"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			arguments := append(requiredChartValues(),
				"--set", "operator.gracefulShutdownTimeoutSeconds="+test.graceful,
				"--set", "operator.terminationGracePeriodSeconds="+test.podBudget,
			)
			output, err := renderEdgeChart(t, arguments...)
			if err == nil {
				t.Fatalf("helm template accepted unsafe shutdown budget:\n%s", output)
			}
			if !bytes.Contains(output, []byte(test.message)) {
				t.Fatalf("unexpected shutdown validation error:\n%s", output)
			}
		})
	}
}

func TestEdgeChartSchemaRejectsMistypedSecurityValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		field     string
		typeName  string
	}{
		{name: "leader election string", arguments: []string{"--set-string", "operator.leaderElection=false", "--set", "operator.replicas=2"}, field: "leaderElection", typeName: "boolean"},
		{name: "numeric Secret name", arguments: []string{"--set", "controlPlane.existingSecret.name=123"}, field: "name", typeName: "string"},
		{name: "boolean Secret key", arguments: []string{"--set", "controlPlane.existingSecret.key=true"}, field: "key", typeName: "string"},
		{name: "whitespace Secret name", arguments: []string{"--set-string", "controlPlane.existingSecret.name=   "}, field: "name", typeName: "pattern"},
		{name: "whitespace Secret key", arguments: []string{"--set-string", "controlPlane.existingSecret.key=   "}, field: "key", typeName: "pattern"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			arguments := append(requiredChartValues(), test.arguments...)
			output, err := renderEdgeChart(t, arguments...)
			if err == nil {
				t.Fatalf("helm template accepted mistyped value:\n%s", output)
			}
			if !bytes.Contains(output, []byte(test.field)) || !bytes.Contains(output, []byte(test.typeName)) {
				t.Fatalf("schema error does not identify %s as %s:\n%s", test.field, test.typeName, output)
			}
		})
	}
}

func TestEdgeChartRejectsWhitespaceAndMistypedSecretsInTemplate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		arguments []string
		message   string
	}{
		{name: "whitespace name", arguments: []string{"--skip-schema-validation", "--set-string", "controlPlane.existingSecret.name=   "}, message: "controlPlane.existingSecret.name must be a non-empty string"},
		{name: "whitespace key", arguments: []string{"--skip-schema-validation", "--set-string", "controlPlane.existingSecret.key=   "}, message: "controlPlane.existingSecret.key must be a non-empty string"},
		{name: "numeric name", arguments: []string{"--skip-schema-validation", "--set", "controlPlane.existingSecret.name=123"}, message: "controlPlane.existingSecret.name must be a non-empty string"},
		{name: "boolean key", arguments: []string{"--skip-schema-validation", "--set", "controlPlane.existingSecret.key=true"}, message: "controlPlane.existingSecret.key must be a non-empty string"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			arguments := append(requiredChartValues(), test.arguments...)
			output, err := renderEdgeChart(t, arguments...)
			if err == nil {
				t.Fatalf("helm template accepted unsafe Secret value:\n%s", output)
			}
			if !bytes.Contains(output, []byte(test.message)) {
				t.Fatalf("unexpected Secret validation error:\n%s", output)
			}
		})
	}
}

func TestEdgeChartTemplateRejectsStringLeaderElectionWithoutSchema(t *testing.T) {
	t.Parallel()

	arguments := append(requiredChartValues(),
		"--skip-schema-validation",
		"--set", "operator.replicas=2",
		"--set-string", "operator.leaderElection=false",
	)
	output, err := renderEdgeChart(t, arguments...)
	if err == nil {
		t.Fatalf("helm template accepted string leaderElection:\n%s", output)
	}
	if !bytes.Contains(output, []byte("operator.leaderElection must be a boolean")) {
		t.Fatalf("unexpected leaderElection type error:\n%s", output)
	}
}

func requiredChartValues() []string {
	return []string{
		"--set", "controlPlane.url=https://xisnove.example.test",
		"--set", "controlPlane.existingSecret.name=xisnove-provisioner",
	}
}

func renderEdgeChart(t *testing.T, arguments ...string) ([]byte, error) {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm is not installed")
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate chart test")
	}
	chart := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "../../../charts/xisnove-edge"))
	base := []string{"template", "edge", chart, "--namespace", "monitoring"}
	return exec.Command(helm, append(base, arguments...)...).CombinedOutput()
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
