package stack

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func run(t *testing.T, dir string, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// A repo with graphite metadata for master ← a ← b ← c, plus d off a, and
// only master checked out.
func TestReadGraphResolvesWithoutCheckout(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "", "init", "-q", "-b", "master")
	run(t, dir, "", "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init")
	if err := os.WriteFile(filepath.Join(dir, ".git", ".graphite_repo_config"),
		[]byte(`{"trunk":"master"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	parents := map[string]string{"a": "master", "b": "a", "c": "b", "d": "a"}
	for b, p := range parents {
		run(t, dir, "", "branch", b)
		oid := run(t, dir, `{"parentBranchName":"`+p+`","branchRevision":"x"}`, "hash-object", "-w", "--stdin")
		run(t, dir, "", "update-ref", "refs/branch-metadata/"+b, oid)
	}
	r, err := FindRepo(dir)
	if err != nil {
		t.Fatal(err)
	}
	g, err := ReadGraph(r)
	if err != nil {
		t.Fatal(err)
	}
	if g.Trunk != "master" || !g.Local["c"] {
		t.Fatalf("trunk %q, local %v", g.Trunk, g.Local)
	}
	// From the middle: ancestors, itself, descendants — not sibling d.
	if got := g.Resolve("b"); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("Resolve(b) = %v", got)
	}
	// From the bottom: every descendant, depth-first by name.
	if got := g.Resolve("a"); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Errorf("Resolve(a) = %v", got)
	}
	if got := g.Resolve("untracked"); !slices.Equal(got, []string{"untracked"}) {
		t.Errorf("Resolve(untracked) = %v", got)
	}
	if head := run(t, dir, "", "branch", "--show-current"); head != "master" {
		t.Errorf("checkout moved to %s", head)
	}
}

func TestParsePRsPicksStateAndChecks(t *testing.T) {
	pr := func(n int, repo, branch, state, rollup string) string {
		r := "null"
		if rollup != "" {
			r = `{"state":"` + rollup + `"}`
		}
		return fmt.Sprintf(`{"number":%d,"headRefName":%q,"state":%q,"isDraft":false,
			"reviewDecision":null,"url":"u","repository":{"nameWithOwner":%q},
			"commits":{"nodes":[{"commit":{"statusCheckRollup":%s}}]}}`, n, branch, state, repo, r)
	}
	raw := `{"data":{"viewer":{"pullRequests":{"nodes":[` + strings.Join([]string{
		pr(1, "o/r", "a", "CLOSED", "FAILURE"),
		pr(2, "o/r", "a", "MERGED", "SUCCESS"),
		pr(3, "o/r", "b", "OPEN", "PENDING"),
		pr(4, "o/r", "c", "OPEN", "ERROR"),
		pr(5, "o/r", "d", "OPEN", ""),
		pr(6, "o/other", "a", "OPEN", "SUCCESS"),
	}, ",") + `]}}}}`
	prs, err := parsePRs([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		n int
		c Checks
	}{"a": {2, ChecksPassing}, "b": {3, ChecksPending}, "c": {4, ChecksFailing}, "d": {5, ChecksNone}}
	for b, w := range want {
		if p := prs["o/r"][b]; p.Number != w.n || p.Checks != w.c {
			t.Errorf("%s = #%d %q, want #%d %q", b, p.Number, p.Checks, w.n, w.c)
		}
	}
	if prs["o/other"]["a"].Number != 6 {
		t.Error("repos not kept apart")
	}
}

func TestGitHubName(t *testing.T) {
	for url, want := range map[string]string{
		"git@github.com:acme/widget.git":       "acme/widget",
		"https://github.com/acme/widget":       "acme/widget",
		"ssh://git@github.com/acme/widget.git": "acme/widget",
		"https://gitlab.com/acme/widget.git":   "",
	} {
		if got := gitHubName(url); got != want {
			t.Errorf("gitHubName(%q) = %q, want %q", url, got, want)
		}
	}
}

func TestGlyphFor(t *testing.T) {
	cases := []struct {
		pr   *PR
		want Glyph
	}{
		{nil, NoPR},
		{&PR{State: "OPEN", Draft: true, Checks: ChecksFailing}, NoPR},
		{&PR{State: "CLOSED"}, NoPR},
		{&PR{State: "MERGED", Checks: ChecksFailing}, Merged},
		{&PR{State: "OPEN", Review: "CHANGES_REQUESTED", Checks: ChecksFailing}, CIFailing},
		{&PR{State: "OPEN", Review: "CHANGES_REQUESTED", Checks: ChecksPending}, ChangesRequested},
		{&PR{State: "OPEN", Review: "APPROVED", Checks: ChecksPending}, CIRunning},
		{&PR{State: "OPEN", Review: "APPROVED", Checks: ChecksPassing}, Approved},
		{&PR{State: "OPEN", Review: "REVIEW_REQUIRED", Checks: ChecksPassing}, InReview},
	}
	for _, c := range cases {
		if got := GlyphFor(c.pr); got != c.want {
			t.Errorf("GlyphFor(%+v) = %s, want %s", c.pr, got, c.want)
		}
	}
}

func TestKeepRetainsOnlyMergedBranchesThatDisappeared(t *testing.T) {
	prs := map[string]PR{"a": {State: "MERGED"}, "x": {State: "CLOSED"}}
	got := Keep([]string{"a", "x", "b", "c"}, []string{"b", "c"}, prs, true)
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Errorf("Keep = %v", got)
	}
	// Before the first gh fetch nothing is dropped.
	got = Keep([]string{"a", "x", "b"}, []string{"b"}, nil, false)
	if !slices.Equal(got, []string{"a", "x", "b"}) {
		t.Errorf("Keep unknown = %v", got)
	}
}

func TestLanded(t *testing.T) {
	prs := map[string]PR{"a": {State: "MERGED"}, "b": {State: "MERGED"}, "c": {State: "OPEN"}}
	if !Landed([]string{"a", "b"}, prs) || Landed([]string{"a", "c"}, prs) ||
		Landed([]string{"a", "nopr"}, prs) || Landed(nil, prs) {
		t.Error("Landed wrong")
	}
}
