package accounts

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/confirm"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/pathpicker"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The change flow is one screen with stages, the path the plan draws in one
// line: pick an account (or sign in to a new one and name it) → "Where
// should this apply?" → the plain-words preview, default No → each step
// live → the done card with verify and undo. Esc leaves the flow at any
// stage but two: while a change is being applied (it finishes or is put
// back first), and after a sign-in that is not named yet (Esc asks whether
// to throw that sign-in away, so nothing is left behind unseen).

// flowKind is how the flow starts.
type flowKind int

const (
	// flowUse starts at the account picker.
	flowUse flowKind = iota
	// flowAdd starts with signing in to a new account.
	flowAdd
	// flowRemove previews removing a folder's rule.
	flowRemove
)

// flowStage is where the flow is.
type flowStage int

const (
	stLoading flowStage = iota
	stPick
	stNameFirst
	stIntro
	stName
	stSaving
	stDiscard
	stScope
	stFolder
	stPlanning
	stPreview
	stSecond
	stApplying
	stDone
	stOnce
	stFailed
)

// Dialog ids.
const (
	dlgApply   = "apply"
	dlgSecond  = "second"
	dlgDiscard = "discard"
)

// Rows of the account picker that are not an account.
const (
	pickAdd = "\x00add"
)

// Messages of the flow.
type (
	listMsg struct {
		list service.ListJSON
		err  error
	}
	savedMsg struct {
		acct accounts.Account
		err  error
	}
	planMsg struct {
		p   accounts.Preview
		err error
	}
	discardedMsg struct {
		note string
		err  error
	}
	// accountAddedMsg is a new account saved by a screen the flow opened
	// (the Git identity form), handed back as that screen closes.
	accountAddedMsg struct{ acct accounts.Account }
	// setupDoneMsg is the Claude setup the flow opened closing.
	setupDoneMsg struct{}
)

// flowKeys are the flow's own keys.
type flowKeys struct {
	Start, Save, Back, Home, Log, Verify, Undo, Copy, Retry key.Binding
}

// flow is the change flow.
type flow struct {
	sess  *session
	tool  accounts.Tool
	kind  flowKind
	scope accounts.ScopeKind
	depth int
	run   *runHandle
	ctx   context.Context
	keys  flowKeys

	stage flowStage
	frame int
	err   error

	// The picker.
	list   service.ListJSON
	filter textinput.Model
	pick   menu.Model
	chosen string

	// Signing in and naming.
	signRun liveRun
	signed  *accounts.Account
	name    textinput.Model
	nameErr string
	discard confirm.Model

	// Where.
	scopes menu.Model
	picker pathpicker.Model
	target accounts.Scope

	// The preview and the change.
	preview accounts.Preview
	pane    confirmPane
	second  confirm.Model
	live    liveRun
	showLog bool
}

// newFlow returns the flow for tool, with scope picked in advance (the
// person can still change it on the scope step).
func newFlow(s *session, t accounts.Tool, kind flowKind, scope accounts.ScopeKind, depth int) flow {
	h, ctx := newRunHandle()
	m := flow{sess: s, tool: t, kind: kind, scope: scope, depth: depth, run: h, ctx: ctx}
	m.keys = flowKeys{
		Start:  bind("start sign-in", "enter"),
		Save:   bind("save", "enter"),
		Back:   bind("back", "enter"),
		Home:   bind("back to Accounts", "enter"),
		Log:    bind("log", "l"),
		Verify: bind("verify", "v"),
		Undo:   bind("undo", "u"),
		Copy:   bind("copy", "c"),
		Retry:  bind("try again", "enter"),
	}
	m.filter = newInput("type to filter", 40)
	m.name = newInput("a short name, like work", 32)
	switch kind {
	case flowAdd:
		m.stage = stIntro
		if service.NameBeforeSignIn(t) {
			m.stage = stNameFirst
			m.name.Focus()
		}
	case flowRemove:
		m.stage = stPlanning
	default:
		m.stage = stLoading
	}
	return m
}

// newFlowAt returns the flow at "Where should this apply?" for an account
// already picked (one just added on another screen).
func newFlowAt(s *session, t accounts.Tool, account string, scope accounts.ScopeKind, depth int) flow {
	m := newFlow(s, t, flowUse, scope, depth)
	m.chosen = account
	next, _ := m.toScope()
	if f, ok := next.(flow); ok {
		return f
	}
	return m
}

// newRemoveRuleFlow previews removing tool's rule on folder.
func newRemoveRuleFlow(s *session, t accounts.Tool, folder string, depth int) flow {
	m := newFlow(s, t, flowRemove, accounts.ScopeFolder, depth)
	m.target = accounts.FolderScope(folder)
	return m
}

// Init implements uictx.Screen.
func (m flow) Init() tea.Cmd {
	switch m.stage {
	case stLoading:
		return tea.Batch(m.listCmd(), m.sess.spin())
	case stPlanning:
		return tea.Batch(m.planCmd(accounts.Change{Tool: m.tool, Scope: m.target, Remove: true}), m.sess.spin())
	case stNameFirst:
		return m.name.Focus()
	}
	return nil
}

// Title implements uictx.Screen.
func (m flow) Title() string {
	name := m.tool.DisplayName()
	switch {
	case m.kind == flowRemove:
		return name + " › Forget a folder's choice"
	case m.kind == flowAdd || m.stage == stIntro || m.stage == stName:
		return name + " › Sign in"
	case m.scope == accounts.ScopeOnce:
		return name + " › Just this once"
	}
	return name + " › Use another account"
}

// Busy implements uictx.BusyReporter: a change in flight finishes (or is
// put back) before anything else, and a sign-in waiting for its name owns
// Esc, so it is never left behind unseen.
func (m flow) Busy() bool {
	switch m.stage {
	case stApplying, stSaving:
		return !m.live.done()
	case stName, stDiscard:
		return !m.run.wasStopped()
	}
	return false
}

// Stop implements uictx.Stopper. During a change it cancels, and the engine
// puts back what was done; a sign-in not named yet is thrown away.
func (m flow) Stop() {
	if (m.stage == stName || m.stage == stDiscard) && m.signed != nil {
		svc, acct := m.sess.svc, *m.signed
		go func() { _, _ = svc.DiscardSignIn(acct) }()
	}
	m.run.stop()
}

// TerminalProgress implements uictx.ProgressReporter.
func (m flow) TerminalProgress() *tea.ProgressBar {
	if m.stage == stApplying {
		return m.live.progress()
	}
	return nil
}

// ShortHelp implements uictx.Screen.
func (m flow) ShortHelp() []key.Binding {
	switch m.stage {
	case stPick:
		return []key.Binding{m.pick.Keys.Up, m.pick.Keys.Select}
	case stNameFirst, stName:
		return []key.Binding{m.keys.Save}
	case stIntro:
		return []key.Binding{m.keys.Start}
	case stDiscard:
		return m.discard.Keys.ShortHelp()
	case stSecond:
		return m.second.Keys.ShortHelp()
	case stScope:
		return []key.Binding{m.scopes.Keys.Up, m.scopes.Keys.Select}
	case stFolder:
		return m.picker.Keys.ShortHelp()
	case stPreview:
		if m.pane.ask {
			return m.pane.dialog.Keys.ShortHelp()
		}
		return []key.Binding{m.keys.Back}
	case stApplying:
		return []key.Binding{m.keys.Log}
	case stDone:
		return []key.Binding{m.keys.Verify, m.keys.Undo, m.keys.Home, m.keys.Log}
	case stOnce:
		return []key.Binding{m.keys.Copy, m.keys.Back}
	case stFailed:
		if m.signed == nil && m.kind == flowAdd {
			return []key.Binding{m.keys.Retry}
		}
		return []key.Binding{m.keys.Back, m.keys.Log}
	}
	return nil
}

// FullHelp implements uictx.Screen.
func (m flow) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

// Update implements uictx.Screen.
func (m flow) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case spinMsg:
		switch m.stage {
		case stLoading, stPlanning, stApplying, stSaving:
			m.frame++
			return m, m.sess.spin()
		}
		return m, nil
	case listMsg:
		return m.onList(msg)
	case signedInMsg:
		return m.onSignedIn(msg)
	case savedMsg:
		return m.onSaved(msg)
	case accountAddedMsg:
		m.chosen = msg.acct.Name
		return m.toScope()
	case setupDoneMsg:
		return m, nil
	case discardedMsg:
		switch {
		case msg.err != nil:
			return m, tea.Batch(uictx.Pop(), uictx.Status("danger", "Devpit could not remove the sign-in's folder: "+accounts.Scrub(msg.err.Error())))
		case msg.note != "":
			return m, tea.Batch(uictx.Pop(), uictx.Status("warning", msg.note))
		}
		return m, tea.Batch(uictx.Pop(), uictx.Status("success", "The sign-in was thrown away. Nothing was saved."))
	case planMsg:
		return m.onPlan(msg)
	case runEventMsg:
		return m.onEvent(msg)
	case confirm.AnsweredMsg:
		return m.onAnswer(msg)
	case pathpicker.ChosenMsg:
		m.target = accounts.FolderScope(msg.Path)
		return m.plan()
	case pathpicker.CancelledMsg:
		m.stage = stScope
		return m, nil
	case pathpicker.BrowsedMsg:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	case menu.SelectedMsg:
		return m.onSelect(msg)
	case tea.KeyPressMsg:
		return m.onKey(msg, ctx)
	}
	return m.onPointer(msg, ctx)
}

// --- the picker ---

// listCmd lists the tool's accounts. It may run the tool's own list of
// logins (gh auth status), never a sign-in check.
func (m flow) listCmd() tea.Cmd {
	svc, t, folder := m.sess.svc, m.tool, m.sess.folder
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		l, err := svc.List(ctx, t, folder)
		return listMsg{list: l, err: err}
	}
}

func (m flow) onList(msg listMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage, m.err = stFailed, msg.err
		return m, nil
	}
	m.list = msg.list
	m.stage = stPick
	m.pick = menu.New(m.pickItems())
	for i, it := range m.pick.Items() {
		if strings.HasPrefix(it.Hint, "used here") {
			m.pick = m.pick.SetCursor(i)
		}
	}
	return m, m.filter.Focus()
}

// pickItems are the accounts that match the filter, then "sign in".
func (m flow) pickItems() []menu.Item {
	needle := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	var out []menu.Item
	for _, a := range m.list.Accounts {
		if needle != "" && !strings.Contains(strings.ToLower(a.Display+" "+a.Name), needle) {
			continue
		}
		it := menu.Item{ID: a.Name, Title: a.Display}
		var desc []string
		if a.HereNow {
			it.Hint = "used here"
		}
		if a.Everywhere {
			desc = append(desc, "used in folders without a choice of their own")
		}
		if len(a.Rules) > 0 {
			desc = append(desc, "chosen for "+strings.Join(a.Rules, ", "))
		}
		if a.Detected {
			it.Title = a.Name + " (" + firstNonEmpty(a.Login, a.Email) + ")"
			desc = []string{"signed in to " + m.tool.DisplayName() + " but not saved in Devpit yet; picking it saves it"}
		}
		it.Desc = strings.Join(desc, " · ")
		out = append(out, it)
	}
	add := menu.Item{ID: pickAdd, Title: "+ Sign in with another account", Desc: m.tool.DisplayName() + "'s own sign-in runs here, then you give the account a short name"}
	if m.tool == accounts.ToolGit {
		add = menu.Item{ID: pickAdd, Title: "+ Add a name and email", Desc: "A short form: the name and email to commit with"}
	}
	if caps := m.caps(); !caps.AddAccount {
		add.Disabled, add.Hint = true, "not available"
	}
	return append(out, add)
}

// caps is the tool's static support table; the tool page asked the tool.
func (m flow) caps() capsView {
	ts, _ := m.sess.status(m.tool)
	return capsView{ts.Supports.FolderRules, ts.Supports.Everywhere, ts.Supports.JustOnce, ts.Supports.AddAccount, ts.Supports.Why}
}

// capsView is the part of a tool's support the flow reads.
type capsView struct {
	FolderRules, Everywhere, JustOnce, AddAccount bool
	Why                                           string
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// onSelect acts on a pick from one of the flow's menus.
func (m flow) onSelect(msg menu.SelectedMsg) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case stPick:
		if msg.ID == pickAdd {
			return m.startAdd()
		}
		for _, a := range m.list.Accounts {
			if a.Name != msg.ID {
				continue
			}
			if a.Detected {
				acct := accounts.Account{Tool: m.tool, Name: a.Name, Label: a.Login, Email: a.Email}
				m.signed = &acct
				m.stage = stName
				m.name.SetValue(m.sess.svc.SuggestName(m.tool, firstNonEmpty(a.Email, a.Login)))
				m.name.CursorEnd()
				m.nameErr = m.checkName()
				return m, m.name.Focus()
			}
			m.chosen = a.Name
			if m.scope == accounts.ScopeOnce {
				m.target = accounts.OnceScope()
				return m.plan()
			}
			return m.toScope()
		}
	case stScope:
		switch msg.ID {
		case string(accounts.ScopeFolder):
			m.target = accounts.FolderScope(m.sess.folder)
		case string(accounts.ScopeEverywhere):
			m.target = accounts.EverywhereScope()
		case string(accounts.ScopeOnce):
			m.target = accounts.OnceScope()
		case scopeOther:
			m.stage = stFolder
			m.picker = folderPicker(m.sess, "Which folder should "+m.chosen+" be used in?", "Type the folder, then press Enter. Esc goes back to the list.")
			return m, nil
		}
		return m.plan()
	}
	return m, nil
}

// startAdd goes to signing in, or to the Git identity form.
func (m flow) startAdd() (uictx.Screen, tea.Cmd) {
	if m.tool == accounts.ToolGit {
		return m, uictx.Push(newIdentityScreen(m.sess, m.depth+1, true))
	}
	m.stage = stIntro
	if service.NameBeforeSignIn(m.tool) {
		m.stage = stNameFirst
		m.name.SetValue("")
		return m, m.name.Focus()
	}
	return m, nil
}

// --- signing in ---

// onSignedIn takes the sign-in's events: on success the account to name, on
// failure the reason (nothing was saved and nothing left behind).
func (m flow) onSignedIn(msg signedInMsg) (uictx.Screen, tea.Cmd) {
	r := liveRun{}
	for _, ev := range msg.res.events {
		r = r.add(ev, true)
	}
	m.signRun = r
	err := msg.err
	if err == nil {
		err = msg.res.err
	}
	if err == nil {
		err = r.err()
	}
	if err == nil && (r.final == nil || r.final.Account == nil) {
		err = accounts.ErrSignInCancelled
	}
	if err != nil {
		m.stage, m.err = stFailed, err
		return m, nil
	}
	acct := *r.final.Account
	m.signed = &acct
	if service.NameBeforeSignIn(m.tool) {
		return m.save(acct.Name)
	}
	m.stage = stName
	m.name.SetValue(m.sess.svc.SuggestName(m.tool, firstNonEmpty(acct.Email, acct.Label)))
	m.name.CursorEnd()
	m.nameErr = m.checkName()
	return m, m.name.Focus()
}

// checkName is the engine's verdict on the name typed so far, in words.
func (m flow) checkName() string {
	v := strings.TrimSpace(m.name.Value())
	if v == "" {
		return "Type a name."
	}
	if err := m.sess.svc.CheckNewName(m.tool, v); err != nil {
		return nameProblem(err)
	}
	return ""
}

// nameProblem says what is wrong with a name.
func nameProblem(err error) string {
	switch {
	case errors.Is(err, accounts.ErrReservedName):
		return "\"default\" is the tool's own sign-in. Pick another name."
	case errors.Is(err, accounts.ErrNameTaken):
		return "Another account already has this name."
	case errors.Is(err, accounts.ErrInvalidName):
		return "Use letters, digits and dashes only, starting with a letter."
	}
	return accounts.Scrub(err.Error())
}

// save saves the signed-in account under name.
func (m flow) save(name string) (uictx.Screen, tea.Cmd) {
	if m.signed == nil {
		return m, nil
	}
	m.stage = stSaving
	svc, acct := m.sess.svc, *m.signed
	return m, tea.Batch(func() tea.Msg {
		saved, _, err := svc.SaveAccount(acct, name)
		return savedMsg{acct: saved, err: err}
	}, m.sess.spin())
}

func (m flow) onSaved(msg savedMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage = stName
		m.nameErr = nameProblem(msg.err)
		return m, m.name.Focus()
	}
	m.signed = nil
	m.chosen = msg.acct.Name
	next, cmd := m.toScope()
	refresh := m.sess.refreshCmd(m.sess.folder, "")
	if m.tool == accounts.ToolClaude {
		// A new Claude Code account starts empty: offer to bring the setup
		// over right away. Closing that screen comes back to "where".
		return next, tea.Batch(cmd, refresh, uictx.Push(newClaudeScreen(m.sess, msg.acct.Name, m.depth+1, true)))
	}
	return next, tea.Batch(cmd, refresh)
}

// --- where ---

// scopeOther is the "Another folder…" row.
const scopeOther = "other"

// toScope asks "Where should this apply?", with the row the action named
// already picked.
func (m flow) toScope() (uictx.Screen, tea.Cmd) {
	m.stage = stScope
	c := m.caps()
	ts, _ := m.sess.status(m.tool)
	here := menu.Item{ID: string(accounts.ScopeFolder), Title: "This folder", Desc: m.sess.folder + " and every folder inside it"}
	if !c.FolderRules {
		here.Disabled, here.Hint = true, "not available"
	} else if m.sess.folderNote != "" {
		here.Disabled, here.Hint = true, "pick a folder"
		here.Desc = "This folder holds too much (a drive or your user folder): pick a folder inside it"
	}
	every := menu.Item{ID: string(accounts.ScopeEverywhere), Title: "Everywhere", Desc: "Every folder without a choice of its own. Now: " + accountPlain(ts.EverywhereDisplay, m.tool)}
	if !c.Everywhere {
		every.Disabled, every.Hint = true, "not available"
	}
	once := menu.Item{ID: string(accounts.ScopeOnce), Title: "Just this once", Desc: "One command, run from a terminal. Nothing is saved."}
	if !c.JustOnce {
		once.Disabled, once.Hint = true, "not available"
	}
	other := menu.Item{ID: scopeOther, Title: "Another folder", Desc: "Pick a folder; the choice covers it and every folder inside it"}
	if !c.FolderRules {
		other.Disabled, other.Hint = true, "not available"
	}
	items := []menu.Item{here, every, once, other}
	m.scopes = menu.New(items)
	want := string(m.scope)
	if m.sess.folderNote != "" && m.scope == accounts.ScopeFolder {
		want = scopeOther
	}
	for i, it := range items {
		if it.ID == want && !it.Disabled {
			m.scopes = m.scopes.SetCursor(i)
		}
	}
	return m, nil
}

// --- the preview ---

// plan previews using the chosen account in the chosen scope.
func (m flow) plan() (uictx.Screen, tea.Cmd) {
	m.stage = stPlanning
	return m, tea.Batch(m.planCmd(accounts.Change{Tool: m.tool, Account: m.chosen, Scope: m.target}), m.sess.spin())
}

func (m flow) planCmd(c accounts.Change) tea.Cmd {
	svc := m.sess.svc
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		p, err := svc.Plan(ctx, c)
		return planMsg{p: p, err: err}
	}
}

func (m flow) onPlan(msg planMsg) (uictx.Screen, tea.Cmd) {
	if msg.err != nil {
		m.stage, m.err = stFailed, msg.err
		return m, nil
	}
	m.preview = msg.p
	if msg.p.Change.Scope.Kind == accounts.ScopeOnce {
		m.stage = stOnce
		return m, nil
	}
	m.stage = stPreview
	m.pane = newConfirmPane(dlgApply, docOf(msg.p), "Make this change?", "You can undo it afterwards: u on the next screen.").readOnly(m.sess.readOnly)
	return m, nil
}

// onAnswer handles the dialogs.
func (m flow) onAnswer(msg confirm.AnsweredMsg) (uictx.Screen, tea.Cmd) {
	switch msg.ID {
	case dlgApply:
		if msg.Answer != confirm.AnswerYes {
			return m, uictx.Pop()
		}
		if m.preview.DriveRoot {
			m.stage = stSecond
			m.second = confirm.New(dlgSecond, m.preview.SecondConfirm, "Every project on the drive uses this account unless a folder has its own choice.")
			return m, nil
		}
		return m.apply()
	case dlgSecond:
		if msg.Answer != confirm.AnswerYes {
			return m, uictx.Pop()
		}
		return m.apply()
	case dlgDiscard:
		if msg.Answer != confirm.AnswerYes {
			m.stage = stName
			return m, m.name.Focus()
		}
		svc, acct := m.sess.svc, *m.signed
		// The sign-in is being thrown away now: leaving the screen must not
		// throw it away a second time.
		m.signed = nil
		return m, func() tea.Msg {
			note, err := svc.DiscardSignIn(acct)
			return discardedMsg{note: note, err: err}
		}
	}
	return m, nil
}

// apply makes the change, step by step.
func (m flow) apply() (uictx.Screen, tea.Cmd) {
	m.stage = stApplying
	m.live = startRun(m.sess.nextRun(), m.sess.svc.Apply(m.ctx, m.preview))
	return m, tea.Batch(m.live.wait(), m.sess.spin())
}

func (m flow) onEvent(msg runEventMsg) (uictx.Screen, tea.Cmd) {
	if msg.id != m.live.id {
		return m, nil
	}
	m.live = m.live.add(msg.ev, msg.ok)
	if !m.live.done() {
		return m, m.live.wait()
	}
	if m.live.ok() {
		m.stage = stDone
	} else {
		m.stage, m.err = stFailed, m.live.err()
	}
	return m, m.sess.refreshCmd(m.sess.folder, m.tool)
}

// --- keys and the mouse ---

func (m flow) onKey(msg tea.KeyPressMsg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	switch m.stage {
	case stPick:
		switch msg.String() {
		case "up", "down", "enter", "pgup", "pgdown":
			next, cmd := m.pick.Update(msg)
			m.pick = next
			return m, cmd
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.pick = m.pick.SetItems(m.pickItems()).SetCursor(0)
		return m, cmd
	case stNameFirst, stName:
		if msg.String() == "esc" && m.stage == stName {
			m.stage = stDiscard
			m.discard = confirm.New(dlgDiscard, "Throw away this sign-in?", "Nothing is saved. Devpit removes the folder the sign-in made.")
			return m, nil
		}
		if msg.String() == "enter" {
			m.nameErr = m.checkName()
			if m.nameErr != "" {
				return m, nil
			}
			name := strings.TrimSpace(m.name.Value())
			if m.stage == stNameFirst {
				m.stage = stIntro
				return m, m.signIn(name)
			}
			return m.save(name)
		}
		var cmd tea.Cmd
		m.name, cmd = m.name.Update(msg)
		m.nameErr = m.checkName()
		return m, cmd
	case stIntro:
		if key.Matches(msg, m.keys.Start) {
			return m, m.signIn(service.PlaceholderName())
		}
	case stDiscard:
		next, cmd := m.discard.Update(msg)
		m.discard = next
		return m, cmd
	case stSecond:
		next, cmd := m.second.Update(msg)
		m.second = next
		return m, cmd
	case stScope:
		next, cmd := m.scopes.Update(msg)
		m.scopes = next
		return m, cmd
	case stFolder:
		next, cmd := m.picker.Update(msg)
		m.picker = next
		return m, cmd
	case stPreview:
		if !m.pane.ask && msg.String() == "enter" {
			return m, uictx.Pop()
		}
		next, cmd := m.pane.update(msg)
		m.pane = next
		return m, cmd
	case stApplying:
		if key.Matches(msg, m.keys.Log) {
			m.showLog = !m.showLog
		}
		// Esc does nothing here: the change finishes, or is put back,
		// before anything else happens.
		return m, nil
	case stDone:
		switch {
		case key.Matches(msg, m.keys.Verify):
			return m, uictx.Push(newVerifyScreen(m.sess, m.depth+1))
		case key.Matches(msg, m.keys.Undo):
			return m, uictx.Push(newUndoScreen(m.sess, m.depth+1))
		case key.Matches(msg, m.keys.Home):
			return m, popN(m.depth)
		case key.Matches(msg, m.keys.Log):
			m.showLog = !m.showLog
		}
	case stOnce:
		switch {
		case key.Matches(msg, m.keys.Copy):
			return m, m.sess.opts.Copy(onceCommand(m.tool, m.chosen))
		case key.Matches(msg, m.keys.Back):
			return m, uictx.Pop()
		}
	case stFailed:
		switch {
		case key.Matches(msg, m.keys.Log):
			m.showLog = !m.showLog
		case msg.String() == "enter" && m.kind == flowAdd && m.signed == nil && m.chosen == "":
			m.err = nil
			return m.startAdd()
		case msg.String() == "enter":
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// signIn hands the terminal to the tool's own sign-in.
func (m flow) signIn(name string) tea.Cmd {
	return signInCmd(m.ctx, m.sess, m.tool, name)
}

// onPointer routes the mouse to whatever is on screen.
func (m flow) onPointer(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	top := m.contentTop()
	switch m.stage {
	case stPick:
		mm := m.pick.SetHeight(m.pickHeight(ctx))
		if next, cmd, ok := mm.Pointer(ctx, msg, top+2); ok {
			m.pick = next
			return m, cmd
		}
		if wm, ok := msg.(tea.MouseWheelMsg); ok {
			m.pick, _ = m.pick.Update(wm)
		}
	case stScope:
		if next, cmd, ok := m.scopes.Pointer(ctx, msg, top+2); ok {
			m.scopes = next
			return m, cmd
		}
		if wm, ok := msg.(tea.MouseWheelMsg); ok {
			m.scopes, _ = m.scopes.Update(wm)
		}
	case stFolder:
		if next, cmd, ok := m.picker.Pointer(ctx, msg, top); ok {
			m.picker = next
			return m, cmd
		}
	case stPreview:
		if cm, ok := msg.(tea.MouseClickMsg); ok && cm.Button == tea.MouseLeft {
			next, cmd := m.pane.click(ctx, ctx.BodyHeight-top, cm.X, ctx.BodyRow(cm.Y)-top)
			m.pane = next
			return m, cmd
		}
		if wm, ok := msg.(tea.MouseWheelMsg); ok {
			m.pane, _ = m.pane.update(wm)
		}
	case stDiscard, stSecond:
		if cm, ok := msg.(tea.MouseClickMsg); ok && cm.Button == tea.MouseLeft {
			dlg := m.discard
			if m.stage == stSecond {
				dlg = m.second
			}
			next, cmd := dlg.Click(ctx, cm.X, ctx.BodyRow(cm.Y)-m.dialogTop(ctx))
			if m.stage == stSecond {
				m.second = next
			} else {
				m.discard = next
			}
			return m, cmd
		}
	}
	return m, nil
}

// --- views ---

// contentTop is the body row a stage's own content starts on, under the
// heading and the blank line.
func (m flow) contentTop() int { return 2 }

// pickHeight is the room for the account list.
func (m flow) pickHeight(ctx uictx.Context) int { return max(3, ctx.BodyHeight-m.contentTop()-4) }

// question is the heading of the stage.
func (m flow) question() string {
	name := m.tool.DisplayName()
	switch m.stage {
	case stLoading, stPick:
		if m.scope == accounts.ScopeOnce {
			return "Which " + name + " account, just this once?"
		}
		return "Which " + name + " account?"
	case stNameFirst:
		return "Name the new " + name + " account"
	case stIntro:
		return "Sign in to another " + name + " account"
	case stName, stSaving, stDiscard:
		return "Name the new " + name + " account"
	case stScope, stFolder:
		return "Where should " + m.chosen + " be used?"
	case stPlanning, stPreview, stSecond:
		if m.kind == flowRemove {
			return "Forget this folder's choice?"
		}
		return "Here is what will change"
	case stApplying:
		return "Making the change"
	case stDone:
		return "Done"
	case stOnce:
		return "Just this once"
	}
	// Failed: say what was being done; the error says what went wrong.
	switch {
	case len(m.live.rows) > 0:
		return "Making the change"
	case len(m.signRun.rows) > 0:
		return "Sign in to another " + name + " account"
	}
	return "That did not work"
}

// dialogTop is where a lone dialog (discard, second confirm) is drawn.
func (m flow) dialogTop(ctx uictx.Context) int {
	if m.stage == stSecond {
		_, top := m.pane.layout(ctx, ctx.BodyHeight-m.contentTop())
		return m.contentTop() + top
	}
	return m.contentTop() + 4
}

// View implements uictx.Screen.
func (m flow) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, m.question(), "in "+m.sess.folder), ""}
	room := ctx.BodyHeight - m.contentTop()
	switch m.stage {
	case stLoading, stPlanning, stSaving:
		word := map[flowStage]string{stLoading: "Looking up the accounts", stPlanning: "Working out what would change", stSaving: "Saving the account"}[m.stage]
		out = append(out, " "+th.Muted.Render(ctx.SpinnerFrame(m.frame)+" "+word+ellipsis(ctx)))
	case stPick:
		f := styleInput(m.filter, ctx)
		out = append(out, " "+f.View(), "")
		out = append(out, m.pick.SetHeight(m.pickHeight(ctx)).View(ctx))
	case stNameFirst:
		out = append(out, wrap(ctx, th.Base, m.tool.DisplayName()+" names the account when it is made, so pick its name first.", 1, 0)...)
		out = append(out, "")
		out = append(out, m.nameField(ctx)...)
	case stIntro:
		out = append(out, m.introView(ctx)...)
	case stName:
		out = append(out, m.signedView(ctx)...)
		out = append(out, "", " "+th.Base.Render("What should Devpit call this account?"))
		out = append(out, m.nameField(ctx)...)
		out = append(out, "", keyHints(ctx, "enter", "save", "esc", "throw this sign-in away"))
	case stDiscard:
		out = append(out, m.signedView(ctx)...)
		out = append(out, "", m.discard.View(ctx))
	case stScope:
		// One line on what follows, then the choices, which the mouse finds
		// two rows under the heading.
		out = append(out, " "+th.Muted.Render(fit(ctx, "Then you see a preview. Nothing changes until you say yes.", ctx.Width-2)), "")
		out = append(out, m.scopes.View(ctx))
	case stFolder:
		out = append(out, m.picker.View(ctx))
	case stPreview:
		ls, _ := m.pane.layout(ctx, room)
		out = append(out, ls...)
	case stSecond:
		ls, top := m.pane.layout(ctx, room)
		out = append(out, ls[:top]...)
		out = append(out, m.second.View(ctx))
	case stApplying:
		out = append(out, m.live.view(ctx, m.frame, room-1, m.showLog))
	case stDone:
		out = append(out, m.doneView(ctx, room)...)
	case stOnce:
		out = append(out, m.onceView(ctx)...)
	case stFailed:
		out = append(out, errorViewAs(ctx, m.err, "The change did not finish"))
		if len(m.live.rows) > 0 || len(m.signRun.rows) > 0 {
			r := m.live
			if len(r.rows) == 0 {
				r = m.signRun
			}
			out = append(out, "", r.view(ctx, m.frame, max(1, room-12), m.showLog))
		}
	}
	return strings.Join(out, "\n")
}

// nameField is the name input with its live verdict.
func (m flow) nameField(ctx uictx.Context) []string {
	th := ctx.Theme
	f := styleInput(m.name, ctx)
	out := []string{" " + f.View()}
	if m.nameErr != "" && strings.TrimSpace(m.name.Value()) != "" {
		out = append(out, "   "+th.Danger.Render(ctx.Icons.Fail+" "+m.nameErr))
	} else {
		out = append(out, wrap(ctx, th.Muted, "Letters, digits and dashes. You type it in commands, like devpit "+string(m.tool)+" use work.", 3, 0)...)
	}
	return out
}

// introView explains the sign-in before the terminal is handed over.
func (m flow) introView(ctx uictx.Context) []string {
	th := ctx.Theme
	name := m.tool.DisplayName()
	cmdName := strings.TrimSpace(m.name.Value())
	out := wrap(ctx, th.Base, "Devpit hands this window to "+name+"'s own sign-in:", 1, 0)
	out = append(out, "   "+th.Info.Render(signInCommand(m.tool, cmdName)), "")
	out = append(out, wrap(ctx, th.Base, signInWhere(m.tool)+" When it is done, Devpit comes back here and shows who signed in.", 1, 0)...)
	out = append(out, "")
	out = append(out, wrap(ctx, th.Muted, "Nothing is saved until you name the account. Ctrl+C in the sign-in stops it, and nothing is left behind.", 1, 0)...)
	return append(out, "", keyHints(ctx, "enter", "start sign-in", "esc", "back"))
}

// signedView says who signed in, and anything worth knowing about it (the
// same login twice, gh switching its active account).
func (m flow) signedView(ctx uictx.Context) []string {
	th := ctx.Theme
	who := ""
	if m.signed != nil {
		who = firstNonEmpty(m.signed.Email, m.signed.Label, m.signed.Name)
	}
	out := []string{" " + th.Success.Render(ctx.Icons.Tick) + " " + th.Base.Bold(true).Render("Signed in as "+who)}
	for _, w := range m.signRun.warnings {
		d := w.Detail
		// The engine names the account by the temporary name it signed in
		// under; it has no name yet, so say "This account".
		if m.signed != nil && strings.HasPrefix(d, m.signed.Name+" ") {
			d = "This account" + strings.TrimPrefix(d, m.signed.Name)
		}
		out = append(out, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+w.Step+": "+d, 1, 2)...)
	}
	return out
}

// doneView is the done card: what changed, the steps, what to know, and
// what to do next.
func (m flow) doneView(ctx uictx.Context, room int) []string {
	th := ctx.Theme
	head := " " + th.Success.Render(ctx.Icons.Tick) + " " + th.CardTitle.Render(fit(ctx, m.preview.Summary, ctx.Width-6))
	out := []string{head, ""}
	var notes []string
	for _, w := range m.live.warnings {
		notes = append(notes, wrap(ctx, th.Warning, ctx.Icons.Warn+" "+w.Step+". "+w.Detail, 1, 2)...)
	}
	rowsRoom := max(1, room-len(notes)-6)
	out = append(out, m.live.view(ctx, m.frame, rowsRoom, m.showLog))
	if len(notes) > 0 {
		out = append(out, "")
		out = append(out, notes...)
	}
	out = append(out, "", keyHints(ctx, "v", "verify", "u", "undo", "enter", "back to Accounts"))
	return out
}

// onceView is "just this once": the engine's sentence and the command to
// run in a terminal, since a full-screen menu cannot hand a command over.
func (m flow) onceView(ctx uictx.Context) []string {
	th := ctx.Theme
	out := docOf(m.preview).render(ctx)
	out = append(out, "")
	out = append(out, wrap(ctx, th.Base, "Run it from any terminal. Put your own command after --:", 1, 0)...)
	out = append(out, strings.Split(card(ctx, th.Info.Render(onceCommand(m.tool, m.chosen))), "\n")...)
	out = append(out, wrap(ctx, th.Muted, "For example: "+onceCommand(m.tool, m.chosen)+onceArgs(m.tool), 1, 0)...)
	return append(out, "", keyHints(ctx, "c", "copy the command", "enter", "back"))
}

// onceCommand is the start of the "just this once" command for an account.
func onceCommand(t accounts.Tool, name string) string {
	return "devpit " + string(t) + " run " + name + " -- " + t.Binary()
}

// onceArgs is an example tail for onceCommand.
func onceArgs(t accounts.Tool) string {
	switch t {
	case accounts.ToolClaude:
		return " -p \"summarise this repo\""
	case accounts.ToolGitHub:
		return " pr list"
	}
	return " deploy"
}
