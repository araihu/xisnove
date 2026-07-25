package sqlite

import (
	"database/sql"

	application "github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

func NewStore(db *sql.DB) application.Store {
	return sqlitecompat.NewStore(db)
}
