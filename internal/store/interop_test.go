package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Both halves of this project open the same mesh.db. They used to declare
// `messages` incompatibly against it, so whichever process created the file won
// and the other's inserts failed against a table it did not expect. These tests
// are what make that regression loud: they open one database with both.

func meshScript(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/store -> repo root.
	path := filepath.Join(wd, "..", "..", "scripts", "mesh.sh")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("mesh.sh not found: %v", err)
	}
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not on PATH")
	}
	return path
}

// runMesh invokes the bash CLI against dir, with the environment that keeps it
// off the developer's live tmux options and out of their real mailbox.
func runMesh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(meshScript(t), args...)
	cmd.Env = append(os.Environ(),
		"MESH_DIR="+dir,
		"MESH_DB="+filepath.Join(dir, "mesh.db"),
		"MESH_NOTIFY_DIR="+filepath.Join(dir, "notify"),
		"MESH_DELIVERY_LOG="+filepath.Join(dir, "delivery.log"),
		"TMUX_PANE=",
		"ENABLED=on",
		"DELIVERY=off",
		"MAX_HOPS=4",
		"MAX_BROADCAST=8",
		"ICON_MAIL=@",
		"DEBUG_LOG=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("mesh.sh %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func TestGoReadsADatabaseBashCreated(t *testing.T) {
	dir := t.TempDir()
	runMesh(t, dir, "init")
	runMesh(t, dir, "register", "--session", "a", "--harness", "claude", "--pane", "")
	runMesh(t, dir, "register", "--session", "b", "--harness", "codex", "--pane", "")
	runMesh(t, dir, "send", "--from", "a", "--to", "b", "--message", "from bash")

	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open a bash-created database: %v", err)
	}
	defer s.Close()

	pending, err := s.Pending("b")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Body != "from bash" {
		t.Fatalf("expected bash's message, got %+v", pending)
	}
	if pending[0].UID == "" {
		t.Fatal("bash wrote a message with no content address")
	}

	// And write into it, which is the direction that used to fail outright.
	if _, err := s.Send(Post{ChannelID: pending[0].ChannelID, From: "b", Body: "from go"},
		DefaultCaps()); err != nil {
		t.Fatalf("write into a bash-created database: %v", err)
	}
	out := runMesh(t, dir, "inbox", "--as", "a")
	if !strings.Contains(out, "from go") {
		t.Fatalf("bash cannot see the message Go wrote:\n%s", out)
	}
}

func TestBashReadsADatabaseGoCreated(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	register(t, s, "a", "claude", "alice", "")
	register(t, s, "b", "codex", "bob", "")
	ch, err := s.DMChannel("a", "b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(Post{ChannelID: ch.ID, From: "a", Body: "from go first"},
		DefaultCaps()); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// init on an existing database has to be non-destructive, so the message is
	// still there afterwards; that is also what makes this a real check.
	runMesh(t, dir, "init")
	out := runMesh(t, dir, "inbox", "--as", "b")
	if !strings.Contains(out, "from go first") {
		t.Fatalf("bash cannot read the message Go wrote:\n%s", out)
	}
	runMesh(t, dir, "send", "--from", "b", "--to", "alice", "--message", "from bash second")

	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	pending, err := s2.Pending("a")
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Body != "from bash second" {
		t.Fatalf("Go cannot see bash's reply, got %+v", pending)
	}
}

// The two sides have to compute the same content address, or the same message
// has two identities and `import` deduplicates nothing. Checked by recomputing
// in Go the uid bash actually wrote, rather than by asserting a constant: a
// constant would have to be updated whenever either side changed, which is how
// two implementations drift apart in the first place.
func TestBashAndGoAgreeOnTheContentAddress(t *testing.T) {
	dir := t.TempDir()
	runMesh(t, dir, "init")
	runMesh(t, dir, "register", "--session", "a", "--harness", "claude", "--pane", "")
	runMesh(t, dir, "register", "--session", "b", "--harness", "codex", "--pane", "")
	runMesh(t, dir, "send", "--from", "a", "--to", "b",
		"--message", "hash me\nover two lines", "--thread", "t1")

	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	var uid, nonce, channel, thread, from, body string
	var created int64
	if err := s.db.QueryRow(`
		SELECT m.uid, m.nonce, c.name, t.name, m.from_session, m.body, m.created_at
		  FROM messages m
		  JOIN channels c ON c.id = m.channel_id
		  JOIN threads  t ON t.id = m.thread_id
		 ORDER BY m.id LIMIT 1`).
		Scan(&uid, &nonce, &channel, &thread, &from, &body, &created); err != nil {
		t.Fatal(err)
	}
	if want := MessageUID(nonce, channel, thread, from, body, created, ""); want != uid {
		t.Fatalf("bash and Go disagree on the uid:\n bash %s\n   go %s", uid, want)
	}
}
