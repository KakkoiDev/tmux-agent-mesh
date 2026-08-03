#!/usr/bin/env bash
# Regenerate _schema_sql() in mesh.sh from the canonical schema.
#
# The canonical file lives in internal/store because Go's //go:embed cannot
# reach outside its own package directory. bash carries a generated copy rather
# than reading the file at runtime so that mesh.sh stays self-contained, which
# is the same trade lib/ makes with its checksum.
#
# Usage: scripts/gen-schema.sh            rewrite mesh.sh in place
#        scripts/gen-schema.sh --check    exit 1 if mesh.sh is out of date
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CANON="$ROOT/internal/store/schema.sql"
TARGET="$ROOT/scripts/mesh.sh"
BEGIN='# BEGIN GENERATED SCHEMA'
END='# END GENERATED SCHEMA'

[[ -f "$CANON" ]] || { printf 'no canonical schema at %s\n' "$CANON" >&2; exit 1; }

# PRAGMAs are per connection and mesh.sh sets its own in _SQL_PREAMBLE, so
# emitting them here would both duplicate that and print "wal" onto stdout.
render() {
    printf '%s\n' "$BEGIN"
    printf '# Generated from internal/store/schema.sql by scripts/gen-schema.sh.\n'
    printf '# Do not edit between these markers; edit the canonical file and rerun.\n'
    printf '_schema_sql() {\n'
    printf "    cat <<'MESH_SCHEMA_SQL'\n"
    grep -v '^PRAGMA ' "$CANON"
    printf 'MESH_SCHEMA_SQL\n'
    printf '}\n'
    printf '%s\n' "$END"
}

grep -q "^$BEGIN\$" "$TARGET" || {
    printf '%s has no %s marker\n' "$TARGET" "$BEGIN" >&2; exit 1; }

built=$(mktemp)
block=$(mktemp)
trap 'rm -f "$built" "$block"' EXIT
render > "$block"

# awk -v cannot carry a multi-line value, so the block is read from a file.
awk -v begin="$BEGIN" -v end="$END" -v blockfile="$block" '
    $0 == begin { while ((getline line < blockfile) > 0) print line; skip = 1; next }
    $0 == end   { skip = 0; next }
    !skip       { print }
' "$TARGET" > "$built"

if [[ "${1:-}" == "--check" ]]; then
    if cmp -s "$built" "$TARGET"; then
        printf 'schema is in sync\n'
        exit 0
    fi
    printf 'scripts/mesh.sh is out of date with internal/store/schema.sql\n' >&2
    printf 'run scripts/gen-schema.sh\n' >&2
    diff -u "$TARGET" "$built" >&2 || true
    exit 1
fi

cat "$built" > "$TARGET"
printf 'regenerated _schema_sql() in %s\n' "$TARGET"
