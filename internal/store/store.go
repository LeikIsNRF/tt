// Package store owns tt's SQLite database: tasks and the Claude sessions
// linked to them. Todos never live here — markdown is their only source.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// State is a Claude session's attention state.
type State string

const (
	NeedsYou State = "needs_you"
	YourTurn State = "your_turn"
	Working  State = "working"
	Idle     State = "idle" // started, no prompt yet
	Ended    State = "ended"
)

// urgency orders states for "most urgent session wins".
var urgency = map[State]int{NeedsYou: 4, YourTurn: 3, Working: 2, Idle: 1}

// MoreUrgent reports whether a needs attention before b.
func MoreUrgent(a, b State) bool { return urgency[a] > urgency[b] }

// MostUrgent returns the most urgent non-ended state, or "" if none.
func MostUrgent(states []State) State {
	var best State
	for _, s := range states {
		if urgency[s] > urgency[best] {
			best = s
		}
	}
	return best
}

var migrations = []string{
	`CREATE TABLE tasks (
		id              INTEGER PRIMARY KEY,
		slug            TEXT    NOT NULL UNIQUE,
		name            TEXT    NOT NULL,
		cwd             TEXT    NOT NULL DEFAULT '',
		env             TEXT    NOT NULL DEFAULT 'local',
		ticket          TEXT    NOT NULL DEFAULT '',
		branch          TEXT    NOT NULL DEFAULT '',
		context_enabled INTEGER NOT NULL DEFAULT 1,
		created_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		done_at         INTEGER
	);
	CREATE TABLE sessions (
		id              TEXT    PRIMARY KEY,
		task_id         INTEGER REFERENCES tasks(id),
		state           TEXT    NOT NULL,
		last_prompt     TEXT    NOT NULL DEFAULT '',
		cwd             TEXT    NOT NULL DEFAULT '',
		transcript_path TEXT    NOT NULL DEFAULT '',
		tmux_pane       TEXT    NOT NULL DEFAULT '',
		tmux_window     TEXT    NOT NULL DEFAULT '',
		started_at      INTEGER NOT NULL,
		updated_at      INTEGER NOT NULL,
		ended_at        INTEGER
	);
	CREATE INDEX sessions_task ON sessions(task_id);
	CREATE INDEX sessions_window ON sessions(tmux_window);`,
	// M4: labels, remembered stacks, and the PR cache they're drawn from.
	`CREATE TABLE task_labels (
		task_id INTEGER NOT NULL REFERENCES tasks(id),
		label   TEXT    NOT NULL,
		PRIMARY KEY (task_id, label)
	);
	CREATE TABLE stack_branches (
		task_id INTEGER NOT NULL REFERENCES tasks(id),
		pos     INTEGER NOT NULL, -- 0 = bottom of the stack
		branch  TEXT    NOT NULL,
		PRIMARY KEY (task_id, pos)
	);
	CREATE TABLE prs (
		repo       TEXT    NOT NULL, -- GitHub owner/name
		branch     TEXT    NOT NULL,
		number     INTEGER NOT NULL,
		url        TEXT    NOT NULL,
		state      TEXT    NOT NULL,
		draft      INTEGER NOT NULL,
		review     TEXT    NOT NULL,
		checks     TEXT    NOT NULL,
		PRIMARY KEY (repo, branch)
	);
	CREATE TABLE meta (
		key   TEXT PRIMARY KEY,
		value TEXT NOT NULL
	);
	ALTER TABLE tasks ADD COLUMN landed_offered INTEGER NOT NULL DEFAULT 0;`,
}

type Store struct{ db *sql.DB }

// Open opens (creating if needed) the DB at path and applies migrations.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := "file:" + path +
		"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

// migrate runs under BEGIN IMMEDIATE so concurrent hooks racing on a fresh
// DB don't both apply the same migration.
func (s *Store) migrate() error {
	ctx := context.Background()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var v int
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	if v >= len(migrations) {
		return nil
	}
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	commit := false
	defer func() {
		if !commit {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	if err := conn.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return err
	}
	for i := v; i < len(migrations); i++ {
		if _, err := conn.ExecContext(ctx, migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(migrations))); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	commit = true
	return nil
}

// EnsureTask returns the id of the task with slug, creating it (name = slug)
// if it doesn't exist yet; created reports whether it did.
func (s *Store) EnsureTask(slug, cwd string) (id int64, created bool, err error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO tasks (slug, name, cwd, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT(slug) DO NOTHING`, slug, slug, cwd, now, now)
	if err != nil {
		return 0, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, err
	}
	err = s.db.QueryRow(`SELECT id FROM tasks WHERE slug = ?`, slug).Scan(&id)
	return id, n > 0, err
}

// SessionState returns a session's current state; ok is false if unknown.
func (s *Store) SessionState(id string) (State, bool, error) {
	var st State
	err := s.db.QueryRow(`SELECT state FROM sessions WHERE id = ?`, id).Scan(&st)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return st, err == nil, err
}

// Event is one hook firing, already resolved to a task and tmux location.
type Event struct {
	SessionID      string
	TaskID         int64 // 0 = unlinked
	State          State // "" = leave state unchanged
	Prompt         string
	Cwd            string
	TranscriptPath string
	TmuxPane       string
	TmuxWindow     string
}

// RecordEvent upserts the session. A session's task link is set once and
// never changed afterwards.
func (s *Store) RecordEvent(e Event) error {
	now := time.Now().Unix()
	var task any
	if e.TaskID != 0 {
		task = e.TaskID
	}
	initial := e.State
	if initial == "" {
		initial = Idle
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`INSERT INTO sessions (id, state, started_at, updated_at)
		VALUES (?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`, e.SessionID, initial, now, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE sessions SET
			task_id         = COALESCE(task_id, ?),
			cwd             = COALESCE(NULLIF(?, ''), cwd),
			transcript_path = COALESCE(NULLIF(?, ''), transcript_path),
			tmux_pane       = COALESCE(NULLIF(?, ''), tmux_pane),
			tmux_window     = COALESCE(NULLIF(?, ''), tmux_window),
			last_prompt     = COALESCE(NULLIF(?, ''), last_prompt),
			updated_at      = ?
		WHERE id = ?`,
		task, e.Cwd, e.TranscriptPath, e.TmuxPane, e.TmuxWindow, e.Prompt, now, e.SessionID); err != nil {
		return err
	}
	if e.State != "" {
		var endedAt any
		if e.State == Ended {
			endedAt = now
		}
		if _, err := tx.Exec(`UPDATE sessions SET state = ?, ended_at = ? WHERE id = ?`,
			e.State, endedAt, e.SessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WindowState is the most urgent state among live sessions in a tmux window.
func (s *Store) WindowState(window string) (State, error) {
	rows, err := s.db.Query(`SELECT state FROM sessions
		WHERE tmux_window = ? AND ended_at IS NULL`, window)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var states []State
	for rows.Next() {
		var st State
		if err := rows.Scan(&st); err != nil {
			return "", err
		}
		states = append(states, st)
	}
	return MostUrgent(states), rows.Err()
}

// NeedsYouCount counts live sessions waiting on the user.
func (s *Store) NeedsYouCount() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions
		WHERE state = ? AND ended_at IS NULL`, NeedsYou).Scan(&n)
	return n, err
}

// Task is a board read model.
type Task struct {
	ID     int64
	Slug   string
	Name   string
	Cwd    string
	Ticket string
	Branch string // any branch of the task's graphite stack
	DoneAt sql.NullInt64
	// LandedOffered: tt already offered to mark the task done because its
	// whole stack merged.
	LandedOffered bool
	// ContextEnabled: new Claude sessions get the task's context injected.
	ContextEnabled bool
}

const taskCols = `id, slug, name, cwd, ticket, branch, done_at, landed_offered, context_enabled`

func scanTask(row interface{ Scan(...any) error }) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.Slug, &t.Name, &t.Cwd, &t.Ticket, &t.Branch, &t.DoneAt, &t.LandedOffered,
		&t.ContextEnabled)
	return t, err
}

// CreateTask inserts a task. The slug must be free (see SlugTaken).
func (s *Store) CreateTask(slug, name, cwd, ticket string) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`INSERT INTO tasks (slug, name, cwd, ticket, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)`, slug, name, cwd, ticket, now, now)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// SlugTaken reports whether any task, done or not, already uses slug.
func (s *Store) SlugTaken(slug string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM tasks WHERE slug = ?`, slug).Scan(&n)
	return n > 0, err
}

// Tasks returns tasks that are not done or were done at or after doneSince,
// oldest first.
func (s *Store) Tasks(doneSince time.Time) ([]Task, error) {
	rows, err := s.db.Query(`SELECT `+taskCols+` FROM tasks
		WHERE done_at IS NULL OR done_at >= ? ORDER BY id`, doneSince.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ts []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		ts = append(ts, t)
	}
	return ts, rows.Err()
}

// LiveSession is a session that hasn't ended.
type LiveSession struct {
	ID         string
	TaskID     int64 // 0 = unlinked
	State      State
	TmuxPane   string
	TmuxWindow string
	Cwd        string
}

// LiveSessions returns every session that hasn't ended.
func (s *Store) LiveSessions() ([]LiveSession, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(task_id, 0), state, tmux_pane, tmux_window, cwd
		FROM sessions WHERE ended_at IS NULL ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ss []LiveSession
	for rows.Next() {
		var l LiveSession
		if err := rows.Scan(&l.ID, &l.TaskID, &l.State, &l.TmuxPane, &l.TmuxWindow, &l.Cwd); err != nil {
			return nil, err
		}
		ss = append(ss, l)
	}
	return ss, rows.Err()
}

// EndSessions marks sessions ended when Claude died without SessionEnd. The
// state is kept as it was, so a resume picker can tell a session that was
// interrupted mid-turn from one that ended cleanly (state Ended), and so is
// updated_at: it's the session's last activity, not when tt noticed.
func (s *Store) EndSessions(ids []string) error {
	now := time.Now().Unix()
	for _, id := range ids {
		if _, err := s.db.Exec(`UPDATE sessions SET ended_at = ?
			WHERE id = ? AND ended_at IS NULL`, now, id); err != nil {
			return err
		}
	}
	return nil
}

// LinkWindowSessions links a window's live unlinked sessions to a task, for
// when the window is adopted mid-session.
func (s *Store) LinkWindowSessions(window string, taskID int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET task_id = ?
		WHERE tmux_window = ? AND task_id IS NULL AND ended_at IS NULL`, taskID, window)
	return err
}

// Session is one session row.
type Session struct {
	ID             string
	TaskID         sql.NullInt64
	State          State // for an ended session: Ended, or the state it was in when Claude died
	LastPrompt     string
	Cwd            string
	TranscriptPath string
	TmuxWindow     string
	UpdatedAt      int64
	EndedAt        sql.NullInt64
}

const sessionCols = `id, task_id, state, last_prompt, cwd, transcript_path, tmux_window,
	updated_at, ended_at`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var r Session
	err := row.Scan(&r.ID, &r.TaskID, &r.State, &r.LastPrompt, &r.Cwd, &r.TranscriptPath,
		&r.TmuxWindow, &r.UpdatedAt, &r.EndedAt)
	return r, err
}

func (s *Store) Session(id string) (Session, error) {
	return scanSession(s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id))
}

// TaskSessions returns every session ever linked to a task, most recently
// active first.
func (s *Store) TaskSessions(taskID int64) ([]Session, error) {
	rows, err := s.db.Query(`SELECT `+sessionCols+` FROM sessions
		WHERE task_id = ? ORDER BY updated_at DESC, started_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ss []Session
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		ss = append(ss, r)
	}
	return ss, rows.Err()
}

// PromptedSessionCounts counts, per task, the sessions that got at least one
// prompt: the ones worth resuming.
func (s *Store) PromptedSessionCounts() (map[int64]int, error) {
	rows, err := s.db.Query(`SELECT task_id, COUNT(*) FROM sessions
		WHERE task_id IS NOT NULL AND last_prompt != '' GROUP BY task_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	n := map[int64]int{}
	for rows.Next() {
		var id int64
		var c int
		if err := rows.Scan(&id, &c); err != nil {
			return nil, err
		}
		n[id] = c
	}
	return n, rows.Err()
}

// TaskBySlug looks a task up; ok is false if there's none.
func (s *Store) TaskBySlug(slug string) (t Task, ok bool, err error) {
	t, err = scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM tasks WHERE slug = ?`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	return t, err == nil, err
}

// DeleteTask removes a task and everything that points at it: its
// sessions, labels and remembered stack.
func (s *Store) DeleteTask(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	for _, q := range []string{
		`DELETE FROM sessions WHERE task_id = ?`,
		`DELETE FROM task_labels WHERE task_id = ?`,
		`DELETE FROM stack_branches WHERE task_id = ?`,
		`DELETE FROM tasks WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MarkDone moves a task to Done.
func (s *Store) MarkDone(id int64) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`UPDATE tasks SET done_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}
