package crypto

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/araihu/xisnove/application/port"
)

const maxKeyringBytes = 64 << 10

type keyringDocument struct {
	ActiveVersion uint32             `json:"activeVersion"`
	Keys          []keyringKeyRecord `json:"keys"`
}

type keyringKeyRecord struct {
	Version uint32 `json:"version"`
	Key     string `json:"key"`
}

type Envelope struct {
	active uint32
	keys   map[uint32][]byte
	random io.Reader
}

func LoadEnvelope(path string) (*Envelope, error) {
	contents, err := readPrivateRegularFile(path, maxKeyringBytes)
	if err != nil {
		return nil, fmt.Errorf("load notification keyring: %w", err)
	}
	decoder := json.NewDecoder(strings.NewReader(string(contents)))
	decoder.DisallowUnknownFields()
	var document keyringDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("load notification keyring: invalid JSON: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("load notification keyring: trailing JSON value")
	}
	if document.ActiveVersion == 0 || len(document.Keys) == 0 {
		return nil, errors.New("load notification keyring: active version and keys are required")
	}
	keys := make(map[uint32][]byte, len(document.Keys))
	for _, record := range document.Keys {
		if record.Version == 0 {
			return nil, errors.New("load notification keyring: key version must be positive")
		}
		if _, exists := keys[record.Version]; exists {
			return nil, fmt.Errorf("load notification keyring: duplicate key version %d", record.Version)
		}
		key, err := base64.StdEncoding.DecodeString(record.Key)
		if err != nil || len(key) != 32 {
			return nil, fmt.Errorf("load notification keyring: key version %d must be 32 base64-encoded bytes", record.Version)
		}
		keys[record.Version] = append([]byte(nil), key...)
	}
	if _, exists := keys[document.ActiveVersion]; !exists {
		return nil, fmt.Errorf("load notification keyring: active version %d is missing", document.ActiveVersion)
	}
	return NewEnvelope(document.ActiveVersion, keys, rand.Reader)
}

func NewEnvelope(active uint32, keys map[uint32][]byte, random io.Reader) (*Envelope, error) {
	if active == 0 || random == nil {
		return nil, errors.New("notification envelope requires an active key and randomness")
	}
	cloned := make(map[uint32][]byte, len(keys))
	for version, key := range keys {
		if version == 0 || len(key) != 32 {
			return nil, fmt.Errorf("notification envelope key version %d must contain 32 bytes", version)
		}
		cloned[version] = append([]byte(nil), key...)
	}
	if _, exists := cloned[active]; !exists {
		return nil, fmt.Errorf("notification envelope active key version %d is missing", active)
	}
	return &Envelope{active: active, keys: cloned, random: random}, nil
}

func (e *Envelope) ActiveVersion() uint32 { return e.active }

func (e *Envelope) CanOpen(version uint32) bool {
	_, exists := e.keys[version]
	return exists
}

func (e *Envelope) Seal(
	ctx context.Context,
	identity port.ConfigIdentity,
	plaintext []byte,
) (port.SealedConfig, error) {
	if err := ctx.Err(); err != nil {
		return port.SealedConfig{}, err
	}
	key := e.keys[e.active]
	aead, err := newGCM(key)
	if err != nil {
		return port.SealedConfig{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(e.random, nonce); err != nil {
		return port.SealedConfig{}, fmt.Errorf("seal notification configuration: read nonce: %w", err)
	}
	additional, err := associatedData(identity, e.active)
	if err != nil {
		return port.SealedConfig{}, err
	}
	ciphertext := aead.Seal(append([]byte(nil), nonce...), nonce, plaintext, additional)
	return port.SealedConfig{KeyVersion: e.active, Ciphertext: ciphertext}, nil
}

func (e *Envelope) Open(
	ctx context.Context,
	identity port.ConfigIdentity,
	sealed port.SealedConfig,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	key, exists := e.keys[sealed.KeyVersion]
	if !exists {
		return nil, fmt.Errorf("open notification configuration: unknown key version %d", sealed.KeyVersion)
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(sealed.Ciphertext) < aead.NonceSize() {
		return nil, errors.New("open notification configuration: malformed envelope")
	}
	nonce := sealed.Ciphertext[:aead.NonceSize()]
	ciphertext := sealed.Ciphertext[aead.NonceSize():]
	additional, err := associatedData(identity, sealed.KeyVersion)
	if err != nil {
		return nil, err
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, additional)
	if err != nil {
		return nil, errors.New("open notification configuration: authentication failed")
	}
	return plaintext, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create notification cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create notification envelope: %w", err)
	}
	return aead, nil
}

func associatedData(identity port.ConfigIdentity, version uint32) ([]byte, error) {
	if identity.ChannelID == "" || identity.Kind == "" || version == 0 {
		return nil, errors.New("notification configuration identity is incomplete")
	}
	if len(identity.ChannelID) > math.MaxUint16 || len(identity.Kind) > math.MaxUint16 {
		return nil, errors.New("notification configuration identity is too large")
	}
	result := make([]byte, 0, 8+len(identity.ChannelID)+len(identity.Kind))
	result = appendField(result, string(identity.ChannelID))
	result = appendField(result, string(identity.Kind))
	versionBytes := make([]byte, 4)
	binary.BigEndian.PutUint32(versionBytes, version)
	return append(result, versionBytes...), nil
}

func appendField(target []byte, value string) []byte {
	length := make([]byte, 2)
	binary.BigEndian.PutUint16(length, uint16(len(value)))
	target = append(target, length...)
	return append(target, value...)
}

func readPrivateRegularFile(path string, limit int64) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("secret file path is required")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("secret file must be a regular non-symlink file")
	}
	if before.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("secret file permissions must not grant group or other access")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.New("secret file changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("secret file exceeds size limit")
	}
	return contents, nil
}
