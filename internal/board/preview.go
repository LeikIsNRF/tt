package board

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/LeikIsNRF/tt/internal/tmux"
)

// previewMin is the fewest rows (title included) the preview keeps when
// columns are long; shorter than that it's not worth showing.
const previewMin = 8

// preview is the last capture of the selected item's pane.
type preview struct {
	pane   string // what's being previewed ("" = nothing)
	screen string
	err    error
}

type previewMsg struct {
	pane, screen string
	err          error
}

// previewTarget is the pane to preview for the selection: a card's Claude
// (or active) pane, or an untracked window's active pane.
func (m *model) previewTarget() (pane string, claude bool) {
	if c, ok := m.selectedCard(); ok {
		return c.Pane, c.Claude
	}
	if u, ok := m.selectedUntracked(); ok {
		return u.Window.Pane, false
	}
	return "", false
}

func capture(pane string) tea.Cmd {
	if pane == "" {
		return nil
	}
	return func() tea.Msg {
		s, err := tmux.Capture(pane)
		return previewMsg{pane: pane, screen: s, err: err}
	}
}

// previewView is a title line plus rows lines of the pane's screen.
func (m *model) previewView(rows int) string {
	pane, claude := m.previewTarget()
	head := bold.Render("Preview")
	var lines []string
	switch {
	case pane == "":
		if c, ok := m.selectedCard(); ok && c.Task.DoneAt.Valid {
			head += faint.Render(" · done")
		} else if ok {
			head += faint.Render(" · dormant · enter revives")
		}
	case m.prev.err != nil:
		head += faint.Render(" · " + pane)
		lines = []string{errSty.Render("  " + m.prev.err.Error())}
	default:
		what := "active pane "
		if claude {
			what = "Claude "
		}
		head += faint.Render(" · " + what + pane + " · enter jumps there")
		lines = screenTail(m.prev.screen, rows, m.width)
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return ansi.Truncate(head, m.width, "…") + "\n" + strings.Join(lines, "\n")
}

// screenTail is the last rows non-blank-trailing lines of a captured
// screen, cut to width. Claude draws its prompt at the bottom, so the tail
// is the part that says what it's doing or asking.
func screenTail(screen string, rows, width int) []string {
	lines := strings.Split(strings.TrimRight(screen, "\n"), "\n")
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[len(lines)-1])) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > rows {
		lines = lines[len(lines)-rows:]
	}
	for i, l := range lines {
		// Reset so a colour left open at the cut doesn't bleed onward.
		lines[i] = ansi.Truncate(l, width, "") + "\x1b[0m"
	}
	return lines
}
