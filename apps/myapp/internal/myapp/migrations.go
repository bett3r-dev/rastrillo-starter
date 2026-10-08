package myapp

import (
	"embed"

	"amadan.net/rastrillo/rastrillo/migrate"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Schema is this app's own migrations, in order — nothing else. It is
// what `rastrillo migration generate` and `rastrillo migration check` read
// and write: both work by replaying a set into an in-memory database
// and diffing the result against Models, so Schema must never carry a
// framework subsystem's migrations too — mixed in, the diff would see
// tables (sessions, auth_links, ...) that Models knows nothing about
// and want to drop every one of them.
//
// Add to it with `rastrillo migration generate` after changing a
// model, or `rastrillo migration new <name>` to write one by hand.
// Migrations apply at boot and are never re-run or reversed: never
// edit one that has shipped, add a new one instead.
var Schema = migrate.MustFromFS(migrationFS, "myapp")

// BootSchema is everything applied at boot, in order — this app's own
// Schema plus every framework subsystem's — and it is what App() below
// hands to migrate.Apply. It is a separate variable from Schema for
// the same reason Schema must stay narrow: generate and check would
// misread a subsystem's tables as ones Models forgot.
//
// A fresh scaffold uses no subsystem yet, so today this is just
// Schema. Adding one (say, sessions) means editing this line, not
// Schema:
//
//	var BootSchema = migrate.Merge(sessions.Schema, Schema)
//
// Merge's argument order is apply order, so a subsystem whose
// migrations another one reads (auth after sessions) goes first.
// Get this line wrong and either boot silently skips a subsystem's
// tables, or generate/check try to drop them.
var BootSchema = migrate.Merge(Schema)
