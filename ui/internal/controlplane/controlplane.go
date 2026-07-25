// Package controlplane defines the contract-independent seam between the UI
// BFF and the generated Xisnove SDK.
package controlplane

import (
	"context"
	"crypto/subtle"
	"errors"
)

var (
	ErrInvalidCredentials = errors.New("invalid administrator credentials")
	ErrUnauthorized       = errors.New("control-plane session is unauthorized")
)

// Client is intentionally limited to the authentication behavior already
// established by the Xisnove architecture. The generated-SDK adapter belongs
// here after the frozen API contract is handed off.
type Client interface {
	ExchangeAdministratorCredentials(ctx context.Context, username, password string) (opaqueCredential string, err error)
	RevokeSession(ctx context.Context, opaqueCredential string) error
}

// Fake is a deterministic development and test implementation. Its configured
// credentials remain server-side and are never rendered into browser content.
type Fake struct {
	username   string
	password   string
	credential string
}

func NewFake(username, password, credential string) *Fake {
	return &Fake{username: username, password: password, credential: credential}
}

func (f *Fake) ExchangeAdministratorCredentials(ctx context.Context, username, password string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	usernameOK := subtle.ConstantTimeCompare([]byte(username), []byte(f.username))
	passwordOK := subtle.ConstantTimeCompare([]byte(password), []byte(f.password))
	if usernameOK&passwordOK != 1 {
		return "", ErrInvalidCredentials
	}
	return f.credential, nil
}

func (f *Fake) RevokeSession(ctx context.Context, credential string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(credential), []byte(f.credential)) != 1 {
		return ErrUnauthorized
	}
	return nil
}

var _ Client = (*Fake)(nil)
