package store

import (
	"errors"
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
