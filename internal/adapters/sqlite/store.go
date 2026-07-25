package sqlite

import (
	"database/sql"

	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
	"github.com/araihu/xisnove/internal/application"
)

func NewStore(db *sql.DB) application.Store {
	return sqlitecompat.NewStore(db)
}
