// Package migrations embeds the Core PostgreSQL schema migrations so that a
// single Core binary can apply them without shipping files alongside it.
//
// Files follow the golang-migrate naming convention:
//
//	<version>_<name>.up.sql
//	<version>_<name>.down.sql
//
// Migrations are append-only: never edit an applied migration, add a new one.
package migrations

import "embed"

// FS holds every migration file.
//
//go:embed *.sql
var FS embed.FS
