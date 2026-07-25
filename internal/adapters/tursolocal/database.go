package tursolocal

import (
	"context"
	"database/sql"
	"fmt"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

func Open(ctx context.Context, path string) (*sql.DB, error) {
	db, err := sql.Open("turso", path)
	if err != nil {
		return nil, fmt.Errorf("open local Turso database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping local Turso database: %w", err)
	}
	return db, nil
}

func Migrate(ctx context.Context, db *sql.DB) error {
	return sqlitecompat.Migrate(ctx, db)
}

func Ready(ctx context.Context, db *sql.DB) error {
	return sqlitecompat.Ready(ctx, db)
}

func NewStore(db *sql.DB) application.Store {
	return sqlitecompat.NewStore(db)
}
