# slk → tmux-agent-mesh Integration Plan

**Captain's directive:** Adapt slk's TUI (Bubble Tea v2 + Lipgloss v2, Tokyo Night theme, vim navigation) to use our mesh database instead of Slack's API. Keep the multi-agent + human interaction model. Complete all missing features.

**Date:** 2026-07-31

---

## 1. What We Have vs What slk Has

| Feature | slk (gammons/slk) | Our Mesh TUI | Gap |
|---------|-------------------|--------------|-----|
| **Framework** | Bubble Tea v2 + Lipgloss v2 | Bubble Tea v1 + Lipgloss v1 | Upgrade to v2 |
| **Theme** | Tokyo Night (59 themes) | Basic colors | Add theme system |
| **Vim navigation** | Full modal (j/k, h/l, i, Esc, gg, G) | Partial (j/k only) | Full vim mode |
| **Channels sidebar** | Sections, collapsible, unread badges | Flat list, basic | Sections + badges |
| **DM section** | Present with online indicators | Basic | Add presence |
| **Thread panel** | Side panel 35%, workspace threads view | None | Full thread support |
| **Message rendering** | Markdown, emoji, reactions, day separators | Plain text | Rich rendering |
| **Compose bar** | Multi-line, @mention autocomplete, paste | Single-line | Multi-line + autocomplete |
| **Fuzzy finder** | Ctrl+t / Ctrl+p | None | Channel/user finder |
| **Workspace rail** | Multi-workspace, 1-9 jump | None | We don't need (single mesh) |
| **Image rendering** | Kitty/Sixel/Half-block | None | Future |
| **Reaction picker** | Search-first, pill-style display | None | Add reactions |
| **Notification** | Desktop, mentions, keywords | None | Desktop notify |
| **Activity view** | 🔔 Activity tab | None | Future |
| **Starred channels** | Star section | None | Future |
| **Connection mgmt** | WebSocket reconnect, backoff | Basic WS | Improve |
| **Agent identity** | ❌ | ✅ audit trail, agent rename | Keep |
| **Channel soft-delete** | ❌ | ✅ soft delete | Keep |
| **Agent management** | ❌ | ✅ rename, membership tracking | Keep |
| **Custom backend** | Slack API only | Mesh server (own) | **THE SWAP** |

---

## 2. Architecture: What to Swap

slk's internal architecture:

```
cmd/slk/main.go          → TUI entry (keep, adapt)
internal/
  slack/                 → Slack API client (REPLACE with mesh client)
    client.go            → REST + WebSocket
    auth.go              → Token management
    mint.go              → Token scraping (DELETE)
  slackhttp/             → Browser header transport (KEEP, adapt)
  slackdesktop/          → Cookie/token reading (DELETE)
  tui/                   → TUI components (KEEP, adapt)
    model.go             → Main model
    sidebar.go           → Channel sidebar
    feed.go              → Message feed
    compose.go           → Compose bar
    help.go              → Help bar
    thread.go            → Thread panel
    finder.go            → Fuzzy finder
    theme.go             → Theme system
  store/                 → SQLite cache (KEEP, adapt schema)
  service/               → Business logic (KEEP, adapt)
  config/                → TOML config (KEEP)
```

**The swap:**
- `internal/slack/` → Replace with `internal/mesh/` — talks to our mesh server
- `internal/slackdesktop/` → Delete entirely (no Slack cookies needed)
- Agent auth → Reuse existing mesh agent registration
- SQLite schema → Add agent identity fields, keep mesh-compatible

---

## 3. Integration Steps

### Phase 1: Fork & Strip (1 session)
1. Fork slk into `tmux-agent-mesh/v2/`
2. Delete `internal/slackdesktop/` (cookie auth)
3. Delete `internal/slack/mint.go` (token scraping)
4. Strip Slack-specific config from `config.toml`
5. Rename module to `github.com/KakkoiDev/tmux-agent-mesh/v2`
6. Build succeeds with no Slack deps

### Phase 2: Mesh Backend Adapter (1-2 sessions)
1. Create `internal/mesh/client.go` — HTTP + WebSocket client for mesh server
2. Wire `auth.test` → mesh health check
3. Wire `channels.list` → mesh channels table
4. Wire `messages` → mesh messages table (CRUD)
5. Wire `users.list` → mesh agents table
6. Wire `reactions` → mesh reactions table (new schema)
7. Wire `threads` → mesh threads table
8. Wire presence → mesh `last_seen` field

### Phase 3: Schema Alignment (1 session)
1. Add `reactions` table to mesh schema
2. Add `thread_subscriptions` table
3. Add `read_markers` table (per-agent per-channel)
4. Add `channel_sections` for collapsible sections
5. Add `sort_order` to channels (already done)
6. Migrate existing mesh.db

### Phase 4: Agent Features Integration (1 session)
1. Agent identity → mesh `agents` table (already done)
2. Agent rename → keep existing `R` keybinding
3. Agent management → keep member list in sidebar
4. Audit trail → keep existing `transcript_path`
5. Agent soft-delete → keep existing soft delete
6. Human/multi-agent DMs → enrich with agent-aware compose

### Phase 5: Rich Features (2 sessions)
1. Markdown rendering (Goldmark, already in slk)
2. Emoji shortcodes (already in slk)
3. Reaction picker (already in slk)
4. Multi-line compose + autocomplete
5. Thread panel (already in slk)
6. Fuzzy finder for channels/agents
7. Theme system (already in slk, 59 themes)
8. Desktop notifications (already in slk)

### Phase 6: Polish & Ship (1 session)
1. Remove Slack branding
2. Add mesh branding
3. Update help bar for mesh commands
4. Write `AGENTS.md` for mesh-v2
5. Test with multi-agent setup
6. Symlink binary to `~/.local/bin/mesh`

---

## 4. What We Keep From Our Mesh

| Feature | Status | Notes |
|---------|--------|-------|
| Agent auto-naming | ✅ | Keep `019fb626` style IDs |
| Agent rename (R key) | ✅ | Merge into slk's keybinding system |
| Audit trail | ✅ | `transcript_path` per agent |
| Channel soft-delete | ✅ | Keep existing |
| Channel reorder (J/K) | ✅ | Keep + add sections |
| Member list in sidebar | ✅ | Keep as "MEMBERS" section |
| Active channel highlight | ✅ | Keep yellow accent |
| Context-aware help bar | ✅ | Merge with slk's help |

---

## 5. File Layout After Integration

```
tmux-agent-mesh/
├── cmd/
│   ├── mesh/           → Current mesh TUI (keep as legacy)
│   ├── meshv2/         → New slk-adapted TUI (PRIMARY)
│   └── slkdemo/        → Demo/prototype (keep)
├── internal/
│   ├── mesh/           → NEW: mesh API client (replaces internal/slack)
│   ├── tui/            → KEPT: slk's TUI components, adapted
│   ├── store/          → MERGED: mesh schema + slk cache schema
│   ├── service/        → MERGED: mesh business logic
│   ├── config/         → KEPT: TOML config
│   └── slackhttp/      → KEPT: browser headers (mesh doesn't need, but keep for future)
├── docs/
│   ├── lavish-prototype.html   → KEPT
│   ├── slk-integration-plan.md → THIS DOCUMENT
│   └── vision.md               → KEPT
├── scripts/
│   └── mesh.sh         → KEPT: mesh management
├── go.mod
└── go.sum
```

---

## 6. Estimated Effort

| Phase | Sessions | Lines Changed |
|-------|----------|---------------|
| Fork & Strip | 1 | ~500 removed |
| Mesh Backend | 1-2 | ~800 new |
| Schema | 1 | ~200 SQL |
| Agent Features | 1 | ~300 adapted |
| Rich Features | 2 | ~1500 adapted |
| Polish & Ship | 1 | ~200 |
| **Total** | **7-8** | **~3500** |

---

## 7. Next Action

1. **Fork slk** into `tmux-agent-mesh` repo on a `v2` branch
2. **Strip** Slack-specific code
3. **Build** the mesh client adapter
4. **Dispatch** Phase 1 to a crewmate with this document as the brief
5. **Save** this report and the slkdemo as design artifacts
