package idempotency

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

var validKey = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

type Policy struct {
	Diagnostics io.Writer
	Generate    func() (string, error)
}

func (p Policy) Resolve(explicit string) (string, error) {
	if explicit != "" {
		if !validKey.MatchString(explicit) {
			return "", errors.New("idempotency key must contain 1-128 letters, digits, dots, underscores, colons, or hyphens")
		}
		return explicit, nil
	}
	generate := p.Generate
	if generate == nil {
		generate = newUUID
	}
	key, err := generate()
	if err != nil {
		return "", fmt.Errorf("generate idempotency key: %w", err)
	}
	if !validKey.MatchString(key) {
		return "", errors.New("generated idempotency key is invalid")
	}
	if p.Diagnostics != nil {
		if _, err := fmt.Fprintf(p.Diagnostics, "generated idempotency key: %s\n", key); err != nil {
			return "", fmt.Errorf("write idempotency diagnostic: %w", err)
		}
	}
	return key, nil
}

func HeaderEditor(key string) func(context.Context, *http.Request) error {
	return func(_ context.Context, request *http.Request) error {
		request.Header.Set("Idempotency-Key", key)
		return nil
	}
}

func newUUID() (string, error) {
	var value [16]byte
	if _, err := io.ReadFull(rand.Reader, value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", value[0:4], value[4:6], value[6:8], value[8:10], value[10:16]), nil
}
