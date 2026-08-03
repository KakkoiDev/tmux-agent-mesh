#!/usr/bin/env bash
# Regenerate the embedded SQL in mesh.sh from the canonical .sql files.
#
# The canonical files live in internal/store because Go's //go:embed cannot
# reach outside its own package directory. bash carries a generated copy rather
# than reading them at runtime so that mesh.sh stays self-contained, which is the
# same trade lib/ makes with its checksum.
#
# Usage: scripts/gen-schema.sh            rewrite mesh.sh in place
#        scripts/gen-schema.sh --check    exit 1 if mesh.sh is out of date
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SRC="$ROOT/internal/store"
TARGET="$ROOT/scripts/mesh.sh"
BEGIN='# BEGIN GENERATED SCHEMA'
END='# END GENERATED SCHEMA'

# file:function pairs, in the order they are emitted.
FILES='schema.sql:_schema_sql
migrate_v1_pre.sql:_migrate_v1_pre_sql
migrate_v1_post.sql:_migrate_v1_post_sql'

# PRAGMAs that configure the connection are per connection and mesh.sh sets its
# own in _SQL_PREAMBLE, so emitting journal_mode here would both duplicate that
# and print "wal" onto stdout. foreign_keys is kept: the migrations turn it off
# deliberately for the table rebuild.
render() {
    printf '%s\n' "$BEGIN"
    printf '# Generated from internal/store/*.sql by scripts/gen-schema.sh.\n'
    printf '# Do not edit between these markers; edit the canonical file and rerun.\n'
    local pair f fn
    while IFS= read -r pair; do
        [[ -z "$pair" ]] && continue
        f="${pair%%:*}"; fn="${pair##*:}"
        [[ -f "$SRC/$f" ]] || { printf 'missing %s\n' "$SRC/$f" >&2; exit 1; }
        printf '%s() {\n' "$fn"
        printf "    cat <<'MESH_SQL_EOF'\n"
        grep -vE '^PRAGMA (journal_mode|busy_timeout)' "$SRC/$f"
        printf 'MESH_SQL_EOF\n'
        printf '}\n'
    done <<EOF
$FILES
EOF
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
    printf 'scripts/mesh.sh is out of date with internal/store/*.sql\n' >&2
    printf 'run scripts/gen-schema.sh\n' >&2
    diff -u "$TARGET" "$built" >&2 || true
    exit 1
fi

cat "$built" > "$TARGET"
printf 'regenerated embedded SQL in %s\n' "$TARGET"
