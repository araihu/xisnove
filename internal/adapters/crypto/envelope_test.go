package crypto_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/domain"
	xiscrypto "github.com/araihu/xisnove/internal/adapters/crypto"
)

func TestEnvelopeBindsIdentityAndVersion(t *testing.T) {
	keys := map[uint32][]byte{1: bytes.Repeat([]byte{1}, 32), 2: bytes.Repeat([]byte{2}, 32)}
	nonces := make([]byte, 64)
	for index := range nonces {
		nonces[index] = byte(index)
	}
	envelope, err := xiscrypto.NewEnvelope(2, keys, bytes.NewReader(nonces))
	if err != nil {
		t.Fatal(err)
	}
	identity := port.ConfigIdentity{
		ChannelID: "00000000-0000-4000-8000-000000000001",
		Kind:      domain.NotificationChannelShoutrrr,
	}
	first, err := envelope.Seal(context.Background(), identity, []byte("secret configuration"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := envelope.Seal(context.Background(), identity, []byte("secret configuration"))
	if err != nil {
		t.Fatal(err)
	}
	if first.KeyVersion != 2 || bytes.Equal(first.Ciphertext, second.Ciphertext) {
		t.Fatalf("sealed configs = %#v, %#v", first, second)
	}
	opened, err := envelope.Open(context.Background(), identity, first)
	if err != nil || string(opened) != "secret configuration" {
		t.Fatalf("Open() = %q, %v", opened, err)
	}

	wrong := identity
	wrong.ChannelID = "00000000-0000-4000-8000-000000000002"
	if _, err := envelope.Open(context.Background(), wrong, first); err == nil {
		t.Fatal("Open() accepted wrong channel identity")
	}
	tampered := first
	tampered.Ciphertext = append([]byte(nil), first.Ciphertext...)
	tampered.Ciphertext[len(tampered.Ciphertext)-1] ^= 1
	if _, err := envelope.Open(context.Background(), identity, tampered); err == nil {
		t.Fatal("Open() accepted tampered ciphertext")
	}

	oldEnvelope, err := xiscrypto.NewEnvelope(1, keys, bytes.NewReader(bytes.Repeat([]byte{4}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	old, err := oldEnvelope.Seal(context.Background(), identity, []byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	opened, err = envelope.Open(context.Background(), identity, old)
	if err != nil || string(opened) != "old" {
		t.Fatalf("Open(old) = %q, %v", opened, err)
	}
	if !envelope.CanOpen(1) || envelope.CanOpen(3) || envelope.ActiveVersion() != 2 {
		t.Fatal("key version capabilities are incorrect")
	}
}

func TestEnvelopeHonorsCanceledContext(t *testing.T) {
	envelope, err := xiscrypto.NewEnvelope(
		1,
		map[uint32][]byte{1: bytes.Repeat([]byte{1}, 32)},
		bytes.NewReader(bytes.Repeat([]byte{2}, 32)),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = envelope.Seal(ctx, port.ConfigIdentity{ChannelID: "channel", Kind: "kind"}, []byte("value"))
	if err == nil {
		t.Fatal("Seal() error = nil")
	}
}

func TestLoadEnvelopeValidatesPrivateKeyring(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	valid := fmt.Sprintf(`{"activeVersion":1,"keys":[{"version":1,"key":%q}]}`, key)

	t.Run("valid", func(t *testing.T) {
		path := writePrivateFile(t, "keyring.json", valid, 0o600)
		envelope, err := xiscrypto.LoadEnvelope(path)
		if err != nil || envelope.ActiveVersion() != 1 {
			t.Fatalf("LoadEnvelope() = %#v, %v", envelope, err)
		}
	})

	tests := []struct {
		name    string
		content string
		mode    os.FileMode
	}{
		{"duplicate version", fmt.Sprintf(`{"activeVersion":1,"keys":[{"version":1,"key":%q},{"version":1,"key":%q}]}`, key, key), 0o600},
		{"missing active", fmt.Sprintf(`{"activeVersion":2,"keys":[{"version":1,"key":%q}]}`, key), 0o600},
		{"short key", fmt.Sprintf(`{"activeVersion":1,"keys":[{"version":1,"key":%q}]}`, base64.StdEncoding.EncodeToString([]byte("short"))), 0o600},
		{"unknown field", strings.Replace(valid, `"activeVersion"`, `"unexpected"`, 1), 0o600},
		{"permissive", valid, 0o644},
		{"group writable", valid, 0o660},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writePrivateFile(t, "keyring.json", test.content, test.mode)
			if _, err := xiscrypto.LoadEnvelope(path); err == nil {
				t.Fatal("LoadEnvelope() error = nil")
			}
		})
	}

	t.Run("projected symlink", func(t *testing.T) {
		target := writePrivateFile(t, "target.json", valid, 0o640)
		link := filepath.Join(t.TempDir(), "keyring.json")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		envelope, err := xiscrypto.LoadEnvelope(link)
		if err != nil || envelope.ActiveVersion() != 1 {
			t.Fatalf("LoadEnvelope() = %#v, %v", envelope, err)
		}
	})

	t.Run("oversized", func(t *testing.T) {
		path := writePrivateFile(t, "keyring.json", strings.Repeat("x", 64<<10+1), 0o600)
		if _, err := xiscrypto.LoadEnvelope(path); err == nil {
			t.Fatal("LoadEnvelope() accepted oversized file")
		}
	})
}

func writePrivateFile(t *testing.T, name, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
