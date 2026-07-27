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
	prepare.Env = append(os.Environ(), "XISNOVE_SECRET_DIR="+secretDirectory, "XISNOVE_ADMIN_PASSWORD=test-password-123")
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
	prepare.Env = append(os.Environ(), "XISNOVE_SECRET_DIR="+secretDirectory, "XISNOVE_ADMIN_PASSWORD=test-password-123")
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
	prepare.Env = append(os.Environ(), "XISNOVE_SECRET_DIR="+secretDirectory, "XISNOVE_ADMIN_PASSWORD=test-password-123")
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
