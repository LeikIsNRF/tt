// Package todo reads and edits the markdown files that hold tt's todos and
// notes: one per task (~/.tt/tasks/<slug>.md) plus the inbox. The files are
// the only source of truth; every edit touches just the lines it must, so
// hand edits and Claude's edits survive.
package todo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/LeikIsNRF/tt/internal/paths"
)

// TaskPath is the markdown file of the task with slug.
func TaskPath(slug string) string { return filepath.Join(paths.Tasks(), slug+".md") }

// DonePath is where a done task's file is archived.
func DonePath(slug string) string { return filepath.Join(paths.DoneTasks(), slug+".md") }

// Archive moves a task's file into the done directory. A missing file is
// fine: there's nothing to archive.
func Archive(slug string) error {
	if err := os.MkdirAll(paths.DoneTasks(), 0o755); err != nil {
		return err
	}
	err := os.Rename(TaskPath(slug), DonePath(slug))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Delete removes a task's file, open or archived. Missing files are fine.
func Delete(slug string) error {
	for _, p := range []string{TaskPath(slug), DonePath(slug)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Todo is one checkbox line.
type Todo struct {
	Line int // 0-based line index in the file
	Raw  string
	Done bool
	Text string
}

// `- [ ] text`, `* [x] text`, `+ [X] text`, possibly indented.
var checkbox = regexp.MustCompile(`^(\s*[-*+] \[)([ xX])(\] ?)(.*)$`)

// Parse returns every todo in content, in file order.
func Parse(content string) []Todo {
	var ts []Todo
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if m := checkbox.FindStringSubmatch(line); m != nil {
			ts = append(ts, Todo{Line: i, Raw: line, Done: m[2] != " ", Text: strings.TrimSpace(m[4])})
		}
	}
	return ts
}

// Load parses the file at path; a missing file has no todos.
func Load(path string) ([]Todo, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return Parse(string(b)), nil
}

// Open returns the todos that aren't ticked off.
func Open(ts []Todo) []Todo {
	var open []Todo
	for _, t := range ts {
		if !t.Done {
			open = append(open, t)
		}
	}
	return open
}

// EnsureTaskFile creates a task's file from the template unless it exists.
func EnsureTaskFile(slug, name, ticket string) error {
	return ensure(TaskPath(slug), taskTemplate(name, ticket))
}

func taskTemplate(name, ticket string) string {
	head := "# " + name + "\n\n"
	if ticket != "" {
		head += "Ticket: " + ticket + "\n\n"
	}
	return head + "## Todos\n\n## Notes\n"
}

// EnsureInbox creates the inbox file unless it exists.
func EnsureInbox() error { return ensure(paths.Inbox(), "# Inbox\n\n") }

func ensure(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = f.WriteString(content)
	return errors.Join(err, f.Close())
}

// Add inserts an open todo after the file's last todo, else under a
// "Todos" heading, else at the end. The file must exist.
func Add(path, text string) error {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if text == "" {
		return errors.New("empty todo")
	}
	return addItem(path, "- [ ] "+text)
}

func addItem(path, item string) error {
	return edit(path, func(lines []string) ([]string, error) {
		return insertTodo(lines, item), nil
	})
}

var todosHeading = regexp.MustCompile(`(?i)^#+\s*todos?$`)

func insertTodo(lines []string, item string) []string {
	last, heading := -1, -1
	for i, l := range lines {
		if checkbox.MatchString(l) {
			last = i
		}
		if heading < 0 && todosHeading.MatchString(strings.TrimSpace(l)) {
			heading = i
		}
	}
	switch {
	case last >= 0:
		return splice(lines, last+1, item)
	case heading >= 0:
		at := heading + 1
		if at < len(lines) && blank(lines[at]) {
			at++ // keep the blank line under the heading
		}
		if at < len(lines) && !blank(lines[at]) {
			return splice(lines, at, item, "") // and one before the next section
		}
		return splice(lines, at, item)
	}
	at := len(lines)
	for at > 0 && blank(lines[at-1]) {
		at--
	}
	if at == 0 {
		return []string{item}
	}
	return append(lines[:at:at], "", item)
}

var (
	notesHeading = regexp.MustCompile(`(?i)^(#+)\s*notes?$`)
	anyHeading   = regexp.MustCompile(`^(#+)\s`)
)

// AddNotes appends text to the end of the file's "Notes" section, else
// adds that section at the end. The file must exist.
func AddNotes(path, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	add := strings.Split(text, "\n")
	return edit(path, func(lines []string) ([]string, error) {
		start, level := -1, 0
		for i, l := range lines {
			if m := notesHeading.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
				start, level = i, len(m[1])
				break
			}
		}
		if start < 0 {
			at := len(lines)
			for at > 0 && blank(lines[at-1]) {
				at--
			}
			out := lines[:at:at]
			if at > 0 {
				out = append(out, "")
			}
			return append(append(out, "## Notes", ""), add...), nil
		}
		end := len(lines) // the next heading at the same level or higher
		for i := start + 1; i < len(lines); i++ {
			if m := anyHeading.FindStringSubmatch(lines[i]); m != nil && len(m[1]) <= level {
				end = i
				break
			}
		}
		at := end
		for at > start+1 && blank(lines[at-1]) {
			at--
		}
		ins := append([]string{""}, add...)
		if at == end && end < len(lines) {
			ins = append(ins, "") // blank before the next section
		}
		return splice(lines, at, ins...), nil
	})
}

func blank(s string) bool { return strings.TrimSpace(s) == "" }

func splice(lines []string, at int, add ...string) []string {
	out := append(lines[:at:at], add...)
	return append(out, lines[at:]...)
}

// ErrChanged means the line on disk is no longer the todo that was shown:
// the file was edited meanwhile.
var ErrChanged = errors.New("the file changed; try again")

// SetDone ticks or unticks t, rewriting only its checkbox.
func SetDone(path string, t Todo, done bool) error {
	return edit(path, func(lines []string) ([]string, error) {
		if err := check(lines, t); err != nil {
			return nil, err
		}
		mark := " "
		if done {
			mark = "x"
		}
		lines[t.Line] = checkbox.ReplaceAllString(lines[t.Line], "${1}"+mark+"${3}${4}")
		return lines, nil
	})
}

// Remove deletes t's line.
func Remove(path string, t Todo) error {
	return edit(path, func(lines []string) ([]string, error) {
		if err := check(lines, t); err != nil {
			return nil, err
		}
		return append(lines[:t.Line], lines[t.Line+1:]...), nil
	})
}

// Move adds t to the file at dst, ticked or not as it was, then removes it
// from src. If removing fails the todo is in both files rather than neither.
func Move(src string, t Todo, dst string) error {
	mark := " "
	if t.Done {
		mark = "x"
	}
	if err := addItem(dst, "- ["+mark+"] "+t.Text); err != nil {
		return err
	}
	return Remove(src, t)
}

func check(lines []string, t Todo) error {
	if t.Line >= len(lines) || strings.TrimSuffix(lines[t.Line], "\r") != t.Raw {
		return ErrChanged
	}
	return nil
}

// edit rewrites path with f applied to its lines, via a temp file and
// rename so a reader never sees half a file.
func edit(path string, f func([]string) ([]string, error)) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	trailing := len(lines) > 0 && lines[len(lines)-1] == ""
	if trailing {
		lines = lines[:len(lines)-1]
	}
	if lines, err = f(lines); err != nil {
		return err
	}
	out := strings.Join(lines, "\n")
	if trailing || len(lines) > 0 {
		out += "\n"
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tt-*.md")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) //nolint:errcheck // gone after a successful rename
	if _, err := tmp.WriteString(out); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if fi, err := os.Stat(path); err == nil {
		_ = os.Chmod(tmp.Name(), fi.Mode().Perm())
	}
	return os.Rename(tmp.Name(), path)
}

// Find picks one open todo by its 1-based number among open todos, or by a
// case-insensitive substring of its text that matches exactly one.
func Find(ts []Todo, arg string) (Todo, error) {
	open := Open(ts)
	if n, err := strconv.Atoi(arg); err == nil {
		if n < 1 || n > len(open) {
			return Todo{}, fmt.Errorf("no open todo #%d (there are %d)", n, len(open))
		}
		return open[n-1], nil
	}
	needle := strings.ToLower(arg)
	var hits []Todo
	for _, t := range open {
		if strings.Contains(strings.ToLower(t.Text), needle) {
			hits = append(hits, t)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return Todo{}, fmt.Errorf("no open todo matches %q", arg)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d open todos:", arg, len(hits))
	for _, h := range hits {
		b.WriteString("\n  " + h.Text)
	}
	return Todo{}, errors.New(b.String())
}
