package board

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// command is one board action. Its key (if any) and `:name` run the same
// code, and `?` lists them from this table, so the three can't drift.
type command struct {
	name  string
	alias string
	args  string // usage, e.g. "[name]"
	key   string // board key bound to it ("" = `:` only), as tea.Key.String()
	help  string
	task  bool // acts on the selected task card
	run   func(m *model, c Card, arg string) tea.Cmd
}

// commands is filled in init: its run funcs reach back into it (help,
// unknown-command errors), which a plain initializer would reject as a cycle.
var commands []command

func init() {
	commands = []command{
		{name: "jump", key: "enter", help: "jump to the task's window; revive a dormant task",
			run: func(m *model, _ Card, _ string) tea.Cmd { return m.jumpSelected() }},
		{name: "todos", key: "tab", task: true, help: "focus the task's todo list",
			run: func(m *model, _ Card, _ string) tea.Cmd { m.focus = onTodos; return nil }},
		{name: "new", key: "n", args: "[name]", help: "new task: name, ticket, cwd → new window; ctrl+g drafts it from a description",
			run: func(m *model, _ Card, arg string) tea.Cmd {
				m.form = newCreateForm(m.opts.DefaultCwd)
				if arg != "" {
					m.form.set("name", arg)
					return m.form.focusField(m.form.index("name"))
				}
				return m.form.focusField(0)
			}},
		{name: "adopt", key: "a", args: "[name]", help: "adopt the selected Untracked window as a task",
			run: func(m *model, _ Card, arg string) tea.Cmd {
				u, ok := m.selectedUntracked()
				if !ok {
					m.status = "select an untracked window to adopt (↓ to the Untracked row)"
					return nil
				}
				m.form = newAdoptForm(u.Window)
				if arg != "" {
					m.form.fields[0].input.SetValue(arg)
				}
				return m.form.focusField(0)
			}},
		{name: "launch", key: "l", task: true, help: "launch Claude on the task: edit the prompt, confirm, new pane",
			run: func(m *model, c Card, _ string) tea.Cmd {
				return m.openLaunch(c, c.Task.Name, taskPrompt(c.Task, c.Todos))
			}},
		{name: "done", key: "d", task: true, help: "mark done: close its window, archive its notes",
			run: func(m *model, _ Card, _ string) tea.Cmd { return m.markDone() }},
		{name: "delete", task: true, help: "delete the task: its windows, notes and history (asks first)",
			run: func(m *model, c Card, _ string) tea.Cmd {
				m.confirm = &confirm{yes: m.deleteTask(c.Task), prompt: fmt.Sprintf(
					"Delete %s? Closes its windows, deletes its notes and history. y/n", c.Task.Name)}
				return nil
			}},
		{name: "edit", key: "e", task: true, help: "open the task's notes file in $VISUAL / $EDITOR",
			run: func(_ *model, c Card, _ string) tea.Cmd { return edit(target(c)) }},
		{name: "open", key: "o", task: true, help: "open the task's ticket in the browser",
			run: func(m *model, _ Card, _ string) tea.Cmd { return m.openTicket() }},
		{name: "branch", alias: "b", key: "b", args: "[name]", task: true,
			help: "set the stack branch (any in the stack); no name → picker",
			run: func(m *model, c Card, arg string) tea.Cmd {
				if arg == "" {
					return m.openBranchPicker(c.Task)
				}
				t := c.Task
				delete(m.offered, t.ID) // a new stack gets its own offer
				return act(func() (string, error) {
					return t.Name + " → " + arg, m.st.SetBranch(t.ID, arg)
				})
			}},
		{name: "label", alias: "l", args: "<text>", task: true, help: "add a manual label",
			run: func(m *model, c Card, arg string) tea.Cmd {
				if arg == "" {
					m.status = "usage: :label <text>"
					return nil
				}
				return act(func() (string, error) {
					return "labelled " + c.Task.Name + " " + arg, m.st.AddLabel(c.Task.ID, arg)
				})
			}},
		{name: "unlabel", args: "<text>", task: true, help: "remove a label",
			run: func(m *model, c Card, arg string) tea.Cmd {
				if arg == "" {
					m.status = "usage: :unlabel <text>"
					return nil
				}
				return act(func() (string, error) {
					ok, err := m.st.RemoveLabel(c.Task.ID, arg)
					if err == nil && !ok {
						return "", fmt.Errorf("%s has no label %q", c.Task.Name, arg)
					}
					return "removed label " + arg, err
				})
			}},
		{name: "ticket", args: "[id]", task: true, help: "set the ticket; no id clears it",
			run: func(m *model, c Card, arg string) tea.Cmd {
				t := c.Task
				return act(func() (string, error) {
					if arg == "" {
						return "cleared " + t.Name + "'s ticket", m.st.SetTicket(t.ID, "")
					}
					return t.Name + " ticket → " + arg, m.st.SetTicket(t.ID, arg)
				})
			}},
		{name: "context", args: "on|off", task: true, help: "inject task context into new Claude sessions, or not",
			run: func(m *model, c Card, arg string) tea.Cmd {
				t := c.Task
				var on bool
				switch arg {
				case "on":
					on = true
				case "off":
				default:
					state := "on"
					if !t.ContextEnabled {
						state = "off"
					}
					m.status = fmt.Sprintf("%s's context is %s; :context on|off", t.Name, state)
					return nil
				}
				return act(func() (string, error) {
					return fmt.Sprintf("%s context %s (new Claude sessions)", t.Name, arg), m.st.SetContext(t.ID, on)
				})
			}},
		{name: "refresh", key: "r", help: "refresh PR / CI state from GitHub now (else every 2 min)",
			run: func(m *model, _ Card, _ string) tea.Cmd {
				if !m.snap.HasStacks {
					m.status = "no task has a branch yet (b to pick one)"
					return nil
				}
				m.status = "refreshing PRs from GitHub…"
				return m.pollGitHub(true)
			}},
		{name: "inbox", key: "i", help: "inbox view",
			run: func(m *model, _ Card, _ string) tea.Cmd { m.inbox = true; return nil }},
		{name: "filter", key: "/", args: "[text]", help: "filter by name, ticket or label (finds old Done tasks too)",
			run: func(m *model, _ Card, arg string) tea.Cmd {
				if arg != "" {
					return m.setFilter(arg)
				}
				return m.openFilter()
			}},
		{name: "command", key: ":", help: "command line",
			run: func(m *model, _ Card, _ string) tea.Cmd {
				in := textinput.New()
				in.Prompt = ":"
				in.SetWidth(60)
				m.cmd = &cmdline{input: in}
				return m.cmd.input.Focus()
			}},
		{name: "help", key: "?", help: "every key and command",
			run: func(m *model, _ Card, _ string) tea.Cmd { m.helpOpen, m.helpTop = true, 0; return nil }},
		{name: "quit", alias: "q", key: "q", help: "quit",
			run: func(*model, Card, string) tea.Cmd { return tea.Quit }},
	}
}

func act(f func() (string, error)) tea.Cmd {
	return func() tea.Msg {
		s, err := f()
		return actionMsg{status: s, err: err}
	}
}

// exec runs c, against the selected card if it needs one.
func (m *model) exec(c command, arg string) tea.Cmd {
	var card Card
	if c.task {
		var ok bool
		if card, ok = m.selectedCard(); !ok {
			m.status = "select a task first"
			return nil
		}
	}
	return c.run(m, card, arg)
}

// keyCommand is the command bound to a board key.
func keyCommand(key string) (command, bool) {
	for _, c := range commands {
		if c.key != "" && c.key == key {
			return c, true
		}
	}
	return command{}, false
}

func findCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name || (c.alias != "" && c.alias == name) {
			return c, true
		}
	}
	return command{}, false
}

// runCommand runs a `:` command line.
func (m *model) runCommand(line string) tea.Cmd {
	name, arg, _ := strings.Cut(strings.TrimSpace(line), " ")
	if name == "" {
		return nil
	}
	c, ok := findCommand(name)
	if !ok {
		m.status = fmt.Sprintf("unknown command %q (? lists them)", name)
		return nil
	}
	return m.exec(c, strings.TrimSpace(arg))
}

// cmdline is the `:` prompt.
type cmdline struct {
	input textinput.Model
}

// commandHint lists the commands matching the name typed so far, with
// what it does once only one is left.
func commandHint(typed string) string {
	name, _, _ := strings.Cut(strings.TrimLeft(typed, " "), " ")
	var ms []string
	var last command
	for _, c := range commands {
		if c.name != "command" && (strings.HasPrefix(c.name, name) || c.alias == name) {
			last = c
			ms = append(ms, strings.TrimSpace(c.name+" "+c.args))
		}
	}
	switch len(ms) {
	case 0:
		return "no such command · esc cancel"
	case 1:
		return ms[0] + " — " + last.help + " · enter run · esc cancel"
	}
	return strings.Join(ms, " · ") + " · enter run · esc cancel"
}
