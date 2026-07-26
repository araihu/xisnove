package command_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/cli/internal/command"
	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
)

func TestStatusUsesPublicGeneratedSDKOperationWithoutCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
  "activeIncidents":[{"id":"00000000-0000-4000-8000-000000000201","lastTransitionAt":"2026-07-25T12:00:00Z","monitorId":"00000000-0000-4000-8000-000000000101","monitorName":"API","openedAt":"2026-07-25T11:00:00Z","severity":"critical","state":"open"}],
  "generatedAt":"2026-07-25T12:00:00Z",
  "monitors":[{"description":"API","id":"00000000-0000-4000-8000-000000000101","name":"API","state":"down","uptime24Hours":99.5,"uptime30Days":99.9}],
  "state":"degraded"
}`))
	}))
	defer server.Close()
	configPath := writeRemoteTestProfile(t, server.URL)
	reads := 0
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			reads++
			return "", false
		}},
	}
	exit := runner.Run(context.Background(), []string{"--config", configPath, "status"})
	if exit != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	want := "STATE     MONITORS  ACTIVE INCIDENTS  GENERATED AT\n" +
		"degraded  1         1                 2026-07-25T12:00:00Z\n"
	if stdout.String() != want {
		t.Fatalf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", stdout.String(), want)
	}
	if reads != 0 {
		t.Fatalf("credential reads = %d, want 0", reads)
	}
}

func TestMonitorListUsesBearerAndOpaqueCursor(t *testing.T) {
	var gotQuery url.Values
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer mock-token" {
			t.Errorf("Authorization = %q", got)
		}
		gotQuery = request.URL.Query()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
  "items":[{
    "createdAt":"2026-07-25T10:00:00Z","description":"Primary API","displayOrder":1,"enabled":true,
    "failureThreshold":3,"id":"00000000-0000-4000-8000-000000000101","intervalSeconds":60,"kind":"http",
    "labels":{"team":"platform"},"locationId":"00000000-0000-4000-8000-000000000001","name":"API",
    "probe":{"body":"","bodyContains":[],"bodyDoesNotContain":[],"expectedStatus":[{"maximum":299,"minimum":200}],"followRedirects":true,"headers":{},"kind":"http","method":"GET","url":"https://example.test/health"},
    "public":true,"recoveryThreshold":2,"requiredLocation":true,"timeoutMillis":5000,"updatedAt":"2026-07-25T10:00:00Z"
  }],
  "page":{"nextCursor":"opaque-next"}
}`))
	}))
	defer server.Close()
	configPath := writeRemoteTestProfile(t, server.URL)
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			return "mock-token", true
		}},
	}
	exit := runner.Run(context.Background(), []string{"--config", configPath, "monitor", "list", "--limit", "1", "--cursor", "opaque-current"})
	if exit != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	if gotQuery.Get("limit") != "1" || gotQuery.Get("cursor") != "opaque-current" {
		t.Fatalf("query = %v", gotQuery)
	}
	want := "ID                                    NAME  KIND  ENABLED  LOCATION ID                           NEXT CURSOR\n" +
		"00000000-0000-4000-8000-000000000101  API   http  true     00000000-0000-4000-8000-000000000001  opaque-next\n"
	if stdout.String() != want {
		t.Fatalf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestMonitorCreateDecodesGeneratedModelAndSendsExplicitIdempotencyKey(t *testing.T) {
	gotKey := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotKey = request.Header.Get("Idempotency-Key")
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{
  "createdAt":"2026-07-25T10:00:00Z","description":"Primary API","displayOrder":1,"enabled":true,
  "failureThreshold":3,"id":"00000000-0000-4000-8000-000000000101","intervalSeconds":60,"kind":"http",
  "labels":{"team":"platform"},"locationId":"00000000-0000-4000-8000-000000000001","name":"API",
  "probe":{"body":"","bodyContains":[],"bodyDoesNotContain":[],"expectedStatus":[{"maximum":299,"minimum":200}],"followRedirects":true,"headers":{},"kind":"http","method":"GET","url":"https://example.test/health"},
  "public":true,"recoveryThreshold":2,"requiredLocation":true,"timeoutMillis":5000,"updatedAt":"2026-07-25T10:00:00Z"
}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	requestPath := filepath.Join(dir, "monitor.json")
	requestBody := `{
  "failureThreshold":3,"intervalSeconds":60,"locationId":"00000000-0000-4000-8000-000000000001","name":"API",
  "probe":{"body":"","bodyContains":[],"bodyDoesNotContain":[],"expectedStatus":[{"maximum":299,"minimum":200}],"followRedirects":true,"headers":{},"kind":"http","method":"GET","url":"https://example.test/health"},
  "recoveryThreshold":2,"requiredLocation":true,"timeoutMillis":5000
}`
	if err := os.WriteFile(requestPath, []byte(requestBody), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	configPath := writeRemoteTestProfile(t, server.URL)
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			return "mock-token", true
		}},
	}
	exit := runner.Run(context.Background(), []string{
		"--config", configPath, "--output", "json",
		"monitor", "create", "--file", requestPath, "--idempotency-key", "deploy-monitor-1",
	})
	if exit != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	if gotKey != "deploy-monitor-1" {
		t.Fatalf("Idempotency-Key = %q", gotKey)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty for explicit key", stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"id": "00000000-0000-4000-8000-000000000101"`)) {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func TestAuthLoginStoresSessionWithoutPrintingCredential(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"expiresAt":"2026-07-26T12:00:00Z","token":"session-secret-value"}`))
	}))
	defer server.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	tokenPath := filepath.Join(dir, "session.token")
	if err := (config.Store{Path: configPath}).Save(config.Config{
		Version:        1,
		CurrentProfile: "mock",
		Profiles: map[string]config.Profile{
			"mock": {URL: server.URL, Credential: config.CredentialRef{Mode: config.CredentialFile, Reference: tokenPath}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	runner := command.Runner{Stdin: strings.NewReader("mock-password\n"), Stdout: &stdout, Stderr: &stderr}
	exit := runner.Run(context.Background(), []string{
		"--config", configPath, "--output", "json",
		"auth", "login", "--email", "admin@xisnove.test", "--password-stdin",
	})
	if exit != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	if bytes.Contains(stdout.Bytes(), []byte("session-secret-value")) || bytes.Contains(stderr.Bytes(), []byte("session-secret-value")) {
		t.Fatalf("credential leaked: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	stored, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadFile(token) error = %v", err)
	}
	if string(stored) != "session-secret-value\n" {
		t.Fatalf("stored token = %q", stored)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestAuthLoginRejectsReadOnlyCredentialBeforeCreatingSession(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	configPath := writeRemoteTestProfile(t, server.URL)
	var stdout, stderr bytes.Buffer
	runner := command.Runner{Stdin: strings.NewReader("mock-password\n"), Stdout: &stdout, Stderr: &stderr}

	exit := runner.Run(context.Background(), []string{
		"--config", configPath, "auth", "login", "--email", "admin@xisnove.test", "--password-stdin",
	})
	if exit != 2 {
		t.Fatalf("Run() exit = %d, want 2; stderr = %s", exit, stderr.String())
	}
	if requests != 0 {
		t.Fatalf("server requests = %d, want 0", requests)
	}
	if !strings.Contains(stderr.String(), "read-only") {
		t.Fatalf("stderr = %q, want read-only credential guidance", stderr.String())
	}
}

func TestAPITokenCreateRejectsReadOnlyTargetBeforeCreatingToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	if err := (config.Store{Path: configPath}).Save(config.Config{
		Version:        1,
		CurrentProfile: "admin",
		Profiles: map[string]config.Profile{
			"admin":      {URL: server.URL, Credential: config.CredentialRef{Mode: config.CredentialFile, Reference: filepath.Join(dir, "admin.token")}},
			"automation": {URL: server.URL, Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "AUTOMATION_TOKEN"}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	requestPath := filepath.Join(dir, "token.json")
	if err := os.WriteFile(requestPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	exit := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{
		"--config", configPath, "auth", "token", "create", "--file", requestPath, "--store-profile", "automation",
	})
	if exit != 2 {
		t.Fatalf("Run() exit = %d, want 2; stderr = %s", exit, stderr.String())
	}
	if requests != 0 {
		t.Fatalf("server requests = %d, want 0", requests)
	}
	if !strings.Contains(stderr.String(), "read-only") {
		t.Fatalf("stderr = %q, want read-only credential guidance", stderr.String())
	}
}

func TestAuthLogoutRevokesThenDeletesWritableCredential(t *testing.T) {
	revoked := false
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer session-secret-value" {
			t.Errorf("Authorization = %q", got)
		}
		revoked = true
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	tokenPath := filepath.Join(dir, "session.token")
	if err := os.WriteFile(tokenPath, []byte("session-secret-value\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(token) error = %v", err)
	}
	if err := (config.Store{Path: configPath}).Save(config.Config{
		Version:        1,
		CurrentProfile: "mock",
		Profiles: map[string]config.Profile{
			"mock": {URL: server.URL, Credential: config.CredentialRef{Mode: config.CredentialFile, Reference: tokenPath}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	exit := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{"--config", configPath, "auth", "logout"})
	if exit != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	if !revoked {
		t.Fatal("server did not receive revocation")
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("token file still exists: %v", err)
	}
}

func TestAgentCredentialLifecycleUsesSDKAndNeverPrintsSecret(t *testing.T) {
	const (
		agentID = "00000000-0000-4000-8000-000000000301"
		secret  = "agent-secret-value"
	)
	requests := make([]string, 0, 3)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer mock-token" {
			t.Errorf("Authorization = %q", got)
		}
		requests = append(requests, request.Method+" "+request.URL.Path+" "+request.Header.Get("Idempotency-Key"))
		switch request.Method + " " + request.URL.Path {
		case http.MethodPost + " /v1/agents/" + agentID + "/credential-rotations":
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"agentId":"` + agentID + `","credential":"` + secret + `","credentialGeneration":2}`))
		case http.MethodDelete + " /v1/agents/" + agentID + "/credentials/1",
			http.MethodDelete + " /v1/agents/" + agentID:
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	configPath := writeRemoteTestProfile(t, server.URL)
	credentialPath := filepath.Join(t.TempDir(), "rotated-agent.token")
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			return "mock-token", true
		}},
	}

	run := func(args ...string) {
		t.Helper()
		if exit := runner.Run(context.Background(), append([]string{"--config", configPath}, args...)); exit != 0 {
			t.Fatalf("Run(%q) exit = %d, stderr = %s", args, exit, stderr.String())
		}
	}
	run("agent", "rotate-credential", agentID, "--store-file", credentialPath, "--idempotency-key", "rotate-1")
	run("agent", "revoke-generation", agentID, "1", "--idempotency-key", "revoke-generation-1")
	run("agent", "revoke", agentID, "--idempotency-key", "revoke-agent-1")

	stored, err := os.ReadFile(credentialPath)
	if err != nil {
		t.Fatalf("ReadFile(rotated credential) error = %v", err)
	}
	if string(stored) != secret+"\n" {
		t.Fatalf("stored rotated credential = %q", stored)
	}
	info, err := os.Stat(credentialPath)
	if err != nil {
		t.Fatalf("Stat(rotated credential) error = %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("rotated credential mode = %#o, want 0600", info.Mode().Perm())
	}
	if strings.Contains(stdout.String(), secret) || strings.Contains(stderr.String(), secret) {
		t.Fatalf("credential leaked: stdout=%s stderr=%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "00000000-0000-4000-8000-000000000301  2") {
		t.Fatalf("rotation output = %q, want agent and generation", stdout.String())
	}
	wantRequests := []string{
		"POST /v1/agents/" + agentID + "/credential-rotations rotate-1",
		"DELETE /v1/agents/" + agentID + "/credentials/1 revoke-generation-1",
		"DELETE /v1/agents/" + agentID + " revoke-agent-1",
	}
	if strings.Join(requests, "\n") != strings.Join(wantRequests, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, wantRequests)
	}
}

func TestAgentRotateCredentialRejectsMissingSecret(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"agentId":"00000000-0000-4000-8000-000000000301","credentialGeneration":2}`))
	}))
	defer server.Close()
	configPath := writeRemoteTestProfile(t, server.URL)
	credentialPath := filepath.Join(t.TempDir(), "missing.token")
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout: &stdout,
		Stderr: &stderr,
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			return "mock-token", true
		}},
	}
	exit := runner.Run(context.Background(), []string{"--config", configPath, "agent", "rotate-credential", "00000000-0000-4000-8000-000000000301", "--store-file", credentialPath})
	if exit != 1 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	if !strings.Contains(stderr.String(), "rotated Agent credential is missing") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if _, err := os.Stat(credentialPath); !os.IsNotExist(err) {
		t.Fatalf("credential file exists after invalid response: %v", err)
	}
}

func writeRemoteTestProfile(t *testing.T, serverURL string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := (config.Store{Path: path}).Save(config.Config{
		Version:        1,
		CurrentProfile: "mock",
		Profiles: map[string]config.Profile{
			"mock": {URL: serverURL, Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "MOCK_TOKEN"}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	return path
}
