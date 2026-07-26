// Package credentials loads the agent's coherent credential bundle.
package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const MaxBundleSize = 64 << 10

var ErrBundleTooLarge = errors.New("credential bundle exceeds maximum size")

type Bundle struct {
	Credential string `json:"credential"`
	Generation int64  `json:"generation"`
}

type Provider interface {
	Current(context.Context) (Bundle, error)
}

type FileProvider struct {
	Path string
}

func (provider FileProvider) Current(ctx context.Context) (Bundle, error) {
	if err := ctx.Err(); err != nil {
		return Bundle{}, err
	}
	if provider.Path == "" {
		return Bundle{}, errors.New("credential bundle path is empty")
	}
	file, err := os.Open(provider.Path)
	if err != nil {
		return Bundle{}, fmt.Errorf("open credential bundle: %w", err)
	}
	defer file.Close()

	contents, err := io.ReadAll(io.LimitReader(file, MaxBundleSize+1))
	if err != nil {
		return Bundle{}, fmt.Errorf("read credential bundle: %w", err)
	}
	defer clear(contents)
	if err := ctx.Err(); err != nil {
		return Bundle{}, err
	}
	if len(contents) > MaxBundleSize {
		return Bundle{}, ErrBundleTooLarge
	}

	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, errors.New("credential bundle is not valid JSON")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Bundle{}, errors.New("credential bundle must contain one JSON value")
	}
	if strings.TrimSpace(bundle.Credential) == "" {
		return Bundle{}, errors.New("credential bundle has empty credential")
	}
	if bundle.Generation <= 0 {
		return Bundle{}, errors.New("credential bundle has invalid generation")
	}
	return bundle, nil
}
