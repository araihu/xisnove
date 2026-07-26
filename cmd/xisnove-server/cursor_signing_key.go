package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"

	"github.com/araihu/xisnove/application"
	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/secrets"
)

const cursorSigningKeyFileEnvironment = "XISNOVE_CURSOR_SIGNING_KEY_FILE"

type cursorSigningKeyFlagValues struct {
	keyFile string
}

func addCursorSigningKeyFlags(
	flags *flag.FlagSet,
	getenv func(string) string,
) *cursorSigningKeyFlagValues {
	values := &cursorSigningKeyFlagValues{}
	flags.StringVar(
		&values.keyFile,
		"cursor-signing-key-file",
		strings.TrimSpace(getenv(cursorSigningKeyFileEnvironment)),
		"cursor HMAC signing-key secret file",
	)
	return values
}

func (v *cursorSigningKeyFlagValues) load(ctx context.Context) (application.AudienceCursorCodec, error) {
	path := strings.TrimSpace(v.keyFile)
	if path == "" {
		return nil, fmt.Errorf("--cursor-signing-key-file or %s is required", cursorSigningKeyFileEnvironment)
	}
	key, err := (secrets.FileResolver{}).Resolve(ctx, port.SecretReference{
		Kind:    port.SecretReferenceFile,
		Locator: path,
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, errors.New("load cursor signing key: secret file is unavailable or unsafe")
	}
	defer clear(key)
	codec, err := application.NewHMACCursorCodec(key)
	if err != nil {
		return nil, errors.New("load cursor signing key: key must contain at least 32 bytes")
	}
	return codec, nil
}
