package migrations

import "embed"

// Files contains the ordered SQLite migration set.
//
//go:embed *.sql
var Files embed.FS
