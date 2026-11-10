package board

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/LeikIsNRF/tt/internal/stack"
	"github.com/LeikIsNRF/tt/internal/store"
)

// ghEvery is how often the board refreshes PR state from GitHub. The fetch
// time is kept in the DB, so a popup opened shortly after the pinned board
// fetched reuses its result instead of calling gh again.
const ghEvery = 2 * time.Minute

// Branch is one branch of a card's stack and its PR (nil: none).
type Branch struct {
	Name string
	PR   *stack.PR
}

// loadStacks resolves each task's stack from graphite's metadata, attaches
// cached PR state, remembers the stack, and flags stacks that fully landed.
// It never calls gh: that runs on its own, slower schedule (pollGitHub).
func (m *model) loadStacks(snap *Snapshot) error {
	labels, err := m.st.Labels()
	if err != nil {
		return err
	}
	remembered, err := m.st.Stacks()
	if err != nil {
		return err
	}
	prs, fetchedAt, err := m.st.PRs()
	if err != nil {
		return err
	}
	known := !fetchedAt.IsZero()
	snap.PRsFetchedAt = fetchedAt
	graphs := map[string]stack.Graph{} // by common git dir
	for col := range snap.Columns {
		for i := range snap.Columns[col] {
			c := &snap.Columns[col][i]
			c.Labels = labels[c.Task.ID]
			if c.Task.Branch == "" {
				continue
			}
			snap.HasStacks = true
			r, err := m.repos.find(c.Task.Cwd)
			if err != nil {
				c.StackErr = err.Error()
				continue
			}
			g, ok := graphs[r.GitDir]
			if !ok {
				if g, err = stack.ReadGraph(r); err != nil {
					c.StackErr = err.Error()
					continue
				}
				graphs[r.GitDir] = g
			}
			repoPRs := prs[r.GitHub]
			old := remembered[c.Task.ID]
			branches := resolve(g, c.Task.Branch, old, repoPRs, known)
			if err := m.st.SaveStack(c.Task.ID, old, branches); err != nil {
				return err
			}
			c.Trunk = g.Trunk
			for _, b := range branches {
				br := Branch{Name: b}
				if p, ok := repoPRs[b]; ok {
					br.PR = &p
				}
				c.Stack = append(c.Stack, br)
			}
			c.Landed = known && stack.Landed(branches, repoPRs)
			switch {
			case c.Landed && !c.Task.LandedOffered && !c.Task.DoneAt.Valid:
				c.OfferDone = true
			case !c.Landed && c.Task.LandedOffered:
				// The stack grew again: offer once more when it lands.
				if err := m.st.SetLandedOffered(c.Task.ID, false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// repoCache remembers which repo each cwd is in, so a refresh doesn't run
// git twice per task every second. Loads can overlap, hence the lock.
type repoCache struct {
	mu   sync.Mutex
	byWd map[string]stack.Repo
}

func (rc *repoCache) find(cwd string) (stack.Repo, error) {
	rc.mu.Lock()
	r, ok := rc.byWd[cwd]
	rc.mu.Unlock()
	if ok {
		return r, nil
	}
	r, err := stack.FindRepo(cwd)
	if err != nil {
		return r, err // not cached: the cwd may become a repo later
	}
	rc.mu.Lock()
	if rc.byWd == nil {
		rc.byWd = map[string]stack.Repo{}
	}
	rc.byWd[cwd] = r
	rc.mu.Unlock()
	return r, nil
}

// resolve finds the stack to show for a task's branch. If the branch is gone
// locally (merged and cleaned up by `gt sync`), the stack is resolved from
// the topmost remembered branch that still exists, so the rest of the stack
// keeps showing.
func resolve(g stack.Graph, branch string, remembered []string, prs map[string]stack.PR, known bool) []string {
	from := branch
	if !g.Local[branch] {
		for i := len(remembered) - 1; i >= 0; i-- {
			if g.Local[remembered[i]] {
				from = remembered[i]
				break
			}
		}
	}
	resolved := g.Resolve(from)
	if len(resolved) == 0 {
		resolved = []string{branch}
	}
	return stack.Keep(remembered, resolved, prs, known)
}

type ghMsg struct{ err error }

// pollGitHub starts the one PR fetch when the cache is older than ghEvery
// (or now, when forced) and some task has a branch. A failed fetch isn't
// retried until ghEvery has passed either.
func (m *model) pollGitHub(force bool) tea.Cmd {
	if m.ghBusy || !m.snap.HasStacks {
		return nil
	}
	if !force && (time.Since(m.snap.PRsFetchedAt) < ghEvery || time.Since(m.ghTried) < ghEvery) {
		return nil
	}
	m.ghBusy, m.ghTried = true, time.Now()
	return func() tea.Msg {
		prs, err := stack.FetchPRs()
		if err == nil {
			err = m.st.SavePRs(prs, time.Now())
		}
		return ghMsg{err: err}
	}
}

// offerDone asks, once per landing, whether to mark a task done whose whole
// stack merged. It waits until nothing else is on screen.
func (m *model) offerDone() tea.Cmd {
	if m.confirm != nil || m.form != nil || m.launch != nil || m.inbox || m.cmd != nil || m.bpick != nil || m.finput != nil || m.helpOpen {
		return nil
	}
	for col := range ColDone {
		for _, c := range m.snap.Columns[col] {
			if !c.Landed {
				delete(m.offered, c.Task.ID) // offer again if it lands again
			}
		}
	}
	for col := range ColDone {
		for _, c := range m.snap.Columns[col] {
			if !c.OfferDone || m.offered[c.Task.ID] {
				continue
			}
			m.offered[c.Task.ID] = true
			m.selSlug = c.Task.Slug
			m.reselect()
			t := c.Task
			m.confirm = &confirm{yes: m.done(t), unasked: true, prompt: fmt.Sprintf(
				"Every PR in the %s stack has merged. Mark the task done and close its window? y / any key dismisses", t.Name)}
			return func() tea.Msg {
				return actionMsg{err: m.st.SetLandedOffered(t.ID, true)}
			}
		}
	}
	return nil
}

// ticketURL turns a ticket into a link: URLs as they are, anything else
// into format (one %s). ok is false for an ID with no format to put it in.
func ticketURL(format, ticket string) (url string, ok bool) {
	if strings.HasPrefix(ticket, "http://") || strings.HasPrefix(ticket, "https://") {
		return ticket, true
	}
	if format == "" {
		return "", false
	}
	return fmt.Sprintf(format, ticket), true
}

func openURL(url string) tea.Cmd {
	return func() tea.Msg {
		opener := "xdg-open"
		if runtime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, url).Start(); err != nil {
			return actionMsg{err: fmt.Errorf("%s: %w", opener, err)}
		}
		return actionMsg{status: "opened " + url}
	}
}

func (m *model) openTicket() tea.Cmd {
	c, ok := m.selectedCard()
	if !ok {
		return nil
	}
	if c.Task.Ticket == "" {
		m.status = "no ticket; set one with :ticket ABC-123"
		return nil
	}
	url, ok := ticketURL(m.opts.TicketURL, c.Task.Ticket)
	if !ok {
		m.status = "set [task] ticket_url in ~/.tt/config.toml to open " + c.Task.Ticket
		return nil
	}
	return openURL(url)
}

// bpicker picks a task's branch from the repo's local branches.
type bpicker struct {
	task   store.Task
	all    []string
	filter textinput.Model
	idx    int
}

func (p *bpicker) matches() []string {
	q := strings.ToLower(strings.TrimSpace(p.filter.Value()))
	if q == "" {
		return p.all
	}
	var out []string
	for _, b := range p.all {
		if strings.Contains(strings.ToLower(b), q) {
			out = append(out, b)
		}
	}
	return out
}

type branchesMsg struct {
	task     store.Task
	branches []string
	err      error
}

func (m *model) openBranchPicker(t store.Task) tea.Cmd {
	return func() tea.Msg {
		r, err := stack.FindRepo(t.Cwd)
		if err != nil {
			return branchesMsg{err: fmt.Errorf("%s's cwd isn't in a git repo: %w", t.Name, err)}
		}
		bs, err := stack.LocalBranches(r)
		return branchesMsg{task: t, branches: bs, err: err}
	}
}
