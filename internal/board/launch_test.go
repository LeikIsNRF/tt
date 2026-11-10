package board

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
	"github.com/LeikIsNRF/tt/internal/todo"
)

func TestTaskPromptListsOpenTodos(t *testing.T) {
	got := taskPrompt(store.Task{Name: "Chart zoom", Ticket: "ABC-1"},
		[]todo.Todo{{Text: "wire TTL"}, {Text: "old", Done: true}})
	want := "Work on the task \"Chart zoom\" (ABC-1).\n\nOpen todos:\n- wire TTL"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// The tick-off line must find the same todo: `tt todo done` matches text.
func TestTodoPromptTicksItselfOff(t *testing.T) {
	got := todoPrompt("live", todo.Todo{Text: `don't drop "$TTL"`})
	if !strings.HasSuffix(got, `tt todo done --task live "don't drop \"\$TTL\""`) {
		t.Errorf("got %q", got)
	}
}

func TestSharedCheckoutOnlyCountsWorkingInSameCwd(t *testing.T) {
	sessions := []store.LiveSession{
		{State: store.YourTurn, Cwd: "/repo"},
		{State: store.Working, Cwd: "/other"},
	}
	if sharedCheckout(sessions, "/repo") {
		t.Error("warned without a working session in /repo")
	}
	sessions = append(sessions, store.LiveSession{State: store.Working, Cwd: "/repo/"})
	if !sharedCheckout(sessions, "/repo") {
		t.Error("no warning with a working session in /repo")
	}
}

func TestLaunchKeyOnTodoPrefillsTodoPrompt(t *testing.T) {
	m := &model{width: 120, height: 40, focus: onTodos}
	m.snap.Columns[ColIdle] = []Card{{
		Task:   store.Task{Slug: "live", Name: "Live"},
		Window: &tmux.Window{ID: "@1"},
		Todos:  []todo.Todo{{Text: "wire TTL"}},
	}}
	m.col = ColIdle
	m.update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if m.launch == nil {
		t.Fatal("l opened no launch")
	}
	if got := m.launch.prompt.Value(); got != todoPrompt("live", todo.Todo{Text: "wire TTL"}) {
		t.Errorf("prompt %q", got)
	}
}
