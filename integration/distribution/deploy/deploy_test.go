package deploy_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/internal/testsupport/postgrescontainer"
)

func TestPrepareSecretsIsPrivateIdempotentAndResumable(t *testing.T) {
	repo := repositoryRoot(t)
	directory := t.TempDir()
	script := filepath.Join(repo, "deploy/raw/prepare-secrets.sh")

	run := func(interrupt string) ([]byte, error) {
		command := exec.Command("sh", script)
		command.Env = append(os.Environ(),
			"XISNOVE_SECRET_DIR="+directory,
			"XISNOVE_ADMIN_PASSWORD=correct-horse-battery-staple",
			"XISNOVE_BOOTSTRAP_INTERRUPT_AFTER="+interrupt,
			testControlPlaneOwner(t),
		)
		return command.CombinedOutput()
	}

	if output, err := run("cursor-signing-key"); err == nil {
		t.Fatalf("interrupted preparation succeeded: %s", output)
	}
	if output, err := run(""); err != nil {
		t.Fatalf("resume preparation: %v: %s", err, output)
	}

	wantFiles := []string{
		"admin-password", "cursor-signing-key", "notification-keyring.json",
		"ui-cookie-secret", "agent-credential.json", "turso-auth-token", "database-url",
	}
	before := map[string][]byte{}
	for _, name := range wantFiles {
		path := filepath.Join(directory, name)
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		before[name] = contents
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %v, %v", name, info.Mode().Perm(), err)
		}
	}
	if string(before["admin-password"]) != "correct-horse-battery-staple\n" {
		t.Fatal("supplied administrator password was not consumed exactly")
	}
	if !bytes.Contains(before["agent-credential.json"], []byte(`"credential":"CHANGE-ME-AFTER-ENROLLMENT"`)) {
		t.Fatal("Agent credential placeholder is not explicit")
	}

	if output, err := run(""); err != nil {
		t.Fatalf("second preparation: %v: %s", err, output)
	}
	for name, expected := range before {
		actual, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("%s changed on retry", name)
		}
	}
}

func TestSingletonWrapperRefusesSecondProcessAndRecovers(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell deployment is unsupported on Windows")
	}
	repo := repositoryRoot(t)
	lock := filepath.Join(t.TempDir(), "server.lock")
	script := filepath.Join(repo, "deploy/raw/run-singleton.sh")
	first := exec.Command("sh", script, lock, "sh", "-c", "sleep 30")
	if err := first.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Process.Kill(); _, _ = first.Process.Wait() })

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(lock + ".d/owner"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first wrapper did not acquire lock")
		}
		time.Sleep(20 * time.Millisecond)
	}

	sentinel := filepath.Join(t.TempDir(), "database-sentinel")
	if err := os.WriteFile(sentinel, []byte("intact"), 0o600); err != nil {
		t.Fatal(err)
	}
	second := exec.Command("sh", script, lock, "sh", "-c", "printf damaged >\"$1\"", "sh", sentinel)
	output, err := second.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "another Xisnove server owns") {
		t.Fatalf("second wrapper = %v, %q", err, output)
	}
	if contents, _ := os.ReadFile(sentinel); string(contents) != "intact" {
		t.Fatal("refused second process changed persistent data")
	}

	if err := first.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_, _ = first.Process.Wait()
	time.Sleep(50 * time.Millisecond)
	recovered := exec.Command("sh", script, lock, "sh", "-c", "exit 0")
	if output, err := recovered.CombinedOutput(); err != nil {
		t.Fatalf("recover stale lock: %v: %s", err, output)
	}
}

func TestSingletonWrapperPrefersKernelLockForContainerRecovery(t *testing.T) {
	script := read(t, filepath.Join(repositoryRoot(t), "deploy/raw/run-singleton.sh"))
	for _, expected := range []string{"command -v flock", "--conflict-exit-code 75"} {
		if !strings.Contains(script, expected) {
			t.Fatalf("singleton wrapper missing %q", expected)
		}
	}
}

func TestRawSQLiteLoginPersistsAcrossRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real server binary")
	}
	repo := repositoryRoot(t)
	directory := t.TempDir()
	server := filepath.Join(directory, "xisnove-server")
	build := exec.Command("go", "build", "-o", server, "./cmd/xisnove-server")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v: %s", err, output)
	}
	secretDirectory := filepath.Join(directory, "secrets")
	prepare := exec.Command("sh", filepath.Join(repo, "deploy/raw/prepare-secrets.sh"))
	prepare.Env = append(os.Environ(), "XISNOVE_SECRET_DIR="+secretDirectory, "XISNOVE_ADMIN_PASSWORD=test-password-123", testControlPlaneOwner(t))
	if output, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare secrets: %v: %s", err, output)
	}
	database := filepath.Join(directory, "xisnove.db")
	runServerCommand(t, server, "db", "migrate", "--database-profile", "sqlite", "--database-url", database, "--lock-timeout", "5s")
	runServerCommand(t, server, "admin", "bootstrap", "--database-profile", "sqlite", "--database-url", database, "--email", "admin@example.test", "--password-file", filepath.Join(secretDirectory, "admin-password"))

	address := freeAddress(t)
	start := func() *exec.Cmd {
		command := exec.Command("sh", filepath.Join(repo, "deploy/raw/run-server.sh"))
		command.Env = append(os.Environ(),
			"XISNOVE_SERVER_BIN="+server,
			"XISNOVE_DATABASE_PROFILE=sqlite",
			"XISNOVE_DATABASE_URL="+database,
			"XISNOVE_LISTEN="+address,
			"XISNOVE_SINGLETON_LOCK="+filepath.Join(directory, "server"),
			"XISNOVE_CURSOR_SIGNING_KEY_FILE="+filepath.Join(secretDirectory, "cursor-signing-key"),
			"XISNOVE_NOTIFICATION_MASTER_KEY_FILE="+filepath.Join(secretDirectory, "notification-keyring.json"),
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	stop := func(command *exec.Cmd) {
		_ = command.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _, _ = command.Process.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(12 * time.Second):
			_ = command.Process.Kill()
			t.Fatal("server did not stop within budget")
		}
	}

	first := start()
	waitReady(t, "http://"+address+"/readyz")
	firstToken := login(t, "http://"+address)
	if firstToken == "" {
		t.Fatal("first login returned empty session")
	}
	agentID := bootstrapAgentAcrossInterruptions(t, repo, directory, server, database, secretDirectory, "http://"+address)
	observeAgent(t, repo, directory, "http://"+address, firstToken, agentID, filepath.Join(secretDirectory, "agent-credential.json"))
	stop(first)

	second := start()
	t.Cleanup(func() {
		if second.ProcessState == nil {
			_ = second.Process.Kill()
			_, _ = second.Process.Wait()
		}
	})
	waitReady(t, "http://"+address+"/readyz")
	if token := login(t, "http://"+address); token == "" {
		t.Fatal("login after restart returned empty session")
	}
	stop(second)
}

func TestRawLocalTursoIsPersistentAndSingleton(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the real local-Turso server")
	}
	repo := repositoryRoot(t)
	directory := t.TempDir()
	server := filepath.Join(directory, "xisnove-server")
	build := exec.Command("go", "build", "-o", server, "./cmd/xisnove-server")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v: %s", err, output)
	}
	secretDirectory := filepath.Join(directory, "secrets")
	prepare := exec.Command("sh", filepath.Join(repo, "deploy/raw/prepare-secrets.sh"))
	prepare.Env = append(os.Environ(), "XISNOVE_SECRET_DIR="+secretDirectory, "XISNOVE_ADMIN_PASSWORD=test-password-123", testControlPlaneOwner(t))
	if output, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare secrets: %v: %s", err, output)
	}
	database := filepath.Join(directory, "turso.db")
	baseArgs := []string{"--database-profile", "turso-local", "--database-url", database}
	runServerCommand(t, server, append([]string{"db", "migrate"}, append(baseArgs, "--lock-timeout", "5s")...)...)
	runServerCommand(t, server, append([]string{"admin", "bootstrap"}, append(baseArgs, "--email", "admin@example.test", "--password-file", filepath.Join(secretDirectory, "admin-password"))...)...)

	address := freeAddress(t)
	start := func(listen string) *exec.Cmd {
		command := exec.Command("sh", filepath.Join(repo, "deploy/raw/run-server.sh"))
		command.Env = append(os.Environ(),
			"XISNOVE_SERVER_BIN="+server,
			"XISNOVE_DATABASE_PROFILE=turso-local",
			"XISNOVE_DATABASE_URL="+database,
			"XISNOVE_LISTEN="+listen,
			"XISNOVE_SINGLETON_LOCK="+filepath.Join(directory, "server"),
			"XISNOVE_CURSOR_SIGNING_KEY_FILE="+filepath.Join(secretDirectory, "cursor-signing-key"),
			"XISNOVE_NOTIFICATION_MASTER_KEY_FILE="+filepath.Join(secretDirectory, "notification-keyring.json"),
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	stop := func(command *exec.Cmd) {
		if command.ProcessState == nil {
			_ = command.Process.Signal(os.Interrupt)
			_, _ = command.Process.Wait()
		}
	}
	first := start(address)
	waitReady(t, "http://"+address+"/readyz")
	if login(t, "http://"+address) == "" {
		t.Fatal("local-Turso login returned empty session")
	}
	second := start(freeAddress(t))
	secondDone := make(chan error, 1)
	go func() { secondDone <- second.Wait() }()
	select {
	case err := <-secondDone:
		if err == nil || second.ProcessState.ExitCode() != 75 {
			t.Fatalf("second local-Turso server exit = %v, %v", second.ProcessState.ExitCode(), err)
		}
	case <-time.After(3 * time.Second):
		_ = second.Process.Kill()
		t.Fatal("second local-Turso server did not refuse singleton start")
	}
	stop(first)

	restarted := start(address)
	t.Cleanup(func() { stop(restarted) })
	waitReady(t, "http://"+address+"/readyz")
	if login(t, "http://"+address) == "" {
		t.Fatal("local-Turso login after restart returned empty session")
	}
	stop(restarted)
}

func TestRawPostgresProfileStartsReplicaAndCleansUp(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the server and provisions PostgreSQL")
	}
	postgresURL := postgrescontainer.URL(t, os.Getenv("XISNOVE_TEST_POSTGRES_URL"))
	repo := repositoryRoot(t)
	directory := t.TempDir()
	server := filepath.Join(directory, "xisnove-server")
	build := exec.Command("go", "build", "-o", server, "./cmd/xisnove-server")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v: %s", err, output)
	}
	urlFile := filepath.Join(directory, "postgres-url")
	if err := os.WriteFile(urlFile, []byte(postgresURL+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretDirectory := filepath.Join(directory, "secrets")
	prepare := exec.Command("sh", filepath.Join(repo, "deploy/raw/prepare-secrets.sh"))
	prepare.Env = append(os.Environ(), "XISNOVE_SECRET_DIR="+secretDirectory, "XISNOVE_ADMIN_PASSWORD=test-password-123", testControlPlaneOwner(t))
	if output, err := prepare.CombinedOutput(); err != nil {
		t.Fatalf("prepare secrets: %v: %s", err, output)
	}
	baseArgs := []string{"--database-profile", "postgres", "--database-url-file", urlFile}
	runServerCommand(t, server, append([]string{"db", "migrate"}, append(baseArgs, "--lock-timeout", "5s")...)...)
	runServerCommand(t, server, append([]string{"admin", "bootstrap"}, append(baseArgs, "--email", "admin@example.test", "--password-file", filepath.Join(secretDirectory, "admin-password"))...)...)

	start := func(address string) *exec.Cmd {
		command := exec.Command("sh", filepath.Join(repo, "deploy/raw/run-server.sh"))
		command.Env = append(os.Environ(),
			"XISNOVE_SERVER_BIN="+server,
			"XISNOVE_DATABASE_PROFILE=postgres",
			"XISNOVE_DATABASE_URL_FILE="+urlFile,
			"XISNOVE_REPLICAS=2",
			"XISNOVE_LISTEN="+address,
			"XISNOVE_INSTALLATION_ID=raw-postgres-test",
			"XISNOVE_CURSOR_SIGNING_KEY_FILE="+filepath.Join(secretDirectory, "cursor-signing-key"),
			"XISNOVE_NOTIFICATION_MASTER_KEY_FILE="+filepath.Join(secretDirectory, "notification-keyring.json"),
		)
		if err := command.Start(); err != nil {
			t.Fatal(err)
		}
		return command
	}
	stop := func(command *exec.Cmd) {
		if command.ProcessState != nil {
			return
		}
		_ = command.Process.Signal(os.Interrupt)
		_, _ = command.Process.Wait()
	}
	firstAddress, secondAddress := freeAddress(t), freeAddress(t)
	first, second := start(firstAddress), start(secondAddress)
	t.Cleanup(func() { stop(second); stop(first) })
	waitReady(t, "http://"+firstAddress+"/readyz")
	waitReady(t, "http://"+secondAddress+"/readyz")
	if token := login(t, "http://"+secondAddress); token == "" {
		t.Fatal("replica login returned empty session")
	}
	stop(second)
	stop(first)
}

func TestComposeAndSystemdContracts(t *testing.T) {
	repo := repositoryRoot(t)
	compose := read(t, filepath.Join(repo, "deploy/compose/compose.yaml"))
	for _, text := range []string{
		"server:", "ui:", "agent:", "postgres:", "profiles: [postgres]",
		"/livez", "/readyz", "XISNOVE_CURSOR_SIGNING_KEY_FILE",
		"XISNOVE_NOTIFICATION_MASTER_KEY_FILE", "XISNOVE_UI_COOKIE_SECRET_FILE",
		"XISNOVE_AGENT_CREDENTIAL_FILE", "restart: unless-stopped",
		"chown 101:101", "chmod 0440", "/usr/bin/wget", "run-server.sh",
	} {
		if !strings.Contains(compose, text) {
			t.Errorf("compose missing %q", text)
		}
	}
	for _, forbidden := range []string{"XISNOVE_ADMIN_PASSWORD:", "XISNOVE_UI_COOKIE_SECRET:", "XISNOVE_DATABASE_AUTH_TOKEN:"} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose renders secret value source %q", forbidden)
		}
	}
	remoteStart := strings.Index(compose, "  server-remote:")
	remoteEnd := strings.Index(compose, "\n  ui:")
	if remoteStart < 0 || remoteEnd <= remoteStart || strings.Contains(compose[remoteStart:remoteEnd], "xisnove-data") {
		t.Fatal("remote server profile must not mount local database storage")
	}

	for _, name := range []string{"xisnove-migrate.service", "xisnove-server.service", "xisnove-ui.service", "xisnove-agent.service"} {
		unit := read(t, filepath.Join(repo, "deploy/systemd", name))
		for _, text := range []string{"NoNewPrivileges=true", "ProtectSystem=strict"} {
			if !strings.Contains(unit, text) {
				t.Errorf("%s missing %q", name, text)
			}
		}
	}
	server := read(t, filepath.Join(repo, "deploy/systemd/xisnove-server.service"))
	for _, text := range []string{"Requires=xisnove-migrate.service", "ReadWritePaths=/var/lib/xisnove", "Restart=on-failure"} {
		if !strings.Contains(server, text) {
			t.Errorf("server unit missing %q", text)
		}
	}
	migration := read(t, filepath.Join(repo, "deploy/systemd/xisnove-migrate.service"))
	if !strings.Contains(migration, "TimeoutStartSec=60s") {
		t.Fatal("migration is not bounded")
	}
}

func TestRawBootstrapKeepsSensitiveValuesOutOfProcessArguments(t *testing.T) {
	script := read(t, filepath.Join(repositoryRoot(t), "deploy/raw/bootstrap.sh"))
	for _, forbidden := range []string{
		`--arg password "$password"`,
		`Authorization: Bearer $session`,
		`--arg token "$token"`,
		`--arg credential "$enrollment_credential"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("bootstrap exposes sensitive value through argv: %s", forbidden)
		}
	}
	for _, required := range []string{
		"--rawfile password", "--rawfile token", "--rawfile credential",
		`--header @"$authorization_header_file"`, "session-token", "authorization-header",
	} {
		if !strings.Contains(script, required) {
			t.Errorf("bootstrap missing private file mechanism %q", required)
		}
	}
}

func TestComposeCommandSupportsPluginBinaryAndOverride(t *testing.T) {
	repo := repositoryRoot(t)
	helper := filepath.Join(repo, "deploy/compose/compose-command.sh")
	tests := []struct {
		name             string
		dockerExit       int
		override         string
		wantInvocation   string
		forbidInvocation string
	}{
		{name: "plugin preferred", wantInvocation: "docker compose version", forbidInvocation: "docker-compose version"},
		{name: "binary fallback", dockerExit: 1, wantInvocation: "docker-compose version"},
		{name: "plugin override", override: "docker compose", wantInvocation: "docker compose version", forbidInvocation: "docker-compose version"},
		{name: "binary override", override: "docker-compose", wantInvocation: "docker-compose version", forbidInvocation: "docker compose version"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			logPath := filepath.Join(directory, "invocations")
			docker := fmt.Sprintf("#!/bin/sh\nprintf 'docker %%s\\n' \"$*\" >>%q\nexit %d\n", logPath, test.dockerExit)
			if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(docker), 0o755); err != nil {
				t.Fatal(err)
			}
			binary := fmt.Sprintf("#!/bin/sh\nprintf 'docker-compose %%s\\n' \"$*\" >>%q\n", logPath)
			if err := os.WriteFile(filepath.Join(directory, "docker-compose"), []byte(binary), 0o755); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("sh", "-c", `. "$1"; compose version`, "sh", helper)
			command.Env = append(withoutEnvironment(os.Environ(), "COMPOSE_COMMAND"),
				"PATH="+directory+":"+os.Getenv("PATH"),
				"COMPOSE_COMMAND="+test.override,
			)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("compose selection: %v: %s", err, output)
			}
			invocations, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(invocations), test.wantInvocation) {
				t.Fatalf("invocations %q do not contain %q", invocations, test.wantInvocation)
			}
			if test.forbidInvocation != "" && strings.Contains(string(invocations), test.forbidInvocation) {
				t.Fatalf("invocations %q contain %q", invocations, test.forbidInvocation)
			}
		})
	}
}

func TestComposeEndpointNormalizationUsesLoopbackOnDesktopAndLinux(t *testing.T) {
	directory := t.TempDir()
	docker := filepath.Join(directory, "docker")
	if err := os.WriteFile(docker, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(repositoryRoot(t), "deploy/compose/compose-command.sh")
	for input, expected := range map[string]string{
		"127.0.0.1:49152": "127.0.0.1:49152",
		"0.0.0.0:49153":   "127.0.0.1:49153",
		"[::]:49154":      "127.0.0.1:49154",
	} {
		command := exec.Command("sh", "-c", `. "$1"; normalize_compose_endpoint "$2"`, "sh", helper, input)
		command.Env = append(withoutEnvironment(os.Environ(), "COMPOSE_COMMAND"), "PATH="+directory+":"+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("normalize %q: %v: %s", input, err, output)
		}
		if actual := strings.TrimSpace(string(output)); actual != expected {
			t.Errorf("normalize %q = %q, want %q", input, actual, expected)
		}
	}
}

func TestRemoteComposeStartsReplicaSetWithoutHostPortCollision(t *testing.T) {
	repo := repositoryRoot(t)
	compose := read(t, filepath.Join(repo, "deploy/compose/compose.yaml"))
	remoteStart := strings.Index(compose, "  server-remote:")
	remoteEnd := strings.Index(compose, "\n  ui:")
	if remoteStart < 0 || remoteEnd <= remoteStart {
		t.Fatal("server-remote service not found")
	}
	remote := compose[remoteStart:remoteEnd]
	if !strings.Contains(remote, `ports: ["127.0.0.1::8080"]`) {
		t.Fatal("remote replicas must use collision-free loopback host ports")
	}
	if strings.Contains(remote, `ports: ["127.0.0.1:8080:8080"]`) {
		t.Fatal("remote replicas retain a fixed colliding host port")
	}
	bootstrap := read(t, filepath.Join(repo, "deploy/compose/bootstrap.sh"))
	for _, required := range []string{
		`--scale "$server_service=$server_replicas"`,
		`port --index 1 "$server_service" 8080`,
		`XISNOVE_SERVER_REPLICAS:-2`,
	} {
		if !strings.Contains(bootstrap, required) {
			t.Errorf("remote bootstrap missing %q", required)
		}
	}
}

func TestRawSecretOwnershipIsExplicitAndFailClosed(t *testing.T) {
	repo := repositoryRoot(t)
	prepare := filepath.Join(repo, "deploy/raw/prepare-secrets.sh")
	directory := t.TempDir()
	command := exec.Command("sh", prepare)
	command.Env = append(withoutEnvironment(os.Environ(), "XISNOVE_CONTROL_PLANE_SECRET_OWNER"),
		"XISNOVE_SECRET_DIR="+directory,
		"XISNOVE_REQUIRE_SECRET_OWNERSHIP=true",
		"XISNOVE_ADMIN_PASSWORD=test-password-123",
	)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "control-plane secret owner is required") {
		t.Fatalf("missing owner result = %v, %q", err, output)
	}
	bootstrap := filepath.Join(repo, "deploy/raw/bootstrap.sh")
	command = exec.Command("sh", bootstrap)
	command.Env = append(withoutEnvironment(os.Environ(), "XISNOVE_AGENT_CREDENTIAL_OWNER"),
		"XISNOVE_BOOTSTRAP_ONLINE=true",
		"XISNOVE_REQUIRE_SECRET_OWNERSHIP=true",
		"XISNOVE_CONTROL_PLANE_SECRET_OWNER=xisnove:xisnove",
	)
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "Agent credential owner is required") {
		t.Fatalf("missing Agent owner result = %v, %q", err, output)
	}

	chownLog := filepath.Join(t.TempDir(), "chown.log")
	chownCommand := filepath.Join(t.TempDir(), "chown")
	contents := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >>%q\n", chownLog)
	if err := os.WriteFile(chownCommand, []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
	command = exec.Command("sh", prepare)
	command.Env = append(os.Environ(),
		"XISNOVE_SECRET_DIR="+directory,
		"XISNOVE_REQUIRE_SECRET_OWNERSHIP=true",
		"XISNOVE_CONTROL_PLANE_SECRET_OWNER=xisnove:xisnove",
		"XISNOVE_CHOWN_COMMAND="+chownCommand,
		"XISNOVE_ADMIN_PASSWORD=test-password-123",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("owned secret preparation: %v: %s", err, output)
	}
	ownership, err := os.ReadFile(chownLog)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, filepath.Join(directory, "cursor-signing-key"), filepath.Join(directory, "ui-cookie-secret")} {
		if !strings.Contains(string(ownership), "xisnove:xisnove "+path) {
			t.Errorf("ownership log missing control-plane path %s: %s", path, ownership)
		}
	}

	bootstrapContents := read(t, bootstrap)
	for _, required := range []string{"XISNOVE_AGENT_CREDENTIAL_OWNER", "Agent credential owner is required"} {
		if !strings.Contains(bootstrapContents, required) {
			t.Errorf("bootstrap missing Agent ownership contract %q", required)
		}
	}
}

func TestPrepareSecretsRootComposePathDoesNotRequireSystemdOwner(t *testing.T) {
	repo := repositoryRoot(t)
	directory := t.TempDir()
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "id"), []byte("#!/bin/sh\nprintf '0\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("sh", filepath.Join(repo, "deploy/raw/prepare-secrets.sh"))
	command.Env = append(withoutEnvironment(os.Environ(), "XISNOVE_CONTROL_PLANE_SECRET_OWNER", "XISNOVE_REQUIRE_SECRET_OWNERSHIP"),
		"PATH="+fakeBin+":"+os.Getenv("PATH"),
		"XISNOVE_SECRET_DIR="+directory,
		"XISNOVE_ADMIN_PASSWORD=test-password-123",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("root-compatible Compose secret preparation: %v: %s", err, output)
	}
}

func withoutEnvironment(environment []string, names ...string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		keep := true
		for _, name := range names {
			if strings.HasPrefix(entry, name+"=") {
				keep = false
				break
			}
		}
		if keep {
			result = append(result, entry)
		}
	}
	return result
}

func testControlPlaneOwner(t *testing.T) string {
	t.Helper()
	uid, err := exec.Command("id", "-u").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(uid)) != "0" {
		return "XISNOVE_CONTROL_PLANE_SECRET_OWNER="
	}
	gid, err := exec.Command("id", "-g").Output()
	if err != nil {
		t.Fatal(err)
	}
	return "XISNOVE_CONTROL_PLANE_SECRET_OWNER=0:" + strings.TrimSpace(string(gid))
}

func testAgentOwner(t *testing.T) string {
	t.Helper()
	owner := strings.TrimPrefix(testControlPlaneOwner(t), "XISNOVE_CONTROL_PLANE_SECRET_OWNER=")
	return "XISNOVE_AGENT_CREDENTIAL_OWNER=" + owner
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(directory, "../../.."))
}

func read(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(contents)
}

func runServerCommand(t *testing.T, server string, args ...string) {
	t.Helper()
	command := exec.Command(server, args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v: %s", strings.Join(args, " "), err, output)
	}
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func waitReady(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := http.Get(url) // #nosec G107 -- test-only loopback URL.
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusNoContent {
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("readiness timed out: %s", url)
}

func login(t *testing.T, baseURL string) string {
	t.Helper()
	body := strings.NewReader(`{"email":"admin@example.test","password":"test-password-123"}`)
	response, err := http.Post(baseURL+"/v1/sessions", "application/json", body) // #nosec G107 -- test-only loopback URL.
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("login status = %d", response.StatusCode)
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Token == "" {
		t.Fatal(fmt.Errorf("empty login token"))
	}
	return payload.Token
}

func bootstrapAgentAcrossInterruptions(t *testing.T, repo, directory, server, database, secretDirectory, baseURL string) string {
	t.Helper()
	stateDirectory := filepath.Join(directory, "bootstrap-state")
	run := func(interrupt string, wantSuccess bool) {
		command := exec.Command("sh", filepath.Join(repo, "deploy/raw/bootstrap.sh"))
		command.Env = append(os.Environ(),
			"XISNOVE_BOOTSTRAP_ONLINE=true",
			"XISNOVE_BOOTSTRAP_SKIP_OFFLINE=true",
			"XISNOVE_BOOTSTRAP_INTERRUPT_AFTER="+interrupt,
			"XISNOVE_BOOTSTRAP_STATE_DIR="+stateDirectory,
			"XISNOVE_SECRET_DIR="+secretDirectory,
			"XISNOVE_SERVER_BIN="+server,
			"XISNOVE_DATABASE_PROFILE=sqlite",
			"XISNOVE_DATABASE_URL="+database,
			"XISNOVE_API_URL="+baseURL,
			"XISNOVE_ADMIN_EMAIL=admin@example.test",
			"XISNOVE_AGENT_NAME=raw-agent",
			testControlPlaneOwner(t),
			testAgentOwner(t),
		)
		output, err := command.CombinedOutput()
		if wantSuccess && err != nil {
			t.Fatalf("resume bootstrap after %q: %v: %s", interrupt, err, output)
		}
		if !wantSuccess && err == nil {
			t.Fatalf("bootstrap did not interrupt after %q", interrupt)
		}
		if !wantSuccess && !strings.Contains(string(output), "interrupted after "+interrupt) {
			t.Fatalf("bootstrap failed before %q: %v: %s", interrupt, err, output)
		}
	}
	for _, boundary := range []string{"server-ready", "location", "enrollment-token", "enrollment-credential", "enrollment", "credential"} {
		run(boundary, false)
	}
	run("", true)
	files := []string{"admin-password", "cursor-signing-key", "notification-keyring.json", "ui-cookie-secret", "agent-credential.json"}
	before := make(map[string][]byte, len(files))
	for _, name := range files {
		contents, err := os.ReadFile(filepath.Join(secretDirectory, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = contents
	}
	run("", true)
	for name, expected := range before {
		actual, err := os.ReadFile(filepath.Join(secretDirectory, name))
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("%s changed after completed bootstrap retry", name)
		}
	}
	agentIDBytes, err := os.ReadFile(filepath.Join(stateDirectory, "agent-id"))
	if err != nil {
		t.Fatal(err)
	}
	agentID := strings.TrimSpace(string(agentIDBytes))
	if agentID == "" {
		t.Fatal("bootstrap enrollment response omitted Agent id")
	}
	for _, name := range []string{"enrollment-token.json", "agent-enrollment-credential", "enrolled-agent.json"} {
		if _, err := os.Stat(filepath.Join(stateDirectory, name)); !os.IsNotExist(err) {
			t.Fatalf("sensitive bootstrap state remains: %s", name)
		}
	}
	return agentID
}

func observeAgent(t *testing.T, repo, directory, baseURL, session, agentID, credentialFile string) {
	t.Helper()
	agentBinary := filepath.Join(directory, "xisnove-agent")
	build := exec.Command("go", "build", "-o", agentBinary, "./agent/cmd/xisnove-agent")
	build.Dir = repo
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build Agent: %v: %s", err, output)
	}
	observabilityAddress := freeAddress(t)
	agent := exec.Command(agentBinary)
	agent.Env = append(os.Environ(),
		"XISNOVE_URL="+baseURL,
		"XISNOVE_AGENT_CREDENTIAL_FILE="+credentialFile,
		"XISNOVE_AGENT_OBSERVABILITY_ADDRESS="+observabilityAddress,
		"XISNOVE_AGENT_CAPABILITIES=http",
	)
	if err := agent.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if agent.ProcessState == nil {
			_ = agent.Process.Signal(os.Interrupt)
			_, _ = agent.Process.Wait()
		}
	}()
	waitReady(t, "http://"+observabilityAddress+"/readyz")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		observed := jsonRequest(t, http.MethodGet, baseURL+"/v1/agents/"+agentID, session, "", "")
		if lastSeen, _ := observed["lastSeenAt"].(string); lastSeen != "" {
			_ = agent.Process.Signal(os.Interrupt)
			_, _ = agent.Process.Wait()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("Agent heartbeat was not observed")
}

func jsonRequest(t *testing.T, method, url, bearer, idempotencyKey, payload string) map[string]any {
	t.Helper()
	request, err := http.NewRequest(method, url, strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if payload != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		contents, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		t.Fatalf("%s %s status = %d: %s", method, url, response.StatusCode, contents)
	}
	var result map[string]any
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	return result
}
