package store

import (
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "tt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestTaskLinkIsPermanent(t *testing.T) {
	s := open(t)
	a, _, _ := s.EnsureTask("a", "/x")
	b, _, _ := s.EnsureTask("b", "/x")
	must(t, s.RecordEvent(Event{SessionID: "s1", TaskID: a, State: Idle}))
	must(t, s.RecordEvent(Event{SessionID: "s1", TaskID: b, State: Working}))
	got, _ := s.Session("s1")
	if got.TaskID.Int64 != a {
		t.Errorf("task = %d, want %d", got.TaskID.Int64, a)
	}
}

func TestUnlinkedSessionIsKept(t *testing.T) {
	s := open(t)
	must(t, s.RecordEvent(Event{SessionID: "s1", State: Working, Prompt: "hi"}))
	got, err := s.Session("s1")
	if err != nil || got.TaskID.Valid || got.LastPrompt != "hi" {
		t.Errorf("got %+v, %v", got, err)
	}
}

func TestWindowStateIsMostUrgentLiveSession(t *testing.T) {
	s := open(t)
	must(t, s.RecordEvent(Event{SessionID: "s1", State: Working, TmuxWindow: "@1"}))
	must(t, s.RecordEvent(Event{SessionID: "s2", State: NeedsYou, TmuxWindow: "@1"}))
	must(t, s.RecordEvent(Event{SessionID: "s3", State: NeedsYou, TmuxWindow: "@2"}))
	if st, _ := s.WindowState("@1"); st != NeedsYou {
		t.Errorf("@1 = %q", st)
	}
	if n, _ := s.NeedsYouCount(); n != 2 {
		t.Errorf("needs-you = %d", n)
	}
	must(t, s.RecordEvent(Event{SessionID: "s2", State: Ended}))
	if st, _ := s.WindowState("@1"); st != Working {
		t.Errorf("@1 after end = %q", st)
	}
	must(t, s.RecordEvent(Event{SessionID: "s1", State: Ended}))
	if st, _ := s.WindowState("@1"); st != "" {
		t.Errorf("@1 all ended = %q", st)
	}
}

func TestResumeRevivesEndedSession(t *testing.T) {
	s := open(t)
	must(t, s.RecordEvent(Event{SessionID: "s1", State: Ended}))
	must(t, s.RecordEvent(Event{SessionID: "s1", State: Idle, TmuxWindow: "@3"}))
	got, _ := s.Session("s1")
	if got.EndedAt.Valid || got.TmuxWindow != "@3" {
		t.Errorf("got %+v", got)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestStaleEndKeepsLastState(t *testing.T) {
	s := open(t)
	must(t, s.RecordEvent(Event{SessionID: "s1", State: Working}))
	must(t, s.EndSessions([]string{"s1"}))
	got, _ := s.Session("s1")
	if !got.EndedAt.Valid || got.State != Working {
		t.Errorf("got %+v", got)
	}
	if live, _ := s.LiveSessions(); len(live) != 0 {
		t.Errorf("still live: %+v", live)
	}
}

func TestDoneTasksAgeOut(t *testing.T) {
	s := open(t)
	id, _ := s.CreateTask("a", "A", "/x", "")
	must(t, s.MarkDone(id))
	if ts, _ := s.Tasks(time.Now().Add(-time.Hour)); len(ts) != 1 {
		t.Errorf("recently done task hidden: %+v", ts)
	}
	if ts, _ := s.Tasks(time.Now().Add(time.Hour)); len(ts) != 0 {
		t.Errorf("old done task shown: %+v", ts)
	}
}
