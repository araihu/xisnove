package controlplane

import (
	"context"
	"errors"
	"testing"
)

func TestFakeExchangesOnlyConfiguredAdministratorCredentials(t *testing.T) {
	client := NewFake("local-admin", "correct horse", "opaque-control-plane-session")

	credential, err := client.ExchangeAdministratorCredentials(t.Context(), "local-admin", "correct horse")
	if err != nil {
		t.Fatalf("exchange valid credentials: %v", err)
	}
	if credential != "opaque-control-plane-session" {
		t.Fatalf("credential = %q, want opaque fixture", credential)
	}

	credential, err = client.ExchangeAdministratorCredentials(t.Context(), "local-admin", "wrong")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("invalid credentials error = %v, want ErrInvalidCredentials", err)
	}
	if credential != "" {
		t.Fatalf("invalid credentials leaked credential %q", credential)
	}
}

func TestFakeHonorsCancellationWithoutExaminingCredentials(t *testing.T) {
	client := NewFake("local-admin", "correct horse", "opaque-control-plane-session")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	credential, err := client.ExchangeAdministratorCredentials(ctx, "local-admin", "correct horse")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("exchange error = %v, want context cancellation", err)
	}
	if credential != "" {
		t.Fatalf("canceled exchange returned credential %q", credential)
	}
}

func TestFakeRevokesOnlyItsIssuedCredential(t *testing.T) {
	client := NewFake("local-admin", "correct horse", "opaque-control-plane-session")

	if err := client.RevokeSession(t.Context(), "opaque-control-plane-session"); err != nil {
		t.Fatalf("revoke issued credential: %v", err)
	}
	if err := client.RevokeSession(t.Context(), "different-session"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoke unknown credential error = %v, want ErrUnauthorized", err)
	}
}
