package secrets

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/araihu/xisnove/application/port"
)

const maxSecretBytes = 64 << 10

type FileResolver struct{}

func (FileResolver) Resolve(
	ctx context.Context,
	reference port.SecretReference,
) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reference.Kind != port.SecretReferenceFile {
		return nil, errors.New("resolve secret file: unsupported secret reference kind")
	}
	contents, err := readPrivateRegularFile(reference.Locator, maxSecretBytes)
	if err != nil {
		return nil, fmt.Errorf("resolve secret file: %w", err)
	}
	if len(contents) >= 2 && contents[len(contents)-2] == '\r' && contents[len(contents)-1] == '\n' {
		contents = contents[:len(contents)-2]
	} else if len(contents) >= 1 && contents[len(contents)-1] == '\n' {
		contents = contents[:len(contents)-1]
	}
	if len(contents) == 0 {
		return nil, errors.New("resolve secret file: secret is empty")
	}
	return append([]byte(nil), contents...), nil
}

func readPrivateRegularFile(path string, limit int64) ([]byte, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("secret file path is required")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, errors.New("secret file must be a regular non-symlink file")
	}
	if before.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("secret file permissions must not grant group or other access")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.New("secret file changed while opening")
	}
	contents, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > limit {
		return nil, errors.New("secret file exceeds size limit")
	}
	return contents, nil
}
