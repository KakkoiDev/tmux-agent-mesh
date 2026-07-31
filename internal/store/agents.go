package store

import (
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Agent struct {
	SessionID   string
	Harness     string
	Alias       string
	Model       string
	Host        string
	TmuxPane    string
	TmuxTarget  string
	Cwd         string
	Project     string
	PushCapable bool
	BlockStreak int
	TurnState   string
	Pending     int
}

// Name is what a person should see: the alias if it has one, otherwise enough of
// the session id to tell two agents apart.
func (a Agent) Name() string {
	if a.Alias != "" {
		return a.Alias
	}
	if len(a.SessionID) > 8 {
		return a.SessionID[:8]
	}
	return a.SessionID
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
func (s *Store) Register(a Agent) error {
	if a.SessionID == "" {
		return errors.New("register: session id is required")
	}
	if !knownHarness[a.Harness] {
		return fmt.Errorf("register: unknown harness %q", a.Harness)
	}
	return s.tx(func(tx *sql.Tx) error {
		if a.TmuxPane != "" {
			if _, err := tx.Exec(
				`DELETE FROM agents WHERE tmux_pane = ? AND host = ? AND session_id <> ?`,
				a.TmuxPane, a.Host, a.SessionID); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`
			INSERT INTO agents (session_id, harness, model, host, tmux_pane, tmux_target,
			                    cwd, project_name, push_capable, last_seen)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, unixepoch())
			ON CONFLICT(session_id) DO UPDATE SET
				harness      = excluded.harness,
				model        = COALESCE(NULLIF(excluded.model, ''), agents.model),
				host         = excluded.host,
				tmux_pane    = CASE WHEN excluded.tmux_pane <> '' THEN excluded.tmux_pane ELSE agents.tmux_pane END,
				tmux_target  = CASE WHEN excluded.tmux_target <> '' THEN excluded.tmux_target ELSE agents.tmux_target END,
				cwd          = excluded.cwd,
				project_name = excluded.project_name,
				push_capable = excluded.push_capable,
				last_seen    = unixepoch()`,
			a.SessionID, a.Harness, a.Model, a.Host, a.TmuxPane, a.TmuxTarget,
			a.Cwd, a.Project, boolInt(pushCapable(a.Harness)))
		return err
	})
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
		       cwd, project_name, push_capable, block_streak, turn_state
		  FROM agents WHERE session_id = ?`, sessionID).Scan(
		&a.SessionID, &a.Harness, &alias, &model, &a.Host, &a.TmuxPane,
		&a.TmuxTarget, &a.Cwd, &a.Project, &a.PushCapable, &a.BlockStreak, &a.TurnState)
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
