package install

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/LeikIsNRF/tt/internal/hook"
	"github.com/LeikIsNRF/tt/internal/paths"
)

// Doctor reports hook registration and tmux status. It returns an error if
// anything needs fixing.
func Doctor(w io.Writer) error {
	ok := true
	check := func(pass bool, msg string, args ...any) {
		mark := "✓"
		if !pass {
			mark, ok = "✗", false
		}
		fmt.Fprintf(w, "%s %s\n", mark, fmt.Sprintf(msg, args...))
	}

	settings, err := os.ReadFile(paths.ClaudeSettings())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	found, err := registeredEvents(settings)
	if err != nil {
		return fmt.Errorf("%s: %w", paths.ClaudeSettings(), err)
	}
	var missing []string
	cmds := map[string]bool{}
	for _, ev := range hook.Events {
		if c, has := found[ev]; has {
			cmds[c] = true
		} else {
			missing = append(missing, ev)
		}
	}
	check(len(missing) == 0, "hooks registered in %s (%d/%d)%s", paths.ClaudeSettings(),
		len(hook.Events)-len(missing), len(hook.Events), listSuffix("missing", missing))
	for c := range cmds {
		bin, _ := hookBinary(c)
		_, err := os.Stat(bin)
		check(err == nil, "hook binary %s exists", bin)
	}
	if len(cmds) > 1 {
		check(false, "hooks point at %d different binaries; rerun `tt install`", len(cmds))
	}

	if exe, err := executable(); err == nil {
		check(skillCurrent(exe), "tt skill current at %s", paths.ClaudeSkill())
	} else {
		_, err := os.Stat(paths.ClaudeSkill())
		check(err == nil, "tt skill installed at %s", paths.ClaudeSkill())
	}

	_, err = os.Stat(paths.TmuxConf())
	check(err == nil, "%s written", paths.TmuxConf())
	check(TmuxConfSourced(), "%s sources it", paths.UserTmuxConf())
	if os.Getenv("TMUX") != "" {
		check(TmuxConfLoaded(), "running tmux server has it loaded")
	} else {
		fmt.Fprintln(w, "- not inside tmux; skipped live server check")
	}

	if !ok {
		return errors.New("problems found; `tt install` fixes most of them")
	}
	return nil
}

func listSuffix(label string, items []string) string {
	if len(items) == 0 {
		return ""
	}
	sort.Strings(items)
	return fmt.Sprintf(" — %s: %s", label, strings.Join(items, ", "))
}
