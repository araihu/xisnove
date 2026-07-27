package contract_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDistributionDocumentsFreezeRequiredContracts(t *testing.T) {
	required := map[string][]string{
		"versioning.md":              {"vX.Y.Z", "SOURCE_DATE_EPOCH", "release.version", "dirty tree", "sha-<commit>"},
		"compatibility.md":           {"semantic versioning", "OpenAPI", "N-1", "expand", "contract", "rollback"},
		"artifact-matrix.md":         {"xisnove-server", "xisnove-ui", "xisnove-agent", "xisnove-operator", "xisnove-edge"},
		"runtime-contracts.md":       {"/livez", "/readyz", "/metrics", "SIGTERM", "--version", "writable"},
		"database-profile-matrix.md": {"SQLite", "local Turso", "PostgreSQL", "managed Turso", "singleton", "replica-safe"},
		"secret-reference-matrix.md": {"cursor", "notification", "cookie", "administrator", "Agent", "owner-only"},
	}
	for name, fragments := range required {
		contents, err := os.ReadFile(filepath.Join(distributionRoot(t), "docs", "distribution", name))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		text := string(contents)
		for _, fragment := range fragments {
			if !strings.Contains(text, fragment) {
				t.Errorf("%s missing %q", name, fragment)
			}
		}
	}
}
