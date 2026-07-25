package postgres

import "embed"

// Files contains the ordered PostgreSQL Goose migrations.
//
//go:embed *.sql
var Files embed.FS

const LatestVersion = 4
