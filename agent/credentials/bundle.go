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

var (
	ErrBundleTooLarge      = errors.New("credential bundle exceeds maximum size")
	ErrInsecurePermissions = errors.New("credential bundle permissions exceed workload read access")
	ErrNotRegular          = errors.New("credential bundle target is not a regular file")
)

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
		return Bundle{}, sanitizedFileError("open credential bundle", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return Bundle{}, sanitizedFileError("inspect credential bundle", err)
	}
	if !info.Mode().IsRegular() {
		return Bundle{}, ErrNotRegular
	}
	if info.Mode().Perm()&0o037 != 0 {
		return Bundle{}, ErrInsecurePermissions
	}

	contents, err := io.ReadAll(io.LimitReader(file, MaxBundleSize+1))
	if err != nil {
		return Bundle{}, sanitizedFileError("read credential bundle", err)
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

func sanitizedFileError(operation string, err error) error {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		err = pathError.Err
	}
	return fmt.Errorf("%s: %w", operation, err)
}
