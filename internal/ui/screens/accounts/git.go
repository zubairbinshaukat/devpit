package accounts

import (
	"context"
	"errors"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The Git page answers the two questions people mix up, as two rows: who a
// commit is made as here (name and email, and which file decided it), and
// which GitHub account a push uses (or that the SSH key decides, for an SSH
// remote). Everything the old Git & SSH screen did is here: see the
// identity, set a new one, make an SSH key and copy its public half.

// gitFactsMsg is what Git says about the folder, offline.
type gitFactsMsg struct {
	commits    adapters.GitCommitIdentity
	commitsErr error
	pushes     adapters.GitHubPush
	pushesErr  error
}

// Git page actions.
const (
	gitHere       = "git-here"
	gitEverywhere = "git-everywhere"
	gitAdd        = "git-add"
	gitHubHere    = "github-here"
	gitSSH        = "ssh"
	gitRemoveRule = "git-remove"
	gitManage     = "git-manage"
)

// gitScreen is the Git page.
type gitScreen struct {
	sess   *session
	depth  int
	loaded bool
	facts  gitFactsMsg
	menu   menu.Model
}

func newGitScreen(s *session, depth int) gitScreen {
	m := gitScreen{sess: s, depth: depth}
	m.menu = menu.New(m.items()).DescOnSelectedOnly(true)
	return m
}

func (m gitScreen) Init() tea.Cmd { return m.factsCmd() }

// factsCmd asks Git about the folder: config reads only, nothing changes.
func (m gitScreen) factsCmd() tea.Cmd {
	svc, folder := m.sess.svc, m.sess.folder
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		var f gitFactsMsg
		f.commits, f.commitsErr = svc.CommitsAs(ctx, folder)
		f.pushes, f.pushesErr = svc.PushesAs(ctx, folder)
		return f
	}
}

func (m gitScreen) Title() string { return "Git" }

func (m gitScreen) ShortHelp() []key.Binding {
	return []key.Binding{m.menu.Keys.Up, m.menu.Keys.Select}
}

func (m gitScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m gitScreen) items() []menu.Item {
	ts, _ := m.sess.status(accounts.ToolGit)
	out := []menu.Item{
		{ID: gitHere, Title: "Commit as another identity here…", Desc: "In this folder and every folder inside it"},
		{ID: gitEverywhere, Title: "Commit as another identity everywhere…", Desc: "Wherever no folder rule says otherwise"},
		{ID: gitAdd, Title: "Add another identity…", Desc: "A name and email to commit as, with suggestions from GitHub"},
		{ID: gitHubHere, Title: "Push as another GitHub account here…", Desc: "Pushes over HTTPS and gh follow the folder"},
		{ID: gitSSH, Title: "Push with an SSH key…", Desc: "Make a key, copy it for GitHub, and use it for this folder"},
	}
	if ts.Resolution.Reason == accounts.ReasonFolderRule {
		out = append(out, menu.Item{ID: gitRemoveRule, Title: "Remove the rule on " + ts.Resolution.RuleFolder, Desc: "This folder then follows the next rule out, or everywhere"})
	}
	if !ts.Installed {
		for i := range out {
			out[i].Disabled, out[i].Hint = true, "Git is not installed"
		}
	}
	return append(out, menu.Item{ID: gitManage, Title: "Manage identities…", Desc: "Rename or remove an identity"})
}

func (m gitScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		m.menu = m.menu.SetItems(m.items())
		return m, m.factsCmd()
	}
	switch msg := msg.(type) {
	case gitFactsMsg:
		m.facts, m.loaded = msg, true
		return m, nil
	case accountAddedMsg:
		return m, uictx.Push(newFlowAt(m.sess, accounts.ToolGit, msg.acct.Name, accounts.ScopeFolder, m.depth+1))
	case menu.SelectedMsg:
		return m, m.act(msg.ID)
	}
	if next, cmd, ok := m.sizedMenu(ctx).Pointer(ctx, msg, m.menuTop(ctx)); ok {
		m.menu = next
		return m, cmd
	}
	next, cmd := m.menu.Update(msg)
	m.menu = next
	return m, cmd
}

func (m gitScreen) act(id string) tea.Cmd {
	d := m.depth + 1
	switch id {
	case gitHere:
		return uictx.Push(newFlow(m.sess, accounts.ToolGit, flowUse, accounts.ScopeFolder, d))
	case gitEverywhere:
		return uictx.Push(newFlow(m.sess, accounts.ToolGit, flowUse, accounts.ScopeEverywhere, d))
	case gitAdd:
		return uictx.Push(newIdentityScreen(m.sess, d, false))
	case gitHubHere:
		return uictx.Push(newFlow(m.sess, accounts.ToolGitHub, flowUse, accounts.ScopeFolder, d))
	case gitSSH:
		return uictx.Push(newSSHScreen(m.sess, m.facts.pushes, d))
	case gitRemoveRule:
		ts, _ := m.sess.status(accounts.ToolGit)
		return uictx.Push(newRemoveRuleFlow(m.sess, accounts.ToolGit, ts.Resolution.RuleFolder, d))
	case gitManage:
		return uictx.Push(newManageScreen(m.sess, accounts.ToolGit, d))
	}
	return nil
}

// rows are the two answers: commits as, pushes as.
func (m gitScreen) rows(ctx uictx.Context) []string {
	th := ctx.Theme
	label := func(s string) string { return " " + th.Muted.Render(padTo(s, 12)) }
	cont := pad(13)
	w := ctx.Width - 14
	ts, _ := m.sess.status(accounts.ToolGit)
	if !ts.Installed {
		return []string{label("Commits as") + th.Muted.Render(ctx.Icons.Absent+" Git is not installed")}
	}
	if !m.loaded {
		return []string{label("Commits as") + th.Base.Render(fit(ctx, ts.Display, w)), cont + th.Muted.Render("asking Git"+ellipsis(ctx))}
	}
	var out []string
	ci := m.facts.commits
	switch {
	case m.facts.commitsErr != nil:
		out = append(out, label("Commits as")+th.Base.Render(fit(ctx, ts.Display, w)))
		out = append(out, cont+th.Warning.Render(fit(ctx, ctx.Icons.Warn+" "+accounts.Scrub(m.facts.commitsErr.Error()), w)))
	default:
		who := strings.TrimSpace(ci.Name.Value + " <" + ci.Email.Value + ">")
		if ci.Email.Value == "" {
			who = "no email set"
		}
		st := th.Base.Bold(true)
		if ci.Mismatch {
			st = th.Danger
			who = ctx.Icons.Fail + " " + who
		}
		out = append(out, label("Commits as")+st.Render(fit(ctx, who, w)))
		from := "from " + string(ci.Email.From)
		if ci.Expected.Reason == accounts.ReasonFolderRule {
			from += sep(ctx) + "rule on " + ci.Expected.RuleFolder
		}
		out = append(out, cont+th.Muted.Render(fit(ctx, from, w)))
		if !ci.Repo.IsRepo && len(ci.Notes) == 0 {
			out = append(out, cont+th.Muted.Render(fit(ctx, "applies once this folder is a Git repo", w)))
		}
		for _, n := range ci.Notes {
			out = append(out, wrap(ctx, th.Warning, n, 13, 0)...)
		}
	}
	out = append(out, "")
	p := m.facts.pushes
	gh, _ := m.sess.status(accounts.ToolGitHub)
	switch {
	case m.facts.pushesErr != nil && !errors.Is(m.facts.pushesErr, accounts.ErrNotFoundTool):
		out = append(out, label("Pushes as")+th.Base.Render(fit(ctx, "GitHub: "+gh.Display, w)))
		out = append(out, cont+th.Warning.Render(fit(ctx, ctx.Icons.Warn+" "+accounts.Scrub(m.facts.pushesErr.Error()), w)))
	case p.Via == adapters.PushViaSSHKey:
		key := "the keys ssh offers by default"
		if p.SSHKey != "" {
			key = p.SSHKey
		}
		out = append(out, label("Pushes as")+th.Base.Bold(true).Render(fit(ctx, "follows the SSH key", w)))
		out = append(out, cont+th.Muted.Render(fit(ctx, "this repo pushes over SSH; key: "+key, w)))
	default:
		out = append(out, label("Pushes as")+th.Base.Bold(true).Render(fit(ctx, "GitHub: "+gh.Display, w)))
		via := "over HTTPS"
		if p.Via != "" {
			via = string(p.Via)
		}
		out = append(out, cont+th.Muted.Render(fit(ctx, gh.Why+sep(ctx)+via, w)))
		for _, n := range p.Notes {
			out = append(out, wrap(ctx, th.Muted, n, 13, 0)...)
		}
	}
	return out
}

func (m gitScreen) menuTop(ctx uictx.Context) int { return 2 + len(m.rows(ctx)) + 1 }

// sizedMenu is the action list at the height View draws it.
func (m gitScreen) sizedMenu(ctx uictx.Context) menu.Model {
	return m.menu.SetHeight(tightHeight(m.menu, ctx.BodyHeight-m.menuTop(ctx)))
}

func (m gitScreen) View(ctx uictx.Context) string {
	out := []string{heading(ctx, "Git", "in "+m.sess.folder), ""}
	out = append(out, m.rows(ctx)...)
	out = append(out, "", m.sizedMenu(ctx).View(ctx))
	return strings.Join(out, "\n")
}

// --- a new identity ---

// suggestMsg is GitHub's suggested commit addresses.
type suggestMsg struct {
	list []adapters.EmailSuggestion
	err  error
}

// identitySavedMsg is a new identity saved.
type identitySavedMsg struct {
	acct accounts.Account
	err  error
}

// identityScreen is the form for a new Git identity: a name for it in
// Devpit, the name commits are made under, and the email, with GitHub's
// private noreply address and verified addresses offered.
type identityScreen struct {
	sess     *session
	depth    int
	fromFlow bool
	fields   [3]textinput.Model
	focus    int
	sugg     []adapters.EmailSuggestion
	asked    bool
	suggMenu menu.Model
	err      string
	saving   bool
	keys     struct{ Next, Prev, Save key.Binding }
}

func newIdentityScreen(s *session, depth int, fromFlow bool) identityScreen {
	m := identityScreen{sess: s, depth: depth, fromFlow: fromFlow}
	m.fields[0] = newInput("for example oss", 32)
	m.fields[1] = newInput("the name in your global Git config", 80)
	m.fields[2] = newInput("you@example.com", 120)
	m.keys.Next = bind("next field", "tab")
	m.keys.Prev = bind("previous field", "shift+tab")
	m.keys.Save = bind("save", "enter")
	m.fields[0].Focus()
	return m
}

func (m identityScreen) Init() tea.Cmd {
	svc := m.sess.svc
	return tea.Batch(func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		l, err := svc.SuggestEmails(ctx)
		return suggestMsg{list: l, err: err}
	})
}

func (m identityScreen) Title() string { return "Git › New identity" }

func (m identityScreen) ShortHelp() []key.Binding {
	return []key.Binding{m.keys.Next, m.keys.Save}
}

func (m identityScreen) FullHelp() [][]key.Binding {
	return [][]key.Binding{{m.keys.Next, m.keys.Prev, m.keys.Save}}
}

func (m identityScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case suggestMsg:
		m.asked, m.sugg = true, msg.list
		items := make([]menu.Item, len(msg.list))
		for i, s := range msg.list {
			items[i] = menu.Item{ID: s.Email, Title: s.Email, Desc: s.Why}
		}
		m.suggMenu = menu.New(items).DescOnSelectedOnly(true)
		return m, nil
	case menu.SelectedMsg:
		m.fields[2].SetValue(msg.ID)
		m.fields[2].CursorEnd()
		return m.focusOn(2)
	case identitySavedMsg:
		m.saving = false
		if msg.err != nil {
			m.err = nameProblem(msg.err)
			return m, nil
		}
		refresh := m.sess.refreshCmd(m.sess.folder, accounts.ToolGit)
		if m.fromFlow {
			return m, tea.Batch(refresh, then(1, accountAddedMsg{acct: msg.acct}))
		}
		return m, tea.Batch(refresh, uictx.Replace(newFlowAt(m.sess, accounts.ToolGit, msg.acct.Name, accounts.ScopeFolder, m.depth)))
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keys.Next):
			return m.focusOn((m.focus + 1) % m.stops())
		case key.Matches(msg, m.keys.Prev):
			return m.focusOn((m.focus + m.stops() - 1) % m.stops())
		}
		if m.focus == 3 {
			next, cmd := m.suggMenu.Update(msg)
			m.suggMenu = next
			return m, cmd
		}
		if msg.String() == "enter" {
			return m.save()
		}
		var cmd tea.Cmd
		m.fields[m.focus], cmd = m.fields[m.focus].Update(msg)
		m.err = ""
		return m, cmd
	}
	if len(m.sugg) > 0 {
		if next, cmd, ok := m.suggMenu.Pointer(ctx, msg, m.suggTop()); ok {
			m.suggMenu = next
			return m, cmd
		}
	}
	return m, nil
}

// stops is how many places Tab moves between: three fields, and the
// suggestions when there are any.
func (m identityScreen) stops() int {
	if len(m.sugg) > 0 {
		return 4
	}
	return 3
}

func (m identityScreen) focusOn(i int) (uictx.Screen, tea.Cmd) {
	m.focus = i
	var cmd tea.Cmd
	for k := range m.fields {
		if k == i {
			cmd = m.fields[k].Focus()
		} else {
			m.fields[k].Blur()
		}
	}
	return m, cmd
}

// save adds the identity: Git's "sign-in" is typed, so it runs no tool
// except a config read for the name when it is left empty.
func (m identityScreen) save() (uictx.Screen, tea.Cmd) {
	name := strings.TrimSpace(m.fields[0].Value())
	email := strings.TrimSpace(m.fields[2].Value())
	switch {
	case name == "":
		m.err = "Give the identity a name in Devpit, like work."
		return m, nil
	case email == "":
		m.err = "Type the email Git should commit with, or pick one below."
		return m, nil
	}
	if err := m.sess.svc.CheckNewName(accounts.ToolGit, name); err != nil {
		m.err = nameProblem(err)
		return m, nil
	}
	m.saving = true
	svc := m.sess.svc
	display := strings.TrimSpace(m.fields[1].Value())
	return m, func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		ch, err := svc.SignIn(ctx, accounts.ToolGit, adapters.LoginRequest{Name: name, Email: email, DisplayName: display})
		if err != nil {
			return identitySavedMsg{err: err}
		}
		var last accounts.Event
		for ev := range ch {
			last = ev
		}
		if last.Account == nil {
			if last.Err != nil {
				return identitySavedMsg{err: last.Err}
			}
			return identitySavedMsg{err: errors.New("the identity was not added")}
		}
		acct, _, err := svc.SaveAccount(*last.Account, name)
		return identitySavedMsg{acct: acct, err: err}
	}
}

func (m identityScreen) suggTop() int { return 2 + 3*3 + 2 }

func (m identityScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, "A new Git identity", ""), ""}
	labels := [3]string{"Name in Devpit", "Commit name", "Email"}
	hints := [3]string{"what you type in commands, like devpit git use work", "leave empty to keep the name in your global Git config", "the address commits are made with"}
	for i := range m.fields {
		st := th.Muted
		if i == m.focus {
			st = th.Accent
		}
		out = append(out, " "+st.Render(labels[i])+"  "+th.Muted.Render(hints[i]))
		out = append(out, " "+styleInput(m.fields[i], ctx).View(), "")
	}
	switch {
	case !m.asked:
		out = append(out, " "+th.Muted.Render("Looking up the addresses your GitHub accounts can use"+ellipsis(ctx)))
	case len(m.sugg) == 0:
		out = append(out, wrap(ctx, th.Muted, "No GitHub account to suggest an address from. Sign in to GitHub on the Accounts page to get its private noreply address here.", 1, 0)...)
	default:
		out = append(out, " "+th.Muted.Render("Suggested by your GitHub accounts (Tab to here, Enter to pick):"))
		out = append(out, m.suggMenu.SetHeight(max(2, ctx.BodyHeight-m.suggTop()-3)).View(ctx))
	}
	if m.err != "" {
		out = append(out, "", " "+th.Danger.Render(ctx.Icons.Fail+" "+m.err))
	}
	if m.saving {
		out = append(out, "", " "+th.Muted.Render("Saving"+ellipsis(ctx)))
	}
	return strings.Join(out, "\n")
}

// --- an SSH key ---

// sshStage is where the SSH key screen is.
type sshStage int

const (
	sshChoose sshStage = iota
	sshMaking
	sshMade
	sshExisting
	sshTyping
	sshPlanning
	sshChange
	sshFailed
)

// Messages of the SSH key screen.
type (
	keyMadeMsg struct {
		res gitssh.KeygenResult
		err error
	}
	keyPlanMsg struct {
		p   adapters.GitSettingsPreview
		err error
	}
)

// SSH key choices.
const (
	sshNew    = "new"
	sshHave   = "have"
	sshRemove = "remove"
)

// sshScreen makes a key for pushes from this folder, shows its public half
// to add to GitHub, and sets it for the folder after a preview.
type sshScreen struct {
	sess   *session
	depth  int
	run    *runHandle
	ctx    context.Context
	push   adapters.GitHubPush
	stage  sshStage
	path   string
	choose menu.Model
	input  textinput.Model
	made   gitssh.KeygenResult
	err    error
	change change
	keys   struct{ Copy, Use, Back key.Binding }
}

func newSSHScreen(s *session, push adapters.GitHubPush, depth int) sshScreen {
	h, ctx := newRunHandle()
	m := sshScreen{sess: s, depth: depth, run: h, ctx: ctx, push: push}
	name := "devpit"
	if gh, ok := s.status(accounts.ToolGitHub); ok && !gh.Account.IsDefault() {
		name = gh.Account.Name
	}
	m.path = s.svc.SuggestedSSHKeyPath(name)
	items := []menu.Item{
		{ID: sshNew, Title: "Make a new key", Desc: m.path},
		{ID: sshHave, Title: "Use a key I already have…", Desc: "Type the path of its private key"},
	}
	if push.SSHKey != "" {
		items = append(items, menu.Item{ID: sshRemove, Title: "Stop using " + push.SSHKey + " here", Desc: "Pushes over SSH use your usual keys again"})
	}
	m.choose = menu.New(items)
	m.input = newInput(`C:\Users\you\.ssh\id_ed25519`, 260)
	m.keys.Copy = bind("copy the public key", "c")
	m.keys.Use = bind("use it for this folder", "enter")
	m.keys.Back = bind("back", "enter")
	return m
}

func (m sshScreen) Init() tea.Cmd { return nil }

func (m sshScreen) Title() string { return "Git › SSH key" }

func (m sshScreen) Busy() bool { return m.stage == sshChange && m.change.busy() }

func (m sshScreen) Stop() { m.run.stop() }

func (m sshScreen) TerminalProgress() *tea.ProgressBar { return m.change.progress() }

func (m sshScreen) ShortHelp() []key.Binding {
	switch m.stage {
	case sshChoose:
		return []key.Binding{m.choose.Keys.Up, m.choose.Keys.Select}
	case sshMade:
		return []key.Binding{m.keys.Copy, m.keys.Use}
	case sshExisting:
		return []key.Binding{m.keys.Use}
	case sshTyping:
		return []key.Binding{m.keys.Use}
	case sshChange:
		return m.change.help()
	case sshFailed:
		return []key.Binding{m.keys.Back}
	}
	return nil
}

func (m sshScreen) FullHelp() [][]key.Binding { return [][]key.Binding{m.ShortHelp()} }

func (m sshScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		return m, nil
	}
	switch msg := msg.(type) {
	case menu.SelectedMsg:
		switch msg.ID {
		case sshNew:
			m.stage = sshMaking
			return m, m.makeCmd()
		case sshHave:
			m.stage = sshTyping
			return m, m.input.Focus()
		case sshRemove:
			m.stage = sshPlanning
			return m, m.planCmd("")
		}
	case keyMadeMsg:
		var exists *gitssh.ExistsError
		switch {
		case errors.As(msg.err, &exists):
			m.stage = sshExisting
		case msg.err != nil:
			m.stage, m.err = sshFailed, msg.err
		default:
			m.stage, m.made = sshMade, msg.res
			m.path = msg.res.Path
		}
		return m, nil
	case keyPlanMsg:
		if msg.err != nil {
			m.stage, m.err = sshFailed, msg.err
			return m, nil
		}
		svc, p := m.sess.svc, msg.p
		doc := previewDoc{sentences: p.Sentences, warnings: p.Warnings, edits: p.Edits, noChange: p.NoChange}
		m.change = newChange(m.ctx, m.sess, doc, "Make this change?", "You can undo it afterwards with u on the Accounts page.", p.Summary,
			func(ctx context.Context) <-chan accounts.Event { return svc.ApplyGitSettings(ctx, p) })
		m.change.fresh = accounts.ToolGitHub
		m.stage = sshChange
		return m, nil
	}
	switch m.stage {
	case sshChoose:
		if next, cmd, ok := m.choose.Pointer(ctx, msg, 2+m.introLines(ctx)); ok {
			m.choose = next
			return m, cmd
		}
		next, cmd := m.choose.Update(msg)
		m.choose = next
		return m, cmd
	case sshMade, sshExisting:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			switch {
			case key.Matches(km, m.keys.Copy) && m.made.PublicKey != "":
				return m, m.sess.opts.Copy(m.made.PublicKey)
			case key.Matches(km, m.keys.Use):
				m.stage = sshPlanning
				return m, m.planCmd(m.path)
			}
		}
	case sshTyping:
		if km, ok := msg.(tea.KeyPressMsg); ok {
			if km.String() == "enter" {
				m.path = strings.TrimSpace(m.input.Value())
				m.stage = sshPlanning
				return m, m.planCmd(m.path)
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(km)
			return m, cmd
		}
	case sshChange:
		next, cmd, out := m.change.update(msg, ctx, 2)
		m.change = next
		switch out {
		case outcomeDeclined:
			return m, uictx.Pop()
		case outcomeBack:
			return m, uictx.Pop()
		}
		return m, cmd
	case sshFailed:
		if km, ok := msg.(tea.KeyPressMsg); ok && key.Matches(km, m.keys.Back) {
			return m, uictx.Pop()
		}
	}
	return m, nil
}

// makeCmd makes the key through the safe keygen, which never overwrites.
func (m sshScreen) makeCmd() tea.Cmd {
	svc, path := m.sess.svc, m.path
	comment := ""
	if ts, ok := m.sess.status(accounts.ToolGit); ok {
		comment = ts.Identity.Email()
	}
	return func() tea.Msg {
		ctx, cancel := engineCtx()
		defer cancel()
		res, err := svc.GenerateSSHKey(ctx, path, comment)
		return keyMadeMsg{res: res, err: err}
	}
}

// planCmd previews the folder's key ("" takes it away).
func (m sshScreen) planCmd(path string) tea.Cmd {
	svc, folder := m.sess.svc, m.sess.folder
	return func() tea.Msg {
		p, err := svc.PlanSSHKey(folder, path)
		return keyPlanMsg{p: p, err: err}
	}
}

// introLines is how tall the explanation above the choices is.
func (m sshScreen) introLines(ctx uictx.Context) int { return len(m.intro(ctx)) + 1 }

func (m sshScreen) intro(ctx uictx.Context) []string {
	return wrap(ctx, ctx.Theme.Base, "A repo that pushes over SSH is pushed as the GitHub account its key belongs to. A key of its own for this folder keeps work and personal pushes apart.", 1, 0)
}

func (m sshScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, "Push with an SSH key", "in "+m.sess.folder), ""}
	switch m.stage {
	case sshChoose:
		out = append(out, m.intro(ctx)...)
		out = append(out, "", m.choose.View(ctx))
	case sshMaking, sshPlanning:
		out = append(out, " "+th.Muted.Render("Working"+ellipsis(ctx)))
	case sshMade:
		out = append(out, " "+th.Success.Render(ctx.Icons.Tick)+" "+th.Base.Bold(true).Render("Made a new key"), "   "+th.Muted.Render(fit(ctx, m.made.Path, ctx.Width-4)), "")
		out = append(out, wrap(ctx, th.Base, "Add the public key to the GitHub account it is for: github.com › Settings › SSH and GPG keys › New SSH key.", 1, 0)...)
		out = append(out, strings.Split(card(ctx, th.Info.Render(strings.TrimSpace(m.made.PublicKey))), "\n")...)
		out = append(out, keyHints(ctx, "c", "copy the public key", "enter", "use this key for pushes from this folder"))
	case sshExisting:
		out = append(out, " "+th.Warning.Render(ctx.Icons.Warn)+" "+th.Base.Render("A key is already at "+fit(ctx, m.path, ctx.Width-24)+"."), "")
		out = append(out, wrap(ctx, th.Muted, "Devpit never replaces a key: one that is in use somewhere would stop working. Use this one, or go back and pick another path.", 1, 0)...)
		out = append(out, "", keyHints(ctx, "enter", "use this key for this folder", "esc", "back"))
	case sshTyping:
		out = append(out, " "+th.Base.Render("The path of the private key (not the .pub file):"))
		out = append(out, " "+styleInput(m.input, ctx).View())
	case sshChange:
		out = append(out, m.change.view(ctx, ctx.BodyHeight-2)...)
	case sshFailed:
		out = append(out, errorView(ctx, m.err), "", keyHints(ctx, "enter", "back"))
	}
	return strings.Join(out, "\n")
}
