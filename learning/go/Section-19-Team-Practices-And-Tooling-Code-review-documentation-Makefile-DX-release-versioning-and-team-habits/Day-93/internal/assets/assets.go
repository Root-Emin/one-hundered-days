// Package assets holds the files the binaries carry with them.
//
// The migrations are embedded, not read from disk, because the binary that
// runs in production must not need the repository checked out beside it.
// `make migrate` and the deployed container run exactly the same bytes.
//
// They live under internal/assets rather than at the project root for a
// mechanical reason worth knowing: //go:embed can only reach files in its own
// directory and below - there is no ../ - so the embedding package has to sit
// above what it embeds.
package assets

import "embed"

// Migrations holds the SQL files under migrations/.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationsDir is the directory inside Migrations that holds the files.
const MigrationsDir = "migrations"
