#!/usr/bin/env bash
# Open the mesh TUI. Bound to prefix + g by agent-mesh.tmux.
#
# tmux runs a key binding's command with no terminal to complain to: a missing
# binary means the window opens and closes again before anything is readable.
# So this resolves the binary itself and, when it cannot, prints why and waits.

set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$HERE")"

for candidate in "$ROOT/bin/mesh" "$(command -v mesh 2>/dev/null || true)"; do
    [[ -n "$candidate" && -x "$candidate" ]] || continue
    exec "$candidate" tui
done

printf 'mesh: the TUI binary is not built.\n\n' >&2
printf 'Build it with:\n  cd %s && go build -o bin/mesh ./cmd/mesh\n' "$ROOT" >&2
printf 'or re-run %s/install.sh, which does the same.\n\n' "$ROOT" >&2
printf 'Press any key to close this window.' >&2
read -r -n 1 -s
exit 1
