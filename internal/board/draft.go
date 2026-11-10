package board

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/LeikIsNRF/tt/internal/config"
	"github.com/LeikIsNRF/tt/internal/draft"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// draftMsg is a finished ctrl+g call for form f.
type draftMsg struct {
	form *form
	d    draft.Draft
	err  error
}

// startDraft sends the create form's description to claude. The form is
// read-only until the reply lands, so the draft can't overwrite typing.
func (f *form) startDraft() tea.Cmd {
	desc := strings.TrimSpace(f.value("describe"))
	if desc == "" {
		f.err = "write a description first, then ctrl+g"
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		f.err = err.Error()
		return nil
	}
	bin, err := exec.LookPath("claude")
	if err != nil {
		f.err = "claude is not on PATH"
		return nil
	}
	timeout := time.Duration(cfg.Draft.Timeout)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	f.cancel, f.err, f.model = cancel, "", cfg.Draft.Model
	f.spin = spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(lipgloss.NewStyle().Foreground(colorSel)))
	return tea.Batch(f.spin.Tick, func() tea.Msg {
		defer cancel()
		d, err := draft.Run(ctx, bin, cfg.Draft.Model, desc)
		if errors.Is(err, draft.ErrTimeout) {
			err = fmt.Errorf("draft timed out after %s", timeout)
		} else if err != nil {
			err = fmt.Errorf("draft failed: %w", err)
		}
		return draftMsg{form: f, d: d, err: err}
	})
}

func (f *form) drafting() bool { return f.cancel != nil }

// stopDraft abandons a call in flight.
func (f *form) stopDraft() {
	if f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

// applyDraft fills name and ticket and keeps the todos and notes for the
// preview; a failure leaves every field as it was.
func (f *form) applyDraft(msg draftMsg) {
	f.cancel = nil
	if msg.err != nil {
		f.err = msg.err.Error()
		return
	}
	f.draft = &msg.d
	f.set("name", msg.d.Name)
	f.set("ticket", msg.d.Ticket)
}

// draftView is the spinner while drafting, else the drafted todos and notes.
func (f *form) draftView(width int) []string {
	if f.drafting() {
		return []string{f.spin.View() + faint.Render(" drafting with "+f.model+"…"), ""}
	}
	d := f.draft
	if d == nil || (len(d.Todos) == 0 && d.Notes == "") {
		return nil
	}
	lines := []string{bold.Render("Drafted") + faint.Render(" · written to the task file on save")}
	for _, t := range d.Todos {
		lines = append(lines, strings.Split(ansi.Wordwrap(" ☐ "+t, width, ""), "\n")...)
	}
	if d.Notes != "" {
		for _, l := range strings.Split(ansi.Wordwrap(d.Notes, width-1, ""), "\n") {
			lines = append(lines, faint.Render(" "+l))
		}
	}
	return append(lines, "")
}

// writeDraft adds a saved draft's todos and notes to the task's file.
func writeDraft(slug string, d *draft.Draft) error {
	if d == nil {
		return nil
	}
	path := todo.TaskPath(slug)
	for _, t := range d.Todos {
		if err := todo.Add(path, t); err != nil {
			return err
		}
	}
	return todo.AddNotes(path, d.Notes)
}
