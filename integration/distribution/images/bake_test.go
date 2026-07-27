package images_test

import (
	"encoding/json"
	"os/exec"
	"testing"
)

func TestBakeExposesProductionAndVerificationTargets(t *testing.T) {
	command := exec.Command("docker", "buildx", "bake", "--print", "default", "test-amd64", "test-arm64", "oci-layout")
	command.Dir = "../../.."
	output, err := command.Output()
	if err != nil {
		t.Fatalf("docker buildx bake --print: %v", err)
	}

	var definition struct {
		Group  map[string]json.RawMessage `json:"group"`
		Target map[string]json.RawMessage `json:"target"`
	}
	if err := json.Unmarshal(output, &definition); err != nil {
		t.Fatalf("decode bake definition: %v\n%s", err, output)
	}
	for _, name := range []string{"server", "ui", "agent", "operator"} {
		if _, ok := definition.Target[name]; !ok {
			t.Errorf("missing production target %q", name)
		}
	}
	for _, name := range []string{"default", "test-amd64", "test-arm64", "oci-layout"} {
		if _, ok := definition.Group[name]; !ok {
			t.Errorf("missing verification group %q", name)
		}
	}
}
