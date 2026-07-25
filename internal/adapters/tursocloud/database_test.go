package tursocloud

import (
	"strings"
	"testing"
)

func TestBuildDSNPreservesQueryAndEscapesToken(t *testing.T) {
	t.Parallel()

	got, err := buildDSN(
		"libsql://example.turso.io?tls=1",
		"secret with ? and &",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "tls=1") ||
		!strings.Contains(got, "authToken=secret+with+%3F+and+%26") {
		t.Fatalf("DSN = %q", got)
	}
}

func TestBuildDSNRejectsUnsafeInputsWithoutLeakingToken(t *testing.T) {
	t.Parallel()

	const secret = "do-not-leak"
	for _, rawURL := range []string{
		"https://example.turso.io",
		"libsql://user:password@example.turso.io",
		":// invalid",
	} {
		_, err := buildDSN(rawURL, secret)
		if err == nil {
			t.Fatalf("buildDSN(%q) error = nil", rawURL)
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("buildDSN(%q) error leaks token: %v", rawURL, err)
		}
	}
}
