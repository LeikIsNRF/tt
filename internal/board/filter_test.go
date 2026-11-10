package board

import (
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
)

func slugs(s Snapshot) []string {
	var out []string
	for _, cards := range s.Columns {
		for _, c := range cards {
			out = append(out, c.Task.Slug)
		}
	}
	return out
}

func TestFilterSnap(t *testing.T) {
	now := time.Unix(100*24*3600, 0)
	old := sql.NullInt64{Int64: now.Add(-30 * 24 * time.Hour).Unix(), Valid: true}
	var full Snapshot
	full.Columns[ColIdle] = []Card{
		{Task: store.Task{Slug: "chart-zoom", Name: "Chart Zoom Controls", Ticket: "ABC-123"}},
		{Task: store.Task{Slug: "login", Name: "Login retries"}, Labels: []string{"blocked-on-design"}},
	}
	full.Columns[ColDone] = []Card{{Task: store.Task{Slug: "old-chart", Name: "Old chart work", DoneAt: old}}}
	full.Untracked = []Untracked{{Window: tmux.Window{Name: "chart-scratch"}}, {Window: tmux.Window{Name: "zsh"}}}

	for _, tc := range []struct {
		q    string
		want []string
	}{
		{"", []string{"chart-zoom", "login"}}, // old Done only shows while filtering
		{"chart", []string{"chart-zoom", "old-chart"}},
		{"abc-123", []string{"chart-zoom"}},
		{"BLOCKED", []string{"login"}},
		{"chart zoom", []string{"chart-zoom"}}, // every word must match
		{"nothing", nil},
	} {
		if got := slugs(filterSnap(full, tc.q, now)); !slices.Equal(got, tc.want) {
			t.Errorf("filter %q = %v, want %v", tc.q, got, tc.want)
		}
	}
	if u := filterSnap(full, "chart", now).Untracked; len(u) != 1 || u[0].Window.Name != "chart-scratch" {
		t.Errorf("untracked filtered to %v", u)
	}
}
