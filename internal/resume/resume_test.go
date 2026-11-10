package resume

import (
	"database/sql"
	"testing"
	"time"

	"github.com/LeikIsNRF/tt/internal/store"
)

func TestRowsOnlyResumableSessions(t *testing.T) {
	now := time.Unix(10_000, 0)
	ended := sql.NullInt64{Int64: 1, Valid: true}
	sessions := []store.Session{
		{ID: "killed", LastPrompt: "fix\n  the test", State: store.Working, EndedAt: ended,
			UpdatedAt: now.Add(-2 * time.Hour).Unix()},
		{ID: "no-prompt", EndedAt: ended},
		{ID: "pruned", LastPrompt: "x", TranscriptPath: "/gone", EndedAt: ended},
		{ID: "clean", LastPrompt: "y", TranscriptPath: "/here", State: store.Ended, EndedAt: ended,
			UpdatedAt: now.Unix()},
	}
	rows := Rows(sessions, now, func(p string) bool { return p == "/here" })
	want := []struct{ id, label, detail string }{
		{"killed", "fix the test", "2h ago · interrupted · was working"},
		{"clean", "y", "just now · ended"},
		{"", "[new session]", ""},
		{"", "[just a shell]", ""},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows: %+v", len(rows), rows)
	}
	for i, w := range want {
		id := ""
		if rows[i].Session != nil {
			id = rows[i].Session.ID
		}
		if id != w.id || rows[i].Label != w.label || rows[i].Detail != w.detail {
			t.Errorf("row %d = %s %q %q, want %+v", i, id, rows[i].Label, rows[i].Detail, w)
		}
	}
}
