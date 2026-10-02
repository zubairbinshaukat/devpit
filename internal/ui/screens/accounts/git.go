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
	"github.com/zubairbinshaukat/devpit/internal/ui/components/choices"
	"github.com/zubairbinshaukat/devpit/internal/ui/components/menu"
	"github.com/zubairbinshaukat/devpit/internal/ui/uictx"
)

// The Git page answers the two questions people mix up: who a commit is made
// as here (name and email, and which setting decided it), and which GitHub
// account a push uses (or that the SSH key decides, for an SSH remote). The
// two answers sit at the top as plain facts; the choices are below them in
// three groups: commits, pushes, and tidying up. Everything the old Git &
// SSH screen did is here: see the name and email, set another, make an SSH
// key and copy its public half.

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
	list   choices.Model
	check  key.Binding
}

func newGitScreen(s *session, depth int) gitScreen {
	return gitScreen{sess: s, depth: depth, list: choices.New(), check: bind("check who is signed in", "v")}
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
	return []key.Binding{m.list.Keys.Up, m.list.Keys.Change, m.check}
}

func (m gitScreen) FullHelp() [][]key.Binding {
	return append(m.list.Keys.FullHelp(), []key.Binding{m.check})
}

// nextPreview and nextPick are what follows a change on these pages.
const (
	nextPreview = "You see a preview first. Nothing changes until you say yes."
	nextPick    = "You pick one, then see a preview. Nothing changes until you say yes."
)

// groups are the choices: commits, pushes, and tidying up.
func (m gitScreen) groups() []choices.Group {
	ts, _ := m.sess.status(accounts.ToolGit)
	commits := []choices.Item{
		{
			ID: gitHere, Label: "Commit as someone else here",
			Desc: "Use a different name and email for commits in this folder and every folder inside it.",
			Next: nextPick,
		},
		{
			ID: gitEverywhere, Label: "Commit as someone else everywhere",
			Desc: "Use a different name and email for commits in every folder that has no choice of its own.",
			Next: nextPick,
		},
		{
			ID: gitAdd, Label: "Add a name and email",
			Desc: "Save another name and email to commit with, such as a work address. Your GitHub accounts can suggest their private no-reply address.",
			Next: "A short form opens. Saving it changes nothing else.",
		},
	}
	if ts.Resolution.Reason == accounts.ReasonFolderRule {
		commits = append(commits, choices.Item{
			ID: gitRemoveRule, Label: "Forget this folder's choice",
			Desc: "Commits here use " + ts.Display + " because you chose it for " + ts.Resolution.RuleFolder +
				" and the folders inside it. Forget that choice and this folder uses the one from the folder around it, or your usual one.",
			Next: nextPreview,
		})
	}
	pushes := []choices.Item{
		{
			ID: gitHubHere, Label: "Push as another GitHub account here",
			Desc: "Pick which GitHub account your pushes (and the gh tool) use in this folder and the folders inside it.",
			Next: nextPick,
		},
		{
			ID: gitSSH, Label: "Push with an SSH key",
			Desc: "An SSH key is a file that proves who you are to GitHub. A key just for this folder keeps work and personal pushes apart.",
			Next: "You make a key or pick one you have, add it to GitHub, then see a preview before it is used.",
		},
	}
	if !ts.Installed {
		for _, l := range [][]choices.Item{commits, pushes} {
			for i := range l {
				l[i].Disabled = "Git is not installed on this PC. Install & Update can install it."
			}
		}
	}
	manage := []choices.Item{{
		ID: gitManage, Label: "Rename or remove a name and email",
		Desc: "Rename or remove a name and email Devpit saved. Your usual one, in Git's own settings, is not listed and is never changed here.",
		Next: "You pick one, then see what changes before anything happens.",
	}}
	return []choices.Group{
		{Title: "Commits", Items: commits},
		{Title: "Pushes", Items: pushes},
		{Title: "Manage", Items: manage},
	}
}

func (m gitScreen) spec(ctx uictx.Context) choices.Spec {
	return choices.Spec{
		FactsTitle: "Right now",
		FactsAside: "in " + m.sess.folder,
		Facts:      m.gitFacts(ctx),
		Groups:     m.groups(),
	}
}

func (m gitScreen) Update(msg tea.Msg, ctx uictx.Context) (uictx.Screen, tea.Cmd) {
	if m.sess.absorb(msg) {
		m.list = m.list.Settle(ctx, m.spec(ctx))
		return m, m.factsCmd()
	}
	switch msg := msg.(type) {
	case gitFactsMsg:
		m.facts, m.loaded = msg, true
		m.list = m.list.Settle(ctx, m.spec(ctx))
		return m, nil
	case accountAddedMsg:
		return m, uictx.Push(newFlowAt(m.sess, accounts.ToolGit, msg.acct.Name, accounts.ScopeFolder, m.depth+1))
	case tea.KeyPressMsg:
		if key.Matches(msg, m.check) {
			return m, uictx.Push(newVerifyScreen(m.sess, m.depth+1))
		}
	}
	next, a := m.list.Update(msg, ctx, m.spec(ctx))
	m.list = next
	return m, m.act(a.ID)
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

// gitFacts are the two answers: commits as, pushes as.
func (m gitScreen) gitFacts(ctx uictx.Context) []choices.Fact {
	ts, _ := m.sess.status(accounts.ToolGit)
	if !ts.Installed {
		return []choices.Fact{{
			Label: "Commits as", Value: "nobody yet: Git is not installed",
			Notes: []string{"Install Git (Install & Update can do it), then come back here."},
		}}
	}
	if !m.loaded {
		return []choices.Fact{{Label: "Commits as", Value: ts.Display, Notes: []string{"asking Git" + ellipsis(ctx)}}}
	}
	var out []choices.Fact
	ci := m.facts.commits
	switch {
	case m.facts.commitsErr != nil:
		out = append(out, choices.Fact{
			Label: "Commits as", Value: ts.Display,
			Warnings: []string{accounts.Scrub(m.facts.commitsErr.Error())},
		})
	default:
		who := strings.TrimSpace(ci.Name.Value + " <" + ci.Email.Value + ">")
		if ci.Email.Value == "" {
			who = "no email set"
		}
		f := choices.Fact{Label: "Commits as", Value: who}
		if ci.Mismatch {
			f.Tone = choices.Bad
		}
		from := "from " + fromPlain(ci.Email.From)
		if ci.Expected.Reason == accounts.ReasonFolderRule {
			from += sep(ctx) + "chosen for " + ci.Expected.RuleFolder
		}
		f.Notes = append(f.Notes, from)
		if !ci.Repo.IsRepo && len(ci.Notes) == 0 {
			f.Notes = append(f.Notes, "This folder is not a Git repository yet; this applies once it is.")
		}
		f.Warnings = append(f.Warnings, ci.Notes...)
		out = append(out, f)
	}

	p := m.facts.pushes
	gh, _ := m.sess.status(accounts.ToolGitHub)
	github := "GitHub: " + accountPlain(gh.Display, accounts.ToolGitHub)
	switch {
	case m.facts.pushesErr != nil && !errors.Is(m.facts.pushesErr, accounts.ErrNotFoundTool):
		out = append(out, choices.Fact{
			Label: "Pushes as", Value: github,
			Warnings: []string{accounts.Scrub(m.facts.pushesErr.Error())},
		})
	case p.Via == adapters.PushViaSSHKey:
		k := "your usual SSH keys"
		if p.SSHKey != "" {
			k = p.SSHKey
		}
		out = append(out, choices.Fact{
			Label: "Pushes as", Value: "the GitHub account of the SSH key " + k,
			Notes: []string{"This repository pushes over SSH, so the key decides which GitHub account is used."},
		})
	default:
		f := choices.Fact{Label: "Pushes as", Value: github}
		note := wherePlain(gh.Resolution)
		if p.Via != "" {
			note += sep(ctx) + viaPlain(p.Via)
		}
		f.Notes = append(f.Notes, note)
		if isNotChecked(gh.Display) {
			f.Notes = append(f.Notes, notCheckedNote(accounts.ToolGitHub))
		}
		f.Notes = append(f.Notes, p.Notes...)
		out = append(out, f)
	}
	return out
}

func (m gitScreen) View(ctx uictx.Context) string { return m.list.View(ctx, m.spec(ctx)) }

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

func (m identityScreen) Title() string { return "Git › New name and email" }

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
		m.err = "Give it a short name in Devpit, like work."
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
			return identitySavedMsg{err: errors.New("the name and email were not saved")}
		}
		acct, _, err := svc.SaveAccount(*last.Account, name)
		return identitySavedMsg{acct: acct, err: err}
	}
}

func (m identityScreen) suggTop() int { return 2 + 3*3 + 2 }

func (m identityScreen) View(ctx uictx.Context) string {
	th := ctx.Theme
	out := []string{heading(ctx, "A new name and email to commit with", ""), ""}
	labels := [3]string{"Short name", "Name on commits", "Email on commits"}
	hints := [3]string{"how Devpit lists it, like work", "leave empty to keep the name in your usual Git settings", "the address your commits show"}
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
		{ID: sshHave, Title: "Use a key I already have", Desc: "Type where its private key file is"},
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
