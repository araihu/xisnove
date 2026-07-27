package migration

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

type FileLock struct{ file *os.File }

func AcquireFileLock(ctx context.Context, path string, timeout, poll time.Duration) (*FileLock, error) {
	if path == "" {
		return nil, fmt.Errorf("migration lock file path is required")
	}
	lockCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open migration lock file: %w", err)
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &FileLock{file: file}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			_ = file.Close()
			return nil, fmt.Errorf("lock migration file: %w", err)
		}
		select {
		case <-lockCtx.Done():
			_ = file.Close()
			return nil, ClassifyLockError(lockCtx.Err())
		case <-time.After(poll):
		}
	}
}

func (l *FileLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	closeErr := l.file.Close()
	if unlockErr != nil {
		return fmt.Errorf("unlock migration file: %w", unlockErr)
	}
	return closeErr
}
