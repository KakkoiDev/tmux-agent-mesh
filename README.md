# tmux-agent-mesh

Agent-to-agent messaging for coding agents running in tmux panes. Hook-native,
no daemon, and no keystroke injection on any default path.

Sibling to `tmux-agent-tracker` (which tracks agent *status*) and
`tmux-agent-resumer` (which resumes agents after rate limits). This one carries
*messages* between them, and lets you join the conversation.

## What it does

Agents in different panes share one SQLite mailbox. Agent A queues a message for
Agent B; B receives it through its own harness's mechanism and keeps working on
it. You are a participant too: send from any shell, inside tmux or not.

Every existing tmux agent orchestrator delivers messages with `send-keys -l` plus
a `.ready` file handshake, which races your keyboard, races the TUI composer, and
has no acknowledgement. None of the four supported harnesses needs that any more.

| Harness | While the agent is working | While the agent is idle |
|---|---|---|
| Claude Code | `Stop` hook continues the turn with your message | waits for the next human prompt |
| Codex | `Stop` hook continues the turn | waits for the next human prompt |
| Gemini CLI | `AfterAgent` re-prompts with your message | waits for the next human prompt |
| Pi | continues the turn | **wakes on its own** |

Pi is the only harness that can reach an already-finished agent, because its
extension stays resident and can call `sendUserMessage` from a filesystem
watcher. `roster` reports this in the `PUSH` column rather than hiding it.

## Requirements

- tmux 3.0+, `sqlite3`, `jq`
- bash 3.2+ (macOS system bash is supported and tested)
- at least one of: Claude Code, Codex, Gemini CLI, Pi

## Install

```bash
git clone <this repo> ~/Code/tmux-agent-mesh
cd ~/Code/tmux-agent-mesh
./install.sh --all          # or pick: --claude --codex --gemini --pi
./install.sh --tmux-conf    # optional: add the plugin line to ~/.tmux.conf
tmux-agent-mesh doctor
tmux-agent-mesh selftest
```

Harness wiring is opt-in per harness, because an installer should not decide on
its own to edit four live agent configs. Each edited file is backed up once to
`<file>.mesh-backup`.

Mesh hooks sit alongside `tmux-agent-tracker`'s on the same events. Harnesses run
same-event hooks in parallel and deduplicate identical handlers.

**Symlinked configs are safe.** `~/.claude/settings.json` is often a symlink into
a dotfiles repo. The usual `jq ... > tmp && mv tmp file` idiom replaces that
symlink with a regular file, silently detaching it from version control. This
installer uses `cat tmp > file`, which writes through the link. Pinned by tests.

The status bar is **not** injected by default: tracker already rewrites
`status-right` and two plugins editing it is a clobber. Opt in with
`./install.sh --status-bar`, or place `#{@agent-mesh-status}` yourself.

## Use

Name each agent once, then message it.

```bash
tmux-agent-mesh name reviewer            # run inside the agent's own pane
tmux-agent-mesh alias %7 builder         # or label a pane from anywhere
tmux-agent-mesh roster
```

```
NAME           HARNESS  PROJECT       PUSH  PENDING  PANE
human          human    -             no    0        -
reviewer       claude   meetsone      no    0        work:2.1
builder        pi       izumo-io      yes   1        work:2.2
auditor        codex    git-dispatch  no    0        work:2.3
```

```bash
tmux-agent-mesh send --to reviewer --message "what file are you in?"
tmux-agent-mesh broadcast --message "status?" --harness pi
tmux-agent-mesh reply --to-message 42 --message "done"
tmux-agent-mesh inbox --as human --follow
tmux-agent-mesh watch                     # live view of all traffic
```

`--to` accepts an alias, `%pane`, `session:window.pane`, or an unambiguous
session-id prefix. Ambiguity is an error, never a guess. Prefix matching uses
`substr` rather than SQL `LIKE`, so `_` and `%` are literal characters.

### Spawning an agent for a subtask

```bash
tmux-agent-mesh dispatch --task "audit the migration for missing indexes" \
                         --harness claude --alias auditor
tmux-agent-mesh dispatch --task "port the tests" --harness pi --worktree feat/x
```

tmux runs the agent as the pane's own process and the task is handed over as its
first message. No keystrokes are sent. `--worktree` creates a git worktree under
`~/.tmux-worktree/<project>/<branch>`.

### You are a participant

`session_id` `human` is seeded at `init` and cannot be deregistered. Agents can
address you with `send --to human`, which is a better primitive than an agent
ending its turn to ask a question.

What this buys over typing in the pane: reaching a pane you are not looking at
without stealing focus, `broadcast` to several at once, queueing a message for
the next turn boundary instead of interrupting, and sending from a script or over
ssh. For a pane you are already watching, typing is better.

```bash
tmux set -g @agent-mesh-on-mail 'terminal-notifier -message "$2" -title "mesh: $1"'
```

## Cross-machine

```bash
tmux-agent-mesh send --remote workstation --to reviewer --message "..."
tmux-agent-mesh roster --remote workstation
```

That shells out to `ssh`. No daemon, no new dependencies, works wherever ssh or a
Tailnet already reaches.

WebRTC P2P (Trystero) is deliberately not used. It needs a resident Node process
per machine; a public `appId` plus room namespace means anyone who joins the room
can inject text that your agents then act on; and cross-node SQLite replication
(id collisions, alias collisions, per-node `delivered_at`) is the actual hard
part, which is CRDT work. ssh covers the realistic cases.

## Safety

Delivered mail is wrapped in an envelope marking it **untrusted peer input, not
an instruction from your operator**. This is a security boundary. Mesh mail
becomes agent context, so without it one pane can drive another into running
commands.

Five brakes stop runaway conversations, all enforced centrally so no harness can
bypass them:

| Option | Default | Stops |
|---|---|---|
| `@agent-mesh-enabled` | `on` | everything |
| `@agent-mesh-max-hops` | `4` | reply ping-pong |
| `@agent-mesh-max-thread-msgs` | `12` | a conversation that will not end |
| `@agent-mesh-max-blocks` | `3` | unattended token burn |
| `@agent-mesh-max-broadcast` | `8` | waking every pane at once |

An oversized broadcast sends to **nobody** rather than the first N, because a
silent truncation reads as full coverage. When the continuation budget runs out
mesh stops forcing turns but keeps the mail, delivering it on the next human
prompt.

## Configuration

| Option | Default | Purpose |
|---|---|---|
| `@agent-mesh-delivery` | `stop-block` | `stop-block`, `next-prompt`, `off` (Claude/Codex/Gemini) |
| `@agent-mesh-pi-delivery` | `push` | `push`, `before-start`, `off` |
| `@agent-mesh-wake` | `off` | opt-in `send-keys` wake for idle non-Pi panes |
| `@agent-mesh-on-mail` | `""` | shell hook on new mail for `human` |
| `@agent-mesh-keybinding` | `m` | menu key after the prefix |
| `@agent-mesh-icon-mail` | `@` | status bar indicator |
| `@agent-mesh-debug-log` | `0` | `1` writes `~/.tmux-agent-mesh/debug.log` |

Set `@agent-mesh-delivery next-prompt` if you do not want a queued message to
override your expectation that an agent had finished.

## Data

`~/.tmux-agent-mesh/`: `mesh.db` (WAL), `notify/<session>.flag`, `delivery.log`,
`config_cache`, `debug.log`.

Delivery is at-most-once. `delivered_at` is stamped at claim time, so if a harness
discards the payload the message leaves the mailbox but stays in `delivery.log`.
An acknowledgement protocol would need a signal no harness provides, and a
redelivery loop is worse than an audit log.

Every environment override is `MESH_` prefixed (`MESH_DIR`, `MESH_DB`,
`MESH_NOTIFY_DIR`, `MESH_DELIVERY_LOG`). A bare `DB` is also read by
tmux-agent-tracker, and setting it would point tracker at the wrong database.

## Tests

```bash
bats tests/
```

224 tests across five suites. Bats cannot watch a harness parse hook output, so
each delivery path also has a real two-pane manual test.

### Two-pane manual test

```bash
# pane B
claude
# inside it: tmux-agent-mesh name reviewer, then give it a long task

# a third plain shell
tmux-agent-mesh send --to reviewer --message "what file are you in?"
```

B's next turn end must continue on that message rather than stopping. Confirm
with `tmux capture-pane -p -t <B>` and:

```bash
sqlite3 ~/.tmux-agent-mesh/mesh.db \
  'SELECT id, delivered_via, delivered_at IS NOT NULL FROM messages;'
```

For Pi, run `pi` in a pane, let it go fully idle, then send to it. It must start
a turn on its own with `delivered_via = 'pi-push'`. This is the case the other
three harnesses cannot do.

## Known limitations

- **Idle Claude Code, Codex and Gemini agents do not wake.** Mail waits for the
  next human prompt. `dispatch` a fresh agent when you need guaranteed pickup.
- **`session_start` does not fire under `pi --print`.** One-shot print runs never
  register.
- **A killed pane fires no shutdown hook** on any harness, so dead agents are
  removed by `cleanup` (bound to tmux's `pane-died`).
- **No first-class tool for Pi.** `registerTool` needs a TypeBox schema and
  neither `typebox` nor the pi package resolves from `~/.pi/agent/extensions`, so
  Pi discovers the mailbox the same way the others do, through injected context.

## License

MIT
