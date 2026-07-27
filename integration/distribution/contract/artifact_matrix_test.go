package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"testing"
)

type releaseManifest struct {
	SchemaVersion int `json:"schema_version"`
	Version       struct {
		Source    string `json:"source"`
		Pattern   string `json:"pattern"`
		Value     string `json:"value"`
		Reference string `json:"reference"`
	} `json:"version"`
	Binaries []struct {
		Name       string   `json:"name"`
		Entrypoint string   `json:"entrypoint"`
		Targets    []string `json:"targets"`
		VersionRef string   `json:"version_ref"`
	} `json:"binaries"`
	Images []struct {
		Name       string   `json:"name"`
		Repository string   `json:"repository"`
		Targets    []string `json:"targets"`
		VersionRef string   `json:"version_ref"`
	} `json:"images"`
	Charts []struct {
		Name       string `json:"name"`
		Repository string `json:"repository"`
		VersionRef string `json:"version_ref"`
	} `json:"charts"`
}

func TestArtifactManifestFreezesReleaseMatrix(t *testing.T) {
	manifest := readReleaseManifest(t)
	if manifest.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", manifest.SchemaVersion)
	}
	if manifest.Version.Source != "git-tag" || manifest.Version.Pattern != "vX.Y.Z[-<identifier>]" {
		t.Fatalf("version = %#v, want one stable-or-prerelease git-tag source", manifest.Version)
	}
	if manifest.Version.Value != "X.Y.Z[-<identifier>]" || manifest.Version.Reference != "release.version" {
		t.Fatalf("version = %#v, want prerelease-preserving canonical release.version", manifest.Version)
	}

	wantBinaries := map[string][]string{
		"xisnove-server":   {"linux-amd64-glibc2.35", "linux-arm64-glibc2.35"},
		"xisnove-ui":       {"linux-amd64", "linux-arm64"},
		"xisnove-agent":    {"linux-amd64", "linux-arm64"},
		"xisnove-operator": {"linux-amd64", "linux-arm64"},
		"xisnove":          {"darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64", "windows-amd64", "windows-arm64"},
	}
	gotBinaries := make(map[string][]string, len(manifest.Binaries))
	for _, artifact := range manifest.Binaries {
		if _, exists := gotBinaries[artifact.Name]; exists {
			t.Errorf("duplicate binary artifact %q", artifact.Name)
			continue
		}
		if artifact.Entrypoint == "" {
			t.Errorf("binary %q has no entrypoint", artifact.Name)
		}
		assertVersionRef(t, artifact.Name, artifact.VersionRef)
		gotBinaries[artifact.Name] = sorted(artifact.Targets)
	}
	if !reflect.DeepEqual(gotBinaries, mapSorted(wantBinaries)) {
		t.Fatalf("binary matrix = %#v, want %#v", gotBinaries, wantBinaries)
	}

	wantImages := map[string][]string{
		"xisnove-server":   {"linux-amd64", "linux-arm64"},
		"xisnove-ui":       {"linux-amd64", "linux-arm64"},
		"xisnove-agent":    {"linux-amd64", "linux-arm64"},
		"xisnove-operator": {"linux-amd64", "linux-arm64"},
	}
	gotImages := make(map[string][]string, len(manifest.Images))
	for _, artifact := range manifest.Images {
		if _, exists := gotImages[artifact.Name]; exists {
			t.Errorf("duplicate image artifact %q", artifact.Name)
			continue
		}
		if artifact.Repository == "" {
			t.Errorf("image %q has no repository", artifact.Name)
		}
		assertVersionRef(t, artifact.Name, artifact.VersionRef)
		gotImages[artifact.Name] = sorted(artifact.Targets)
	}
	if !reflect.DeepEqual(gotImages, mapSorted(wantImages)) {
		t.Fatalf("image matrix = %#v, want %#v", gotImages, wantImages)
	}

	wantCharts := map[string]string{
		"xisnove":      "oci://ghcr.io/araihu/charts/xisnove",
		"xisnove-edge": "oci://ghcr.io/araihu/charts/xisnove-edge",
	}
	gotCharts := make(map[string]string, len(manifest.Charts))
	for _, artifact := range manifest.Charts {
		if _, exists := gotCharts[artifact.Name]; exists {
			t.Errorf("duplicate chart artifact %q", artifact.Name)
			continue
		}
		assertVersionRef(t, artifact.Name, artifact.VersionRef)
		gotCharts[artifact.Name] = artifact.Repository
	}
	if !reflect.DeepEqual(gotCharts, wantCharts) {
		t.Fatalf("chart matrix = %#v, want %#v", gotCharts, wantCharts)
	}
}

func readReleaseManifest(t *testing.T) releaseManifest {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(distributionRoot(t), "build", "release", "artifacts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest releaseManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func assertVersionRef(t *testing.T, name, ref string) {
	t.Helper()
	if ref != "release.version" {
		t.Errorf("artifact %q version_ref = %q, want release.version", name, ref)
	}
}

func sorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}

func mapSorted(values map[string][]string) map[string][]string {
	result := make(map[string][]string, len(values))
	for key, value := range values {
		result[key] = sorted(value)
	}
	return result
}

func distributionRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
