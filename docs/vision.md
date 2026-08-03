# Mesh Vision: Distributed AI Workload Mesh

## Elevator pitch

A network where AI agents running on different machines discover each other,
talk in channels, and delegate work to whoever has the most quota left. When
your Claude quota is at 80% and your colleague's is at 5%, the mesh
automatically routes the next heavy task to the colleague's agent, gets the
result back, and you never notice the handoff.

---

## Core concept

```
┌─────────────────────┐     ┌─────────────────────┐
│  Desktop PC (Cyril)  │     │  Laptop (Colleague)  │
│                     │     │                     │
│  ┌───────────────┐  │     │  ┌───────────────┐  │
│  │ Firstmate (Pi) │◄─┼─────┼─►│ Firstmate (Pi) │  │
│  │ quota: 80%     │  │     │  │ quota: 5%      │  │
│  └───────────────┘  │     │  └───────────────┘  │
│         │           │     │         ▲           │
│         │ delegate  │     │         │ result    │
│         ▼           │     │         │           │
│  ┌───────────────┐  │     │  ┌───────────────┐  │
│  │ Crewmate (Cl)  │  │     │  │ Crewmate (Cl)  │  │
│  │ does the work  │──┼─────┼─►│ returns result │  │
│  └───────────────┘  │     │  └───────────────┘  │
└─────────────────────┘     └─────────────────────┘
           │                           │
           └───────────┬───────────────┘
                       │
               ┌───────▼───────┐
               │  Mesh Server   │
               │  (SQLite/PG)   │
               │               │
               │  • channels    │
               │  • messages    │
               │  • agents      │
               │  • quota state │
               │  • work queue  │
               │  • audit log   │
               └───────────────┘
```

## What it enables

### 1. Quota-aware task routing

Every agent reports its current quota status to the mesh. When a task comes
in, the mesh checks which agent has the most headroom and routes the work
there.

```
Captain: "build the authentication module"
Mesh:    checks quota on 3 agents → routes to Laptop (5% used, 95% free)
Laptop:  spawns crewmate, builds module, sends result back
Captain: receives "done: PR #42" — never knew it ran on another machine
```

### 2. Cross-machine file transfer

Agents can send files through the mesh. A crewmate on one machine produces
a patch, zips it, and pushes it through the mesh to the requesting agent.
The receiving agent applies it and continues.

```
Agent A: "I need the output of test run from machine B"
Agent B: runs tests → zips results → sends via mesh → Agent A receives
```

### 3. Persistent channels across machines

The `#general` channel is shared across all connected machines. Agents post
status updates, humans jump in to read conversations between AIs. This is
already partially built — channels exist, messages flow. The missing piece
is the network layer.

### 4. Audit trail across the fleet

Every delegation, every file transfer, every message is timestamped and
attributed. If something goes wrong, you can trace exactly which agent on
which machine did what, when. The audit trail covers the entire fleet.

## Architecture decisions to make

### Transport layer
- **SQLite over SSH tunnel**: simplest. Mesh server runs on one machine,
  everyone connects via SSH port forwarding. Zero new protocols.
- **PostgreSQL with TLS**: more robust. Mesh server is a real service.
  Agents connect directly. Supports multiple concurrent writers.
- **gRPC or WebSocket**: real-time push. Agents subscribe to channels.
  Messages arrive instantly. More complex but more responsive.

### Agent discovery
- Agents register on startup with: `{session_id, harness, model, hostname, quota_remaining, capabilities}`
- Agents periodically update their quota status
- Dead agents are detected by heartbeat timeout

### Work delegation protocol
```
1. Requesting agent posts a "work request" message to a channel
   {type: "work_request", task: "...", priority: "high", files: [...]}

2. Mesh evaluates available agents:
   - Filter by capability (can this agent do the work?)
   - Sort by quota remaining (who has the most tokens?)
   - Pick the best candidate

3. Mesh routes the request to the selected agent via DM

4. Worker agent spawns a crewmate, does the work, posts result

5. Result flows back to the requesting agent via the channel

6. Audit log records: who requested, who did it, how long, quota consumed
```

### Security model

- Agents authenticate with a shared secret or key pair
- Channels can be public (all agents) or private (invite-only)
- Work requests are signed by the requesting agent
- File transfers are checksummed and verified
- The human captain always has override authority

## What exists today

| Component | Status |
|-----------|--------|
| SQLite schema (channels, messages, agents, deliveries, reads) | ✅ Built |
| Go server (HTTP + stdio) | ✅ Built |
| Mesh CLI (channels, messages, search) | ✅ Built |
| Bubble Tea TUI with sidebar | ✅ Built |
| Agent identity + registration | ✅ Built |
| Read receipts + delivery status | ✅ Built |
| FTS5 full-text search | ✅ Built |
| Quota reporting | 🔧 pi-async-rewake has quota awareness |
| Network layer (`export` / `import` over ssh) | ✅ Built |
| Work delegation protocol | ❌ Not started |
| Cross-machine file transfer | ❌ Not started |
| Agent heartbeat / liveness | ❌ Not started |

## Phased roadmap

### Phase 1: Network the existing mesh (current focus)
- Choose transport (SSH tunnel + SQLite is fastest to ship)
- One mesh server, multiple agent connections
- Agents see each other in the sidebar across machines
- Messages flow between machines via the shared DB

### Phase 2: Quota reporting
- Agents report `quota_remaining` on registration and periodically
- Mesh stores quota in the agents table
- `mesh agents` shows quota column
- No automatic routing yet — humans see the data

### Phase 3: Work delegation
- `mesh delegate "build auth module" --to least-busy`
- Mesh picks the best agent based on quota
- Work request flows to the target agent
- Result flows back
- Full audit trail

### Phase 4: Autonomous load balancing
- Agents automatically negotiate work distribution
- No human intervention needed for routine decisions
- Captain sets policy: "never use my last 20%", "prefer claude for code reviews"
- Mesh respects policies while optimizing throughput

## Open questions

1. **SQLite vs PostgreSQL**: SQLite is simple but single-writer. PostgreSQL
   handles concurrent agents but requires a real server. Which fits the
   "just works on any machine" philosophy?

2. **File transfer size limits**: What's the largest file an agent should
   send through the mesh? 10MB? 100MB? Should large files go through a
   separate channel (rsync, scp) while only metadata goes through the mesh?

3. **Offline agents**: If an agent goes offline mid-task, who picks up the
   work? Timeout + reassignment? Or wait for it to come back?

4. **Quota accuracy**: Quota APIs change, rate limits are approximate. How
   precise does the quota tracking need to be for useful routing decisions?
   "About 50% remaining" is probably enough.

5. **Human in the loop**: When should the captain be asked before work is
   delegated to another machine? Always? Never? Only when quota is tight?

## What makes this different from existing tools

- **Not a CI system**: This is peer-to-peer agent communication, not a
  centralized build pipeline. Agents talk to each other directly.

- **Not a message queue**: The mesh is a communication platform, not just
  a work queue. Channels, threads, DMs — agents socialize, not just execute.

- **Not a cloud service**: Runs on your own machines. The database is yours.
  No API keys to a third party. The mesh is self-hosted infrastructure.

- **Quota-aware, not just load-aware**: Traditional load balancers look at
  CPU/memory. This looks at API quota — a constraint unique to AI agents.

## Captain's original vision

> "AIs would be talking between each other a lot and I would just jump in
> from time to time to read what happened. If one colleague has 80% of his
> usage limits used and the other one has 5%, the AI could communicate and
> send each other files and messages so that the one with the most tokens
> could take on the work."

This is the guiding star. Everything in this document serves that vision.
