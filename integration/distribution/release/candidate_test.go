package release_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestCandidatePlanPackagesChartsExtractsOCIAndCoversSubjects(t *testing.T) {
	tool := buildCandidatePlanTool(t)
	root := t.TempDir()
	chart := filepath.Join(root, "xisnove")
	mustWriteFile(t, filepath.Join(chart, "Chart.yaml"), "apiVersion: v2\nname: xisnove\nversion: 0.1.0\nappVersion: \"0.1.0\"\n", 0o644)
	mustWriteFile(t, filepath.Join(chart, "LICENSE"), "license\n", 0o644)
	mustWriteFile(t, filepath.Join(chart, "NOTICE"), "notice\n", 0o644)
	mustWriteFile(t, filepath.Join(chart, "templates", "deployment.yaml"), "kind: Deployment\n", 0o644)

	chartOne := filepath.Join(root, "charts", "xisnove-1.2.3.tgz")
	chartTwo := filepath.Join(t.TempDir(), "xisnove-1.2.3.tgz")
	for _, output := range []string{chartOne, chartTwo} {
		runTool(t, tool, "package-chart", "--chart", chart, "--output", output, "--version", "1.2.3", "--source-date-epoch", releaseEpoch)
	}
	if !reflect.DeepEqual(mustReadFile(t, chartOne), mustReadFile(t, chartTwo)) {
		t.Fatal("same chart inputs produced different package bytes")
	}
	contents := readTarFile(t, chartOne, "xisnove/Chart.yaml")
	if !strings.Contains(contents, "version: 1.2.3") || !strings.Contains(contents, "appVersion: \"1.2.3\"") {
		t.Fatalf("packaged Chart.yaml has wrong release identity:\n%s", contents)
	}

	layout := filepath.Join(root, "server.tar")
	indexBytes, amd64Bytes, arm64Bytes := writeSyntheticOCILayout(t, layout)
	runTool(t, tool, "extract-oci", "--layout", layout, "--output-dir", filepath.Join(root, "oci", "images", "xisnove-server"), "--name", "xisnove-server")
	indexDigest := sha256.Sum256(indexBytes)
	amd64Digest := sha256.Sum256(amd64Bytes)
	arm64Digest := sha256.Sum256(arm64Bytes)
	for path, want := range map[string][]byte{
		"oci/images/xisnove-server/layout/blobs/sha256/" + hex.EncodeToString(indexDigest[:]): indexBytes,
		"oci/images/xisnove-server/layout/blobs/sha256/" + hex.EncodeToString(amd64Digest[:]): amd64Bytes,
		"oci/images/xisnove-server/layout/blobs/sha256/" + hex.EncodeToString(arm64Digest[:]): arm64Bytes,
		"oci/images/xisnove-server/layout/oci-layout":                                         []byte(`{"imageLayoutVersion":"1.0.0"}`),
	} {
		if got := mustReadFile(t, filepath.Join(root, filepath.FromSlash(path))); !reflect.DeepEqual(got, want) {
			t.Fatalf("%s differs from digest-addressed OCI bytes", path)
		}
	}

	mustWriteFile(t, filepath.Join(root, "archives", "xisnove-server_1.2.3_linux_amd64.tar.gz"), "archive", 0o644)
	mustWriteFile(t, filepath.Join(root, "bundles", "xisnove-source_1.2.3.tar.gz"), "bundle", 0o644)
	mustWriteFile(t, filepath.Join(root, "sboms", "archive.spdx.json"), "{}\n", 0o644)
	chartLayout := filepath.Join(root, "oci", "charts", "xisnove", "layout")
	writeSyntheticChartLayout(t, chartLayout, chartOne)
	runTool(t, tool, "verify-chart-layout", "--layout-dir", chartLayout, "--chart", chartOne)
	plan := filepath.Join(root, "subjects.json")
	runTool(t, tool, "plan", "--root", root, "--output", plan)
	var subjects []struct {
		Kind, Name, Locator, Path, Platform, MediaType string
	}
	if err := json.Unmarshal(mustReadFile(t, plan), &subjects); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, subject := range subjects {
		keys = append(keys, subject.Kind+"/"+subject.Locator+"/"+subject.Platform)
	}
	for _, want := range []string{
		"archive/archives/xisnove-server_1.2.3_linux_amd64.tar.gz/",
		"bundle/bundles/xisnove-source_1.2.3.tar.gz/",
		"chart/charts/xisnove-1.2.3.tgz/",
		"oci-manifest/oci/charts/xisnove/layout/blobs/sha256/",
		"sbom/sboms/archive.spdx.json/",
	} {
		matched := contains(keys, want)
		if strings.HasSuffix(want, "sha256/") {
			for _, key := range keys {
				if strings.HasPrefix(key, want) {
					matched = true
				}
			}
		}
		if !matched {
			t.Errorf("subject plan missing %s; got %v", want, keys)
		}
	}
	for _, prefix := range []string{"oci-index/oci/images/xisnove-server/layout/blobs/sha256/", "oci-platform-manifest/oci/images/xisnove-server/layout/blobs/sha256/"} {
		found := false
		for _, key := range keys {
			if strings.HasPrefix(key, prefix) {
				found = true
			}
		}
		if !found {
			t.Errorf("subject plan missing digest-addressed %s; got %v", prefix, keys)
		}
	}
}

func TestBuildCandidateRejectsInvalidIdentityBeforeDocker(t *testing.T) {
	root := t.TempDir()
	runCommandIn(t, root, nil, "git", "init", "--quiet")
	runCommandIn(t, root, nil, "git", "config", "user.email", "candidate@example.invalid")
	runCommandIn(t, root, nil, "git", "config", "user.name", "Candidate Test")
	mustWriteFile(t, filepath.Join(root, "tracked"), "clean\n", 0o644)
	runCommandIn(t, root, nil, "git", "add", "tracked")
	runCommandIn(t, root, nil, "git", "commit", "--quiet", "-m", "fixture")
	commit := strings.TrimSpace(commandOutput(t, root, "git", "rev-parse", "HEAD"))
	script := filepath.Join(repositoryRoot(t), "scripts", "release", "build-candidate.sh")

	assertCandidateFailure(t, root, script, []string{"--root", root, "--version", "1.2", "--commit", commit, "--source-date-epoch", releaseEpoch}, "semantic version")
	assertCandidateFailure(t, root, script, []string{"--root", root, "--version", "1.2.3", "--commit", strings.Repeat("a", 40), "--source-date-epoch", releaseEpoch}, "does not match HEAD")
	mustWriteFile(t, filepath.Join(root, ".env"), "TURSO_TOKEN=must-not-enter-candidate\n", 0o600)
	assertCandidateFailure(t, root, script, []string{"--root", root, "--version", "1.2.3", "--commit", commit, "--source-date-epoch", releaseEpoch}, "working tree must be clean")
}

func TestBuildCandidateUsesLocalChecksumVerifiedGoLicenseCache(t *testing.T) {
	script := string(mustReadFile(t, filepath.Join(repositoryRoot(t), "scripts", "release", "build-candidate.sh")))
	for _, required := range []string{
		"GOWORK=off go mod download",
		"SYFT_GOLANG_SEARCH_LOCAL_MOD_CACHE_LICENSES=true",
		"SYFT_GOLANG_LOCAL_MOD_CACHE_DIR=/tmp/go-mod",
		"SYFT_GOLANG_SEARCH_REMOTE_LICENSES=false",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("candidate Go license cache contract missing %q", required)
		}
	}
}

func TestReleaseDockerfilesPinUbuntuSnapshot(t *testing.T) {
	const snapshot = "20260701T000000Z"
	root := repositoryRoot(t)
	paths := []string{
		"build/package/Dockerfile.agent",
		"build/package/Dockerfile.operator",
		"build/package/Dockerfile.server",
		"build/package/Dockerfile.ui",
		"build/release/Dockerfile.candidate-binaries",
	}
	for _, path := range paths {
		contents := string(mustReadFile(t, filepath.Join(root, path)))
		if !strings.Contains(contents, "ARG UBUNTU_SNAPSHOT="+snapshot) {
			t.Errorf("%s does not pin Ubuntu snapshot %s", path, snapshot)
		}
		if installs := strings.Count(contents, "apt-get install"); installs == 0 || strings.Count(contents, "snapshot.ubuntu.com/ubuntu/${UBUNTU_SNAPSHOT}") < installs {
			t.Errorf("%s does not bind every apt install stage to the snapshot", path)
		}
		if !strings.Contains(contents, "ADD --checksum=sha256:6e8cdcc8c86103acd4fc14649eac62ff2037108389074a7b167567af33c32245") || !strings.Contains(contents, "ca-certificates_20260601~22.04.1_all.deb") {
			t.Errorf("%s does not bootstrap the checksum-verified snapshot CA package", path)
		}
	}
}

func TestCandidateOCISBOMUsesCompleteLayoutAndExplicitPlatforms(t *testing.T) {
	tool := buildCandidatePlanTool(t)
	root := t.TempDir()
	layoutTar := filepath.Join(root, "layout.tar")
	writeSyntheticOCILayout(t, layoutTar)
	extracted := filepath.Join(root, "image")
	runTool(t, tool, "extract-oci", "--layout", layoutTar, "--output-dir", extracted, "--name", "xisnove-server")
	capture := filepath.Join(root, "syft.log")
	fakeSyft := filepath.Join(root, "syft")
	mustWriteFile(t, fakeSyft, `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$CAPTURE"
for arg in "$@"; do case "$arg" in spdx-json=*) output=${arg#spdx-json=} ;; esac; done
printf '%s\n' '{"spdxVersion":"SPDX-2.3","packages":[]}' > "$output"
`, 0o755)
	fakeReleasebundle := filepath.Join(root, "releasebundle")
	mustWriteFile(t, fakeReleasebundle, `#!/bin/sh
set -eu
shift
while [ "$#" -gt 0 ]; do case "$1" in --input) input=$2; shift 2 ;; --output) output=$2; shift 2 ;; *) shift ;; esac; done
cp "$input" "$output"
`, 0o755)
	output := filepath.Join(root, "sboms")
	cmd := exec.Command(tool, "sbom-oci", "--layout-dir", filepath.Join(extracted, "layout"), "--kind", "oci-index", "--name", "xisnove-server", "--output-dir", output, "--source-date-epoch", releaseEpoch, "--syft", fakeSyft, "--releasebundle", fakeReleasebundle)
	cmd.Env = append(os.Environ(), "CAPTURE="+capture)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sbom-oci: %v\n%s", err, combined)
	}
	for _, name := range []string{"oci-index--xisnove-server.spdx.json", "oci-platform-manifest--xisnove-server--linux-amd64.spdx.json", "oci-platform-manifest--xisnove-server--linux-arm64.spdx.json"} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
	log := string(mustReadFile(t, capture))
	if strings.Count(log, "oci-dir:"+filepath.Join(extracted, "layout")) != 2 || !strings.Contains(log, "--platform linux/amd64") || !strings.Contains(log, "--platform linux/arm64") {
		t.Fatalf("Syft source contract not explicit:\n%s", log)
	}
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		if !strings.Contains(line, "--platform") || !strings.Contains(line, "--enrich golang") {
			t.Fatalf("image index used unqualified Syft call: %s", line)
		}
	}
	composition := string(mustReadFile(t, filepath.Join(output, "oci-index--xisnove-server.spdx.json")))
	for _, required := range []string{"DocumentRef-linux-amd64", "DocumentRef-linux-arm64", "oci-platform-manifest--xisnove-server--linux-amd64.spdx.json", "oci-platform-manifest--xisnove-server--linux-arm64.spdx.json"} {
		if !strings.Contains(composition, required) {
			t.Errorf("index composition lacks %s: %s", required, composition)
		}
	}
}

func TestCandidateChartOCISBOMIndexesChartLayerBlob(t *testing.T) {
	tool := buildCandidatePlanTool(t)
	root := t.TempDir()
	chart := filepath.Join(root, "xisnove.tgz")
	mustWriteFile(t, chart, "exact chart package", 0o644)
	layout := filepath.Join(root, "layout")
	writeSyntheticChartLayout(t, layout, chart)
	chartDigest := sha256.Sum256(mustReadFile(t, chart))
	wantSource := "file:" + filepath.Join(layout, "blobs", "sha256", hex.EncodeToString(chartDigest[:]))

	capture := filepath.Join(root, "syft.log")
	fakeSyft := filepath.Join(root, "syft")
	mustWriteFile(t, fakeSyft, `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$CAPTURE"
for arg in "$@"; do case "$arg" in spdx-json=*) output=${arg#spdx-json=} ;; esac; done
printf '%s\n' '{"spdxVersion":"SPDX-2.3","packages":[]}' > "$output"
`, 0o755)
	fakeReleasebundle := filepath.Join(root, "releasebundle")
	mustWriteFile(t, fakeReleasebundle, `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$RELEASEBUNDLE_CAPTURE"
shift
while [ "$#" -gt 0 ]; do case "$1" in --input) input=$2; shift 2 ;; --output) output=$2; shift 2 ;; *) shift ;; esac; done
cp "$input" "$output"
`, 0o755)
	releasebundleCapture := filepath.Join(root, "releasebundle.log")
	output := filepath.Join(root, "sboms")
	cmd := exec.Command(tool, "sbom-oci", "--layout-dir", layout, "--kind", "oci-manifest", "--name", "xisnove", "--output-dir", output, "--source-date-epoch", releaseEpoch, "--syft", fakeSyft, "--releasebundle", fakeReleasebundle, "--platforms=false")
	cmd.Env = append(os.Environ(), "CAPTURE="+capture, "RELEASEBUNDLE_CAPTURE="+releasebundleCapture)
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("sbom-oci: %v\n%s", err, combined)
	}
	if got := strings.TrimSpace(string(mustReadFile(t, capture))); !strings.HasPrefix(got, wantSource+" ") || !strings.Contains(got, "--enrich golang") {
		t.Fatalf("Syft chart source = %q, want exact chart layer %q", got, wantSource)
	}
	if got := strings.TrimSpace(string(mustReadFile(t, releasebundleCapture))); !strings.Contains(got, "--first-party-sha256 "+hex.EncodeToString(chartDigest[:])) {
		t.Fatalf("chart layer digest was not passed as first-party evidence: %s", got)
	}
	if _, err := os.Stat(filepath.Join(output, "oci-manifest--xisnove.spdx.json")); err != nil {
		t.Fatalf("missing chart OCI SBOM: %v", err)
	}
}

func TestCandidatePlanRequiresExact64SubjectClosure(t *testing.T) {
	tool := buildCandidatePlanTool(t)
	root := t.TempDir()
	version := "1.2.3"
	var archiveNames []string
	for _, binary := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		for _, arch := range []string{"amd64", "arm64"} {
			archiveNames = append(archiveNames, binary+"_"+version+"_linux_"+arch)
		}
	}
	for _, osName := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			archiveNames = append(archiveNames, "xisnove_"+version+"_"+osName+"_"+arch)
		}
	}
	for _, name := range archiveNames {
		mustWriteFile(t, filepath.Join(root, "archives", name+".tar.gz"), name, 0o644)
		mustWriteFile(t, filepath.Join(root, "sboms", "archive--"+name+".spdx.json"), "{}\n", 0o644)
	}
	for _, chart := range []string{"xisnove", "xisnove-edge"} {
		chartPath := filepath.Join(root, "charts", chart+"_"+version+".tgz")
		mustWriteFile(t, chartPath, chart, 0o644)
		mustWriteFile(t, filepath.Join(root, "sboms", "chart--"+chart+".spdx.json"), "{}\n", 0o644)
		writeSyntheticChartLayout(t, filepath.Join(root, "oci", "charts", chart, "layout"), chartPath)
		mustWriteFile(t, filepath.Join(root, "sboms", "oci-manifest--"+chart+".spdx.json"), "{}\n", 0o644)
	}
	for _, bundle := range []string{"xisnove-source", "xisnove-deployment"} {
		mustWriteFile(t, filepath.Join(root, "bundles", bundle+"_"+version+".tar.gz"), bundle, 0o644)
	}
	for _, image := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		layoutTar := filepath.Join(t.TempDir(), image+".tar")
		writeSyntheticOCILayout(t, layoutTar)
		runTool(t, tool, "extract-oci", "--layout", layoutTar, "--output-dir", filepath.Join(root, "oci", "images", image), "--name", image)
		mustWriteFile(t, filepath.Join(root, "sboms", "oci-index--"+image+".spdx.json"), "{}\n", 0o644)
		for _, arch := range []string{"amd64", "arm64"} {
			mustWriteFile(t, filepath.Join(root, "sboms", "oci-platform-manifest--"+image+"--linux-"+arch+".spdx.json"), "{}\n", 0o644)
		}
	}
	mustWriteFile(t, filepath.Join(root, "metadata", "licenses.json"), "{}\n", 0o644)
	mustWriteFile(t, filepath.Join(root, "metadata", "toolchain.lock.json"), "{}\n", 0o644)
	output := filepath.Join(root, "subjects.json")
	runTool(t, tool, "plan", "--root", root, "--output", output, "--contract-version", version)
	var plans []map[string]any
	if err := json.Unmarshal(mustReadFile(t, output), &plans); err != nil {
		t.Fatal(err)
	}
	if len(plans) != 64 {
		t.Fatalf("subject count=%d want=64", len(plans))
	}
}

func TestCandidateBuilderPinsContainerAndToolInputs(t *testing.T) {
	dockerfile := string(mustReadFile(t, filepath.Join(repositoryRoot(t), "build", "release", "Dockerfile.candidate-binaries")))
	for _, required := range []string{
		"ubuntu:22.04@sha256:0e0a0fc6d18feda9db1590da249ac93e8d5abfea8f4c3c0c849ce512b5ef8982",
		"GO_SHA256", "GORELEASER_SHA256", "SYFT_SHA256", "ORAS_SHA256", "sha256sum -c",
	} {
		if !strings.Contains(dockerfile, required) {
			t.Errorf("candidate Dockerfile lacks %q", required)
		}
	}
	script := string(mustReadFile(t, filepath.Join(repositoryRoot(t), "scripts", "release", "build-candidate.sh")))
	for _, required := range []string{
		"XISNOVE_RELEASE_OUTPUT=/out/archives", `release_tmp_root=${XISNOVE_RELEASE_TMPDIR:-$(dirname "$root")}`, `work_dir=$(cd "$work_dir" && pwd -P)`, "buildx bake", `--allow="fs.write=$layouts"`, "generate-sboms.sh",
		"sbom-oci", "org.opencontainers.image.created=${XISNOVE_BUILD_DATE}", "inventory-licenses.sh", "candidate-manifest.json.sha256", "verify --root",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("candidate builder lacks %q", required)
		}
	}
	helper := string(mustReadFile(t, filepath.Join(repositoryRoot(t), "scripts", "release", "cmd", "candidateplan", "main.go")))
	for _, required := range []string{"oci-dir:", `"--platform"`, "validateLayoutClosure"} {
		if !strings.Contains(helper, required) {
			t.Errorf("candidate helper lacks %q", required)
		}
	}
}

func TestReleaseMakeTargetsWireCandidateIdentityAndReproducibilityGate(t *testing.T) {
	root := repositoryRoot(t)
	command := exec.Command("make", "--no-print-directory", "-n", "distribution-release-candidate", "distribution-release-check")
	command.Dir = root
	command.Env = append(os.Environ(),
		"XISNOVE_RELEASE_VERSION=1.2.3",
		"XISNOVE_RELEASE_COMMIT="+strings.Repeat("a", 40),
		"SOURCE_DATE_EPOCH="+releaseEpoch,
		"XISNOVE_RELEASE_CANDIDATE_OUTPUT=dist/candidate-test",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("dry-run release targets: %v\n%s", err, output)
	}
	text := string(output)
	for _, required := range []string{
		"scripts/release/build-candidate.sh",
		"--output-dir \"dist/candidate-test\"",
		"--version \"1.2.3\"",
		"--commit \"" + strings.Repeat("a", 40) + "\"",
		"--source-date-epoch \"" + releaseEpoch + "\"",
		"scripts/release/check-reproducible-candidate.sh",
		"go test -race ./integration/distribution/release -count=1",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("release target dry-run lacks %q:\n%s", required, text)
		}
	}
}

func TestReproducibleCandidateGateComparesIndependentCleanWorktrees(t *testing.T) {
	scriptContents := string(mustReadFile(t, filepath.Join(repositoryRoot(t), "scripts", "release", "check-reproducible-candidate.sh")))
	for _, required := range []string{
		`reproducibility_tmp_root=${XISNOVE_RELEASE_TMPDIR:-$(dirname "$root")}`,
		`work_root=$(mktemp -d "$reproducibility_tmp_root/xisnove-reproducibility.XXXXXXXX")`,
		`work_root=$(cd "$work_root" && pwd -P)`,
	} {
		if !strings.Contains(scriptContents, required) {
			t.Errorf("reproducibility gate lacks Docker-shareable worktree placement %q", required)
		}
	}
	if t.Failed() {
		return
	}

	root := t.TempDir()
	runCommandIn(t, root, nil, "git", "init", "--quiet")
	runCommandIn(t, root, nil, "git", "config", "user.email", "candidate@example.invalid")
	runCommandIn(t, root, nil, "git", "config", "user.name", "Candidate Test")
	writeBuilder := func(payload string) string {
		return `#!/bin/sh
set -eu
output=
rootarg=
version=
commit=
epoch=
while [ "$#" -gt 0 ]; do
  case "$1" in
    --root) rootarg=$2; shift 2 ;;
    --output-dir) output=$2; shift 2 ;;
    --version) version=$2; shift 2 ;;
    --commit) commit=$2; shift 2 ;;
    --source-date-epoch) epoch=$2; shift 2 ;;
    *) shift ;;
  esac
done
mkdir -p "$output"
payload=` + payload + `
printf '%s\n' "$payload" "$version" "$commit" "$epoch" > "$output/candidate-manifest.json"
sha256sum "$output/candidate-manifest.json" | sed 's# .*/#  #' > "$output/candidate-manifest.json.sha256"
cp "$output/candidate-manifest.json.sha256" "$output/checksums.txt"
`
	}
	mustWriteFile(t, filepath.Join(root, "scripts", "release", "build-candidate.sh"), writeBuilder("stable"), 0o755)
	runCommandIn(t, root, nil, "git", "add", ".")
	runCommandIn(t, root, nil, "git", "commit", "--quiet", "-m", "stable builder")
	commit := strings.TrimSpace(commandOutput(t, root, "git", "rev-parse", "HEAD"))
	script := filepath.Join(repositoryRoot(t), "scripts", "release", "check-reproducible-candidate.sh")
	args := []string{script, "--root", root, "--version", "1.2.3", "--commit", commit, "--source-date-epoch", releaseEpoch}
	command := exec.Command("bash", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("stable reproducibility gate: %v\n%s", err, output)
	}

	mustWriteFile(t, filepath.Join(root, "scripts", "release", "build-candidate.sh"), writeBuilder("$rootarg"), 0o755)
	runCommandIn(t, root, nil, "git", "add", ".")
	runCommandIn(t, root, nil, "git", "commit", "--quiet", "-m", "nondeterministic builder")
	commit = strings.TrimSpace(commandOutput(t, root, "git", "rev-parse", "HEAD"))
	args = []string{script, "--root", root, "--version", "1.2.3", "--commit", commit, "--source-date-epoch", releaseEpoch}
	command = exec.Command("bash", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "candidate manifests differ") {
		t.Fatalf("nondeterministic gate = %v, output=%s", err, output)
	}
}

func buildCandidatePlanTool(t *testing.T) string {
	t.Helper()
	tool := filepath.Join(t.TempDir(), "candidateplan")
	cmd := exec.Command("go", "build", "-trimpath", "-o", tool, "./scripts/release/cmd/candidateplan")
	cmd.Dir = repositoryRoot(t)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build candidateplan: %v\n%s", err, output)
	}
	return tool
}

func assertCandidateFailure(t *testing.T, dir, script string, args []string, marker string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{script}, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DOCKER_BIN=/definitely/not/docker")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), marker) {
		t.Fatalf("candidate builder failure = %v, output=%s, want marker %q", err, output, marker)
	}
}

func commandOutput(t *testing.T, dir, command string, args ...string) string {
	t.Helper()
	cmd := exec.Command(command, args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", command, err, output)
	}
	return string(output)
}

func writeSyntheticOCILayout(t *testing.T, path string) ([]byte, []byte, []byte) {
	t.Helper()
	amd64Config := []byte(`{"architecture":"amd64","os":"linux"}`)
	arm64Config := []byte(`{"architecture":"arm64","os":"linux"}`)
	amd64ConfigDigest := sha256.Sum256(amd64Config)
	arm64ConfigDigest := sha256.Sum256(arm64Config)
	amd64 := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + hex.EncodeToString(amd64ConfigDigest[:]) + `","size":` + decimal(len(amd64Config)) + `},"layers":[]}`)
	arm64 := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"sha256:` + hex.EncodeToString(arm64ConfigDigest[:]) + `","size":` + decimal(len(arm64Config)) + `},"layers":[]}`)
	amd64Digest := sha256.Sum256(amd64)
	arm64Digest := sha256.Sum256(arm64)
	index := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.index.v1+json","manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:` + hex.EncodeToString(amd64Digest[:]) + `","size":` + decimal(len(amd64)) + `,"platform":{"architecture":"amd64","os":"linux"}},{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:` + hex.EncodeToString(arm64Digest[:]) + `","size":` + decimal(len(arm64)) + `,"platform":{"architecture":"arm64","os":"linux"}}]}`)
	indexDigest := sha256.Sum256(index)
	layoutIndex := []byte(`{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"sha256:` + hex.EncodeToString(indexDigest[:]) + `","size":` + decimal(len(index)) + `}]}`)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := tar.NewWriter(file)
	for name, contents := range map[string][]byte{
		"index.json": layoutIndex,
		"oci-layout": []byte(`{"imageLayoutVersion":"1.0.0"}`),
		"blobs/sha256/" + hex.EncodeToString(indexDigest[:]):       index,
		"blobs/sha256/" + hex.EncodeToString(amd64Digest[:]):       amd64,
		"blobs/sha256/" + hex.EncodeToString(arm64Digest[:]):       arm64,
		"blobs/sha256/" + hex.EncodeToString(amd64ConfigDigest[:]): amd64Config,
		"blobs/sha256/" + hex.EncodeToString(arm64ConfigDigest[:]): arm64Config,
	} {
		if err := w.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(contents))}); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(contents); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return index, amd64, arm64
}

func writeSyntheticChartLayout(t *testing.T, root, chart string) {
	t.Helper()
	chartBytes := mustReadFile(t, chart)
	chartDigest := sha256.Sum256(chartBytes)
	config := []byte(`{}`)
	configDigest := sha256.Sum256(config)
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.cncf.helm.config.v1+json","digest":"sha256:` + hex.EncodeToString(configDigest[:]) + `","size":2},"layers":[{"mediaType":"application/vnd.cncf.helm.chart.content.v1.tar+gzip","digest":"sha256:` + hex.EncodeToString(chartDigest[:]) + `","size":` + decimal(len(chartBytes)) + `}]}`)
	manifestDigest := sha256.Sum256(manifest)
	index := []byte(`{"schemaVersion":2,"manifests":[{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:` + hex.EncodeToString(manifestDigest[:]) + `","size":` + decimal(len(manifest)) + `}]}`)
	mustWriteFile(t, filepath.Join(root, "oci-layout"), `{"imageLayoutVersion":"1.0.0"}`, 0o644)
	mustWriteFile(t, filepath.Join(root, "index.json"), string(index), 0o644)
	mustWriteFile(t, filepath.Join(root, "blobs", "sha256", hex.EncodeToString(configDigest[:])), string(config), 0o644)
	mustWriteFile(t, filepath.Join(root, "blobs", "sha256", hex.EncodeToString(chartDigest[:])), string(chartBytes), 0o644)
	mustWriteFile(t, filepath.Join(root, "blobs", "sha256", hex.EncodeToString(manifestDigest[:])), string(manifest), 0o644)
}

func readTarFile(t *testing.T, path, name string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	reader, err := gzipReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err != nil {
			t.Fatal(err)
		}
		if header.Name != name {
			continue
		}
		var output bytes.Buffer
		if _, err := output.ReadFrom(tarReader); err != nil {
			t.Fatal(err)
		}
		return output.String()
	}
}

func gzipReader(file *os.File) (*gzip.Reader, error) { return gzip.NewReader(file) }

func decimal(value int) string { return strconv.Itoa(value) }
