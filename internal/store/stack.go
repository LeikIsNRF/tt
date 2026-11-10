package store

import (
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/LeikIsNRF/tt/internal/stack"
)

// SetBranch sets the task's stack branch. Its remembered stack is reset:
// the next refresh resolves it from the new branch.
func (s *Store) SetBranch(taskID int64, branch string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`UPDATE tasks SET branch = ?, landed_offered = 0, updated_at = ? WHERE id = ?`,
		branch, time.Now().Unix(), taskID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM stack_branches WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	return tx.Commit()
}

// SetTicket sets the task's ticket ("" clears it).
func (s *Store) SetTicket(taskID int64, ticket string) error {
	_, err := s.db.Exec(`UPDATE tasks SET ticket = ?, updated_at = ? WHERE id = ?`,
		ticket, time.Now().Unix(), taskID)
	return err
}

// SetContext turns context injection into new Claude sessions on or off.
func (s *Store) SetContext(taskID int64, on bool) error {
	_, err := s.db.Exec(`UPDATE tasks SET context_enabled = ?, updated_at = ? WHERE id = ?`,
		on, time.Now().Unix(), taskID)
	return err
}

// TaskStack returns one task's remembered stack, bottom→top.
func (s *Store) TaskStack(taskID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT branch FROM stack_branches WHERE task_id = ? ORDER BY pos`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// SetLandedOffered records whether tt has offered to mark the task done
// because its whole stack merged, so it offers once per landing.
func (s *Store) SetLandedOffered(taskID int64, offered bool) error {
	_, err := s.db.Exec(`UPDATE tasks SET landed_offered = ? WHERE id = ?`, offered, taskID)
	return err
}

// AddLabel adds a manual label; adding one twice is a no-op.
func (s *Store) AddLabel(taskID int64, label string) error {
	_, err := s.db.Exec(`INSERT INTO task_labels (task_id, label) VALUES (?, ?)
		ON CONFLICT DO NOTHING`, taskID, label)
	return err
}

// RemoveLabel removes a label; ok is false if the task didn't have it.
func (s *Store) RemoveLabel(taskID int64, label string) (ok bool, err error) {
	res, err := s.db.Exec(`DELETE FROM task_labels WHERE task_id = ? AND label = ?`, taskID, label)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Labels returns every task's labels, in the order they were added.
func (s *Store) Labels() (map[int64][]string, error) {
	rows, err := s.db.Query(`SELECT task_id, label FROM task_labels ORDER BY rowid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var l string
		if err := rows.Scan(&id, &l); err != nil {
			return nil, err
		}
		out[id] = append(out[id], l)
	}
	return out, rows.Err()
}

// Stacks returns every task's remembered stack, bottom→top.
func (s *Store) Stacks() (map[int64][]string, error) {
	rows, err := s.db.Query(`SELECT task_id, branch FROM stack_branches ORDER BY task_id, pos`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var b string
		if err := rows.Scan(&id, &b); err != nil {
			return nil, err
		}
		out[id] = append(out[id], b)
	}
	return out, rows.Err()
}

// SaveStack replaces a task's remembered stack (bottom→top). It writes only
// when it changed, since the board calls it on every refresh.
func (s *Store) SaveStack(taskID int64, old, branches []string) error {
	if slices.Equal(old, branches) {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM stack_branches WHERE task_id = ?`, taskID); err != nil {
		return err
	}
	for i, b := range branches {
		if _, err := tx.Exec(`INSERT INTO stack_branches (task_id, pos, branch) VALUES (?, ?, ?)`,
			taskID, i, b); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const prsFetchedKey = "prs_fetched_at"

// SavePRs replaces the whole PR cache with one fetch's result (owner/name →
// branch → PR) and records when it happened.
func (s *Store) SavePRs(prs stack.PRs, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.Exec(`DELETE FROM prs`); err != nil {
		return err
	}
	for repo, byBranch := range prs {
		for b, p := range byBranch {
			if _, err := tx.Exec(`INSERT INTO prs (repo, branch, number, url, state, draft, review, checks)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, repo, b, p.Number, p.URL, p.State, p.Draft, p.Review, p.Checks); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, prsFetchedKey, at.Unix()); err != nil {
		return err
	}
	return tx.Commit()
}

// PRs returns the cached PRs and when they were fetched (zero time: never).
func (s *Store) PRs() (stack.PRs, time.Time, error) {
	var at int64
	err := s.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, prsFetchedKey).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return stack.PRs{}, time.Time{}, nil
	} else if err != nil {
		return nil, time.Time{}, err
	}
	rows, err := s.db.Query(`SELECT repo, branch, number, url, state, draft, review, checks FROM prs`)
	if err != nil {
		return nil, time.Time{}, err
	}
	defer rows.Close()
	out := stack.PRs{}
	for rows.Next() {
		var repo, b string
		var p stack.PR
		if err := rows.Scan(&repo, &b, &p.Number, &p.URL, &p.State, &p.Draft, &p.Review, &p.Checks); err != nil {
			return nil, time.Time{}, err
		}
		if out[repo] == nil {
			out[repo] = map[string]stack.PR{}
		}
		out[repo][b] = p
	}
	return out, time.Unix(at, 0), rows.Err()
}
