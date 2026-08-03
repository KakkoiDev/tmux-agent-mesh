package store

import (
	"errors"
	"testing"
)

func channelWith(t *testing.T, s *Store, name string, members ...string) Channel {
	t.Helper()
	ch, err := s.CreateChannel(name, "channel", "public", "", members[0])
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	for _, m := range members[1:] {
		if err := s.Join(ch.ID, m); err != nil {
			t.Fatalf("join %s: %v", m, err)
		}
	}
	return ch
}

func send(t *testing.T, s *Store, ch Channel, from, body string) Message {
	t.Helper()
	m, err := s.Send(Post{ChannelID: ch.ID, From: from, Body: body}, DefaultCaps())
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	return m
}

func TestEveryMemberButTheSenderIsARecipient(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	register(t, s, "c", "pi", "", "")
	ch := channelWith(t, s, "team", "a", "b", "c")

	send(t, s, ch, "a", "morning")

	for _, id := range []string{"b", "c"} {
		pending, err := s.Pending(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(pending) != 1 {
			t.Fatalf("%s should have one message, got %d", id, len(pending))
		}
	}
	// One message, several recipients, and the sender is not one of them.
	pending, err := s.Pending("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Fatalf("the sender should not receive its own message, got %d", len(pending))
	}
}

func TestClaimIsAtMostOncePerRecipient(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	send(t, s, ch, "a", "once")

	first, err := s.Claim("b", "claude:turn-end")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("expected one message, got %d", len(first))
	}
	second, err := s.Claim("b", "claude:turn-end")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 0 {
		t.Fatalf("a claimed message must not be redelivered, got %d", len(second))
	}
}

// One recipient claiming must not consume another's copy.
func TestClaimIsIndependentPerRecipient(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	register(t, s, "c", "pi", "", "")
	ch := channelWith(t, s, "team", "a", "b", "c")
	send(t, s, ch, "a", "for both of you")

	if _, err := s.Claim("b", "codex:turn-end"); err != nil {
		t.Fatal(err)
	}
	pending, err := s.Pending("c")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("c should still have its copy, got %d", len(pending))
	}
}

func TestClaimRequiresAMechanism(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	// The audit trail is the only record of a message that a client dropped, so
	// an unlabelled delivery is not worth having.
	if _, err := s.Claim("a", ""); err == nil {
		t.Fatal("claim without a mechanism should be refused")
	}
}

func TestSendRefusedForANonMemberOfAPrivateChannel(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch, _ := s.CreateChannel("secrets", "channel", "private", "", "a")

	if _, err := s.Send(Post{ChannelID: ch.ID, From: "b", Body: "let me in"},
		DefaultCaps()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a channel you cannot see is not one you can post into, got %v", err)
	}
}

func TestCapsRefuseRatherThanTruncate(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	register(t, s, "c", "pi", "", "")
	ch := channelWith(t, s, "team", "a", "b", "c")

	caps := DefaultCaps()
	caps.MaxRecipients = 1
	if _, err := s.Send(Post{ChannelID: ch.ID, From: "a", Body: "too wide"}, caps); err == nil {
		t.Fatal("an oversized fan-out should be refused")
	}
	// Refused means nobody got it. A partial delivery reads as full coverage.
	for _, id := range []string{"b", "c"} {
		pending, _ := s.Pending(id)
		if len(pending) != 0 {
			t.Fatalf("%s should have received nothing, got %d", id, len(pending))
		}
	}
}

func TestKillSwitchStopsASend(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	caps := DefaultCaps()
	caps.Enabled = false
	if _, err := s.Send(Post{ChannelID: ch.ID, From: "a", Body: "x"}, caps); err == nil {
		t.Fatal("the kill switch should stop a send")
	}
}

func TestHopCapRefuses(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	caps := DefaultCaps()
	caps.MaxHops = 1
	if _, err := s.Send(Post{ChannelID: ch.ID, From: "a", Body: "x", Hops: 2}, caps); err == nil {
		t.Fatal("the hop cap should refuse")
	}
}

// A name is what an agent can say out loud, so posting into a thread does not
// require having first read the message that opened it.
func TestPostingByThreadNameJoinsTheSameThread(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")

	first, err := s.Send(Post{ChannelID: ch.ID, From: "a", Body: "one", Thread: "auth-bug"}, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Send(Post{ChannelID: ch.ID, From: "b", Body: "two", Thread: "auth-bug"}, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	if first.ThreadID != second.ThreadID {
		t.Fatalf("same name should be the same thread, got %d and %d", first.ThreadID, second.ThreadID)
	}
	if second.ThreadName != "auth-bug" {
		t.Fatalf("thread name should survive the round trip, got %q", second.ThreadName)
	}
}

// The same name in two channels is two conversations. A thread is scoped to the
// channel it lives in, so "auth-bug" in #team and in #ops never merge.
func TestTheSameThreadNameInTwoChannelsIsTwoThreads(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	team := channelWith(t, s, "team", "a", "b")
	ops := channelWith(t, s, "ops", "a", "b")

	one, err := s.Send(Post{ChannelID: team.ID, From: "a", Body: "x", Thread: "auth-bug"}, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	two, err := s.Send(Post{ChannelID: ops.ID, From: "a", Body: "y", Thread: "auth-bug"}, DefaultCaps())
	if err != nil {
		t.Fatal(err)
	}
	if one.ThreadID == two.ThreadID {
		t.Fatalf("threads should not cross channels, both are %d", one.ThreadID)
	}
}

// Every message belongs to exactly one thread, so a reply never starts a second
// conversation and history always has something to group by.
func TestATopLevelPostOpensItsOwnThread(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	m := send(t, s, ch, "a", "hello")
	if m.ThreadID == 0 || m.ThreadName == "" {
		t.Fatalf("message should carry a thread, got id %d name %q", m.ThreadID, m.ThreadName)
	}
	n := send(t, s, ch, "a", "unrelated")
	if n.ThreadID == m.ThreadID {
		t.Fatalf("two unnamed posts should not share a thread, both are %d", m.ThreadID)
	}
}

// ── read receipts ────────────────────────────────────────────────────

// For an agent, read is exactly the drain: the moment the text entered its
// context. There is nothing further to wait for.
func TestClaimingRecordsAReadReceipt(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "reviewer", "")
	ch := channelWith(t, s, "team", "a", "b")
	m := send(t, s, ch, "a", "did you see this")

	if _, err := s.Claim("b", "codex:turn-end"); err != nil {
		t.Fatal(err)
	}
	receipts, err := s.Receipts(m.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 {
		t.Fatalf("expected one receipt, got %d", len(receipts))
	}
	r := receipts[0]
	if r.Reader != "b" || r.Name != "reviewer" || r.Source != "drain" {
		t.Fatalf("receipt should name the reader and how: %+v", r)
	}
	if r.At == 0 {
		t.Fatal("a receipt without a time is not a receipt")
	}
}

// "Read three times" is a thing the app has to be able to say, so receipts are
// an append-only log and never a flag.
func TestRepeatReadsAreSeparateReceipts(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	m := send(t, s, ch, "a", "again")

	if _, err := s.Claim("b", "codex:turn-end"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.MarkRead(m.ID, "b"); err != nil {
			t.Fatal(err)
		}
	}
	receipts, err := s.Receipts(m.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 3 {
		t.Fatalf("one drain plus two client reads is three receipts, got %d", len(receipts))
	}
}

func TestTheHumanLeavesAReceiptToo(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	ch := channelWith(t, s, "team", "a", HumanID)
	m := send(t, s, ch, "a", "for you")

	if err := s.MarkRead(m.ID, HumanID); err != nil {
		t.Fatal(err)
	}
	receipts, err := s.Receipts(m.ID, "a")
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].Reader != HumanID || receipts[0].Source != "client" {
		t.Fatalf("the human read should be recorded as a client read: %+v", receipts)
	}
}

// Otherwise the receipt list becomes a way to prove a private message exists.
func TestAnOutsiderCannotLeaveOrSeeReceipts(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	register(t, s, "c", "pi", "", "")
	ch, _ := s.CreateChannel("secrets", "channel", "private", "", "a")
	if err := s.Join(ch.ID, "b"); err != nil {
		t.Fatal(err)
	}
	m := send(t, s, ch, "a", "sensitive")

	if err := s.MarkRead(m.ID, "c"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an outsider must not leave a receipt, got %v", err)
	}
	if _, err := s.Receipts(m.ID, "c"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("an outsider must not read the receipts, got %v", err)
	}
}

// ── history ──────────────────────────────────────────────────────────

// Opening a channel in the app must not consume an agent's mail.
func TestHistoryDoesNotDeliver(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	send(t, s, ch, "a", "still waiting")

	if _, err := s.History(ch.ID, "b", 50); err != nil {
		t.Fatal(err)
	}
	pending, _ := s.Pending("b")
	if len(pending) != 1 {
		t.Fatalf("history must not consume mail, got %d pending", len(pending))
	}
}

func TestHistoryOfAPrivateChannelIsMembersOnly(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch, _ := s.CreateChannel("secrets", "channel", "private", "", "a")
	send(t, s, ch, "a", "sensitive")

	if _, err := s.History(ch.ID, "b", 50); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a non-member must not read the history, got %v", err)
	}
}

func TestHistoryReadsOldestFirst(t *testing.T) {
	s := open(t)
	register(t, s, "a", "claude", "", "")
	register(t, s, "b", "codex", "", "")
	ch := channelWith(t, s, "team", "a", "b")
	send(t, s, ch, "a", "first")
	send(t, s, ch, "a", "second")

	msgs, err := s.History(ch.ID, "b", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Body != "first" {
		t.Fatalf("history should read oldest first, got %+v", msgs)
	}
}
