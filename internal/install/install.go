// Package install registers tt's hooks in Claude Code's settings.json and
// writes the tmux snippet. It never edits the user's own tmux.conf.
package install

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/LeikIsNRF/tt/internal/hook"
	"github.com/LeikIsNRF/tt/internal/paths"
)

type Options struct {
	Yes    bool // skip the confirmation prompt
	Stdin  io.Reader
	Stdout io.Writer
}

// Install merges tt's hook entries into settings.json and writes tmux.conf.
func Install(o Options) error {
	exe, err := executable()
	if err != nil {
		return err
	}
	if err := updateSettings(o, shellQuote(exe)+" hook"); err != nil {
		return err
	}
	if err := writeSkill(o.Stdout, exe); err != nil {
		return err
	}
	if err := writeTmuxConf(exe); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "Wrote %s\n", paths.TmuxConf())
	if !TmuxConfSourced() {
		fmt.Fprintf(o.Stdout, "\nAdd this to %s, then reload tmux:\n\n    source-file %s\n",
			paths.UserTmuxConf(), paths.TmuxConf())
	}
	return nil
}

// Uninstall removes exactly tt's hook entries and its skill. ~/.tt (DB,
// tmux.conf) stays.
func Uninstall(o Options) error {
	if err := updateSettings(o, ""); err != nil {
		return err
	}
	if err := removeSkill(o.Stdout); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "Left %s in place; remove its source-file line from %s yourself.\n",
		paths.Home(), paths.UserTmuxConf())
	return nil
}

func updateSettings(o Options, command string) error {
	path := paths.ClaudeSettings()
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	updated, err := rewrite(old, hook.Events, command)
	if err != nil {
		return err
	}
	if bytes.Equal(old, updated) {
		fmt.Fprintf(o.Stdout, "%s already up to date.\n", path)
		return nil
	}
	fmt.Fprintln(o.Stdout, diff(path, old, updated))
	if !o.Yes && !confirm(o, "Write these changes?") {
		return errors.New("aborted; nothing written")
	}
	if len(old) > 0 {
		backup := fmt.Sprintf("%s.tt-backup-%s", path, time.Now().Format("20060102-150405"))
		if err := os.WriteFile(backup, old, 0o600); err != nil {
			return fmt.Errorf("backup: %w", err)
		}
		fmt.Fprintf(o.Stdout, "Backed up to %s\n", backup)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(path, updated); err != nil {
		return err
	}
	fmt.Fprintf(o.Stdout, "Updated %s\n", path)
	return nil
}

func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	if strings.Contains(exe, "go-build") {
		return "", errors.New("run install from a built binary, not `go run` (the hook needs a stable path)")
	}
	return exe, nil
}

func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t'\"$`\\") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func confirm(o Options, q string) bool {
	fmt.Fprintf(o.Stdout, "%s [y/N] ", q)
	line, _ := bufio.NewReader(o.Stdin).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(line))
	return a == "y" || a == "yes"
}

// diff shells out to diff(1); there is no diff in Go's stdlib.
func diff(path string, old, updated []byte) string {
	dir, err := os.MkdirTemp("", "tt-diff")
	if err != nil {
		return string(updated)
	}
	defer os.RemoveAll(dir)
	a, b := filepath.Join(dir, "current"), filepath.Join(dir, "new")
	_ = os.WriteFile(a, old, 0o600)
	_ = os.WriteFile(b, updated, 0o600)
	out, _ := exec.Command("diff", "-u", "--label", path, "--label", path+" (new)", a, b).Output()
	return string(out)
}

func writeAtomic(path string, data []byte) error {
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := path + ".tt-tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
