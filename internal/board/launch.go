package board

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// launch is the `l` flow: edit a prompt, confirm the command, then start
// Claude in a new pane of the task's window. The session that starts is an
// ordinary one: TT_TASK links it, and its hooks move it through the columns.
type launch struct {
	task   store.Task
	window string // "" = dormant: revive a window first
	what   string // what it launches on, for the title
	prompt textarea.Model
	// confirming: showing the command; else editing the prompt.
	confirming bool
	claude     string // resolved claude binary, set on confirm
	warn       string
}

// taskPrompt is the prompt `l` on a card starts from.
func taskPrompt(t store.Task, todos []todo.Todo) string {
	head := fmt.Sprintf("Work on the task %q", t.Name)
	if t.Ticket != "" {
		head += " (" + t.Ticket + ")"
	}
	lines := []string{head + "."}
	if open := todo.Open(todos); len(open) > 0 {
		lines = append(lines, "", "Open todos:")
		for _, td := range open {
			lines = append(lines, "- "+td.Text)
		}
	}
	return strings.Join(lines, "\n")
}

// todoPrompt is the prompt `l` on a todo starts from. It ends by telling
// Claude how to tick the todo off.
func todoPrompt(slug string, td todo.Todo) string {
	return td.Text + "\n\nWhen it is done, tick it off: tt todo done --task " + slug + " " + dquote(td.Text)
}

// dquote double-quotes s for sh. The confirm screen shows the whole prompt
// single-quoted, where a single quote in here would turn into an escape.
func dquote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "$", `\$`, "`", "\\`").Replace(s) + `"`
}

// sharedCheckout reports whether a session already working in cwd would
// have its branch switched under it by another agent.
func sharedCheckout(sessions []store.LiveSession, cwd string) bool {
	cwd = filepath.Clean(cwd)
	for _, s := range sessions {
		if s.State == store.Working && s.Cwd != "" && filepath.Clean(s.Cwd) == cwd {
			return true
		}
	}
	return false
}

// openLaunch starts the flow for card c with prompt.
func (m *model) openLaunch(c Card, what, prompt string) tea.Cmd {
	if c.Task.DoneAt.Valid {
		m.status = c.Task.Name + " is done"
		return nil
	}
	ta := textarea.New()
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("alt+enter", "shift+enter", "ctrl+j"))
	ta.SetWidth(max(20, min(m.width-6, 100)))
	ta.SetHeight(max(3, min(10, m.height-16)))
	ta.SetValue(prompt)
	l := &launch{task: c.Task, what: what, prompt: ta}
	if c.Window != nil {
		l.window = c.Window.ID
	}
	m.launch = l
	return m.launch.prompt.Focus()
}

func (m *model) updateLaunch(k tea.KeyPressMsg) tea.Cmd {
	l := m.launch
	if l.confirming {
		switch k.String() {
		case "y", "Y":
			m.launch = nil
			return m.startLaunch(l)
		case "n", "N", "esc":
			l.confirming = false
			return l.prompt.Focus()
		case "ctrl+c":
			m.launch = nil
		}
		return nil
	}
	switch k.String() {
	case "esc", "ctrl+c":
		m.launch = nil
		return nil
	case "enter":
		p := strings.TrimSpace(l.prompt.Value())
		if p == "" {
			return nil
		}
		if strings.HasPrefix(p, "-") {
			// Claude would read it as a flag, perhaps a permission bypass.
			l.warn = "the prompt can't start with -"
			return nil
		}
		claude, err := exec.LookPath("claude")
		if err != nil {
			m.launch = nil
			m.err = errors.New("claude is not on PATH")
			return nil
		}
		l.claude, l.warn = claude, ""
		sessions, err := m.st.LiveSessions()
		if err != nil {
			l.warn = "couldn't check for other agents: " + err.Error()
		} else if sharedCheckout(sessions, l.task.Cwd) {
			l.warn = "⚠ another agent is working in this checkout"
		}
		l.confirming = true
		l.prompt.Blur()
		return nil
	}
	var c tea.Cmd
	l.prompt, c = l.prompt.Update(k)
	return c
}

// startLaunch splits a pane running Claude into the task's window (reviving
// one first if the task is dormant) and jumps to it. Never with a
// permission-bypass flag: prompts turn the tab red like any session's.
func (m *model) startLaunch(l *launch) tea.Cmd {
	t, window, prompt := l.task, l.window, strings.TrimSpace(l.prompt.Value())
	return func() tea.Msg {
		if fi, err := os.Stat(t.Cwd); err != nil || !fi.IsDir() {
			return actionMsg{err: fmt.Errorf("can't launch in %s: cwd %q is not a directory", t.Slug, t.Cwd)}
		}
		if window == "" {
			w, err := tmux.NewWindow(t.Slug, t.Cwd)
			if err != nil {
				return actionMsg{err: err}
			}
			window = w
		}
		if err := tmux.Split(window, t.Cwd, t.Slug, l.claude, prompt); err != nil {
			return actionMsg{err: err, sel: t.Slug}
		}
		status := "launched Claude in " + t.Slug
		if err := tmux.Jump(window); err != nil {
			return actionMsg{status: status, err: err, sel: t.Slug}
		}
		return actionMsg{status: status, jumped: true, sel: t.Slug}
	}
}

// promotedMsg: an inbox todo became a task; launch on it there.
type promotedMsg struct {
	card Card
	todo todo.Todo
}

func (m *model) launchView() string {
	l := m.launch
	title := "Launch Claude on " + l.what
	lines := []string{bold.Render(title), ""}
	if !l.confirming {
		lines = append(lines, l.prompt.View(), "")
		if l.warn != "" {
			lines = append(lines, errSty.Render(l.warn))
		}
		lines = append(lines, faint.Render("enter next · alt+enter newline · esc cancel"))
	} else {
		w := max(20, min(m.width-6, 100))
		where := "new pane in " + l.task.Slug + "'s window"
		if l.window == "" {
			where = "revive " + l.task.Slug + "'s window, then a new pane in it"
		}
		lines = append(lines,
			faint.Render(where+" · cwd "+tildePath(l.task.Cwd)+" · TT_TASK="+l.task.Slug), "",
			lipgloss.NewStyle().Width(w).Render(shellQuote(l.claude)+" "+shellQuote(strings.TrimSpace(l.prompt.Value()))), "")
		if l.warn != "" {
			lines = append(lines, stateColors[store.NeedsYou].Bold(true).Render(l.warn), "")
		}
		lines = append(lines, faint.Render("y launch · n/esc edit the prompt · ctrl+c cancel"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorSel).
		Padding(0, 1).Render(strings.Join(lines, "\n")) + "\n"
}
