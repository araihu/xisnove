package command_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/araihu/xisnove/cli/internal/command"
	"github.com/araihu/xisnove/cli/internal/credential"
)

func TestGeneratedSDKListFamiliesRenderTypedResponses(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		response string
		contains string
	}{
		{
			name: "locations", args: []string{"location", "list"}, contains: "edge-south",
			response: `{"items":[{"createdAt":"2026-07-25T12:00:00Z","enabled":true,"id":"00000000-0000-4000-8000-000000000001","name":"edge-south","updatedAt":"2026-07-25T12:00:00Z"}]}`,
		},
		{
			name: "agents", args: []string{"agent", "list"}, contains: "probe-1",
			response: `{"items":[{"capabilities":["http"],"createdAt":"2026-07-25T12:00:00Z","credentialGeneration":1,"enabled":true,"id":"00000000-0000-4800-8000-000000000801","locationId":"00000000-0000-4000-8000-000000000001","name":"probe-1","updatedAt":"2026-07-25T12:00:00Z","version":"1.0.0"}]}`,
		},
		{
			name: "incidents", args: []string{"incident", "list"}, contains: "critical",
			response: `{"items":[{"id":"00000000-0000-4300-8000-000000000201","lastTransitionAt":"2026-07-25T12:00:00Z","monitorId":"00000000-0000-4200-8000-000000000101","openedAt":"2026-07-25T12:00:00Z","severity":"critical","state":"open"}]}`,
		},
		{
			name: "discovery", args: []string{"discovery", "list"}, contains: "router metrics",
			response: `{"items":[{"agentId":"00000000-0000-4800-8000-000000000801","externalId":"service/router","firstSeenAt":"2026-07-25T12:00:00Z","id":"00000000-0000-4400-8000-000000000401","kind":"http","labels":{},"lastSeenAt":"2026-07-25T12:00:00Z","locationId":"00000000-0000-4000-8000-000000000001","name":"router metrics","state":"pending","target":"https://router.example.test/metrics"}]}`,
		},
		{
			name: "notification channels", args: []string{"notification", "channel", "list"}, contains: "Alertmanager",
			response: `{"items":[{"createdAt":"2026-07-25T12:00:00Z","enabled":true,"id":"00000000-0000-4500-8000-000000000501","kind":"alertmanager","name":"Alertmanager","updatedAt":"2026-07-25T12:00:00Z"}],"limit":50,"offset":0}`,
		},
		{
			name: "notification routes", args: []string{"notification", "route", "list"}, contains: "critical incidents",
			response: `{"items":[{"actions":["open"],"channelId":"00000000-0000-4500-8000-000000000501","createdAt":"2026-07-25T12:00:00Z","enabled":true,"id":"00000000-0000-4600-8000-000000000601","labelMatchers":{},"name":"critical incidents","precedence":10,"severities":["critical"],"template":"incident","updatedAt":"2026-07-25T12:00:00Z"}],"limit":50,"offset":0}`,
		},
		{
			name: "maintenance", args: []string{"maintenance", "list"}, contains: "planned upgrade",
			response: `{"items":[{"createdAt":"2026-07-25T12:00:00Z","id":"00000000-0000-4900-8000-000000000901","monitorId":"00000000-0000-4200-8000-000000000101","reason":"planned upgrade","startsAt":"2026-07-26T12:00:00Z","updatedAt":"2026-07-25T12:00:00Z"}],"limit":50,"offset":0}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if got := request.Header.Get("Authorization"); got != "Bearer mock-token" {
					t.Errorf("Authorization = %q", got)
				}
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(tt.response))
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
			args := append([]string{"--config", configPath}, tt.args...)
			if exit := runner.Run(context.Background(), args); exit != 0 {
				t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
			}
			if !strings.Contains(stdout.String(), tt.contains) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), tt.contains)
			}
		})
	}
}

func TestRemoteFamilyCommandTopologyCoversFrozenSDKWorkflows(t *testing.T) {
	tests := []struct {
		parent   []string
		commands []string
	}{
		{parent: []string{"auth"}, commands: []string{"login", "logout", "token"}},
		{parent: []string{"auth", "token"}, commands: []string{"list", "get", "create", "update", "revoke"}},
		{parent: []string{"monitor"}, commands: []string{"list", "get", "create", "update", "disable", "health", "incident"}},
		{parent: []string{"location"}, commands: []string{"list", "get", "create", "update", "disable"}},
		{parent: []string{"agent"}, commands: []string{"list", "get", "update", "revoke", "rotate-credential", "revoke-generation", "enrollment-token"}},
		{parent: []string{"incident"}, commands: []string{"list", "get", "events"}},
		{parent: []string{"discovery"}, commands: []string{"list", "get", "promote"}},
		{parent: []string{"notification", "channel"}, commands: []string{"list", "get", "create", "update", "disable"}},
		{parent: []string{"notification", "route"}, commands: []string{"list", "get", "create", "update", "disable"}},
		{parent: []string{"notification", "delivery"}, commands: []string{"list", "get", "replay"}},
		{parent: []string{"maintenance"}, commands: []string{"list", "get", "create", "end", "delete"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.parent, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := append(append([]string(nil), tt.parent...), "--help")
			if exit := (command.Runner{Stdout: &stdout, Stderr: &stderr}).Run(context.Background(), args); exit != 0 {
				t.Fatalf("Run() exit = %d, stderr = %s", exit, stderr.String())
			}
			for _, name := range tt.commands {
				if !strings.Contains(stdout.String(), name) {
					t.Errorf("help missing %q\n%s", name, stdout.String())
				}
			}
		})
	}
}
