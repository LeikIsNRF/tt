package stack

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// PR is the GitHub state tt shows for one branch.
type PR struct {
	Number int
	URL    string
	State  string // OPEN, MERGED, CLOSED
	Draft  bool
	Review string // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED, ""
	Checks Checks
}

// Checks is a PR's combined CI state.
type Checks string

const (
	ChecksNone    Checks = ""
	ChecksPending Checks = "pending"
	ChecksFailing Checks = "failing"
	ChecksPassing Checks = "passing"
)

// prQuery asks for the user's most recently updated PRs across every repo,
// with only the head commit's combined check state. `gh pr list --json
// statusCheckRollup` fetches every individual check instead, and on a repo
// with many checks GitHub times out (504) well before 100 PRs. 100 covers
// the open PRs plus enough merged ones to keep landed branches visible.
const prQuery = `query {
  viewer {
    pullRequests(first: 100, orderBy: {field: UPDATED_AT, direction: DESC}) {
      nodes {
        number headRefName state isDraft reviewDecision url
        repository { nameWithOwner }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
      }
    }
  }
}`

// PRs maps "owner/name" → head branch → PR.
type PRs map[string]map[string]PR

// FetchPRs gets the state of every recent PR the user authored, in all
// repos, with one gh call. When a branch has had several PRs, an open one
// wins, then a merged one, then the newest.
func FetchPRs() (PRs, error) {
	out, err := exec.Command("gh", "api", "graphql", "-f", "query="+prQuery).Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("gh api graphql: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}
	return parsePRs(out)
}

type ghPR struct {
	Number         int    `json:"number"`
	HeadRefName    string `json:"headRefName"`
	State          string `json:"state"`
	IsDraft        bool   `json:"isDraft"`
	ReviewDecision string `json:"reviewDecision"`
	URL            string `json:"url"`
	Repository     struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

func parsePRs(raw []byte) (PRs, error) {
	var resp struct {
		Data struct {
			Viewer struct {
				PullRequests struct {
					Nodes []ghPR `json:"nodes"`
				} `json:"pullRequests"`
			} `json:"viewer"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("gh api graphql: %w", err)
	}
	out := PRs{}
	for _, g := range resp.Data.Viewer.PullRequests.Nodes {
		var rollup string
		if n := g.Commits.Nodes; len(n) > 0 && n[0].Commit.StatusCheckRollup != nil {
			rollup = n[0].Commit.StatusCheckRollup.State
		}
		p := PR{Number: g.Number, URL: g.URL, State: g.State, Draft: g.IsDraft,
			Review: g.ReviewDecision, Checks: checks(rollup)}
		repo := g.Repository.NameWithOwner
		if out[repo] == nil {
			out[repo] = map[string]PR{}
		}
		if old, ok := out[repo][g.HeadRefName]; !ok || better(p, old) {
			out[repo][g.HeadRefName] = p
		}
	}
	return out, nil
}

func better(a, b PR) bool {
	rank := map[string]int{"OPEN": 2, "MERGED": 1}
	if rank[a.State] != rank[b.State] {
		return rank[a.State] > rank[b.State]
	}
	return a.Number > b.Number
}

// checks maps GitHub's StatusState for a commit's rollup.
func checks(rollup string) Checks {
	switch rollup {
	case "SUCCESS":
		return ChecksPassing
	case "FAILURE", "ERROR":
		return ChecksFailing
	case "PENDING", "EXPECTED":
		return ChecksPending
	}
	return ChecksNone
}
