# tmux-agent-mesh

Agent-to-agent messaging for coding agents running in tmux panes. Hook-native, no daemon, no
keystroke injection.

Sibling to [tmux-agent-tracker](https://github.com/cyril/tmux-agent-tracker) (which tracks agent
*status*) and tmux-agent-resumer (which resumes agents after rate limits). This one carries
*messages* between them.

## What it does

Agents in different panes share one SQLite mailbox. Agent A queues a message for Agent B; B receives
it through its own harness's hook mechanism and keeps working on it. You are a participant too: send
from any shell, inside tmux or not.

Delivery is harness-native, never `send-keys`:

| Harness | Delivery while working | Delivery while idle |
|---|---|---|
| Claude Code | `Stop` hook returns `decision:"block"` plus `additionalContext` | not possible, waits for the next prompt |
| Codex | `Stop` hook returns `{"decision":"block","reason":...}` | not possible, waits for the next prompt |
| Gemini CLI | `AfterAgent` blocks with a reason, which becomes the next prompt | not possible, waits for the next prompt |
| Pi | `pi.sendMessage(..., {deliverAs:"followUp"})` | **yes**, `pi.sendUserMessage` from an fs watcher |

Pi is the only harness that can wake an idle agent. That asymmetry is real and `roster` shows it in
the `PUSH` column, rather than hiding it.

## Status

Phase 1 of 7 is implemented and tested: the agent registry, alias and reference resolution, the
human participant, cleanup, and the `SessionStart`/`SessionEnd` hook path.

Not yet implemented: `send`, `broadcast`, `drain`, delivery to any harness, `dispatch`, `watch`,
`menu`, `install.sh`, the Pi extension. See the plan for the phase order.

## Requirements

- tmux 3.0+
- `sqlite3`
- `jq` (for `doctor` and, later, hook auto-configuration)
- bash 3.2+ (macOS system bash is supported)

## Install

No installer yet. For now:

```bash
git clone <this repo> ~/Code/tmux-agent-mesh
cd ~/Code/tmux-agent-mesh
./scripts/mesh.sh init
ln -sf "$PWD/bin/tmux-agent-mesh" ~/.local/bin/tmux-agent-mesh
```

Then wire the two hooks that exist, in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "SessionStart": [{ "matcher": "", "hooks": [{ "type": "command", "command": "tmux-agent-mesh hook SessionStart" }] }],
    "SessionEnd":   [{ "matcher": "", "hooks": [{ "type": "command", "command": "tmux-agent-mesh hook SessionEnd" }] }]
  }
}
```

These coexist with `tmux-agent-tracker`'s hooks on the same events: Claude Code runs same-event hooks
in parallel and deduplicates identical handlers.

Via TPM, add to `~/.tmux.conf`:

```
run-shell /path/to/tmux-agent-mesh/agent-mesh.tmux
```

The plugin file symlinks the CLI, creates the DB, and syncs the skill bundle. It deliberately does
**not** touch `status-right`, because `tmux-agent-tracker` already rewrites that string.

## Commands

```
init [--reset]                  create the database (--reset drops all data)
register --session <id> [--harness claude|codex|gemini|pi] [--alias <a>] [--pane <%N>] [--cwd <p>]
deregister [--session <id>]
name <alias>                    alias the calling session
roster [--json] [--remote <host>]
cleanup                         reap dead panes and old mail
hook <event>                    harness hook entry point (JSON on stdin)
doctor                          check dependencies and wiring
```

### References

Anywhere a `<ref>` is accepted, these resolve in order: alias, exact session id, `%pane`,
`session:window.pane`, then an unambiguous session-id prefix. An ambiguous prefix is an error, not a
guess.

Prefix matching uses `substr`, not SQL `LIKE`, so `_` and `%` in a reference are literal characters
rather than wildcards.

### The human participant

`session_id` `human` is seeded at `init` and cannot be deregistered. It is how you appear in the
mailbox. For a pane you are already looking at, typing is better than sending; `send` earns its place
for panes you are not watching, for `broadcast`, and for scripting from outside tmux.

## Cross-machine

`--remote <host>` shells out to `ssh <host> tmux-agent-mesh ...`. That is the whole feature: no
daemon, no new dependencies, works wherever ssh or a Tailnet already reaches.

WebRTC P2P (Trystero) is deliberately not used. It would need a resident Node process per machine, and
a public `appId` plus room namespace means anyone who joins the room can inject text that agents then
act on. Cross-node SQLite replication (id collisions, alias collisions, per-node `delivered_at`) is
also the actual hard part, and it is CRDT work.

## Configuration

Set in `~/.tmux.conf` with `set -g @option value`.

| Option | Default | Purpose |
|---|---|---|
| `@agent-mesh-enabled` | `on` | Master kill switch |
| `@agent-mesh-delivery` | `stop-block` | `stop-block`, `next-prompt`, or `off` (Claude/Codex/Gemini) |
| `@agent-mesh-pi-delivery` | `push` | `push`, `before-start`, or `off` |
| `@agent-mesh-max-hops` | `4` | Hop limit per thread |
| `@agent-mesh-max-thread-msgs` | `12` | Message limit per thread |
| `@agent-mesh-max-blocks` | `3` | Consecutive auto-continuations before downgrading a session |
| `@agent-mesh-max-broadcast` | `8` | Fan-out cap; `broadcast` refuses rather than truncating |
| `@agent-mesh-wake` | `off` | Opt-in `send-keys` wake for idle non-Pi panes |
| `@agent-mesh-on-mail` | `""` | Shell hook fired on new mail for `human` |
| `@agent-mesh-keybinding` | `m` | Menu key after the prefix |
| `@agent-mesh-debug-log` | `0` | `1` writes to `~/.tmux-agent-mesh/debug.log` |

## Data directory

`~/.tmux-agent-mesh/`

| Path | Purpose |
|---|---|
| `mesh.db` | SQLite, WAL mode: `agents`, `messages`, `threads`, `dispatches` |
| `notify/<session>.flag` | Wake flag per agent; the Pi extension watches its own |
| `delivery.log` | JSONL audit of every delivery |
| `config_cache` | Cached tmux option values, 60s TTL |
| `debug.log` | Present when `@agent-mesh-debug-log` is `1` |

Delivery is at-most-once: `delivered_at` is stamped at emit time. If a harness drops the payload the
message leaves the mailbox but stays in `delivery.log`. An ack protocol would need a signal no harness
provides, and a redelivery loop is worse than an audit log.

## Tests

```bash
bats tests/mesh.bats
```

62 tests. Bats cannot observe a harness parsing hook output, so every delivery phase also carries a
real two-pane manual test.

## License

MIT
