#!/usr/bin/env bash
set -euo pipefail

# tmux-agent-mesh installer.
#
# Harness wiring is opt-in per harness. Editing four live agent configs on a
# bare `./install.sh` is not something an installer should decide for you.

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_SRC="$HERE/bin/tmux-agent-mesh"
BIN_LINK="$HOME/.local/bin/tmux-agent-mesh"

CLAUDE_SETTINGS="$HOME/.claude/settings.json"
CODEX_HOOKS="$HOME/.codex/hooks.json"
GEMINI_SETTINGS="$HOME/.gemini/settings.json"
PI_EXT_DIR="$HOME/.pi/agent/extensions/tmux-agent-mesh"

DO_CLAUDE=0 DO_CODEX=0 DO_GEMINI=0 DO_PI=0 DO_STATUS=0 DO_TMUXCONF=0

usage() {
    cat <<'USAGE'
usage: ./install.sh [--all] [--claude] [--codex] [--gemini] [--pi]
                    [--status-bar] [--tmux-conf]

Always done:
  symlink bin/tmux-agent-mesh into ~/.local/bin
  create the database
  sync the Claude skill bundle

Opt in per harness (each edits that harness's live config, with a backup):
  --claude      SessionStart, SessionEnd, UserPromptSubmit, Stop
  --codex       SessionStart, SessionEnd, UserPromptSubmit, Stop
  --gemini      SessionStart, SessionEnd, BeforeAgent, AfterAgent
  --pi          symlink the extension (no config edit; pi auto-discovers)
  --all         every harness detected on PATH

Optional:
  --status-bar  inject #{@agent-mesh-status} into status-right
  --tmux-conf   add the run-shell plugin line to ~/.tmux.conf
USAGE
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --all)        DO_CLAUDE=1; DO_CODEX=1; DO_GEMINI=1; DO_PI=1 ;;
        --claude)     DO_CLAUDE=1 ;;
        --codex)      DO_CODEX=1 ;;
        --gemini)     DO_GEMINI=1 ;;
        --pi)         DO_PI=1 ;;
        --status-bar) DO_STATUS=1 ;;
        --tmux-conf)  DO_TMUXCONF=1 ;;
        -h|--help)    usage; exit 0 ;;
        *) printf 'unknown flag: %s\n\n' "$1" >&2; usage >&2; exit 1 ;;
    esac
    shift
done

say()  { printf '%s\n' "$*"; }
warn() { printf '%s\n' "$*" >&2; }

# ── writing config files without destroying symlinks ─────────────────
#
# ~/.claude/settings.json is commonly a symlink into a dotfiles repo. The usual
# `jq ... > tmp && mv tmp file` idiom REPLACES that symlink with a regular file,
# silently detaching the file from version control. `cat tmp > file` writes
# through the link instead. This is not hypothetical: it is how a settings.json
# managed by dotfiles quietly became a plain file once already.
write_through() {
    local target="$1" tmp="$2"
    cat "$tmp" > "$target"
    rm -f "$tmp"
}

backup_of() {
    local f="$1"
    local b="${f}.mesh-backup"
    [[ -e "$b" ]] && return 0
    cp "$f" "$b" 2>/dev/null && say "  backup: $b"
    return 0
}

require_jq() {
    command -v jq >/dev/null 2>&1 || {
        warn "jq is required to edit harness configs; install it and re-run"
        exit 1
    }
}

# Append one hook entry unless an identical command is already registered.
# Matches on the exact command string, so re-running is a no-op.
add_hook_entry() {
    local file="$1" event="$2" cmd="$3" matcher="${4:-}"
    local tmp="${file}.mesh-tmp.$$"

    if jq -e --arg e "$event" --arg c "$cmd" \
        '(.hooks[$e] // []) | map(.hooks[]? | select(.command == $c)) | length > 0' \
        "$file" >/dev/null 2>&1; then
        say "  $event already wired"
        return 0
    fi

    jq --arg e "$event" --arg c "$cmd" --arg m "$matcher" '
        .hooks //= {} |
        .hooks[$e] = ((.hooks[$e] // []) + [{matcher: $m, hooks: [{type: "command", command: $c}]}])
    ' "$file" > "$tmp"
    write_through "$file" "$tmp"
    say "  $event wired"
}

ensure_json_file() {
    local f="$1"
    mkdir -p "$(dirname "$f")"
    if [[ ! -e "$f" ]]; then
        printf '{}\n' > "$f"
        say "  created $f"
    fi
}

# ── base install ─────────────────────────────────────────────────────

say "tmux-agent-mesh"
say ""

mkdir -p "$(dirname "$BIN_LINK")"
ln -sf "$BIN_SRC" "$BIN_LINK"
say "cli: $BIN_LINK"
case ":$PATH:" in
    *":$(dirname "$BIN_LINK"):"*) ;;
    *) warn "note: $(dirname "$BIN_LINK") is not on PATH" ;;
esac

"$HERE/scripts/mesh.sh" init | sed 's/^/db: /'

for src in "$HERE"/.claude/skills/tmux-agent-mesh*; do
    [[ -d "$src" && -f "$src/SKILL.md" ]] || continue
    dest="$HOME/.claude/skills/$(basename "$src")"
    mkdir -p "$dest"
    cp -Rf "$src/." "$dest/"
    say "skill: $dest"
done

# ── claude ───────────────────────────────────────────────────────────

if [[ "$DO_CLAUDE" -eq 1 ]]; then
    require_jq
    say ""
    say "claude: $CLAUDE_SETTINGS"
    ensure_json_file "$CLAUDE_SETTINGS"
    [[ -L "$CLAUDE_SETTINGS" ]] && say "  (symlink to $(readlink "$CLAUDE_SETTINGS"); writing through it)"
    backup_of "$CLAUDE_SETTINGS"
    for ev in SessionStart SessionEnd UserPromptSubmit Stop; do
        add_hook_entry "$CLAUDE_SETTINGS" "$ev" "tmux-agent-mesh hook $ev --harness claude" ""
    done
fi

# ── codex ────────────────────────────────────────────────────────────
#
# Written to hooks.json rather than config.toml: a separate file cannot mangle
# an existing config.toml, and hooks.json takes precedence anyway.

if [[ "$DO_CODEX" -eq 1 ]]; then
    require_jq
    say ""
    say "codex: $CODEX_HOOKS"
    ensure_json_file "$CODEX_HOOKS"
    backup_of "$CODEX_HOOKS"
    for ev in SessionStart SessionEnd UserPromptSubmit Stop; do
        add_hook_entry "$CODEX_HOOKS" "$ev" "tmux-agent-mesh hook $ev --harness codex" ""
    done
fi

# ── gemini ───────────────────────────────────────────────────────────
#
# Gemini names the turn boundaries BeforeAgent and AfterAgent, and expects
# decision:"deny" where the others use "block". mesh.sh handles the payload.

if [[ "$DO_GEMINI" -eq 1 ]]; then
    require_jq
    say ""
    say "gemini: $GEMINI_SETTINGS"
    ensure_json_file "$GEMINI_SETTINGS"
    backup_of "$GEMINI_SETTINGS"
    for ev in SessionStart SessionEnd BeforeAgent AfterAgent; do
        add_hook_entry "$GEMINI_SETTINGS" "$ev" "tmux-agent-mesh hook $ev --harness gemini" ""
    done
fi

# ── pi ───────────────────────────────────────────────────────────────

if [[ "$DO_PI" -eq 1 ]]; then
    say ""
    say "pi: $PI_EXT_DIR"
    mkdir -p "$(dirname "$PI_EXT_DIR")"
    ln -sfn "$HERE/pi-extension" "$PI_EXT_DIR"
    say "  extension linked (pi auto-discovers; no settings.json edit)"
    say "  note: session_start does not fire in 'pi --print' mode, only interactively"
fi

# ── tmux ─────────────────────────────────────────────────────────────

if [[ "$DO_TMUXCONF" -eq 1 ]]; then
    conf="$HOME/.tmux.conf"
    line="run-shell $HERE/agent-mesh.tmux"
    say ""
    if [[ -f "$conf" ]] && grep -qF "agent-mesh.tmux" "$conf"; then
        say "tmux.conf: already present"
    else
        printf '\n# tmux-agent-mesh\n%s\n' "$line" >> "$conf"
        say "tmux.conf: added $line"
    fi
fi

if [[ "$DO_STATUS" -eq 1 ]]; then
    say ""
    # list-sessions, not `tmux info`: info exits non-zero with "no current
    # client" when a server is running but nothing is attached.
    if command -v tmux >/dev/null 2>&1 && tmux list-sessions >/dev/null 2>&1; then
        cur=$(tmux show-option -gqv status-right 2>/dev/null || true)
        case "$cur" in
            *"@agent-mesh-status"*) say "status-bar: already present" ;;
            *) tmux set -g status-right "#{@agent-mesh-status}${cur:+ }$cur"
               say "status-bar: prepended #{@agent-mesh-status}" ;;
        esac
    else
        warn "status-bar: no running tmux server; add #{@agent-mesh-status} to status-right yourself"
    fi
fi

# ── done ─────────────────────────────────────────────────────────────

say ""
if [[ $((DO_CLAUDE + DO_CODEX + DO_GEMINI + DO_PI)) -eq 0 ]]; then
    say "No harness wired. Re-run with --all, or pick: --claude --codex --gemini --pi"
fi
say "Check with: tmux-agent-mesh doctor"
say "Prove it with: tmux-agent-mesh selftest"
