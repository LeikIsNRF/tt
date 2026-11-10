package draft

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	want := Draft{Name: "Fix login API", Ticket: "ABC-456", Todos: []string{"add tests"}, Notes: "admin only"}
	for _, tc := range []struct {
		name, out, err string
	}{
		{name: "structured output",
			out: `{"result":"ignored","structured_output":{"name":" Fix login API ","ticket":"ABC-456","todos":["add tests"," "],"notes":"admin only"}}`},
		{name: "fenced result fallback",
			out: `{"result":"` + "```json\\n" + `{\"name\":\"Fix login API\",\"ticket\":\"ABC-456\",\"todos\":[\"add tests\"],\"notes\":\"admin only\"}` + "\\n```" + `"}`},
		{name: "error reply", out: `{"is_error":true,"result":"Credit balance\ntoo low"}`, err: "claude: Credit balance too low"},
		{name: "not json", out: `oops`, err: "claude's reply isn't JSON"},
		{name: "result not json", out: `{"result":"Sure! Here is a task."}`, err: "the draft isn't the JSON asked for"},
		{name: "no name", out: `{"structured_output":{"name":"","ticket":"","todos":[],"notes":""}}`, err: "the draft has no name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse([]byte(tc.out))
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("err = %v, want %q", err, tc.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

// fakeClaude writes an executable that stands in for claude.
func fakeClaude(t *testing.T, script string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunSendsDescriptionOnStdin(t *testing.T) {
	bin := fakeClaude(t, `desc=$(cat)
printf '{"structured_output":{"name":"%s","ticket":"","todos":[],"notes":""}}' "$desc"`)
	d, err := Run(context.Background(), bin, "haiku", "--dangerously-skip-permissions")
	if err != nil || d.Name != "--dangerously-skip-permissions" {
		t.Fatalf("got %+v, %v", d, err)
	}
}

func TestRunFailures(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, fakeClaude(t, "exec sleep 5"), "haiku", "x"); !errors.Is(err, ErrTimeout) {
		t.Errorf("slow claude: err = %v, want ErrTimeout", err)
	}
	_, err := Run(context.Background(), fakeClaude(t, "echo 'Not logged in' >&2; exit 1"), "haiku", "x")
	if err == nil || !strings.Contains(err.Error(), "Not logged in") {
		t.Errorf("failing claude: err = %v, want its stderr", err)
	}
}
