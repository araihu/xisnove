package port

import (
	"context"

	"github.com/araihu/xisnove/domain"
)

// SecretReferenceFile selects the self-hosted filesystem resolver.
const SecretReferenceFile = "file"

// SecretReference identifies secret material without embedding it in persisted
// configuration. Kind selects an injected resolver's namespace and Locator is
// opaque to the application. V1 supports file locators; future composition
// roots may support Vault, OpenBao, ESO, or cloud secret managers.
type SecretReference struct {
	Kind    string
	Locator string
}

// SecretResolver resolves an opaque reference through infrastructure selected
// by the composition root.
type SecretResolver interface {
	Resolve(context.Context, SecretReference) ([]byte, error)
}

// ConfigIdentity is authenticated alongside encrypted channel configuration.
// It deliberately contains no tenant, organization, or workspace scope: the
// public core is single-installation, and future cloud isolation belongs in a
// proprietary adapter and composition root.
type ConfigIdentity struct {
	ChannelID domain.NotificationChannelID
	Kind      domain.NotificationChannelKind
}

// SealedConfig is encrypted infrastructure configuration tagged with the key
// version required to open it.
type SealedConfig struct {
	KeyVersion uint32
	Ciphertext []byte
}

// ConfigSealer protects and opens infrastructure configuration while allowing
// callers to validate and rotate persisted key versions.
type ConfigSealer interface {
	ActiveVersion() uint32
	CanOpen(uint32) bool
	Seal(context.Context, ConfigIdentity, []byte) (SealedConfig, error)
	Open(context.Context, ConfigIdentity, SealedConfig) ([]byte, error)
}
