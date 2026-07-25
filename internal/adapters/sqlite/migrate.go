package sqlite

import (
	"context"
	"database/sql"

	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

const LatestMigrationVersion = sqlitecompat.LatestMigrationVersion

func Migrate(ctx context.Context, db *sql.DB) error {
	return sqlitecompat.Migrate(ctx, db)
}

func Ready(ctx context.Context, db *sql.DB) error {
	return sqlitecompat.Ready(ctx, db)
}
