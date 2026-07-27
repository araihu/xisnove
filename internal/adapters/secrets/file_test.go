package secrets_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/secrets"
)

func TestFileResolverTrimsOneFinalNewlineOnly(t *testing.T) {
	path := writeSecret(t, "  value  \n\n", 0o600)
	resolved, err := (secrets.FileResolver{}).Resolve(
		context.Background(),
		port.SecretReference{Kind: port.SecretReferenceFile, Locator: path},
	)
	if err != nil || string(resolved) != "  value  \n" {
		t.Fatalf("Resolve() = %q, %v", resolved, err)
	}
	resolved[0] = 'x'
	again, err := (secrets.FileResolver{}).Resolve(context.Background(), port.SecretReference{
		Kind: port.SecretReferenceFile, Locator: path,
	})
	if err != nil || string(again) != "  value  \n" {
		t.Fatalf("second Resolve() = %q, %v", again, err)
	}
}

func TestFileResolverAcceptsProjectedReadOnlySecret(t *testing.T) {
	target := writeSecret(t, "projected-value\n", 0o640)
	link := filepath.Join(t.TempDir(), "secret")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolved, err := (secrets.FileResolver{}).Resolve(context.Background(), port.SecretReference{
		Kind: port.SecretReferenceFile, Locator: link,
	})
	if err != nil || string(resolved) != "projected-value" {
		t.Fatalf("Resolve() = %q, %v", resolved, err)
	}
}

func TestFileResolverRejectsUnsafeFilesWithoutLeakingContent(t *testing.T) {
	secret := "must-not-appear-in-errors"
	tests := []struct {
		name string
		path func(*testing.T) string
	}{
		{"empty", func(t *testing.T) string { return writeSecret(t, "\n", 0o600) }},
		{"permissive", func(t *testing.T) string { return writeSecret(t, secret, 0o644) }},
		{"oversized", func(t *testing.T) string { return writeSecret(t, strings.Repeat("x", 64<<10+1), 0o600) }},
		{"directory", func(t *testing.T) string { return t.TempDir() }},
		{"group-writable", func(t *testing.T) string { return writeSecret(t, secret, 0o660) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (secrets.FileResolver{}).Resolve(
				context.Background(), port.SecretReference{
					Kind: port.SecretReferenceFile, Locator: test.path(t),
				},
			)
			if err == nil {
				t.Fatal("Resolve() error = nil")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("Resolve() leaked secret: %v", err)
			}
		})
	}
}

func TestFileResolverHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (secrets.FileResolver{}).Resolve(ctx, port.SecretReference{
		Kind: port.SecretReferenceFile, Locator: "unused",
	})
	if err == nil {
		t.Fatal("Resolve() error = nil")
	}
}

func TestFileResolverRejectsOtherReferenceKinds(t *testing.T) {
	_, err := (secrets.FileResolver{}).Resolve(context.Background(), port.SecretReference{
		Kind: "vault", Locator: "secret/notifications/primary",
	})
	if err == nil || strings.Contains(err.Error(), "secret/notifications/primary") {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func writeSecret(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
