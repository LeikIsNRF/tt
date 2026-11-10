// Package paths resolves where tt keeps its data and where Claude Code keeps
// its settings. Both can be overridden by env vars so tests and smoke runs
// never touch the real files.
package paths

import (
	"os"
	"path/filepath"
)

// Home is tt's data dir: $TT_HOME, else ~/.tt.
func Home() string {
	if v := os.Getenv("TT_HOME"); v != "" {
		return v
	}
	return filepath.Join(userHome(), ".tt")
}

func DB() string        { return filepath.Join(Home(), "tt.db") }
func HookLog() string   { return filepath.Join(Home(), "hook.log") }
func TmuxConf() string  { return filepath.Join(Home(), "tmux.conf") }
func Tasks() string     { return filepath.Join(Home(), "tasks") }
func DoneTasks() string { return filepath.Join(Tasks(), "done") }
func Inbox() string     { return filepath.Join(Home(), "inbox.md") }
func Config() string    { return filepath.Join(Home(), "config.toml") }

// ClaudeSettings is $TT_CLAUDE_SETTINGS, else ~/.claude/settings.json.
func ClaudeSettings() string {
	if v := os.Getenv("TT_CLAUDE_SETTINGS"); v != "" {
		return v
	}
	return filepath.Join(userHome(), ".claude", "settings.json")
}

// ClaudeSkill is where tt's Claude skill lives: skills/tt/ next to the
// settings file, so $TT_CLAUDE_SETTINGS sandboxes it too.
func ClaudeSkill() string {
	return filepath.Join(filepath.Dir(ClaudeSettings()), "skills", "tt", "SKILL.md")
}

// UserTmuxConf is the user's own tmux config, only ever read (never written).
func UserTmuxConf() string {
	return filepath.Join(userHome(), ".tmux.conf")
}

func userHome() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}
