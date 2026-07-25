package api_test

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
)

func TestGeneratedAPIBindingsAreCurrent(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct {
		name   string
		config string
		target string
	}{
		{"control-plane strict server", "oapi-codegen-server.yaml", "../internal/adapters/httpapi/generated.gen.go"},
		{"public SDK", "oapi-codegen-sdk.yaml", "../sdk/generated.gen.go"},
		{"Agent subset", "oapi-codegen-agent.yaml", "../agent/internal/controlplane/generated.gen.go"},
		{"full strict contract", "oapi-codegen-strict-contract.yaml", "../internal/mockapi/generated.gen.go"},
	} {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			assertGeneratedFileCurrent(t, fixture.config, fixture.target)
		})
	}
}

func assertGeneratedFileCurrent(t *testing.T, configPath, targetPath string) {
	t.Helper()
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.ReadFile(targetPath)
	if err != nil {
		t.Fatal(err)
	}

	generatedPath := filepath.Join(t.TempDir(), "generated.gen.go")
	outputPattern := regexp.MustCompile(`(?m)^output: .+$`)
	config = outputPattern.ReplaceAll(
		config,
		[]byte(fmt.Sprintf("output: %q", generatedPath)),
	)
	temporaryConfig := filepath.Join(t.TempDir(), "oapi-codegen.yaml")
	if err := os.WriteFile(temporaryConfig, config, 0o600); err != nil {
		t.Fatal(err)
	}

	command := exec.Command(
		"go", "tool", "oapi-codegen",
		"-config", temporaryConfig,
		"openapi.yaml",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generate with %s: %v\n%s", configPath, err, output)
	}
	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, target) {
		t.Fatalf("%s is stale; regenerate it with %s", targetPath, configPath)
	}
}
