package hook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/LeikIsNRF/tt/internal/paths"
	"github.com/LeikIsNRF/tt/internal/stack"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/todo"
)

// sessionStart runs the hook for a new session and returns what it injected.
func sessionStart(t *testing.T, ttTask string) string {
	t.Helper()
	t.Setenv("TT_TASK", ttTask)
	var out bytes.Buffer
	in := `{"session_id":"s-` + ttTask + `","hook_event_name":"SessionStart","source":"startup","cwd":"/nowhere"}`
	if err := Run(strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	if out.Len() == 0 {
		return ""
	}
	var got struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("hook output %q: %v", out.String(), err)
	}
	if got.HookSpecificOutput.HookEventName != "SessionStart" {
		t.Errorf("hookEventName = %q", got.HookSpecificOutput.HookEventName)
	}
	return got.HookSpecificOutput.AdditionalContext
}

func TestSessionStartContext(t *testing.T) {
	t.Setenv("TT_HOME", t.TempDir())
	t.Setenv("TMUX_PANE", "")
	st, err := store.Open(paths.DB())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, err := st.CreateTask("chart-zoom", "Chart zoom", "/nowhere", "ABC-123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask("quiet", "Quiet", "/nowhere", ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetBranch(id, "me/zoom"); err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"chart-zoom", "quiet"} {
		if err := todo.EnsureTaskFile(s, s, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := todo.Add(todo.TaskPath("chart-zoom"), "write the docs"); err != nil {
		t.Fatal(err)
	}

	got := sessionStart(t, "chart-zoom")
	for _, want := range []string{
		"Chart zoom (slug chart-zoom) · ticket ABC-123",
		todo.TaskPath("chart-zoom"),
		"○ me/zoom not submitted",
		"1. write the docs",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("context lacks %q:\n%s", want, got)
		}
	}

	quiet, _, _ := st.TaskBySlug("quiet")
	if err := st.SetContext(quiet.ID, false); err != nil {
		t.Fatal(err)
	}
	if got := sessionStart(t, "quiet"); got != "" {
		t.Errorf("context off still injected:\n%s", got)
	}
	if got := sessionStart(t, ""); got != "" {
		t.Errorf("unlinked session got context:\n%s", got)
	}
}

func TestContextStaysShort(t *testing.T) {
	c := taskContext{Task: store.Task{Name: "n", Slug: "n"}, Notes: "/n.md", PRsAge: -1}
	for i := range 9 {
		c.Stack = append(c.Stack, stackLine{Branch: fmt.Sprint("b", i), PR: &stack.PR{Number: i, State: "MERGED"}})
		c.Todos = append(c.Todos, fmt.Sprint("todo ", i))
	}
	got := c.format()
	if n := strings.Count(got, "\n") + 1; n > 15 {
		t.Errorf("%d lines:\n%s", n, got)
	}
	for _, want := range []string{"b8", "… 5 lower branches", "… 5 more"} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
}
