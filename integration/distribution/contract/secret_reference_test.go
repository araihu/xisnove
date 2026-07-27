package contract_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretReferenceMatrixFreezesProviderNeutralInputs(t *testing.T) {
	content := read(t, filepath.Join(repositoryRoot(t), "docs", "distribution", "secret-reference-matrix.md"))
	for _, required := range []string{
		"XISNOVE_CURSOR_SIGNING_KEY_FILE",
		"XISNOVE_NOTIFICATION_MASTER_KEY_FILE",
		"--password-file",
		"XISNOVE_UI_COOKIE_SECRET_FILE",
		"XISNOVE_PROVISIONING_CREDENTIAL_FILE",
		"XISNOVE_AGENT_CREDENTIAL_FILE",
		"External Secrets Operator",
		"Vault",
		"OpenBao",
		"SecretReferenceResolver",
		"idempotent",
		"owner-only",
	} {
		if !strings.Contains(content, required) {
			t.Errorf("secret reference matrix missing %q", required)
		}
	}
}
