package contract_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

type toolchainManifest struct {
	SchemaVersion int `json:"schema_version"`
	Actions       []struct {
		Name string `json:"name"`
		SHA  string `json:"sha"`
	} `json:"actions"`
	Tools []struct {
		Name      string            `json:"name"`
		Version   string            `json:"version"`
		Checksums map[string]string `json:"checksums"`
	} `json:"tools"`
	GoModuleTools []struct {
		Name         string `json:"name"`
		Module       string `json:"module"`
		Version      string `json:"version"`
		Verification string `json:"verification"`
	} `json:"go_module_tools"`
	Images []struct {
		Name   string `json:"name"`
		Use    string `json:"use"`
		Digest string `json:"digest"`
	} `json:"images"`
	ScannerDatabase struct {
		Mode             string `json:"mode"`
		MaximumAgeHours  int    `json:"maximum_age_hours"`
		RecordResolvedAt bool   `json:"record_resolved_at"`
		OfflineFallback  string `json:"offline_fallback"`
	} `json:"scanner_database"`
	VulnerabilityExceptionSchema struct {
		Required            []string `json:"required"`
		MaximumLifetimeDays int      `json:"maximum_lifetime_days"`
		ExpiredPolicy       string   `json:"expired_policy"`
	} `json:"vulnerability_exception_schema"`
	VulnerabilityExceptions []struct {
		ID        string `json:"id"`
		ExpiresAt string `json:"expires_at"`
		Reason    string `json:"reason"`
		Owner     string `json:"owner"`
	} `json:"vulnerability_exceptions"`
}

func TestToolchainPinsAreImmutableAndComplete(t *testing.T) {
	contents, err := os.ReadFile(filepath.Join(distributionRoot(t), "build", "release", "toolchain.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest toolchainManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", manifest.SchemaVersion)
	}
	sha := regexp.MustCompile(`^[0-9a-f]{40}$`)
	digest := regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	checksum := regexp.MustCompile(`^[0-9a-f]{64}$`)

	wantActions := set("actions/checkout", "actions/setup-go", "actions/upload-artifact")
	for _, pin := range manifest.Actions {
		if !sha.MatchString(pin.SHA) {
			t.Errorf("action %q SHA %q is not immutable", pin.Name, pin.SHA)
		}
		delete(wantActions, pin.Name)
	}
	if len(wantActions) != 0 {
		t.Errorf("missing action pins: %v", wantActions)
	}

	wantTools := set("go", "helm", "kind", "kubectl", "gotestsum", "goreleaser", "cosign", "syft", "trivy")
	for _, pin := range manifest.Tools {
		if pin.Version == "" || len(pin.Checksums) == 0 {
			t.Errorf("tool %q must pin version and release checksums", pin.Name)
		}
		for _, target := range []string{"linux-amd64", "linux-arm64"} {
			if _, ok := pin.Checksums[target]; !ok {
				t.Errorf("tool %q missing %s checksum", pin.Name, target)
			}
		}
		for target, value := range pin.Checksums {
			if !checksum.MatchString(value) {
				t.Errorf("tool %q target %q checksum %q is invalid", pin.Name, target, value)
			}
		}
		delete(wantTools, pin.Name)
	}
	if len(wantTools) != 0 {
		t.Errorf("missing tool pins: %v", wantTools)
	}
	wantModuleTools := set("vacuum", "oapi-codegen", "oasdiff", "sqlc", "templ", "setup-envtest", "controller-gen")
	for _, pin := range manifest.GoModuleTools {
		if pin.Module == "" || pin.Version == "" || pin.Verification != "go.sum" {
			t.Errorf("Go module tool %q is not versioned through go.sum: %#v", pin.Name, pin)
		}
		delete(wantModuleTools, pin.Name)
	}
	if len(wantModuleTools) != 0 {
		t.Errorf("missing Go module tool pins: %v", wantModuleTools)
	}

	wantImageUses := set("builder", "runtime-base", "database-service")
	for _, pin := range manifest.Images {
		if !digest.MatchString(pin.Digest) {
			t.Errorf("image %q digest %q is not immutable", pin.Name, pin.Digest)
		}
		delete(wantImageUses, pin.Use)
	}
	if len(wantImageUses) != 0 {
		t.Errorf("missing image pin uses: %v", wantImageUses)
	}

	policy := manifest.ScannerDatabase
	if policy.Mode != "pinned-snapshot" || policy.MaximumAgeHours <= 0 || !policy.RecordResolvedAt || policy.OfflineFallback == "" {
		t.Errorf("scanner_database policy incomplete: %#v", policy)
	}
	schema := manifest.VulnerabilityExceptionSchema
	wantFields := set("id", "package", "vulnerability", "reason", "owner", "approved_at", "expires_at")
	for _, field := range schema.Required {
		delete(wantFields, field)
	}
	if len(wantFields) != 0 || schema.MaximumLifetimeDays <= 0 || schema.ExpiredPolicy != "fail" {
		t.Errorf("vulnerability_exception_schema incomplete: %#v; missing %v", schema, wantFields)
	}
	for _, exception := range manifest.VulnerabilityExceptions {
		if exception.ID == "" || exception.Reason == "" || exception.Owner == "" {
			t.Errorf("vulnerability exception incomplete: %#v", exception)
		}
		if _, err := time.Parse(time.RFC3339, exception.ExpiresAt); err != nil {
			t.Errorf("vulnerability exception %q expiry: %v", exception.ID, err)
		}
	}
}

func set(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
