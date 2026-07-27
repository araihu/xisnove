package release_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomelabAcceptancePromotesExactCandidateImagesWithoutRebuild(t *testing.T) {
	root := t.TempDir()
	candidate := filepath.Join(root, "candidate")
	bin := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}

	type subject struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		Locator  string `json:"locator"`
		SHA256   string `json:"sha256"`
		Size     int    `json:"size"`
		Platform string `json:"platform,omitempty"`
	}
	var subjects []subject
	for index, name := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		indexBytes := []byte("index-" + name)
		indexDigest := sha256.Sum256(indexBytes)
		indexHex := hex.EncodeToString(indexDigest[:])
		indexLocator := "oci/images/" + name + "/layout/blobs/sha256/" + indexHex
		mustWriteFile(t, filepath.Join(candidate, filepath.FromSlash(indexLocator)), string(indexBytes), 0o644)
		subjects = append(subjects, subject{Kind: "oci-index", Name: name, Locator: indexLocator, SHA256: indexHex, Size: len(indexBytes)})

		platformBytes := []byte("platform-" + name)
		platformDigest := sha256.Sum256(platformBytes)
		platformHex := hex.EncodeToString(platformDigest[:])
		platformLocator := "oci/images/" + name + "/layout/blobs/sha256/" + platformHex
		mustWriteFile(t, filepath.Join(candidate, filepath.FromSlash(platformLocator)), string(platformBytes), 0o644)
		subjects = append(subjects, subject{Kind: "oci-platform-manifest", Name: name, Locator: platformLocator, SHA256: platformHex, Size: len(platformBytes), Platform: "linux/amd64"})

		_ = index
	}
	manifest := map[string]any{
		"schemaVersion":   1,
		"repository":      "github.com/araihu/xisnove",
		"commit":          strings.Repeat("a", 40),
		"version":         "1.2.3",
		"sourceDateEpoch": 1_700_000_000,
		"subjects":        subjects,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mustWriteFile(t, filepath.Join(candidate, "candidate-manifest.json"), string(manifestBytes)+"\n", 0o644)

	fakeDocker := `#!/bin/sh
set -eu
printf 'docker %s\n' "$*" >> "$COMMAND_LOG"
case "$1 $2" in
  "run -d") printf '%s\n' registry-container ;;
  "port registry-container") printf '%s\n' '127.0.0.1:45000' ;;
esac
`
	fakeOras := `#!/bin/sh
set -eu
printf 'oras %s\n' "$*" >> "$COMMAND_LOG"
case "$1" in
  cp)
    count=0
    test ! -f "$ORAS_COUNTER" || count=$(cat "$ORAS_COUNTER")
    count=$((count + 1))
    printf '%s\n' "$count" > "$ORAS_COUNTER"
    test "$count" -gt 1 || exit 42
    ;;
  resolve) printf '%s\n' "${ORAS_EXPECTED_DIGEST:?}" ;;
esac
`
	fakeKind := `#!/bin/sh
set -eu
printf 'kind prebuilt=%s server=%s ui=%s agent=%s operator=%s\n' \
  "${XISNOVE_KIND_E2E_PREBUILT:-}" "${XISNOVE_KIND_E2E_SERVER_IMAGE:-}" \
  "${XISNOVE_KIND_E2E_UI_IMAGE:-}" "${XISNOVE_KIND_E2E_AGENT_IMAGE:-}" \
  "${XISNOVE_KIND_E2E_OPERATOR_IMAGE:-}" >> "$COMMAND_LOG"
`
	mustWriteFile(t, filepath.Join(bin, "docker"), fakeDocker, 0o755)
	mustWriteFile(t, filepath.Join(bin, "oras"), fakeOras, 0o755)
	mustWriteFile(t, filepath.Join(bin, "kind-e2e"), fakeKind, 0o755)

	script := filepath.Join(repositoryRoot(t), "scripts", "release", "accept-candidate-homelab.sh")
	cmd := exec.Command("bash", script,
		"--candidate-root", candidate,
		"--commit", strings.Repeat("a", 40),
		"--version", "1.2.3",
	)
	cmd.Dir = repositoryRoot(t)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"COMMAND_LOG="+logPath,
		"ORAS_COUNTER="+filepath.Join(root, "oras-counter"),
		"DOCKER_BIN=docker",
		"ORAS_BIN=oras",
		"XISNOVE_KIND_E2E_SCRIPT="+filepath.Join(bin, "kind-e2e"),
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("accept candidate: %v\n%s", err, output)
	}
	commands := string(mustReadFile(t, logPath))
	if strings.Contains(commands, "docker build") {
		t.Fatalf("candidate acceptance rebuilt source images:\n%s", commands)
	}
	if !strings.Contains(commands, "docker.io/library/registry:2.8.3@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373") {
		t.Fatalf("candidate acceptance did not use the locked registry image:\n%s", commands)
	}
	for _, name := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		if !strings.Contains(commands, "oras cp --from-oci-layout") || !strings.Contains(commands, "/"+name+"@sha256:") {
			t.Errorf("candidate %s was not promoted from its OCI layout:\n%s", name, commands)
		}
	}
	if !strings.Contains(commands, "kind prebuilt=1") {
		t.Fatalf("kind smoke did not receive prebuilt candidate images:\n%s", commands)
	}
}

func TestKindEdgeSmokeUsesSuppliedPrebuiltImages(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	logPath := filepath.Join(root, "commands.log")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeDocker := `#!/bin/sh
set -eu
printf 'docker %s\n' "$*" >> "$COMMAND_LOG"
case "$1" in
  build) echo 'source rebuild forbidden' >&2; exit 91 ;;
  port) printf '%s\n' '127.0.0.1:48080' ;;
esac
`
	fakeCommand := `#!/bin/sh
set -eu
printf '%s %s\n' "$(basename "$0")" "$*" >> "$COMMAND_LOG"
`
	mustWriteFile(t, filepath.Join(bin, "docker"), fakeDocker, 0o755)
	for _, name := range []string{"kind", "kubectl", "helm", "go", "curl"} {
		mustWriteFile(t, filepath.Join(bin, name), fakeCommand, 0o755)
	}
	script := filepath.Join(repositoryRoot(t), "scripts", "kind-edge-e2e.sh")
	cmd := exec.Command("bash", script)
	cmd.Dir = repositoryRoot(t)
	cmd.Env = append(os.Environ(),
		"PATH="+bin+":"+os.Getenv("PATH"),
		"COMMAND_LOG="+logPath,
		"DOCKER_BIN=docker",
		"KIND_BIN=kind",
		"KUBECTL_BIN=kubectl",
		"HELM_BIN=helm",
		"XISNOVE_KIND_E2E_PREBUILT=1",
		"XISNOVE_KIND_E2E_SERVER_IMAGE=xisnove-server:accepted",
		"XISNOVE_KIND_E2E_UI_IMAGE=xisnove-ui:accepted",
		"XISNOVE_KIND_E2E_AGENT_IMAGE=xisnove-agent:accepted",
		"XISNOVE_KIND_E2E_OPERATOR_IMAGE=xisnove-operator:accepted",
		"XISNOVE_KIND_TEMP_PARENT="+filepath.Join(root, "tmp"),
	)
	if err := os.MkdirAll(filepath.Join(root, "tmp"), 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("prebuilt kind smoke: %v\n%s", err, output)
	}
	commands := string(mustReadFile(t, logPath))
	if strings.Contains(commands, "docker build") {
		t.Fatalf("prebuilt kind smoke rebuilt source images:\n%s", commands)
	}
	for _, image := range []string{"xisnove-server:accepted", "xisnove-agent:accepted", "xisnove-operator:accepted"} {
		if !strings.Contains(commands, image) {
			t.Errorf("prebuilt image %s was not used:\n%s", image, commands)
		}
	}
}
