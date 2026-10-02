package accounts

import (
	"context"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/foldertree"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// Browse folders is every folder that has a rule, as a tree under
// "Everywhere", with the accounts each one sets. Opening a folder shows the
// Accounts page for it, where anything can be changed. A rule whose folder
// is gone or whose drive is not connected keeps its row and says so; Fix
// old rules offers to remove it or point it at the folder's new place, and
// never does either by itself.

// browseMsg is what the tree is built from.
type browseMsg struct {
	st    *accounts.Store
	stale []accounts.StaleRule
	err   error
}

// fixPlanMsg is the previews of fixing one old rule.
type fixPlanMsg struct {
	previews []accounts.Preview
	doc      previewDoc
	summary  string
	err      error
}

// browseStage is where Browse folders is.
type browseStage int

const (
	bLoading browseStage = iota
	bTree
	bFix
	bFixFolder
	bFixPlanning
	bFixChange
	bFailed
)

// browseScreen is Browse folders.
type browseScreen struct {
	sess   *session
	depth  int
	run    *runHandle
	ctx    context.Context
	stage  browseStage
	err    error
	st     *accounts.Store
	stale  []accounts.StaleRule
	tree   foldertree.Model
	fixes  menu.Model
	picker pathpicker.Model
	fixing accounts.StaleRule
	change change
	keys   struct{ Fix, Remove, Repoint, Back key.Binding }
}

func newBrowseScreen(s *session, depth int) browseScreen {
	h, ctx := newRunHandle()
	m := browseScreen{sess: s, depth: depth, run: h, ctx: ctx}
	m.keys.Fix = bind("fix old rules", "x")
	m.keys.Remove = bind("remove the rule", "d")
	m.keys.Repoint = bind("point it elsewhere", "p")
	m.keys.Back = bind("back", "enter")
	return m
}

func (m browseScreen) Init() tea.Cmd { return m.loadCmd() }

func (m browseScreen) loadCmd() tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		st, _, err := svc.Load()
		if err != nil {
			return browseMsg{err: err}
		}
		stale, err := svc.StaleRules()
		return browseMsg{st: st, stale: stale, err: err}
	}
}

func (m browseScreen) Title() string {
	if m.stage >= bFix && m.stage != bFailed {
		return "Browse folders › Fix old rules"
	}
	return "Browse folders"
}

func (m browseScreen) Busy() bool { return m.stage == bFixChange && m.change.busy() }

func (m browseScreen) Stop() { m.run.stop() }

func (m browseScreen) TerminalProgress() *tea.ProgressBar { return m.change.progress() }

func (m browseScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case bTree:
		out := m.tree.ShortHelp()
		if len(m.stale) > 0 {
			out = append(out, m.keys.Fix)
		}
		return out
	case bFix:
		return []key.Binding{m.fixes.Keys.Up, m.keys.Remove, m.keys.Repoint}
	case bFixFolder:
		return m.picker.Keys.ShortHelp()
	case bFixChange:
		return m.change.help()
	case bFailed:
		return []key.Binding{m.keys.Back}
	}
	return nil
}

func (m browseScreen) FullHelp() [][]key.Binding {
	if m.stage == bTree {
		return append(m.tree.FullHelp(), []key.Binding{m.keys.Fix})
	}
	return [][]key.Binding{m.ShortHelp()}
}

func (m browseScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case browseMsg:
		if msg.err != nil {
			m.stage, m.err = bFailed, msg.err
			return m, nil
		}
		m.st, m.stale = msg.st, msg.stale
		m.tree = m.buildTree(ctx)
		if m.stage == bLoading || m.stage == bTree {
			m.stage = bTree
		}
		return m, nil
	case foldertree.OpenMsg:
		if msg.ID == foldertree.EverywhereID || msg.Path == "" {
			return m, nil
		}
		// Show the Accounts page for that folder; everything is changed
		// from there.
		return m, tea.Sequence(popN(m.depth), m.sess.refreshCmd(msg.Path, ""))
	case fixPlanMsg:
		if msg.err != nil {
			m.stage, m.err = bFailed, msg.err
			return m, nil
		}
		svc, previews := m.sess.svc, msg.previews
		m.change = newChange(m.ctx, m.sess, msg.doc, "Make this change?", "One undo takes all of it back.", msg.summary,
			func(ctx context.Context) <-chan accounts.Event { return svc.ApplyGroup(ctx, previews) })
		m.stage = bFixChange
		return m, nil
	case pathpicker.ChosenMsg:
		m.stage = bFixPlanning
		return m, m.repointCmd(m.fixing, msg.Path)
	case pathpicker.CancelledMsg:
		m.stage = bFix
		return m, nil
	case pathpicker.BrowsedMsg:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	}

	switch m.stage {
	case bTree:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			if key.Matches(km, m.keys.Fix) && len(m.stale) > 0 && !m.tree.Filtering() {
				return m.toFix()
			}
			next, cmd := m.tree.Update(km)
			m.tree = next
			return m, cmd
		}
		if next, cmd, ok := m.tree.Pointer(ctx, msg, 2); ok {
			m.tree = next
			return m, cmd
		}
	case bFix:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(km, m.keys.Remove):
				return m.fixWith(false)
			case key.Matches(km, m.keys.Repoint):
				return m.fixWith(true)
			}
			next, cmd := m.fixes.Update(km)
			m.fixes = next
			return m, cmd
		}
		if sm, ok := msg.(menu.SelectedMsg); ok {
			m.fixes = m.fixes.SetCursor(sm.Index)
			return m, nil
		}
		if next, cmd, ok := m.sizedFixes(ctx).Pointer(ctx, msg, m.fixTop(ctx)); ok {
			m.fixes = next
			return m, cmd
		}
	case bFixFolder:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			next, cmd := m.picker.Update(km)
			m.picker = next
			return m, cmd
		}
		if next, cmd, ok := m.picker.Pointer(ctx, msg, 2); ok {
			m.picker = next
			return m, cmd
		}
	case bFixChange:
		next, cmd, out := m.change.update(msg, ctx, 2)
		m.change = next
		switch out {
		case outcomeDeclined:
			m.stage = bFix
			return m, nil
		case outcomeBack:
			m.stage = bLoading
			return m, m.loadCmd()
		}
		return m, cmd
	case bFailed:
		if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, m.keys.Back) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// buildTree turns the rules into the tree.
func (m browseScreen) buildTree(ctx uictx.Context) foldertree.Model {
	st := m.st
	var every []foldertree.Chip
	for _, t := range accounts.Tools() {
		if !st.Manages(t) {
			continue
		}
		name := accounts.DefaultName
		if v, ok := st.Everywhere[t]; ok {
			name = v
		}
		every = append(every, foldertree.Chip{Tool: string(t), Account: name})
	}
	staleKind := map[string]foldertree.State{}
	for _, s := range m.stale {
		k := strings.ToLower(s.Rule.Folder)
		staleKind[k] = foldertree.StateMissing
		if s.Kind == accounts.StaleDriveNotConnected {
			staleKind[k] = foldertree.StateOffline
		}
	}
	var folders []foldertree.Folder
	here := false
	for _, r := range st.Rules {
		f := foldertree.Folder{Path: r.Folder, State: staleKind[strings.ToLower(r.Folder)], Current: accounts.SameFolder(r.Folder, m.sess.folder)}
		here = here || f.Current
		for _, t := range accounts.Tools() {
			if v, ok := r.Accounts[t]; ok {
				f.Chips = append(f.Chips, foldertree.Chip{Tool: string(t), Account: v})
			}
		}
		folders = append(folders, f)
	}
	if !here && m.sess.folder != "" {
		folders = append(folders, foldertree.Folder{Path: m.sess.folder, Current: true})
	}
	return foldertree.New(every, foldertree.Build(folders)).SetSize(ctx.Width, max(4, ctx.BodyHeight-4))
}

// toFix lists the old rules.
func (m browseScreen) toFix() (uictx.Screen, tea.Cmd) {
	items := make([]menu.Item, 0, len(m.stale))
	for _, s := range m.stale {
		var chips []string
		for _, t := range accounts.Tools() {
			if v, ok := s.Rule.Accounts[t]; ok {
				chips = append(chips, string(t)+" "+v)
			}
		}
		items = append(items, menu.Item{ID: s.Rule.Folder, Title: s.Rule.Folder, Hint: string(s.Kind), Desc: strings.Join(chips, " · ")})
	}
	m.fixes = menu.New(items)
	m.stage = bFix
	return m, nil
}

// fixWith fixes the selected old rule: remove it, or point it at another
// folder first.
func (m browseScreen) fixWith(repoint bool) (uictx.Screen, tea.Cmd) {
	it, ok := m.fixes.Selected()
	if !ok {
		return m, nil
	}
	for _, s := range m.stale {
		if s.Rule.Folder == it.ID {
			m.fixing = s
		}
	}
	if repoint {
		m.stage = bFixFolder
		m.picker = folderPicker(m.sess, "Where is "+m.fixing.Rule.Folder+" now?", "Type the folder's new place, then press Enter.")
		return m, nil
	}
	m.stage = bFixPlanning
	return m, m.removeCmd(m.fixing)
}

// toolsOf lists a rule's tools in display order.
func toolsOf(r accounts.Rule) []accounts.Tool {
	var out []accounts.Tool
	for _, t := range accounts.Tools() {
		if _, ok := r.Accounts[t]; ok {
			out = append(out, t)
		}
	}
	return out
}

// removeCmd previews removing every tool's rule on a folder.
func (m browseScreen) removeCmd(s accounts.StaleRule) tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		var out fixPlanMsg
		for _, t := range toolsOf(s.Rule) {
			p, err := svc.Plan(ctx, accounts.Change{Tool: t, Scope: accounts.FolderScope(s.Rule.Folder), Remove: true})
			if err != nil {
				return fixPlanMsg{err: err}
			}
			out.previews = append(out.previews, p)
		}
		out.doc = groupDoc(out.previews)
		out.summary = "Removed the old rule on " + s.Rule.Folder
		return out
	}
}

// repointCmd previews moving a folder's rules to its new place: the same
// accounts set on the new folder, then the old folder's rules removed.
func (m browseScreen) repointCmd(s accounts.StaleRule, to string) tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		var out fixPlanMsg
		for _, t := range toolsOf(s.Rule) {
			p, err := svc.Plan(ctx, accounts.Change{Tool: t, Account: s.Rule.Accounts[t], Scope: accounts.FolderScope(to)})
			if err != nil {
				return fixPlanMsg{err: err}
			}
			out.previews = append(out.previews, p)
		}
		for _, t := range toolsOf(s.Rule) {
			p, err := svc.Plan(ctx, accounts.Change{Tool: t, Scope: accounts.FolderScope(s.Rule.Folder), Remove: true})
			if err != nil {
				return fixPlanMsg{err: err}
			}
			out.previews = append(out.previews, p)
		}
		out.doc = groupDoc(out.previews)
		out.summary = "Moved the rule on " + s.Rule.Folder + " to " + to
		return out
	}
}

// groupDoc is several previews as one: every sentence in order, every file
// once (the last edit of a file is what it will hold).
func groupDoc(ps []accounts.Preview) previewDoc {
	var d previewDoc
	byPath := map[string]int{}
	for _, p := range ps {
		d.sentences = append(d.sentences, p.Sentences...)
		d.warnings = append(d.warnings, p.Warnings...)
		if p.NoChange {
			continue
		}
		for _, e := range p.Edits {
			if i, ok := byPath[e.Path]; ok {
				d.edits[i] = e
				continue
			}
			byPath[e.Path] = len(d.edits)
			d.edits = append(d.edits, e)
		}
	}
	d.warnings = slices.Compact(d.warnings)
	d.noChange = len(d.edits) == 0
	return d
}

// fixIntro is the explanation above the old rules.
func (m browseScreen) fixIntro(ctx uictx.Context) []string {
	return wrap(ctx, ctx.Theme.Base, "These rules point at folders that are not there. Devpit never removes one by itself: the folder may be on a drive that is not plugged in.", 1, 0)
}

// fixTop is the body row the old rules start on.
func (m browseScreen) fixTop(ctx uictx.Context) int { return 2 + len(m.fixIntro(ctx)) + 1 }

// sizedFixes is the old rules at the height View draws them.
func (m browseScreen) sizedFixes(ctx uictx.Context) menu.Model {
	return m.fixes.SetHeight(max(3, ctx.BodyHeight-m.fixTop(ctx)-2))
}

func (m browseScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, m.Title(), "here: "+m.sess.folder), ""}
	switch m.stage {
	case bLoading, bFixPlanning:
		out = append(out, " "+th.Muted.Render("Reading the rules"+ellipsis(ctx)))
	case bTree:
		out = append(out, m.tree.View(ctx))
		if len(m.stale) > 0 {
			out = append(out, "", " "+th.Warning.Render(ctx.Icons.Warn+" "+plural(len(m.stale), "1 rule points at a folder that is not there.", "%d rules point at folders that are not there."))+
				"  "+ctx.KeyHint("x", "fix old rules"))
		} else if len(m.st.Rules) == 0 {
			out = append(out, "")
			out = append(out, wrap(ctx, th.Muted, "No folder has a rule yet. A folder rule is an account you chose for a folder and the folders inside it: open a tool on the Accounts page and pick \"Use another account here\" to make one.", 1, 0)...)
		}
	case bFix:
		out = append(out, m.fixIntro(ctx)...)
		out = append(out, "", m.sizedFixes(ctx).View(ctx), "")
		out = append(out, keyHints(ctx, "d", "remove the rule", "p", "point it at the folder's new place"))
	case bFixFolder:
		out = append(out, m.picker.View(ctx))
	case bFixChange:
		out = append(out, m.change.view(ctx, ctx.BodyHeight-2)...)
	case bFailed:
		out = append(out, errorView(ctx, m.err), "", keyHints(ctx, "enter", "back"))
	}
	return strings.Join(out, "\n")
}
