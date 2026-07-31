package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Agent struct {
	SessionID      string
	Harness        string
	Alias          string
	Model          string
	Host           string
	TmuxPane       string
	TmuxTarget     string
	Cwd            string
	Project        string
	PushCapable    bool
	BlockStreak    int
	TurnState      string
	TranscriptPath string
	LastSeen       int64
	Pending        int
}

// Name is what a person should see: the alias if it has one, otherwise enough of
// the session id to tell two agents apart.
func (a Agent) Name() string {
	if a.Alias != "" {
		return a.Alias
	}
	return shortID(a.SessionID)
}

// Only pi can be reached while it is idle without typing into its pane, because
// its extension stays resident and can start a turn on its own.
func pushCapable(harness string) bool { return harness == "pi" }

var knownHarness = map[string]bool{
	"claude": true, "codex": true, "gemini": true, "pi": true, "human": true,
}

var aliasRe = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// Register upserts an agent. A pane hosts at most one agent, so registering on a
// known pane evicts the stale row: otherwise addressing by pane resolves to an
// agent that is no longer there.
//
// An agent without an alias gets one: <harness>-<first 8 of the session id>,
// so a roster is readable the moment agents show up. The alias is UNIQUE, so a
// generated name that is already taken gets a counter suffix rather than
// failing the registration; re-registering never replaces a name the agent
// chose for itself.
func (s *Store) Register(a Agent) error {
	if a.SessionID == "" {
		return errors.New("register: session id is required")
	}
	if !knownHarness[a.Harness] {
		return fmt.Errorf("register: unknown harness %q", a.Harness)
	}
	explicit := a.Alias != ""
	if explicit && !aliasRe.MatchString(a.Alias) {
		return errors.New("alias must be alphanumeric, dash or underscore")
	}
	// The human row is seeded by name and keeps it.
	auto := !explicit && a.Harness != "human" && a.SessionID != HumanID
	base := ""
	if auto {
		base = fmt.Sprintf("%s-%s", a.Harness, shortID(a.SessionID))
		a.Alias = base
	}
	return s.tx(func(tx *sql.Tx) error {
		if a.TmuxPane != "" {
			if _, err := tx.Exec(
				`DELETE FROM agents WHERE tmux_pane = ? AND host = ? AND session_id <> ?`,
				a.TmuxPane, a.Host, a.SessionID); err != nil {
				return err
			}
		}
		// INSERT OR IGNORE style: a UNIQUE alias collision costs the generated
		// name, not the registration, so retry with a counter suffix. The loop
		// terminates because every candidate is a fresh alias.
		alias := a.Alias
		for n := 2; ; n++ {
			err := upsertAgent(tx, a, alias, explicit)
			if err == nil {
				return nil
			}
			if !auto || !strings.Contains(err.Error(), "UNIQUE") {
				return err
			}
			alias = fmt.Sprintf("%s-%d", base, n)
		}
	})
}

// upsertAgent writes one agent row. The alias lands only when the caller
// explicitly passed one, or when the row has none yet: a session that
// re-registers without a name keeps the alias it already has.
func upsertAgent(tx *sql.Tx, a Agent, alias string, explicit bool) error {
	_, err := tx.Exec(`
		INSERT INTO agents (session_id, harness, alias, model, host, tmux_pane, tmux_target,
		                    cwd, project_name, push_capable, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, unixepoch())
		ON CONFLICT(session_id) DO UPDATE SET
			harness      = excluded.harness,
			alias        = CASE
			                 WHEN excluded.alias <> '' AND (? = 1 OR agents.alias IS NULL) THEN excluded.alias
			                 ELSE agents.alias END,
			model        = COALESCE(NULLIF(excluded.model, ''), agents.model),
			host         = excluded.host,
			tmux_pane    = CASE WHEN excluded.tmux_pane <> '' THEN excluded.tmux_pane ELSE agents.tmux_pane END,
			tmux_target  = CASE WHEN excluded.tmux_target <> '' THEN excluded.tmux_target ELSE agents.tmux_target END,
			cwd          = excluded.cwd,
			project_name = excluded.project_name,
			push_capable = excluded.push_capable,
			last_seen    = unixepoch()`,
		a.SessionID, a.Harness, alias, a.Model, a.Host, a.TmuxPane, a.TmuxTarget,
		a.Cwd, a.Project, boolInt(pushCapable(a.Harness)), boolInt(explicit))
	return err
}

func (s *Store) SetAlias(sessionID, alias string) error {
	if alias == HumanID {
		return fmt.Errorf("%q is reserved", HumanID)
	}
	if !aliasRe.MatchString(alias) {
		return errors.New("alias must be alphanumeric, dash or underscore")
	}
	var holder string
	err := s.db.QueryRow(`SELECT session_id FROM agents WHERE alias = ?`, alias).Scan(&holder)
	switch {
	case err == nil && holder != sessionID:
		return fmt.Errorf("alias %q is already held by %s", alias, holder)
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	}
	res, err := s.db.Exec(
		`UPDATE agents SET alias = ?, last_seen = unixepoch() WHERE session_id = ?`,
		alias, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: agent %s", ErrNotFound, sessionID)
	}
	return nil
}

// SetTranscript records the full path of the agent's conversation transcript,
// reported by the agent itself so another agent or the human can open the
// conversation later. Recording a path counts as activity.
func (s *Store) SetTranscript(sessionID, path string) error {
	if path == "" {
		return errors.New("set-transcript: a path is required")
	}
	res, err := s.db.Exec(
		`UPDATE agents SET transcript_path = ?, last_seen = unixepoch() WHERE session_id = ?`,
		path, sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: agent %s", ErrNotFound, sessionID)
	}
	return nil
}

// Transcript returns the stored transcript path for an agent, "" when the
// agent has not reported one.
func (s *Store) Transcript(sessionID string) (string, error) {
	var path string
	err := s.db.QueryRow(
		`SELECT transcript_path FROM agents WHERE session_id = ?`, sessionID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("%w: agent %s", ErrNotFound, sessionID)
	}
	return path, err
}

func (s *Store) SetTurnState(sessionID, state string) error {
	if state != "idle" && state != "working" {
		return fmt.Errorf("unknown turn state %q", state)
	}
	_, err := s.db.Exec(
		`UPDATE agents SET turn_state = ?, last_seen = unixepoch() WHERE session_id = ?`,
		state, sessionID)
	return err
}

func (s *Store) Deregister(sessionID string) error {
	if sessionID == HumanID {
		return errors.New("refusing to remove the human participant")
	}
	_, err := s.db.Exec(`DELETE FROM agents WHERE session_id = ?`, sessionID)
	return err
}

// Resolve turns a reference into a session id. Order: alias, exact session id,
// pane, tmux target, then an unambiguous session-id prefix.
//
// Ambiguity is an error, never a guess, and prefix matching uses substr rather
// than LIKE because _ and % are LIKE wildcards: "abc_" would otherwise also match
// "abcXdef" and mail would silently go to the wrong agent.
func (s *Store) Resolve(ref string) (string, error) {
	if ref == "" {
		return "", fmt.Errorf("%w: empty reference", ErrNotFound)
	}
	for _, q := range []string{
		`SELECT session_id FROM agents WHERE alias = ? LIMIT 1`,
		`SELECT session_id FROM agents WHERE session_id = ? LIMIT 1`,
		`SELECT session_id FROM agents WHERE tmux_pane = ? AND tmux_pane <> '' LIMIT 1`,
		`SELECT session_id FROM agents WHERE tmux_target = ? AND tmux_target <> '' LIMIT 1`,
	} {
		var id string
		if err := s.db.QueryRow(q, ref).Scan(&id); err == nil {
			return id, nil
		} else if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
	}

	rows, err := s.db.Query(
		`SELECT session_id FROM agents WHERE substr(session_id, 1, length(?)) = ?`, ref, ref)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		found = append(found, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", fmt.Errorf("%w: no agent matches %q", ErrNotFound, ref)
	default:
		return "", fmt.Errorf("%w: %q matches %d agents: %s",
			ErrAmbiguous, ref, len(found), strings.Join(found, ", "))
	}
}

func (s *Store) Agent(sessionID string) (Agent, error) {
	var a Agent
	var alias, model sql.NullString
	err := s.db.QueryRow(`
		SELECT session_id, harness, alias, model, host, tmux_pane, tmux_target,
		       cwd, project_name, push_capable, block_streak, turn_state,
		       transcript_path, last_seen
		  FROM agents WHERE session_id = ?`, sessionID).Scan(
		&a.SessionID, &a.Harness, &alias, &model, &a.Host, &a.TmuxPane,
		&a.TmuxTarget, &a.Cwd, &a.Project, &a.PushCapable, &a.BlockStreak,
		&a.TurnState, &a.TranscriptPath, &a.LastSeen)
	if errors.Is(err, sql.ErrNoRows) {
		return a, fmt.Errorf("%w: agent %s", ErrNotFound, sessionID)
	}
	a.Alias, a.Model = alias.String, model.String
	return a, err
}

// Roster lists every participant with the count of mail waiting for it.
func (s *Store) Roster() ([]Agent, error) {
	rows, err := s.db.Query(`
		SELECT a.session_id, a.harness, a.alias, a.model, a.host, a.tmux_target,
		       a.project_name, a.push_capable, a.turn_state,
		       (SELECT COUNT(*) FROM messages m
		         JOIN channel_members cm ON cm.channel_id = m.channel_id
		        WHERE cm.session_id = a.session_id
		          AND m.from_session <> a.session_id
		          AND NOT EXISTS (SELECT 1 FROM deliveries d
		                           WHERE d.message_id = m.id AND d.session_id = a.session_id))
		  FROM agents a
		 ORDER BY (a.harness = 'human') DESC, a.alias IS NULL, a.alias, a.registered_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Agent
	for rows.Next() {
		var a Agent
		var alias, model sql.NullString
		if err := rows.Scan(&a.SessionID, &a.Harness, &alias, &model, &a.Host,
			&a.TmuxTarget, &a.Project, &a.PushCapable, &a.TurnState, &a.Pending); err != nil {
			return nil, err
		}
		a.Alias, a.Model = alias.String, model.String
		out = append(out, a)
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
