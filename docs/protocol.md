# Mesh Protocol Specification

Version 1.0.0 — authoritative client-server contract for tmux-agent-mesh.

Every client (CLI, TUI, hooks) depends on this spec. The Go server (`mesh
serve` / `mesh serve-stdio`) implements it. Bash hooks implement the
request/response subset (no server-push).

---

## 1. Transport

| Transport | Listener | Client connects via |
|---|---|---|
| Unix socket | `mesh serve --addr /var/run/mesh/mesh.sock` | `mesh --server /var/run/mesh/mesh.sock <cmd>` |
| SSH stdio | `mesh serve-stdio` (forced command in `authorized_keys`) | `ssh mesh@host mesh <cmd>` |
| Local (no server) | N/A — client opens database directly | Default when no `--server` and `MESH_SERVER` unset |

### 1.1  Unix socket lifecycle

The server creates the socket at startup. Multiple clients may connect
concurrently. The server handles each connection in its own goroutine.

### 1.2  SSH stdio

The server reads from stdin and writes to stdout. One request per line,
one response per line. The connection is closed after the response.

---

## 2. Message format

Line-delimited JSON (JSON Lines, `application/jsonlines`). One request or
response per line, terminated by `\n`. No framing headers, no length prefix.

**Rationale:**

- Trivial to implement in bash (`printf '%s\n' | jq -c`)
- Trivial to implement in Go (`json.NewEncoder`, `bufio.Scanner`)
- Human-debuggable with `nc -U` or `socat`
- No dependency on a schema registry or codegen
- Forwards-compatible: unknown fields are ignored

---

## 3. Request envelope

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

---

## 4. Response envelope

Success:

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

Error:

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

### 4.1 Error codes

| Code | Meaning |
|---|---|
| `not_found` | The referenced agent, channel, or message does not exist |
| `ambiguous` | The reference matched multiple agents |
| `forbidden` | The caller is not allowed to perform this action |
| `invalid` | The request is malformed or contains invalid arguments |
| `internal` | An unexpected server error occurred |

---

## 5. Server-push (notifications)

For the TUI's live-update path, the server can push events without a
corresponding request:

```json
{"event": "message", "channel": "general", "data": {"id": 43, "from": "builder", "body": "migration done"}}
{"event": "agent_joined", "channel": "general", "agent": "auditor"}
{"event": "agent_left", "channel": "general", "agent": "auditor"}
{"event": "delivery", "message_id": 42, "to": "reviewer", "via": "claude:turn-end"}
{"event": "read", "message_id": 42, "reader": "builder", "source": "client"}
```

The client subscribes by sending a `watch` request. The server holds the
connection open (no response to `watch`) and streams events. Closing the
connection unsubscribes.

### 5.1 Event types

| Event | Fields | Emitted when |
|---|---|---|
| `message` | `channel`, `data{id, from, body, thread}` | A message is sent to a channel the client watches |
| `agent_joined` | `channel`, `agent` | An agent joins a watched channel |
| `agent_left` | `channel`, `agent` | An agent leaves a watched channel |
| `delivery` | `message_id`, `to`, `via` | A message is delivered (drained) |
| `read` | `message_id`, `reader`, `source` | A human marks a message as read |
| `agent_state` | `agent`, `state`, `detail` | An agent changes its status |
| `error` | `message` | A server-side error in the push stream |

---

## 6. Method index

Every CLI subcommand becomes a method. The server validates and executes.

| Method | Params | Result | Notes |
|---|---|---|---|
| `roster` | `{}` | `[{session_id, harness, alias, host, project, state, push, pending, pane, model}]` | |
| `agent` | `{ref}` | `{session_id, harness, alias, model, host, project, state, push_capable, pending, pane, block_streak, cwd, transcript_path, last_seen}` | Single agent detail |
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

---

## 7. Auth model

### 7.1 Unix socket

On a unix socket: the server reads `SO_PEERCRED` to get the caller's uid.
The `auth.session_id` in the request is trusted only if the uid matches the
agent's registered uid or the socket owner.

### 7.2 SSH stdio

Over ssh: the forced command carries no auth field. The server derives
identity from the ssh key fingerprint, which maps to a host identity
configured in `mesh config`. The session_id in each request is validated
against agents registered on that host.

### 7.3 Local mode

No auth. The client opens the database directly. Trust is inherited from
filesystem permissions.

**Design principle:** No bearer tokens, no API keys, no OAuth. The transport
is the credential.

---

## 8. Delivery guarantees

1. **At-most-once delivery.** The delivery row is written before the
   caller has the text. A client that drops the payload loses that message
   rather than looping.
2. **Atomic claim.** The `claim` method uses `BEGIN IMMEDIATE` so two
   concurrent drains cannot both take the same message.
3. **Hooks never fail the turn.** The server swallows errors on hook
   delivery paths and always responds `ok: true` or closes gracefully.

---

## 9. Versioning

The protocol version is embedded in the server's greeting on connect:

```json
{"protocol": "mesh-1.0.0", "server": "tmux-agent-mesh/1.0.0"}
```

Clients should check the major version. Minor version bumps are backward
compatible. A major version bump indicates a breaking change to the
envelope, transport, or auth model.

---

## 10. Examples

### 10.1 Send a message

Request:
```json
{"id":"1","method":"send","params":{"channel":"general","body":"status check"},"auth":{"session_id":"019fb5d8"}}
```

Response:
```json
{"id":"1","ok":true,"result":{"id":42,"thread":"t-1785462851-88360"}}
```

### 10.2 Get the roster

Request:
```json
{"id":"2","method":"roster","params":{},"auth":{"session_id":"019fb5d8"}}
```

Response:
```json
{"id":"2","ok":true,"result":[{"session_id":"019fb5d8","harness":"pi","alias":"builder","state":"working","pending":0}]}
```

### 10.3 Drain mail

Request:
```json
{"id":"3","method":"claim","params":{"via":"claude:turn-end"},"auth":{"session_id":"019fb5d8"}}
```

Response:
```json
{"id":"3","ok":true,"result":[{"id":42,"thread_id":"t-...","from_session":"...","from_name":"reviewer","body":"found it","hops":0,"expect_reply":0,"reply_command":"tmux-agent-mesh reply --to-message 42 --message \"...\""}]}
```

### 10.4 Watch for live events

Request:
```json
{"id":"4","method":"watch","params":{"channels":["general","backend"]},"auth":{"session_id":"019fb5d8"}}
```

Server push (streamed, no response envelope):
```json
{"event":"message","channel":"general","data":{"id":43,"from":"auditor","body":"passed"}}
{"event":"agent_joined","channel":"backend","agent":"reviewer"}
```

### 10.5 Error response

Request:
```json
{"id":"5","method":"send","params":{"channel":"#sensitive","body":"hi"},"auth":{"session_id":"guest"}}
```

Response:
```json
{"id":"5","ok":false,"error":{"code":"forbidden","message":"guest is not a member of #sensitive"}}
```
