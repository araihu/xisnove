//go:build browser

package browser_test

import (
	"bufio"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/araihu/xisnove/ui/internal/controlplane"
	"github.com/araihu/xisnove/ui/internal/web"
)

func TestFoundationBrowserSmoke(t *testing.T) {
	baseURL := strings.TrimRight(os.Getenv("XISNOVE_UI_BROWSER_BASE_URL"), "/")
	if baseURL == "" {
		handler, err := web.New(web.Config{
			ControlPlane:   controlplane.NewFake("browser-admin", "browser-password", "browser-session"),
			CookieSecret:   []byte("0123456789abcdef0123456789abcdef"),
			CookieSecure:   true,
			RequestTimeout: 5 * time.Second,
		})
		if err != nil {
			t.Fatalf("create UI handler: %v", err)
		}
		server := httptest.NewTLSServer(handler)
		t.Cleanup(server.Close)
		baseURL = server.URL
	}
	browser := browserBinary(t)
	profile := t.TempDir()
	screenshotDir := os.Getenv("XISNOVE_UI_BROWSER_SCREENSHOT_DIR")
	if screenshotDir != "" {
		if err := os.MkdirAll(screenshotDir, 0o755); err != nil {
			t.Fatalf("create screenshot directory: %v", err)
		}
	}

	for _, page := range []struct {
		name string
		path string
		want []string
	}{
		{name: "login", path: "/login", want: []string{`id="login-content"`, `type="password"`, `/assets/styles.css`}},
		{name: "status", path: "/status", want: []string{`id="status-content"`, `aria-live="polite"`, `Status surface ready`}},
	} {
		t.Run(page.name, func(t *testing.T) {
			dom := dumpDOM(t, browser, profile, baseURL+page.path)
			for _, want := range page.want {
				if !strings.Contains(dom, want) {
					t.Errorf("browser DOM missing %q", want)
				}
			}
			for _, secret := range []string{"browser-admin", "browser-password", "browser-session"} {
				if strings.Contains(dom, secret) {
					t.Errorf("browser DOM exposed server-side fixture %q", secret)
				}
			}
			if screenshotDir != "" {
				captureScreenshot(t, browser, profile, filepath.Join(screenshotDir, page.name+".png"), baseURL+page.path)
			}
		})
	}
	if !t.Failed() {
		t.Logf("browser smoke passed with %s against %s", browser, baseURL)
	}
}

func browserBinary(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("XISNOVE_UI_BROWSER_BIN"); configured != "" {
		return configured
	}
	for _, candidate := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if path, err := exec.LookPath(candidate); err == nil {
			return path
		}
	}
	const macChrome = "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	if _, err := os.Stat(macChrome); err == nil {
		return macChrome
	}
	t.Fatal("no Chromium browser found; set XISNOVE_UI_BROWSER_BIN")
	return ""
}

func dumpDOM(t *testing.T, browser, profile, pageURL string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	args := []string{
		"--headless=new",
		"--disable-background-networking",
		"--disable-extensions",
		"--disable-gpu",
		"--ignore-certificate-errors",
		"--no-default-browser-check",
		"--no-first-run",
		"--no-sandbox",
		"--virtual-time-budget=2000",
		"--user-data-dir=" + profile,
		"--window-size=1440,900",
		"--dump-dom",
	}
	args = append(args, pageURL)
	command := exec.CommandContext(ctx, browser, args...)
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("open browser stdout: %v", err)
	}
	stderrPath, stderrFile := browserStderrFile(t)
	command.Stderr = stderrFile
	if err := command.Start(); err != nil {
		t.Fatalf("start browser: %v", err)
	}

	type domResult struct {
		dom      string
		complete bool
		err      error
	}
	resultCh := make(chan domResult, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		var dom strings.Builder
		for scanner.Scan() {
			dom.WriteString(scanner.Text())
			dom.WriteByte('\n')
			if strings.Contains(dom.String(), "</html>") {
				resultCh <- domResult{dom: dom.String(), complete: true}
				return
			}
		}
		resultCh <- domResult{dom: dom.String(), err: scanner.Err()}
	}()

	var result domResult
	select {
	case result = <-resultCh:
	case <-ctx.Done():
		_ = command.Process.Kill()
		_ = command.Wait()
		closeBrowserStderr(t, stderrFile)
		t.Fatalf("browser timed out waiting for complete DOM: %v\nstderr:\n%s", ctx.Err(), readBrowserStderr(t, stderrPath))
	}
	if result.complete {
		_ = command.Process.Signal(os.Interrupt)
	}
	waitErr := command.Wait()
	closeBrowserStderr(t, stderrFile)
	if !result.complete {
		t.Fatalf("browser exited before complete DOM: read error=%v wait error=%v\nstderr:\n%s", result.err, waitErr, readBrowserStderr(t, stderrPath))
	}
	return result.dom
}

func captureScreenshot(t *testing.T, browser, profile, outputPath, pageURL string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	if err := os.Remove(outputPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove stale screenshot: %v", err)
	}
	command := exec.CommandContext(ctx, browser,
		"--headless=new",
		"--disable-background-networking",
		"--disable-extensions",
		"--disable-gpu",
		"--ignore-certificate-errors",
		"--no-default-browser-check",
		"--no-first-run",
		"--no-sandbox",
		"--run-all-compositor-stages-before-draw",
		"--user-data-dir="+profile,
		"--window-size=1440,900",
		"--screenshot="+outputPath,
		pageURL,
	)
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open null output: %v", err)
	}
	defer devNull.Close()
	stderrPath, stderrFile := browserStderrFile(t)
	command.Stdout = devNull
	command.Stderr = stderrFile
	if err := command.Start(); err != nil {
		t.Fatalf("start screenshot browser: %v", err)
	}
	waitCh := make(chan error, 1)
	go func() { waitCh <- command.Wait() }()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case waitErr := <-waitCh:
			closeBrowserStderr(t, stderrFile)
			if screenshotReady(outputPath) {
				return
			}
			t.Fatalf("browser exited before writing screenshot: %v\nstderr:\n%s", waitErr, readBrowserStderr(t, stderrPath))
		case <-ticker.C:
			if screenshotReady(outputPath) {
				_ = command.Process.Signal(os.Interrupt)
				<-waitCh
				closeBrowserStderr(t, stderrFile)
				return
			}
		case <-ctx.Done():
			_ = command.Process.Kill()
			<-waitCh
			closeBrowserStderr(t, stderrFile)
			t.Fatalf("browser timed out waiting for screenshot: %v\nstderr:\n%s", ctx.Err(), readBrowserStderr(t, stderrPath))
		}
	}
}

func screenshotReady(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Size() > 0
}

func browserStderrFile(t *testing.T) (string, *os.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chromium-stderr.log")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create browser stderr file: %v", err)
	}
	return path, file
}

func closeBrowserStderr(t *testing.T, file *os.File) {
	t.Helper()
	if err := file.Close(); err != nil {
		t.Fatalf("close browser stderr: %v", err)
	}
}

func readBrowserStderr(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read browser stderr: %v", err)
	}
	return string(content)
}
