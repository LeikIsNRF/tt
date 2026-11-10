package board

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/LeikIsNRF/tt/internal/draft"
	"github.com/LeikIsNRF/tt/internal/paths"
	"github.com/LeikIsNRF/tt/internal/store"
	"github.com/LeikIsNRF/tt/internal/tmux"
	"github.com/LeikIsNRF/tt/internal/todo"
)

const (
	refreshEvery = time.Second
	doneFor      = 7 * 24 * time.Hour
)

// Options configures a board run.
type Options struct {
	// Popup: the board runs in `display-popup -E`; jumping to a window exits
	// so the popup closes. Otherwise it's pinned in a window and keeps running.
	Popup      bool
	DefaultCwd string
	TicketURL  string // a format with one %s; "" = can't open ticket IDs
}

// Run opens the store and runs the board until the user quits.
func Run(o Options) error {
	if os.Getenv("TMUX") == "" {
		return errors.New("the board runs inside tmux; start tmux first")
	}
	st, err := store.Open(paths.DB())
	if err != nil {
		return err
	}
	defer st.Close()
	m := &model{st: st, opts: o,
		offered: map[int64]bool{}}
	if !o.Popup {
		// Mark the pinned window so no board lists it as Untracked.
		if pane := os.Getenv("TMUX_PANE"); pane != "" {
			if self, _, err := tmux.PaneInfo(pane); err == nil {
				_ = tmux.MarkBoard(self, true)
				defer tmux.MarkBoard(self, false) //nolint:errcheck
			}
		}
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

type focus int

const (
	onBoard focus = iota
	onUntracked
	onTodos // the selected card's todo list in the detail pane
)

type model struct {
	st   *store.Store
	opts Options

	full          Snapshot // last load, unfiltered
	snap          Snapshot // full, filtered: what's shown
	width, height int

	focus  focus
	col    Column
	idx    [numCols]int
	uIdx   int
	tIdx   int // todo cursor in the detail pane
	inbox  bool
	iIdx   int // inbox cursor
	picker *picker
	// Selected task slug / untracked window id, so the cursor follows its
	// item across refreshes.
	selSlug, selWindow string
	loaded             bool // first snapshot seen

	form    *form
	launch  *launch
	confirm *confirm
	cmd     *cmdline // the `:` prompt
	filter  string
	finput  *textinput.Model // the `/` prompt
	prev    preview
	// helpOpen: `?` is showing; helpTop is its scroll offset.
	helpOpen bool
	helpTop  int
	bpick    *bpicker // branch picker
	status   string
	err      error

	ghBusy  bool           // the gh fetch is in flight
	ghTried time.Time      // this board's last fetch attempt
	ghErr   string         // last gh failure, until a fetch succeeds
	offered map[int64]bool // tasks offered done this run
	repos   repoCache
}

type (
	tickMsg     struct{}
	snapshotMsg struct {
		snap Snapshot
		err  error
	}
	actionMsg struct {
		status string
		err    error
		jumped bool
		sel    string // task slug to put the cursor on
	}
)

func tick() tea.Cmd {
	return tea.Tick(refreshEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Init() tea.Cmd { return tea.Batch(m.loader(), tick()) }

// loader loads a snapshot. While filtering it includes every Done task,
// so `/` can find ones older than a week.
func (m *model) loader() tea.Cmd {
	all := m.filter != "" || m.finput != nil
	return func() tea.Msg { return m.load(all) }
}

// load reads tmux and the DB, ends sessions whose Claude is gone, and
// derives the board.
func (m *model) load(allDone bool) tea.Msg {
	windows, err := tmux.Windows()
	if err != nil {
		return snapshotMsg{err: err}
	}
	sessions, err := m.st.LiveSessions()
	if err != nil {
		return snapshotMsg{err: err}
	}
	if err := m.endStale(sessions); err != nil {
		return snapshotMsg{err: err}
	}
	if sessions, err = m.st.LiveSessions(); err != nil {
		return snapshotMsg{err: err}
	}
	since := time.Now().Add(-doneFor)
	if allDone {
		since = time.Time{}
	}
	tasks, err := m.st.Tasks(since)
	if err != nil {
		return snapshotMsg{err: err}
	}
	snap := Derive(tasks, sessions, windows)
	past, err := m.st.PromptedSessionCounts()
	if err != nil {
		return snapshotMsg{err: err}
	}
	for col := range snap.Columns {
		for i := range snap.Columns[col] {
			c := &snap.Columns[col][i]
			c.Past = past[c.Task.ID]
		}
	}
	if err := loadTodos(&snap); err != nil {
		return snapshotMsg{err: err}
	}
	if err := m.loadStacks(&snap); err != nil {
		return snapshotMsg{err: err}
	}
	return snapshotMsg{snap: snap}
}

func (m *model) endStale(sessions []store.LiveSession) error {
	if len(sessions) == 0 {
		return nil
	}
	panes, err := tmux.Panes()
	if err != nil {
		return err
	}
	claude, ok := tmux.ClaudePanes(panes)
	if !ok {
		return nil
	}
	stale := Stale(sessions, panes, claude)
	if len(stale) == 0 {
		return nil
	}
	ids := make([]string, len(stale))
	windows := map[string]bool{}
	for i, s := range stale {
		ids[i] = s.ID
		if s.TmuxWindow != "" {
			windows[s.TmuxWindow] = true
		}
	}
	if err := m.st.EndSessions(ids); err != nil {
		return err
	}
	for w := range windows {
		_ = tmux.SyncAttention(m.st, w, "") // window may be gone; that's fine
	}
	return nil
}

// Update handles msg, then captures the selected pane straight away if the
// selection moved to another one.
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	cmd := m.update(msg)
	if p, _ := m.previewTarget(); p != m.prev.pane {
		m.prev = preview{pane: p}
		cmd = tea.Batch(cmd, capture(p))
	}
	return m, cmd
}

func (m *model) update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return nil
	case tickMsg:
		return tea.Batch(m.loader(), tick(), capture(m.prev.pane))
	case previewMsg:
		if msg.pane == m.prev.pane {
			m.prev.screen, m.prev.err = msg.screen, msg.err
		}
		return nil
	case snapshotMsg:
		if msg.err != nil {
			m.err = msg.err
			return nil
		}
		m.err = nil
		m.full = msg.snap
		m.snap = filterSnap(m.full, m.filter, time.Now())
		m.reselect()
		return tea.Batch(m.pollGitHub(false), m.offerDone())
	case ghMsg:
		m.ghBusy = false
		if msg.err != nil {
			m.ghErr = msg.err.Error()
			return nil
		}
		m.ghErr = ""
		return m.loader()
	case branchesMsg:
		if msg.err != nil {
			m.err = msg.err
			return nil
		}
		f := textinput.New()
		f.Prompt = "/ "
		f.Placeholder = "filter"
		f.SetWidth(40)
		m.bpick = &bpicker{task: msg.task, all: msg.branches, filter: f}
		return m.bpick.filter.Focus()
	case draftMsg:
		if msg.form == m.form {
			m.form.applyDraft(msg)
		}
		return nil
	case promotedMsg:
		m.inbox, m.focus, m.selSlug = false, onBoard, msg.card.Task.Slug
		m.status = "created " + msg.card.Task.Slug
		return tea.Batch(m.openLaunch(msg.card, "“"+msg.todo.Text+"”", todoPrompt(msg.card.Task.Slug, msg.todo)), m.loader())
	case actionMsg:
		m.status, m.err = msg.status, msg.err
		if msg.sel != "" {
			m.selSlug, m.focus = msg.sel, onBoard
		}
		if msg.jumped && m.opts.Popup {
			return tea.Quit
		}
		return m.loader()
	case tea.KeyPressMsg:
		switch {
		case m.confirm != nil:
			return m.updateConfirm(msg)
		case m.launch != nil:
			return m.updateLaunch(msg)
		case m.helpOpen:
			return m.updateHelp(msg)
		case m.cmd != nil:
			return m.updateCmd(msg)
		case m.finput != nil:
			return m.updateFilter(msg)
		case m.bpick != nil:
			return m.updateBranchPicker(msg)
		case m.form != nil:
			return m.updateForm(msg)
		case m.inbox:
			m.status = ""
			return m.updateInbox(msg)
		case m.focus == onTodos:
			m.status = ""
			return m.updateTodos(msg)
		}
		return m.updateBoard(msg)
	}
	switch {
	case m.launch != nil:
		var c tea.Cmd
		m.launch.prompt, c = m.launch.prompt.Update(msg)
		return c
	case m.form != nil:
		return m.form.update(msg)
	case m.cmd != nil:
		var c tea.Cmd
		m.cmd.input, c = m.cmd.input.Update(msg)
		return c
	case m.finput != nil:
		var c tea.Cmd
		*m.finput, c = m.finput.Update(msg)
		return c
	case m.bpick != nil:
		var c tea.Cmd
		m.bpick.filter, c = m.bpick.filter.Update(msg)
		return c
	}
	return nil
}

func (m *model) updateCmd(k tea.KeyPressMsg) tea.Cmd {
	switch k.String() {
	case "esc", "ctrl+c":
		m.cmd = nil
		return nil
	case "enter":
		line := m.cmd.input.Value()
		m.cmd = nil
		return m.runCommand(line)
	}
	var c tea.Cmd
	m.cmd.input, c = m.cmd.input.Update(k)
	return c
}

func (m *model) updateBranchPicker(k tea.KeyPressMsg) tea.Cmd {
	p := m.bpick
	switch k.String() {
	case "esc", "ctrl+c":
		m.bpick = nil
		return nil
	case "up":
		p.idx = max(p.idx-1, 0)
		return nil
	case "down":
		p.idx = clamp(p.idx+1, len(p.matches()))
		return nil
	case "enter":
		ms := p.matches()
		m.bpick = nil
		if len(ms) == 0 {
			return nil
		}
		return m.runCommand("branch " + ms[clamp(p.idx, len(ms))])
	}
	var c tea.Cmd
	p.filter, c = p.filter.Update(k)
	p.idx = clamp(p.idx, len(p.matches()))
	return c
}

func (m *model) updateBoard(k tea.KeyPressMsg) tea.Cmd {
	m.status = ""
	before := m.selSlug
	defer func() {
		if m.selSlug != before {
			m.tIdx = 0
		}
	}()
	switch k.String() {
	case "ctrl+c":
		return tea.Quit
	case "esc":
		if m.filter != "" {
			return m.setFilter("")
		}
		if m.opts.Popup {
			return tea.Quit
		}
	case "left":
		if m.focus == onUntracked {
			m.uIdx = max(m.uIdx-1, 0)
		} else if m.col > 0 {
			m.col--
		}
	case "right":
		if m.focus == onUntracked {
			m.uIdx = min(m.uIdx+1, len(m.snap.Untracked)-1)
		} else if m.col < numCols-1 {
			m.col++
		}
	case "up":
		if m.focus == onUntracked {
			m.focus = onBoard
		} else if m.idx[m.col] > 0 {
			m.idx[m.col]--
		}
	case "down":
		if m.focus == onBoard && m.idx[m.col] < len(m.snap.Columns[m.col])-1 {
			m.idx[m.col]++
		} else if m.focus == onBoard && len(m.snap.Untracked) > 0 {
			m.focus = onUntracked
		}
	default:
		if c, ok := keyCommand(k.String()); ok {
			return m.exec(c, "")
		}
	}
	m.remember()
	return nil
}

func (m *model) updateForm(k tea.KeyPressMsg) tea.Cmd {
	f := m.form
	switch k.String() {
	case "esc", "ctrl+c":
		f.stopDraft()
		m.form = nil
		return nil
	}
	if f.drafting() {
		return nil
	}
	switch k.String() {
	case "ctrl+g":
		if f.kind == formCreate {
			return f.startDraft()
		}
	case "enter":
		vals := f.values()
		if strings.TrimSpace(vals["name"]) == "" {
			f.err = "name is required"
			if f.kind == formCreate && strings.TrimSpace(vals["describe"]) != "" && f.draft == nil {
				f.err += " (ctrl+g drafts one from the description)"
			}
			return nil
		}
		m.form = nil
		if f.kind == formCreate {
			return m.create(vals, f.promote, f.draft)
		}
		return m.adopt(f.window, vals)
	}
	return f.update(k)
}

// remember records what the cursor is on so reselect can find it again.
func (m *model) remember() {
	if c, ok := m.selectedCard(); ok {
		m.selSlug = c.Task.Slug
	} else {
		m.selSlug = ""
	}
	if u, ok := m.selectedUntracked(); ok {
		m.selWindow = u.Window.ID
	}
}

// reselect puts the cursor back on the remembered task (following it to a
// new column if its state changed) and clamps everything else.
func (m *model) reselect() {
	if !m.loaded {
		// Open on the most urgent card.
		m.loaded = true
		for col := range numCols {
			if len(m.snap.Columns[col]) > 0 {
				m.col = col
				break
			}
		}
	}
	found := false
	for col := range numCols {
		for i, c := range m.snap.Columns[col] {
			if m.selSlug != "" && c.Task.Slug == m.selSlug {
				m.col, m.idx[col] = col, i
				found = true
			}
		}
	}
	for col := range numCols {
		m.idx[col] = clamp(m.idx[col], len(m.snap.Columns[col]))
	}
	for i, u := range m.snap.Untracked {
		if u.Window.ID == m.selWindow {
			m.uIdx = i
		}
	}
	m.uIdx = clamp(m.uIdx, len(m.snap.Untracked))
	if m.focus == onUntracked && len(m.snap.Untracked) == 0 {
		m.focus = onBoard
	}
	if c, ok := m.selectedCard(); ok {
		m.tIdx = clamp(m.tIdx, len(c.Todos))
	} else if m.focus == onTodos {
		m.focus = onBoard
	}
	m.iIdx = clamp(m.iIdx, len(m.snap.Inbox))
	// A snapshot loaded before a create can land after it: keep wanting the
	// new task rather than adopting whatever card the cursor is on now.
	if found || m.selSlug == "" {
		m.remember()
	}
}

func clamp(i, n int) int { return max(0, min(i, n-1)) }

func (m *model) selectedCard() (Card, bool) {
	cards := m.snap.Columns[m.col]
	if m.focus == onUntracked || len(cards) == 0 {
		return Card{}, false
	}
	return cards[m.idx[m.col]], true
}

func (m *model) selectedUntracked() (Untracked, bool) {
	if m.focus != onUntracked || len(m.snap.Untracked) == 0 {
		return Untracked{}, false
	}
	return m.snap.Untracked[m.uIdx], true
}

func (m *model) jumpSelected() tea.Cmd {
	var window string
	if c, ok := m.selectedCard(); ok {
		switch {
		case c.Task.DoneAt.Valid:
			m.status = c.Task.Name + " is done"
			return nil
		case c.Window == nil:
			return m.revive(c.Task)
		}
		window = c.Window.ID
	} else if u, ok := m.selectedUntracked(); ok {
		window = u.Window.ID
	} else {
		return nil
	}
	return func() tea.Msg {
		if err := tmux.Jump(window); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{jumped: true}
	}
}

// revive gives a dormant task a window again (same slug, cwd and TT_TASK)
// and types `tt resume` into its shell, which offers the task's past
// sessions.
func (m *model) revive(t store.Task) tea.Cmd {
	return func() tea.Msg {
		if fi, err := os.Stat(t.Cwd); err != nil || !fi.IsDir() {
			return actionMsg{err: fmt.Errorf("can't revive %s: cwd %q is not a directory", t.Slug, t.Cwd)}
		}
		exe, err := os.Executable()
		if err != nil {
			return actionMsg{err: err}
		}
		window, err := tmux.NewWindow(t.Slug, t.Cwd)
		if err != nil {
			return actionMsg{err: err}
		}
		if err := tmux.Type(window, shellQuote(exe)+" resume "+shellQuote(t.Slug)); err != nil {
			return actionMsg{err: fmt.Errorf("start resume picker: %w", err), sel: t.Slug}
		}
		if err := tmux.Jump(window); err != nil {
			return actionMsg{status: "revived " + t.Slug, err: err, sel: t.Slug}
		}
		return actionMsg{status: "revived " + t.Slug, jumped: true, sel: t.Slug}
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// confirm is a y/n question; yes runs on y.
type confirm struct {
	prompt string
	yes    tea.Cmd
	// unasked: tt raised it on its own (a landed stack), so it may appear
	// mid-typing. Any key but y dismisses it and then does what it would
	// have done on the board.
	unasked bool
}

func (m *model) updateConfirm(k tea.KeyPressMsg) tea.Cmd {
	c := m.confirm
	switch k.String() {
	case "y", "Y":
		m.confirm = nil
		return c.yes
	case "n", "N", "esc", "ctrl+c":
		m.confirm = nil
	default:
		if c.unasked {
			m.confirm = nil
			return m.updateBoard(k)
		}
	}
	return nil
}

// markDone moves the selected task to Done, asking first if one of its
// sessions is mid-turn or waiting on a prompt.
func (m *model) markDone() tea.Cmd {
	c, ok := m.selectedCard()
	if !ok {
		return nil
	}
	if c.Task.DoneAt.Valid {
		m.status = c.Task.Name + " is already done"
		return nil
	}
	do := m.done(c.Task)
	switch store.MostUrgent(c.States) {
	case store.NeedsYou, store.Working:
		m.confirm = &confirm{yes: do, prompt: fmt.Sprintf(
			"%s has a Claude session that %s. Mark done and close its window? y/n",
			c.Task.Name, map[store.State]string{
				store.NeedsYou: "needs you", store.Working: "is working",
			}[store.MostUrgent(c.States)])}
		return nil
	}
	return do
}

// done marks t done, archives its markdown file and closes its windows.
func (m *model) done(t store.Task) tea.Cmd {
	return func() tea.Msg {
		if err := m.st.MarkDone(t.ID); err != nil {
			return actionMsg{err: err}
		}
		if err := todo.Archive(t.Slug); err != nil {
			return actionMsg{err: fmt.Errorf("archive notes: %w", err)}
		}
		if err := killTaskWindows(t.Slug); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{status: "done: " + t.Name, sel: t.Slug}
	}
}

// deleteTask removes t from the DB, deletes its markdown file and closes
// its windows.
func (m *model) deleteTask(t store.Task) tea.Cmd {
	return func() tea.Msg {
		if err := m.st.DeleteTask(t.ID); err != nil {
			return actionMsg{err: err}
		}
		if err := todo.Delete(t.Slug); err != nil {
			return actionMsg{err: fmt.Errorf("delete notes: %w", err)}
		}
		if err := killTaskWindows(t.Slug); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{status: "deleted " + t.Name}
	}
}

func killTaskWindows(slug string) error {
	windows, err := tmux.Windows()
	if err != nil {
		return err
	}
	for _, w := range windows {
		if w.Task == slug {
			if err := tmux.KillWindow(w.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

// create makes a task with its own window and jumps there. A draft's todos
// and notes go into its file. With promote (an inbox todo), the todo moves
// into the new task and, instead of jumping, the board opens a launch on it.
func (m *model) create(vals map[string]string, promote *todo.Todo, d *draft.Draft) tea.Cmd {
	name, ticket := strings.TrimSpace(vals["name"]), strings.TrimSpace(vals["ticket"])
	cwd := expandHome(strings.TrimSpace(vals["cwd"]))
	return func() tea.Msg {
		if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
			return actionMsg{err: fmt.Errorf("cwd %s is not a directory", cwd)}
		}
		slug, err := m.uniqueSlug(name)
		if err != nil {
			return actionMsg{err: err}
		}
		if _, err := m.st.CreateTask(slug, name, cwd, ticket); err != nil {
			return actionMsg{err: err}
		}
		if err := todo.EnsureTaskFile(slug, name, ticket); err != nil {
			return actionMsg{err: err}
		}
		if err := writeDraft(slug, d); err != nil {
			return actionMsg{err: fmt.Errorf("write the draft into %s: %w", tildePath(todo.TaskPath(slug)), err)}
		}
		window, err := tmux.NewWindow(slug, cwd)
		if err != nil {
			return actionMsg{err: err}
		}
		if promote != nil {
			if err := todo.Move(paths.Inbox(), *promote, todo.TaskPath(slug)); err != nil {
				return actionMsg{status: "created " + slug, err: fmt.Errorf("move todo: %w", err), sel: slug}
			}
			t, _, err := m.st.TaskBySlug(slug)
			if err != nil {
				return actionMsg{status: "created " + slug, err: err, sel: slug}
			}
			return promotedMsg{card: Card{Task: t, Window: &tmux.Window{ID: window}}, todo: *promote}
		}
		if err := tmux.Jump(window); err != nil {
			return actionMsg{status: "created " + slug, err: err, sel: slug}
		}
		return actionMsg{status: "created " + slug, jumped: true, sel: slug}
	}
}

func (m *model) adopt(w tmux.Window, vals map[string]string) tea.Cmd {
	name, ticket := strings.TrimSpace(vals["name"]), strings.TrimSpace(vals["ticket"])
	return func() tea.Msg {
		slug, err := m.uniqueSlug(name)
		if err != nil {
			return actionMsg{err: err}
		}
		id, err := m.st.CreateTask(slug, name, w.Path, ticket)
		if err != nil {
			return actionMsg{err: err}
		}
		if err := todo.EnsureTaskFile(slug, name, ticket); err != nil {
			return actionMsg{err: err}
		}
		if err := tmux.Adopt(w.ID, slug); err != nil {
			return actionMsg{err: err}
		}
		// Claude already running there gets linked now, not on its next event.
		if err := m.st.LinkWindowSessions(w.ID, id); err != nil {
			return actionMsg{err: err}
		}
		return actionMsg{status: "adopted " + w.SessionName + ":" + w.Index + " as " + slug, sel: slug}
	}
}

func (m *model) uniqueSlug(name string) (string, error) {
	base := Slugify(name)
	for i := 1; ; i++ {
		slug := base
		if i > 1 {
			slug = fmt.Sprintf("%s-%d", base, i)
		}
		taken, err := m.st.SlugTaken(slug)
		if err != nil || !taken {
			return slug, err
		}
	}
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[1:])
		}
	}
	return p
}

// form is the small inline form for creating or adopting a task.
type formKind int

const (
	formCreate formKind = iota
	formAdopt
)

type field struct {
	key, label string
	input      textinput.Model
}

type form struct {
	kind    formKind
	promote *todo.Todo // create: the inbox todo this task is made from
	title   string
	window  tmux.Window // adopt target
	fields  []field
	focus   int
	err     string

	// create's ctrl+g draft: cancel is set while the call runs.
	cancel context.CancelFunc
	spin   spinner.Model
	model  string
	draft  *draft.Draft // shown, and written on save
}

func newField(key, label, value, placeholder string) field {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = placeholder
	in.SetValue(value)
	in.SetWidth(50)
	return field{key: key, label: label, input: in}
}

// formWidth is the widest a form's content gets: label plus the describe
// field, which is wider than the rest so a sentence fits.
const formWidth = 9 + 70

func newCreateForm(cwd string) *form {
	describe := newField("describe", "Describe", "", "optional: rough notes, then ctrl+g to draft the rest")
	describe.input.SetWidth(formWidth - 9)
	return &form{kind: formCreate, title: "New task", fields: []field{
		describe,
		newField("name", "Name", "", "what you're working on"),
		newField("ticket", "Ticket", "", "optional, e.g. ABC-123"),
		newField("cwd", "Cwd", cwd, ""),
	}}
}

func newAdoptForm(w tmux.Window) *form {
	return &form{kind: formAdopt, window: w,
		title: fmt.Sprintf("Adopt %s:%s as a task", w.SessionName, w.Index),
		fields: []field{
			newField("name", "Name", w.Name, ""),
			newField("ticket", "Ticket", "", "optional"),
		}}
}

func (f *form) focusField(i int) tea.Cmd {
	f.fields[f.focus].input.Blur()
	f.focus = (i + len(f.fields)) % len(f.fields)
	in := &f.fields[f.focus].input
	in.CursorEnd()
	return in.Focus()
}

func (f *form) update(msg tea.Msg) tea.Cmd {
	if t, ok := msg.(spinner.TickMsg); ok {
		if !f.drafting() {
			return nil
		}
		var cmd tea.Cmd
		f.spin, cmd = f.spin.Update(t)
		return cmd
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "tab", "down":
			return f.focusField(f.focus + 1)
		case "shift+tab", "up":
			return f.focusField(f.focus - 1)
		}
		f.err = "" // typing clears it; a cursor blink mustn't
	}
	var cmd tea.Cmd
	f.fields[f.focus].input, cmd = f.fields[f.focus].input.Update(msg)
	return cmd
}

func (f *form) index(key string) int {
	for i, fl := range f.fields {
		if fl.key == key {
			return i
		}
	}
	panic("no form field " + key)
}

func (f *form) value(key string) string { return f.fields[f.index(key)].input.Value() }

func (f *form) set(key, v string) { f.fields[f.index(key)].input.SetValue(v) }

func (f *form) values() map[string]string {
	v := map[string]string{}
	for _, fl := range f.fields {
		v[fl.key] = fl.input.Value()
	}
	return v
}
