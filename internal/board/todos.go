package board

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/LeikIsNRF/tt/internal/paths"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// detailRows is how many todo lines the detail pane shows, so the columns
// above it don't jump when the selection changes.
const detailRows = 5

// loadTodos reads every shown task's file and the inbox. The files are
// re-read on every refresh, so hand edits show up within a tick.
func loadTodos(snap *Snapshot) error {
	for col := range snap.Columns {
		for i := range snap.Columns[col] {
			c := &snap.Columns[col][i]
			ts, err := todo.Load(target(*c).Path())
			if err != nil {
				return err
			}
			c.Todos = ts
		}
	}
	ts, err := todo.Load(paths.Inbox())
	snap.Inbox = ts
	return err
}

// target is a card's markdown file: archived once the task is done.
func target(c Card) todo.Target {
	return todo.Target{Slug: c.Task.Slug, Done: c.Task.DoneAt.Valid}
}

// picker chooses the task an inbox todo moves to.
type picker struct {
	todo  todo.Todo
	tasks []Card
	idx   int
}

func (m *model) openTasks() []Card {
	var cs []Card
	for col := range ColDone {
		cs = append(cs, m.snap.Columns[col]...)
	}
	return cs
}

// updateTodos handles keys while the selected card's todo list has focus.
func (m *model) updateTodos(k tea.KeyPressMsg) tea.Cmd {
	c, ok := m.selectedCard()
	if !ok {
		m.focus = onBoard
		return nil
	}
	switch k.String() {
	case "tab", "esc":
		m.focus = onBoard
	case "up":
		m.tIdx = max(m.tIdx-1, 0)
	case "down":
		m.tIdx = clamp(m.tIdx+1, len(c.Todos))
	case "space", " ":
		if len(c.Todos) > 0 {
			return toggle(target(c), c.Todos[m.tIdx])
		}
	case "e":
		return edit(target(c))
	case "l":
		if len(c.Todos) == 0 {
			return nil
		}
		td := c.Todos[m.tIdx]
		if td.Done {
			m.status = "that todo is already done"
			return nil
		}
		return m.openLaunch(c, "“"+td.Text+"”", todoPrompt(c.Task.Slug, td))
	case "enter":
		return m.jumpSelected()
	case "q", "ctrl+c":
		return tea.Quit
	}
	return nil
}

// updateInbox handles keys in the inbox view.
func (m *model) updateInbox(k tea.KeyPressMsg) tea.Cmd {
	if p := m.picker; p != nil {
		switch k.String() {
		case "esc":
			m.picker = nil
		case "up":
			p.idx = max(p.idx-1, 0)
		case "down":
			p.idx = clamp(p.idx+1, len(p.tasks))
		case "enter":
			m.picker = nil
			if len(p.tasks) > 0 {
				return move(p.todo, p.tasks[p.idx].Task.Slug)
			}
		case "ctrl+c":
			return tea.Quit
		}
		return nil
	}
	switch k.String() {
	case "i", "esc":
		m.inbox = false
	case "up":
		m.iIdx = max(m.iIdx-1, 0)
	case "down":
		m.iIdx = clamp(m.iIdx+1, len(m.snap.Inbox))
	case "space", " ":
		if len(m.snap.Inbox) > 0 {
			return toggle(todo.Inbox, m.snap.Inbox[m.iIdx])
		}
	case "m":
		if len(m.snap.Inbox) == 0 {
			return nil
		}
		tasks := m.openTasks()
		if len(tasks) == 0 {
			m.status = "no open task to move it to"
			return nil
		}
		m.picker = &picker{todo: m.snap.Inbox[m.iIdx], tasks: tasks}
	case "l":
		// Promote: a new task named after the todo, which moves into it.
		if len(m.snap.Inbox) == 0 {
			return nil
		}
		td := m.snap.Inbox[m.iIdx]
		if td.Done {
			m.status = "that todo is already done"
			return nil
		}
		m.form = newCreateForm(m.opts.DefaultCwd)
		m.form.title = "New task from “" + td.Text + "”, then launch Claude on it"
		m.form.promote = &td
		// The todo is the description too, so ctrl+g drafts from it.
		m.form.set("describe", td.Text)
		m.form.set("name", td.Text)
		return m.form.focusField(0)
	case "e":
		return edit(todo.Inbox)
	case "q", "ctrl+c":
		return tea.Quit
	}
	return nil
}

func toggle(t todo.Target, td todo.Todo) tea.Cmd {
	return func() tea.Msg {
		return actionMsg{err: todo.SetDone(t.Path(), td, !td.Done)}
	}
}

func move(td todo.Todo, slug string) tea.Cmd {
	return func() tea.Msg {
		dst := todo.Target{Slug: slug}
		if err := dst.Ensure(); err != nil {
			return actionMsg{err: err}
		}
		if err := todo.Move(paths.Inbox(), td, dst.Path()); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{status: "moved “" + td.Text + "” to " + slug}
	}
}

// edit suspends the board and opens t's file in $VISUAL / $EDITOR (vi if
// neither is set); the board resumes and reloads when the editor exits.
func edit(t todo.Target) tea.Cmd {
	if err := t.Ensure(); err != nil {
		return func() tea.Msg { return actionMsg{err: err} }
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	// Through sh so an EDITOR with arguments ("code -w") works.
	c := exec.Command("sh", "-c", editor+` "$1"`, "sh", t.Path())
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if err != nil {
			return actionMsg{err: fmt.Errorf("editor: %w", err)}
		}
		return actionMsg{}
	})
}

// detailView lists the selected task's todos under the columns.
func (m *model) detailView() string {
	c, ok := m.selectedCard()
	focused := m.focus == onTodos
	title := bold
	if focused {
		title = title.Foreground(colorSel)
	}
	if !ok {
		return title.Render("Todos") + "\n" + strings.Repeat("\n", detailRows)
	}
	// Todos on the left, the stack on the right.
	leftW := m.width / 2
	rightW := m.width - leftW - 1
	head := title.Render(c.Task.Name) + faint.Render(fmt.Sprintf(" · %d open · %s",
		len(todo.Open(c.Todos)), tildePath(target(c).Path())))
	sel := -1
	if focused {
		sel = m.tIdx
	}
	left := ansi.Truncate(head, leftW-1, "…") + "\n" + strings.TrimSuffix(todoList(c.Todos, sel, detailRows, leftW), "\n")
	left = lipgloss.NewStyle().Width(leftW).Render(left)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", stackView(c, detailRows, rightW)) + "\n"
}

// todoList renders rows lines of todos, scrolled to keep sel in view
// (sel < 0: no cursor), padded so its height never changes.
func todoList(ts []todo.Todo, sel, rows, width int) string {
	var lines []string
	if len(ts) == 0 {
		lines = append(lines, faint.Render("  no todos"))
	}
	start := 0
	if sel >= rows {
		start = sel - rows + 1
	}
	end := min(len(ts), start+rows)
	for i := start; i < end; i++ {
		lines = append(lines, todoLine(ts[i], i == sel, width))
	}
	if end < len(ts) && len(lines) == rows {
		lines[rows-1] = faint.Render(fmt.Sprintf("  ↓ %d more", len(ts)-end+1))
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n") + "\n"
}

func todoLine(t todo.Todo, sel bool, width int) string {
	box, text := "☐", t.Text
	sty := lipgloss.NewStyle()
	if t.Done {
		box, sty = "☑", faint
	}
	line := ansi.Truncate(" "+box+" "+text, width-1, "…")
	if sel {
		return lipgloss.NewStyle().Reverse(true).Render(line)
	}
	return sty.Render(line)
}

func (m *model) inboxView() string {
	if p := m.picker; p != nil {
		lines := []string{bold.Render("Move “" + p.todo.Text + "” to"), ""}
		for i, c := range p.tasks {
			name := "  " + c.Task.Name
			if i == p.idx {
				name = lipgloss.NewStyle().Foreground(colorSel).Render("› " + c.Task.Name)
			}
			if c.Task.Slug != c.Task.Name {
				name += faint.Render("  " + c.Task.Slug)
			}
			lines = append(lines, name)
		}
		return strings.Join(lines, "\n") + "\n"
	}
	head := bold.Foreground(colorSel).Render("Inbox") + faint.Render(fmt.Sprintf(" · %d open · %s",
		len(todo.Open(m.snap.Inbox)), tildePath(paths.Inbox())))
	rows := max(1, m.height-7)
	return head + "\n\n" + todoList(m.snap.Inbox, m.iIdx, min(rows, max(1, len(m.snap.Inbox))), m.width)
}

func tildePath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, h+"/") {
		return "~" + p[len(h):]
	}
	return p
}
