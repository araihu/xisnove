package release_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type goreleaserConfig struct {
	Version int    `yaml:"version"`
	Dist    string `yaml:"dist"`
	Builds  []struct {
		ID      string   `yaml:"id"`
		Dir     string   `yaml:"dir"`
		Main    string   `yaml:"main"`
		Binary  string   `yaml:"binary"`
		Env     []string `yaml:"env"`
		Flags   []string `yaml:"flags"`
		LDFlags []string `yaml:"ldflags"`
		GOOS    []string `yaml:"goos"`
		GOARCH  []string `yaml:"goarch"`
		MTime   string   `yaml:"mod_timestamp"`
	} `yaml:"builds"`
	Archives []struct {
		ID         string   `yaml:"id"`
		IDs        []string `yaml:"ids"`
		Formats    []string `yaml:"formats"`
		Name       string   `yaml:"name_template"`
		BuildsInfo struct {
			Owner string `yaml:"owner"`
			Group string `yaml:"group"`
			Mode  uint32 `yaml:"mode"`
			MTime string `yaml:"mtime"`
		} `yaml:"builds_info"`
		Files []struct {
			Src  string `yaml:"src"`
			Info struct {
				Owner string `yaml:"owner"`
				Group string `yaml:"group"`
				Mode  uint32 `yaml:"mode"`
				MTime string `yaml:"mtime"`
			} `yaml:"info"`
		} `yaml:"files"`
	} `yaml:"archives"`
}

func TestBinaryGoReleaserMatrixAndMetadataContract(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), ".goreleaser.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var config goreleaserConfig
	if err := yaml.Unmarshal(contents, &config); err != nil {
		t.Fatal(err)
	}
	if config.Version != 2 {
		t.Fatalf("configuration version = %d, want 2", config.Version)
	}
	if config.Dist != "dist" {
		t.Fatalf("dist = %q, want stable direct-check default", config.Dist)
	}

	type target struct {
		dir, main, binary, buildinfo string
		goos, goarch                 []string
	}
	want := map[string]target{
		"xisnove-server":   {".", "./cmd/xisnove-server", "xisnove-server", "github.com/araihu/xisnove/internal/buildinfo", []string{"linux"}, []string{"amd64", "arm64"}},
		"xisnove-ui":       {"ui", "./cmd/server", "xisnove-ui", "github.com/araihu/xisnove/ui/internal/buildinfo", []string{"linux"}, []string{"amd64", "arm64"}},
		"xisnove-agent":    {"agent", "./cmd/xisnove-agent", "xisnove-agent", "github.com/araihu/xisnove/agent/internal/buildinfo", []string{"linux"}, []string{"amd64", "arm64"}},
		"xisnove-operator": {"operator", "./cmd/xisnove-operator", "xisnove-operator", "github.com/araihu/xisnove/operator/internal/buildinfo", []string{"linux"}, []string{"amd64", "arm64"}},
		"xisnove":          {"cli", "./cmd/xisnove", "xisnove", "github.com/araihu/xisnove/cli/internal/buildinfo", []string{"linux", "darwin", "windows"}, []string{"amd64", "arm64"}},
	}
	if len(config.Builds) != len(want) {
		t.Fatalf("build count = %d, want %d", len(config.Builds), len(want))
	}
	for _, build := range config.Builds {
		expected, ok := want[build.ID]
		if !ok {
			t.Errorf("unexpected build ID %q", build.ID)
			continue
		}
		if build.Dir != expected.dir || build.Main != expected.main || build.Binary != expected.binary {
			t.Errorf("build %q path = (%q, %q, %q), want (%q, %q, %q)", build.ID, build.Dir, build.Main, build.Binary, expected.dir, expected.main, expected.binary)
		}
		if !reflect.DeepEqual(build.GOOS, expected.goos) || !reflect.DeepEqual(build.GOARCH, expected.goarch) {
			t.Errorf("build %q matrix = %v/%v, want %v/%v", build.ID, build.GOOS, build.GOARCH, expected.goos, expected.goarch)
		}
		if !contains(build.Env, "CGO_ENABLED=0") || !contains(build.Flags, "-trimpath") {
			t.Errorf("build %q lacks CGO_ENABLED=0 or -trimpath", build.ID)
		}
		if build.MTime != "{{ .Env.SOURCE_DATE_EPOCH }}" {
			t.Errorf("build %q mod_timestamp = %q", build.ID, build.MTime)
		}
		joined := strings.Join(build.LDFlags, " ")
		for _, field := range []string{"Version={{ .Env.XISNOVE_RELEASE_VERSION }}", "Commit={{ .Env.XISNOVE_RELEASE_COMMIT }}", "BuildDate={{ .Env.XISNOVE_BUILD_DATE }}", "Dirty=false"} {
			if !strings.Contains(joined, expected.buildinfo+"."+field) {
				t.Errorf("build %q ldflags lack %s.%s", build.ID, expected.buildinfo, field)
			}
		}
	}

	if len(config.Archives) != len(want) {
		t.Fatalf("archive count = %d, want %d", len(config.Archives), len(want))
	}
	for _, archive := range config.Archives {
		if len(archive.IDs) != 1 || archive.IDs[0] != archive.ID {
			t.Errorf("archive %q ids = %v", archive.ID, archive.IDs)
		}
		if !reflect.DeepEqual(archive.Formats, []string{"tar.gz"}) {
			t.Errorf("archive %q formats = %v", archive.ID, archive.Formats)
		}
		if archive.BuildsInfo.Owner != "root" || archive.BuildsInfo.Group != "root" || archive.BuildsInfo.Mode != 0o755 || archive.BuildsInfo.MTime != "{{ .Env.XISNOVE_BUILD_DATE }}" {
			t.Errorf("archive %q binary metadata is not normalized", archive.ID)
		}
		if len(archive.Files) != 2 {
			t.Errorf("archive %q file count = %d, want LICENSE and NOTICE", archive.ID, len(archive.Files))
			continue
		}
		for index, name := range []string{"LICENSE", "NOTICE"} {
			file := archive.Files[index]
			if file.Src != name || file.Info.Owner != "root" || file.Info.Group != "root" || file.Info.Mode != 0o644 || file.Info.MTime != "{{ .Env.XISNOVE_BUILD_DATE }}" {
				t.Errorf("archive %q file %q metadata is not normalized", archive.ID, name)
			}
		}
	}
}

func TestBinaryBuildScriptDerivesStableReleaseMetadata(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell contract")
	}
	temporary := t.TempDir()
	capture := filepath.Join(temporary, "capture")
	fake := filepath.Join(temporary, "goreleaser")
	script := `#!/bin/sh
set -eu
config=
previous=
for argument in "$@"; do
  if [ "$previous" = --config ]; then config=$argument; fi
  previous=$argument
done
printf '%s\n' "$*" "$(awk '$1 == "dist:" { print; exit }' "$config")" "$XISNOVE_RELEASE_VERSION" "$XISNOVE_RELEASE_COMMIT" "$XISNOVE_BUILD_DATE" "$SOURCE_DATE_EPOCH" > "$CAPTURE"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	command := exec.Command("bash", filepath.Join(repositoryRoot(t), "scripts/release/build-binaries.sh"))
	command.Dir = repositoryRoot(t)
	command.Env = append(os.Environ(),
		"GORELEASER_BIN="+fake,
		"CAPTURE="+capture,
		"XISNOVE_RELEASE_OUTPUT=dist/candidate/archives",
		"XISNOVE_RELEASE_VERSION=1.2.3-rc.1",
		"XISNOVE_RELEASE_COMMIT=0123456789abcdef0123456789abcdef01234567",
		"SOURCE_DATE_EPOCH=1785121445",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build script: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(contents)), "\n")
	if len(lines) != 6 || !strings.HasPrefix(lines[0], "release --snapshot --clean --config ") || strings.Contains(lines[0], "--output") || lines[1] != `dist: "dist/candidate/archives"` || lines[2] != "1.2.3-rc.1" || lines[3] != "0123456789abcdef0123456789abcdef01234567" || lines[4] != "2026-07-27T03:04:05Z" || lines[5] != "1785121445" {
		t.Fatalf("captured invocation:\n%s", contents)
	}
}
