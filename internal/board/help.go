package board

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Keys that aren't commands: they move a cursor or only exist in a view.
var (
	navKeys = [][2]string{
		{"← →", "move between columns (or along the Untracked row)"},
		{"↑ ↓", "move between cards; ↓ past the last card → Untracked row"},
		{"esc", "clear the filter; else close the popup"},
		{"ctrl+c", "quit"},
	}
	todoKeys = [][2]string{
		{"↑ ↓", "move"}, {"space", "toggle the todo"}, {"l", "launch Claude on the todo"}, {"e", "edit the notes file"},
		{"enter", "jump to the task"}, {"tab / esc", "back to the cards"},
	}
	inboxKeys = [][2]string{
		{"↑ ↓", "move"}, {"space", "toggle the todo"}, {"m", "move the todo to a task"},
		{"l", "promote the todo to a new task, then launch Claude on it"},
		{"e", "edit the inbox"}, {"i / esc", "back to the board"},
	}
	promptKeys = [][2]string{
		{"/ prompt", "type to narrow · enter keeps the filter · esc clears it"},
		{": prompt", "enter runs · esc cancels"},
		{"launch", "enter next · alt+enter newline · esc cancel; then y launches · n edits again"},
		{"forms", "tab / ↓ next field · shift+tab / ↑ previous · enter save · esc cancel"},
		{"new task", "ctrl+g drafts name, ticket, todos and notes from the description (nothing saved until enter)"},
		{"offers", "y confirms · n / esc cancels (a landed-stack offer: any key dismisses)"},
	}
)

// helpLines is the whole help text, one entry per line.
func helpLines() []string {
	section := func(title string, rows [][2]string) []string {
		out := []string{bold.Render(title)}
		for _, r := range rows {
			out = append(out, fmt.Sprintf("  %-10s %s", r[0], r[1]))
		}
		return append(out, "")
	}
	// A keyed command shows once, under its key, with its `:` form.
	var keys, cmds [][2]string
	for _, c := range commands {
		name := ":" + c.name
		if c.alias != "" {
			name += " (:" + c.alias + ")"
		}
		if c.args != "" {
			name += " " + c.args
		}
		if c.key != "" {
			keys = append(keys, [2]string{c.key, c.help + faint.Render("  "+name)})
		} else {
			cmds = append(cmds, [2]string{name, c.help})
		}
	}
	var out []string
	out = append(out, section("Board", append(append([][2]string{}, navKeys...), keys...))...)
	out = append(out, section("Todo list (tab)", todoKeys)...)
	out = append(out, section("Inbox (i)", inboxKeys)...)
	out = append(out, section("Prompts", promptKeys)...)
	// Commands get a wider name column: their args make them longer.
	out = append(out, bold.Render("Commands without a key")+faint.Render("  (any key above also runs as its :command)"))
	for _, r := range cmds {
		out = append(out, fmt.Sprintf("  %-24s %s", r[0], r[1]))
	}
	return out
}

func (m *model) helpRows() int { return max(1, m.height-6) } // header + blank, then blank, status, footer

func (m *model) updateHelp(k tea.KeyPressMsg) tea.Cmd {
	last := max(0, len(helpLines())-m.helpRows())
	switch k.String() {
	case "?", "esc", "q":
		m.helpOpen = false
	case "ctrl+c":
		return tea.Quit
	case "up":
		m.helpTop = max(m.helpTop-1, 0)
	case "down":
		m.helpTop = min(m.helpTop+1, last)
	case "pgup":
		m.helpTop = max(m.helpTop-m.helpRows(), 0)
	case "pgdown", "space", " ":
		m.helpTop = min(m.helpTop+m.helpRows(), last)
	}
	return nil
}

func (m *model) helpView() string {
	lines := helpLines()
	rows := m.helpRows()
	top := min(m.helpTop, max(0, len(lines)-rows))
	shown := lines[top:min(len(lines), top+rows)]
	for i := range shown {
		shown[i] = ansi.Truncate(shown[i], m.width, "…")
	}
	return strings.Join(shown, "\n") + "\n"
}
