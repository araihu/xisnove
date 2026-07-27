package contract_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const rootModule = "github.com/araihu/xisnove"

func TestReleaseWorkspaceClosesOverCurrentCheckout(t *testing.T) {
	root := repositoryRoot(t)
	var workspace struct {
		Use []struct {
			DiskPath string
		}
	}
	workspacePath := filepath.Join(root, "go.work")
	decodeJSON(t, run(t, root, []string{"GOWORK=" + workspacePath}, "go", "work", "edit", "-json"), &workspace)

	want := map[string]bool{
		filepath.Clean(root):            false,
		filepath.Join(root, "agent"):    false,
		filepath.Join(root, "cli"):      false,
		filepath.Join(root, "operator"): false,
		filepath.Join(root, "ui"):       false,
	}
	for _, use := range workspace.Use {
		path := use.DiskPath
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		if _, ok := want[path]; ok {
			want[path] = true
		}
	}
	for path, found := range want {
		if !found {
			t.Errorf("release workspace missing %s", path)
		}
	}

	for _, module := range []string{"cli", "operator", "ui"} {
		module := module
		t.Run(module, func(t *testing.T) {
			var resolved struct {
				Path string
				Dir  string
			}
			output := run(t, filepath.Join(root, module), []string{"GOWORK=" + workspacePath}, "go", "list", "-m", "-json", rootModule)
			decodeJSON(t, output, &resolved)
			if resolved.Path != rootModule || filepath.Clean(resolved.Dir) != filepath.Clean(root) {
				t.Fatalf("%s resolves root module to path=%q dir=%q, want current checkout %q", module, resolved.Path, resolved.Dir, root)
			}
			run(t, filepath.Join(root, module), []string{"GOWORK=" + workspacePath}, "go", "test", "-run", "^$", "./...")
		})
	}
}

func TestGeneratedClientsNameCanonicalOpenAPIInput(t *testing.T) {
	root := repositoryRoot(t)
	for path, needle := range map[string]string{
		"sdk/generate.go":                         "../api/openapi.yaml",
		"agent/internal/controlplane/generate.go": "../../../api/openapi.yaml",
	} {
		content := read(t, filepath.Join(root, path))
		if !strings.Contains(content, "go:generate go tool oapi-codegen") || !strings.Contains(content, needle) {
			t.Errorf("%s does not generate from canonical %s", path, needle)
		}
	}
}

func TestCIUsesImmutableLeastPrivilegeInputs(t *testing.T) {
	root := repositoryRoot(t)
	lock := readToolchainManifest(t, root)
	actionPins := make(map[string]string, len(lock.Actions))
	for _, pin := range lock.Actions {
		actionPins[pin.Name] = pin.SHA
	}
	goVersion := ""
	for _, pin := range lock.Tools {
		if pin.Name == "go" {
			goVersion = pin.Version
		}
	}
	if goVersion == "" {
		t.Fatal("release toolchain lock has no Go version")
	}
	databaseService := ""
	lockedImages := map[string]bool{}
	for _, pin := range lock.Images {
		lockedImages[pin.Name+"@"+pin.Digest] = true
		if pin.Use == "database-service" {
			databaseService = pin.Name + "@" + pin.Digest
		}
	}
	if databaseService == "" {
		t.Fatal("release toolchain lock has no database-service image")
	}
	usedActions := make(map[string]bool, len(actionPins))
	usedDatabaseService := false
	shaAction := regexp.MustCompile(`uses:\s*[^\s@]+@[0-9a-f]{40}(?:\s|$)`)
	mutableAction := regexp.MustCompile(`uses:\s*[^\s@]+@[^\s#]+`)
	actionReference := regexp.MustCompile(`uses:\s*([^\s@]+)@([0-9a-f]{40})(?:\s|$)`)
	serviceReference := regexp.MustCompile(`(?m)^\s*image:\s*([^\s]+)\s*$`)

	for _, path := range []string{".github/workflows/ci.yml", ".github/workflows/turso-conformance.yml"} {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			content := read(t, filepath.Join(root, path))
			if !strings.Contains(content, "build/release/toolchain.lock.json") {
				t.Error("workflow does not consume the release toolchain lock")
			}
			for _, forbidden := range []string{
				"go install sigs.k8s.io/kind@",
				"go install helm.sh/helm",
				"go install gotest.tools/gotestsum@",
				"kubectl.sha256",
			} {
				if strings.Contains(content, forbidden) {
					t.Errorf("workflow bypasses the release toolchain lock with %q", forbidden)
				}
			}
			if !strings.Contains(content, "permissions:\n  contents: read") {
				t.Error("workflow lacks top-level read-only contents permission")
			}
			lines := strings.Split(content, "\n")
			for index, line := range lines {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "uses:") || strings.HasPrefix(trimmed, "- uses:") {
					if mutableAction.MatchString(trimmed) && !shaAction.MatchString(trimmed) {
						t.Errorf("mutable action reference at line %d: %s", index+1, trimmed)
					}
					match := actionReference.FindStringSubmatch(trimmed)
					if len(match) == 3 {
						want, ok := actionPins[match[1]]
						if !ok {
							t.Errorf("action %q at line %d is absent from the release toolchain lock", match[1], index+1)
						} else if match[2] != want {
							t.Errorf("action %q SHA at line %d = %s, want lock %s", match[1], index+1, match[2], want)
						} else {
							usedActions[match[1]] = true
						}
					}
				}
				if strings.Contains(trimmed, "uses: actions/checkout@") {
					end := min(index+8, len(lines))
					if !strings.Contains(strings.Join(lines[index:end], "\n"), "persist-credentials: false") {
						t.Errorf("checkout at line %d retains credentials", index+1)
					}
				}
				if strings.HasPrefix(trimmed, "runs-on:") && strings.Contains(trimmed, "latest") {
					t.Errorf("mutable runner label at line %d: %s", index+1, trimmed)
				}
				if strings.HasPrefix(trimmed, "go-version:") && !strings.Contains(trimmed, `"`+goVersion+`"`) {
					t.Errorf("Go toolchain drift at line %d: %s; want lock %s", index+1, trimmed, goVersion)
				}
			}
			for _, match := range serviceReference.FindAllStringSubmatch(content, -1) {
				if !lockedImages[match[1]] {
					t.Errorf("workflow image %s is absent from the release lock", match[1])
				} else if match[1] == databaseService {
					usedDatabaseService = true
				}
			}
		})
	}
	for name := range actionPins {
		if !usedActions[name] {
			t.Errorf("release-lock action %q is not used by checked workflows", name)
		}
	}
	if !usedDatabaseService {
		t.Error("release-lock database-service image is not used by checked workflows")
	}
}

func TestImageRuntimeFixturesUseLockedImages(t *testing.T) {
	root := repositoryRoot(t)
	lock := readToolchainManifest(t, root)
	want := map[string]string{}
	for _, image := range lock.Images {
		if image.Use == "runtime-base" || image.Use == "database-service" {
			want[image.Use] = image.Name + "@" + image.Digest
		}
	}
	content := read(t, filepath.Join(root, "integration/distribution/images/runtime_test.go"))
	for _, use := range []string{"runtime-base", "database-service"} {
		image := want[use]
		if image == "" {
			t.Fatalf("release toolchain lock has no %s image", use)
		}
		if !strings.Contains(content, image) {
			t.Errorf("image runtime fixture does not use locked %s image %q", use, image)
		}
	}
	for _, mutable := range []string{`"ubuntu:22.04"`, `"postgres:18-alpine"`} {
		if strings.Contains(content, mutable) {
			t.Errorf("image runtime fixture contains mutable image reference %s", mutable)
		}
	}
}

func TestM62DistributionGatesAreWired(t *testing.T) {
	root := repositoryRoot(t)
	makefile := read(t, filepath.Join(root, "Makefile"))
	for _, required := range []string{
		"distribution-image-native-check:",
		"distribution-image-oci-check:",
		"distribution-helm-check:",
		"distribution-deploy-check:",
		"distribution-check:",
		"docker buildx bake test-$(DISTRIBUTION_ARCH) --load",
		"docker buildx bake oci-layout",
		"go test -race ./integration/distribution/images",
		"helm lint charts/xisnove",
		"go test -race ./integration/distribution/helm",
		"systemd-analyze verify deploy/systemd/*.service",
		"shellcheck deploy/raw/*.sh deploy/compose/bootstrap.sh",
		"go test -race ./integration/distribution/deploy",
	} {
		if !strings.Contains(makefile, required) {
			t.Errorf("Makefile missing M6.2 gate %q", required)
		}
	}

	workflow := read(t, filepath.Join(root, ".github/workflows/ci.yml"))
	for _, required := range []string{
		"distribution:\n",
		"make distribution-image-native-check",
		"make distribution-image-oci-check",
		"make distribution-helm-check",
		"make distribution-deploy-check",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI workflow missing M6.2 gate %q", required)
		}
	}
	lock := readToolchainManifest(t, root)
	qemuAction := ""
	buildxAction := ""
	for _, action := range lock.Actions {
		if action.Name == "docker/setup-qemu-action" {
			qemuAction = action.Name + "@" + action.SHA
		}
		if action.Name == "docker/setup-buildx-action" {
			buildxAction = action.Name + "@" + action.SHA
		}
	}
	binfmtImage := ""
	for _, image := range lock.Images {
		if image.Use == "test-emulation" {
			binfmtImage = image.Name + "@" + image.Digest
		}
	}
	for label, required := range map[string]string{"QEMU action": qemuAction, "Buildx action": buildxAction, "binfmt image": binfmtImage} {
		if required == "" {
			t.Errorf("release toolchain lock has no %s", label)
		} else if !strings.Contains(workflow, required) {
			t.Errorf("CI workflow does not consume locked %s %q", label, required)
		}
	}
	if !strings.Contains(workflow, "driver: docker-container") {
		t.Error("CI workflow does not select the docker-container Buildx driver required by OCI export")
	}
}

func TestDockerBuildInputsArePinnedAndSecretFree(t *testing.T) {
	root := repositoryRoot(t)
	ignore := read(t, filepath.Join(root, ".dockerignore"))
	for _, excluded := range []string{"deploy/compose/secrets", "deploy/compose/.bootstrap-state"} {
		if !strings.Contains(ignore, excluded) {
			t.Errorf(".dockerignore does not exclude bootstrap runtime path %q", excluded)
		}
	}
	gitignore := read(t, filepath.Join(root, ".gitignore"))
	for _, excluded := range []string{"deploy/compose/secrets", "deploy/compose/.bootstrap-state"} {
		if !strings.Contains(gitignore, excluded) {
			t.Errorf(".gitignore does not exclude bootstrap runtime path %q", excluded)
		}
	}

	lock := readToolchainManifest(t, root)
	frontend := ""
	for _, image := range lock.Images {
		if image.Use == "dockerfile-frontend" {
			frontend = image.Name + "@" + image.Digest
		}
	}
	if frontend == "" {
		t.Fatal("release toolchain lock has no Dockerfile frontend image")
	}
	for _, name := range []string{"server", "ui", "agent", "operator"} {
		path := filepath.Join(root, "build/package/Dockerfile."+name)
		first, _, _ := strings.Cut(read(t, path), "\n")
		if first != "# syntax="+frontend {
			t.Errorf("%s frontend = %q, want pinned %q", path, first, frontend)
		}
	}
}

func readToolchainManifest(t *testing.T, root string) toolchainManifest {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(root, "build", "release", "toolchain.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest toolchainManifest
	decodeJSON(t, contents, &manifest)
	return manifest
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate source file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}

func read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func run(t *testing.T, dir string, environment []string, name string, arguments ...string) []byte {
	t.Helper()
	command := exec.Command(name, arguments...)
	command.Dir = dir
	command.Env = append(os.Environ(), environment...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(arguments, " "), err, output)
	}
	return output
}

func decodeJSON(t *testing.T, content []byte, destination any) {
	t.Helper()
	if err := json.Unmarshal(content, destination); err != nil {
		t.Fatalf("decode JSON: %v\n%s", err, content)
	}
}
