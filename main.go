// tt is a task tracker and Claude Code session control tower on top of tmux.
package main

import (
	"fmt"
	"os"

	"github.com/LeikIsNRF/tt/internal/board"
	"github.com/LeikIsNRF/tt/internal/config"
	"github.com/LeikIsNRF/tt/internal/hook"
	"github.com/LeikIsNRF/tt/internal/install"
	"github.com/LeikIsNRF/tt/internal/resume"
)

const usage = `usage: tt [command]

  (none)      open the board; --popup when run from the prefix-t popup
  hook        Claude Code hook entry point (reads hook JSON on stdin)
  install     register hooks in ~/.claude/settings.json, install the tt Claude
              skill, write ~/.tt/tmux.conf
              -y  skip the confirmation prompt
  uninstall   remove tt's hooks and skill from ~/.claude
  doctor      check hooks and tmux setup

  add [text]                  add a todo to this window's task, else the inbox;
                              with no text, prompt for one (the prefix-T popup)
  todo [list]                 list open todos, numbered
  todo add [text]             same as add
  todo done <n|text>          tick off open todo n, or the one matching text
                              all take --inbox, or --task <slug> for another task

  resume [slug]               pick a past Claude session of this window's task
                              to resume, or start a new one (revive runs this)
`

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--popup" {
		popup := len(os.Args) == 2
		cfg, err := config.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, "tt:", err)
			os.Exit(1)
		}
		if err := board.Run(board.Options{Popup: popup, DefaultCwd: cfg.Task.Cwd, TicketURL: cfg.Task.TicketURL}); err != nil {
			fmt.Fprintln(os.Stderr, "tt:", err)
			os.Exit(1)
		}
		return
	}
	cmd, args := os.Args[1], os.Args[2:]

	switch cmd {
	case "add":
		exit(runAdd(args))
	case "todo":
		exit(runTodo(args))
	case "resume":
		slug := os.Getenv("TT_TASK")
		if len(args) > 0 {
			slug = args[0]
		}
		if slug == "" {
			exit(fmt.Errorf("usage: tt resume [slug] (defaults to $TT_TASK)"))
		}
		exit(resume.Run(slug))
	}

	if cmd == "hook" {
		// Never fail Claude: log and exit 0 whatever happens.
		if err := hook.Run(os.Stdin, os.Stdout); err != nil {
			hook.LogError(err)
		}
		return
	}

	opts := install.Options{Stdin: os.Stdin, Stdout: os.Stdout}
	for _, a := range args {
		switch a {
		case "-y", "--yes":
			opts.Yes = true
		default:
			fmt.Fprintf(os.Stderr, "tt %s: unknown argument %q\n", cmd, a)
			os.Exit(2)
		}
	}

	var err error
	switch cmd {
	case "install":
		err = install.Install(opts)
	case "uninstall":
		err = install.Uninstall(opts)
	case "doctor":
		err = install.Doctor(os.Stdout)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "tt:", err)
		os.Exit(1)
	}
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "tt:", err)
		os.Exit(1)
	}
	os.Exit(0)
}
