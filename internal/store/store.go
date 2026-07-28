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

	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var schemaSQL string

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

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schemaSQL); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO agents (session_id, harness, alias, turn_state)
		 VALUES (?, 'human', ?, 'idle')
		 ON CONFLICT(session_id) DO NOTHING`, HumanID, HumanID); err != nil {
		return fmt.Errorf("seed human: %w", err)
	}
	return nil
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
