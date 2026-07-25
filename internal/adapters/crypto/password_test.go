package crypto_test

import (
	"bytes"
	"strings"
	"testing"

	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
)

func TestArgon2idHasherRoundTripsEncodedPassword(t *testing.T) {
	hasher := xiscrypto.NewPasswordHasher(xiscrypto.PasswordParams{
		Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16,
	}, bytes.NewReader(bytes.Repeat([]byte{0x42}, 16)))

	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=64,t=1,p=1$") {
		t.Fatalf("encoded = %q", encoded)
	}
	if !hasher.Verify(encoded, "correct horse battery staple") {
		t.Fatal("correct password was rejected")
	}
	if hasher.Verify(encoded, "incorrect password") {
		t.Fatal("incorrect password was accepted")
	}
}

func TestArgon2idHasherRejectsMalformedEncoding(t *testing.T) {
	hasher := xiscrypto.NewPasswordHasher(
		xiscrypto.PasswordParams{Memory: 64, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 16},
		bytes.NewReader(bytes.Repeat([]byte{0x42}, 16)),
	)
	for _, encoded := range []string{
		"",
		"$argon2i$v=19$m=64,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=18$m=64,t=1,p=1$c2FsdA$a2V5",
		"$argon2id$v=19$m=bad,t=1,p=1$c2FsdA$a2V5",
	} {
		if hasher.Verify(encoded, "password") {
			t.Fatalf("accepted malformed encoding %q", encoded)
		}
	}
}
