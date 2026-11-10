package board

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// filterSnap keeps the cards whose name, slug, ticket or a label contains
// every word of q (case-insensitive), and the untracked windows whose name
// does. While a filter is on, load fetches every Done task, not just the
// last week's; with q empty those older ones are dropped again.
func filterSnap(full Snapshot, q string, now time.Time) Snapshot {
	words := strings.Fields(strings.ToLower(q))
	out := full
	for col := range numCols {
		out.Columns[col] = nil
		for _, c := range full.Columns[col] {
			if len(words) == 0 && c.Task.DoneAt.Valid && time.Unix(c.Task.DoneAt.Int64, 0).Before(now.Add(-doneFor)) {
				continue
			}
			if matches(words, append([]string{c.Task.Name, c.Task.Slug, c.Task.Ticket}, c.Labels...)) {
				out.Columns[col] = append(out.Columns[col], c)
			}
		}
	}
	out.Untracked = nil
	for _, u := range full.Untracked {
		if matches(words, []string{u.Window.Name, u.Window.SessionName + ":" + u.Window.Index}) {
			out.Untracked = append(out.Untracked, u)
		}
	}
	return out
}

func matches(words, fields []string) bool {
	for _, w := range words {
		found := false
		for _, f := range fields {
			if strings.Contains(strings.ToLower(f), w) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// openFilter opens the `/` prompt on the current filter, and loads every
// Done task so old ones can match.
func (m *model) openFilter() tea.Cmd {
	in := textinput.New()
	in.Prompt = "/"
	in.Placeholder = "name, ticket or label"
	in.SetWidth(40)
	in.SetValue(m.filter)
	in.CursorEnd()
	m.finput = &in
	return tea.Batch(in.Focus(), m.loader())
}

// setFilter applies q to the last snapshot right away. Turning a filter on
// also reloads, to pull in Done tasks older than a week.
func (m *model) setFilter(q string) tea.Cmd {
	was := m.filter
	m.filter = strings.TrimSpace(q)
	m.snap = filterSnap(m.full, m.filter, time.Now())
	if !m.visible(m.selSlug) {
		// The selected card was filtered out: go to the first match.
		m.selSlug, m.focus = "", onBoard
		for col := range numCols {
			if len(m.snap.Columns[col]) > 0 {
				m.col, m.idx[col] = col, 0
				break
			}
		}
	}
	m.reselect()
	if was == "" && m.filter != "" {
		return m.loader()
	}
	return nil
}

func (m *model) visible(slug string) bool {
	for col := range numCols {
		for _, c := range m.snap.Columns[col] {
			if c.Task.Slug == slug {
				return true
			}
		}
	}
	return false
}

func (m *model) updateFilter(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "ctrl+c":
		m.finput = nil
		return m.setFilter("")
	case "enter":
		m.finput = nil
		return nil
	}
	var c tea.Cmd
	*m.finput, c = m.finput.Update(k)
	return tea.Batch(c, m.setFilter(m.finput.Value()))
}
