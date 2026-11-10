// Package tmux wraps the handful of tmux commands tt needs. All calls go to
// the server named by $TMUX, which hooks inherit from the Claude process.
package tmux

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/LeikIsNRF/tt/internal/store"
)

// PaneInfo resolves a pane to its window id and the window's @tt_task.
func PaneInfo(pane string) (window, task string, err error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", pane,
		"#{window_id}\t#{@tt_task}").Output()
	if err != nil {
		return "", "", err
	}
	window, task, _ = strings.Cut(strings.TrimRight(string(out), "\n"), "\t")
	return window, task, nil
}

// SetAttention sets @tt_state on a window (unsetting it when state is "",
// idle or ended) and the global needs-you count, in one tmux invocation. A
// non-empty task is also stored as the window's @tt_task.
func SetAttention(window string, state store.State, needsYou int, task string) error {
	var args []string
	if state == "" || state == store.Ended || state == store.Idle {
		args = []string{"set-option", "-wu", "-t", window, "@tt_state"}
	} else {
		args = []string{"set-option", "-w", "-t", window, "@tt_state", string(state)}
	}
	if task != "" {
		args = append(args, ";", "set-option", "-w", "-t", window, "@tt_task", task)
	}
	args = append(args, ";", "set-option", "-g", "@tt_needs_you", strconv.Itoa(needsYou))
	return exec.Command("tmux", args...).Run()
}

// SyncAttention recomputes a window's @tt_state and the global needs-you
// count from the DB and pushes both to tmux. A non-empty task is also stored
// as the window's @tt_task.
func SyncAttention(st *store.Store, window, task string) error {
	ws, err := st.WindowState(window)
	if err != nil {
		return err
	}
	n, err := st.NeedsYouCount()
	if err != nil {
		return err
	}
	return SetAttention(window, ws, n, task)
}

// GlobalOption reads a global option from the running server ("" if unset or
// no server is reachable).
func GlobalOption(name string) string {
	out, err := exec.Command("tmux", "show-options", "-gqv", name).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// Window is one tmux window in any session on the server.
type Window struct {
	ID          string // @N
	SessionName string
	Index       string
	Name        string
	Task        string // @tt_task, "" if untracked
	Path        string // active pane's current directory
	Pane        string // active pane's id
	Board       bool   // a pinned tt board runs here (@tt_board)
}

// Windows lists every window on the server, across all sessions.
func Windows() ([]Window, error) {
	rows, err := list("list-windows",
		"#{window_id}", "#{session_name}", "#{window_index}", "#{window_name}", "#{@tt_task}",
		"#{pane_current_path}", "#{@tt_board}", "#{pane_id}")
	if err != nil {
		return nil, err
	}
	ws := make([]Window, 0, len(rows))
	for _, f := range rows {
		ws = append(ws, Window{ID: f[0], SessionName: f[1], Index: f[2], Name: f[3], Task: f[4], Path: f[5],
			Board: f[6] == "1", Pane: f[7]})
	}
	return ws, nil
}

// Pane is one tmux pane and the pid of the process tmux started in it.
type Pane struct {
	ID     string // %N
	Window string // @N
	PID    int
}

// Panes lists every pane on the server.
func Panes() ([]Pane, error) {
	rows, err := list("list-panes", "#{pane_id}", "#{window_id}", "#{pane_pid}")
	if err != nil {
		return nil, err
	}
	ps := make([]Pane, 0, len(rows))
	for _, f := range rows {
		pid, _ := strconv.Atoi(f[2])
		ps = append(ps, Pane{ID: f[0], Window: f[1], PID: pid})
	}
	return ps, nil
}

// list runs `tmux <cmd> -a -F <fields joined by tab>` and splits the output.
func list(cmd string, fields ...string) ([][]string, error) {
	out, err := exec.Command("tmux", cmd, "-a", "-F", strings.Join(fields, "\t")).Output()
	if err != nil {
		return nil, fmt.Errorf("tmux %s: %w", cmd, err)
	}
	var rows [][]string
	for line := range strings.SplitSeq(strings.TrimRight(string(out), "\n"), "\n") {
		if f := strings.Split(line, "\t"); len(f) == len(fields) {
			rows = append(rows, f)
		}
	}
	return rows, nil
}

// NewWindow opens a detached window for a task in the current session and
// returns its id. TT_TASK is set for everything started in it, and @tt_task
// marks the window itself so tt can find it.
func NewWindow(slug, cwd string) (string, error) {
	out, err := exec.Command("tmux", "new-window", "-d", "-P", "-F", "#{window_id}",
		"-n", slug, "-c", cwd, "-e", "TT_TASK="+slug).Output()
	if err != nil {
		return "", fmt.Errorf("tmux new-window: %w", err)
	}
	id := strings.TrimSpace(string(out))
	if err := exec.Command("tmux", "set-option", "-w", "-t", id, "@tt_task", slug).Run(); err != nil {
		return id, fmt.Errorf("tmux set-option @tt_task: %w", err)
	}
	return id, nil
}

// Split opens a new pane in window, in cwd with TT_TASK=slug, running argv
// directly (no shell, so argv needs no quoting). The new pane becomes the
// window's active one. It closes when the command exits.
func Split(window, cwd, slug string, argv ...string) error {
	args := append([]string{"split-window", "-t", window, "-c", cwd, "-e", "TT_TASK=" + slug, "--"}, argv...)
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux split-window: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Adopt marks an existing window as a task's window and names it after the
// task.
func Adopt(window, slug string) error {
	return exec.Command("tmux", "set-option", "-w", "-t", window, "@tt_task", slug,
		";", "rename-window", "-t", window, slug).Run()
}

// KillWindow closes a window and everything running in it.
func KillWindow(window string) error {
	if out, err := exec.Command("tmux", "kill-window", "-t", window).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux kill-window: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// Type types a command line into a window's active pane and presses Enter,
// so it runs in the pane's interactive shell (and the shell is still there
// when it exits). Input sent before the shell is ready waits in the pty.
func Type(window, line string) error {
	return exec.Command("tmux", "send-keys", "-t", window, "-l", line,
		";", "send-keys", "-t", window, "Enter").Run()
}

// Capture returns the visible screen of a pane, with its colours.
func Capture(pane string) (string, error) {
	out, err := exec.Command("tmux", "capture-pane", "-p", "-e", "-t", pane).Output()
	if err != nil {
		return "", fmt.Errorf("tmux capture-pane: %w", err)
	}
	return string(out), nil
}

// MarkBoard sets or clears @tt_board on the window a board is pinned in.
func MarkBoard(window string, on bool) error {
	if on {
		return exec.Command("tmux", "set-option", "-w", "-t", window, "@tt_board", "1").Run()
	}
	return exec.Command("tmux", "set-option", "-wu", "-t", window, "@tt_board").Run()
}

// Jump switches the calling client to a window, in whichever session it is.
func Jump(window string) error {
	if out, err := exec.Command("tmux", "switch-client", "-t", window).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux switch-client: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// ClaudePanes reports which panes have a Claude Code process somewhere under
// them. It walks the process tree because pane_current_command shows the
// native binary's version ("2.1.319"), not "claude". ok is false if ps
// failed, in which case nothing can be concluded.
func ClaudePanes(panes []Pane) (running map[string]bool, ok bool) {
	out, err := exec.Command("ps", "-A", "-o", "pid=,ppid=,command=").Output()
	if err != nil {
		return nil, false
	}
	return claudePanes(panes, string(out)), true
}

func claudePanes(panes []Pane, ps string) map[string]bool {
	children := map[int][]int{}
	claude := map[int]bool{}
	for line := range strings.SplitSeq(ps, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		children[ppid] = append(children[ppid], pid)
		if filepath.Base(f[2]) == "claude" || strings.Contains(line, "@anthropic-ai/claude-code") {
			claude[pid] = true
		}
	}
	running := map[string]bool{}
	for _, p := range panes {
		stack, seen := []int{p.PID}, map[int]bool{}
		for len(stack) > 0 {
			pid := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[pid] {
				continue
			}
			seen[pid] = true
			if claude[pid] {
				running[p.ID] = true
				break
			}
			stack = append(stack, children[pid]...)
		}
	}
	return running
}

// CurrentTask is the @tt_task of the window the caller is in ("" if none).
// In a pane that's $TMUX_PANE's window. A display-popup has its own pane id
// that resolves to no window, so it falls back to the client's current
// window: the one the popup was opened over.
func CurrentTask() string {
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		if window, task, err := PaneInfo(pane); err == nil && window != "" {
			return task
		}
	}
	out, err := exec.Command("tmux", "display-message", "-p", "#{@tt_task}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
