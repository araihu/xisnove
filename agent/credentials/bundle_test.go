package credentials_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/agent/credentials"
)

func TestFileProviderReadsCoherentBundleAfterReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	writeBundle(t, path, `{"credential":"first-credential","generation":7}`)
	provider := credentials.FileProvider{Path: path}

	first, err := provider.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first != (credentials.Bundle{Credential: "first-credential", Generation: 7}) {
		t.Fatalf("first bundle = %#v", first)
	}

	replacement := filepath.Join(filepath.Dir(path), "replacement.json")
	writeBundle(t, replacement, `{"credential":"second-credential","generation":8}`)
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	second, err := provider.Current(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second != (credentials.Bundle{Credential: "second-credential", Generation: 8}) {
		t.Fatalf("second bundle = %#v", second)
	}
}

func TestFileProviderRejectsInvalidBundlesWithoutLeakingCredential(t *testing.T) {
	secret := "do-not-log-this-agent-credential"
	tests := map[string]string{
		"partial write":      `{"credential":"` + secret,
		"invalid JSON":       `{"credential":` + secret + `,"generation":2}`,
		"missing credential": `{"generation":2}`,
		"missing generation": `{"credential":"` + secret + `"}`,
		"zero generation":    `{"credential":"` + secret + `","generation":0}`,
	}
	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "current.json")
			writeBundle(t, path, contents)
			_, err := (credentials.FileProvider{Path: path}).Current(context.Background())
			if err == nil {
				t.Fatal("expected error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaked credential: %v", err)
			}
		})
	}
}

func TestFileProviderRejectsOversizedBundle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	contents := `{"credential":"` + strings.Repeat("a", credentials.MaxBundleSize) + `","generation":1}`
	writeBundle(t, path, contents)
	_, err := (credentials.FileProvider{Path: path}).Current(context.Background())
	if !errors.Is(err, credentials.ErrBundleTooLarge) {
		t.Fatalf("error = %v", err)
	}
}

func TestFileProviderReturnsContextCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	writeBundle(t, path, `{"credential":"credential","generation":1}`)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (credentials.FileProvider{Path: path}).Current(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v", err)
	}
}

func TestFileProviderReportsPermissionErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "current.json")
	writeBundle(t, path, `{"credential":"credential","generation":1}`)
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
	_, err := (credentials.FileProvider{Path: path}).Current(context.Background())
	if err == nil {
		t.Skip("test process can read mode 000 files")
	}
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
}

func writeBundle(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
