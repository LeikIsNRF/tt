package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/LeikIsNRF/tt/internal/paths"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// target routes a todo command: --inbox forces the inbox and --task <slug>
// names a task; otherwise the task named by $TT_TASK, else the current tmux
// window's @tt_task, else the inbox. It returns the remaining arguments.
func target(args []string) (todo.Target, []string, error) {
	inbox := false
	var slug string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--inbox":
			inbox = true
		case a == "--task":
			if i+1 == len(args) {
				return todo.Target{}, nil, errors.New("--task needs a slug")
			}
			i++
			slug = args[i]
		case strings.HasPrefix(a, "--task="):
			slug = strings.TrimPrefix(a, "--task=")
		default:
			rest = append(rest, a)
		}
	}
	switch {
	case inbox && slug != "":
		return todo.Target{}, nil, errors.New("--inbox and --task are exclusive")
	case inbox:
		return todo.Inbox, rest, nil
	case slug != "":
		// An explicit slug must name an open task, so a typo doesn't quietly
		// start a new file.
		t, err := openTask(slug)
		if err != nil {
			return todo.Target{}, nil, err
		}
		return todo.Target{Slug: slug, Name: t.Name, Ticket: t.Ticket}, rest, nil
	}
	slug = os.Getenv("TT_TASK")
	if slug == "" && os.Getenv("TMUX") != "" {
		slug = tmux.CurrentTask()
	}
	return todo.Target{Slug: slug}, rest, nil
}

// openTask looks up a task that isn't done; the error lists the ones there are.
func openTask(slug string) (store.Task, error) {
	st, err := store.Open(paths.DB())
	if err != nil {
		return store.Task{}, err
	}
	defer st.Close()
	if t, ok, err := st.TaskBySlug(slug); err != nil || (ok && !t.DoneAt.Valid) {
		return t, err
	}
	ts, err := st.Tasks(time.Now())
	if err != nil {
		return store.Task{}, err
	}
	var open []string
	for _, t := range ts {
		if !t.DoneAt.Valid {
			open = append(open, t.Slug)
		}
	}
	return store.Task{}, fmt.Errorf("no open task %q; open tasks: %s", slug, strings.Join(open, ", "))
}

func runAdd(args []string) error {
	t, rest, err := target(args)
	if err != nil {
		return err
	}
	text := strings.Join(rest, " ")
	if strings.TrimSpace(text) == "" {
		if text, err = prompt("todo → " + t.Label() + ": "); err != nil {
			return err
		}
		if strings.TrimSpace(text) == "" {
			return nil // nothing typed: cancel quietly
		}
	}
	if err := t.Ensure(); err != nil {
		return err
	}
	if err := todo.Add(t.Path(), text); err != nil {
		return err
	}
	fmt.Println("added to " + t.Label())
	return nil
}

func prompt(label string) (string, error) {
	fmt.Print(label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", nil // EOF / ^D
	}
	return strings.TrimSpace(line), nil
}

func runTodo(args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "add":
		return runAdd(args)
	case "list", "ls":
		t, rest, err := target(args)
		if err != nil {
			return err
		}
		if len(rest) > 0 {
			return fmt.Errorf("todo list: unexpected %q", rest[0])
		}
		return listTodos(t)
	case "done":
		t, rest, err := target(args)
		if err != nil {
			return err
		}
		if len(rest) == 0 {
			return errors.New("usage: tt todo done [--inbox] <n|text>")
		}
		ts, err := todo.Load(t.Path())
		if err != nil {
			return err
		}
		td, err := todo.Find(ts, strings.Join(rest, " "))
		if err != nil {
			return err
		}
		if err := todo.SetDone(t.Path(), td, true); err != nil {
			return err
		}
		fmt.Printf("done: %s (%s)\n", td.Text, t.Label())
		return nil
	}
	return fmt.Errorf("unknown todo command %q (add, list, done)", sub)
}

func listTodos(t todo.Target) error {
	ts, err := todo.Load(t.Path())
	if err != nil {
		return err
	}
	open := todo.Open(ts)
	fmt.Printf("%s — %d open  (%s)\n", t.Label(), len(open), t.Path())
	for i, td := range open {
		fmt.Printf("%3d. %s\n", i+1, td.Text)
	}
	return nil
}
