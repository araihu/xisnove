package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	modernsqlite "modernc.org/sqlite"
)

type onlineBackuper interface {
	NewBackup(string) (*modernsqlite.Backup, error)
}

func createSQLite(ctx context.Context, source *sql.DB, destination string) (retErr error) {
	if destination == "" {
		return fmt.Errorf("create SQLite backup: destination is required")
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("%w: %s", ErrDestinationExists, filepath.Base(destination))
	}
	if err != nil {
		return fmt.Errorf("reserve SQLite backup destination: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("close SQLite backup destination: %w", err)
	}
	complete := false
	defer func() {
		if !complete {
			retErr = errors.Join(retErr, os.Remove(destination))
		}
	}()

	connection, err := source.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire SQLite backup connection: %w", err)
	}
	defer connection.Close()
	err = connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(onlineBackuper)
		if !ok {
			return fmt.Errorf("SQLite driver does not expose the online backup API")
		}
		operation, err := backuper.NewBackup(destination)
		if err != nil {
			return fmt.Errorf("initialize SQLite online backup: %w", err)
		}
		for {
			more, stepErr := operation.Step(128)
			if stepErr != nil {
				return errors.Join(
					fmt.Errorf("copy SQLite backup pages: %w", stepErr),
					operation.Finish(),
				)
			}
			if !more {
				break
			}
			select {
			case <-ctx.Done():
				return errors.Join(ctx.Err(), operation.Finish())
			default:
			}
		}
		if err := operation.Finish(); err != nil {
			return fmt.Errorf("finish SQLite online backup: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("create SQLite online backup: %w", err)
	}
	if err := os.Chmod(destination, 0o600); err != nil {
		return fmt.Errorf("restrict SQLite backup permissions: %w", err)
	}
	completed, err := os.OpenFile(destination, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open completed SQLite backup: %w", err)
	}
	if err := completed.Sync(); err != nil {
		_ = completed.Close()
		return fmt.Errorf("sync completed SQLite backup: %w", err)
	}
	if err := completed.Close(); err != nil {
		return fmt.Errorf("close completed SQLite backup: %w", err)
	}
	complete = true
	return nil
}
