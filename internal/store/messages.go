package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Replaced by tests that need a fixed clock. The row's created_at is written
// explicitly rather than left to the column default because it is one of the
// fields the uid hashes, and the two have to agree.
var nowFn = func() int64 { return time.Now().Unix() }

// Every read of a message goes through the same projection, so a column added
// here reaches Pending, Claim and History at once rather than two of the three.
const messageColumns = `
		SELECT m.id, COALESCE(m.uid, ''), m.channel_id, c.name, m.thread_id, t.name,
		       m.from_session, COALESCE(a.alias, substr(m.from_session, 1, 8)),
		       m.body, m.hops, m.expect_reply, COALESCE(m.reply_to_id, 0), m.created_at
		  FROM messages m
		  JOIN channels c ON c.id = m.channel_id
		  JOIN threads  t ON t.id = m.thread_id
		  LEFT JOIN agents a ON a.session_id = m.from_session`

// Caps are the brakes on a runaway conversation. They are enforced here, in the
// one place every client has to go through, so no harness and no transport can
// skip them.
//
// There is deliberately no per-thread message cap. It counted human posts as
// well as automated ones, so it punished exactly the long-lived topic whose
// accumulated decisions are the most valuable thing the mailbox holds. MaxHops
// bounds a reply chain and the caller's continuation budget bounds forced turns;
// both count automated exchanges, which is the thing that runs away.
type Caps struct {
	Enabled       bool
	MaxHops       int
	MaxRecipients int
}

func DefaultCaps() Caps {
	return Caps{Enabled: true, MaxHops: 4, MaxRecipients: 8}
}

type Message struct {
	ID          int64
	UID         string
	ChannelID   int64
	ChannelName string
	ThreadID    int64
	ThreadName  string
	From        string
	FromName    string
	Body        string
	Hops        int
	ExpectReply bool
	ReplyToID   int64
	CreatedAt   int64
}

type Post struct {
	ChannelID   int64
	From        string
	Body        string
	Hops        int
	ExpectReply bool
	ReplyToID   int64
	// Thread names the thread within the channel. Empty opens a new one. A name
	// can be chosen before the thread exists, which is what lets one agent tell
	// another where to put its findings.
	Thread string
}

// Send posts a message to a channel. The sender must be allowed to read the
// channel: a channel you cannot see is not one you can post into.
//
// Refusals are errors rather than silent truncations. An oversized fan-out
// reaches nobody, because a partial delivery reads as full coverage.
func (s *Store) Send(p Post, caps Caps) (Message, error) {
	if !caps.Enabled {
		return Message{}, errors.New("mesh is disabled")
	}
	if p.Body == "" {
		return Message{}, errors.New("message body is required")
	}
	if p.Hops > caps.MaxHops {
		return Message{}, fmt.Errorf("hop limit reached (%d)", caps.MaxHops)
	}
	if err := s.MayRead(p.ChannelID, p.From); err != nil {
		return Message{}, err
	}

	members, err := s.Members(p.ChannelID)
	if err != nil {
		return Message{}, err
	}
	recipients := 0
	for _, m := range members {
		if m != p.From {
			recipients++
		}
	}
	if recipients > caps.MaxRecipients {
		return Message{}, fmt.Errorf(
			"%d recipients exceeds the fan-out cap (%d); narrow the channel",
			recipients, caps.MaxRecipients)
	}

	var msg Message
	err = s.tx(func(tx *sql.Tx) error {
		var channelName string
		if err := tx.QueryRow(`SELECT name FROM channels WHERE id = ?`, p.ChannelID).
			Scan(&channelName); err != nil {
			return err
		}

		threadName := p.Thread
		if threadName == "" {
			threadName = NewNonce()
		}
		if _, err := tx.Exec(`
			INSERT INTO threads (channel_id, name, opener_session)
			VALUES (?, ?, ?)
			ON CONFLICT(channel_id, name) DO NOTHING`,
			p.ChannelID, threadName, p.From); err != nil {
			return err
		}
		var threadID int64
		if err := tx.QueryRow(
			`SELECT id FROM threads WHERE channel_id = ? AND name = ?`,
			p.ChannelID, threadName).Scan(&threadID); err != nil {
			return err
		}

		// The parent's uid, not its row id: a reply has to keep pointing at the
		// same message once either row leaves this database.
		var replyToUID string
		if p.ReplyToID != 0 {
			var u sql.NullString
			if err := tx.QueryRow(`SELECT uid FROM messages WHERE id = ?`, p.ReplyToID).
				Scan(&u); err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			replyToUID = u.String
		}

		nonce := NewNonce()
		createdAt := nowFn()
		uid := MessageUID(nonce, channelName, threadName, p.From, p.Body, createdAt, replyToUID)

		res, err := tx.Exec(`
			INSERT INTO messages (uid, nonce, channel_id, thread_id, from_session, body,
			                      hops, expect_reply, reply_to_id, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			uid, nonce, p.ChannelID, threadID, p.From, p.Body, p.Hops,
			boolInt(p.ExpectReply), nullInt(p.ReplyToID), createdAt)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}

		msg = Message{ID: id, UID: uid, ChannelID: p.ChannelID, ChannelName: channelName,
			ThreadID: threadID, ThreadName: threadName, From: p.From, Body: p.Body,
			Hops: p.Hops, ExpectReply: p.ExpectReply, ReplyToID: p.ReplyToID,
			CreatedAt: createdAt}
		// Sending is activity: the roster's liveness column has to move even for
		// an agent that only talks and never turns.
		if _, err := tx.Exec(
			`UPDATE agents SET last_seen = unixepoch() WHERE session_id = ?`, p.From); err != nil {
			return err
		}
		return nil
	})
	return msg, err
}

// Pending is the mail a session has not been given yet: posted to a channel it
// belongs to, by somebody else, with no delivery row.
func (s *Store) Pending(sessionID string) ([]Message, error) {
	return s.pendingQuery(sessionID, 0)
}

func (s *Store) pendingQuery(sessionID string, limit int) ([]Message, error) {
	q := messageColumns + `
		  JOIN channel_members cm ON cm.channel_id = m.channel_id AND cm.session_id = ?
		 WHERE m.from_session <> ?
		   AND NOT EXISTS (SELECT 1 FROM deliveries d
		                    WHERE d.message_id = m.id AND d.session_id = ?)
		 ORDER BY m.id`
	args := []any{sessionID, sessionID, sessionID}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// Claim takes a session's pending mail and stamps it delivered, in one
// transaction, so two concurrent drains cannot both take the same message.
//
// Delivery is at-most-once: the row is written before the caller has the text, so
// a client that drops the payload loses the message rather than looping on it. An
// acknowledgement would need a signal no harness provides, and a redelivery loop
// is worse than an audit trail.
//
// Claiming is also reading: for an agent, the moment the text enters its context
// is the moment it was read, so a read receipt is recorded here rather than
// waiting for a client to report one.
func (s *Store) Claim(sessionID, via string) ([]Message, error) {
	if via == "" {
		return nil, errors.New("claim: a delivery mechanism is required")
	}
	var out []Message
	err := s.tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(messageColumns+`
			  JOIN channel_members cm ON cm.channel_id = m.channel_id AND cm.session_id = ?
			 WHERE m.from_session <> ?
			   AND NOT EXISTS (SELECT 1 FROM deliveries d
			                    WHERE d.message_id = m.id AND d.session_id = ?)
			 ORDER BY m.id`, sessionID, sessionID, sessionID)
		if err != nil {
			return err
		}
		claimed, err := scanMessages(rows)
		rows.Close()
		if err != nil {
			return err
		}
		for _, m := range claimed {
			if _, err := tx.Exec(
				`INSERT INTO deliveries (message_id, session_id, delivered_via)
				 VALUES (?, ?, ?)`, m.ID, sessionID, via); err != nil {
				return err
			}
			if _, err := tx.Exec(
				`INSERT INTO reads (message_id, reader, source) VALUES (?, ?, 'drain')`,
				m.ID, sessionID); err != nil {
				return err
			}
		}
		// Claiming mail is the strongest liveness signal a harness gives: the
		// agent's context consumed the text just now.
		if _, err := tx.Exec(
			`UPDATE agents SET last_seen = unixepoch() WHERE session_id = ?`, sessionID); err != nil {
			return err
		}
		out = claimed
		return nil
	})
	return out, err
}

// History is what a client shows. It does not deliver anything, so opening a
// channel in the app never consumes an agent's mail.
func (s *Store) History(channelID int64, viewer string, limit int) ([]Message, error) {
	if err := s.MayRead(channelID, viewer); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.db.Query(messageColumns+`
		 WHERE m.channel_id = ?
		 ORDER BY m.id DESC LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	msgs, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	// Newest-first is how it is fetched, oldest-first is how it reads.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

// Receipt is one act of reading. Repeat reads by the same reader are separate
// receipts, because "read three times" is a thing the app has to be able to say.
type Receipt struct {
	MessageID int64
	Reader    string
	Name      string
	Harness   string
	At        int64
	Source    string
}

// MarkRead records a read by a client, which is the only way a human read
// becomes observable. Append-only on purpose: nothing here overwrites an
// earlier receipt.
func (s *Store) MarkRead(messageID int64, reader string) error {
	var channelID int64
	err := s.db.QueryRow(`SELECT channel_id FROM messages WHERE id = ?`, messageID).
		Scan(&channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: message %d", ErrNotFound, messageID)
	}
	if err != nil {
		return err
	}
	// A reader who may not see the channel may not leave a receipt on it either,
	// or the receipt list becomes a way to prove a private message exists.
	if err := s.MayRead(channelID, reader); err != nil {
		return err
	}
	_, err = s.db.Exec(
		`INSERT INTO reads (message_id, reader, source) VALUES (?, ?, 'client')`,
		messageID, reader)
	return err
}

func (s *Store) Receipts(messageID int64, viewer string) ([]Receipt, error) {
	var channelID int64
	err := s.db.QueryRow(`SELECT channel_id FROM messages WHERE id = ?`, messageID).
		Scan(&channelID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: message %d", ErrNotFound, messageID)
	}
	if err != nil {
		return nil, err
	}
	if err := s.MayRead(channelID, viewer); err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`
		SELECT r.message_id, r.reader,
		       COALESCE(a.alias, substr(r.reader, 1, 8)),
		       COALESCE(a.harness, 'gone'), r.read_at, r.source
		  FROM reads r
		  LEFT JOIN agents a ON a.session_id = r.reader
		 WHERE r.message_id = ?
		 ORDER BY r.read_at, r.id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Receipt
	for rows.Next() {
		var r Receipt
		if err := rows.Scan(&r.MessageID, &r.Reader, &r.Name, &r.Harness, &r.At, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) BlockStreak(sessionID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT block_streak FROM agents WHERE session_id = ?`, sessionID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

func (s *Store) BumpStreak(sessionID string) error {
	_, err := s.db.Exec(
		`UPDATE agents SET block_streak = block_streak + 1 WHERE session_id = ?`, sessionID)
	return err
}

func (s *Store) ResetStreak(sessionID string) error {
	_, err := s.db.Exec(`UPDATE agents SET block_streak = 0 WHERE session_id = ?`, sessionID)
	return err
}

func scanMessages(rows *sql.Rows) ([]Message, error) {
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.UID, &m.ChannelID, &m.ChannelName, &m.ThreadID,
			&m.ThreadName, &m.From, &m.FromName, &m.Body, &m.Hops, &m.ExpectReply,
			&m.ReplyToID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func nullInt(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
