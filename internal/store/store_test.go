package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func register(t *testing.T, s *Store, id, harness, alias, model string) {
	t.Helper()
	if err := s.Register(Agent{SessionID: id, Harness: harness, Model: model}); err != nil {
		t.Fatalf("register %s: %v", id, err)
	}
	if alias != "" {
		if err := s.SetAlias(id, alias); err != nil {
			t.Fatalf("alias %s: %v", alias, err)
		}
	}
}

func TestHumanIsSeededAndCannotBeRemoved(t *testing.T) {
	s := open(t)
	if _, err := s.Agent(HumanID); err != nil {
		t.Fatalf("human not seeded: %v", err)
	}
	if err := s.Deregister(HumanID); err == nil {
		t.Fatal("deregistering the human should be refused")
	}
}

func TestRegisterEvictsTheStaleAgentOnAPane(t *testing.T) {
	s := open(t)
	if err := s.Register(Agent{SessionID: "old", Harness: "claude", TmuxPane: "%1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(Agent{SessionID: "new", Harness: "claude", TmuxPane: "%1"}); err != nil {
		t.Fatal(err)
	}
	// Otherwise addressing by pane resolves to an agent that is no longer there.
	if _, err := s.Agent("old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale agent on the pane should be gone, got %v", err)
	}
	if id, err := s.Resolve("%1"); err != nil || id != "new" {
		t.Fatalf("pane should resolve to the live agent, got %q %v", id, err)
	}
}

func TestRegisterKeepsAgentsOnTheSamePaneIdOnDifferentHosts(t *testing.T) {
	s := open(t)
	if err := s.Register(Agent{SessionID: "a", Harness: "claude", TmuxPane: "%1", Host: "laptop"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Register(Agent{SessionID: "b", Harness: "claude", TmuxPane: "%1", Host: "server"}); err != nil {
		t.Fatal(err)
	}
	// A shared mailbox serves several machines, and pane ids are only unique
	// within one tmux server.
	if _, err := s.Agent("a"); err != nil {
		t.Fatalf("agent on another host should survive: %v", err)
	}
}

func TestResolveOrderAndAmbiguity(t *testing.T) {
	s := open(t)
	register(t, s, "abc111", "claude", "reviewer", "")
	register(t, s, "abc222", "codex", "", "")

	if id, err := s.Resolve("reviewer"); err != nil || id != "abc111" {
		t.Fatalf("alias: got %q %v", id, err)
	}
	if id, err := s.Resolve("abc222"); err != nil || id != "abc222" {
		t.Fatalf("exact id: got %q %v", id, err)
	}
	if _, err := s.Resolve("abc"); !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("prefix matching two agents must be ambiguous, got %v", err)
	}
	if _, err := s.Resolve("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown ref: got %v", err)
	}
}

// _ and % are LIKE wildcards. With LIKE, "abc_" would also match "abcXdef" and
// mail would silently go to the wrong agent.
func TestResolveTreatsUnderscoreAsALiteral(t *testing.T) {
	s := open(t)
	register(t, s, "abc_1", "claude", "", "")
	register(t, s, "abcX2", "claude", "", "")
	id, err := s.Resolve("abc_")
	if err != nil || id != "abc_1" {
		t.Fatalf("underscore must be literal: got %q %v", id, err)
	}
}

func TestAliasIsUniqueAndReserved(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "scout", "")
	register(t, s, "b", "claude", "", "")
	if err := s.SetAlias("b", "scout"); err == nil {
		t.Fatal("a taken alias should be refused")
	}
	if err := s.SetAlias("b", HumanID); err == nil {
		t.Fatal("the human alias should be reserved")
	}
	if err := s.SetAlias("b", "bad name!"); err == nil {
		t.Fatal("a malformed alias should be refused")
	}
}

func TestOnlyPiIsPushCapable(t *testing.T) {
	s := open(t)
	register(t, s, "p", "pi", "", "")
	register(t, s, "c", "claude", "", "")
	if a, _ := s.Agent("p"); !a.PushCapable {
		t.Fatal("pi should be push capable")
	}
	if a, _ := s.Agent("c"); a.PushCapable {
		t.Fatal("claude cannot be reached while idle without keystrokes")
	}
}

// The agents table as it was before transcript_path existed. Opening such a
// database must add the column and keep old rows readable.
const v1AgentsSchema = `
CREATE TABLE agents (
    session_id    TEXT PRIMARY KEY,
    harness       TEXT NOT NULL,
    alias         TEXT UNIQUE,
    model         TEXT,
    host          TEXT NOT NULL DEFAULT '',
    tmux_pane     TEXT NOT NULL DEFAULT '',
    tmux_target   TEXT NOT NULL DEFAULT '',
    cwd           TEXT NOT NULL DEFAULT '',
    project_name  TEXT NOT NULL DEFAULT '',
    push_capable  INTEGER NOT NULL DEFAULT 0,
    block_streak  INTEGER NOT NULL DEFAULT 0,
    turn_state    TEXT NOT NULL DEFAULT 'idle'
        CHECK (turn_state IN ('idle', 'working')),
    registered_at INTEGER NOT NULL DEFAULT (unixepoch()),
    last_seen     INTEGER NOT NULL DEFAULT (unixepoch())
);`

func TestRegisterAutoNamesAgents(t *testing.T) {
	s := open(t)
	if err := s.Register(Agent{SessionID: "019fb695aaaa", Harness: "pi"}); err != nil {
		t.Fatal(err)
	}
	a, err := s.Agent("019fb695aaaa")
	if err != nil {
		t.Fatal(err)
	}
	if a.Alias != "pi-019fb695" {
		t.Fatalf("auto alias: got %q, want pi-019fb695", a.Alias)
	}
	// The human row is seeded by name and registering as the human must not
	// give it a generated one.
	if err := s.Register(Agent{SessionID: HumanID, Harness: "human"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent(HumanID); a.Alias != "human" {
		t.Fatalf("the human alias should be untouched, got %q", a.Alias)
	}
}

// The alias is UNIQUE, so two agents sharing a session-id prefix must get
// distinct generated names instead of failing the second registration.
func TestRegisterAutoAliasHandlesCollisions(t *testing.T) {
	s := open(t)
	for _, id := range []string{"019fb695aaaa", "019fb695bbbb"} {
		if err := s.Register(Agent{SessionID: id, Harness: "pi"}); err != nil {
			t.Fatal(err)
		}
	}
	first, _ := s.Agent("019fb695aaaa")
	second, _ := s.Agent("019fb695bbbb")
	if first.Alias != "pi-019fb695" {
		t.Fatalf("first: got %q", first.Alias)
	}
	if second.Alias != "pi-019fb695-2" {
		t.Fatalf("collision: got %q, want pi-019fb695-2", second.Alias)
	}
}

func TestRegisterKeepsAnAliasTheAgentChose(t *testing.T) {
	s := open(t)
	if err := s.Register(Agent{SessionID: "019fb695aaaa", Harness: "pi", Alias: "builder"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent("019fb695aaaa"); a.Alias != "builder" {
		t.Fatalf("explicit alias: got %q", a.Alias)
	}
	// Re-registering without a name must not replace the chosen one.
	if err := s.Register(Agent{SessionID: "019fb695aaaa", Harness: "pi"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent("019fb695aaaa"); a.Alias != "builder" {
		t.Fatalf("re-register should keep the alias, got %q", a.Alias)
	}
	// Re-registering with a new explicit name replaces it.
	if err := s.Register(Agent{SessionID: "019fb695aaaa", Harness: "pi", Alias: "scout"}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent("019fb695aaaa"); a.Alias != "scout" {
		t.Fatalf("an explicit alias should win, got %q", a.Alias)
	}
	if err := s.Register(Agent{SessionID: "bad", Harness: "pi", Alias: "bad name!"}); err == nil {
		t.Fatal("a malformed explicit alias should be refused")
	}
}

func TestTranscriptPathRoundTrips(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	if p, err := s.Transcript("a"); err != nil || p != "" {
		t.Fatalf("no transcript yet: got %q %v", p, err)
	}
	const path = "/home/x/.claude/projects/slug/019fb695aaaa.jsonl"
	if err := s.SetTranscript("a", path); err != nil {
		t.Fatal(err)
	}
	if p, err := s.Transcript("a"); err != nil || p != path {
		t.Fatalf("round trip: got %q %v", p, err)
	}
	if err := s.SetTranscript("ghost", "/tmp/x.jsonl"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown agent: got %v", err)
	}
	if err := s.SetTranscript("a", ""); err == nil {
		t.Fatal("an empty path should be refused")
	}
}

// A database created before transcript_path existed must gain the column on
// Open, with the NOT NULL DEFAULT ” shape, and stay fully usable.
func TestMigrationAddsTranscriptPath(t *testing.T) {
	dir := t.TempDir()
	v1, err := sql.Open("sqlite", filepath.Join(dir, "mesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v1.Exec(v1AgentsSchema); err != nil {
		t.Fatal(err)
	}
	if err := v1.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer s.Close()

	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('agents')
		 WHERE name = 'transcript_path'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("transcript_path column missing after migration")
	}
	// And the column is usable end to end on the migrated database.
	if err := s.Register(Agent{SessionID: "019fb695aaaa", Harness: "pi"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTranscript("019fb695aaaa", "/x/a.jsonl"); err != nil {
		t.Fatal(err)
	}
	if p, _ := s.Transcript("019fb695aaaa"); p != "/x/a.jsonl" {
		t.Fatalf("got %q", p)
	}
}

// unixepoch() is second-grained, so age the rows first: the movement has to be
// observable no matter when the test runs.
func TestSendAndClaimMoveLastSeen(t *testing.T) {
	s := open(t)
	register(t, s, "sender", "claude", "", "")
	register(t, s, "reader", "pi", "", "")
	ch := channelWith(t, s, "team", "sender", "reader")
	if _, err := s.db.Exec(
		`UPDATE agents SET last_seen = 1 WHERE session_id IN ('sender', 'reader')`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Send(Post{ChannelID: ch.ID, From: "sender", Body: "hi"}, DefaultCaps()); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent("sender"); a.LastSeen <= 1 {
		t.Fatalf("send should move last_seen, got %d", a.LastSeen)
	}
	if _, err := s.Claim("reader", "test"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent("reader"); a.LastSeen <= 1 {
		t.Fatalf("claim should move last_seen, got %d", a.LastSeen)
	}
}

// ── channels and access ──────────────────────────────────────────────

func TestPublicChannelIsReadableWithoutJoining(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch, err := s.CreateChannel("general", "channel", "public", "", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MayRead(ch.ID, "b"); err != nil {
		t.Fatalf("a public channel should be readable by any participant: %v", err)
	}
}

func TestPrivateChannelIsMembersOnly(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch, err := s.CreateChannel("secrets", "channel", "private", "", "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MayRead(ch.ID, "a"); err != nil {
		t.Fatalf("the owner is a member: %v", err)
	}
	if err := s.MayRead(ch.ID, "b"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non-member must be refused, got %v", err)
	}
}

func TestAnUnknownSessionCannotReadAPublicChannel(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	ch, _ := s.CreateChannel("general", "channel", "public", "", "a")
	if err := s.MayRead(ch.ID, "ghost"); err == nil {
		t.Fatal("an unregistered session should not read anything")
	}
}

// The rule set is an allow-list the moment it is non-empty. A channel whose rules
// match nobody must be locked, not open: the cost of a wrong no is an error
// message, the cost of a wrong yes is the thing the channel existed to prevent.
func TestAccessRulesFailClosed(t *testing.T) {
	s := open(t)
	register(t, s, "owner", "claude", "", "")
	register(t, s, "cdx", "codex", "", "")
	ch, _ := s.CreateChannel("sensitive", "channel", "private", "", "owner")

	if err := s.MayJoin(ch.ID, "cdx"); err != nil {
		t.Fatalf("with no rules, membership is the only gate: %v", err)
	}
	if err := s.AddRule(ch.ID, "harness", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.MayJoin(ch.ID, "cdx"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("codex matches no rule and must be refused, got %v", err)
	}
	if err := s.MayJoin(ch.ID, "owner"); err != nil {
		t.Fatalf("claude matches the rule: %v", err)
	}
}

func TestJoinRefusedByRuleDoesNotCreateMembership(t *testing.T) {
	s := open(t)
	register(t, s, "owner", "claude", "", "")
	register(t, s, "cdx", "codex", "", "")
	ch, _ := s.CreateChannel("sensitive", "channel", "private", "", "owner")
	s.AddRule(ch.ID, "harness", "claude")

	if err := s.Join(ch.ID, "cdx"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("join should be refused, got %v", err)
	}
	member, err := s.IsMember(ch.ID, "cdx")
	if err != nil {
		t.Fatal(err)
	}
	if member {
		t.Fatal("a refused join must leave no membership behind")
	}
	if err := s.MayRead(ch.ID, "cdx"); !errors.Is(err, ErrForbidden) {
		t.Fatal("and must not grant read")
	}
}

// A model id carries a version suffix that a rule should not have to chase.
func TestModelRuleMatchesByPrefix(t *testing.T) {
	s := open(t)
	register(t, s, "opus", "claude", "", "claude-opus-5-20260101")
	register(t, s, "haiku", "claude", "", "claude-haiku-4-5-20251001")
	ch, _ := s.CreateChannel("bigmodels", "channel", "private", "", "opus")
	s.AddRule(ch.ID, "model", "claude-opus")

	if err := s.MayJoin(ch.ID, "opus"); err != nil {
		t.Fatalf("opus should match the model rule: %v", err)
	}
	if err := s.MayJoin(ch.ID, "haiku"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("haiku should not match, got %v", err)
	}
}

// An agent whose harness never reported a model cannot satisfy a model rule.
// Treating unknown as matching would be the wrong way to fail.
func TestModelRuleRefusesAnAgentWithNoKnownModel(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	ch, _ := s.CreateChannel("bigmodels", "channel", "private", "", "a")
	s.AddRule(ch.ID, "model", "claude-opus")
	if err := s.MayJoin(ch.ID, "a"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an unknown model must not satisfy a model rule, got %v", err)
	}
}

func TestDMIsTheSameChannelFromEitherSide(t *testing.T) {
	s := open(t)
	register(t, s, "aaaa1111", "claude", "", "")
	register(t, s, "bbbb2222", "pi", "", "")

	one, err := s.DMChannel("aaaa1111", "bbbb2222")
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.DMChannel("bbbb2222", "aaaa1111")
	if err != nil {
		t.Fatal(err)
	}
	if one.ID != two.ID {
		t.Fatalf("a DM opened from either side must be one conversation: %d vs %d", one.ID, two.ID)
	}
	if len(two.Members) != 2 {
		t.Fatalf("both participants should be members, got %v", two.Members)
	}
}

// A DM's membership is its definition, so it is not subject to the rules that
// gate joining a channel.
func TestDMIgnoresChannelRules(t *testing.T) {
	s := open(t)
	register(t, s, "aaaa1111", "claude", "", "")
	register(t, s, "bbbb2222", "codex", "", "")
	ch, err := s.DMChannel("aaaa1111", "bbbb2222")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MayRead(ch.ID, "bbbb2222"); err != nil {
		t.Fatalf("both sides of a DM can read it: %v", err)
	}
}

func TestDMIsPrivateToItsTwoParticipants(t *testing.T) {
	s := open(t)
	register(t, s, "aaaa1111", "claude", "", "")
	register(t, s, "bbbb2222", "codex", "", "")
	register(t, s, "cccc3333", "pi", "", "")
	ch, _ := s.DMChannel("aaaa1111", "bbbb2222")
	if err := s.MayRead(ch.ID, "cccc3333"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a third agent must not read someone else's DM, got %v", err)
	}
}

func TestChannelNameIsValidatedAndUnique(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	if _, err := s.CreateChannel("bad name!", "channel", "public", "", "a"); err == nil {
		t.Fatal("a malformed channel name should be refused")
	}
	if _, err := s.CreateChannel("general", "channel", "public", "", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateChannel("general", "channel", "public", "", "a"); err == nil {
		t.Fatal("a duplicate channel name should be refused")
	}
}

func TestChannelTopicRoundTrips(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	ch, _ := s.CreateChannel("general", "channel", "public", "", "a")

	if err := s.SetChannelTopic(ch.ID, "the release"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ChannelByName("general"); got.Topic != "the release" {
		t.Fatalf("got topic %q", got.Topic)
	}
	// Clearing is what an empty topic means, not a refusal.
	if err := s.SetChannelTopic(ch.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ChannelByName("general"); got.Topic != "" {
		t.Fatalf("topic should be cleared, got %q", got.Topic)
	}
	if err := s.SetChannelTopic(9999, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a channel that does not exist should be not found, got %v", err)
	}
}

func TestArchivedChannelTakesNoTopic(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	ch, _ := s.CreateChannel("gone", "channel", "public", "", "a")
	if err := s.ArchiveChannel(ch.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SetChannelTopic(ch.ID, "too late"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an archived channel should be not found, got %v", err)
	}
}

func TestRemoveRuleReopensTheChannel(t *testing.T) {
	s := open(t)
	register(t, s, "owner", "claude", "", "")
	register(t, s, "cdx", "codex", "", "")
	ch, _ := s.CreateChannel("sensitive", "channel", "private", "", "owner")
	if err := s.AddRule(ch.ID, "harness", "claude"); err != nil {
		t.Fatal(err)
	}
	if err := s.MayJoin(ch.ID, "cdx"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("codex matches no rule, got %v", err)
	}

	if err := s.RemoveRule(ch.ID, "harness", "claude"); err != nil {
		t.Fatal(err)
	}
	if rules, _ := s.Rules(ch.ID); len(rules) != 0 {
		t.Fatalf("rule should be gone, got %v", rules)
	}
	// An empty rule set is not a locked channel: MayJoin reads it as
	// "membership is the only gate", which is what the channel was created as.
	if err := s.MayJoin(ch.ID, "cdx"); err != nil {
		t.Fatalf("with no rules left, membership is the only gate: %v", err)
	}
}

func TestRemoveRuleThatIsNotThereIsNotFound(t *testing.T) {
	s := open(t)
	register(t, s, "owner", "claude", "", "")
	ch, _ := s.CreateChannel("sensitive", "channel", "private", "", "owner")
	s.AddRule(ch.ID, "harness", "claude")

	if err := s.RemoveRule(ch.ID, "harness", "codex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("removing a rule that was never added should be not found, got %v", err)
	}
	// And must not take the neighbouring rule with it.
	if rules, _ := s.Rules(ch.ID); len(rules) != 1 {
		t.Fatalf("the existing rule should survive, got %v", rules)
	}
}

func TestUnreadCountsEveryChannelNotJustTheOpenOne(t *testing.T) {
	s := open(t)
	register(t, s, "human", "human", "", "")
	register(t, s, "bot", "claude", "", "")
	general, _ := s.CreateChannel("general", "channel", "public", "", "human")
	backend, _ := s.CreateChannel("backend", "channel", "public", "", "human")
	s.Join(general.ID, "bot")
	s.Join(backend.ID, "bot")

	for _, body := range []string{"one", "two"} {
		if _, err := s.Send(Post{ChannelID: general.ID, From: "bot", Body: body}, DefaultCaps()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Send(Post{ChannelID: backend.ID, From: "bot", Body: "three"}, DefaultCaps()); err != nil {
		t.Fatal(err)
	}
	// Your own messages are not something you have to catch up on.
	if _, err := s.Send(Post{ChannelID: general.ID, From: "human", Body: "mine"}, DefaultCaps()); err != nil {
		t.Fatal(err)
	}

	counts, err := s.UnreadCounts("human")
	if err != nil {
		t.Fatal(err)
	}
	if counts[general.ID] != 2 {
		t.Fatalf("general should have 2 unread, got %d", counts[general.ID])
	}
	if counts[backend.ID] != 1 {
		t.Fatalf("backend should have 1 unread, got %d", counts[backend.ID])
	}
}

func TestUnreadCountsSkipChannelsYouAreNotIn(t *testing.T) {
	s := open(t)
	register(t, s, "human", "human", "", "")
	register(t, s, "bot", "claude", "", "")
	theirs, _ := s.CreateChannel("theirs", "channel", "public", "", "bot")
	if _, err := s.Send(Post{ChannelID: theirs.ID, From: "bot", Body: "not for you"}, DefaultCaps()); err != nil {
		t.Fatal(err)
	}

	counts, _ := s.UnreadCounts("human")
	if n, ok := counts[theirs.ID]; ok {
		t.Fatalf("a channel you are not a member of should be absent, got %d", n)
	}
}

func TestMarkChannelReadClearsTheCount(t *testing.T) {
	s := open(t)
	register(t, s, "human", "human", "", "")
	register(t, s, "bot", "claude", "", "")
	ch, _ := s.CreateChannel("general", "channel", "public", "", "human")
	s.Join(ch.ID, "bot")
	s.Send(Post{ChannelID: ch.ID, From: "bot", Body: "one"}, DefaultCaps())
	s.Send(Post{ChannelID: ch.ID, From: "bot", Body: "two"}, DefaultCaps())

	n, err := s.MarkChannelRead(ch.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("should have marked 2 messages, got %d", n)
	}
	if counts, _ := s.UnreadCounts("human"); counts[ch.ID] != 0 {
		t.Fatalf("nothing should be unread, got %d", counts[ch.ID])
	}

	// Reading twice is not two reads, and the second pass has nothing to do.
	if n, _ := s.MarkChannelRead(ch.ID, "human"); n != 0 {
		t.Fatalf("a second pass should mark nothing, got %d", n)
	}
	s.Send(Post{ChannelID: ch.ID, From: "bot", Body: "three"}, DefaultCaps())
	if counts, _ := s.UnreadCounts("human"); counts[ch.ID] != 1 {
		t.Fatalf("a message that arrived after reading is unread, got %d", counts[ch.ID])
	}
}

func TestMarkChannelReadRefusesAChannelYouCannotRead(t *testing.T) {
	s := open(t)
	register(t, s, "human", "human", "", "")
	register(t, s, "bot", "claude", "", "")
	ch, _ := s.CreateChannel("private", "channel", "private", "", "bot")
	s.Send(Post{ChannelID: ch.ID, From: "bot", Body: "one"}, DefaultCaps())

	if _, err := s.MarkChannelRead(ch.ID, "human"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("should be forbidden, got %v", err)
	}
}

func TestTurnStateRoundTrips(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	if a, _ := s.Agent("a"); a.TurnState != "idle" {
		t.Fatalf("a fresh agent is idle, got %q", a.TurnState)
	}
	if err := s.SetTurnState("a", "working"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Agent("a"); a.TurnState != "working" {
		t.Fatalf("got %q", a.TurnState)
	}
	if err := s.SetTurnState("a", "confused"); err == nil {
		t.Fatal("an unknown turn state should be refused")
	}
}

// ── channel members ─────────────────────────────────────────────────

func TestChannelMembersReturnsOwnersFirst(t *testing.T) {
	s := open(t)
	register(t, s, "owner1", "claude", "", "")
	register(t, s, "memb2", "codex", "", "")
	register(t, s, "memb3", "pi", "", "")
	ch, _ := s.CreateChannel("team", "channel", "public", "", "owner1")
	s.Join(ch.ID, "memb2")
	s.Join(ch.ID, "memb3")

	members, err := s.ChannelMembers(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("got %d members, want 3", len(members))
	}
	if members[0].SessionID != "owner1" || members[0].Role != "owner" {
		t.Fatalf("first member should be owner, got %s (%s)", members[0].SessionID, members[0].Role)
	}
}

func TestChannelMembersHandlesAgentGone(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	ch, _ := s.CreateChannel("team", "channel", "public", "", "a")
	// Add a member that does not have an agent row (simulates deregistered).
	s.db.Exec(`INSERT INTO channel_members (channel_id, session_id) VALUES (?, ?)`,
		ch.ID, "ghost")

	members, err := s.ChannelMembers(ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 2 {
		t.Fatalf("got %d members, want 2", len(members))
	}
	// The ghost agent gets harness='gone' via COALESCE.
	for _, m := range members {
		if m.SessionID == "ghost" {
			if m.Harness != "gone" {
				t.Fatalf("gone agent should show harness='gone', got %q", m.Harness)
			}
		}
	}
}

// ── channel reorder ─────────────────────────────────────────────────

func TestSwapChannelOrderDownThenUp(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	c1, _ := s.CreateChannel("first", "channel", "public", "", "a")
	_, _ = s.CreateChannel("second", "channel", "public", "", "a")
	_, _ = s.CreateChannel("third", "channel", "public", "", "a")

	// Initially all have sort_order 0, ordered by id.
	chs, _ := s.Channels()
	if chs[0].Name != "first" || chs[1].Name != "second" || chs[2].Name != "third" {
		t.Fatalf("initial order: got %v", names(chs))
	}

	// Move first DOWN (swap with second).
	if err := s.SwapChannelOrder(c1.ID, 1); err != nil {
		t.Fatal(err)
	}
	chs, _ = s.Channels()
	if chs[0].Name != "second" || chs[1].Name != "first" || chs[2].Name != "third" {
		t.Fatalf("after moving first down: got %v", names(chs))
	}

	// Move it UP (back).
	if err := s.SwapChannelOrder(c1.ID, -1); err != nil {
		t.Fatal(err)
	}
	chs, _ = s.Channels()
	if chs[0].Name != "first" || chs[1].Name != "second" || chs[2].Name != "third" {
		t.Fatalf("after moving back up: got %v", names(chs))
	}
}

func TestSwapChannelOrderAtEdgeIsNoop(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	c1, _ := s.CreateChannel("top", "channel", "public", "", "a")
	c2, _ := s.CreateChannel("bottom", "channel", "public", "", "a")

	// Move top UP (no prev).
	if err := s.SwapChannelOrder(c1.ID, -1); err != nil {
		t.Fatal(err)
	}
	chs, _ := s.Channels()
	if chs[0].Name != "top" {
		t.Fatalf("top should stay: got %v", names(chs))
	}

	// Move bottom DOWN (no next).
	if err := s.SwapChannelOrder(c2.ID, 1); err != nil {
		t.Fatal(err)
	}
	chs, _ = s.Channels()
	if chs[1].Name != "bottom" {
		t.Fatalf("bottom should stay: got %v", names(chs))
	}
}

func TestSwapChannelOrderPreservesOrderAcrossMixedValues(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	c1, _ := s.CreateChannel("a", "channel", "public", "", "a")
	c2, _ := s.CreateChannel("b", "channel", "public", "", "a")
	// Manually set different sort_orders.
	s.db.Exec(`UPDATE channels SET sort_order = 10 WHERE id = ?`, c1.ID)
	s.db.Exec(`UPDATE channels SET sort_order = 20 WHERE id = ?`, c2.ID)

	// Swap first DOWN.
	if err := s.SwapChannelOrder(c1.ID, 1); err != nil {
		t.Fatal(err)
	}
	chs, _ := s.Channels()
	if chs[0].Name != "b" || chs[1].Name != "a" {
		t.Fatalf("after swapping different orders: got %v", names(chs))
	}
}

// ── migration v3 ────────────────────────────────────────────────────

const v2ChannelsSchema = `
CREATE TABLE channels (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    kind        TEXT NOT NULL DEFAULT 'channel',
    visibility  TEXT NOT NULL DEFAULT 'public',
    topic       TEXT NOT NULL DEFAULT '',
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  INTEGER NOT NULL DEFAULT (unixepoch()),
    archived_at INTEGER
);
CREATE TABLE schema_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
INSERT INTO schema_meta (key, value) VALUES ('schema_version', '2');
`

func TestMigrationAddsSortOrder(t *testing.T) {
	dir := t.TempDir()
	v2, err := sql.Open("sqlite", filepath.Join(dir, "mesh.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Exec(v2ChannelsSchema); err != nil {
		t.Fatal(err)
	}
	if err := v2.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}
	defer s.Close()

	var count int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('channels')
		 WHERE name = 'sort_order'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("sort_order column missing after migration")
	}

	// Create a channel — it should get sort_order=0 and be queryable.
	register(t, s, "a", "claude", "", "")
	ch, err := s.CreateChannel("test", "channel", "public", "", "a")
	if err != nil {
		t.Fatal(err)
	}
	chs, err := s.Channels()
	if err != nil {
		t.Fatal(err)
	}
	if len(chs) != 1 || chs[0].Name != "test" {
		t.Fatalf("channel query after migration: got %v", names(chs))
	}
	_ = ch
}

func names(chs []Channel) []string {
	out := make([]string, len(chs))
	for i, c := range chs {
		out[i] = c.Name
	}
	return out
}
