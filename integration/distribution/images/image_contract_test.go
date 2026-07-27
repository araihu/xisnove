package images_test

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type imageInspect struct {
	Architecture string `json:"Architecture"`
	Config       struct {
		User        string            `json:"User"`
		Env         []string          `json:"Env"`
		Entrypoint  []string          `json:"Entrypoint"`
		Labels      map[string]string `json:"Labels"`
		Volumes     map[string]any    `json:"Volumes"`
		Healthcheck *struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
	} `json:"Config"`
}

func TestNativeImagesHonorRuntimeAndSupplyChainContract(t *testing.T) {
	native := dockerArchitecture(t)
	if native != runtime.GOARCH {
		t.Fatalf("Docker architecture %q differs from Go test architecture %q; native evidence required", native, runtime.GOARCH)
	}

	images := map[string]string{
		"server":   "/usr/local/bin/xisnove-server",
		"ui":       "/usr/local/bin/xisnove-ui",
		"agent":    "/usr/local/bin/xisnove-agent",
		"operator": "/usr/local/bin/xisnove-operator",
	}
	for name, entrypoint := range images {
		t.Run(name, func(t *testing.T) {
			image := fmt.Sprintf("xisnove-%s:test-%s", name, runtime.GOARCH)
			inspect := inspectImage(t, image)
			if inspect.Architecture != runtime.GOARCH {
				t.Fatalf("architecture = %q, want %q", inspect.Architecture, runtime.GOARCH)
			}
			if inspect.Config.User != "101:101" {
				t.Fatalf("user = %q, want numeric 101:101", inspect.Config.User)
			}
			if !slices.Equal(inspect.Config.Entrypoint, []string{entrypoint}) {
				t.Fatalf("entrypoint = %q, want %q", inspect.Config.Entrypoint, entrypoint)
			}
			for label, want := range map[string]string{
				"org.opencontainers.image.source":   "https://github.com/araihu/xisnove",
				"org.opencontainers.image.version":  "0.0.0",
				"org.opencontainers.image.revision": strings.Repeat("0", 40),
				"org.opencontainers.image.created":  "1970-01-01T00:00:00Z",
				"org.opencontainers.image.licenses": "Apache-2.0",
			} {
				if got := inspect.Config.Labels[label]; got != want {
					t.Errorf("label %s = %q, want %q", label, got, want)
				}
			}
			for _, value := range inspect.Config.Env {
				upper := strings.ToUpper(value)
				if strings.Contains(upper, "SECRET=") || strings.Contains(upper, "TOKEN=") || strings.Contains(upper, "PASSWORD=") {
					t.Errorf("secret-shaped image environment: %q", value)
				}
			}
			if name == "server" {
				if len(inspect.Config.Volumes) != 1 || inspect.Config.Volumes["/var/lib/xisnove"] == nil {
					t.Fatalf("server volumes = %#v, want only /var/lib/xisnove", inspect.Config.Volumes)
				}
			} else if len(inspect.Config.Volumes) != 0 {
				t.Fatalf("%s volumes = %#v, want none", name, inspect.Config.Volumes)
			}
			if name == "operator" {
				if inspect.Config.Healthcheck != nil {
					t.Fatalf("operator must not define image healthcheck: %#v", inspect.Config.Healthcheck)
				}
			} else {
				if inspect.Config.Healthcheck == nil || len(inspect.Config.Healthcheck.Test) < 2 || inspect.Config.Healthcheck.Test[1] != "/usr/bin/wget" {
					t.Fatalf("healthcheck = %#v, want exec-form /usr/bin/wget", inspect.Config.Healthcheck)
				}
			}

			output := docker(t, "run", "--rm", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", image, "--version")
			if !strings.HasPrefix(strings.TrimSpace(output), "xisnove-") {
				t.Fatalf("version output = %q", output)
			}
			probeCheck := ""
			if name != "operator" {
				probeCheck = "test -x /usr/bin/wget;"
			}
			if name == "server" {
				probeCheck += " test -x /usr/bin/flock; /usr/bin/flock -n /var/lib/xisnove/xisnove-image-contract.lock true;"
			}
			files := docker(t, "run", "--rm", "--read-only", "--entrypoint", "/bin/sh", image, "-ec", probeCheck+" test -s /etc/ssl/certs/ca-certificates.crt; sha256sum /usr/share/licenses/xisnove/LICENSE /usr/share/licenses/xisnove/NOTICE; getconf GNU_LIBC_VERSION")
			for _, want := range []string{
				"cfc7749b96f63bd31c3c42b5c471bf756814053e847c10f3eb003417bc523d30  /usr/share/licenses/xisnove/LICENSE",
				"e3ad5f2f51b2365c85478db227c5fdf36e7f193cab97d132904af61765e4e7ba  /usr/share/licenses/xisnove/NOTICE",
				"glibc 2.35",
			} {
				if !strings.Contains(files, want) {
					t.Errorf("runtime evidence missing %q:\n%s", want, files)
				}
			}
			history := strings.ToLower(docker(t, "image", "history", "--no-trunc", "--format", "{{.CreatedBy}}", image))
			for _, forbidden := range []string{".env", "password=", "token=", "secret="} {
				if strings.Contains(history, forbidden) {
					t.Errorf("image history contains forbidden marker %q", forbidden)
				}
			}
		})
	}
}

func dockerArchitecture(t *testing.T) string {
	t.Helper()
	value := strings.TrimSpace(docker(t, "info", "--format", "{{.Architecture}}"))
	if value == "aarch64" {
		return "arm64"
	}
	if value == "x86_64" {
		return "amd64"
	}
	return value
}

func inspectImage(t *testing.T, image string) imageInspect {
	t.Helper()
	output := docker(t, "image", "inspect", image)
	var values []imageInspect
	if err := json.Unmarshal([]byte(output), &values); err != nil || len(values) != 1 {
		t.Fatalf("decode docker image inspect for %s: %v", image, err)
	}
	return values[0]
}

func docker(t *testing.T, arguments ...string) string {
	t.Helper()
	command := exec.Command("docker", arguments...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}
