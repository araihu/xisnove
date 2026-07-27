package release_test

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const releaseEpoch = "1700000000"

func TestBundleIsDeterministicAndNormalized(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "LICENSE"), "license\n", 0o644)
	mustWriteFile(t, filepath.Join(root, "deploy", "run.sh"), "#!/bin/sh\n", 0o755)
	mustWriteFile(t, filepath.Join(root, "docs", "upgrade.md"), "upgrade\n", 0o644)

	one := filepath.Join(t.TempDir(), "one.tar.gz")
	two := filepath.Join(t.TempDir(), "two.tar.gz")
	for _, output := range []string{one, two} {
		runTool(t, tool, "bundle", "--root", root, "--output", output, "--prefix", "xisnove-deployment-1.2.3", "--source-date-epoch", releaseEpoch, "--include", "LICENSE", "--include", "deploy", "--include", "docs")
	}
	if first, second := mustReadFile(t, one), mustReadFile(t, two); !reflect.DeepEqual(first, second) {
		t.Fatal("same inputs produced different bundle bytes")
	}

	headers := readTarHeaders(t, one)
	wantNames := []string{
		"xisnove-deployment-1.2.3/LICENSE",
		"xisnove-deployment-1.2.3/deploy/run.sh",
		"xisnove-deployment-1.2.3/docs/upgrade.md",
	}
	if got := headerNames(headers); !reflect.DeepEqual(got, wantNames) {
		t.Fatalf("bundle names = %v, want %v", got, wantNames)
	}
	wantTime := time.Unix(1700000000, 0).UTC()
	for _, header := range headers {
		if header.Uid != 0 || header.Gid != 0 || header.Uname != "" || header.Gname != "" {
			t.Errorf("%s has non-normal ownership", header.Name)
		}
		if !header.ModTime.Equal(wantTime) {
			t.Errorf("%s mtime = %s", header.Name, header.ModTime)
		}
		if header.Name == "xisnove-deployment-1.2.3/deploy/run.sh" && header.Mode != 0o755 {
			t.Errorf("executable mode = %#o", header.Mode)
		}
	}
}

func TestManifestIsCanonicalOrderedAndCleanConsumerVerifiable(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	candidate := t.TempDir()
	mustWriteFile(t, filepath.Join(candidate, "archives", "server.tar.gz"), "server", 0o644)
	mustWriteFile(t, filepath.Join(candidate, "sboms", "server.spdx.json"), "{}\n", 0o644)
	mustWriteFile(t, filepath.Join(candidate, "metadata", "licenses.json"), "{}\n", 0o644)
	mustWriteFile(t, filepath.Join(candidate, "metadata", "toolchain.lock.json"), "{}\n", 0o644)
	mustWriteFile(t, filepath.Join(candidate, "oci", "index.json"), "index", 0o644)
	plan := filepath.Join(candidate, "subjects.json")
	mustWriteFile(t, plan, `[
  {"kind":"sbom","name":"server-sbom","locator":"sboms/server.spdx.json","path":"sboms/server.spdx.json","mediaType":"application/spdx+json"},
  {"kind":"metadata","name":"licenses","locator":"metadata/licenses.json","path":"metadata/licenses.json","mediaType":"application/json"},
  {"kind":"metadata","name":"toolchain-lock","locator":"metadata/toolchain.lock.json","path":"metadata/toolchain.lock.json","mediaType":"application/json"},
  {"kind":"oci-index","name":"server","locator":"oci/index.json","path":"oci/index.json","mediaType":"application/vnd.oci.image.index.v1+json"},
  {"kind":"archive","name":"server","locator":"archives/server.tar.gz","path":"archives/server.tar.gz"}
]`, 0o644)
	manifest := filepath.Join(candidate, "candidate-manifest.json")
	checksum := filepath.Join(candidate, "candidate-manifest.json.sha256")
	runTool(t, tool, "manifest", "--root", candidate, "--repository", "github.com/araihu/xisnove", "--commit", strings.Repeat("a", 40), "--version", "1.2.3", "--source-date-epoch", releaseEpoch, "--subjects", plan, "--output", manifest, "--checksum", checksum)

	var got struct {
		Subjects []struct {
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			Locator  string `json:"locator"`
			SHA256   string `json:"sha256"`
			Size     int64  `json:"size"`
			Platform string `json:"platform,omitempty"`
		} `json:"subjects"`
	}
	if err := json.Unmarshal(mustReadFile(t, manifest), &got); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, subject := range got.Subjects {
		order = append(order, subject.Kind+"/"+subject.Name+"/"+subject.Platform)
		if strings.Contains(subject.Locator, "candidate-manifest") {
			t.Fatalf("manifest recursively names itself: %q", subject.Locator)
		}
	}
	wantOrder := []string{"archive/server/", "metadata/licenses/", "metadata/toolchain-lock/", "oci-index/server/", "sbom/server-sbom/"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("subject order = %v, want %v", order, wantOrder)
	}

	consumer := t.TempDir()
	copyTree(t, candidate, consumer)
	verifier := filepath.Join(consumer, "verify-bundle.sh")
	copyFile(t, filepath.Join(repositoryRoot(t), "scripts", "release", "verify-bundle.sh"), verifier, 0o755)
	runCommandIn(t, consumer, []string{"RELEASEBUNDLE_BIN=" + tool}, verifier, "--root", ".", "--manifest", "candidate-manifest.json", "--checksum", "candidate-manifest.json.sha256")
	canonical := mustReadFile(t, filepath.Join(consumer, "candidate-manifest.json"))
	noncanonical := append(append([]byte(nil), canonical...), '\n')
	mustWriteFile(t, filepath.Join(consumer, "candidate-manifest.json"), string(noncanonical), 0o644)
	digest := sha256.Sum256(noncanonical)
	mustWriteFile(t, filepath.Join(consumer, "candidate-manifest.json.sha256"), hex.EncodeToString(digest[:])+"  candidate-manifest.json\n", 0o644)
	cmd := exec.Command(verifier, "--root", ".", "--manifest", "candidate-manifest.json", "--checksum", "candidate-manifest.json.sha256")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "RELEASEBUNDLE_BIN="+tool)
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "not canonical JSON") {
		t.Fatalf("clean consumer verifier accepted noncanonical JSON: err=%v output=%s", err, output)
	}
	mustWriteFile(t, filepath.Join(consumer, "candidate-manifest.json"), string(canonical), 0o644)
	digest = sha256.Sum256(canonical)
	mustWriteFile(t, filepath.Join(consumer, "candidate-manifest.json.sha256"), hex.EncodeToString(digest[:])+"  candidate-manifest.json\n", 0o644)
	mustWriteFile(t, filepath.Join(consumer, "archives", "server.tar.gz"), "tampered", 0o644)
	cmd = exec.Command(verifier, "--root", ".", "--manifest", "candidate-manifest.json", "--checksum", "candidate-manifest.json.sha256")
	cmd.Dir = consumer
	cmd.Env = append(os.Environ(), "RELEASEBUNDLE_BIN="+tool)
	if output, err := cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "subject mismatch") {
		t.Fatalf("clean consumer verifier accepted tampering: err=%v output=%s", err, output)
	}
	firstManifest := mustReadFile(t, manifest)
	runTool(t, tool, "manifest", "--root", candidate, "--repository", "github.com/araihu/xisnove", "--commit", strings.Repeat("a", 40), "--version", "1.2.3", "--source-date-epoch", releaseEpoch, "--subjects", plan, "--output", manifest, "--checksum", checksum)
	if !reflect.DeepEqual(firstManifest, mustReadFile(t, manifest)) {
		t.Fatal("same subject plan produced different manifest bytes")
	}
}

func TestDeploymentAssemblyIncludesContractAndLegalClosure(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	for _, path := range []string{
		"LICENSE",
		"NOTICE",
		"charts/xisnove/Chart.yaml",
		"charts/xisnove-edge/Chart.yaml",
		"charts/xisnove-edge/LICENSE",
		"charts/xisnove-edge/NOTICE",
		"deploy/compose/compose.yaml",
		"deploy/raw/run-server.sh",
		"deploy/systemd/xisnove-server.service",
		"config/crd/bases/monitor.yaml",
		"docs/operations/upgrade.md",
	} {
		mustWriteFile(t, filepath.Join(root, path), path+"\n", 0o644)
	}
	mustWriteFile(t, filepath.Join(root, ".worktrees", "secret"), "excluded", 0o644)
	mustWriteFile(t, filepath.Join(root, ".env"), "TURSO_TOKEN=must-not-ship\n", 0o600)
	runCommandIn(t, root, nil, "git", "init", "--quiet")
	runCommandIn(t, root, nil, "git", "add", "LICENSE", "NOTICE", "charts", "deploy", "config", "docs")
	first := t.TempDir()
	second := t.TempDir()
	script := filepath.Join(repositoryRoot(t), "scripts", "release", "assemble-bundle.sh")
	for _, output := range []string{first, second} {
		runCommandIn(t, repositoryRoot(t), []string{"RELEASEBUNDLE_BIN=" + tool}, script, "--root", root, "--output-dir", output, "--version", "1.2.3", "--source-date-epoch", releaseEpoch)
	}
	for _, name := range []string{"xisnove-source_1.2.3.tar.gz", "xisnove-deployment_1.2.3.tar.gz"} {
		if !reflect.DeepEqual(mustReadFile(t, filepath.Join(first, name)), mustReadFile(t, filepath.Join(second, name))) {
			t.Fatalf("%s is not reproducible", name)
		}
	}
	deploymentNames := headerNames(readTarHeaders(t, filepath.Join(first, "xisnove-deployment_1.2.3.tar.gz")))
	for _, required := range []string{
		"xisnove-deployment-1.2.3/LICENSE",
		"xisnove-deployment-1.2.3/NOTICE",
		"xisnove-deployment-1.2.3/charts/xisnove/Chart.yaml",
		"xisnove-deployment-1.2.3/charts/xisnove-edge/LICENSE",
		"xisnove-deployment-1.2.3/deploy/compose/compose.yaml",
		"xisnove-deployment-1.2.3/deploy/raw/run-server.sh",
		"xisnove-deployment-1.2.3/deploy/systemd/xisnove-server.service",
		"xisnove-deployment-1.2.3/config/crd/bases/monitor.yaml",
		"xisnove-deployment-1.2.3/docs/operations/upgrade.md",
	} {
		if !contains(deploymentNames, required) {
			t.Errorf("deployment bundle missing %s", required)
		}
	}
	sourceNames := strings.Join(headerNames(readTarHeaders(t, filepath.Join(first, "xisnove-source_1.2.3.tar.gz"))), "\n")
	if strings.Contains(sourceNames, ".git/") || strings.Contains(sourceNames, ".worktrees/") || strings.Contains(sourceNames, ".env") || strings.Contains(sourceNames, "TURSO_TOKEN") {
		t.Fatalf("source bundle contains excluded state:\n%s", sourceNames)
	}

	repository := repositoryRoot(t)
	if !reflect.DeepEqual(mustReadFile(t, filepath.Join(repository, "LICENSE")), mustReadFile(t, filepath.Join(repository, "charts", "xisnove-edge", "LICENSE"))) {
		t.Fatal("edge chart LICENSE differs from root LICENSE")
	}
	if !reflect.DeepEqual(mustReadFile(t, filepath.Join(repository, "NOTICE")), mustReadFile(t, filepath.Join(repository, "charts", "xisnove-edge", "NOTICE"))) {
		t.Fatal("edge chart NOTICE differs from root NOTICE")
	}
}

func TestSBOMNormalizationAndLicensePolicyFailClosed(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	raw := filepath.Join(root, "raw.spdx.json")
	normalized := filepath.Join(root, "normalized.spdx.json")
	mustWriteFile(t, raw, `{"spdxVersion":"SPDX-2.3","documentNamespace":"urn:uuid:random","creationInfo":{"created":"2020-01-01T00:00:00Z","creators":["Tool: syft"]},"packages":[{"name":"allowed","versionInfo":"1.0.0","licenseDeclared":"MIT","licenseConcluded":"MIT"},{"name":"mystery","versionInfo":"2.0.0","licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"}]}`, 0o644)
	runTool(t, tool, "normalize-sbom", "--input", raw, "--output", normalized, "--subject-sha256", strings.Repeat("b", 64), "--source-date-epoch", releaseEpoch)
	contents := string(mustReadFile(t, normalized))
	if !strings.Contains(contents, `"created":"2023-11-14T22:13:20Z"`) || !strings.Contains(contents, `"documentNamespace":"https://xisnove.dev/sbom/`+strings.Repeat("b", 64)+`"`) {
		t.Fatalf("SBOM identity not normalized: %s", contents)
	}

	policy := filepath.Join(root, "policy.json")
	mustWriteFile(t, policy, `{"schemaVersion":1,"allow":["MIT"],"deny":["AGPL-3.0-only"]}`, 0o644)
	inventory := filepath.Join(root, "licenses.json")
	cmd := exec.Command(tool, "licenses", "--sbom", normalized, "--policy", policy, "--output", inventory)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "unknown license") {
		t.Fatalf("unknown license must fail closed: err=%v output=%s", err, output)
	}

	mustWriteFile(t, raw, `{"spdxVersion":"SPDX-2.3","packages":[{"name":"denied","versionInfo":"1.0.0","licenseDeclared":"AGPL-3.0-only","licenseConcluded":"AGPL-3.0-only"}]}`, 0o644)
	runTool(t, tool, "normalize-sbom", "--input", raw, "--output", normalized, "--subject-sha256", strings.Repeat("c", 64), "--source-date-epoch", releaseEpoch)
	cmd = exec.Command(tool, "licenses", "--sbom", normalized, "--policy", policy, "--output", inventory)
	output, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "denied license") {
		t.Fatalf("denied license must fail closed: err=%v output=%s", err, output)
	}
}

func TestSBOMAndLicenseScriptsUseExplicitToolsAndInputs(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	artifact := filepath.Join(root, "server.tar.gz")
	mustWriteFile(t, artifact, "artifact", 0o644)
	fakeSyft := filepath.Join(root, "fake-syft")
	mustWriteFile(t, fakeSyft, `#!/bin/sh
set -eu
output=
for argument in "$@"; do
  case "$argument" in
    spdx-json=*) output=${argument#spdx-json=} ;;
  esac
done
[ -n "$output" ]
printf '%s\n' '{"spdxVersion":"SPDX-2.3","documentNamespace":"urn:uuid:random","creationInfo":{"created":"2020-01-01T00:00:00Z","creators":["Tool: fake-syft"]},"packages":[{"name":"dependency","versionInfo":"1.0.0","licenseDeclared":"MIT","licenseConcluded":"MIT"}]}' > "$output"
`, 0o755)
	sboms := filepath.Join(root, "sboms")
	generate := filepath.Join(repositoryRoot(t), "scripts", "release", "generate-sboms.sh")
	runCommandIn(t, root, []string{"RELEASEBUNDLE_BIN=" + tool, "SYFT_BIN=" + fakeSyft}, generate, "--output-dir", sboms, "--source-date-epoch", releaseEpoch, artifact)
	generated := filepath.Join(sboms, "server.tar.gz.spdx.json")
	if _, err := os.Stat(generated); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "policy.json")
	mustWriteFile(t, policy, `{"schemaVersion":1,"allow":["MIT"],"deny":["AGPL-3.0-only"]}`, 0o644)
	inventory := filepath.Join(root, "licenses.json")
	inventoryScript := filepath.Join(repositoryRoot(t), "scripts", "release", "inventory-licenses.sh")
	runCommandIn(t, root, []string{"RELEASEBUNDLE_BIN=" + tool}, inventoryScript, "--sbom-dir", sboms, "--policy", policy, "--output", inventory)
	if contents := string(mustReadFile(t, inventory)); !strings.Contains(contents, `"status":"allowed"`) {
		t.Fatalf("license inventory did not allow MIT: %s", contents)
	}
}

func buildReleaseBundleTool(t *testing.T) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "releasebundle")
	cmd := exec.Command("go", "build", "-trimpath", "-o", tool, "./scripts/release/cmd/releasebundle")
	cmd.Dir = repositoryRoot(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build releasebundle: %v\n%s", err, output)
	}
	return tool
}

func runTool(t *testing.T, tool string, args ...string) {
	t.Helper()
	runToolIn(t, repositoryRoot(t), tool, args...)
}

func runToolIn(t *testing.T, dir, tool string, args ...string) {
	t.Helper()
	cmd := exec.Command(tool, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", tool, strings.Join(args, " "), err, output)
	}
}

func runCommandIn(t *testing.T, dir string, environment []string, command string, args ...string) {
	t.Helper()
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), environment...)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", command, strings.Join(args, " "), err, output)
	}
}

func mustWriteFile(t *testing.T, path, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func copyFile(t *testing.T, source, destination string, mode os.FileMode) {
	t.Helper()
	mustWriteFile(t, destination, string(mustReadFile(t, source)), mode)
}

func readTarHeaders(t *testing.T, path string) []*tar.Header {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	var headers []*tar.Header
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return headers
		}
		if err != nil {
			t.Fatal(err)
		}
		copy := *header
		headers = append(headers, &copy)
	}
}

func headerNames(headers []*tar.Header) []string {
	names := make([]string, 0, len(headers))
	for _, header := range headers {
		names = append(names, header.Name)
	}
	return names
}

func copyTree(t *testing.T, source, destination string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, contents, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}
