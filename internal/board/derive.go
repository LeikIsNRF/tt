// Package board is tt's control tower: a Bubble Tea board of tasks in
// columns derived from their Claude sessions' attention state.
package board

import (
	"strings"
	"time"

	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// Column is a board column. Cards land in one by state; nobody drags them.
type Column int

const (
	ColNeedsYou Column = iota
	ColYourTurn
	ColWorking
	ColIdle
	ColDone
	numCols
)

var columnTitles = [numCols]string{"🔴 Needs you", "🟡 Your turn", "🟢 Working", "💤 Idle", "✓ Done"}

// Card is one task on the board.
type Card struct {
	Task   store.Task
	Window *tmux.Window  // nil = dormant (no window)
	States []store.State // states of its live sessions
	Todos  []todo.Todo   // from the task's markdown file
	Past   int           // sessions ever linked that got a prompt (resumable)
	Labels []string
	// Pane is what the preview shows: the pane of the most urgent live
	// Claude session (Claude is true), else the window's active pane. ""
	// when dormant.
	Pane   string
	Claude bool

	Stack    []Branch // bottom→top; empty when the task has no branch
	Trunk    string
	StackErr string // why the stack couldn't be resolved
	// Landed: every PR in the stack merged. OfferDone: and tt hasn't
	// offered to mark the task done for it yet.
	Landed, OfferDone bool
}

// Column is the task's most urgent session state; Done is the only manual
// column, and live tasks with no active session sit in Idle.
func (c Card) Column() Column {
	if c.Task.DoneAt.Valid {
		return ColDone
	}
	switch store.MostUrgent(c.States) {
	case store.NeedsYou:
		return ColNeedsYou
	case store.YourTurn:
		return ColYourTurn
	case store.Working:
		return ColWorking
	}
	return ColIdle
}

// Untracked is a tmux window tt doesn't know as a task.
type Untracked struct {
	Window tmux.Window
	States []store.State // states of live sessions seen in it
}

// Snapshot is everything the board shows, derived from one refresh.
type Snapshot struct {
	Columns   [numCols][]Card
	Untracked []Untracked
	Inbox     []todo.Todo
	// HasStacks: some shown task has a branch, so PRs are worth fetching.
	HasStacks    bool
	PRsFetchedAt time.Time // zero: never
}

// Derive places tasks in columns and lists untracked windows. Windows a
// board is pinned in aren't listed.
func Derive(tasks []store.Task, sessions []store.LiveSession, windows []tmux.Window) Snapshot {
	bySlug := map[string]bool{}
	for _, t := range tasks {
		bySlug[t.Slug] = true
	}
	taskWindow := map[string]*tmux.Window{}
	var snap Snapshot
	for i := range windows {
		w := &windows[i]
		switch {
		case bySlug[w.Task]:
			if taskWindow[w.Task] == nil {
				taskWindow[w.Task] = w
			}
		case !w.Board:
			// No @tt_task, or one naming a task tt no longer has: adoptable.
			u := Untracked{Window: *w}
			for _, s := range sessions {
				if s.TmuxWindow == w.ID {
					u.States = append(u.States, s.State)
				}
			}
			snap.Untracked = append(snap.Untracked, u)
		}
	}
	for _, t := range tasks {
		c := Card{Task: t, Window: taskWindow[t.Slug]}
		var best store.State
		for _, s := range sessions {
			if s.TaskID != t.ID {
				continue
			}
			c.States = append(c.States, s.State)
			// Sessions come oldest first: on a tie the newest wins.
			if c.Window != nil && s.TmuxPane != "" && !store.MoreUrgent(best, s.State) {
				c.Pane, c.Claude, best = s.TmuxPane, true, s.State
			}
		}
		if c.Window != nil && c.Pane == "" {
			c.Pane = c.Window.Pane
		}
		col := c.Column()
		snap.Columns[col] = append(snap.Columns[col], c)
	}
	return snap
}

// Stale returns live sessions whose pane is gone or no longer runs Claude:
// Claude was killed without firing SessionEnd. Sessions with no known pane
// can't be checked and are left alone.
func Stale(sessions []store.LiveSession, panes []tmux.Pane, claude map[string]bool) []store.LiveSession {
	exists := map[string]bool{}
	for _, p := range panes {
		exists[p.ID] = true
	}
	var stale []store.LiveSession
	for _, s := range sessions {
		if s.TmuxPane != "" && (!exists[s.TmuxPane] || !claude[s.TmuxPane]) {
			stale = append(stale, s)
		}
	}
	return stale
}

// Slugify turns a task name into a kebab-case slug.
func Slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	if b.Len() == 0 {
		return "task"
	}
	return b.String()
}
