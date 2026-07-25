package database

import (
	"context"
	"database/sql"
	"fmt"

	sqlitestore "github.com/araihu/xisnove/internal/adapters/sqlite"
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
}

func Open(ctx context.Context, config Config) (*Handle, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}

	switch config.Profile {
	case ProfileSQLite:
		return openSQLite(ctx, config)
	case ProfileTursoLocal, ProfileTursoCloud, ProfilePostgres:
		return nil, fmt.Errorf("%s is not implemented", config)
	default:
		panic("validated database profile is not handled")
	}
}

func (h *Handle) Migrate(ctx context.Context) error {
	return h.migrate(ctx)
}

func (h *Handle) Ready(ctx context.Context) error {
	return h.ready(ctx)
}

func (h *Handle) Close() error {
	return h.close()
}

func openSQLite(ctx context.Context, config Config) (*Handle, error) {
	db, err := sqlitestore.Open(config.URL)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", config, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", config, err)
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
		close: db.Close,
	}, nil
}
