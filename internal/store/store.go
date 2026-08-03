// Package store owns the mailbox database. Nothing else opens it.
//
// On a remote deployment this package runs only on the server, reached over ssh,
// which is what makes the access rules real: an agent that cannot open the file
// cannot bypass a check written here.
package store

import (
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

//go:embed migrate_v1_pre.sql
var migrateV1PreSQL string

//go:embed migrate_v1_post.sql
var migrateV1PostSQL string

// HumanID is the reserved session for the person. Seeded once and never removed:
// an agent addressing the human is a better primitive than an agent ending its
// turn to ask a question.
const HumanID = "human"

var (
	ErrNotFound  = errors.New("not found")
	ErrAmbiguous = errors.New("ambiguous")
	ErrForbidden = errors.New("forbidden")
)

type Store struct {
	db  *sql.DB
	dir string
}

// Open creates the data directory, the database and the private file store.
//
// The file store is 0700 on purpose. It is the only reason an access rule on a
// channel means anything: the bytes are unreachable except through this process,
// so "ask the service" is not a convention that a determined agent can skip.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("data dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0o700); err != nil {
		return nil, fmt.Errorf("file store: %w", err)
	}

	path := filepath.Join(dir, "mesh.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	// One writer at a time. SQLite serialises writes anyway, and a single
	// connection removes any chance of two pooled connections deadlocking on the
	// write lock under a burst of hook invocations.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, dir: dir}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// Dir is the data directory. FileStore is deliberately not exported as a path
// helper for clients: only this package should be resolving it.
func (s *Store) Dir() string { return s.dir }

func (s *Store) fileStore() string { return filepath.Join(s.dir, "files") }

// SchemaVersion is the shape this build expects. It is stamped into
// schema_meta at the end of a successful migration, and scripts/mesh.sh carries
// the same number in the database's PRAGMA user_version.
const SchemaVersion = 5

// migrate brings any database this build can open up to SchemaVersion.
//
// Order matters, and not in the obvious way. The column adds run *before*
// schema.sql, because schema.sql builds an index over channels(sort_order) and a
// pre-v3 database has the table but not the column: creating the index first
// fails with "no such column". Renaming the v1 tables runs earlier still, for
// the mirror-image reason: CREATE TABLE IF NOT EXISTS would find them and leave
// the old shape in place.
func (s *Store) migrate() error {
	v1, err := s.renameV1Tables()
	if err != nil {
		return err
	}
	for _, c := range []struct{ table, ddl string }{
		{"agents", `transcript_path TEXT NOT NULL DEFAULT ''`},
		{"channels", `sort_order INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := s.addColumn(c.table, c.ddl); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if v1 {
		if err := s.backfillV1(); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(
		`INSERT INTO schema_meta (key, value) VALUES ('schema_version', ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		strconv.Itoa(SchemaVersion)); err != nil {
		return fmt.Errorf("stamp schema version: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO agents (session_id, harness, alias, turn_state)
		 VALUES (?, 'human', ?, 'idle')
		 ON CONFLICT(session_id) DO NOTHING`, HumanID, HumanID); err != nil {
		return fmt.Errorf("seed human: %w", err)
	}
	return nil
}

func (s *Store) tableExists(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		name).Scan(&n)
	return n > 0, err
}

// addColumn is the only way this package adds a column to an existing table.
// CREATE TABLE IF NOT EXISTS cannot do it, so the ALTER is its own step. Two
// outcomes are not failures: the table is not there yet (fresh database, schema
// .sql is about to create it with the column), and the column is already there.
func (s *Store) addColumn(table, ddl string) error {
	ok, err := s.tableExists(table)
	if err != nil || !ok {
		return err
	}
	if _, err := s.db.Exec("ALTER TABLE " + table + " ADD COLUMN " + ddl); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			return fmt.Errorf("add %s.%s: %w", table, ddl, err)
		}
	}
	return nil
}

// renameV1Tables moves a pre-channel messages table out of the way, reporting
// whether it found one. v1 addressed a message with messages.to_session and
// stamped delivery onto the row; a database still shaped that way is detected by
// that column rather than by a version marker, because bash wrote those
// databases and never stamped one.
func (s *Store) renameV1Tables() (bool, error) {
	ok, err := s.tableExists("messages")
	if err != nil || !ok {
		return false, err
	}
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('messages') WHERE name = 'to_session'`).
		Scan(&n); err != nil {
		return false, fmt.Errorf("inspect messages: %w", err)
	}
	if n == 0 {
		return false, nil
	}
	if _, err := s.db.Exec(migrateV1PreSQL); err != nil {
		return false, fmt.Errorf("migrate v1 (rename): %w", err)
	}
	return true, nil
}

// backfillV1 copies the renamed v1 rows into the channel model and drops them.
// The uid pass is separate because sqlite3 has no sha256: the SQL leaves uid
// NULL and it is computed here, row by row, from the same fields Send hashes.
func (s *Store) backfillV1() error {
	if _, err := s.db.Exec(migrateV1PostSQL); err != nil {
		return fmt.Errorf("migrate v1 (backfill): %w", err)
	}
	if err := s.backfillUIDs(); err != nil {
		return err
	}
	// The backfill runs with foreign_keys off so that reply_to_id may point at a
	// row later in the same INSERT..SELECT. This proves the result rather than
	// trusting the insert order.
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("migrate v1 (check): %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		return errors.New("migrate v1: the backfill left dangling references")
	}
	return rows.Err()
}

// backfillUIDs gives every message without one a content address. A migrated
// message keeps an empty nonce: it predates the column, and inventing one would
// make the same message hash differently on the two machines that both migrated
// it, which is exactly what the uid exists to prevent.
//
// Ascending id order is what lets a reply hash its parent's uid: the parent is
// an earlier row, so it already has one by the time the reply is reached.
func (s *Store) backfillUIDs() error {
	return s.tx(func(tx *sql.Tx) error {
		rows, err := tx.Query(`
			SELECT m.id, m.nonce, c.name, t.name, m.from_session, m.body, m.created_at,
			       COALESCE(m.reply_to_id, 0)
			  FROM messages m
			  JOIN channels c ON c.id = m.channel_id
			  JOIN threads  t ON t.id = m.thread_id
			 WHERE m.uid IS NULL
			 ORDER BY m.id`)
		if err != nil {
			return err
		}
		type row struct {
			id                                 int64
			nonce, channel, thread, from, body string
			createdAt, replyTo                 int64
		}
		var todo []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.nonce, &r.channel, &r.thread, &r.from,
				&r.body, &r.createdAt, &r.replyTo); err != nil {
				rows.Close()
				return err
			}
			todo = append(todo, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		uids := make(map[int64]string, len(todo))
		for _, r := range todo {
			var parent string
			if r.replyTo != 0 {
				parent = uids[r.replyTo]
			}
			uid := MessageUID(r.nonce, r.channel, r.thread, r.from, r.body, r.createdAt, parent)
			if _, err := tx.Exec(`UPDATE messages SET uid = ? WHERE id = ?`, uid, r.id); err != nil {
				return fmt.Errorf("backfill uid for message %d: %w", r.id, err)
			}
			uids[r.id] = uid
		}
		return nil
	})
}

// tx runs fn in a transaction, rolling back on any error. Every write that spans
// more than one statement goes through here, so a refused cap or a failed access
// check cannot leave half a message behind.
func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
