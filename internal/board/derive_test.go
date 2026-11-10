package board

import (
	"database/sql"
	"slices"
	"testing"

	"github.com/LeikIsNRF/tt/internal/stack"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
)

func TestDeriveColumns(t *testing.T) {
	tasks := []store.Task{
		{ID: 1, Slug: "urgent"},
		{ID: 2, Slug: "busy"},
		{ID: 3, Slug: "fresh"}, // window, no session
		{ID: 4, Slug: "dormant"},
		{ID: 5, Slug: "shipped", DoneAt: sql.NullInt64{Int64: 1, Valid: true}},
		{ID: 6, Slug: "started"}, // session that hasn't had a prompt yet
	}
	sessions := []store.LiveSession{
		{TaskID: 1, State: store.Working},
		{TaskID: 1, State: store.NeedsYou},
		{TaskID: 2, State: store.Working},
		{TaskID: 6, State: store.Idle},
	}
	windows := []tmux.Window{
		{ID: "@1", Task: "urgent"}, {ID: "@2", Task: "busy"}, {ID: "@3", Task: "fresh"},
		{ID: "@6", Task: "started"},
	}
	snap := Derive(tasks, sessions, windows)
	want := map[string]Column{
		"urgent": ColNeedsYou, "busy": ColWorking, "fresh": ColIdle,
		"dormant": ColIdle, "shipped": ColDone, "started": ColIdle,
	}
	for col, cards := range snap.Columns {
		for _, c := range cards {
			if want[c.Task.Slug] != Column(col) {
				t.Errorf("%s in column %d, want %d", c.Task.Slug, col, want[c.Task.Slug])
			}
			delete(want, c.Task.Slug)
		}
	}
	if len(want) != 0 {
		t.Errorf("not placed: %v", want)
	}
}

func TestDeriveUntracked(t *testing.T) {
	tasks := []store.Task{{ID: 1, Slug: "known"}}
	sessions := []store.LiveSession{{State: store.YourTurn, TmuxWindow: "@2"}}
	windows := []tmux.Window{
		{ID: "@1", Task: "known"},
		{ID: "@2"},               // plain window
		{ID: "@3", Task: "gone"}, // names a task tt doesn't have
		{ID: "@9", Board: true},  // a pinned board
	}
	snap := Derive(tasks, sessions, windows)
	var ids []string
	for _, u := range snap.Untracked {
		ids = append(ids, u.Window.ID)
	}
	if len(ids) != 2 || ids[0] != "@2" || ids[1] != "@3" {
		t.Fatalf("untracked = %v", ids)
	}
	if got := snap.Untracked[0].States; len(got) != 1 || got[0] != store.YourTurn {
		t.Errorf("@2 states = %v", got)
	}
}

func TestStale(t *testing.T) {
	sessions := []store.LiveSession{
		{ID: "alive", TmuxPane: "%1"},
		{ID: "shell-only", TmuxPane: "%2"},
		{ID: "pane-gone", TmuxPane: "%3"},
		{ID: "outside-tmux"},
	}
	panes := []tmux.Pane{{ID: "%1"}, {ID: "%2"}}
	got := Stale(sessions, panes, map[string]bool{"%1": true})
	if len(got) != 2 || got[0].ID != "shell-only" || got[1].ID != "pane-gone" {
		t.Errorf("stale = %+v", got)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Chart Zoom Controls": "chart-zoom-controls",
		"  ABC-123: fix it! ": "abc-123-fix-it",
		"🔥":                   "task",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveFallsBackWhenBranchWasCleanedUp(t *testing.T) {
	// a merged and `gt sync` deleted it; b was re-parented onto master.
	g := stack.Graph{Trunk: "master", Parent: map[string]string{"b": "master", "c": "b"},
		Local: map[string]bool{"b": true, "c": true}}
	prs := map[string]stack.PR{"a": {State: "MERGED"}}
	got := resolve(g, "a", []string{"a", "b", "c"}, prs, true)
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("resolve = %v", got)
	}
}
