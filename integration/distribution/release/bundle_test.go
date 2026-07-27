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
	mustWriteFile(t, filepath.Join(candidate, "oci", "chart-manifest.json"), "chart", 0o644)
	mustWriteFile(t, filepath.Join(candidate, "oci", "index.json"), "index", 0o644)
	plan := filepath.Join(candidate, "subjects.json")
	mustWriteFile(t, plan, `[
  {"kind":"sbom","name":"server-sbom","locator":"sboms/server.spdx.json","path":"sboms/server.spdx.json","mediaType":"application/spdx+json"},
  {"kind":"metadata","name":"licenses","locator":"metadata/licenses.json","path":"metadata/licenses.json","mediaType":"application/json"},
  {"kind":"metadata","name":"toolchain-lock","locator":"metadata/toolchain.lock.json","path":"metadata/toolchain.lock.json","mediaType":"application/json"},
  {"kind":"oci-manifest","name":"chart","locator":"oci/chart-manifest.json","path":"oci/chart-manifest.json","mediaType":"application/vnd.oci.image.manifest.v1+json"},
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
	wantOrder := []string{"archive/server/", "metadata/licenses/", "metadata/toolchain-lock/", "oci-index/server/", "oci-manifest/chart/", "sbom/server-sbom/"}
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
	subjectDigest := strings.Repeat("b", 64)
	mustWriteFile(t, raw, `{"spdxVersion":"SPDX-2.3","documentNamespace":"urn:uuid:random","creationInfo":{"created":"2020-01-01T00:00:00Z","creators":["Tool: syft"]},"packages":[{"name":"allowed","versionInfo":"1.0.0","licenseDeclared":"MIT","licenseConcluded":"MIT"},{"name":"github.com/araihu/xisnove/agent","versionInfo":"UNKNOWN","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/github.com/araihu/xisnove/agent"}],"licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"},{"name":"github.com/araihu/xisnove-evil","versionInfo":"UNKNOWN","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/github.com/araihu/xisnove-evil"}],"licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"},{"name":"candidate-root","checksums":[{"algorithm":"SHA256","checksumValue":"`+subjectDigest+`"}],"licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"},{"name":"mystery","versionInfo":"2.0.0","licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"}]}`, 0o644)
	runTool(t, tool, "normalize-sbom", "--input", raw, "--output", normalized, "--subject-sha256", subjectDigest, "--source-date-epoch", releaseEpoch)
	contents := string(mustReadFile(t, normalized))
	if !strings.Contains(contents, `"created":"2023-11-14T22:13:20Z"`) || !strings.Contains(contents, `"documentNamespace":"https://xisnove.dev/sbom/`+strings.Repeat("b", 64)+`"`) {
		t.Fatalf("SBOM identity not normalized: %s", contents)
	}
	var normalizedDocument struct {
		Packages []struct {
			Name      string `json:"name"`
			Declared  string `json:"licenseDeclared"`
			Concluded string `json:"licenseConcluded"`
		} `json:"packages"`
	}
	if err := json.Unmarshal([]byte(contents), &normalizedDocument); err != nil {
		t.Fatal(err)
	}
	licenses := map[string]string{}
	for _, pkg := range normalizedDocument.Packages {
		licenses[pkg.Name] = pkg.Declared + "/" + pkg.Concluded
	}
	for _, name := range []string{"github.com/araihu/xisnove/agent", "candidate-root"} {
		if licenses[name] != "Apache-2.0/Apache-2.0" {
			t.Errorf("first-party license for %s = %q", name, licenses[name])
		}
	}
	if licenses["mystery"] != "NOASSERTION/NOASSERTION" {
		t.Fatalf("third-party unknown license was weakened: %q", licenses["mystery"])
	}
	if licenses["github.com/araihu/xisnove-evil"] != "NOASSERTION/NOASSERTION" {
		t.Fatalf("spoofed first-party prefix was licensed: %q", licenses["github.com/araihu/xisnove-evil"])
	}

	policy := filepath.Join(root, "policy.json")
	mustWriteFile(t, policy, `{"schemaVersion":2,"default":{"allow":["MIT"],"deny":["AGPL-3.0-only"]},"golang":{"allow":["MIT"],"deny":["AGPL-3.0-only"]},"ubuntu":{"distro":"ubuntu-22.04","snapshot":"20260701T000000Z","lock":"ubuntu-lock.json"}}`, 0o644)
	mustWriteFile(t, filepath.Join(root, "ubuntu-lock.json"), `{"schemaVersion":1,"packages":[]}`, 0o644)
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

func TestLicensePolicyV2ScopesGoAndUbuntuDecisions(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	policy := filepath.Join(root, "policy.json")
	lock := filepath.Join(root, "ubuntu-lock.json")
	evidence := filepath.Join(root, "evidence", "apache.txt")
	mustWriteFile(t, evidence, "reviewed Apache evidence\n", 0o644)
	evidenceDigest := sha256.Sum256(mustReadFile(t, evidence))
	mustWriteFile(t, policy, `{
  "schemaVersion": 2,
  "globalDeny":["AGPL-3.0-only","SSPL-1.0"],
  "default": {"allow":["Apache-2.0","MIT"],"deny":["AGPL-3.0-only","SSPL-1.0"]},
  "golang": {
    "allow":["Apache-2.0","BSD-3-Clause","MIT"],
    "deny":["AGPL-3.0-only","GPL-3.0-only","SSPL-1.0"],
    "overrides":[{"purl":"pkg:golang/example.invalid/reviewed@v1.0.0","reportedLicense":"Apache-2.0 AND LicenseRef-reviewed","resolvedLicense":"Apache-2.0","evidenceFile":"evidence/apache.txt","evidenceSHA256":"`+hex.EncodeToString(evidenceDigest[:])+`","obligations":["retain-license-notice"]},{"purl":"pkg:golang/example.invalid/badresolved@v1.0.0","reportedLicense":"LicenseRef-reviewed","resolvedLicense":"AGPL-3.0-only","evidenceFile":"evidence/apache.txt","evidenceSHA256":"`+hex.EncodeToString(evidenceDigest[:])+`","obligations":["retain-license-notice"]}]
  },
  "ubuntu":{"distro":"ubuntu-22.04","snapshot":"20260701T000000Z","lock":"ubuntu-lock.json"}
}`, 0o644)
	mustWriteFile(t, lock, `{"schemaVersion":1,"packages":[{"purl":"pkg:deb/ubuntu/apt@2.4.14?arch=arm64&distro=ubuntu-22.04","packageVerificationCode":"abc123","reportedLicense":"GPL-2.0-or-later","resolvedLicense":"GPL-2.0-or-later","evidenceSHA256":"`+strings.Repeat("a", 64)+`"},{"purl":"pkg:deb/ubuntu/adduser@3.118ubuntu5?arch=all&distro=ubuntu-22.04","packageVerificationCode":"def456","reportedLicense":"LicenseRef-reviewed-adduser","resolvedLicense":"LicenseRef-reviewed-adduser","evidenceSHA256":"`+strings.Repeat("b", 64)+`"},{"purl":"pkg:deb/ubuntu/base-passwd@3.5.52build1?arch=arm64&distro=ubuntu-22.04","packageVerificationCode":"ghi789","reportedLicense":"NOASSERTION","resolvedLicense":"LicenseRef-Ubuntu-base-passwd","evidenceSHA256":"`+strings.Repeat("c", 64)+`"}]}`, 0o644)

	good := filepath.Join(root, "good.spdx.json")
	mustWriteFile(t, good, `{"spdxVersion":"SPDX-2.3","packages":[
{"name":"go-composite","versionInfo":"v1.0.0","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/composite@v1.0.0"}],"licenseDeclared":"Apache-2.0 AND MIT","licenseConcluded":"Apache-2.0 AND MIT"},
{"name":"go-reviewed","versionInfo":"v1.0.0","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/reviewed@v1.0.0"}],"licenseDeclared":"Apache-2.0 AND LicenseRef-reviewed","licenseConcluded":"Apache-2.0 AND LicenseRef-reviewed"},
{"name":"apt","versionInfo":"2.4.14","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/apt@2.4.14?arch=arm64&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"abc123"},"licenseDeclared":"GPL-2.0-or-later","licenseConcluded":"GPL-2.0-or-later"},
{"name":"adduser","versionInfo":"3.118ubuntu5","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/adduser@3.118ubuntu5?arch=all&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"def456"},"licenseDeclared":"NOASSERTION","licenseConcluded":"LicenseRef-reviewed-adduser"},
{"name":"base-passwd","versionInfo":"3.5.52build1","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/base-passwd@3.5.52build1?arch=arm64&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"ghi789"},"licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"}
]}`, 0o644)
	inventory := filepath.Join(root, "licenses.json")
	runTool(t, tool, "licenses", "--sbom", good, "--policy", policy, "--output", inventory)
	contents := string(mustReadFile(t, inventory))
	for _, rule := range []string{"golang-expression", "golang-override", "ubuntu-lock"} {
		if !strings.Contains(contents, `"rule":"`+rule+`"`) {
			t.Errorf("inventory missing decision rule %q: %s", rule, contents)
		}
	}
	if !strings.Contains(contents, `"reportedLicense":"Apache-2.0 AND LicenseRef-reviewed"`) || !strings.Contains(contents, `"license":"Apache-2.0"`) || !strings.Contains(contents, `"obligations":["retain-license-notice"]`) {
		t.Fatalf("Go override did not preserve resolution evidence: %s", contents)
	}
	if !strings.Contains(contents, `"license":"LicenseRef-Ubuntu-base-passwd"`) || !strings.Contains(contents, `"reportedLicense":"NOASSERTION"`) {
		t.Fatalf("Ubuntu lock did not preserve raw and resolved license: %s", contents)
	}

	badCases := []struct {
		name    string
		pkg     string
		message string
	}{
		{"go denied", `{"name":"copyleft-go","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/copyleft@v1.0.0"}],"licenseDeclared":"GPL-3.0-only","licenseConcluded":"GPL-3.0-only"}`, "denied license"},
		{"go unknown", `{"name":"unknown-go","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/unknown@v1.0.0"}],"licenseDeclared":"LicenseRef-missing","licenseConcluded":"LicenseRef-missing"}`, "unknown license"},
		{"resolved globally denied", `{"name":"bad-resolved","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/badresolved@v1.0.0"}],"licenseDeclared":"LicenseRef-reviewed","licenseConcluded":"LicenseRef-reviewed"}`, "denied resolved license"},
		{"ubuntu drift", `{"name":"apt","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/apt@2.4.14?arch=arm64&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"changed"},"licenseDeclared":"GPL-2.0-or-later","licenseConcluded":"GPL-2.0-or-later"}`, "Ubuntu lock mismatch"},
		{"unlocked Ubuntu", `{"name":"new-deb","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/new-deb@1?arch=arm64&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"new"},"licenseDeclared":"MIT","licenseConcluded":"MIT"}`, "Ubuntu package not locked"},
		{"ambiguous purl", `{"name":"ambiguous","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/a@v1"},{"referenceType":"purl","referenceLocator":"pkg:golang/example.invalid/b@v1"}],"licenseDeclared":"MIT","licenseConcluded":"MIT"}`, "exactly one purl"},
	}
	for _, test := range badCases {
		t.Run(test.name, func(t *testing.T) {
			sbom := filepath.Join(t.TempDir(), "bad.spdx.json")
			mustWriteFile(t, sbom, `{"spdxVersion":"SPDX-2.3","packages":[`+test.pkg+`]}`, 0o644)
			cmd := exec.Command(tool, "licenses", "--sbom", sbom, "--policy", policy, "--output", filepath.Join(t.TempDir(), "out.json"))
			output, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(output), test.message) {
				t.Fatalf("want %q failure: err=%v output=%s", test.message, err, output)
			}
		})
	}
}

func TestUbuntuLicenseLockProposalIsDeterministicAndFailClosed(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	sbom := filepath.Join(root, "runtime.spdx.json")
	mustWriteFile(t, sbom, `{"spdxVersion":"SPDX-2.3","packages":[
{"name":"base-passwd","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/base-passwd@1?arch=arm64&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"code-b"},"licenseDeclared":"NOASSERTION","licenseConcluded":"NOASSERTION"},
{"name":"apt","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/apt@1?arch=arm64&distro=ubuntu-22.04"}],"packageVerificationCode":{"packageVerificationCodeValue":"code-a"},"licenseDeclared":"GPL-2.0-or-later","licenseConcluded":"GPL-2.0-or-later"}
]}`, 0o644)
	one := filepath.Join(root, "one.json")
	two := filepath.Join(root, "two.json")
	for _, output := range []string{one, two} {
		runTool(t, tool, "propose-ubuntu-lock", "--sbom", sbom, "--output", output)
	}
	if !reflect.DeepEqual(mustReadFile(t, one), mustReadFile(t, two)) {
		t.Fatal("same Ubuntu SBOM produced different lock bytes")
	}
	contents := string(mustReadFile(t, one))
	for _, required := range []string{
		`"reportedLicense":"NOASSERTION"`,
		`"resolvedLicense":"LicenseRef-Ubuntu-`,
		`"reportedLicense":"GPL-2.0-or-later"`,
		`"obligations":["provide-corresponding-source-reference","retain-license-notice"]`,
	} {
		if !strings.Contains(contents, required) {
			t.Errorf("Ubuntu lock proposal missing %s: %s", required, contents)
		}
	}

	bad := filepath.Join(root, "bad.spdx.json")
	mustWriteFile(t, bad, `{"spdxVersion":"SPDX-2.3","packages":[{"name":"missing-code","externalRefs":[{"referenceType":"purl","referenceLocator":"pkg:deb/ubuntu/missing@1?arch=arm64&distro=ubuntu-22.04"}],"licenseDeclared":"MIT","licenseConcluded":"MIT"}]}`, 0o644)
	cmd := exec.Command(tool, "propose-ubuntu-lock", "--sbom", bad, "--output", filepath.Join(root, "bad.json"))
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "packageVerificationCode") {
		t.Fatalf("missing Ubuntu evidence must fail: err=%v output=%s", err, output)
	}
}

func TestSBOMAndLicenseScriptsUseExplicitToolsAndInputs(t *testing.T) {
	tool := buildReleaseBundleTool(t)
	root := t.TempDir()
	artifact := filepath.Join(root, "server.tar.gz")
	mustWriteFile(t, artifact, "artifact", 0o644)
	fakeSyft := filepath.Join(root, "fake-syft")
	capture := filepath.Join(root, "syft.log")
	mustWriteFile(t, fakeSyft, `#!/bin/sh
set -eu
printf '%s\n' "$*" > "$CAPTURE"
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
	runCommandIn(t, root, []string{"RELEASEBUNDLE_BIN=" + tool, "SYFT_BIN=" + fakeSyft, "CAPTURE=" + capture}, generate, "--output-dir", sboms, "--source-date-epoch", releaseEpoch, artifact)
	if args := string(mustReadFile(t, capture)); !strings.Contains(args, "--enrich golang") {
		t.Fatalf("Syft Go license enrichment not explicit: %s", args)
	}
	generated := filepath.Join(sboms, "server.tar.gz.spdx.json")
	if _, err := os.Stat(generated); err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "policy.json")
	mustWriteFile(t, policy, `{"schemaVersion":2,"default":{"allow":["MIT"],"deny":["AGPL-3.0-only"]},"golang":{"allow":["MIT"],"deny":["AGPL-3.0-only"]},"ubuntu":{"distro":"ubuntu-22.04","snapshot":"20260701T000000Z","lock":"ubuntu-lock.json"}}`, 0o644)
	mustWriteFile(t, filepath.Join(root, "ubuntu-lock.json"), `{"schemaVersion":1,"packages":[]}`, 0o644)
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
