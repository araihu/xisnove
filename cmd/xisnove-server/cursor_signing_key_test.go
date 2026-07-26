package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/araihu/xisnove/application"
)

func TestCursorSigningKeyFlagsUseEnvironmentAndAllowFlagOverride(t *testing.T) {
	t.Setenv(cursorSigningKeyFileEnvironment, " /environment/cursor-key ")
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	values := addCursorSigningKeyFlags(flags, os.Getenv)
	if err := flags.Parse(nil); err != nil {
		t.Fatal(err)
	}
	if values.keyFile != "/environment/cursor-key" {
		t.Fatalf("key file = %q", values.keyFile)
	}

	flags = flag.NewFlagSet("test", flag.ContinueOnError)
	values = addCursorSigningKeyFlags(flags, os.Getenv)
	if err := flags.Parse([]string{"--cursor-signing-key-file", "/flag/cursor-key"}); err != nil {
		t.Fatal(err)
	}
	if values.keyFile != "/flag/cursor-key" {
		t.Fatalf("overridden key file = %q", values.keyFile)
	}
}

func TestCursorSigningKeyLoadRequiresPrivateKeyFile(t *testing.T) {
	key := bytes.Repeat([]byte("k"), 32)
	path := writeCursorSigningKey(t, key, 0o600)
	values := &cursorSigningKeyFlagValues{keyFile: path}
	codec, err := values.load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := codec.EncodeFor(
		application.CursorAudience{Endpoint: "/v1/monitors"},
		application.CursorKey{Sort: "10", ID: "00000000-0000-4000-8000-000000000001"},
		application.CursorShapeInt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.DecodeFor(
		cursor,
		application.CursorAudience{Endpoint: "/v1/monitors"},
		application.CursorShapeInt,
	); err != nil {
		t.Fatalf("decode signed cursor: %v", err)
	}
}

func TestCursorSigningKeyLoadRejectsMissingOrShortKey(t *testing.T) {
	if _, err := (&cursorSigningKeyFlagValues{}).load(context.Background()); err == nil ||
		err.Error() != "--cursor-signing-key-file or XISNOVE_CURSOR_SIGNING_KEY_FILE is required" {
		t.Fatalf("missing key error = %v", err)
	}
	path := writeCursorSigningKey(t, []byte("too-short"), 0o600)
	_, err := (&cursorSigningKeyFlagValues{keyFile: path}).load(context.Background())
	if err == nil || err.Error() != "load cursor signing key: key must contain at least 32 bytes" {
		t.Fatalf("short key error = %v", err)
	}
}

func TestCursorSigningKeyLoadErrorsDoNotLeakSecretOrPath(t *testing.T) {
	secret := "must-not-appear-in-errors"
	permissive := writeCursorSigningKey(t, []byte(secret+strings.Repeat("x", 32)), 0o644)
	missing := filepath.Join(t.TempDir(), "sensitive-cursor-key-name")
	for _, path := range []string{permissive, missing} {
		_, err := (&cursorSigningKeyFlagValues{keyFile: path}).load(context.Background())
		if err == nil {
			t.Fatalf("load(%q) error = nil", path)
		}
		if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), path) {
			t.Fatalf("load error leaked secret material: %v", err)
		}
		if err.Error() != "load cursor signing key: secret file is unavailable or unsafe" {
			t.Fatalf("load error = %v", err)
		}
	}
}

func TestCursorSigningKeyLoadHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := (&cursorSigningKeyFlagValues{keyFile: "unused"}).load(ctx)
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("load error = %v", err)
	}
}

func writeCursorSigningKey(t *testing.T, key []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-signing-key")
	contents := append(append([]byte(nil), key...), '\n')
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}
