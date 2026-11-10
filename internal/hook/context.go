package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/LeikIsNRF/tt/internal/stack"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// Caps that keep the injected block short. Each includes its "… more" line.
const (
	maxStackLines = 5
	maxTodoLines  = 5
)

// taskContext is what a new Claude session is told about its task.
type taskContext struct {
	Task  store.Task
	Notes string // path of the task's markdown file
	Stack []stackLine
	// PRsAge is how old the cached PR state is; negative: never fetched.
	PRsAge time.Duration
	Todos  []string // open todos, in `tt todo list` order
}

type stackLine struct {
	Branch string
	PR     *stack.PR
}

// loadContext gathers a task's context from the DB and its markdown file. It
// never calls gh or graphite: the stack and PR state are what the board last
// resolved and cached. ok is false when nothing should be injected.
func loadContext(st *store.Store, slug string, now time.Time) (c taskContext, ok bool, err error) {
	t, found, err := st.TaskBySlug(slug)
	if err != nil || !found || !t.ContextEnabled || t.DoneAt.Valid {
		return c, false, err
	}
	c = taskContext{Task: t, Notes: todo.TaskPath(slug), PRsAge: -1}

	branches, err := st.TaskStack(t.ID)
	if err != nil {
		return c, false, err
	}
	if len(branches) == 0 && t.Branch != "" {
		branches = []string{t.Branch} // the board hasn't resolved it yet
	}
	if len(branches) > 0 {
		prs, fetchedAt, err := st.PRs()
		if err != nil {
			return c, false, err
		}
		var repoPRs map[string]stack.PR
		if r, err := stack.FindRepo(t.Cwd); err == nil {
			repoPRs = prs[r.GitHub]
		}
		if !fetchedAt.IsZero() {
			c.PRsAge = now.Sub(fetchedAt)
		}
		for _, b := range branches {
			l := stackLine{Branch: b}
			if p, ok := repoPRs[b]; ok {
				l.PR = &p
			}
			c.Stack = append(c.Stack, l)
		}
	}

	ts, err := todo.Load(c.Notes)
	if err != nil {
		return c, false, err
	}
	for _, td := range todo.Open(ts) {
		c.Todos = append(c.Todos, td.Text)
	}
	return c, true, nil
}

// format renders the context as a short plain-text block.
func (c taskContext) format() string {
	var b strings.Builder
	line := func(f string, a ...any) { fmt.Fprintf(&b, f+"\n", a...) }

	head := fmt.Sprintf("tt task: %s (slug %s)", c.Task.Name, c.Task.Slug)
	if c.Task.Ticket != "" {
		head += " · ticket " + c.Task.Ticket
	}
	line("%s", head)
	line("Notes and todos: %s", c.Notes)

	if len(c.Stack) > 0 {
		age := "no PR data yet"
		if c.PRsAge >= 0 {
			age = "PR state " + ago(c.PRsAge)
		}
		line("Graphite stack, bottom→top (%s):", age)
		shown := c.Stack
		if len(shown) > maxStackLines {
			// Keep the top of the stack: the bottom is usually what merged.
			hidden := len(shown) - (maxStackLines - 1)
			line("  … %d lower branches", hidden)
			shown = shown[hidden:]
		}
		for _, s := range shown {
			pr := ""
			if s.PR != nil {
				pr = fmt.Sprintf(" #%d", s.PR.Number)
			}
			line("  %s %s%s %s", stack.GlyphFor(s.PR), s.Branch, pr, stack.Describe(s.PR))
		}
	}

	if len(c.Todos) == 0 {
		line("Open todos: none")
	} else {
		line("Open todos (%d):", len(c.Todos))
		shown := c.Todos
		if len(shown) > maxTodoLines {
			shown = shown[:maxTodoLines-1]
		}
		for i, t := range shown {
			line("  %d. %s", i+1, t)
		}
		if n := len(c.Todos) - len(shown); n > 0 {
			line("  … %d more (`tt todo list`)", n)
		}
	}
	line("Track work with `tt todo add|done`; the tt skill has the details.")
	return strings.TrimSuffix(b.String(), "\n")
}

func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just fetched"
	case d < time.Hour:
		return fmt.Sprintf("%dm old", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh old", int(d.Hours()))
	}
	return fmt.Sprintf("%dd old", int(d.Hours()/24))
}

// writeContext emits the SessionStart hook output that injects text into
// Claude's context.
func writeContext(w io.Writer, text string) error {
	out := map[string]any{"hookSpecificOutput": map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": text,
	}}
	return json.NewEncoder(w).Encode(out)
}
