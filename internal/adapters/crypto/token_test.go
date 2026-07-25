package crypto_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"testing"

	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
)

func TestTokenIssuerReturnsRawTokenAndStableHash(t *testing.T) {
	random := make([]byte, 32)
	for i := range random {
		random[i] = byte(i)
	}
	issuer := xiscrypto.NewTokenIssuer(bytes.NewReader(random))

	token, err := issuer.New()
	if err != nil {
		t.Fatal(err)
	}
	if len(token.Raw) != 43 {
		t.Fatalf("raw token length = %d", len(token.Raw))
	}
	want := sha256.Sum256([]byte(token.Raw))
	if !bytes.Equal(token.Hash, want[:]) {
		t.Fatal("issued hash does not match raw token")
	}
	if !bytes.Equal(issuer.Hash(token.Raw), want[:]) {
		t.Fatal("Hash returned a different digest")
	}
}

func TestTokenIssuerPropagatesRandomSourceFailure(t *testing.T) {
	issuer := xiscrypto.NewTokenIssuer(errorReader{})
	if _, err := issuer.New(); err == nil {
		t.Fatal("expected random source error")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("random source failed")
}
