package integration_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/araihu/xisnove/sdk"
)

const (
	kindRelease            = "journey"
	kindProvisioningSecret = "xisnove-operator-provisioning"
)

type kindEnvironment struct {
	baseURL       string
	clusterURL    string
	namespace     string
	operatorImage string
	agentImage    string
	helm          string
	kubectl       string
	docker        string
	server        string
}

func TestKindFailureEvidenceNeverExportsSecretValues(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(repositoryRoot(t), "scripts", "kind-edge-e2e.sh"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "get secret") && (strings.Contains(line, "-o yaml") || strings.Contains(line, "-o json")) {
			t.Fatalf("failure evidence must not serialize Secret objects: %s", strings.TrimSpace(line))
		}
	}
	for _, required := range []string{"get secrets -o go-template=", "secret-metadata.txt", "refusing to retain failure artifacts containing Secret values"} {
		if !strings.Contains(text, required) {
			t.Fatalf("failure evidence guard %q is missing", required)
		}
	}
}

func TestKindBuildContextExcludesLocalSecretsAndState(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	lines := make(map[string]bool)
	for _, line := range strings.Split(string(contents), "\n") {
		lines[strings.TrimSpace(line)] = true
	}
	for _, required := range []string{".git", ".env", ".env.*", ".artifacts", ".superpowers", ".worktrees"} {
		if !lines[required] {
			t.Fatalf("Docker build context exclusion %q is missing", required)
		}
	}
}

func TestKubernetesEdgeKind(t *testing.T) {
	if os.Getenv("XISNOVE_KIND_E2E") != "1" {
		t.Skip("set XISNOVE_KIND_E2E=1 or run make kind-edge-e2e")
	}
	environment := loadKindEnvironment(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	client, err := sdk.NewClientWithResponses(environment.baseURL, sdk.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	password, err := os.ReadFile(requiredEnvironment(t, "XISNOVE_KIND_E2E_ADMIN_PASSWORD_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	passwordValue := strings.TrimSpace(string(password))
	session, err := client.CreateSessionWithResponse(ctx, sdk.CreateSessionRequest{
		Email:    openapi_types.Email(requiredEnvironment(t, "XISNOVE_KIND_E2E_ADMIN_EMAIL")),
		Password: &passwordValue,
	})
	if err != nil || session.JSON201 == nil {
		t.Fatalf("create administrator session: status=%v err=%v", responseStatus(session), err)
	}
	admin := bearer(session.JSON201.Token)
	location := ensureKindLocation(t, ctx, client, admin)
	namespaceManifest := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", environment.namespace)
	runCommand(t, ctx, []byte(namespaceManifest), environment.kubectl, "apply", "-f", "-")
	if !kubernetesResourceExists(ctx, environment.kubectl, environment.namespace, "secret", kindProvisioningSecret) {
		tokenKey := sdk.IdempotencyKey("kind-edge-operator-token")
		provisioner, err := client.CreateAPITokenWithResponse(ctx, &sdk.CreateAPITokenParams{IdempotencyKey: &tokenKey}, sdk.CreateAPITokenRequest{
			Name: "kind-edge-operator", Scopes: []sdk.APITokenScope{"operator:provision"},
		}, admin)
		if err != nil || provisioner.JSON201 == nil || provisioner.JSON201.Token == "" {
			t.Fatalf("create operator token: status=%v err=%v", responseStatus(provisioner), err)
		}
		secretManifest, err := json.Marshal(map[string]any{
			"apiVersion": "v1", "kind": "Secret", "metadata": map[string]string{"name": kindProvisioningSecret, "namespace": environment.namespace},
			"type": "Opaque", "stringData": map[string]string{"token": provisioner.JSON201.Token},
		})
		if err != nil {
			t.Fatal(err)
		}
		runCommand(t, ctx, secretManifest, environment.kubectl, "apply", "-f", "-")
	}
	operatorRepository, operatorTag := splitImage(t, environment.operatorImage)
	agentRepository, agentTag := splitImage(t, environment.agentImage)
	runCommand(t, ctx, nil, environment.helm, "upgrade", "--install", kindRelease, filepath.Join(repositoryRoot(t), "charts", "xisnove-edge"),
		"--namespace", environment.namespace,
		"--set-string", "controlPlane.url="+environment.clusterURL,
		"--set-string", "controlPlane.existingSecret.name="+kindProvisioningSecret,
		"--set-string", "controlPlane.existingSecret.key=token",
		"--set-string", "operator.image.repository="+operatorRepository,
		"--set-string", "operator.image.tag="+operatorTag,
		"--set-string", "operator.pollInterval=1s",
		"--set-string", "operator.heartbeatStaleAfter=30s",
		"--set-string", "agent.enabled=true",
		"--set-string", "agent.locationID="+location.Id.String(),
		"--set-string", "agent.image.repository="+agentRepository,
		"--set-string", "agent.image.tag="+agentTag,
		"--set", "agent.discovery.resources={services,endpointSlices,ingresses}",
		"--set", "metrics.service.enabled=false",
		"--wait", "--timeout", "180s")

	monitorManifest := fmt.Sprintf(`apiVersion: monitoring.xisnove.io/v1alpha1
kind: Monitor
metadata:
  name: edge-health
  namespace: %s
spec:
  intervalSeconds: 30
  timeoutMillis: 5000
  failureThreshold: 2
  recoveryThreshold: 2
  locationID: %s
  requiredLocation: true
  probe:
    kind: http
    http:
      method: GET
      url: https://example.test/health
      expectedStatus:
        - minimum: 200
          maximum: 299
`, environment.namespace, location.Id)
	runCommand(t, ctx, []byte(monitorManifest), environment.kubectl, "apply", "-f", "-")
	waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.status.externalID}", func(value string) bool { return value != "" })
	agentName := kindRelease + "-xisnove-edge"
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, "{.status.externalID}", func(value string) bool { return value != "" })

	// The real in-cluster Agent must publish an empty complete snapshot before
	// any fixture is created. This proves that emptiness is a committed catalog
	// observation rather than a skipped no-op.
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, "{.status.lastDiscoverySyncTime}", func(value string) bool { return value != "" })
	emptyCatalog := listDiscoveryCandidates(t, ctx, client, admin)
	for _, candidate := range emptyCatalog {
		if candidate.Present {
			t.Fatalf("initial complete discovery snapshot retained a present candidate: id=%s", candidate.Id)
		}
	}

	discoveryServiceAccount := kindRelease + "-xisnove-edge-discovery"
	identity := "system:serviceaccount:" + environment.namespace + ":" + discoveryServiceAccount
	for _, verb := range []string{"get", "list", "watch"} {
		answer := kubectlCanI(t, ctx, environment.kubectl, verb, "secrets", identity, environment.namespace)
		if answer != "no" {
			t.Fatalf("discovery identity can %s Secrets: %q", verb, answer)
		}
	}
	if answer := kubectlCanI(t, ctx, environment.kubectl, "list", "services", identity, environment.namespace); answer != "yes" {
		t.Fatalf("discovery identity cannot list Services: %q", answer)
	}

	runCommand(t, ctx, nil, environment.kubectl, "apply", "-f", filepath.Join(repositoryRoot(t), "integration", "testdata", "kind", "fixtures.yaml"))
	candidate := waitForDiscoveryCandidate(t, ctx, client, admin, func(candidate sdk.DiscoveryCandidate) bool {
		return candidate.SourceKind == "ingress" && candidate.Name == "catalog-target" && candidate.Present
	})
	if candidate.PromotedMonitorId != nil || candidate.State != sdk.DiscoveryCandidateStatePending {
		t.Fatalf("discovery implicitly promoted a Monitor: state=%s promoted=%t", candidate.State, candidate.PromotedMonitorId != nil)
	}
	promotionKey := sdk.IdempotencyKey("kind-edge-promote-ingress-" + candidate.Id.String())
	promoted, err := client.PromoteDiscoveryCandidateWithResponse(ctx, candidate.Id, &sdk.PromoteDiscoveryCandidateParams{IdempotencyKey: &promotionKey}, sdk.PromotionRequest{
		Name: "Catalog target " + candidate.Id.String()[:8], LocationId: location.Id, RequiredLocation: true, Public: false,
		IntervalSeconds: 30, TimeoutMillis: 5000, FailureThreshold: 2, RecoveryThreshold: 2,
	}, admin)
	if err != nil || promoted.JSON201 == nil || promoted.JSON201.Candidate.PromotedMonitorId == nil || *promoted.JSON201.Candidate.PromotedMonitorId != promoted.JSON201.Monitor.Id {
		t.Fatalf("promote discovery candidate: status=%v err=%v", responseStatus(promoted), err)
	}
	runCommand(t, ctx, nil, environment.kubectl, "delete", "-f", filepath.Join(repositoryRoot(t), "integration", "testdata", "kind", "fixtures.yaml"), "--ignore-not-found=true")
	// Deletion produces no non-empty partial observation. Restarting the Agent
	// forces its bootstrap full-list path immediately instead of waiting for the
	// production five-minute complete-snapshot cadence.
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "restart", "deployment/"+agentName)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+agentName, "--timeout=120s")
	tombstone := waitForDiscoveryCandidate(t, ctx, client, admin, func(observed sdk.DiscoveryCandidate) bool {
		return observed.Id == candidate.Id && !observed.Present
	})
	if tombstone.PromotedMonitorId == nil || *tombstone.PromotedMonitorId != promoted.JSON201.Monitor.Id {
		t.Fatalf("complete empty discovery snapshot lost promoted monitor link: candidate=%s", candidate.Id)
	}

	assertInterruptedCredentialRotation(t, ctx, environment, client, admin, agentName)
	assertAPIPartitionRecovery(t, ctx, environment, agentName)
	assertControlPlaneRestart(t, ctx, environment, client, admin, candidate, promoted.JSON201.Monitor.Id, agentName)
	assertRecreatedMonitorOwnershipRefusal(t, ctx, environment, client, admin, []byte(monitorManifest))

	assertNoOperationalKubernetesState(t, ctx, environment)
}

func assertAPIPartitionRecovery(t *testing.T, ctx context.Context, environment kindEnvironment, agentName string) {
	t.Helper()
	operatorDeployment := kindRelease + "-xisnove-edge-operator"
	current := readCredentialBundle(t, ctx, environment, agentName+"-credential", "credential")
	desired := current.Generation + 1
	runCommand(t, ctx, nil, environment.docker, "network", "disconnect", "kind", environment.server)
	connected := false
	t.Cleanup(func() {
		if !connected {
			bestEffortCommand(environment.docker, "network", "connect", "kind", environment.server)
		}
		bestEffortCommand(environment.kubectl, "-n", environment.namespace, "scale", "deployment/"+operatorDeployment, "--replicas=1")
	})
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "patch", "monitor", "edge-health", "--type=merge", "-p", `{"spec":{"description":"queued during control-plane partition"}}`)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "patch", "agent", agentName, "--type=merge", "-p", fmt.Sprintf(`{"spec":{"credentialRotation":{"requestedGeneration":%d}}}`, desired))
	waitForJSONPath(t, ctx, environment, "monitor/edge-health", `{.status.conditions[?(@.type=="Synced")].status}`, func(value string) bool { return value == "False" })
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, `{.status.conditions[?(@.type=="Synced")].status}`, func(value string) bool { return value == "False" })
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "restart", "deployment/"+operatorDeployment)
	runCommand(t, ctx, nil, environment.docker, "network", "connect", "kind", environment.server)
	connected = true
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+operatorDeployment, "--timeout=120s")
	waitForJSONPath(t, ctx, environment, "monitor/edge-health", `{.status.conditions[?(@.type=="Synced")].status}`, func(value string) bool { return value == "True" })
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, "{.status.credentialGeneration}", func(value string) bool { return value == fmt.Sprint(desired) })
	waitForSecretKey(t, ctx, environment, agentName+"-credential", "credential.previous", true)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "restart", "deployment/"+agentName)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+agentName, "--timeout=120s")
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, "{.status.rotationPhase}", func(value string) bool { return value == "Complete" })
}

func assertControlPlaneRestart(t *testing.T, ctx context.Context, environment kindEnvironment, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn, candidate sdk.DiscoveryCandidate, promotedID openapi_types.UUID, agentName string) {
	t.Helper()
	monitorID := waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.status.externalID}", func(value string) bool { return value != "" })
	readinessClient := &http.Client{Timeout: 2 * time.Second}
	runCommand(t, ctx, nil, environment.docker, "restart", environment.server)
	published := runCommand(t, ctx, nil, environment.docker, "port", environment.server, "8080/tcp")
	separator := strings.LastIndex(published, ":")
	if separator < 0 || separator == len(published)-1 {
		t.Fatalf("unexpected control-plane port mapping %q", published)
	}
	restartedBaseURL := "http://127.0.0.1:" + published[separator+1:]
	ready := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		request, _ := http.NewRequestWithContext(ctx, http.MethodGet, restartedBaseURL+"/readyz", nil)
		response, err := readinessClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode >= 200 && response.StatusCode < 300 {
				ready = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ready {
		t.Fatal("control plane did not become ready after restart")
	}
	client, err := sdk.NewClientWithResponses(restartedBaseURL, sdk.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := client.GetDiscoveryCandidateWithResponse(ctx, candidate.Id, admin)
	if err != nil || observed.JSON200 == nil || observed.JSON200.PromotedMonitorId == nil || *observed.JSON200.PromotedMonitorId != promotedID {
		t.Fatalf("promoted discovery link after control-plane restart: status=%v err=%v", responseStatus(observed), err)
	}
	if got := waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.status.externalID}", func(value string) bool { return value != "" }); got != monitorID {
		t.Fatalf("Monitor identity changed after control-plane restart: got=%s want=%s", got, monitorID)
	}
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, `{.status.conditions[?(@.type=="DiscoveryFresh")].status}`, func(value string) bool { return value == "True" })
}

func assertRecreatedMonitorOwnershipRefusal(t *testing.T, ctx context.Context, environment kindEnvironment, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn, manifest []byte) {
	t.Helper()
	liveBaseURL := controlPlaneHostURL(t, ctx, environment)
	var err error
	client, err = sdk.NewClientWithResponses(liveBaseURL, sdk.WithHTTPClient(&http.Client{Timeout: 10 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	oldID := waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.status.externalID}", func(value string) bool { return value != "" })
	oldUID := waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.metadata.uid}", func(value string) bool { return value != "" })
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "annotate", "monitor", "edge-health", "monitoring.xisnove.io/force-delete=true", "--overwrite")
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "delete", "monitor", "edge-health", "--wait=true", "--timeout=120s")
	runCommand(t, ctx, manifest, environment.kubectl, "apply", "-f", "-")
	newUID := waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.metadata.uid}", func(value string) bool { return value != "" && value != oldUID })
	if newUID == oldUID {
		t.Fatal("recreated Monitor retained the deleted Kubernetes UID")
	}
	waitForJSONPath(t, ctx, environment, "monitor/edge-health", `{.status.conditions[?(@.type=="Degraded")].status}`, func(value string) bool { return value == "True" })
	if external := waitForJSONPath(t, ctx, environment, "monitor/edge-health", "{.status.externalID}", func(string) bool { return true }); external != "" {
		t.Fatalf("recreated UID inherited remote identity %s", external)
	}
	parsed, err := uuid.Parse(oldID)
	if err != nil {
		t.Fatal("parse old Monitor identity")
	}
	remote, err := client.GetMonitorWithResponse(ctx, parsed, admin)
	if err != nil || remote.JSON200 == nil || remote.JSON200.Id != parsed {
		t.Fatalf("orphaned remote Monitor changed after recreated UID refusal: status=%v err=%v", responseStatus(remote), err)
	}
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "annotate", "monitor", "edge-health", "monitoring.xisnove.io/force-delete=true", "--overwrite")
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "delete", "monitor", "edge-health", "--wait=true", "--timeout=120s")
}

func controlPlaneHostURL(t *testing.T, ctx context.Context, environment kindEnvironment) string {
	t.Helper()
	published := runCommand(t, ctx, nil, environment.docker, "port", environment.server, "8080/tcp")
	separator := strings.LastIndex(published, ":")
	if separator < 0 || separator == len(published)-1 {
		t.Fatalf("unexpected control-plane port mapping %q", published)
	}
	return "http://127.0.0.1:" + published[separator+1:]
}

type testCredentialBundle struct {
	Credential string `json:"credential"`
	Generation int64  `json:"generation"`
}

func assertInterruptedCredentialRotation(t *testing.T, ctx context.Context, environment kindEnvironment, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn, agentName string) {
	t.Helper()
	secretName := agentName + "-credential"
	old := readCredentialBundle(t, ctx, environment, secretName, "credential")
	if old.Generation < 1 || old.Credential == "" {
		t.Fatal("initial credential bundle was invalid")
	}
	desiredGeneration := old.Generation + 1
	operatorDeployment := kindRelease + "-xisnove-edge-operator"
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "scale", "deployment/"+operatorDeployment, "--replicas=0")
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+operatorDeployment, "--timeout=120s")
	policyName := "xisnove-kind-hold-next"
	policy := fmt.Sprintf(`apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicy
metadata:
  name: %s
spec:
  failurePolicy: Fail
  matchConstraints:
    resourceRules:
      - apiGroups: [""]
        apiVersions: ["v1"]
        operations: ["UPDATE"]
        resources: ["secrets"]
  validations:
    - expression: "object.metadata.namespace != '%s' || object.metadata.name != '%s' || !('credential.next' in oldObject.data) || ('credential.next' in object.data)"
      message: hold staged Xisnove credential for restart recovery test
---
apiVersion: admissionregistration.k8s.io/v1
kind: ValidatingAdmissionPolicyBinding
metadata:
  name: %s
spec:
  policyName: %s
  validationActions: [Deny]
`, policyName, environment.namespace, secretName, policyName, policyName)
	runCommand(t, ctx, []byte(policy), environment.kubectl, "apply", "-f", "-")
	t.Cleanup(func() {
		bestEffortCommand(environment.kubectl, "delete", "validatingadmissionpolicybinding", policyName, "--ignore-not-found=true")
		bestEffortCommand(environment.kubectl, "delete", "validatingadmissionpolicy", policyName, "--ignore-not-found=true")
		bestEffortCommand(environment.kubectl, "-n", environment.namespace, "scale", "deployment/"+operatorDeployment, "--replicas=1")
	})
	waitForJSONPath(t, ctx, environment, "validatingadmissionpolicy/"+policyName, "{.status.observedGeneration}", func(value string) bool { return value == "1" })
	stageCredentialSecret(t, ctx, environment, secretName, desiredGeneration)
	assertSecretPromotionDenied(t, ctx, environment, secretName)
	rotationPatch := fmt.Sprintf(`{"spec":{"credentialRotation":{"requestedGeneration":%d}}}`, desiredGeneration)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "patch", "agent", agentName, "--type=merge", "-p", rotationPatch)
	waitForSecretKey(t, ctx, environment, secretName, "credential.next", true)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "scale", "deployment/"+operatorDeployment, "--replicas=1")
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+operatorDeployment, "--timeout=120s")
	waitForRemoteAgentGeneration(t, ctx, client, admin, desiredGeneration)
	overlap, err := client.HeartbeatAgentWithResponse(ctx, sdk.AgentHeartbeat{CredentialGeneration: old.Generation, Version: "kind-e2e-overlap", Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp}}, bearer(old.Credential))
	if err != nil || overlap.StatusCode() != 204 {
		t.Fatalf("overlap credential heartbeat: status=%v err=%v", responseStatus(overlap), err)
	}

	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "scale", "deployment/"+operatorDeployment, "--replicas=0")
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+operatorDeployment, "--timeout=120s")
	runCommand(t, ctx, nil, environment.kubectl, "delete", "validatingadmissionpolicybinding", policyName)
	runCommand(t, ctx, nil, environment.kubectl, "delete", "validatingadmissionpolicy", policyName)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "scale", "deployment/"+operatorDeployment, "--replicas=1")
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+operatorDeployment, "--timeout=120s")
	waitForSecretKey(t, ctx, environment, secretName, "credential.previous", true)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "restart", "deployment/"+agentName)
	runCommand(t, ctx, nil, environment.kubectl, "-n", environment.namespace, "rollout", "status", "deployment/"+agentName, "--timeout=120s")
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, "{.status.credentialGeneration}", func(value string) bool { return value == fmt.Sprint(desiredGeneration) })
	waitForJSONPath(t, ctx, environment, "agent/"+agentName, "{.status.rotationPhase}", func(value string) bool { return value == "Complete" })
	waitForSecretKey(t, ctx, environment, secretName, "credential.previous", false)

	unauthorized, err := client.HeartbeatAgentWithResponse(ctx, sdk.AgentHeartbeat{CredentialGeneration: old.Generation, Version: "kind-e2e-retired", Capabilities: []sdk.AgentCapability{sdk.AgentCapabilityHttp}}, bearer(old.Credential))
	if err != nil || unauthorized.StatusCode() != 401 {
		t.Fatalf("retired credential heartbeat: status=%v err=%v", responseStatus(unauthorized), err)
	}
}

func stageCredentialSecret(t *testing.T, ctx context.Context, environment kindEnvironment, secretName string, generation int64) {
	t.Helper()
	command := exec.CommandContext(ctx, environment.kubectl, "-n", environment.namespace, "get", "secret", secretName, "-o", "json")
	document, err := command.Output()
	if err != nil {
		t.Fatal("read credential Secret for staged restart state")
	}
	var secret map[string]any
	if err := json.Unmarshal(document, &secret); err != nil {
		t.Fatal("decode credential Secret for staged restart state")
	}
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		t.Fatal("generate staged credential")
	}
	bundle, err := json.Marshal(testCredentialBundle{Credential: base64.RawURLEncoding.EncodeToString(material), Generation: generation})
	if err != nil {
		t.Fatal("encode staged credential bundle")
	}
	data, ok := secret["data"].(map[string]any)
	if !ok {
		t.Fatal("credential Secret data was absent")
	}
	data["credential.next"] = base64.StdEncoding.EncodeToString(bundle)
	replacement, err := json.Marshal(secret)
	if err != nil {
		t.Fatal("encode staged credential Secret")
	}
	runCommand(t, ctx, replacement, environment.kubectl, "replace", "-f", "-")
}

func assertSecretPromotionDenied(t *testing.T, ctx context.Context, environment kindEnvironment, secretName string) {
	t.Helper()
	patch := `[{"op":"remove","path":"/data/credential.next"}]`
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.CommandContext(ctx, environment.kubectl, "-n", environment.namespace, "patch", "secret", secretName, "--type=json", "-p", patch, "--dry-run=server")
		output, err := command.CombinedOutput()
		if err != nil && strings.Contains(string(output), "hold staged Xisnove credential") {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("credential promotion admission barrier was not active")
}

func readCredentialBundle(t *testing.T, ctx context.Context, environment kindEnvironment, secretName, key string) testCredentialBundle {
	t.Helper()
	command := exec.CommandContext(ctx, environment.kubectl, "-n", environment.namespace, "get", "secret", secretName, "-o", "json")
	output, err := command.Output()
	if err != nil {
		t.Fatal("read credential Secret metadata and in-memory bundle")
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &secret); err != nil {
		t.Fatal("decode credential Secret document")
	}
	encoded := secret.Data[key]
	material, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal("decode credential Secret key")
	}
	var bundle testCredentialBundle
	if err := json.Unmarshal(material, &bundle); err != nil {
		t.Fatal("decode credential bundle")
	}
	return bundle
}

func waitForSecretKey(t *testing.T, ctx context.Context, environment kindEnvironment, secretName, key string, present bool) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.CommandContext(ctx, environment.kubectl, "-n", environment.namespace, "get", "secret", secretName, "-o", "json")
		output, err := command.Output()
		var secret struct {
			Data map[string]string `json:"data"`
		}
		if err == nil && json.Unmarshal(output, &secret) == nil {
			_, found := secret.Data[key]
			if found == present {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for credential Secret key %q present=%t", key, present)
}

func waitForRemoteAgentGeneration(t *testing.T, ctx context.Context, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn, generation int64) sdk.Agent {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.ListAgentsWithResponse(ctx, nil, admin)
		if err == nil && response.JSON200 != nil {
			for _, agent := range response.JSON200.Items {
				if agent.CredentialGeneration == generation {
					return agent
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for remote Agent credential generation %d", generation)
	return sdk.Agent{}
}

func bestEffortCommand(name string, arguments ...string) {
	_ = exec.Command(name, arguments...).Run()
}

func kubectlCanI(t *testing.T, ctx context.Context, kubectl, verb, resource, identity, namespace string) string {
	t.Helper()
	command := exec.CommandContext(ctx, kubectl, "auth", "can-i", verb, resource, "--as", identity, "--namespace", namespace)
	output, err := command.CombinedOutput()
	answer := strings.TrimSpace(string(output))
	if err != nil && answer != "no" {
		t.Fatalf("kubectl auth can-i failed: %v\n%s", err, output)
	}
	return answer
}

func ensureKindLocation(t *testing.T, ctx context.Context, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn) sdk.Location {
	t.Helper()
	locationKey := sdk.IdempotencyKey("kind-edge-location")
	created, err := client.CreateLocationWithResponse(ctx, &sdk.CreateLocationParams{IdempotencyKey: &locationKey}, sdk.CreateLocationRequest{Name: "kind-edge"}, admin)
	if err == nil && created.JSON201 != nil {
		return *created.JSON201
	}
	if err != nil || responseStatus(created) != 409 {
		t.Fatalf("create location: status=%v err=%v", responseStatus(created), err)
	}
	listed, err := client.ListLocationsWithResponse(ctx, nil, admin)
	if err != nil || listed.JSON200 == nil {
		t.Fatalf("recover existing location: status=%v err=%v", responseStatus(listed), err)
	}
	for _, location := range listed.JSON200.Items {
		if location.Name == "kind-edge" {
			return location
		}
	}
	t.Fatal("location creation conflicted but kind-edge was not listed")
	return sdk.Location{}
}

func kubernetesResourceExists(ctx context.Context, kubectl, namespace, kind, name string) bool {
	command := exec.CommandContext(ctx, kubectl, "-n", namespace, "get", kind, name, "-o", "name")
	return command.Run() == nil
}

func listDiscoveryCandidates(t *testing.T, ctx context.Context, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn) []sdk.DiscoveryCandidate {
	t.Helper()
	response, err := client.ListDiscoveryCandidatesWithResponse(ctx, nil, admin)
	if err != nil || response.JSON200 == nil {
		t.Fatalf("list discovery candidates: status=%v err=%v", responseStatus(response), err)
	}
	return response.JSON200.Items
}

func waitForDiscoveryCandidate(t *testing.T, ctx context.Context, client *sdk.ClientWithResponses, admin sdk.RequestEditorFn, match func(sdk.DiscoveryCandidate) bool) sdk.DiscoveryCandidate {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		for _, candidate := range listDiscoveryCandidates(t, ctx, client, admin) {
			if match(candidate) {
				return candidate
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatal("timed out waiting for discovery candidate")
	return sdk.DiscoveryCandidate{}
}

func assertNoOperationalKubernetesState(t *testing.T, ctx context.Context, environment kindEnvironment) {
	t.Helper()
	crds := runCommand(t, ctx, nil, environment.kubectl, "get", "crd", "-o", "name")
	for _, line := range strings.Fields(crds) {
		if !strings.HasSuffix(line, ".monitoring.xisnove.io") {
			continue
		}
		if line != "customresourcedefinition.apiextensions.k8s.io/agents.monitoring.xisnove.io" && line != "customresourcedefinition.apiextensions.k8s.io/monitors.monitoring.xisnove.io" {
			t.Fatalf("forbidden operational CRD was installed: %s", line)
		}
	}
	if jobs := runCommand(t, ctx, nil, environment.kubectl, "get", "jobs", "--all-namespaces", "-o", "name"); jobs != "" {
		t.Fatalf("operator created Jobs: %s", jobs)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate kind integration source")
	}
	return filepath.Dir(filepath.Dir(filename))
}

func loadKindEnvironment(t *testing.T) kindEnvironment {
	t.Helper()
	return kindEnvironment{
		baseURL: requiredEnvironment(t, "XISNOVE_KIND_E2E_BASE_URL"), clusterURL: requiredEnvironment(t, "XISNOVE_KIND_E2E_CLUSTER_URL"),
		namespace: requiredEnvironment(t, "XISNOVE_KIND_E2E_NAMESPACE"), operatorImage: requiredEnvironment(t, "XISNOVE_KIND_E2E_OPERATOR_IMAGE"),
		agentImage: requiredEnvironment(t, "XISNOVE_KIND_E2E_AGENT_IMAGE"), helm: requiredEnvironment(t, "XISNOVE_KIND_E2E_HELM_BIN"),
		kubectl: requiredEnvironment(t, "XISNOVE_KIND_E2E_KUBECTL_BIN"), docker: requiredEnvironment(t, "XISNOVE_KIND_E2E_DOCKER_BIN"),
		server: requiredEnvironment(t, "XISNOVE_KIND_E2E_SERVER_CONTAINER"),
	}
}

func requiredEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s is required", name)
	}
	return value
}

func splitImage(t *testing.T, image string) (string, string) {
	t.Helper()
	index := strings.LastIndex(image, ":")
	if index <= strings.LastIndex(image, "/") || index == len(image)-1 {
		t.Fatalf("image %q must include a tag", image)
	}
	return image[:index], image[index+1:]
}

func runCommand(t *testing.T, ctx context.Context, stdin []byte, name string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, name, arguments...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", name, err, output)
	}
	return strings.TrimSpace(string(output))
}

func waitForJSONPath(t *testing.T, ctx context.Context, environment kindEnvironment, resource, path string, ready func(string) bool) string {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.CommandContext(ctx, environment.kubectl, "-n", environment.namespace, "get", resource, "-o", "jsonpath="+path)
		output, err := command.Output()
		if err == nil && ready(strings.TrimSpace(string(output))) {
			return strings.TrimSpace(string(output))
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	t.Fatalf("timed out waiting for %s %s", resource, path)
	return ""
}

func responseStatus(response interface{ StatusCode() int }) int {
	if response == nil {
		return 0
	}
	return response.StatusCode()
}
