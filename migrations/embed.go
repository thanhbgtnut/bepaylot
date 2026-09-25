// Package migrations embeds the goose SQL migrations so every binary carries
// its own schema. Files live in migrations/postgres and are applied in order.
package migrations

import "embed"

// Postgres holds migrations/postgres/*.sql.
//
//go:embed postgres/*.sql
var Postgres embed.FS
