// Package store persists runs, log events, failures and operator notes in
// SQLite. It uses modernc.org/sqlite, a pure-Go driver, so the Windows CI
// build needs no cgo toolchain.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/SAY-5/station-diag-dashboard/internal/ingest"
	"github.com/SAY-5/station-diag-dashboard/internal/rules"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database connection.
type Store struct {
	db *sql.DB
}

// Run summarizes a single bench run.
type Run struct {
	RunID      string    `json:"run_id"`
	StationID  string    `json:"station_id"`
	FirstSeen  time.Time `json:"first_seen"`
	LastSeen   time.Time `json:"last_seen"`
	EventCount int       `json:"event_count"`
	Failures   int       `json:"failures"`
	Resolved   bool      `json:"resolved"`
}

// Note is a persisted operator note.
type Note struct {
	ID        int64     `json:"id"`
	StationID string    `json:"station_id"`
	RunID     string    `json:"run_id"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	run_id      TEXT PRIMARY KEY,
	station_id  TEXT NOT NULL,
	first_seen  TEXT NOT NULL,
	last_seen   TEXT NOT NULL,
	resolved    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS events (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id      TEXT NOT NULL,
	station_id  TEXT NOT NULL,
	ts          TEXT NOT NULL,
	level       TEXT NOT NULL,
	subsystem   TEXT NOT NULL,
	message     TEXT NOT NULL,
	actuator_id TEXT
);
CREATE INDEX IF NOT EXISTS idx_events_run ON events(run_id);
CREATE TABLE IF NOT EXISTS failures (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id      TEXT NOT NULL,
	station_id  TEXT NOT NULL,
	rule_id     TEXT NOT NULL,
	actuator_id TEXT,
	detail      TEXT NOT NULL,
	severity    TEXT NOT NULL,
	at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_failures_run ON failures(run_id);
CREATE TABLE IF NOT EXISTS notes (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id      TEXT NOT NULL,
	station_id  TEXT NOT NULL,
	author      TEXT NOT NULL,
	body        TEXT NOT NULL,
	created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_notes_run ON notes(run_id);
`

// Open opens (and migrates) the SQLite database at path. Use ":memory:"
// for tests. The pragmas favor correctness for a single-process service.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

const tsLayout = time.RFC3339Nano

// RecordEvent persists a log event and upserts its parent run.
func (s *Store) RecordEvent(e ingest.LogEvent) error {
	if e.RunID == "" {
		return errors.New("store: event has no run_id")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	ts := e.TS.Format(tsLayout)
	_, err = tx.Exec(`
		INSERT INTO runs (run_id, station_id, first_seen, last_seen)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(run_id) DO UPDATE SET last_seen = excluded.last_seen`,
		e.RunID, e.StationID, ts, ts)
	if err != nil {
		return fmt.Errorf("store: upsert run: %w", err)
	}
	_, err = tx.Exec(`
		INSERT INTO events (run_id, station_id, ts, level, subsystem, message, actuator_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.RunID, e.StationID, ts, e.Level, e.Subsystem, e.Message, e.ActuatorID)
	if err != nil {
		return fmt.Errorf("store: insert event: %w", err)
	}
	return tx.Commit()
}

// RecordFailure persists a detected actuator failure.
func (s *Store) RecordFailure(f rules.Failure) error {
	_, err := s.db.Exec(`
		INSERT INTO failures (run_id, station_id, rule_id, actuator_id, detail, severity, at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		f.RunID, f.StationID, f.RuleID, f.Actuator, f.Detail, f.Severity,
		f.At.Format(tsLayout))
	if err != nil {
		return fmt.Errorf("store: insert failure: %w", err)
	}
	return nil
}

// AddNote persists an operator note and returns it with its assigned ID.
func (s *Store) AddNote(stationID, runID, author, body string) (Note, error) {
	if runID == "" || body == "" {
		return Note{}, errors.New("store: note needs run_id and body")
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`
		INSERT INTO notes (run_id, station_id, author, body, created_at)
		VALUES (?, ?, ?, ?, ?)`,
		runID, stationID, author, body, now.Format(tsLayout))
	if err != nil {
		return Note{}, fmt.Errorf("store: insert note: %w", err)
	}
	id, _ := res.LastInsertId()
	return Note{
		ID: id, StationID: stationID, RunID: runID,
		Author: author, Body: body, CreatedAt: now,
	}, nil
}

// SetResolved marks a run resolved or unresolved.
func (s *Store) SetResolved(runID string, resolved bool) error {
	v := 0
	if resolved {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE runs SET resolved = ? WHERE run_id = ?`, v, runID)
	if err != nil {
		return fmt.Errorf("store: set resolved: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return errors.New("store: run not found")
	}
	return nil
}

// ListRuns returns runs ordered by most recent activity first.
func (s *Store) ListRuns(limit int) ([]Run, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
		SELECT r.run_id, r.station_id, r.first_seen, r.last_seen, r.resolved,
			(SELECT COUNT(*) FROM events  e WHERE e.run_id = r.run_id),
			(SELECT COUNT(*) FROM failures f WHERE f.run_id = r.run_id)
		FROM runs r
		ORDER BY r.last_seen DESC
		LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list runs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Run
	for rows.Next() {
		var r Run
		var first, last string
		var resolved int
		if err := rows.Scan(&r.RunID, &r.StationID, &first, &last, &resolved,
			&r.EventCount, &r.Failures); err != nil {
			return nil, err
		}
		r.FirstSeen, _ = time.Parse(tsLayout, first)
		r.LastSeen, _ = time.Parse(tsLayout, last)
		r.Resolved = resolved != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRun returns a single run summary.
func (s *Store) GetRun(runID string) (Run, error) {
	var r Run
	var first, last string
	var resolved int
	err := s.db.QueryRow(`
		SELECT r.run_id, r.station_id, r.first_seen, r.last_seen, r.resolved,
			(SELECT COUNT(*) FROM events  e WHERE e.run_id = r.run_id),
			(SELECT COUNT(*) FROM failures f WHERE f.run_id = r.run_id)
		FROM runs r WHERE r.run_id = ?`, runID).
		Scan(&r.RunID, &r.StationID, &first, &last, &resolved,
			&r.EventCount, &r.Failures)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, fmt.Errorf("store: get run: %w", err)
	}
	r.FirstSeen, _ = time.Parse(tsLayout, first)
	r.LastSeen, _ = time.Parse(tsLayout, last)
	r.Resolved = resolved != 0
	return r, nil
}

// ErrNotFound is returned when a run does not exist.
var ErrNotFound = errors.New("store: run not found")

// RunEvent is a stored log event for a run timeline.
type RunEvent struct {
	TS        time.Time `json:"ts"`
	Level     string    `json:"level"`
	Subsystem string    `json:"subsystem"`
	Message   string    `json:"message"`
	Actuator  string    `json:"actuator_id"`
}

// RunEvents returns the ordered event timeline of a run.
func (s *Store) RunEvents(runID string) ([]RunEvent, error) {
	rows, err := s.db.Query(`
		SELECT ts, level, subsystem, message, actuator_id
		FROM events WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: run events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []RunEvent
	for rows.Next() {
		var e RunEvent
		var ts string
		var actuator sql.NullString
		if err := rows.Scan(&ts, &e.Level, &e.Subsystem, &e.Message, &actuator); err != nil {
			return nil, err
		}
		e.TS, _ = time.Parse(tsLayout, ts)
		e.Actuator = actuator.String
		out = append(out, e)
	}
	return out, rows.Err()
}

// RunFailures returns the failures recorded against a run.
func (s *Store) RunFailures(runID string) ([]rules.Failure, error) {
	rows, err := s.db.Query(`
		SELECT rule_id, station_id, actuator_id, detail, severity, at
		FROM failures WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: run failures: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []rules.Failure
	for rows.Next() {
		var f rules.Failure
		var at string
		var actuator sql.NullString
		if err := rows.Scan(&f.RuleID, &f.StationID, &actuator, &f.Detail,
			&f.Severity, &at); err != nil {
			return nil, err
		}
		f.RunID = runID
		f.Actuator = actuator.String
		f.At, _ = time.Parse(tsLayout, at)
		out = append(out, f)
	}
	return out, rows.Err()
}

// RunNotes returns the operator notes attached to a run.
func (s *Store) RunNotes(runID string) ([]Note, error) {
	rows, err := s.db.Query(`
		SELECT id, run_id, station_id, author, body, created_at
		FROM notes WHERE run_id = ? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, fmt.Errorf("store: run notes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []Note
	for rows.Next() {
		var n Note
		var created string
		if err := rows.Scan(&n.ID, &n.RunID, &n.StationID, &n.Author,
			&n.Body, &created); err != nil {
			return nil, err
		}
		n.CreatedAt, _ = time.Parse(tsLayout, created)
		out = append(out, n)
	}
	return out, rows.Err()
}
