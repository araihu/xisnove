package helm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestKindSQLiteUpgradeKeepsSingletonAndRWOOwnership(t *testing.T) {
	if os.Getenv("XISNOVE_HELM_KIND_E2E") != "1" {
		t.Skip("set XISNOVE_HELM_KIND_E2E=1 to run the disposable multi-node kind upgrade")
	}
	for _, tool := range []string{"docker", "kind", "kubectl", "helm", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("%s is required: %v", tool, err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cluster := "xisnove-helm-" + strconv.Itoa(os.Getpid())
	image := "xisnove-helm-fake-" + strconv.Itoa(os.Getpid())
	if os.Getenv("XISNOVE_HELM_KIND_KEEP_ON_FAILURE") != "1" {
		defer runCleanup(t, "kind", "delete", "cluster", "--name", cluster)
	} else {
		t.Logf("diagnostic cluster retained: %s", cluster)
	}
	defer runCleanup(t, "docker", "image", "rm", image+":v1", image+":v2")

	kindConfig := "kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nnodes:\n- role: control-plane\n- role: worker\n- role: worker\n"
	runInput(t, ctx, kindConfig, "kind", "create", "cluster", "--name", cluster, "--config", "-")
	buildFakeImages(t, ctx, image)
	run(t, ctx, "kind", "load", "docker-image", "--name", cluster, image+":v1", image+":v2")

	run(t, ctx, "kubectl", "create", "namespace", "monitoring")
	run(t, ctx, "kubectl", "-n", "monitoring", "create", "secret", "generic", "xisnove-server", "--from-literal=cursor-signing-key=01234567890123456789012345678901", "--from-literal=notification-master-key={}")
	run(t, ctx, "kubectl", "-n", "monitoring", "create", "secret", "generic", "xisnove-admin", "--from-literal=password=kind-test-password")
	run(t, ctx, "kubectl", "-n", "monitoring", "create", "secret", "generic", "xisnove-ui", "--from-literal=cookie-secret=MDEyMzQ1Njc4OTAxMjM0NTY3ODkwMTIzNDU2Nzg5MDE=")
	run(t, ctx, "kubectl", "-n", "monitoring", "create", "secret", "generic", "xisnove-agent-enrollment", "--from-literal=token=one-time-kind-token")

	values := filepath.Join(t.TempDir(), "values.yaml")
	contents := fmt.Sprintf("database:\n  profile: sqlite\nserver:\n  replicas: 1\n  image: {repository: %s, tag: v1, pullPolicy: Never}\nui:\n  replicas: 1\n  image: {repository: %s, tag: v1, pullPolicy: Never}\nagent:\n  enabled: true\n  image: {repository: %s, tag: v1, pullPolicy: Never}\n  enrollment:\n    enabled: true\n    existingSecret: {name: xisnove-agent-enrollment}\nsecrets:\n  server:\n    existingSecret: {name: xisnove-server}\n  admin:\n    email: admin@example.test\n    existingSecret: {name: xisnove-admin}\n  ui:\n    existingSecret: {name: xisnove-ui}\n", image, image, image)
	if err := os.WriteFile(values, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, ctx, "helm", "upgrade", "--install", "xisnove", chart(t), "--namespace", "monitoring", "--values", values, "--wait", "--timeout", "2m")

	before := readServerPod(t, ctx)
	if !before.Ready || before.UID == "" {
		t.Fatalf("initial server pod = %#v", before)
	}
	assertInitCompleted(t, ctx)
	assertAgentEnrollmentReady(t, ctx)

	command := exec.CommandContext(ctx, "helm", "upgrade", "xisnove", chart(t), "--namespace", "monitoring", "--values", values, "--set", "server.image.tag=v2", "--set", "ui.image.tag=v2", "--set", "agent.image.tag=v2", "--wait", "--timeout", "2m")
	var upgradeOutput bytes.Buffer
	command.Stdout, command.Stderr = &upgradeOutput, &upgradeOutput
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	seenOld, seenNew := false, false
	var upgradeErr error
	monitoring := true
	for monitoring {
		select {
		case upgradeErr = <-done:
			monitoring = false
		case <-time.After(100 * time.Millisecond):
			pod := readServerPodOptional(t, ctx)
			if pod.UID == before.UID {
				seenOld = true
			}
			if pod.UID != "" && pod.UID != before.UID {
				seenNew = true
			}
			if pod.ActiveContainers > 1 {
				t.Fatalf("concurrent server containers observed: %#v", pod)
			}
		}
	}
	if upgradeErr != nil {
		t.Fatalf("helm upgrade failed: %v\n%s", upgradeErr, upgradeOutput.String())
	}
	after := readServerPod(t, ctx)
	if after.UID != "" && after.UID != before.UID {
		seenNew = true
	}
	if after.UID == before.UID || !after.Ready || !strings.HasSuffix(after.Image, ":v2") {
		t.Fatalf("upgraded server pod = %#v, before = %#v", after, before)
	}
	if !seenOld || !seenNew {
		t.Fatalf("upgrade observations old=%t new=%t", seenOld, seenNew)
	}
	assertInitCompleted(t, ctx)
	assertAgentEnrollmentReady(t, ctx)
	if output := runOutput(t, ctx, "kubectl", "-n", "monitoring", "get", "events", "-o", "json"); strings.Contains(output, "Multi-Attach error") {
		t.Fatal("RWO volume emitted Multi-Attach error")
	}
	if output := runOutput(t, ctx, "kubectl", "-n", "monitoring", "get", "pvc", "data-xisnove-xisnove-server-0", "-o", "jsonpath={.status.phase}"); output != "Bound" {
		t.Fatalf("PVC phase = %q", output)
	}
}

func assertAgentEnrollmentReady(t *testing.T, ctx context.Context) {
	t.Helper()
	output := runOutput(t, ctx, "kubectl", "-n", "monitoring", "get", "pod", "-l", "app.kubernetes.io/component=agent", "-o", "jsonpath={range .items[0].status.initContainerStatuses[*]}{.name}:{.state.terminated.exitCode}{\"\\n\"}{end}")
	if !strings.Contains(output, "enroll:0") {
		t.Fatalf("Agent init status = %q", output)
	}
	if phase := runOutput(t, ctx, "kubectl", "-n", "monitoring", "get", "pvc", "credential-data-xisnove-xisnove-agent-0", "-o", "jsonpath={.status.phase}"); phase != "Bound" {
		t.Fatalf("Agent credential PVC phase = %q", phase)
	}
}

type podState struct {
	UID              string
	Image            string
	Ready            bool
	ActiveContainers int
}

func readServerPod(t *testing.T, ctx context.Context) podState {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if state := readServerPodOptional(t, ctx); state.UID != "" {
			return state
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatal("server pod did not appear")
	return podState{}
}

func readServerPodOptional(t *testing.T, ctx context.Context) podState {
	t.Helper()
	command := exec.CommandContext(ctx, "kubectl", "-n", "monitoring", "get", "pods", "-l", "app.kubernetes.io/component=server", "-o", "json")
	output, err := command.Output()
	if err != nil {
		return podState{}
	}
	var list struct {
		Items []struct {
			Metadata struct {
				UID string `json:"uid"`
			} `json:"metadata"`
			Spec struct {
				Containers []struct {
					Image string `json:"image"`
				} `json:"containers"`
			} `json:"spec"`
			Status struct {
				Phase             string `json:"phase"`
				ContainerStatuses []struct {
					Ready bool `json:"ready"`
					State struct {
						Running any `json:"running"`
					} `json:"state"`
				} `json:"containerStatuses"`
			} `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(output, &list); err != nil {
		t.Fatalf("decode pods: %v", err)
	}
	if len(list.Items) > 1 {
		t.Fatalf("more than one server pod exists: %d", len(list.Items))
	}
	if len(list.Items) == 0 {
		return podState{}
	}
	item := list.Items[0]
	state := podState{UID: item.Metadata.UID}
	if len(item.Spec.Containers) > 0 {
		state.Image = item.Spec.Containers[0].Image
	}
	for _, status := range item.Status.ContainerStatuses {
		if status.Ready {
			state.Ready = true
		}
		if status.State.Running != nil {
			state.ActiveContainers++
		}
	}
	return state
}

func assertInitCompleted(t *testing.T, ctx context.Context) {
	t.Helper()
	output := runOutput(t, ctx, "kubectl", "-n", "monitoring", "get", "pod", "-l", "app.kubernetes.io/component=server", "-o", "jsonpath={range .items[0].status.initContainerStatuses[*]}{.name}:{.state.terminated.exitCode}{\"\\n\"}{end}")
	for _, expected := range []string{"migrate:0", "bootstrap:0"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("init status %q missing %q", output, expected)
		}
	}
}

func buildFakeImages(t *testing.T, ctx context.Context, image string) {
	t.Helper()
	directory := t.TempDir()
	binary := filepath.Join(directory, "xisnove-fake")
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", binary, "./testdata/fake-runtime")
	command.Dir = directoryForPackage(t)
	command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake runtime: %v\n%s", err, output)
	}
	dockerfile := "FROM scratch\nCOPY xisnove-fake /xisnove-fake\nUSER 101:101\nENTRYPOINT [\"/xisnove-fake\"]\n"
	if err := os.WriteFile(filepath.Join(directory, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	run(t, ctx, "docker", "build", "--provenance=false", "-t", image+":v1", directory)
	run(t, ctx, "docker", "tag", image+":v1", image+":v2")
}

func directoryForPackage(t *testing.T) string { return directory(t) }

func run(t *testing.T, ctx context.Context, name string, args ...string) {
	t.Helper()
	if output, err := exec.CommandContext(ctx, name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}
func runInput(t *testing.T, ctx context.Context, input, name string, args ...string) {
	t.Helper()
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = strings.NewReader(input)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}
func runOutput(t *testing.T, ctx context.Context, name string, args ...string) string {
	t.Helper()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
	return strings.TrimSpace(string(output))
}
func runCleanup(t *testing.T, name string, args ...string) {
	t.Helper()
	if output, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Logf("cleanup %s %v: %v: %s", name, args, err, output)
	}
}
