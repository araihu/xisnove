package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	migrations "github.com/araihu/xisnove/db/migrations/sqlite"
	"github.com/pressly/goose/v3"
)

var migrationMu sync.Mutex

func Migrate(ctx context.Context, db *sql.DB) error {
	migrationMu.Lock()
	defer migrationMu.Unlock()

	goose.SetBaseFS(migrations.Files)
	goose.SetTableName("schema_migrations")
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set migration dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
