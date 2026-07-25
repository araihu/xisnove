package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"

	"github.com/araihu/xisnove/application"
)

type TokenIssuer struct {
	random io.Reader
}

func NewTokenIssuer(random io.Reader) *TokenIssuer {
	return &TokenIssuer{random: random}
}

func NewProductionTokenIssuer() *TokenIssuer {
	return NewTokenIssuer(rand.Reader)
}

func (i *TokenIssuer) New() (application.IssuedToken, error) {
	entropy := make([]byte, 32)
	if _, err := io.ReadFull(i.random, entropy); err != nil {
		return application.IssuedToken{}, fmt.Errorf("read token entropy: %w", err)
	}
	raw := base64.RawURLEncoding.EncodeToString(entropy)
	return application.IssuedToken{Raw: raw, Hash: i.Hash(raw)}, nil
}

func (*TokenIssuer) Hash(raw string) []byte {
	sum := sha256.Sum256([]byte(raw))
	return sum[:]
}
