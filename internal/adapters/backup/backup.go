package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/araihu/xisnove/internal/adapters/database"
)

var (
	ErrDestinationExists      = errors.New("backup destination already exists")
	ErrQuiescedBackupRequired = errors.New("local Turso requires a quiesced backup procedure")
	ErrProviderBackupRequired = errors.New("managed Turso requires a provider backup or branch")
	ErrExternalToolRequired   = errors.New("PostgreSQL backup requires pg_dump")
)

func Create(
	ctx context.Context,
	profile database.Profile,
	source *sql.DB,
	destination string,
) error {
	switch profile {
	case database.ProfileSQLite:
		if source == nil {
			return fmt.Errorf("create SQLite backup: source database is required")
		}
		return createSQLite(ctx, source, destination)
	case database.ProfileTursoLocal:
		return ErrQuiescedBackupRequired
	case database.ProfileTursoCloud:
		return ErrProviderBackupRequired
	case database.ProfilePostgres:
		return ErrExternalToolRequired
	default:
		return fmt.Errorf("backup database profile %q: %w", profile, database.Config{Profile: profile, URL: "unused"}.Validate())
	}
}
