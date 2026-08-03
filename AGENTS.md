# Project agent memory

This file is the project's committed home for project-intrinsic agent knowledge: build, test, release, architecture, and sharp-edge notes that should travel with the code.

- Add durable project-specific notes here as they are discovered through real work.

## Maintaining this file

Keep this file for knowledge useful to almost every future agent session in this project.
Do not repeat what the codebase already shows; point to the authoritative file or command instead.
Prefer rewriting or pruning existing entries over appending new ones.
When updating this file, preserve this bar for all agents and keep entries concise.

## Schema lives twice

The agents table (and every table) is declared in both `internal/store/schema.sql`
(the Go store) and `scripts/mesh.sh` (`_SCHEMA_SQL`). A column change must land
in both, plus the migration steps: `_MIGRATIONS_SQL` + bumping `_SCHEMA_VERSION`
(stamped into the database's `PRAGMA user_version`) for bash, and the versioned
`migrateAgentsV2` pattern (schema_meta `schema_version`) for the Go store.
Existing databases get columns via `ALTER TABLE`, never by editing
`CREATE TABLE`. A column the code cannot live without also belongs in
`_REQUIRED_COLUMNS`, which is what stops a database being stamped current when
it is not.

## Tests

Go store tests: `go test ./...`. CLI tests: bats per file (`bats tests/mesh.bats`
etc.) — the full `bats tests/` run is slow, run files individually with a
generous timeout.
