package architecture_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPublicPackagesRespectDependencyDirection(t *testing.T) {
	root := repositoryRoot(t)
	for _, packagePath := range []string{"./domain", "./application", "./application/port"} {
		command := exec.CommandContext(
			context.Background(),
			"go", "list", "-deps", "-f", "{{.ImportPath}}", packagePath,
		)
		command.Dir = root
		command.Env = append(os.Environ(), "GOWORK=off")
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go list %s: %v\n%s", packagePath, err, output)
		}
		for _, forbidden := range []string{
			"github.com/araihu/xisnove/internal/adapters",
			"github.com/araihu/xisnove/db/generated",
		} {
			if bytes.Contains(output, []byte(forbidden)) {
				t.Fatalf("%s depends on forbidden package prefix %s", packagePath, forbidden)
			}
		}
		if packagePath == "./domain" && bytes.Contains(
			output,
			[]byte("github.com/araihu/xisnove/application"),
		) {
			t.Fatal("domain depends on application")
		}
	}
}

func TestApplicationDoesNotDiscardIncomingContext(t *testing.T) {
	applicationDir := filepath.Join(repositoryRoot(t), "application")
	err := filepath.WalkDir(applicationDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, discarded := range []string{"context.Background()", "context.TODO()"} {
			if bytes.Contains(contents, []byte(discarded)) {
				t.Errorf("%s discards caller context with %s", path, discarded)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLegacyInternalPublicPackagesAreGone(t *testing.T) {
	root := repositoryRoot(t)
	for _, path := range []string{"internal/domain", "internal/application", "internal/adapters/conformance"} {
		if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
			t.Errorf("legacy package path still exists: %s", path)
		}
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate architecture test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}
