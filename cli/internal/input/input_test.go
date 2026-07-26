package input_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/cli/internal/input"
	"github.com/araihu/xisnove/sdk"
)

func TestDecodeFileUsesGeneratedSDKModelsForJSONAndYAML(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "json", contents: `{"name":"edge-south"}`},
		{name: "yaml", contents: "name: edge-south\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "request."+tt.name)
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			var request sdk.CreateLocationRequest
			if err := input.DecodeFile(path, nil, &request); err != nil {
				t.Fatalf("DecodeFile() error = %v", err)
			}
			if request.Name != "edge-south" {
				t.Fatalf("request = %#v", request)
			}
		})
	}
}

func TestDecodeFileRejectsUnknownGeneratedModelFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(path, []byte(`{"name":"edge","invented":true}`), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	var request sdk.CreateLocationRequest
	err := input.DecodeFile(path, nil, &request)
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeFile() error = %v, want unknown field", err)
	}
}

func TestDecodeFileReadsDashFromBoundedStdin(t *testing.T) {
	var request sdk.CreateLocationRequest
	if err := input.DecodeFile("-", strings.NewReader("name: stdin-location\n"), &request); err != nil {
		t.Fatalf("DecodeFile() error = %v", err)
	}
	if request.Name != "stdin-location" {
		t.Fatalf("request = %#v", request)
	}
}
