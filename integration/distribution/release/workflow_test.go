package release_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

const (
	checkoutAction         = "actions/checkout@11d5960a326750d5838078e36cf38b85af677262"
	setupGoAction          = "actions/setup-go@924ae3a1cded613372ab5595356fb5720e22ba16"
	setupQEMUAction        = "docker/setup-qemu-action@96fe6ef7f33517b61c61be40b68a1882f3264fb8"
	setupBuildxAction      = "docker/setup-buildx-action@bb05f3f5519dd87d3ba754cc423b652a5edd6d2c"
	uploadArtifactAction   = "actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a"
	downloadArtifactAction = "actions/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c"
	attestAction           = "actions/attest@f7c74d28b9d84cb8768d0b8ca14a4bac6ef463e6"
	loginAction            = "docker/login-action@abd2ef45e78c5afb21d64d4ca52ee8550d9572c7"
)

type workflowDocument struct {
	On          map[string]any            `yaml:"on"`
	Permissions map[string]string         `yaml:"permissions"`
	Env         map[string]string         `yaml:"env"`
	Jobs        map[string]map[string]any `yaml:"jobs"`
}

func TestReleaseWorkflowTrustAndPromotionContract(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), ".github", "workflows", "release.yml")
	contents, workflow := readWorkflow(t, path)

	if len(workflow.On) != 1 || workflow.On["workflow_dispatch"] == nil {
		t.Fatalf("release trigger = %#v, want workflow_dispatch only", workflow.On)
	}
	for _, input := range []string{"commit", "version", "publish"} {
		if !strings.Contains(contents, input+":") {
			t.Errorf("release workflow lacks %s input", input)
		}
	}
	assertPermissions(t, workflow.Permissions, map[string]string{"contents": "read"})

	qualityGate := requiredJob(t, workflow, "quality-gate")
	qualityText := yamlText(t, qualityGate)
	if !strings.Contains(qualityText, "./.github/workflows/ci.yml") {
		t.Fatal("release candidate must reuse the exact callable CI/native amd64 gate")
	}

	candidate := requiredJob(t, workflow, "candidate")
	assertNeeds(t, candidate, "quality-gate")
	assertPermissions(t, stringMap(t, candidate["permissions"]), map[string]string{"contents": "read"})
	candidateText := yamlText(t, candidate)
	for _, required := range []string{
		checkoutAction,
		setupGoAction,
		setupQEMUAction,
		setupBuildxAction,
		uploadArtifactAction,
		"ref: ${{ inputs.commit }}",
		"persist-credentials: false",
		"ubuntu:22.04@sha256:0e0a0fc6d18feda9db1590da249ac93e8d5abfea8f4c3c0c849ce512b5ef8982",
		"make distribution-release-candidate",
		"XISNOVE_RELEASE_CANDIDATE_OUTPUT",
		"scripts/release/build-binaries.sh",
		"scripts/release/assemble-bundle.sh",
		"scripts/release/generate-sboms.sh",
		"scripts/release/inventory-licenses.sh",
		"scripts/release/verify-bundle.sh",
		"name: xisnove-release-candidate",
		"path: dist/candidate",
	} {
		if !strings.Contains(candidateText, required) {
			t.Errorf("candidate job lacks %q", required)
		}
	}
	if strings.Count(candidateText, "make distribution-release-candidate") != 1 {
		t.Fatal("candidate must invoke the parent-owned candidate build exactly once")
	}
	if strings.Contains(candidateText, "XISNOVE_RELEASE_OUTPUT") {
		t.Fatal("candidate workflow must set the candidate root, not the inner archive output")
	}
	assertExactCheckout(t, candidateText)

	attestor := requiredJob(t, workflow, "attestor")
	assertNeeds(t, attestor, "candidate")
	assertPermissions(t, stringMap(t, attestor["permissions"]), map[string]string{
		"attestations": "write",
		"contents":     "read",
		"id-token":     "write",
	})
	attestorText := yamlText(t, attestor)
	for _, required := range []string{
		downloadArtifactAction,
		attestAction,
		"name: xisnove-release-candidate",
		"subject-path: dist/candidate/candidate-manifest.json",
		"create-storage-record: false",
		"needs.candidate.outputs.artifact_digest",
		"python3 -c \"$XISNOVE_TRUSTED_CANDIDATE_VERIFIER\"",
	} {
		if !strings.Contains(attestorText, required) {
			t.Errorf("attestor job lacks %q", required)
		}
	}
	assertNoBuildOrCheckout(t, "attestor", attestorText)
	assertNoCandidateExecutable(t, "attestor", attestorText)

	homelab := requiredJob(t, workflow, "homelab-acceptance")
	assertNeeds(t, homelab, "candidate")
	assertNeeds(t, homelab, "attestor")
	homelabText := yamlText(t, homelab)
	for _, required := range []string{
		"./.github/workflows/homelab-acceptance.yml",
		"candidate_run_id: ${{ github.run_id }}",
		"candidate_commit: ${{ inputs.commit }}",
		"candidate_version: ${{ inputs.version }}",
		"candidate_artifact_digest: ${{ needs.candidate.outputs.artifact_digest }}",
	} {
		if !strings.Contains(homelabText, required) {
			t.Errorf("release homelab gate lacks %q", required)
		}
	}

	publish := requiredJob(t, workflow, "publish")
	assertNeeds(t, publish, "homelab-acceptance")
	if got := fmt.Sprint(publish["environment"]); !strings.Contains(got, "xisnove-release") {
		t.Fatalf("publish environment = %q", got)
	}
	assertPermissions(t, stringMap(t, publish["permissions"]), map[string]string{
		"attestations": "read",
		"contents":     "read",
		"packages":     "write",
	})
	publishText := yamlText(t, publish)
	for _, required := range []string{
		downloadArtifactAction,
		loginAction,
		"needs.candidate.outputs.artifact_digest",
		"python3 -c \"$XISNOVE_TRUSTED_CANDIDATE_VERIFIER\"",
		"attestation verify",
		"--cert-oidc-issuer https://token.actions.githubusercontent.com",
		"--repo \"$GITHUB_REPOSITORY\"",
		"--signer-workflow \"$GITHUB_REPOSITORY/.github/workflows/release.yml\"",
		"--source-digest \"${{ inputs.commit }}\"",
		"--source-ref \"$GITHUB_REF\"",
		"verifiedTimestamps",
		"cp --from-oci-layout",
		".subjects[]",
		"python3 -c \"$XISNOVE_TRUSTED_CANDIDATE_VERIFIER\"",
		"published-registry-subjects",
		"xisnove-edge",
		"cmp --silent",
	} {
		if !strings.Contains(publishText, required) {
			t.Errorf("publish job lacks %q", required)
		}
	}
	assertNoBuild(t, "publish", publishText)
	assertNoCandidateExecutable(t, "publish", publishText)
	if strings.Contains(publishText, "oras push") || strings.Contains(publishText, `\"$ORAS\" push`) {
		t.Fatal("chart publication must copy accepted OCI layouts, never create a registry manifest with oras push")
	}
	if strings.Contains(publishText, "actions/checkout@") {
		t.Fatal("publish must promote candidate without checking out repository code")
	}
	if !strings.Contains(fmt.Sprint(publish["if"]), "inputs.publish") {
		t.Fatal("protected publish is not explicitly selected")
	}

	signer := requiredJob(t, workflow, "signer")
	assertNeeds(t, signer, "publish")
	assertPermissions(t, stringMap(t, signer["permissions"]), map[string]string{
		"contents": "read",
		"id-token": "write",
		"packages": "write",
	})
	signerText := yamlText(t, signer)
	for _, required := range []string{
		downloadArtifactAction,
		loginAction,
		"published-registry-subjects",
		"cosign sign --yes",
		"cosign attest --yes",
		"cosign verify",
		"cosign verify-attestation",
		"--certificate-oidc-issuer https://token.actions.githubusercontent.com",
		"--certificate-identity",
		"expected-registry-subjects",
		"cmp --silent",
		".subjects[]",
	} {
		if !strings.Contains(signerText, required) {
			t.Errorf("signer job lacks %q", required)
		}
	}
	assertNoBuildOrCheckout(t, "signer", signerText)
	assertNoCandidateExecutable(t, "signer", signerText)

	releaseAssets := requiredJob(t, workflow, "release-assets")
	assertNeeds(t, releaseAssets, "signer")
	assertPermissions(t, stringMap(t, releaseAssets["permissions"]), map[string]string{
		"attestations": "read",
		"contents":     "write",
	})
	releaseText := yamlText(t, releaseAssets)
	for _, required := range []string{
		downloadArtifactAction,
		"attestation verify",
		"repos/$GITHUB_REPOSITORY/git/refs",
		"ref=refs/tags/v${{ inputs.version }}",
		"sha=${{ inputs.commit }}",
		".subjects[]",
		"release upload",
		"release download",
		"asset digest mismatch",
		"python3 -c \"$XISNOVE_TRUSTED_CANDIDATE_VERIFIER\"",
	} {
		if !strings.Contains(releaseText, required) {
			t.Errorf("release-assets job lacks %q", required)
		}
	}
	assertNoBuildOrCheckout(t, "release-assets", releaseText)
	assertNoCandidateExecutable(t, "release-assets", releaseText)
	if strings.Contains(releaseText, "--clobber") {
		t.Fatal("release retry must never overwrite an existing asset")
	}
	if strings.Contains(releaseText, "find dist/candidate") {
		t.Fatal("GitHub Release assets must come from canonical manifest subjects, not an unbounded directory walk")
	}
	if !strings.Contains(releaseText, "sha256sum") {
		t.Fatal("release publisher must verify downloaded asset digests")
	}

	if strings.Count(contents, "if: always()") < 5 || strings.Count(contents, "test ! -e") < 5 {
		t.Fatal("each release job must clean and assert no workspace residue with if: always()")
	}
	assertAllActionsPinned(t, contents)
	assertWorkflowToolMatchesLock(t, contents, "gh", "2.96.0", "83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60")
	assertWorkflowToolMatchesLock(t, contents, "oras", "1.3.2", "9229ccc6d17bb282039ad4a69abb16dcb887a5bce567c075d731d9b3c7ad8eaf")
	assertWorkflowToolMatchesLock(t, contents, "cosign", "3.1.2", "f7622ed3cf22e55e1ae6377c080979ff77a22da9981c11df222a2e444991e7cf")
	assertWorkflowToolMatchesLock(t, contents, "gh", "2.96.0", "83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60")
	for _, contract := range []string{
		"json.dumps(manifest, ensure_ascii=False, separators=(\",\", \":\"))",
		"expected-subject-closure",
		"len(expected) != 64",
		"oci-manifest",
		"metadata/licenses.json",
		"metadata/toolchain.lock.json",
		"sbom_name = kind + \"--\" + name + suffix",
	} {
		if !strings.Contains(contents, contract) {
			t.Errorf("trusted candidate verifier lacks %q", contract)
		}
	}
	for _, jobName := range []string{"attestor", "publish", "signer", "release-assets"} {
		jobText := yamlText(t, requiredJob(t, workflow, jobName))
		if !strings.Contains(jobText, "python3 -c \"$XISNOVE_TRUSTED_CANDIDATE_VERIFIER\"") {
			t.Errorf("%s does not invoke trusted candidate closure verifier", jobName)
		}
	}
}

func TestReleaseWorkflowTrustedVerifierAcceptsOnlyExactClosure(t *testing.T) {
	_, workflow := readWorkflow(t, filepath.Join(repositoryRoot(t), ".github", "workflows", "release.yml"))
	verifier := workflow.Env["XISNOVE_TRUSTED_CANDIDATE_VERIFIER"]
	if verifier == "" {
		t.Fatal("trusted candidate verifier is empty")
	}
	root := t.TempDir()
	version := "1.2.3"
	commit := strings.Repeat("a", 40)
	type subject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Locator   string `json:"locator"`
		SHA256    string `json:"sha256"`
		Size      int    `json:"size"`
		Platform  string `json:"platform,omitempty"`
		MediaType string `json:"mediaType,omitempty"`
	}
	var subjects []subject
	add := func(kind, name, platform, locator string) subject {
		contents := []byte(kind + "/" + name + "/" + platform + "\n")
		digest := fmt.Sprintf("%x", sha256.Sum256(contents))
		if locator == "oci-image" {
			locator = "oci/images/" + name + "/layout/blobs/sha256/" + digest
		}
		if locator == "oci-chart" {
			locator = "oci/charts/" + name + "/layout/blobs/sha256/" + digest
		}
		path := filepath.Join(root, filepath.FromSlash(locator))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			t.Fatal(err)
		}
		item := subject{Kind: kind, Name: name, Locator: locator, SHA256: digest, Size: len(contents), Platform: platform}
		subjects = append(subjects, item)
		return item
	}
	addWithSBOM := func(kind, name, platform, locator string) {
		add(kind, name, platform, locator)
		suffix := ""
		if platform != "" {
			suffix = "--" + strings.ReplaceAll(platform, "/", "-")
		}
		sbomName := kind + "--" + name + suffix
		add("sbom", sbomName, "", "sboms/"+sbomName+".spdx.json")
	}
	for _, binary := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := binary + "_" + version + "_linux_" + arch
			addWithSBOM("archive", name, "", "archives/"+name+".tar.gz")
		}
	}
	for _, operatingSystem := range []string{"linux", "darwin", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "xisnove_" + version + "_" + operatingSystem + "_" + arch
			addWithSBOM("archive", name, "", "archives/"+name+".tar.gz")
		}
	}
	for _, chart := range []string{"xisnove", "xisnove-edge"} {
		addWithSBOM("chart", chart, "", "charts/"+chart+"_"+version+".tgz")
	}
	for _, bundle := range []string{"xisnove-source", "xisnove-deployment"} {
		add("bundle", bundle, "", "bundles/"+bundle+"_"+version+".tar.gz")
	}
	for _, image := range []string{"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator"} {
		addWithSBOM("oci-index", image, "", "oci-image")
		for _, platform := range []string{"linux/amd64", "linux/arm64"} {
			addWithSBOM("oci-platform-manifest", image, platform, "oci-image")
		}
	}
	for _, chart := range []string{"xisnove", "xisnove-edge"} {
		addWithSBOM("oci-manifest", chart, "", "oci-chart")
	}
	add("metadata", "licenses", "", "metadata/licenses.json")
	add("metadata", "toolchain-lock", "", "metadata/toolchain.lock.json")
	sort.Slice(subjects, func(i, j int) bool {
		left := subjects[i].Kind + "\x00" + subjects[i].Name + "\x00" + subjects[i].Platform
		right := subjects[j].Kind + "\x00" + subjects[j].Name + "\x00" + subjects[j].Platform
		return left < right
	})
	manifest := struct {
		SchemaVersion   int       `json:"schemaVersion"`
		Repository      string    `json:"repository"`
		Commit          string    `json:"commit"`
		Version         string    `json:"version"`
		SourceDateEpoch int64     `json:"sourceDateEpoch"`
		Subjects        []subject `json:"subjects"`
	}{1, "github.com/araihu/xisnove", commit, version, 1700000000, subjects}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(manifest); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "candidate-manifest.json")
	if err := os.WriteFile(manifestPath, encoded.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(encoded.Bytes()))
	if err := os.WriteFile(filepath.Join(root, "candidate-manifest.json.sha256"), []byte(digest+"  candidate-manifest.json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func() error {
		command := exec.Command("python3", "-c", verifier, root, commit, version)
		return command.Run()
	}
	if err := run(); err != nil {
		t.Fatalf("trusted verifier rejected exact closure: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(subjects[0].Locator)), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := run(); err == nil {
		t.Fatal("trusted verifier accepted tampered subject")
	}
}

func TestHomelabWorkflowUsesExactCandidateIdentity(t *testing.T) {
	path := filepath.Join(repositoryRoot(t), ".github", "workflows", "homelab-acceptance.yml")
	contents, workflow := readWorkflow(t, path)
	for _, trigger := range []string{"workflow_dispatch", "workflow_call"} {
		if workflow.On[trigger] == nil {
			t.Errorf("homelab workflow lacks %s", trigger)
		}
	}
	for _, input := range []string{"candidate_run_id", "candidate_commit", "candidate_version", "candidate_artifact_digest"} {
		if strings.Count(contents, input+":") < 2 {
			t.Errorf("homelab workflow must define %s for manual and callable triggers", input)
		}
	}
	assertPermissions(t, workflow.Permissions, map[string]string{"actions": "read", "contents": "read"})

	acceptance := requiredJob(t, workflow, "homelab-acceptance")
	text := yamlText(t, acceptance)
	for _, required := range []string{
		checkoutAction,
		downloadArtifactAction,
		"ref: ${{ inputs.candidate_commit }}",
		"run-id: ${{ inputs.candidate_run_id }}",
		"name: xisnove-release-candidate",
		"candidate_artifact_digest",
		"scripts/release/verify-bundle.sh",
		"RELEASEBUNDLE_BIN=dist/candidate/tools/releasebundle",
		"candidate-manifest.json",
		"candidate_commit",
		"candidate_version",
		"Execute accepted linux amd64 binaries",
		"--version",
		"scripts/release/accept-candidate-homelab.sh",
		"--candidate-root \"$XISNOVE_RELEASE_CANDIDATE_ROOT\"",
		"--commit \"$XISNOVE_RELEASE_COMMIT\"",
		"--version \"$XISNOVE_RELEASE_VERSION\"",
		"ORAS_BIN: ${{ runner.temp }}/xisnove-oras",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("homelab acceptance lacks %q", required)
		}
	}
	if strings.Contains(text, "scripts/kind-edge-e2e.sh") {
		t.Fatal("homelab release acceptance must consume candidate OCI bytes, not rebuild source images")
	}
	assertExactCheckout(t, text)
	for _, forbidden := range []string{"ref: main", "ref: master", "github.ref_name", "github.event.repository.default_branch"} {
		if strings.Contains(contents, forbidden) {
			t.Errorf("homelab workflow assumes branch through %q", forbidden)
		}
	}
	if !strings.Contains(contents, "if: always()") || !strings.Contains(contents, "test ! -e") {
		t.Fatal("homelab workflow lacks cleanup and residue assertion")
	}
	assertAllActionsPinned(t, contents)
	assertWorkflowToolMatchesLock(t, contents, "gh", "2.96.0", "83d5c2ccad5498f58bf6368acb1ab32588cf43ab3a4b1c301bf36328b1c8bd60")
	assertWorkflowToolMatchesLock(t, contents, "oras", "1.3.2", "9229ccc6d17bb282039ad4a69abb16dcb887a5bce567c075d731d9b3c7ad8eaf")
}

func readWorkflow(t *testing.T, path string) (string, workflowDocument) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var workflow workflowDocument
	if err := yaml.Unmarshal(contents, &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if len(workflow.Jobs) == 0 {
		t.Fatalf("%s has no jobs", path)
	}
	return string(contents), workflow
}

func requiredJob(t *testing.T, workflow workflowDocument, name string) map[string]any {
	t.Helper()
	job, ok := workflow.Jobs[name]
	if !ok {
		t.Fatalf("workflow lacks %q job", name)
	}
	return job
}

func stringMap(t *testing.T, value any) map[string]string {
	t.Helper()
	result := map[string]string{}
	for key, raw := range value.(map[string]any) {
		result[key] = fmt.Sprint(raw)
	}
	return result
}

func assertPermissions(t *testing.T, got, want map[string]string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("permissions = %v, want exactly %v", got, want)
	}
}

func assertNeeds(t *testing.T, job map[string]any, want string) {
	t.Helper()
	if !strings.Contains(fmt.Sprint(job["needs"]), want) {
		t.Fatalf("job needs = %v, want %q", job["needs"], want)
	}
}

func yamlText(t *testing.T, value any) string {
	t.Helper()
	contents, err := yaml.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func assertExactCheckout(t *testing.T, text string) {
	t.Helper()
	if !strings.Contains(text, checkoutAction) || !strings.Contains(text, "persist-credentials: false") {
		t.Fatal("checkout must be pinned and must not persist credentials")
	}
}

func assertNoBuildOrCheckout(t *testing.T, job, text string) {
	t.Helper()
	if strings.Contains(text, "actions/checkout@") {
		t.Errorf("%s must not checkout repository code", job)
	}
	assertNoBuild(t, job, text)
}

func assertNoBuild(t *testing.T, job, text string) {
	t.Helper()
	for _, forbidden := range []string{"build-binaries.sh", "assemble-bundle.sh", "generate-sboms.sh", "inventory-licenses.sh", "docker build", "buildx bake", "goreleaser"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Errorf("%s rebuilds candidate through %q", job, forbidden)
		}
	}
}

func assertNoCandidateExecutable(t *testing.T, job, text string) {
	t.Helper()
	for _, forbidden := range []string{
		"dist/candidate/verify-bundle.sh",
		"dist/candidate/tools/",
		"RELEASEBUNDLE_BIN=dist/candidate",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("%s executes candidate-provided code through %q", job, forbidden)
		}
	}
}

func assertAllActionsPinned(t *testing.T, contents string) {
	t.Helper()
	uses := regexp.MustCompile(`(?m)^\s*-?\s*uses:\s*([^\s]+)`).FindAllStringSubmatch(contents, -1)
	pinned := regexp.MustCompile(`^[^@]+@[0-9a-f]{40}$`)
	for _, match := range uses {
		if strings.HasPrefix(match[1], "./") {
			continue
		}
		if !pinned.MatchString(match[1]) {
			t.Fatalf("mutable action reference: %s", match[1])
		}
	}
}

func assertWorkflowToolMatchesLock(t *testing.T, workflow, name, version, checksum string) {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "build", "release", "toolchain.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var lock struct {
		Tools []struct {
			Name      string            `json:"name"`
			Version   string            `json:"version"`
			Checksums map[string]string `json:"checksums"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(contents, &lock); err != nil {
		t.Fatal(err)
	}
	for _, tool := range lock.Tools {
		if tool.Name != name {
			continue
		}
		if tool.Version != version || tool.Checksums["linux-amd64"] != checksum {
			t.Fatalf("workflow %s pin differs from toolchain lock", name)
		}
		for _, required := range []string{version, checksum} {
			if !strings.Contains(workflow, required) {
				t.Errorf("workflow lacks locked %s value %q", name, required)
			}
		}
		return
	}
	t.Fatalf("toolchain lock lacks %s", name)
}
