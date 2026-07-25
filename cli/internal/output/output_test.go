package output_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/xisnove/cli/internal/output"
)

type fixture struct {
	Name   string            `json:"name"`
	State  string            `json:"state"`
	Count  int               `json:"count"`
	Labels map[string]string `json:"labels"`
}

func TestRendererMatchesStableGoldens(t *testing.T) {
	value := fixture{Name: "edge-api", State: "degraded", Count: 2, Labels: map[string]string{"zone": "south", "team": "platform"}}
	table := output.Table{
		Headers: []string{"NAME", "STATE"},
		Rows: [][]string{
			{"worker", "up"},
			{"edge-api", "degraded"},
			{"database", "down"},
		},
	}

	tests := []struct {
		format output.Format
		golden string
	}{
		{format: output.TableFormat, golden: "table.golden"},
		{format: output.JSONFormat, golden: "json.golden"},
		{format: output.YAMLFormat, golden: "yaml.golden"},
	}
	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			var got bytes.Buffer
			err := (output.Renderer{Writer: &got, Format: tt.format}).Render(value, table)
			if err != nil {
				t.Fatalf("Render() error = %v", err)
			}
			want, err := os.ReadFile(filepath.Join("testdata", tt.golden))
			if err != nil {
				t.Fatalf("ReadFile(golden) error = %v", err)
			}
			if got.String() != string(want) {
				t.Fatalf("output mismatch\n--- got ---\n%s--- want ---\n%s", got.String(), want)
			}
		})
	}
}

func TestRendererRejectsUnknownFormatWithoutWriting(t *testing.T) {
	var got bytes.Buffer
	err := (output.Renderer{Writer: &got, Format: "toml"}).Render(struct{}{}, output.Table{})
	if err == nil {
		t.Fatal("Render() error = nil, want unsupported format error")
	}
	if got.Len() != 0 {
		t.Fatalf("Render() wrote %q before returning error", got.String())
	}
}
