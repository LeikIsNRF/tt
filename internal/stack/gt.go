// Package stack resolves a task's graphite stack and its PRs' GitHub state.
//
// Stacks come from graphite's own metadata, never from `gt`: graphite keeps
// one JSON blob per tracked branch under refs/branch-metadata/<branch>, with
// the branch's parentBranchName. `gt log` has no machine-readable output and
// `gt log --stack` only works on the checked-out branch, while the refs can be
// read for any branch, read-only, in two git calls, and all tasks share one
// checkout.
package stack

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Repo is the git repository a task's cwd is in.
type Repo struct {
	Dir    string // a directory inside the repo to run git in
	GitDir string // absolute common git dir, shared by its worktrees
	GitHub string // "owner/name" from the origin remote, "" if not GitHub
}

// FindRepo resolves the repository containing dir.
func FindRepo(dir string) (Repo, error) {
	out, err := git(dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return Repo{}, err
	}
	r := Repo{Dir: dir, GitDir: strings.TrimSpace(string(out))}
	if url, err := git(dir, "config", "--get", "remote.origin.url"); err == nil {
		r.GitHub = gitHubName(strings.TrimSpace(string(url)))
	}
	return r, nil
}

// gitHubName extracts "owner/name" from a github.com remote URL in any of
// its forms (https, ssh://, scp-style git@github.com:owner/name.git).
func gitHubName(url string) string {
	_, rest, ok := strings.Cut(url, "github.com")
	if !ok || rest == "" || (rest[0] != '/' && rest[0] != ':') {
		return ""
	}
	parts := strings.Split(strings.Trim(strings.TrimSuffix(rest[1:], ".git"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

// Graph is graphite's view of a repo: each tracked branch's parent.
type Graph struct {
	Trunk  string
	Parent map[string]string
	Local  map[string]bool // branches that exist locally (refs/heads)
}

// ReadGraph reads every graphite branch-metadata blob plus the local
// branches. A repo graphite has never touched gives an empty graph.
func ReadGraph(r Repo) (Graph, error) {
	g := Graph{Trunk: trunk(r.GitDir), Parent: map[string]string{}, Local: map[string]bool{}}
	out, err := git(r.Dir, "for-each-ref", "--format=%(objectname) %(refname)",
		"refs/branch-metadata/", "refs/heads/")
	if err != nil {
		return g, err
	}
	var blobs bytes.Buffer
	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		oid, ref, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		if b, ok := strings.CutPrefix(ref, "refs/heads/"); ok {
			g.Local[b] = true
		} else if b, ok := strings.CutPrefix(ref, "refs/branch-metadata/"); ok {
			blobs.WriteString(oid + "\n")
			names = append(names, b)
		}
	}
	if len(names) == 0 {
		return g, nil
	}
	cmd := exec.Command("git", "-C", r.Dir, "cat-file", "--batch")
	cmd.Stdin = &blobs
	raw, err := cmd.Output()
	if err != nil {
		return g, fmt.Errorf("git cat-file: %w", err)
	}
	rd := bufio.NewReader(bytes.NewReader(raw))
	for _, name := range names {
		// "<oid> <type> <size>\n<content>\n", or "<oid> missing\n".
		head, err := rd.ReadString('\n')
		if err != nil {
			return g, fmt.Errorf("git cat-file: %w", err)
		}
		f := strings.Fields(head)
		if len(f) != 3 {
			continue
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return g, fmt.Errorf("git cat-file: bad header %q", head)
		}
		body := make([]byte, size+1)
		if _, err := io.ReadFull(rd, body); err != nil {
			return g, fmt.Errorf("git cat-file: %w", err)
		}
		var meta struct {
			ParentBranchName string `json:"parentBranchName"`
		}
		if json.Unmarshal(body[:size], &meta) == nil && meta.ParentBranchName != "" {
			g.Parent[name] = meta.ParentBranchName
		}
	}
	return g, nil
}

// trunk reads graphite's configured trunk, defaulting to main/master by
// what's conventional.
func trunk(gitDir string) string {
	var cfg struct {
		Trunk string `json:"trunk"`
	}
	if b, err := os.ReadFile(filepath.Join(gitDir, ".graphite_repo_config")); err == nil {
		if json.Unmarshal(b, &cfg) == nil && cfg.Trunk != "" {
			return cfg.Trunk
		}
	}
	return "main"
}

// Resolve returns branch's stack bottom→top, trunk excluded: its ancestors
// down to trunk, itself, and every descendant (depth-first, children by
// name), the same set `gt log --stack` shows. A branch graphite doesn't track
// is a stack of one.
func (g Graph) Resolve(branch string) []string {
	if branch == "" || branch == g.Trunk {
		return nil
	}
	var down []string
	seen := map[string]bool{}
	for b := branch; b != "" && b != g.Trunk && !seen[b]; b = g.Parent[b] {
		seen[b] = true
		down = append(down, b)
	}
	slices.Reverse(down)

	children := map[string][]string{}
	for c, p := range g.Parent {
		children[p] = append(children[p], c)
	}
	var walk func(string)
	walk = func(b string) {
		kids := children[b]
		slices.Sort(kids)
		for _, k := range kids {
			if !seen[k] {
				seen[k] = true
				down = append(down, k)
				walk(k)
			}
		}
	}
	walk(branch)
	return down
}

// LocalBranches lists local branches, most recently committed first, for
// the branch picker.
func LocalBranches(r Repo) ([]string, error) {
	out, err := git(r.Dir, "for-each-ref", "--sort=-committerdate", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(out)), nil
}

func git(dir string, args ...string) ([]byte, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("git %s: %s", args[0], strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("git %s: %w", args[0], err)
	}
	return out, nil
}
