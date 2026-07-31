#!/usr/bin/env bash
# mesh-wrapper.sh — always exits 0 for tmux run-shell hooks
SCRIPTS_DIR="$(cd "$(dirname "$0")" && pwd)"
"$SCRIPTS_DIR/mesh.sh" "$@" 2>/dev/null || true
