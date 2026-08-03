# Upgrade Plan

> **Superseded 2026-08-03.** The schema half of this document is done and
> landed differently from what it proposed: `messages` is on the channel model,
> delivery and reads are append-only rows, every message carries a content
> address, `max-thread-msgs` is gone, and every command takes `--json` with a
> structured exit-code table. Read ARCHITECTURE.md and ROADMAP.md for what runs;
> this file is kept for the reasoning behind the gaps it found, not as a
> work list.

> Audit of every gap between what mesh ships today and what it takes to be a
> first-class communication layer for humans and agents. Written from the
> outside in: human UX first, then agent API, then internals.
>
> **Update 2025-07-31:** The Go store (`internal/store/`) has been recovered
> from git history and restored. 36 tests pass against `modernc.org/sqlite`.
> The store is the foundation for channels, read receipts, access rules, and
> the TUI — all of which were previously blocked on it.

---

## 1. Human UX: CLI gaps

The human currently has no first-class path. `inbox` is read-only, there is no
history command, and channels are not wired.

### 1.1 mark-read — acknowledge mail

**Missing.** `inbox` reads but never claims. A human has to shell out to `drain`
or raw SQL to mark messages seen, and `drain` with `--via human:read` is an
accidental API, not a designed one.

```
tmux-agent-mesh mark-read [--as <ref>] [--message-id <id>]
```

- No argument: claims all pending for the caller
- `--message-id`: claims one message
- Logs `delivered_via = human:read`
- Same atomic claim as `drain` under the hood, different surface

### 1.2 history — read past messages

**Missing.** `inbox` only shows undelivered mail. Once an agent drains it, the
human cannot see it without SQL. The database stores every message; nothing
exposes them.

```
tmux-agent-mesh history [--as <ref>] [--thread <id>] [--from <ref>]
                        [--since <iso>] [--limit <n>] [--json] [--follow]
```

Output format, one per line:

```
#42  thread t-...  human -> reviewer  2m ago  hop 0
     the message body
     `- read by reviewer 1m ago . builder 45s ago
```

### 1.3 channels — the missing half of the messaging model

**Store built, entirely unwired.** The Go store models channels as the one
recipient mechanism, but bash still uses `to_session` for everything. No CLI
subcommand reaches the channel tables, and `_SCHEMA_SQL` does not create them.

```
tmux-agent-mesh channel list [--json]
tmux-agent-mesh channel create <name> [--private] [--description <text>]
tmux-agent-mesh channel join <name> [--as <ref>]
tmux-agent-mesh channel leave <name> [--as <ref>]
tmux-agent-mesh channel archive <name>
tmux-agent-mesh channel members <name>
tmux-agent-mesh channel rule <name> --harness <h> [--model <m>]   # add
tmux-agent-mesh channel rule <name> --harness <h> --remove         # remove
```

A `general` channel is seeded at `init` and every registered agent joins it.

### 1.4 thread view

**Missing.** Threads exist in the schema but there is no way to read one.

```
tmux-agent-mesh thread <id> [--json] [--limit <n>]
```

Prints the full conversation with hop counts, timestamps, and read receipts.

### 1.5 edit and delete

**Missing.** Messages are immutable once written. The TUI design calls for edits
that keep the original in history; the CLI should match.

```
tmux-agent-mesh edit <id> --message "..."
tmux-agent-mesh delete <id>
```

`edit` appends a new row with `edit_of=<id>` rather than mutating the original,
because an agent may already have acted on what it read. `delete` soft-deletes
(`deleted_at`). Both are restricted to the original sender.

### 1.6 search

**Missing.** No way to find a past message.

```
tmux-agent-mesh search <query> [--channel <name>] [--from <ref>] [--since <iso>] [--limit <n>]
```

Uses SQLite FTS5 on `messages.body` plus `thread_id` and `from_session`.

### 1.7 who-read — read receipts

**Store built, not wired.** The Go store has an append-only `reads` table. Nothing
in bash creates it, writes to it, or queries it.

```
tmux-agent-mesh who-read <message-id>
```

Output:

```
message #42 read by:
  reviewer   2025-07-31 14:02:01
  builder    2025-07-31 14:03:15
  builder    2025-07-31 14:03:16   (re-read)
```

### 1.8 agent info

**Missing.** `roster` shows one row per agent. There is no single-agent detail
view.

```
tmux-agent-mesh info <ref>
```

Output:

```
session:  019fb5d8
alias:    (none)
harness:  pi
project:  firstmate-try
pane:     firstmate-main:1.2
state:    working
model:    claude-opus-4.5
push:     yes
pending:  0
block streak: 1 / 3
registered: 2025-07-30 09:15:00
last seen:  2025-07-31 14:05:00
channels:  general, backend
host:      (local)
```

### 1.9 shell completion

**Missing.** No tab completion for agent names, channel names, or subcommands.

- bash completion script for all subcommands
- dynamic completion of agent names (from `roster`) and channel names (from `channel list`)
- zsh completion via the same script with a compatibility shim

---

## 2. Agent API gaps

### 2.1 reply-to-sender hint is buried in context

When an agent drains mail, the envelope says `To answer: tmux-agent-mesh reply
--to-message <id> --message "..."`. That works but costs tokens. A structured
field would let the agent reply without parsing the prose.

Add to the drain JSON output:

```json
{
  "id": 42,
  "from_session": "019fb5d8",
  "from_name": "builder",
  "reply_command": "tmux-agent-mesh reply --to-message 42 --message \"...\""
}
```

### 2.2 no first-class tool for any harness

Pi cannot register a tool because `@earendil-works/pi-coding-agent` does not
resolve from `~/.pi/agent/extensions`. Claude, Codex and Gemini get injected
context but no tool either.

A tool that resolves for all four harnesses:

| Harness | Mechanism |
|---|---|
| Claude Code | `tools` field in settings.json hook output |
| Codex | `tools` field in hooks.json |
| Gemini | `tools` in settings.json |
| Pi | `registerTool` once the package resolves or via a bundling step |

The tool itself:

```
mesh_send(to: string, message: string, expect_reply?: boolean) -> { id: number, thread: string }
mesh_inbox() -> [{ id, from, body, thread, reply_to }]
mesh_roster() -> [{ name, harness, project, state }]
mesh_history(thread: string) -> [{ id, from, body, created_at }]
```

### 2.3 no structured output mode for roster/drain

Every agent parses the human-readable text envelope. `--json` exists on `drain`,
`roster` and `inbox`, but the default is prose. An agent that calls the CLI from
a bash tool should be able to get structured output everywhere. `--json` should
be universal across every subcommand that outputs data.

### 2.4 no agent-to-agent handshake

An agent can `send` but cannot know whether the peer is alive, what it is doing,
or whether it will reply. A lightweight ping/ack mechanism:

```
tmux-agent-mesh ping <ref>
```

Returns immediately with `{state, last_seen, pending_count, model}`. No message
is queued; it reads the registry directly. The peer never knows it was pinged.

### 2.5 no delegation with a deadline

`dispatch` spawns an agent but provides no completion signal. The calling agent
has to poll with `recv --wait` on a thread, and if the dispatched agent never
replies, the caller hangs until its own timeout.

```
tmux-agent-mesh delegate --task "..." --to <ref> --deadline <iso-duration>
```

Queues a message with `expect_reply=1` and a deadline. If no reply arrives by
the deadline, mesh sends the caller a timeout notice on the same thread.

### 2.6 no status broadcast

An agent that changes state (finishes a task, switches branches, hits an error)
has no way to announce it except by spamming a channel.

```
tmux-agent-mesh status --state <busy|free|blocked|done> [--detail "..."]
```

Updates the agent's own registry row with a user-visible status field. Appears
in `roster` and the TUI without generating a message.

---

## 3. TUI: terminal Slack

### 3.1 Language and stack

**Go with Bubble Tea** (`github.com/charmbracelet/bubbletea`, `bubbles`,
`lipgloss`).

Why:
- The Go store already exists (36 tests) and models channels, membership, rules,
  read receipts, files, and edits. The TUI is the consumer of that store.
- The server (`mesh serve`) is planned in Go, so the TUI client talks to the
  same binary over a unix socket or ssh.
- Bubble Tea is the most mature terminal UI framework in any language. It
  composes, tests, and handles resize/signals/SSR correctly.
- Single static binary, no runtime dependency, no npm install.
- Same language as the server means zero serialization glue between the TUI and
  the store.

### 3.2 Layout

```
+-- channels -----+-- #general (3 members) --------------------+
|  #general       |  14:02  reviewer   found it: the index is   |
|  #backend   *   |                    missing on orders.cust…  |
|  #sensitive [L] |           `- read by you 14:02 · builder    |
|  #ops            |              14:03 (×2)                   |
|                  |  14:04  builder    adding the migration    |
|  @reviewer      |           `- read by reviewer 14:04        |
|  @builder   1   |  14:07  you        ship it after CI        |
|  @auditor  idle |           `- unread                        |
|                  |---------------------------------------------|
|  AGENTS          |  > _                                       |
|  reviewer work  |                                            |
|  builder  idle  |  enter: send  ctrl-t: thread  ctrl-u:       |
|  auditor  idle  |  upload  ctrl-n: new DM  ctrl-k: channel   |
+-----------------+  ?: help  ctrl-c: quit                     |
```

Four panels, all resizable with the mouse or keyboard:

1. **Channel list** (left sidebar): channel names with unread badges,
   member counts, and lock icons. Direct messages below. Agent roster at the
   bottom with state indicators.

2. **Message area** (main): scrollable message feed. Each message shows sender,
   timestamp, body, and read receipts. Thread replies are inlined or open in a
   new view. System messages (agent joined, agent left, dispatch claimed) are
   rendered in dim text.

3. **Compose bar** (bottom): single-line input. Enter sends. Ctrl-T opens a
   thread on the highlighted message. Ctrl-U picks a file to upload. Ctrl-N
   opens a "new DM" picker. Ctrl-K opens a "new/join channel" dialog.

4. **Detail bar** (optional, toggle with `i`): selected message metadata,
   read receipts, edit history.

### 3.3 Keybindings

| Key | Action |
|---|---|
| `j` / `k` / `↓` / `↑` | Navigate messages |
| `g g` | Scroll to top |
| `G` | Scroll to bottom |
| `Enter` | Send composed message |
| `r` | Reply to highlighted message (pre-fills compose with thread context) |
| `e` | Edit your own highlighted message |
| `d` | Delete your own highlighted message (with confirm) |
| `Tab` | Cycle panels: channel list → messages → compose |
| `Shift+Tab` | Cycle panels backward |
| `Ctrl+T` | Open thread view for the highlighted message |
| `Ctrl+N` | New direct message (fuzzy-find an agent) |
| `Ctrl+K` | Create or join a channel |
| `Ctrl+U` | Upload a file |
| `i` | Toggle detail bar |
| `s` | Toggle agent sidebar |
| `/` | Search messages in current channel |
| `?` | Help overlay |
| `q` / `Ctrl+C` | Quit |

### 3.4 Views (beyond the main feed)

**Thread view.** Opens in-place overlaying the message area. Shows the thread
root pinned at top, then all replies in order with hop counts and indentation.
Esc returns to the channel.

**Search.** `/` opens a search bar. Results replace the message area. Each
result shows channel, sender, timestamp, and a snippet. Enter jumps to that
message in its channel.

**Channel picker.** Ctrl+K opens a fuzzy-find list of existing channels plus a
"create new" entry.

**Agent picker.** Ctrl+N opens a fuzzy-find over `roster` output. Selecting an
agent opens or switches to the DM with that agent.

**Help overlay.** `?` shows all keybindings in a centered modal.

**System messages.** Rendered dim and inline: `reviewer joined #backend`,
`dispatch auditor claimed`, `builder went idle`.

### 3.5 Status indicators

**Channels in the sidebar:**

| Icon | Meaning |
|---|---|
| `*` after name | Unread messages |
| `[L]` after name | Private channel with active access rules |

**Agents in the sidebar:**

| Dot | Meaning |
|---|---|
| `●` green | Working (turn_state = working) |
| `◐` yellow | Idle, mail pending |
| `○` grey | Idle, nothing pending |
| `✕` red | Dead (pane gone, not yet reaped) |
| `◔` | Dispatched, not yet claimed |

**Messages:**

| Marker | Meaning |
|---|---|
| `✓` | Sent (delivered_at set) |
| `✓✓` | Read by recipient |
| `◔` | Pending (delivered_at null) |

### 3.6 Mouse support

Bubble Tea supports mouse events natively. Click a channel to switch, click a
message to highlight it. The scroll wheel scrolls the message area. Right-click
opens a context menu with `Reply`, `Thread`, `Copy`.

### 3.7 Notification integration

When the TUI is not focused and mail arrives for the channel/DM the user is
viewing, it runs `@agent-mesh-on-mail` (same hook as today). For the TUI
itself, a terminal bell (`\a`) on new messages in the active channel, and an
unread badge in the tmux status bar.

### 3.8 Architecture inside the Go binary

```
cmd/mesh/
  main.go          # mesh serve | mesh serve-stdio | mesh tui | mesh <cli-subcommand>

internal/
  store/           # resurrected from the deleted package, wired to the server
    store.go       # CRUD for agents, channels, messages, threads, dispatches
    schema.sql     # single source of truth, replaces bash _SCHEMA_SQL
    readreceipt.go # append-only read log
    access.go      # channel rules engine

  server/
    serve.go       # unix socket listener
    stdio.go       # stdin/stdout for ssh forced-command mode
    handler.go     # dispatch commands to store

  client/
    client.go      # talks to server over unix socket or ssh
    commands.go    # send, drain, roster, etc. → same CLI flags, different backend

  tui/
    model.go       # top-level bubbletea model
    sidebar.go     # channel list + agent roster
    feed.go        # message area with viewport
    compose.go     # input bar with keybindings
    thread.go      # thread overlay
    search.go      # search bar + results
    picker.go      # fuzzy channel/agent picker
    styles.go      # lipgloss theme
```

The CLI subcommands currently in bash (`send`, `roster`, `inbox`, `drain`,
etc.) live in `cmd/mesh/main.go` as subcommands. They call the Go client or
open the database directly in local mode (until the server exists, then
always through the client).

### 3.9 Migration path: bash → Go

The bash CLI does not disappear. It moves to a compatibility shim:

```
bin/tmux-agent-mesh  →  exec ~/.tmux-agent-mesh/bin/mesh "$@"
```

The Go binary is the real implementation. The bash script stays as a symlink
target for backwards compatibility but delegates everything.

The hooks (`mesh.sh hook`) continue in bash until the Go server is stable
enough to trust with turn boundaries. The Go binary provides `mesh hook` as
a drop-in replacement with identical stdin/stdout contract.

### 3.10 File upload

Two paths in the TUI:
- `Ctrl+U` opens a file picker (via `fzf` or a built-in file browser)
- Drag-and-drop in a terminal that supports it (kitty, WezTerm, iTerm2)

Files land in `~/.tmux-agent-mesh/files/<channel>/<message-id>-<filename>`.
The message body becomes a link the agent can resolve:

```
[tmux-agent-mesh file] config.yaml (2048 bytes)
Retrieve with: tmux-agent-mesh file get 42
```

Agents get a `file get <id>` command that writes the file to stdout or a
specified path.

---

## 4. Schema: what needs to happen in the database

### 4.1 Channel tables (from the deleted Go store, ported to bash)

```sql
CREATE TABLE IF NOT EXISTS channels (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    private     INTEGER NOT NULL DEFAULT 0,
    archived    INTEGER NOT NULL DEFAULT 0,
    created_by  TEXT NOT NULL,
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS channel_members (
    channel_id  INTEGER NOT NULL REFERENCES channels(id),
    session_id  TEXT NOT NULL REFERENCES agents(session_id),
    joined_at   INTEGER NOT NULL DEFAULT (unixepoch()),
    PRIMARY KEY (channel_id, session_id)
);

CREATE TABLE IF NOT EXISTS channel_rules (
    channel_id  INTEGER NOT NULL REFERENCES channels(id),
    harness     TEXT,
    model       TEXT,
    action      TEXT NOT NULL DEFAULT 'allow',
    created_at  INTEGER NOT NULL DEFAULT (unixepoch())
);

CREATE TABLE IF NOT EXISTS reads (
    message_id  INTEGER NOT NULL REFERENCES messages(id),
    reader      TEXT NOT NULL,
    source      TEXT NOT NULL,   -- 'drain', 'client', 'tui'
    read_at     INTEGER NOT NULL DEFAULT (unixepoch())
);
CREATE INDEX IF NOT EXISTS idx_reads_message ON reads(message_id);
```

### 4.2 Column additions to existing tables

```sql
ALTER TABLE agents ADD COLUMN host TEXT DEFAULT '';        -- empty = local
ALTER TABLE agents ADD COLUMN status TEXT;                  -- free-form status line
ALTER TABLE messages ADD COLUMN channel_id INTEGER;         -- NULL = legacy DM
ALTER TABLE messages ADD COLUMN edit_of INTEGER;            -- NULL = original, else pointer
ALTER TABLE messages ADD COLUMN deleted_at INTEGER;         -- soft delete
ALTER TABLE messages ADD COLUMN file_path TEXT;              -- NULL = no file attachment
ALTER TABLE messages ADD COLUMN file_size INTEGER;
ALTER TABLE messages ADD COLUMN deadline INTEGER;            -- for delegate
ALTER TABLE threads ADD COLUMN channel_id INTEGER;           -- NULL = DM thread
```

### 4.3 FTS index

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    body, thread_id, from_session, content= messages, content_rowid=id
);
```

---

## 5. The human participant: first-class, not an afterthought

Today `human` is a row with `harness='human'`, `push_capable=0`, and a reserved
alias. That is enough for `send` to resolve it and for `inbox --as human` to
work, but nothing else.

### 5.1 Inbox auto-delivery for the human

The TUI drains the human's mailbox on focus. Before the TUI exists, the human
should have a poll command that both reads and marks:

```
tmux-agent-mesh check [--follow]
```

Like `inbox --follow` but it claims messages as it prints them, so the same
message is never shown twice. Equivalent to running `drain --via human:read`
in a loop.

### 5.2 Presence

The human has no `turn_state`. It should have a `last_active` timestamp updated
whenever the TUI is open or `inbox` is polled. `roster` shows `online` for a
human whose TUI is connected, `away` after 5 minutes, and nothing for never.

### 5.3 Notification hook improvements

`@agent-mesh-on-mail` fires once per message. For a human running the TUI, it
should fire once per batch with the channel name:

```
tmux set -g @agent-mesh-on-mail 'terminal-notifier -message "mesh: $3 new in $1"'
```

Where `$1` is the channel/DM name, `$2` is the sender, and `$3` is the count
of messages since the last check.

---

## 6. Remote: the path to multi-machine

Blocked on the Go server (ROADMAP.md §1). When it lands, these are the CLI
additions needed:

### 6.1 Remote config

```
tmux-agent-mesh config set remote.user mesh
tmux-agent-mesh config set remote.host mailbox.example.com
tmux-agent-mesh config set remote.identity ~/.ssh/mesh_ed25519
tmux-agent-mesh config show
```

### 6.2 Remote subcommands

Every subcommand that reads or writes the database gains `--remote <host>` or
uses the configured remote by default:

```
tmux-agent-mesh --remote workstation roster
tmux-agent-mesh --remote workstation send --to builder --message "status?"
```

### 6.3 The serve command

```
mesh serve --addr /var/run/mesh/mesh.sock --data /var/lib/mesh
mesh serve-stdio    # for ssh forced-command
```

The authorized_keys entry:

```
command="/usr/local/bin/mesh serve-stdio",no-pty,no-port-forwarding,
no-agent-forwarding,no-X11-forwarding ssh-ed25519 AAAA... laptop
```

---

## 7. The resident helper agent

A long-lived Pi agent whose job is knowing where things are and who is doing
what. Not a per-errand invocation: accumulated context is the product.

### 7.1 Constraints

- **Read-only tools only.** The helper never sends, dispatches, or edits. Its
  tools are `mesh_roster`, `mesh_history`, `mesh_search`, and the filesystem
  (`ls`, `rg`, `cat` within allowed paths).
- **Answers are pointers with provenance.** `file:line` plus the command to
  verify, never prose conclusions. A wrong pointer is checkable; a wrong
  assertion is not.
- **Runs as a dispatch with a dedicated alias** (`helper`). Other agents
  `send --to helper --message "where is the auth middleware?"` and get back
  a pointer.

### 7.2 Tools it needs

| Tool | Returns |
|---|---|
| `mesh_roster` | Who is online, what they are working on, what channel they are in |
| `mesh_history(channel)` | Recent messages in a channel, to answer "what did we decide" |
| `mesh_search(query)` | Full-text search across all channels |
| `mesh_who_read(message_id)` | Whether a specific agent saw a specific message |
| `mesh_info(agent)` | Detailed agent view |

### 7.3 Prompt shape

```
You are the mesh helper agent. Your job is to answer factual questions about
this project by reporting what you find, not by reasoning about it.

Rules:
- Every answer must cite a source: a file path with line number, a message id,
  or a tmux-agent-mesh command the asker can run themselves to verify.
- Never guess. If you cannot find the answer, say so.
- Do not send messages, dispatch agents, or edit anything.
```

---

## 8. What should not change

### 8.1 The drain seam

Every delivery path goes through one claiming function. This is the single best
design decision in the codebase. Adding channels, edits, file attachments, and
read receipts should all hang off the same claim rather than introducing new
delivery paths.

### 8.2 At-most-once delivery

The delivery row is written before the caller has the text. A client that drops
the payload loses that message rather than looping. This is correct for an
agent communication layer and should not become at-least-once.

### 8.3 The untrusted peer envelope

Every message that enters an agent's context is wrapped in a header stating it
is untrusted input from a peer. This is a security boundary, not decoration,
and it must survive every transport change.

### 8.4 Hooks never fail the turn

The `hook` dispatcher swallows every error and exits 0. A broken mailbox costs
messages, not sessions. This invariant must hold through the bash-to-Go migration.

### 8.5 No daemon for the bash CLI

The fire-and-forget model (one sqlite3 process per command, WAL mode) works
and is correct for a local deployment. The Go server is for remote and
enforcement; local bash should remain a supported deployment until the Go
server is proven.

---

## 9. Go store recovery

The `internal/store/` package was deleted in commit `87f6c2d` because it was
unreachable (`internal/` with no `package main`) and its schema was incompatible
with bash's `_SCHEMA_SQL`. Both problems are now fixable.

**Recovered files** (from `d452213`):

```
internal/store/
  schema.sql          # single source of truth for all tables
  store.go            # Open, Close, migrate, tx helper
  agents.go           # Register, Deregister, SetAlias, SetTurnState,
                      #   Resolve, Agent, Roster
  channels.go         # CreateChannel, ChannelByName, Join, Leave,
                      #   IsMember, MayJoin, MayRead, AddRule, Rules,
                      #   Channels, DMChannel
  messages.go         # Send (with Caps), Pending, Claim (atomic),
                      #   History, MarkRead, Receipts,
                      #   BlockStreak, BumpStreak, ResetStreak
  messages_test.go    # 16 tests: delivery, caps, receipts, history
  store_test.go       # 20 tests: agents, channels, rules, DMs
```

**Key differences from the bash schema** (these are intentional improvements):

| Concern | Bash `_SCHEMA_SQL` | Go `schema.sql` |
|---|---|---|
| Recipients | `messages.to_session` — one agent per message | `channel_members` — one channel, N recipients |
| Delivery | `messages.delivered_at` / `delivered_via` on the message row | `deliveries` table — per-recipient delivery tracking |
| Read receipts | None | `reads` table — append-only, source-tagged (`drain` vs `client`) |
| Files | None | `files` table + `file_access` audit log |
| Host column | None | `agents.host` — empty = local, else the machine name |
| Channel rules | None | `channel_rules` — allow-list by harness or model |
| Message edits | None | Schema has `edit_of` column ready (not yet in Go code) |
| Soft deletes | None | Schema has `deleted_at` column ready |

**What the recovery unlocks:**

1. The TUI has a real API to call instead of shelling out to bash
2. Channels and read receipts work without new schema design
3. The `host` column makes a distributed roster possible without reshaping every row
4. The `internal/` restriction is resolved by putting `cmd/mesh/` at the module root

---

## 10. Protocol: client ↔ server communication

The Go server (`mesh serve` / `mesh serve-stdio`) needs a well-defined protocol
before a single line of handler code is written. Every client (CLI, TUI, hooks)
depends on it.

### 10.1 Transport

| Transport | Listener | Client connects via |
|---|---|---|
| Unix socket | `mesh serve --addr /var/run/mesh/mesh.sock` | `mesh --server /var/run/mesh/mesh.sock <cmd>` |
| SSH stdio | `mesh serve-stdio` (forced command in `authorized_keys`) | `ssh mesh@host mesh <cmd>` |
| Local (no server) | N/A — client opens database directly | Default when no `--server` and `MESH_SERVER` unset |

### 10.2 Message format

Line-delimited JSON (JSON Lines, `application/jsonlines`). One request or
response per line, terminated by `\n`. No framing headers, no length prefix.

**Rationale:**
- Trivial to implement in bash (`printf '%s\n' | jq -c`)
- Trivial to implement in Go (`json.NewEncoder`, `bufio.Scanner`)
- Human-debuggable with `nc -U` or `socat`
- No dependency on a schema registry or codegen
- Forwards-compatible: unknown fields are ignored

### 10.3 Request envelope

```json
{
  "id": "req-001",
  "method": "send",
  "params": {
    "channel": "general",
    "body": "status check",
    "expect_reply": false
  },
  "auth": {
    "session_id": "019fb5d8",
    "host": "laptop"
  }
}
```

| Field | Type | Required | Purpose |
|---|---|---|---|
| `id` | string | yes | Client-chosen, echoed in the response. UUID or counter. |
| `method` | string | yes | The subcommand name: `send`, `roster`, `drain`, etc. |
| `params` | object | yes | Method-specific arguments. Same names as CLI flags. |
| `auth` | object | yes | Caller identity. Server trusts the transport (uid on unix socket, ssh key) but still needs the session_id to apply access rules. |

### 10.4 Response envelope

```json
{
  "id": "req-001",
  "ok": true,
  "result": {
    "id": 42,
    "thread": "t-1785462851-88360"
  }
}
```

```json
{
  "id": "req-002",
  "ok": false,
  "error": {
    "code": "forbidden",
    "message": "019fb5d8 is not a member of #sensitive"
  }
}
```

| Field | Type | Always present | Purpose |
|---|---|---|---|
| `id` | string | yes | Echo of the request id for correlation |
| `ok` | bool | yes | `true` = success, `false` = error |
| `result` | any | on success | Method-specific return value, always an object or array |
| `error.code` | string | on error | Machine-readable: `not_found`, `ambiguous`, `forbidden`, `invalid`, `internal` |
| `error.message` | string | on error | Human-readable, safe to show to the user |

### 10.5 Server-push (notifications)

For the TUI's live-update path, the server can push events without a
corresponding request:

```json
{
  "event": "message",
  "channel": "general",
  "data": {
    "id": 43,
    "from": "builder",
    "body": "migration done"
  }
}
```

```json
{"event": "agent_joined", "channel": "general", "agent": "auditor"}
{"event": "agent_left", "channel": "general", "agent": "auditor"}
{"event": "delivery", "message_id": 42, "to": "reviewer", "via": "claude:turn-end"}
{"event": "read", "message_id": 42, "reader": "builder", "source": "client"}
```

The client subscribes by sending a `watch` request. The server holds the
connection open (no response to `watch`) and streams events. Closing the
connection unsubscribes.

### 10.6 Method index

Every CLI subcommand becomes a method. The server validates and executes.

| Method | Params | Result | Notes |
|---|---|---|---|
| `roster` | `{}` | `[{session_id, harness, alias, host, project, state, push, pending, pane, model}]` | |
| `agent` | `{ref}` | `{session_id, harness, alias, ...}` | Single agent detail |
| `send` | `{channel, body, [expect_reply], [thread]}` | `{id, thread}` | Caps enforced server-side |
| `reply` | `{to_message, body}` | `{id, thread, hops}` | Validates sender is original recipient |
| `pending` | `{}` | `[{id, channel, from, body, ...}]` | Read-only, does not claim |
| `claim` | `{via}` | `[{id, channel, from, body, ...}]` | Atomic claim + delivery |
| `history` | `{channel, [limit], [before]}` | `[{id, from, body, created_at, ...}]` | Pagination via `before` cursor |
| `mark_read` | `{message_ids: [int]}` | `{count}` | Client read receipts |
| `receipts` | `{message_id}` | `[{reader, name, at, source}]` | |
| `channel_list` | `{}` | `[{id, name, kind, visibility, member_count}]` | |
| `channel_create` | `{name, [private], [description]}` | `{id, name}` | |
| `channel_join` | `{channel}` | `{id}` | Subject to access rules |
| `channel_leave` | `{channel}` | `{}` | |
| `channel_rule_add` | `{channel, subject, value}` | `{}` | |
| `channel_rule_list` | `{channel}` | `[{subject, value}]` | |
| `dm` | `{with}` | `{channel_id, name}` | Idempotent: finds or creates |
| `search` | `{query, [channel], [limit]}` | `[{id, channel, from, body_preview, created_at}]` | FTS5 |
| `ping` | `{ref}` | `{session_id, state, last_seen, pending}` | Reads registry, no message |
| `status` | `{state, [detail]}` | `{}` | Updates own status field |
| `watch` | `{channels: [string]}` | *(stream of events)* | Long-lived, server push |
| `file_put` | `{channel, name, body_b64}` | `{id, sha256}` | Binary-safe via base64 |
| `file_get` | `{id}` | `{name, size, sha256, body_b64}` | Membership-gated |
| `dispatch` | `{task, harness, [alias], [worktree], [env]}` | `{pane}` | Needs tmux on the same host |

### 10.7 Auth model

On a unix socket: the server reads `SO_PEERCRED` to get the caller's uid. The
`auth.session_id` in the request is trusted only if the uid matches the agent's
registered uid or the socket owner.

Over ssh: the forced command carries no auth field. The server derives identity
from the ssh key fingerprint, which maps to a host identity configured in
`mesh config`. The session_id in each request is validated against agents
registered on that host.

No bearer tokens, no API keys, no OAuth. The transport is the credential.

---

## 11. Order of work (revised)

Dependencies force the order, not preference.

```
 ✓ Go store recovered                     (36 tests passing)
 1. mark-read, history, info              (pure CLI, bash, no schema changes)
 2. Protocol spec finalized               (this document, §10)
 3. Go server + unix socket transport     (`mesh serve`)
 4. Go client library                     (`mesh <cmd> --server ...`)
 5. Schema migration: bash → Go schema    (one-shot, applied by server on start)
 6. Channel subcommands                   (create, join, leave, list, rule)
 7. Search                                (FTS5)
 8. Hook rewiring to Go client            (ROADMAP.md §2)
 9. Wake path for non-Pi agents           (ROADMAP.md §3)
10. File upload/download                  (store ready, needs CLI + TUI)
11. TUI                                   (Bubble Tea, consumes server + client)
12. Edit / delete messages                (edit_of + deleted_at in schema)
13. Resident helper agent                 (Pi, dispatched, read-only)
14. ssh transport (`mesh serve-stdio`)    (ROADMAP.md, remote deployment)
15. Enforcement: sandbox on dispatch      (ROADMAP.md §5)
16. Shell completion                      (low cost, high convenience)
```

Steps 1–2 can happen in parallel with 3. Step 5 is the hard cutover: once the
Go schema is live, bash hooks must go through the server or be retired.
Everything after 8 waits on the server.

---

## 12. Immediate low-cost wins

Things that take under an hour and fix real friction:

| # | What | Effort |
|---|---|---|
| 1 | `mark-read` subcommand | 20 lines of bash, wraps `drain` |
| 2 | `history` subcommand | 40 lines, SQL query + formatting |
| 3 | `info <ref>` subcommand | 30 lines, one SQL row formatted |
| 4 | `--json` on every data command | wrap existing JSON output paths |
| 5 | `thread <id>` subcommand | same as history, filtered by thread_id |
| 6 | Add `reply_command` field to drain JSON | 1 line in `_render_mail` |
| 7 | `ping <ref>` subcommand | 15 lines, reads registry, no message |
| 8 | Run `go test ./...` in CI | 5 lines in `.github/workflows/ci.yml` |
