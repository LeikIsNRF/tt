package board

import (
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
)

func TestPreviewPane(t *testing.T) {
	tasks := []store.Task{{ID: 1, Slug: "two-claudes"}, {ID: 2, Slug: "shell-only"}, {ID: 3, Slug: "dormant"}}
	sessions := []store.LiveSession{
		{TaskID: 1, State: store.Working, TmuxPane: "%1"},
		{TaskID: 1, State: store.NeedsYou, TmuxPane: "%2"},
		{TaskID: 1, State: store.Working, TmuxPane: "%3"},
		{TaskID: 3, State: store.YourTurn, TmuxPane: "%9"}, // pane of a closed window
	}
	windows := []tmux.Window{{ID: "@1", Task: "two-claudes", Pane: "%0"}, {ID: "@2", Task: "shell-only", Pane: "%5"}}
	got := map[string][2]any{}
	for _, cards := range Derive(tasks, sessions, windows).Columns {
		for _, c := range cards {
			got[c.Task.Slug] = [2]any{c.Pane, c.Claude}
		}
	}
	want := map[string][2]any{
		"two-claudes": {"%2", true},  // the one that needs you
		"shell-only":  {"%5", false}, // no Claude: the active pane
		"dormant":     {"", false},
	}
	for slug, w := range want {
		if got[slug] != w {
			t.Errorf("%s previews %v, want %v", slug, got[slug], w)
		}
	}
}

func TestScreenTail(t *testing.T) {
	screen := "one\ntwo\n\x1b[31mthree is long\n  \n\n"
	got := screenTail(screen, 2, 5)
	for i := range got {
		got[i] = ansi.Strip(got[i])
	}
	if want := []string{"two", "three"}; !slices.Equal(got, want) {
		t.Errorf("tail = %q, want %q", got, want)
	}
}
