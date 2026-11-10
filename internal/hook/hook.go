// Package hook implements `tt hook`, the Claude Code hook entry point. It
// must start fast and must never break Claude: every failure is logged and
// swallowed. The only stdout it writes is SessionStart's task context, which
// Claude Code injects into the new session.
package hook

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/LeikIsNRF/tt/internal/paths"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// Events tt registers for in settings.json.
var Events = []string{
	"SessionStart", "UserPromptSubmit", "Notification", "PostToolUse", "Stop", "SessionEnd",
}

type Payload struct {
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	Event            string `json:"hook_event_name"`
	Prompt           string `json:"prompt"`
	Source           string `json:"source"`
	Message          string `json:"message"`
	NotificationType string `json:"notification_type"`
}

// Transition is what an event does to a session's state. To == "" leaves the
// state alone; OnlyFrom restricts the change to sessions currently in it.
type Transition struct {
	To       store.State
	OnlyFrom store.State
}

// Map decides the state change for a hook payload.
//
// Notification is the one to verify against real payloads (see hook.log):
// permission/elicitation prompts mean the user must act (🔴); the idle
// reminder only means the turn is over (🟡).
func Map(p Payload) Transition {
	switch p.Event {
	case "SessionStart":
		if p.Source == "compact" { // mid-turn; keep whatever state it had
			return Transition{}
		}
		return Transition{To: store.Idle}
	case "UserPromptSubmit":
		return Transition{To: store.Working}
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt", "elicitation_dialog":
			return Transition{To: store.NeedsYou}
		case "idle_prompt":
			return Transition{To: store.YourTurn}
		case "":
			msg := strings.ToLower(p.Message)
			if strings.Contains(msg, "permission") {
				return Transition{To: store.NeedsYou}
			}
			if strings.Contains(msg, "waiting for your input") {
				return Transition{To: store.YourTurn}
			}
		}
		return Transition{}
	case "PostToolUse":
		// A tool ran, so any permission prompt was answered: back to working.
		return Transition{To: store.Working, OnlyFrom: store.NeedsYou}
	case "Stop":
		return Transition{To: store.YourTurn}
	case "SessionEnd":
		return Transition{To: store.Ended}
	}
	return Transition{}
}

// Run handles one hook invocation. Errors are for logging only.
func Run(stdin io.Reader, stdout io.Writer) error {
	raw, err := io.ReadAll(io.LimitReader(stdin, 16<<20))
	if err != nil {
		return err
	}
	var p Payload
	if err := json.Unmarshal(raw, &p); err != nil {
		logLine(raw, p, err)
		return err
	}
	logLine(raw, p, nil)
	if p.SessionID == "" {
		return nil
	}
	tr := Map(p)

	st, err := store.Open(paths.DB())
	if err != nil {
		return err
	}
	defer st.Close()

	if tr.OnlyFrom != "" {
		cur, known, err := st.SessionState(p.SessionID)
		if err != nil {
			return err
		}
		if known && cur != tr.OnlyFrom {
			return nil // hot path for PostToolUse: nothing to do
		}
	}

	pane := os.Getenv("TMUX_PANE")
	var window, windowTask string
	if pane != "" {
		window, windowTask, err = tmux.PaneInfo(pane)
		if err != nil {
			logError(fmt.Errorf("tmux pane info: %w", err))
		}
	}

	slug := os.Getenv("TT_TASK")
	if slug == "" {
		slug = windowTask
	}
	var taskID int64
	if slug != "" {
		var created bool
		if taskID, created, err = st.EnsureTask(slug, p.Cwd); err != nil {
			return err
		}
		if created {
			if err := todo.EnsureTaskFile(slug, slug, ""); err != nil {
				logError(fmt.Errorf("task file: %w", err))
			}
		}
	}

	ev := store.Event{
		SessionID:      p.SessionID,
		TaskID:         taskID,
		State:          tr.To,
		Cwd:            p.Cwd,
		TranscriptPath: p.TranscriptPath,
		TmuxPane:       pane,
		TmuxWindow:     window,
	}
	if p.Event == "UserPromptSubmit" {
		ev.Prompt = p.Prompt
	}
	if err := st.RecordEvent(ev); err != nil {
		return err
	}

	if p.Event == "SessionStart" && slug != "" {
		// Compaction drops it from the conversation, so compact re-injects too.
		if c, ok, err := loadContext(st, slug, time.Now()); err != nil {
			logError(fmt.Errorf("task context: %w", err))
		} else if ok {
			if err := writeContext(stdout, c.format()); err != nil {
				logError(fmt.Errorf("task context: %w", err))
			}
		}
	}

	if window == "" {
		return nil
	}
	// A TT_TASK window that predates @tt_task gets marked so the board finds it.
	var markTask string
	if windowTask == "" {
		markTask = slug
	}
	return tmux.SyncAttention(st, window, markTask)
}

const maxLog = 2 << 20

// logLine appends the raw payload to hook.log so the Notification mapping can
// be checked against reality. PostToolUse payloads carry whole tool inputs and
// outputs, so only their envelope is kept.
func logLine(raw []byte, p Payload, parseErr error) {
	entry := map[string]any{
		"ts":        time.Now().Format(time.RFC3339),
		"event":     p.Event,
		"tt_task":   os.Getenv("TT_TASK"),
		"tmux_pane": os.Getenv("TMUX_PANE"),
	}
	switch {
	case parseErr != nil:
		entry["error"] = parseErr.Error()
		entry["raw"] = string(raw)
	case p.Event == "PostToolUse":
		entry["session_id"] = p.SessionID
	default:
		entry["payload"] = json.RawMessage(raw)
	}
	writeLog(entry)
}

func logError(err error) {
	writeLog(map[string]any{"ts": time.Now().Format(time.RFC3339), "error": err.Error()})
}

func writeLog(entry map[string]any) {
	path := paths.HookLog()
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLog {
		_ = os.Rename(path, path+".1")
	}
	_ = os.MkdirAll(paths.Home(), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(entry)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// LogError is for main to record a failed Run without surfacing it to Claude.
func LogError(err error) { logError(err) }
