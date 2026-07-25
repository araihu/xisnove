package architecture_test

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIImportsOnlyThePublicSDKFromXisnove(t *testing.T) {
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	cmd := exec.Command("go", "list", "-json", "./...")
	cmd.Dir = moduleRoot
	cmd.Env = append(cmd.Environ(), "GOWORK=off")
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for decoder.More() {
		var pkg struct {
			ImportPath string
			Imports    []string
		}
		if err := decoder.Decode(&pkg); err != nil {
			t.Fatalf("decode go list: %v", err)
		}
		for _, imported := range pkg.Imports {
			if strings.HasPrefix(imported, "github.com/araihu/xisnove/") &&
				!strings.HasPrefix(imported, "github.com/araihu/xisnove/cli/") &&
				imported != "github.com/araihu/xisnove/sdk" {
				t.Errorf("%s imports forbidden Xisnove package %s", pkg.ImportPath, imported)
			}
			if imported == "database/sql" || strings.Contains(imported, "sqlc") {
				t.Errorf("%s imports forbidden database package %s", pkg.ImportPath, imported)
			}
		}
	}
}
