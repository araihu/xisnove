package images_test

import (
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestServerLocalProfilesRunReadOnlyAndTerminateGracefully(t *testing.T) {
	image := "xisnove-server:test-" + runtime.GOARCH
	for _, profile := range []string{"sqlite", "turso-local"} {
		t.Run(profile, func(t *testing.T) {
			data := createVolume(t, "xisnove-image-data")
			secrets := createVolume(t, "xisnove-image-secrets")
			docker(t, "run", "--rm", "--user", "0:0", "--mount", "type=volume,source="+secrets+",target=/secrets", "ubuntu:22.04", "/bin/sh", "-ec", `printf '01234567890123456789012345678901' > /secrets/cursor; chown 101:101 /secrets/cursor; chmod 0400 /secrets/cursor`)
			database := "/var/lib/xisnove/xisnove.db"
			if profile == "turso-local" {
				database = "/var/lib/xisnove/turso.db"
			}
			docker(t, "run", "--rm", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--mount", "type=volume,source="+data+",target=/var/lib/xisnove", image,
				"db", "migrate", "--phase=expand", "--database-profile="+profile, "--database-url="+database)

			name := uniqueName("xisnove-server-" + strings.ReplaceAll(profile, "-", ""))
			start := []string{"run", "-d", "--name", name, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--mount", "type=volume,source=" + data + ",target=/var/lib/xisnove", "--mount", "type=volume,source=" + secrets + ",target=/run/secrets,readonly", image,
				"serve", "--listen=0.0.0.0:8080", "--database-profile=" + profile, "--database-url=" + database, "--cursor-signing-key-file=/run/secrets/cursor"}
			docker(t, start...)
			registerContainerCleanup(t, name)
			waitContainerHealthy(t, name, 30*time.Second)
			docker(t, "exec", name, "/usr/bin/wget", "--quiet", "--spider", "--timeout=1", "http://127.0.0.1:8080/readyz")
			stopAndRequireCleanExit(t, name, 15*time.Second)
		})
	}
}

func TestServerPostgresProfileRunsReadOnlyAndTerminatesGracefully(t *testing.T) {
	image := "xisnove-server:test-" + runtime.GOARCH
	network := uniqueName("xisnove-image-network")
	docker(t, "network", "create", network)
	t.Cleanup(func() { _ = exec.Command("docker", "network", "rm", network).Run() })

	postgresName := uniqueName("xisnove-postgres")
	docker(t, "run", "-d", "--name", postgresName, "--network", network, "--network-alias", "postgres",
		"-e", "POSTGRES_USER=xisnove", "-e", "POSTGRES_PASSWORD=image-contract", "-e", "POSTGRES_DB=xisnove",
		"postgres:18-alpine")
	registerContainerCleanup(t, postgresName)
	waitForPostgres(t, postgresName, 30*time.Second)

	secrets := createVolume(t, "xisnove-image-secrets")
	docker(t, "run", "--rm", "--user", "0:0", "--mount", "type=volume,source="+secrets+",target=/secrets", "ubuntu:22.04", "/bin/sh", "-ec", `printf '01234567890123456789012345678901' > /secrets/cursor; chown 101:101 /secrets/cursor; chmod 0400 /secrets/cursor`)
	databaseURL := "postgres://xisnove:image-contract@postgres:5432/xisnove?sslmode=disable"
	docker(t, "run", "--rm", "--network", network, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", image,
		"db", "migrate", "--phase=expand", "--database-profile=postgres", "--database-url="+databaseURL)

	name := uniqueName("xisnove-server-postgres")
	docker(t, "run", "-d", "--name", name, "--network", network, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--mount", "type=volume,source="+secrets+",target=/run/secrets,readonly", image,
		"serve", "--listen=0.0.0.0:8080", "--database-profile=postgres", "--database-url="+databaseURL, "--cursor-signing-key-file=/run/secrets/cursor")
	registerContainerCleanup(t, name)
	waitContainerHealthy(t, name, 30*time.Second)
	docker(t, "exec", name, "/usr/bin/wget", "--quiet", "--spider", "--timeout=1", "http://127.0.0.1:8080/readyz")
	stopAndRequireCleanExit(t, name, 15*time.Second)
}

func TestUIAndAgentHealthchecksRunReadOnlyAndTerminateGracefully(t *testing.T) {
	t.Run("ui", func(t *testing.T) {
		name := uniqueName("xisnove-ui")
		docker(t, "run", "-d", "--name", name, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m",
			"-e", "XISNOVE_UI_DEV_FAKE=true", "-e", "XISNOVE_UI_DEV_ADMIN_EMAIL=admin@example.test", "-e", "XISNOVE_UI_DEV_ADMIN_PASSWORD=development-only", "-e", "XISNOVE_UI_DEV_SESSION=development-session", "-e", "XISNOVE_UI_COOKIE_SECRET=QUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUFBQUE=", "-e", "XISNOVE_UI_COOKIE_SECURE=false",
			"xisnove-ui:test-"+runtime.GOARCH)
		registerContainerCleanup(t, name)
		waitContainerHealthy(t, name, 20*time.Second)
		docker(t, "exec", name, "/usr/bin/wget", "--quiet", "--spider", "--timeout=1", "http://127.0.0.1:8081/readyz")
		stopAndRequireCleanExit(t, name, 15*time.Second)
	})

	t.Run("agent", func(t *testing.T) {
		listener, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			t.Fatal(err)
		}
		server := &http.Server{Handler: http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			if request.URL.Path == "/v1/agent/heartbeat" {
				response.WriteHeader(http.StatusNoContent)
				return
			}
			response.WriteHeader(http.StatusNotFound)
		})}
		go server.Serve(listener)
		t.Cleanup(func() { _ = server.Close() })

		secrets := createVolume(t, "xisnove-agent-secrets")
		docker(t, "run", "--rm", "--user", "0:0", "--mount", "type=volume,source="+secrets+",target=/secrets", "ubuntu:22.04", "/bin/sh", "-ec", `printf '{"credential":"runtime-only","generation":1}' > /secrets/credential; chown 101:101 /secrets/credential; chmod 0440 /secrets/credential`)
		port := listener.Addr().(*net.TCPAddr).Port
		name := uniqueName("xisnove-agent")
		docker(t, "run", "-d", "--name", name, "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16m", "--mount", "type=volume,source="+secrets+",target=/run/secrets,readonly",
			"-e", fmt.Sprintf("XISNOVE_URL=http://host.docker.internal:%d", port), "-e", "XISNOVE_AGENT_CREDENTIAL_FILE=/run/secrets/credential", "-e", "XISNOVE_AGENT_CAPABILITIES=kubernetes-discovery",
			"xisnove-agent:test-"+runtime.GOARCH)
		registerContainerCleanup(t, name)
		waitContainerHealthy(t, name, 20*time.Second)
		docker(t, "exec", name, "/usr/bin/wget", "--quiet", "--spider", "--timeout=1", "http://127.0.0.1:9090/readyz")
		stopAndRequireCleanExit(t, name, 15*time.Second)
	})
}

func createVolume(t *testing.T, prefix string) string {
	t.Helper()
	name := uniqueName(prefix)
	docker(t, "volume", "create", name)
	t.Cleanup(func() { _ = exec.Command("docker", "volume", "rm", "-f", name).Run() })
	return name
}

func uniqueName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

func registerContainerCleanup(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", "-v", name).Run() })
}

func waitContainerHealthy(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		command := exec.Command("docker", "inspect", "--format", "{{.State.Status}} {{if .State.Health}}{{.State.Health.Status}}{{end}}", name)
		output, err := command.CombinedOutput()
		status := strings.TrimSpace(string(output))
		if err == nil && status == "running healthy" {
			return
		}
		if strings.HasPrefix(status, "exited") {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("container exited before healthy: %s\n%s", status, logs)
		}
		time.Sleep(250 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
	t.Fatalf("container did not become healthy in %s\n%s", timeout, logs)
}

func waitForPostgres(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		command := exec.Command("docker", "exec", name, "pg_isready", "--username=xisnove", "--dbname=xisnove")
		if err := command.Run(); err == nil {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
	t.Fatalf("PostgreSQL did not become ready in %s\n%s", timeout, logs)
}

func stopAndRequireCleanExit(t *testing.T, name string, timeout time.Duration) {
	t.Helper()
	started := time.Now()
	docker(t, "stop", "--time", fmt.Sprint(int(timeout.Seconds())), name)
	if elapsed := time.Since(started); elapsed > timeout+2*time.Second {
		t.Fatalf("graceful stop took %s, budget %s", elapsed, timeout)
	}
	code := strings.TrimSpace(docker(t, "inspect", "--format", "{{.State.ExitCode}}", name))
	if code != "0" {
		logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
		t.Fatalf("exit code = %s, want 0\n%s", code, logs)
	}
}
