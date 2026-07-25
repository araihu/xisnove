package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

type PasswordParams struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

var ProductionPasswordParams = PasswordParams{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 2,
	SaltLength:  16,
	KeyLength:   32,
}

type PasswordHasher struct {
	params PasswordParams
	random io.Reader
}

func NewPasswordHasher(params PasswordParams, random io.Reader) *PasswordHasher {
	return &PasswordHasher{params: params, random: random}
}

func NewProductionPasswordHasher() *PasswordHasher {
	return NewPasswordHasher(ProductionPasswordParams, rand.Reader)
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	if err := validatePasswordParams(h.params); err != nil {
		return "", err
	}
	salt := make([]byte, h.params.SaltLength)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("read password salt: %w", err)
	}
	key := argon2.IDKey(
		[]byte(password),
		salt,
		h.params.Iterations,
		h.params.Memory,
		h.params.Parallelism,
		h.params.KeyLength,
	)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.params.Memory,
		h.params.Iterations,
		h.params.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

func (h *PasswordHasher) Verify(encodedHash, password string) bool {
	params, salt, expected, err := parsePasswordHash(encodedHash)
	if err != nil {
		return false
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		params.Iterations,
		params.Memory,
		params.Parallelism,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func parsePasswordHash(encoded string) (PasswordParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return PasswordParams{}, nil, nil, errors.New("invalid argon2id encoding")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil ||
		version != argon2.Version {
		return PasswordParams{}, nil, nil, errors.New("invalid argon2id version")
	}
	params := PasswordParams{}
	if _, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&params.Memory,
		&params.Iterations,
		&params.Parallelism,
	); err != nil {
		return PasswordParams{}, nil, nil, errors.New("invalid argon2id parameters")
	}
	if params.Memory == 0 || params.Iterations == 0 || params.Parallelism == 0 {
		return PasswordParams{}, nil, nil, errors.New("invalid argon2id parameters")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return PasswordParams{}, nil, nil, errors.New("invalid argon2id salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) == 0 {
		return PasswordParams{}, nil, nil, errors.New("invalid argon2id key")
	}
	return params, salt, expected, nil
}

func validatePasswordParams(params PasswordParams) error {
	if params.Memory == 0 ||
		params.Iterations == 0 ||
		params.Parallelism == 0 ||
		params.SaltLength == 0 ||
		params.KeyLength == 0 {
		return errors.New("argon2id parameters must be positive")
	}
	return nil
}
