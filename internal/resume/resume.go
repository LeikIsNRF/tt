// Package resume implements `tt resume`, the picker a revived task window
// opens with: resume one of the task's past Claude sessions, start a new one,
// or drop to the shell. It's typed into the window's interactive shell, so it
// execs into Claude and the shell is back when Claude exits.
package resume

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/LeikIsNRF/tt/internal/paths"
	"github.com/LeikIsNRF/tt/internal/store"
)

// Run shows the picker for the task with slug and carries out the choice.
func Run(slug string) error {
	st, err := store.Open(paths.DB())
	if err != nil {
		return err
	}
	task, ok, err := st.TaskBySlug(slug)
	if err != nil {
		st.Close()
		return err
	}
	if !ok {
		st.Close()
		return fmt.Errorf("no task %q", slug)
	}
	sessions, err := st.TaskSessions(task.ID)
	st.Close()
	if err != nil {
		return err
	}
	m := &model{task: task, rows: Rows(sessions, time.Now(), exists)}
	if _, err := tea.NewProgram(m).Run(); err != nil {
		return err
	}
	if m.chosen < 0 {
		return nil // esc / ctrl+c: just the shell
	}
	return run(m.rows[m.chosen])
}

// Row is one picker choice. A Row with no Session is [new session] or
// [just a shell].
type Row struct {
	Session *store.Session
	New     bool // [new session]
	Label   string
	Detail  string
}

// Rows lists the sessions that can be resumed, newest first, then
// [new session] and [just a shell]. A session needs a prompt (Claude keeps
// no conversation without one) and, if its transcript path is known, that
// file must still exist (Claude prunes old ones).
func Rows(sessions []store.Session, now time.Time, exists func(string) bool) []Row {
	var rows []Row
	for i := range sessions {
		s := &sessions[i]
		if s.LastPrompt == "" || (s.TranscriptPath != "" && !exists(s.TranscriptPath)) {
			continue
		}
		rows = append(rows, Row{
			Session: s,
			Label:   strings.Join(strings.Fields(s.LastPrompt), " "),
			Detail:  age(now.Sub(time.Unix(s.UpdatedAt, 0))) + " · " + stateLabel(*s),
		})
	}
	return append(rows,
		Row{New: true, Label: "[new session]"},
		Row{Label: "[just a shell]"})
}

func stateLabel(s store.Session) string {
	names := map[store.State]string{
		store.NeedsYou: "needs you", store.YourTurn: "your turn",
		store.Working: "working", store.Idle: "idle", store.Ended: "ended",
	}
	switch {
	case !s.EndedAt.Valid:
		return "still running · " + names[s.State]
	case s.State == store.Ended:
		return "ended"
	}
	return "interrupted · was " + names[s.State]
}

func age(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// run replaces tt with the chosen command. Claude looks sessions up by
// project directory, so a resume runs from the session's own cwd.
func run(r Row) error {
	if r.Session == nil && !r.New {
		return nil // [just a shell]
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		return errors.New("claude is not on PATH")
	}
	argv := []string{"claude"}
	if r.Session != nil {
		argv = append(argv, "--resume", r.Session.ID)
		if fi, err := os.Stat(r.Session.Cwd); err == nil && fi.IsDir() {
			if err := os.Chdir(r.Session.Cwd); err != nil {
				return err
			}
		}
	}
	return syscall.Exec(claude, argv, os.Environ())
}

var (
	sel   = lipgloss.NewStyle().Foreground(lipgloss.Color("212"))
	faint = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	bold  = lipgloss.NewStyle().Bold(true)
)

type model struct {
	task   store.Task
	rows   []Row
	idx    int
	chosen int
	width  int
	done   bool
}

func (m *model) Init() tea.Cmd { return nil }

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyPressMsg:
		switch msg.String() {
		case "up":
			m.idx = max(m.idx-1, 0)
		case "down":
			m.idx = min(m.idx+1, len(m.rows)-1)
		case "enter":
			m.chosen, m.done = m.idx, true
			return m, tea.Quit
		case "esc", "q", "ctrl+c":
			m.chosen, m.done = -1, true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m *model) View() tea.View {
	var v tea.View
	if m.done {
		return v // leave nothing behind for Claude or the shell
	}
	w := m.width
	if w == 0 {
		w = 80
	}
	head := bold.Render("Revive " + m.task.Name)
	if m.task.Slug != m.task.Name {
		head += faint.Render("  " + m.task.Slug)
	}
	lines := []string{head, ""}
	if len(m.rows) == 2 {
		lines = append(lines, faint.Render("  no past sessions to resume"), "")
	}
	for i, r := range m.rows {
		cursor, sty := "  ", lipgloss.NewStyle()
		if i == m.idx {
			cursor, sty = "› ", sel
		}
		detail := ""
		if r.Detail != "" {
			detail = "  " + r.Detail
		}
		label := ansi.Truncate(r.Label, max(10, w-lipgloss.Width(cursor)-lipgloss.Width(detail)-1), "…")
		lines = append(lines, sty.Render(cursor+label)+faint.Render(detail))
	}
	lines = append(lines, "", faint.Render("↑↓ choose · enter run · esc shell"))
	v.SetContent(strings.Join(lines, "\n") + "\n")
	return v
}
