package journey_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/cli/internal/command"
	"github.com/araihu/xisnove/sdk"
)

const frozenMockPackage = "github.com/araihu/xisnove/cmd/xisnove-mock@v0.0.0-20260726121002-9741fed1ef08"

func TestFrozenMockHumanJourney(t *testing.T) {
	baseURL, stop := startFrozenMock(t)
	defer stop()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.yaml")
	tokenPath := filepath.Join(dir, "session.token")
	run := func(stdin string, args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		runner := command.Runner{Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr}
		return runner.Run(context.Background(), append([]string{"--config", configPath}, args...)), stdout.String(), stderr.String()
	}

	if exit, _, stderr := run("", "profile", "set", "mock", "--url", baseURL, "--credential-mode", "file", "--credential-ref", tokenPath); exit != 0 {
		t.Fatalf("profile set exit = %d, stderr = %s", exit, stderr)
	}
	if exit, stdout, stderr := run("mock-password\n", "--output", "json", "auth", "login", "--email", "admin@xisnove.test", "--password-stdin"); exit != 0 {
		t.Fatalf("auth login exit = %d, stderr = %s", exit, stderr)
	} else if strings.Contains(stdout+stderr, "xisnove_mock_session") {
		t.Fatalf("session credential leaked: stdout=%s stderr=%s", stdout, stderr)
	}

	if exit, stdout, stderr := run("", "--output", "json", "monitor", "list", "--limit", "1"); exit != 0 {
		t.Fatalf("monitor list exit = %d, stderr = %s", exit, stderr)
	} else if !strings.Contains(stdout, "homelab router") || !strings.Contains(stdout, `"page"`) {
		t.Fatalf("monitor list stdout = %s", stdout)
	}

	requestPath := filepath.Join(dir, "monitor.json")
	request := `{
  "failureThreshold":3,
  "intervalSeconds":60,
  "locationId":"00000000-0000-4000-8000-000000000001",
  "name":"CLI journey monitor",
  "probe":{"body":"","bodyContains":[],"bodyDoesNotContain":[],"expectedStatus":[{"maximum":299,"minimum":200}],"followRedirects":true,"headers":{},"kind":"http","method":"GET","url":"https://journey.example.test/health"},
  "recoveryThreshold":2,
  "requiredLocation":true,
  "timeoutMillis":5000
}`
	if err := os.WriteFile(requestPath, []byte(request), 0o600); err != nil {
		t.Fatalf("WriteFile(request) error = %v", err)
	}
	if exit, stdout, stderr := run("", "--output", "json", "monitor", "create", "--file", requestPath, "--idempotency-key", "cli-journey-monitor-1"); exit != 0 {
		t.Fatalf("monitor create exit = %d, stderr = %s", exit, stderr)
	} else if !strings.Contains(stdout, "CLI journey monitor") || stderr != "" {
		t.Fatalf("monitor create stdout=%s stderr=%s", stdout, stderr)
	}

	for _, check := range []struct {
		args []string
		want string
	}{
		{args: []string{"incident", "list"}, want: "critical"},
		{args: []string{"discovery", "list"}, want: "router metrics"},
		{args: []string{"notification", "channel", "list"}, want: "fixture Alertmanager"},
		{args: []string{"status"}, want: "down"},
	} {
		if exit, stdout, stderr := run("", check.args...); exit != 0 {
			t.Fatalf("%v exit = %d, stderr = %s", check.args, exit, stderr)
		} else if !strings.Contains(stdout, check.want) {
			t.Fatalf("%v stdout = %s, want %q", check.args, stdout, check.want)
		}
	}

	var stdout, stderr bytes.Buffer
	scenarioFactory := func(server string, options ...sdk.ClientOption) (*sdk.ClientWithResponses, error) {
		options = append(options, sdk.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("X-Xisnove-Mock-Scenario", "rate-limit")
			return nil
		}))
		return sdk.NewClientWithResponses(server, options...)
	}
	exit := (command.Runner{Stdout: &stdout, Stderr: &stderr, ClientFactory: scenarioFactory}).Run(context.Background(), []string{"--config", configPath, "--output", "json", "status"})
	if exit != 7 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `"code": "mock_rate_limit"`) || !strings.Contains(stderr.String(), `"correlationId"`) {
		t.Fatalf("rate limit exit=%d stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}

	if exit, _, stderr := run("", "auth", "logout"); exit != 0 {
		t.Fatalf("auth logout exit = %d, stderr = %s", exit, stderr)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("session token still exists: %v", err)
	}
}

func startFrozenMock(t *testing.T) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve mock port: %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release mock port: %v", err)
	}
	binDir := t.TempDir()
	binary := filepath.Join(binDir, "xisnove-mock")
	build := exec.Command("go", "install", frozenMockPackage)
	build.Env = append(build.Environ(), "GOWORK=off", "GOBIN="+binDir)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build frozen mock: %v\n%s", err, output)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var logs bytes.Buffer
	process := exec.CommandContext(ctx, binary, "-listen", address)
	process.Env = append(process.Environ(), "GOWORK=off")
	process.Stdout = &logs
	process.Stderr = &logs
	if err := process.Start(); err != nil {
		cancel()
		t.Fatalf("start frozen mock: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	baseURL := "http://" + address
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(45 * time.Second)
	for {
		response, requestErr := client.Get(baseURL + "/v1/status-page")
		if requestErr == nil {
			response.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatalf("mock readiness timeout: %v\n%s", requestErr, logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	return baseURL, func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Errorf("mock did not stop\n%s", logs.String())
		}
	}
}

func Example_frozenMockPackage() {
	fmt.Println(frozenMockPackage)
	// Output: github.com/araihu/xisnove/cmd/xisnove-mock@v0.0.0-20260726121002-9741fed1ef08
}
