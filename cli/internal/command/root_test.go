package command_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/cli/internal/command"
	"github.com/araihu/xisnove/cli/internal/config"
	"github.com/araihu/xisnove/cli/internal/credential"
	"github.com/araihu/xisnove/cli/internal/problem"
	"github.com/spf13/cobra"
)

type fakeSDKFamily struct{}

func (fakeSDKFamily) Name() string { return "status" }

func (fakeSDKFamily) Command(runtime command.Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "status",
		RunE: func(*cobra.Command, []string) error {
			session, err := runtime.OpenSession()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(runtime.Stdout, "%s %s token-bytes=%d\n", session.Name, session.URL, len(session.Token))
			return err
		},
	}
}

type sdkProbeFamily struct{}

func (sdkProbeFamily) Name() string { return "probe" }

func (sdkProbeFamily) Command(runtime command.Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, _, err := runtime.OpenClient(true)
			if err != nil {
				return err
			}
			response, err := client.GetPublicStatusWithResponse(cmd.Context())
			if err != nil {
				return err
			}
			if response.JSON200 == nil {
				return fmt.Errorf("HTTP %d", response.StatusCode())
			}
			return nil
		},
	}
}

type unavailableSDKFamily struct{}

func (unavailableSDKFamily) Name() string { return "monitor" }

func (unavailableSDKFamily) Command(command.Runtime) *cobra.Command {
	return &cobra.Command{
		Use: "monitor",
		RunE: func(*cobra.Command, []string) error {
			return problem.ContractUnavailable("monitor")
		},
	}
}

func TestProfileSetDefaultsToKeyringAndWritesOnlySuccessToStdout(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	var stdout, stderr bytes.Buffer
	runner := command.Runner{Stdout: &stdout, Stderr: &stderr}

	exitCode := runner.Run(context.Background(), []string{
		"--config", configPath,
		"--output", "json",
		"profile", "set", "production",
		"--url", "https://xisnove.example.com/",
	})
	if exitCode != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exitCode, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	wantOutput := "{\n  \"current\": true,\n  \"name\": \"production\",\n  \"url\": \"https://xisnove.example.com\",\n  \"credential\": {\n    \"mode\": \"keyring\",\n    \"reference\": \"production\"\n  }\n}\n"
	if stdout.String() != wantOutput {
		t.Fatalf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", stdout.String(), wantOutput)
	}
	cfg, err := (config.Store{Path: configPath}).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CurrentProfile != "production" || cfg.Profiles["production"].Credential.Mode != config.CredentialKeyring {
		t.Fatalf("saved config = %#v", cfg)
	}
}

func TestProfileListIsDeterministicAndKeepsStderrEmpty(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	store := config.Store{Path: configPath}
	cfg := config.Config{
		Version:        1,
		CurrentProfile: "zeta",
		Profiles: map[string]config.Profile{
			"zeta":  {URL: "https://zeta.example", Credential: config.CredentialRef{Mode: config.CredentialKeyring, Reference: "zeta"}},
			"alpha": {URL: "https://alpha.example", Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "ALPHA_TOKEN"}},
		},
	}
	if err := store.Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{"--config", configPath, "profile", "list"})
	if exitCode != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exitCode, stderr.String())
	}
	want := "CURRENT  NAME   URL                    CREDENTIAL  REFERENCE\n         alpha  https://alpha.example  env         ALPHA_TOKEN\n*        zeta   https://zeta.example   keyring     zeta\n"
	if stdout.String() != want {
		t.Fatalf("stdout mismatch\n--- got ---\n%s--- want ---\n%s", stdout.String(), want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestProfileFileModeRequiresExplicitAbsoluteReference(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	var stdout, stderr bytes.Buffer
	exitCode := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{
		"--config", configPath,
		"profile", "set", "automation",
		"--url", "https://example.test",
		"--credential-mode", "file",
	})
	if exitCode != 2 {
		t.Fatalf("Run() exit = %d, want 2", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "--credential-ref is required for file mode") {
		t.Fatalf("stderr = %q", got)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config was written on failure: %v", err)
	}
}

func TestProfileSetReportsInvalidCredentialModeAsUsage(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	var stdout, stderr bytes.Buffer
	exitCode := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{
		"--config", configPath,
		"profile", "set", "automation",
		"--url", "https://example.test",
		"--credential-mode", "magic",
		"--credential-ref", "reference",
	})
	if exitCode != 2 {
		t.Fatalf("Run() exit = %d, want 2; stderr = %s", exitCode, stderr.String())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, "credential mode") {
		t.Fatalf("stderr = %q", got)
	}
}

func TestUnavailableSDKFamilyReturnsTypedProblemOnStderr(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := (command.Runner{Stdout: &stdout, Stderr: &stderr, Families: []command.Family{unavailableSDKFamily{}}}).Run(context.Background(), []string{
		"--output", "json", "monitor",
	})
	if exitCode != 1 {
		t.Fatalf("Run() exit = %d, want 1", exitCode)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	want := "{\n  \"type\": \"https://xisnove.dev/problems/contract-unavailable\",\n  \"title\": \"Command unavailable\",\n  \"status\": 501,\n  \"detail\": \"monitor commands require the frozen generated SDK contract\",\n  \"code\": \"contract_unavailable\"\n}\n"
	if stderr.String() != want {
		t.Fatalf("stderr mismatch\n--- got ---\n%s--- want ---\n%s", stderr.String(), want)
	}
}

func TestCommandSyntaxFailuresUseStableUsageExit(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"unknown"}},
		{name: "extra argument", args: []string{"profile", "list", "extra"}},
		{name: "unsupported output", args: []string{"--output", "toml", "profile", "list"}},
		{name: "missing URL", args: []string{"profile", "set", "local"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			exitCode := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), tt.args)
			if exitCode != 2 {
				t.Fatalf("Run() exit = %d, want 2; stderr = %s", exitCode, stderr.String())
			}
			if stdout.Len() != 0 {
				t.Fatalf("stdout = %q, want empty", stdout.String())
			}
			if !strings.HasPrefix(stderr.String(), "error: Invalid command usage:") {
				t.Fatalf("stderr = %q, want typed usage error", stderr.String())
			}
		})
	}
}

func TestHelpDeclaresAllHumanWorkflowFamilies(t *testing.T) {
	var stdout, stderr bytes.Buffer
	exitCode := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), []string{"--help"})
	if exitCode != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exitCode, stderr.String())
	}
	for _, family := range []string{"profile", "auth", "monitor", "location", "agent", "incident", "notification", "discovery", "maintenance", "status"} {
		if !strings.Contains(stdout.String(), family) {
			t.Fatalf("help does not contain %q\n%s", family, stdout.String())
		}
	}
}

func TestInjectedSDKFamilyOpensExplicitProfileThroughCredentialAbstraction(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	cfg := config.Config{
		Version:        1,
		CurrentProfile: "production",
		Profiles: map[string]config.Profile{
			"production": {URL: "https://production.example", Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "PROD_TOKEN"}},
			"staging":    {URL: "https://staging.example", Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "STAGING_TOKEN"}},
		},
	}
	if err := (config.Store{Path: configPath}).Save(cfg); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout:   &stdout,
		Stderr:   &stderr,
		Families: []command.Family{fakeSDKFamily{}},
		Credentials: credential.Resolver{LookupEnv: func(name string) (string, bool) {
			if name != "STAGING_TOKEN" {
				t.Fatalf("LookupEnv(%q), want STAGING_TOKEN", name)
			}
			return "staging-secret", true
		}},
	}
	exitCode := runner.Run(context.Background(), []string{"--config", configPath, "--profile", "staging", "status"})
	if exitCode != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exitCode, stderr.String())
	}
	if got := stdout.String(); got != "staging https://staging.example token-bytes=14\n" {
		t.Fatalf("stdout = %q", got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestRuntimeBuildsGeneratedSDKClientWithBearerEditor(t *testing.T) {
	authorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		authorization = request.Header.Get("Authorization")
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"activeIncidents":[],"generatedAt":"2026-07-25T12:00:00Z","monitors":[],"state":"up"}`))
	}))
	defer server.Close()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	if err := (config.Store{Path: configPath}).Save(config.Config{
		Version:        1,
		CurrentProfile: "mock",
		Profiles: map[string]config.Profile{
			"mock": {URL: server.URL, Credential: config.CredentialRef{Mode: config.CredentialEnv, Reference: "MOCK_TOKEN"}},
		},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	runner := command.Runner{
		Stdout:   &stdout,
		Stderr:   &stderr,
		Families: []command.Family{sdkProbeFamily{}},
		Credentials: credential.Resolver{LookupEnv: func(string) (string, bool) {
			return "bearer-value", true
		}},
	}
	if exit := runner.Run(context.Background(), []string{"--config", configPath, "probe"}); exit != 0 {
		t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
	}
	if authorization != "Bearer bearer-value" {
		t.Fatalf("Authorization = %q", authorization)
	}
}
