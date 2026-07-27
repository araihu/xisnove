package release_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCanonicalManifestSchemaIsClosedAndVersioned(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(repositoryRoot(t), "build/release/candidate-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		ID                   string                     `json:"$id"`
		AdditionalProperties bool                       `json:"additionalProperties"`
		Required             []string                   `json:"required"`
		Properties           map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.ID != "https://xisnove.dev/schemas/release-candidate-manifest-v1.json" {
		t.Fatalf("schema ID = %q", schema.ID)
	}
	if schema.AdditionalProperties {
		t.Fatal("candidate manifest schema must reject unknown top-level fields")
	}
	for _, required := range []string{"schemaVersion", "repository", "commit", "version", "sourceDateEpoch", "subjects"} {
		if _, ok := schema.Properties[required]; !ok || !contains(schema.Required, required) {
			t.Errorf("candidate manifest schema does not require %q", required)
		}
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", "..", ".."))
}
