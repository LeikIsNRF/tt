package todo

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestParse(t *testing.T) {
	ts := Parse("# x\n- [ ] one\nnot a todo\n  * [x] two\n- [X] three\n- [] nope\n")
	if len(ts) != 3 {
		t.Fatalf("got %d todos: %+v", len(ts), ts)
	}
	if ts[0].Text != "one" || ts[0].Done || ts[0].Line != 1 {
		t.Errorf("first = %+v", ts[0])
	}
	if !ts[1].Done || !ts[2].Done || ts[1].Text != "two" {
		t.Errorf("done flags = %+v", ts)
	}
}

func TestAddPlacement(t *testing.T) {
	for name, c := range map[string]struct{ in, want string }{
		"template": {
			"# T\n\n## Todos\n\n## Notes\n",
			"# T\n\n## Todos\n\n- [ ] new\n\n## Notes\n",
		},
		"after last todo": {
			"# T\n\n## Todos\n\n- [ ] a\n- [x] b\n\n## Notes\n- [ ] not here?\nnotes\n",
			"# T\n\n## Todos\n\n- [ ] a\n- [x] b\n\n## Notes\n- [ ] not here?\n- [ ] new\nnotes\n",
		},
		"inbox": {"# Inbox\n\n", "# Inbox\n\n- [ ] new\n"},
		"empty": {"", "- [ ] new\n"},
	} {
		t.Run(name, func(t *testing.T) {
			p := write(t, c.in)
			if err := Add(p, "  new\n"); err != nil {
				t.Fatal(err)
			}
			if got := read(t, p); got != c.want {
				t.Errorf("got\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestSetDoneTouchesOnlyThatLine(t *testing.T) {
	in := "# T\r\n- [ ] a\n- [ ] b  \nfree *text* [ ] here\n"
	p := write(t, in)
	ts, _ := Load(p)
	if err := SetDone(p, ts[1], true); err != nil {
		t.Fatal(err)
	}
	want := "# T\r\n- [ ] a\n- [x] b  \nfree *text* [ ] here\n"
	if got := read(t, p); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStaleEditIsRefused(t *testing.T) {
	p := write(t, "- [ ] a\n- [ ] b\n")
	ts, _ := Load(p)
	if err := os.WriteFile(p, []byte("- [ ] inserted\n- [ ] a\n- [ ] b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SetDone(p, ts[0], true); err != ErrChanged {
		t.Errorf("err = %v, want ErrChanged", err)
	}
}

func TestMove(t *testing.T) {
	inbox := write(t, "# Inbox\n\n- [ ] keep\n- [x] go\n")
	task := write(t, "# T\n\n## Todos\n\n## Notes\n")
	ts, _ := Load(inbox)
	if err := Move(inbox, ts[1], task); err != nil {
		t.Fatal(err)
	}
	if got := read(t, inbox); got != "# Inbox\n\n- [ ] keep\n" {
		t.Errorf("inbox = %q", got)
	}
	if got := read(t, task); got != "# T\n\n## Todos\n\n- [x] go\n\n## Notes\n" {
		t.Errorf("task = %q", got)
	}
}

func TestFind(t *testing.T) {
	ts := Parse("- [x] fix tests\n- [ ] fix lint\n- [ ] write docs\n- [ ] fix CI\n")
	if got, err := Find(ts, "2"); err != nil || got.Text != "write docs" {
		t.Errorf("by number: %+v %v", got, err)
	}
	if got, err := Find(ts, "LINT"); err != nil || got.Text != "fix lint" {
		t.Errorf("by text: %+v %v", got, err)
	}
	if _, err := Find(ts, "fix"); err == nil {
		t.Error("ambiguous match should fail")
	}
	if _, err := Find(ts, "tests"); err == nil {
		t.Error("done todos shouldn't match")
	}
}

func TestEnsureKeepsExisting(t *testing.T) {
	t.Setenv("TT_HOME", t.TempDir())
	if err := EnsureTaskFile("s", "Name", "ABC-1"); err != nil {
		t.Fatal(err)
	}
	p := TaskPath("s")
	if got := read(t, p); got != "# Name\n\nTicket: ABC-1\n\n## Todos\n\n## Notes\n" {
		t.Errorf("template = %q", got)
	}
	_ = os.WriteFile(p, []byte("mine\n"), 0o644)
	if err := EnsureTaskFile("s", "Other", ""); err != nil || read(t, p) != "mine\n" {
		t.Errorf("existing file overwritten: %v %q", err, read(t, p))
	}
}

func TestArchiveMovesTaskFile(t *testing.T) {
	t.Setenv("TT_HOME", t.TempDir())
	if err := EnsureTaskFile("a", "A", ""); err != nil {
		t.Fatal(err)
	}
	if err := Archive("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(TaskPath("a")); !os.IsNotExist(err) {
		t.Errorf("task file still there: %v", err)
	}
	if got := read(t, DonePath("a")); got != "# A\n\n## Todos\n\n## Notes\n" {
		t.Errorf("archived = %q", got)
	}
}

func TestAddNotes(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"template", "# T\n\n## Todos\n\n## Notes\n", "# T\n\n## Todos\n\n## Notes\n\nnew\n"},
		{"after existing notes, before the next section",
			"## Notes\n\nold\n\n## Links\n- x\n", "## Notes\n\nold\n\nnew\n\n## Links\n- x\n"},
		{"empty section before the next one", "## Notes\n\n## Links\n", "## Notes\n\nnew\n\n## Links\n"},
		{"no section", "# T\n- [ ] a\n", "# T\n- [ ] a\n\n## Notes\n\nnew\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := write(t, tc.in)
			if err := AddNotes(p, "new"); err != nil {
				t.Fatal(err)
			}
			if got := read(t, p); got != tc.want {
				t.Errorf("got %q\nwant %q", got, tc.want)
			}
		})
	}
}
