package board

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/LeikIsNRF/tt/internal/stack"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/todo"
)

const cardHeight = 6 // 4 lines + border

var (
	colorSel    = lipgloss.Color("212")
	colorDim    = lipgloss.Color("240")
	stateColors = map[store.State]lipgloss.Style{
		store.NeedsYou: lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		store.YourTurn: lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		store.Working:  lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		store.Idle:     lipgloss.NewStyle().Foreground(lipgloss.Color("245")),
	}
	stateOrder = []store.State{store.NeedsYou, store.YourTurn, store.Working, store.Idle}

	glyphColors = map[stack.Glyph]lipgloss.Style{
		stack.Merged:           lipgloss.NewStyle().Foreground(lipgloss.Color("141")),
		stack.Approved:         lipgloss.NewStyle().Foreground(lipgloss.Color("10")),
		stack.InReview:         lipgloss.NewStyle().Foreground(lipgloss.Color("12")),
		stack.ChangesRequested: lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
		stack.CIRunning:        lipgloss.NewStyle().Foreground(lipgloss.Color("11")),
		stack.CIFailing:        lipgloss.NewStyle().Foreground(lipgloss.Color("9")),
		stack.NoPR:             lipgloss.NewStyle().Foreground(colorDim),
	}
	labelSty = lipgloss.NewStyle().Foreground(lipgloss.Color("75"))

	bold   = lipgloss.NewStyle().Bold(true)
	faint  = lipgloss.NewStyle().Foreground(colorDim)
	errSty = lipgloss.NewStyle().Foreground(lipgloss.Color("9"))
)

func (m *model) View() tea.View {
	var v tea.View
	v.AltScreen = true
	if m.width == 0 {
		v.SetContent("loading…")
		return v
	}
	var b, foot strings.Builder
	b.WriteString(m.header() + "\n\n")
	board := false
	switch {
	case m.helpOpen:
		b.WriteString(m.helpView())
	case m.launch != nil:
		b.WriteString(m.launchView())
	case m.form != nil:
		b.WriteString(m.formView())
	case m.bpick != nil:
		b.WriteString(m.branchPickerView())
	case m.inbox:
		b.WriteString(m.inboxView())
	default:
		board = true
		b.WriteString(m.columnsView() + "\n")
		b.WriteString(m.detailView() + "\n")
		foot.WriteString(m.untrackedView())
	}
	foot.WriteString("\n")
	switch {
	case m.cmd != nil:
		foot.WriteString(m.cmd.input.View() + "\n")
	case m.finput != nil:
		foot.WriteString(m.finput.View() + "\n")
	case m.confirm != nil:
		foot.WriteString(bold.Render(m.confirm.prompt) + "\n")
	case m.err != nil:
		foot.WriteString(errSty.Render("error: "+m.err.Error()) + "\n")
	case m.status != "":
		foot.WriteString(m.status + "\n")
	default:
		foot.WriteString("\n")
	}
	if m.form == nil && m.launch == nil {
		foot.WriteString(faint.Render(ansi.Truncate(m.help(), m.width, "…")))
	}
	if board {
		// The preview gets whatever height is left, less a blank line.
		used := strings.Count(b.String(), "\n") + strings.Count(foot.String(), "\n") + 1
		if rows := m.height - used - 2; rows >= previewMin-1 {
			b.WriteString(m.previewView(rows) + "\n\n")
		}
	}
	v.SetContent(b.String() + foot.String())
	return v
}

func (m *model) help() string {
	switch {
	case m.helpOpen:
		return "↑↓ scroll · space/pgdown page · ?/esc close"
	case m.cmd != nil:
		return commandHint(m.cmd.input.Value())
	case m.finput != nil:
		return "type to narrow by name, ticket or label (old Done tasks too) · enter keep · esc clear"
	case m.bpick != nil:
		return "type to filter · ↑↓ choose · enter set branch · esc cancel"
	case m.confirm != nil:
		return "y confirm · n/esc cancel"
	case m.picker != nil:
		return "↑↓ choose task · enter move · esc cancel"
	case m.inbox:
		return "↑↓ move · space toggle · m move to task · l promote + launch · e edit · i/esc board · q quit"
	case m.focus == onTodos:
		return "↑↓ move · space toggle · l launch · e edit · enter jump · tab/esc cards · q quit"
	}
	s := "←→↑↓ move · enter jump/revive · tab todos · l launch · e edit · i inbox · n new · d done · " +
		"/ filter · : command · ? all keys · q quit"
	if m.filter != "" {
		s = "esc clear filter · " + s
	}
	return s
}

func (m *model) header() string {
	n, needs := 0, len(m.snap.Columns[ColNeedsYou])
	for col := range ColDone {
		n += len(m.snap.Columns[col])
	}
	s := bold.Render("tt") + faint.Render(fmt.Sprintf("  %d open tasks", n))
	if m.filter != "" {
		s += "  " + labelSty.Render("/"+m.filter) + faint.Render(fmt.Sprintf(" · %d shown", n+len(m.snap.Columns[ColDone])))
	}
	if needs > 0 {
		s += "  " + stateColors[store.NeedsYou].Bold(true).Render(fmt.Sprintf("%d need you", needs))
	}
	if n := len(todo.Open(m.snap.Inbox)); n > 0 {
		s += faint.Render(fmt.Sprintf("  📥 %d in inbox", n))
	}
	if m.ghErr != "" {
		s += "  " + errSty.Render(ansi.Truncate("GitHub: "+m.ghErr, max(20, m.width-lipgloss.Width(s)-2), "…"))
	}
	return s
}

func (m *model) columnsView() string {
	colW := max(12, m.width/int(numCols))
	// header, blank, column title, detail pane, untracked block, status,
	// help, and the least of the preview
	maxCards := max(1, (m.height-9-(detailRows+2)-(previewMin+1))/cardHeight)
	cols := make([]string, numCols)
	for col := range numCols {
		cards := m.snap.Columns[col]
		title := fmt.Sprintf("%s (%d)", columnTitles[col], len(cards))
		ts := bold
		if m.focus == onBoard && col == m.col {
			ts = ts.Foreground(colorSel)
		}
		lines := []string{ts.Render(ansi.Truncate(title, colW-1, "…"))}

		start := 0
		if col == m.col && m.idx[col] >= maxCards {
			start = m.idx[col] - maxCards + 1
		}
		end := min(len(cards), start+maxCards)
		if start > 0 {
			lines = append(lines, faint.Render(fmt.Sprintf("↑ %d more", start)))
		}
		for i := start; i < end; i++ {
			sel := m.focus != onUntracked && col == m.col && i == m.idx[col]
			lines = append(lines, cardView(cards[i], colW-1, sel))
		}
		if end < len(cards) {
			lines = append(lines, faint.Render(fmt.Sprintf("↓ %d more", len(cards)-end)))
		}
		if len(cards) == 0 {
			lines = append(lines, faint.Render("—"))
		}
		cols[col] = lipgloss.NewStyle().Width(colW).Render(strings.Join(lines, "\n"))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

func cardView(c Card, w int, sel bool) string {
	inner := w - 2
	name := c.Task.Name
	nameSty := bold
	if c.Window == nil {
		nameSty = faint
	}
	meta := c.Task.Ticket
	if meta == "" && c.Task.Slug != name {
		meta = c.Task.Slug
	}
	if n := len(todo.Open(c.Todos)); n > 0 {
		if meta != "" {
			meta += " · "
		}
		meta += fmt.Sprintf("☐ %d", n)
	}
	body := strings.Join([]string{
		nameSty.Render(ansi.Truncate(name, inner, "…")),
		faint.Render(ansi.Truncate(meta, inner, "…")),
		ansi.Truncate(stripLine(c), inner, "…"),
		ansi.Truncate(summary(c), inner, "…"),
	}, "\n")
	border := colorDim
	if sel {
		border = colorSel
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).
		Width(w).Render(body)
}

// stripLine is the card's stack strip (one glyph per branch, bottom→top)
// followed by its manual labels.
func stripLine(c Card) string {
	var parts []string
	if len(c.Stack) > 0 {
		var g strings.Builder
		for _, b := range c.Stack {
			g.WriteString(glyph(b.PR))
		}
		parts = append(parts, g.String())
	} else if c.StackErr != "" {
		parts = append(parts, errSty.Render("stack?"))
	}
	for _, l := range c.Labels {
		parts = append(parts, labelSty.Render(l))
	}
	if !c.Task.ContextEnabled {
		parts = append(parts, faint.Render("ctx off"))
	}
	return strings.Join(parts, " ")
}

func summary(c Card) string {
	switch {
	case c.Landed && !c.Task.DoneAt.Valid:
		return glyphColors[stack.Merged].Render("stack landed · d to finish")
	case c.Task.DoneAt.Valid:
		return faint.Render("done")
	case c.Window == nil:
		s := "dormant"
		if c.Past == 1 {
			s += " · 1 past session"
		} else if c.Past > 1 {
			s += fmt.Sprintf(" · %d past sessions", c.Past)
		}
		return faint.Render(s)
	case len(c.States) == 0:
		return faint.Render("no session")
	}
	s := fmt.Sprintf("%d session", len(c.States))
	if len(c.States) > 1 {
		s += "s"
	}
	return s + " " + dots(c.States)
}

// dots renders one coloured ● per state with a count, most urgent first.
func dots(states []store.State) string {
	count := map[store.State]int{}
	for _, s := range states {
		count[s]++
	}
	var parts []string
	for _, s := range stateOrder {
		if count[s] > 0 {
			parts = append(parts, stateColors[s].Render(fmt.Sprintf("●%d", count[s])))
		}
	}
	return strings.Join(parts, " ")
}

func (m *model) untrackedView() string {
	label := bold
	if m.focus == onUntracked {
		label = label.Foreground(colorSel)
	}
	if len(m.snap.Untracked) == 0 {
		return label.Render("Untracked") + faint.Render("  none") + "\n"
	}
	var lines []string
	line := label.Render("Untracked") + " "
	for i, u := range m.snap.Untracked {
		chip := fmt.Sprintf(" %s:%s %s ", u.Window.SessionName, u.Window.Index, u.Window.Name)
		if m.focus == onUntracked && i == m.uIdx {
			chip = lipgloss.NewStyle().Reverse(true).Render(chip)
		}
		if len(u.States) > 0 {
			chip += dots(u.States)
		}
		if lipgloss.Width(line)+lipgloss.Width(chip)+1 > m.width && lipgloss.Width(line) > 0 {
			lines = append(lines, line)
			line = "          "
		}
		line += chip + " "
	}
	lines = append(lines, line)
	return strings.Join(lines, "\n") + "\n"
}

func (m *model) formView() string {
	f := m.form
	lines := []string{bold.Render(f.title), ""}
	for i, fl := range f.fields {
		label := fmt.Sprintf("%-9s", fl.label)
		if i == f.focus {
			label = lipgloss.NewStyle().Foreground(colorSel).Render(label)
		}
		lines = append(lines, label+fl.input.View())
	}
	lines = append(lines, "")
	lines = append(lines, f.draftView(max(20, min(m.width-6, formWidth)))...)
	if f.err != "" {
		lines = append(lines, errSty.Render(ansi.Truncate(f.err, max(20, min(m.width-6, formWidth)), "…")))
	}
	switch {
	case f.drafting():
		lines = append(lines, faint.Render("esc cancel"))
	case f.kind == formCreate:
		lines = append(lines, faint.Render("enter save · ctrl+g draft from description · tab/↑↓ next field · esc cancel"))
	default:
		lines = append(lines, faint.Render("enter save · tab/↑↓ next field · esc cancel"))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorSel).
		Padding(0, 1).Render(strings.Join(lines, "\n")) + "\n"
}

func glyph(p *stack.PR) string {
	g := stack.GlyphFor(p)
	return glyphColors[g].Render(string(g))
}

// stackView draws the selected card's stack like `gt log`: top of the stack
// first, trunk at the bottom, the task's own branch in bold.
func stackView(c Card, rows, width int) string {
	head := bold.Render("Stack")
	var lines []string
	switch {
	case c.Task.Branch == "":
		head += faint.Render(" · no branch")
		lines = append(lines, faint.Render("  b to pick one, or :branch <name>"))
	case c.StackErr != "":
		lines = append(lines, errSty.Render("  "+c.StackErr))
	default:
		merged := 0
		for _, b := range c.Stack {
			if b.PR != nil && b.PR.State == "MERGED" {
				merged++
			}
		}
		n := fmt.Sprintf("%d branches", len(c.Stack))
		if len(c.Stack) == 1 {
			n = "1 branch"
		}
		head += faint.Render(fmt.Sprintf(" · %s · %d merged", n, merged))
		nameW := 0
		for _, b := range c.Stack {
			nameW = max(nameW, lipgloss.Width(b.Name))
		}
		nameW = min(nameW, max(10, width/2))
		for i := len(c.Stack) - 1; i >= 0; i-- {
			b := c.Stack[i]
			name := fmt.Sprintf("%-*s", nameW, ansi.Truncate(b.Name, nameW, "…"))
			if b.Name == c.Task.Branch {
				name = bold.Render(name)
			}
			num := "      "
			if b.PR != nil {
				num = fmt.Sprintf("#%-5d", b.PR.Number)
			}
			lines = append(lines, " "+glyph(b.PR)+" "+name+"  "+num+" "+faint.Render(stack.Describe(b.PR)))
		}
		lines = append(lines, faint.Render(" ┴ "+c.Trunk))
	}
	if len(lines) > rows {
		more := len(lines) - rows + 1
		lines = append(lines[:rows-1], faint.Render(fmt.Sprintf("   ↓ %d more", more)))
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	for len(lines) < rows {
		lines = append(lines, "")
	}
	return ansi.Truncate(head, width, "…") + "\n" + strings.Join(lines, "\n")
}

func (m *model) branchPickerView() string {
	p := m.bpick
	ms := p.matches()
	lines := []string{bold.Render("Branch for " + p.task.Name), p.filter.View(), ""}
	rows := max(1, m.height-9)
	start := 0
	if p.idx >= rows {
		start = p.idx - rows + 1
	}
	for i := start; i < min(len(ms), start+rows); i++ {
		name := "  " + ms[i]
		if i == p.idx {
			name = lipgloss.NewStyle().Foreground(colorSel).Render("› " + ms[i])
		}
		if ms[i] == p.task.Branch {
			name += faint.Render("  (current)")
		}
		lines = append(lines, name)
	}
	if len(ms) == 0 {
		lines = append(lines, faint.Render("  no matching local branch"))
	}
	return strings.Join(lines, "\n") + "\n"
}
