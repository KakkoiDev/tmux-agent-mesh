package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type Channel struct {
	ID         int64
	Name       string
	Kind       string
	Visibility string
	Topic      string
	CreatedBy  string
	Members    []string
}

func (c Channel) IsPrivate() bool { return c.Visibility == "private" }

// CreateChannel makes a channel and puts its creator in it. A private channel
// with no members but its owner is the useful default for sensitive work: nobody
// else can see it until they are added, and an access rule decides who may be.
func (s *Store) CreateChannel(name, kind, visibility, topic, createdBy string) (Channel, error) {
	if !aliasRe.MatchString(name) {
		return Channel{}, errors.New("channel name must be alphanumeric, dash or underscore")
	}
	if kind != "channel" && kind != "dm" {
		return Channel{}, fmt.Errorf("unknown channel kind %q", kind)
	}
	if visibility != "public" && visibility != "private" {
		return Channel{}, fmt.Errorf("unknown visibility %q", visibility)
	}

	var ch Channel
	err := s.tx(func(tx *sql.Tx) error {
		res, err := tx.Exec(
			`INSERT INTO channels (name, kind, visibility, topic, created_by)
			 VALUES (?, ?, ?, ?, ?)`, name, kind, visibility, topic, createdBy)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if createdBy != "" {
			if _, err := tx.Exec(
				`INSERT INTO channel_members (channel_id, session_id, role)
				 VALUES (?, ?, 'owner')`, id, createdBy); err != nil {
				return err
			}
		}
		ch = Channel{ID: id, Name: name, Kind: kind, Visibility: visibility,
			Topic: topic, CreatedBy: createdBy}
		return nil
	})
	return ch, err
}

func (s *Store) ChannelByName(name string) (Channel, error) {
	var ch Channel
	err := s.db.QueryRow(
		`SELECT id, name, kind, visibility, topic, created_by FROM channels
		  WHERE name = ? AND archived_at IS NULL`, name).Scan(
		&ch.ID, &ch.Name, &ch.Kind, &ch.Visibility, &ch.Topic, &ch.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return ch, fmt.Errorf("%w: channel %q", ErrNotFound, name)
	}
	if err != nil {
		return ch, err
	}
	ch.Members, err = s.Members(ch.ID)
	return ch, err
}

func (s *Store) Members(channelID int64) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT session_id FROM channel_members WHERE channel_id = ? ORDER BY joined_at, session_id`,
		channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// AddRule restricts who may join a private channel, by harness or by model.
func (s *Store) AddRule(channelID int64, subject, value string) error {
	if subject != "harness" && subject != "model" {
		return fmt.Errorf("rule subject must be harness or model, got %q", subject)
	}
	if value == "" {
		return errors.New("rule value is required")
	}
	_, err := s.db.Exec(
		`INSERT INTO channel_rules (channel_id, subject, value) VALUES (?, ?, ?)
		 ON CONFLICT DO NOTHING`, channelID, subject, value)
	return err
}

type Rule struct {
	Subject string
	Value   string
}

func (s *Store) Rules(channelID int64) ([]Rule, error) {
	rows, err := s.db.Query(
		`SELECT subject, value FROM channel_rules WHERE channel_id = ? ORDER BY subject, value`,
		channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := rows.Scan(&r.Subject, &r.Value); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MayJoin decides whether an agent is allowed into a channel.
//
// No rules means membership is the only gate. Once any rule exists the set
// becomes an allow-list and everything unmatched is refused, so a channel whose
// rules match nobody is locked rather than open. Sensitive work has to fail
// closed: the cost of a wrong "no" is an error message, the cost of a wrong
// "yes" is the thing the channel existed to prevent.
func (s *Store) MayJoin(channelID int64, sessionID string) error {
	rules, err := s.Rules(channelID)
	if err != nil {
		return err
	}
	if len(rules) == 0 {
		return nil
	}
	a, err := s.Agent(sessionID)
	if err != nil {
		return err
	}
	for _, r := range rules {
		switch r.Subject {
		case "harness":
			if r.Value == a.Harness {
				return nil
			}
		case "model":
			// Prefix match: a model id carries a version suffix that a rule
			// should not have to chase.
			if a.Model != "" && strings.HasPrefix(a.Model, r.Value) {
				return nil
			}
		}
	}
	return fmt.Errorf("%w: %s (%s/%s) matches no access rule on this channel",
		ErrForbidden, a.Name(), a.Harness, orDash(a.Model))
}

func (s *Store) Join(channelID int64, sessionID string) error {
	if err := s.MayJoin(channelID, sessionID); err != nil {
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO channel_members (channel_id, session_id) VALUES (?, ?)
		 ON CONFLICT DO NOTHING`, channelID, sessionID)
	return err
}

func (s *Store) Leave(channelID int64, sessionID string) error {
	_, err := s.db.Exec(
		`DELETE FROM channel_members WHERE channel_id = ? AND session_id = ?`,
		channelID, sessionID)
	return err
}

func (s *Store) IsMember(channelID int64, sessionID string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM channel_members WHERE channel_id = ? AND session_id = ?`,
		channelID, sessionID).Scan(&n)
	return n > 0, err
}

// MayRead gates every read of a channel's contents: messages, history and file
// bodies all go through it, so there is one place to get this right.
//
// A public channel is readable by any registered participant even without
// joining, which is what makes a public channel useful. A private channel is
// members only, with no exception for the creator once they have left it.
func (s *Store) MayRead(channelID int64, sessionID string) error {
	var visibility string
	err := s.db.QueryRow(`SELECT visibility FROM channels WHERE id = ?`, channelID).
		Scan(&visibility)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: channel %d", ErrNotFound, channelID)
	}
	if err != nil {
		return err
	}
	member, err := s.IsMember(channelID, sessionID)
	if err != nil {
		return err
	}
	if member {
		return nil
	}
	if visibility == "public" {
		if _, err := s.Agent(sessionID); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("%w: %s is not a member of this private channel", ErrForbidden, sessionID)
}

// DMChannel finds or creates the direct-message channel between two sessions.
// The name is derived from the sorted pair so either direction lands on the same
// channel rather than creating two half-conversations.
func (s *Store) DMChannel(a, b string) (Channel, error) {
	pair := []string{a, b}
	sort.Strings(pair)
	name := "dm-" + shortID(pair[0]) + "-" + shortID(pair[1])

	if ch, err := s.ChannelByName(name); err == nil {
		return ch, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Channel{}, err
	}

	ch, err := s.CreateChannel(name, "dm", "private", "", a)
	if err != nil {
		return Channel{}, err
	}
	// Straight insert, not Join: a DM's membership is its definition, so it is
	// not subject to the access rules that gate joining a channel.
	if _, err := s.db.Exec(
		`INSERT INTO channel_members (channel_id, session_id) VALUES (?, ?)
		 ON CONFLICT DO NOTHING`, ch.ID, b); err != nil {
		return Channel{}, err
	}
	ch.Members, err = s.Members(ch.ID)
	return ch, err
}

func (s *Store) Channels() ([]Channel, error) {
	rows, err := s.db.Query(`
		SELECT id, name, kind, visibility, topic, created_by FROM channels
		 WHERE archived_at IS NULL ORDER BY kind, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		var c Channel
		if err := rows.Scan(&c.ID, &c.Name, &c.Kind, &c.Visibility, &c.Topic, &c.CreatedBy); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Members, err = s.Members(out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ArchiveChannel soft-deletes a channel by setting archived_at.
func (s *Store) ArchiveChannel(channelID int64) error {
	res, err := s.db.Exec(
		`UPDATE channels SET archived_at = unixepoch() WHERE id = ? AND archived_at IS NULL`,
		channelID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: channel %d", ErrNotFound, channelID)
	}
	return nil
}

// RenameChannel updates the name of a channel.
func (s *Store) RenameChannel(channelID int64, newName string) error {
	if !aliasRe.MatchString(newName) {
		return errors.New("channel name must be alphanumeric, dash or underscore")
	}
	res, err := s.db.Exec(
		`UPDATE channels SET name = ? WHERE id = ? AND archived_at IS NULL`, newName, channelID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: channel %d", ErrNotFound, channelID)
	}
	return nil
}

// SetChannelVisibility changes a channel's visibility.
func (s *Store) SetChannelVisibility(channelID int64, visibility string) error {
	if visibility != "public" && visibility != "private" {
		return fmt.Errorf("unknown visibility %q", visibility)
	}
	res, err := s.db.Exec(
		`UPDATE channels SET visibility = ? WHERE id = ? AND archived_at IS NULL`, visibility, channelID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: channel %d", ErrNotFound, channelID)
	}
	return nil
}

func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
