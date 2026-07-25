package database

import (
	"context"
	"database/sql"
	"fmt"

	postgresstore "github.com/araihu/xisnove/internal/adapters/postgres"
	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
	"github.com/araihu/xisnove/internal/adapters/tursocloud"
	"github.com/araihu/xisnove/internal/adapters/tursolocal"
	"github.com/araihu/xisnove/internal/application"
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "github.com/tursodatabase/libsql-client-go/libsql"
	_ "turso.tech/database/tursogo"
)

type Handle struct {
	DB          *sql.DB
	Store       application.Store
	Profile     Profile
	ReplicaSafe bool

	migrate func(context.Context) error
	ready   func(context.Context) error
	close   func() error
	redact  func(error) error
}

func Open(ctx context.Context, config Config) (*Handle, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	switch config.Profile {
	case ProfileSQLite:
		return openSQLite(ctx, config)
	case ProfileTursoLocal:
		return openTursoLocal(ctx, config)
	case ProfilePostgres:
		return openPostgres(ctx, config)
	case ProfileTursoCloud:
		return openTursoCloud(ctx, config)
	default:
		panic("validated database profile is not handled")
	}
}

func openTursoCloud(ctx context.Context, config Config) (*Handle, error) {
	db, err := tursocloud.Open(ctx, config.URL, config.AuthToken)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", config, redactDatabaseError(err, config))
	}
	return &Handle{
		DB:          db,
		Store:       tursocloud.NewStore(db),
		Profile:     config.Profile,
		ReplicaSafe: config.Profile.ReplicaSafe(),
		migrate: func(ctx context.Context) error {
			return tursocloud.Migrate(ctx, db)
		},
		ready: func(ctx context.Context) error {
			return tursocloud.Ready(ctx, db)
		},
		close:  db.Close,
		redact: func(err error) error { return redactDatabaseError(err, config) },
	}, nil
}

func openPostgres(ctx context.Context, config Config) (*Handle, error) {
	db, err := postgresstore.Open(ctx, config.URL)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", config, redactDatabaseError(err, config))
	}
	return &Handle{
		DB:          db,
		Store:       postgresstore.NewStore(db),
		Profile:     config.Profile,
		ReplicaSafe: config.Profile.ReplicaSafe(),
		migrate: func(ctx context.Context) error {
			return postgresstore.Migrate(ctx, db)
		},
		ready: func(ctx context.Context) error {
			return postgresstore.Ready(ctx, db)
		},
		close:  db.Close,
		redact: func(err error) error { return redactDatabaseError(err, config) },
	}, nil
}

func openTursoLocal(ctx context.Context, config Config) (*Handle, error) {
	db, err := tursolocal.Open(ctx, config.URL)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", config, redactDatabaseError(err, config))
	}
	return &Handle{
		DB:          db,
		Store:       tursolocal.NewStore(db),
		Profile:     config.Profile,
		ReplicaSafe: config.Profile.ReplicaSafe(),
		migrate: func(ctx context.Context) error {
			return tursolocal.Migrate(ctx, db)
		},
		ready: func(ctx context.Context) error {
			return tursolocal.Ready(ctx, db)
		},
		close:  db.Close,
		redact: func(err error) error { return redactDatabaseError(err, config) },
	}, nil
}

func (h *Handle) Migrate(ctx context.Context) error {
	return h.redacted(h.migrate(ctx))
}

func (h *Handle) Ready(ctx context.Context) error {
	return h.redacted(h.ready(ctx))
}

func (h *Handle) Close() error {
	return h.close()
}

func openSQLite(ctx context.Context, config Config) (*Handle, error) {
	db, err := sqlitestore.Open(config.URL)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", config, redactDatabaseError(err, config))
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", config, redactDatabaseError(err, config))
	}
	return &Handle{
		DB:          db,
		Store:       sqlitestore.NewStore(db),
		Profile:     config.Profile,
		ReplicaSafe: config.Profile.ReplicaSafe(),
		migrate: func(ctx context.Context) error {
			return sqlitestore.Migrate(ctx, db)
		},
		ready: func(ctx context.Context) error {
			return sqlitestore.Ready(ctx, db)
		},
		close:  db.Close,
		redact: func(err error) error { return redactDatabaseError(err, config) },
	}, nil
}

func (h *Handle) redacted(err error) error {
	if err == nil || h.redact == nil {
		return err
	}
	return h.redact(err)
}
