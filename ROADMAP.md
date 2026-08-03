# Roadmap

Sequenced, with the reason each step blocks the next. The order is forced by
dependencies, not preference.

## Where it stands

Running: peer messaging, delivery into a working agent's turn for Claude, Codex
and Gemini, idle wake for Pi, `dispatch`, the five brakes, the untrusted-peer
envelope, the tmux menu and status bar. 318 bash tests.

Built and not wired: the Go store. Channels, membership, private channels, access
rules, per-recipient delivery, append-only read receipts. 36 Go tests.

## 1. Server and the two transports

Everything else waits on this, for a hard reason rather than a scheduling one: a
remote mailbox cannot be a network share, because SQLite in WAL mode needs shared
memory and locking over NFS or SSHFS corrupts the file. A process has to own it.

One binary, three modes. The client **never** opens the database, in either
deployment, which is what makes an access rule worth more than a comment.

```
mesh serve            unix socket, local
mesh serve-stdio      over ssh, forced command in authorized_keys
mesh <command>        the client the hooks and the TUI both call
```

ssh with `ControlMaster` for the remote transport: no port, no tokens, credentials
already exist, and a hook costs roughly 20ms rather than a full handshake.

2026-08-03: the schema fix this step was gated on has landed. `messages` is on
the channel model, delivery and reads are append-only rows, every message carries
a content address, and `max-thread-msgs` is gone (see ARCHITECTURE.md). The
server itself is deferred: `mesh export` / `mesh import` over ssh reaches two
machines without a resident process.

## 2. Client, hook rewiring, budget ledger

The hooks currently open the database directly. They become clients.

Add the spend ceiling at the same time, because it is cheapest while the request
path is being written and because a mailbox meant to run continuously without one
is a bill, not a system. `max-blocks` bounds one agent's streak; nothing bounds
the fleet. Reuse `tmux-agent-resumer`'s quota reader rather than inventing a
second accounting.

Also: queue the intent before advancing delivery state, so a client that dies
mid-claim can be recovered by draining rather than losing the message. Taken from
firstmate's `state/.wake-queue` ordering.

## 3. The wake path

Three gates, all of which must agree, or the mail waits. See the README section
for the diagram and the reasoning. Gate 2 is not optional: without it, mesh types
a peer agent's message into a pane that has dropped to a shell, and that is
command execution by mail.

Land the composer classifier in `tmux-agent-resumer` first (see that repo's
`TASK-composer-content-guard.md`). Its payload is an operator-configured string,
so a mistake there is input corruption; here the payload is another agent's text
and the same mistake is remote code execution. Prove it where it is cheap.

## 4. Channels, recipients, files

The store is built. This is wiring plus the file store.

File bodies live outside the database under a directory only the server can read.
The row is the handle, the bytes are unreachable except through the service, and
that is where membership is checked. This is the one feature that is *only* honest
once step 1 exists, because on a shared uid an agent can read the store directly.

## 5. Enforcement

`dispatch` sandboxes by default, `--no-sandbox` opts out, `doctor` reports which
agents are fenced. Enforce what mesh launched, report what it did not.

Verified working on macOS: a seatbelt profile denying the data directory blocks
`cat`, `sqlite3` and the `/tmp` symlink route. Seatbelt matches **resolved** paths,
so a profile written against `/tmp/...` does nothing. `sandbox-exec` is formally
deprecated while remaining functional; Claude Code's own sandbox settings may be
the better hook. Linux equivalent is bubblewrap, or simply not mounting the
directory into a container.

Until this lands, local channel privacy is advisory and the README says so.

## 6. The TUI

Go, in its own tmux window rather than a popup, because a popup is modal and dies
on focus change. `prefix + g` opens it or jumps to it.

Read any channel you have access to, post, open a thread, DM one agent, upload a
file, and see who read what and when. Editing your own message keeps the original
in history rather than overwriting: an agent may already have acted on what it
read.

## Open design decisions

### A resident helper is a new participant type

A long-lived agent whose job is knowing where things are and who is doing what.
Not a per-errand invocation: accumulated context is the product, and auto-compact
is built for exactly that session shape.

Two constraints that are not negotiable if it exists, because it inverts the
threat model. Every other participant is untrusted peer input by default, but a
designated helper is one that other agents will trust *by construction* - mislead
it once and it misdirects everyone who consults it.

- **Read-only tools only.**
- **Answers are pointers with provenance** (`file:line` plus the command to
  verify), never prose conclusions. This is also what makes a cheap model
  acceptable for the role: a wrong pointer is checkable, a wrong assertion is not.

### Threads as durable context

A thread retains who said what, when, and what was decided. That is strictly more
than a vector store of chunks, which keeps similarity and throws provenance away.
Worth designing deliberately: thread summaries, search, and "what did we decide
about X". Blocked on removing the message cap first.

### Worktrees stay visible

Not a managed pool. A predictable path, an addressable pane, a queryable row.
Tools that hide worktrees read as simpler until you need to know what is running
where. Teardown must refuse rather than discard: dirty worktrees, and committed
work that has not landed, block removal.

### Coexisting with a second blocking hook

Mesh returns `{decision:"block"}` on `Stop`. firstmate exits 2 with a banner on
stderr for the same purpose. Two hooks cannot both own a turn boundary. The exit-2
form composes where a blocking decision does not, so if mesh ever has to share
`Stop`, that is the mechanism to switch to. Today the answer is
`@agent-mesh-delivery next-prompt`.

### The channel model, rescued from the deleted Go package

`internal/store` was removed in the commit that added this section. It was
unreachable by construction: no `package main` anywhere in the repo, and
`internal/` cannot be imported from outside the module. 1,830 lines, including
tests, that nothing ever ran.

Worse than dead. Its `schema.sql` and bash's `_SCHEMA_SQL` both targeted
`mesh.db` with incompatible definitions, so wiring the Go up would have corrupted
a live database. Keeping it cost a Go toolchain in CI and left the next reader
treating it as authoritative.

Two ideas in it are better than what bash does today, and are the reason this
note exists rather than just a deletion:

**One recipient mechanism instead of four.** A message is posted to a *channel*
and every member of that channel is a recipient, so a direct message, a named
group and a broadcast are the same operation with different membership. Today
bash has `to_session` for point-to-point plus a separate `broadcast` path, and a
DM is a channel with exactly two members expressed as a special case. Membership
also gives read receipts and join rules a natural home: `channel_rules` gated
joining by harness or model, and a rule matching nothing locked the channel
rather than opening it, which is the right default for a private one.

**An explicit `host` column.** The bash schema has no concept of which machine an
agent is on, so `send --remote` shells out over ssh and the roster cannot
represent a peer that is not local. A `host` column, defaulting to empty for
local, is what makes a genuinely distributed roster possible without reshaping
every row later. `tk_identity_list` already reserves the field for this reason.

Neither is worth building until the message cap comes off and there is a second
machine in play.
