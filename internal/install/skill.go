package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/LeikIsNRF/tt/internal/paths"
)

// skillMarker identifies a SKILL.md tt wrote, so uninstall never deletes a
// skill of the same name that someone else made.
const skillMarker = "<!-- written by `tt install`; `tt uninstall` removes it -->"

// skill is tt's Claude skill. It names the installed binary and data dir, so
// it works without tt on Claude's PATH or with a custom $TT_HOME.
func skill(exe string) string {
	tasks := paths.Tasks()
	return `---
name: tt
description: Track todos and notes on the user's tt tasks (their tmux task tracker). Use when the user asks to add, list or tick off a todo, to note, track or remember something for a task, or asks which task this is or what's left on it.
---

` + skillMarker + `

# tt

The user tracks ongoing work as tt tasks: one tmux window each, with one
markdown file of todos and notes. A session started in a task's window gets a
"tt task:" block in its context naming the task, its slug and its file. No
such block means this session isn't linked to a task.

Run tt as ` + "`" + exe + "`" + ` (it may not be on PATH).

## Todos

- ` + "`tt todo list`" + ` lists open todos, numbered.
- ` + "`tt todo add \"<text>\"`" + ` adds one.
- ` + "`tt todo done <n|text>`" + ` ticks one off: n from the list, or a
  case-insensitive substring that matches exactly one open todo.

They act on this window's task, or the inbox outside a task (the output
says which). Add ` + "`--task <slug>`" + ` for another task, or ` + "`--inbox`" + `
for the user's inbox. Task files are ` + "`" + tasks + "/<slug>.md`" + `: when
the user names a task, match it against those slugs and each file's
` + "`# Name`" + ` line. The board picks changes up within a second.

Use these commands for todos rather than editing checkbox lines by hand.

## Notes

The task file holds ` + "`# Name`" + `, an optional ` + "`Ticket:`" + ` line,
` + "`## Todos`" + ` (checkboxes) and ` + "`## Notes`" + ` (free text). The user edits it
too: change only the lines you mean to, never rewrite the whole file.

When asked to track or remember something: an action to take later is a
todo; a decision, finding or link is a short line under ` + "`## Notes`" + `.
`
}

// writeSkill installs or updates tt's skill. A SKILL.md there that tt didn't
// write is left alone.
func writeSkill(w io.Writer, exe string) error {
	path := paths.ClaudeSkill()
	want := skill(exe)
	old, err := os.ReadFile(path)
	switch {
	case err == nil && string(old) == want:
		fmt.Fprintf(w, "%s already up to date.\n", path)
		return nil
	case err == nil && !strings.Contains(string(old), skillMarker):
		return fmt.Errorf("%s exists and wasn't written by tt; move it away and rerun", path)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(path, []byte(want)); err != nil {
		return err
	}
	fmt.Fprintf(w, "Wrote %s\n", path)
	return nil
}

// removeSkill deletes tt's skill directory, if tt wrote it.
func removeSkill(w io.Writer) error {
	path := paths.ClaudeSkill()
	old, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if !strings.Contains(string(old), skillMarker) {
		fmt.Fprintf(w, "Left %s: tt didn't write it.\n", path)
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	_ = os.Remove(filepath.Dir(path)) // only if empty
	fmt.Fprintf(w, "Removed %s\n", path)
	return nil
}

// skillCurrent reports whether the installed skill is the one this binary
// would write.
func skillCurrent(exe string) bool {
	b, err := os.ReadFile(paths.ClaudeSkill())
	return err == nil && string(b) == skill(exe)
}
