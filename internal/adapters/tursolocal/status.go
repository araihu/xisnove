package tursolocal

import (
	"database/sql"

	"github.com/araihu/xisnove/application/port"
	"github.com/araihu/xisnove/internal/adapters/sqlitecompat"
)

func NewPublicStatusUnitOfWork(db *sql.DB) port.PublicStatusUnitOfWork {
	return sqlitecompat.NewPublicStatusUnitOfWork(db)
}
