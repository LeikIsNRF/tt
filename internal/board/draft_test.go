package board

import (
	"errors"
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/LeikIsNRF/tt/internal/draft"
	"github.com/LeikIsNRF/tt/internal/todo"
)

func TestSavedDraftLandsUnderTodosAndNotes(t *testing.T) {
	t.Setenv("TT_HOME", t.TempDir())
	if err := todo.EnsureTaskFile("fix-api", "Fix API", "ABC-456"); err != nil {
		t.Fatal(err)
	}
	d := &draft.Draft{Todos: []string{"reproduce", "add tests"}, Notes: "admin endpoint only"}
	if err := writeDraft("fix-api", d); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(todo.TaskPath("fix-api"))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Fix API\n\nTicket: ABC-456\n\n## Todos\n\n- [ ] reproduce\n- [ ] add tests\n\n## Notes\n\nadmin endpoint only\n"
	if string(b) != want {
		t.Errorf("got %q\nwant %q", b, want)
	}
}

func TestDraftFillsFormOnlyOnSuccess(t *testing.T) {
	f := newCreateForm("/repo")
	f.set("describe", "fix the api, ABC-456")
	f.set("name", "my name")

	f.cancel = func() {}
	f.applyDraft(draftMsg{form: f, err: errors.New("draft timed out after 30s")})
	if f.value("name") != "my name" || f.draft != nil || f.err != "draft timed out after 30s" || f.drafting() {
		t.Fatalf("failed draft changed the form: name=%q draft=%v err=%q", f.value("name"), f.draft, f.err)
	}

	f.applyDraft(draftMsg{form: f, d: draft.Draft{Name: "Fix API", Ticket: "ABC-456", Todos: []string{"x"}}})
	if f.value("name") != "Fix API" || f.value("ticket") != "ABC-456" || f.value("describe") != "fix the api, ABC-456" || f.draft == nil {
		t.Fatalf("draft not applied: %+v", f.values())
	}
}

func TestCtrlGWithoutDescriptionDoesNotCallClaude(t *testing.T) {
	t.Setenv("PATH", "") // a claude call would fail with "not on PATH" instead
	m := &model{form: newCreateForm("/repo")}
	if cmd := m.updateForm(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}); cmd != nil || m.form.drafting() {
		t.Fatal("ctrl+g with no description started a draft")
	}
	if m.form.err != "write a description first, then ctrl+g" {
		t.Errorf("err = %q", m.form.err)
	}
}

func TestEscWhileDraftingDiscardsEverything(t *testing.T) {
	cancelled := false
	m := &model{form: newCreateForm("/repo")}
	m.form.cancel = func() { cancelled = true }
	f := m.form
	m.updateForm(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.form != nil || !cancelled {
		t.Fatalf("form=%v cancelled=%v", m.form, cancelled)
	}
	// A reply landing after esc goes nowhere.
	m.update(draftMsg{form: f, d: draft.Draft{Name: "late"}})
	if m.form != nil {
		t.Fatal("late draft reopened the form")
	}
}

func TestDraftErrorSurvivesNonKeyMessages(t *testing.T) {
	f := newCreateForm("/repo")
	f.applyDraft(draftMsg{form: f, err: errors.New("draft failed: boom")})
	f.update(struct{}{}) // e.g. the cursor blinking
	if f.err == "" {
		t.Fatal("a non-key message cleared the error")
	}
}
