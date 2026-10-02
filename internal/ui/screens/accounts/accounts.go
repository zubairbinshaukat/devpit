// Package accounts is the Accounts section: which account every developer
// tool uses in a folder and why, and every change to that, made the way the
// rest of Devpit makes changes. The person sees the plain-words preview
// first (the engine's own sentences, the same ones the command line
// prints), the answer defaults to No, each step shows as a live row, the
// done card offers undo, and Verify asks the tools themselves.
//
// The screens hold no engine logic. Everything goes through the [Service]
// interface, which *service.Service implements; tests and the screenshot
// renderer pass a fake. The screens share one session (session.go): the
// engine, the folder, and the latest overview, so a change made three
// screens deep is on the table the moment the person comes back.
//
// Files: accounts.go is the page; tool.go the tool page; flow.go the change
// flow (account picker, sign-in, name, scope, preview, applying, done);
// signin.go the hand-over of the terminal to a tool's own sign-in;
// verify.go, undo.go, browse.go (with Fix old rules), git.go (Commits as,
// Pushes as, a new identity, an SSH key), claude.go (bringing a Claude Code
// setup over), importcard.go (accounts already on this PC) and manage.go
// (rename and remove).
package accounts

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/acctable"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// pageKeys are the page's own keys.
type pageKeys struct {
	Open, Verify, Browse, Folder, Undo, Import, Reload key.Binding
}

func newPageKeys() pageKeys {
	return pageKeys{
		Open:   bind("open", "enter"),
		Verify: bind("verify", "v"),
		Browse: bind("folders", "b"),
		Folder: bind("folder", "f"),
		Undo:   bind("undo", "u"),
		Import: bind("import", "i"),
		Reload: bind("reload", "r"),
	}
}

// Model is the Accounts page.
type Model struct {
	opts  Options
	sess  *session
	keys  pageKeys
	table acctable.Model
	frame int
	err   error

	// picking is the "change folder" picker, shown in place of the page.
	picking bool
	picker  pathpicker.Model
}

// New returns the Accounts page over the real engine, about the folder
// Devpit was started in.
func New() Model { return NewWith(Options{}) }

// NewWith returns the page over the given options.
func NewWith(opts Options) Model {
	return Model{
		opts:  opts.withDefaults(),
		keys:  newPageKeys(),
		table: acctable.New(nil).SetEmptyText("Opening" + "…"),
	}
}

// Init implements uictx.Screen: open the engine off the first frame.
func (m Model) Init() tea.Cmd {
	return tea.Batch(openCmd(m.opts), m.tick())
}

// tick is the spinner while the page opens.
func (m Model) tick() tea.Cmd {
	if m.opts.Tick == nil {
		return nil
	}
	return m.opts.Tick(spinEvery, func(time.Time) tea.Msg { return spinMsg{} })
}

// Title implements uictx.Screen.
func (m Model) Title() string { return "Accounts" }

// Breadcrumb names the screens opened from here: "Accounts › Claude Code".
func (m Model) Breadcrumb(top string, _ bool) string { return m.Title() + " › " + top }

// Stop implements uictx.Stopper: leaving Accounts lets go of the lock, so
// another Devpit window can change accounts again.
func (m Model) Stop() {
	if m.sess != nil && m.sess.svc != nil {
		m.sess.svc.Release()
	}
}

// ShortHelp implements uictx.Screen.
func (m Model) ShortHelp() []key.Binding {
	if m.picking {
		return m.picker.Keys.ShortHelp()
	}
	if m.sess == nil {
		return nil
	}
	// Enter (open) is the table's own key and listed in the full help: the
	// footer has room for six hints at 80 columns, and these are the ones a
	// person would not guess.
	out := []key.Binding{m.keys.Verify, m.keys.Browse, m.keys.Folder, m.keys.Undo}
	if m.sess.hasFound {
		out = append(out, m.keys.Import)
	}
	return out
}

// FullHelp implements uictx.Screen.
func (m Model) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.table.Keys.Up, m.table.Keys.Down, m.keys.Open},
		{m.keys.Verify, m.keys.Browse, m.keys.Folder, m.keys.Undo, m.keys.Import, m.keys.Reload},
	}
}

// Update implements uictx.Screen.
func (m Model) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case spinMsg:
		if m.sess == nil && m.err == nil {
			m.frame++
			return m, m.tick()
		}
		return m, nil

	case openedMsg:
		return m.onOpened(msg)

	case acctable.OpenMsg:
		return m, m.openTool(accounts.Tool(msg.ID))

	case pathpicker.ChosenMsg:
		m.picking = false
		return m, m.sess.refreshCmd(msg.Path, "")
	case pathpicker.CancelledMsg:
		m.picking = false
		return m, nil
	case pathpicker.BrowsedMsg:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd

	case tea.KeyPressMsg:
		return m.onKey(msg, ctx)
	}
	if m.picking {
		if next, cmd, ok := m.picker.Pointer(ctx, msg, 0); ok {
			m.picker = next
			return m, cmd
		}
		return m, nil
	}
	if m.sess != nil {
		m.table = m.sized(ctx)
		if next, cmd, ok := m.table.Pointer(ctx, msg, m.tableTop(ctx)); ok {
			m.table = next
			m.sess.fresh = ""
			return m, cmd
		}
	}
	return m, nil
}

// onOpened stores what opening found, and opens the import card on its own
// the first time accounts are found on this PC.
func (m Model) onOpened(msg openedMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.err = msg.err
		return m, nil
	}
	m.sess = &session{
		svc: msg.svc, opts: m.opts, folder: msg.folder, ov: msg.ov, ovErr: msg.ovErr, gone: msg.gone,
		folderNote: msg.folderNote, readOnly: msg.readOnly, notices: msg.notices,
		found: msg.found, hasFound: msg.hasFound, dismissed: msg.dismissed,
	}
	if msg.hasFound && !msg.dismissed && msg.readOnly == "" {
		return m, uictx.Push(newImportScreen(m.sess, 1))
	}
	return m, nil
}

// onKey handles the page's keys.
func (m Model) onKey(msg tea.KeyPressMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.picking {
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	}
	if m.sess == nil {
		return m, nil
	}
	m.sess.fresh = ""
	m.table = m.sized(ctx)
	switch {
	case key.Matches(msg, m.keys.Verify):
		return m, uictx.Push(newVerifyScreen(m.sess, 1))
	case key.Matches(msg, m.keys.Browse):
		return m, uictx.Push(newBrowseScreen(m.sess, 1))
	case key.Matches(msg, m.keys.Folder):
		m.picking = true
		m.picker = folderPicker(m.sess, "Which folder do you want to see the accounts of?",
			"Type the folder, then press Enter. Esc goes back to the list.")
		return m, nil
	case key.Matches(msg, m.keys.Undo):
		return m, uictx.Push(newUndoScreen(m.sess, 1))
	case key.Matches(msg, m.keys.Import) && m.sess.hasFound:
		return m, uictx.Push(newImportScreen(m.sess, 1))
	case key.Matches(msg, m.keys.Reload):
		return m, m.reload()
	}
	next, cmd := m.table.Update(msg)
	m.table = next
	return m, cmd
}

// reload asks for the lock again (another window may have let go) and
// builds a fresh overview.
func (m Model) reload() tea.Cmd {
	s := m.sess
	if s.readOnly != "" && s.svc.Hold() == nil {
		s.readOnly = ""
		s.notices = append(s.notices, notice{"success", "This window can change accounts again."})
	}
	return s.refreshCmd(s.folder, "")
}

// openTool opens the tool page of t.
func (m Model) openTool(t accounts.Tool) tea.Cmd {
	if m.sess == nil {
		return nil
	}
	if t == accounts.ToolGit {
		return uictx.Push(newGitScreen(m.sess, 1))
	}
	return uictx.Push(newToolScreen(m.sess, t, 1))
}

// folderPicker is the folder picker the page and the scope step use.
func folderPicker(s *session, pick, typ string) pathpicker.Model {
	cfg := config.Default()
	cfg.DefaultProjectsFolder = s.folder
	p := pathpicker.New(cfg).WithPrompts(pick, typ).WithStat(s.opts.Stat)
	if s.opts.Browse != nil {
		p = p.WithBrowse(s.opts.Browse)
	}
	return p
}

// sized is the table with this frame's rows and room.
func (m Model) sized(ctx uictx.Context) acctable.Model {
	rows := rowsFor(ctx, m.sess)
	t := m.table.SetRows(rows).SetFrame(m.frame)
	return t.SetSize(ctx.Width, max(4, ctx.BodyHeight-m.tableTop(ctx)-m.reserveBelow(ctx)))
}

// top lines of the page: the heading, the notices, and a blank line.
func (m Model) topLines(ctx uictx.Context) []string {
	th := ctx.Theme
	s := m.sess
	out := []string{heading(ctx, "Accounts", "in "+s.folder)}
	var ns []notice
	if s.folderNote != "" {
		ns = append(ns, notice{"warning", "Pick a folder first: " + s.folderNote + " Press f."})
	}
	if s.gone {
		ns = append(ns, notice{"danger", "This folder is gone. Rules still show what would apply here. Press f to pick another."})
	}
	if s.readOnly != "" {
		ns = append(ns, notice{"warning", s.readOnly})
	}
	ns = append(ns, s.notices...)
	if s.hasFound && s.dismissed {
		ns = append(ns, notice{"", "Accounts from claude-acc are on this PC. Press i to bring them into Devpit."})
	}
	for _, n := range ns {
		out = append(out, noticeLines(ctx, n, 2)...)
	}
	if s.ovErr != nil {
		out = append(out, " "+th.Danger.Render(ctx.Icons.Fail+" "+fit(ctx, accounts.Scrub(s.ovErr.Error()), ctx.Width-4)))
	}
	return append(out, "")
}

// tableTop is the body row the table's heading is on.
func (m Model) tableTop(ctx uictx.Context) int {
	if m.sess == nil {
		return 0
	}
	return len(m.topLines(ctx))
}

// reserveBelow is the room kept under the table for the selected tool's
// problems or the first-step hint.
func (m Model) reserveBelow(ctx uictx.Context) int {
	return min(5, max(0, ctx.BodyHeight/4))
}

// View implements uictx.Screen.
func (m Model) View(ctx uictx.Context) string {
	th := ctx.Theme
	switch {
	case m.err != nil:
		return heading(ctx, "Accounts", "") + "\n\n" + errorView(ctx, m.err)
	case m.sess == nil:
		return heading(ctx, "Accounts", "") + "\n\n " + th.Muted.Render(ctx.SpinnerFrame(m.frame)+" Opening your accounts"+ellipsis(ctx))
	case m.picking:
		return heading(ctx, "Accounts", "in "+m.sess.folder) + "\n\n" + m.picker.View(ctx)
	}
	out := m.topLines(ctx)
	t := m.sized(ctx)
	out = append(out, t.View(ctx))
	out = append(out, m.below(ctx, t)...)
	return strings.Join(out, "\n")
}

// below is what sits under the table: the selected tool's problems with
// their fixes, or, before anything is set up, the first step.
func (m Model) below(ctx uictx.Context, t acctable.Model) []string {
	th := ctx.Theme
	room := ctx.BodyHeight - m.tableTop(ctx) - t.Height(ctx) - 1
	if room <= 0 {
		return nil
	}
	var out []string
	if r, ok := t.Selected(); ok {
		if ts, ok := m.sess.status(accounts.Tool(r.ID)); ok && len(ts.Problems) > 0 {
			p := ts.Problems[0]
			out = append(out, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+p.Message, 1, 2)...)
			if p.Fix != "" {
				out = append(out, wrap(ctx, th.Muted, "Fix: "+p.Fix, 3, 5)...)
			}
			if n := len(ts.Problems) - 1; n > 0 {
				out = append(out, "   "+th.Muted.Render(plural(n, "1 more problem", "%d more problems")+": press v to see them all"))
			}
		}
	}
	if len(out) == 0 && !managesAnything(m.sess) {
		out = append(out, wrap(ctx, th.Muted,
			"Nothing is set up yet, so every tool uses its own sign-in. To use a second account here, pick a tool, press Enter, then \"Use another account here…\".", 1, 0)...)
	}
	if len(out) > room {
		out = out[:room]
	}
	if len(out) == 0 {
		return nil
	}
	return append([]string{""}, out...)
}
