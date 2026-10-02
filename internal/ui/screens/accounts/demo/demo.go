// Package demo is an Accounts engine that lives in memory: the real
// accounts store, resolver and preview sentences, with every tool, sign-in
// and file faked. The Accounts screens' tests, the app's golden frames and
// the documentation screenshots all drive the screens through it, so
// nothing they do can run a tool, open a browser, or touch a real account,
// ~/.claude, ~/.gitconfig or the PATH.
//
// Every name, email and path in it is made up.
package demo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
	"github.com/zubairbinshaukat/devpit/internal/gitssh"
)

// The made-up machine.
const (
	Home     = `C:\Users\you`
	Folder   = `C:\Projects\quiz-slayer`
	Projects = `C:\Projects`
)

// StorePath is how the demo's accounts.toml is shown.
const StorePath = "~/AppData/Roaming/devpit/accounts.toml"

// Service is the in-memory engine. Its exported fields script what the
// tools "say"; the zero values are a quiet, healthy machine. It is safe for
// the screens' commands to call from their own goroutines.
type Service struct {
	mu sync.Mutex

	// Store is the accounts file.
	Store *accounts.Store
	// Installed lists the tools on PATH; nil means every tool.
	Installed map[accounts.Tool]bool
	// Problems are extra problems per tool (a variable in this terminal, a
	// shadowed shim).
	Problems map[accounts.Tool][]service.Problem
	// Identities are who each tool says an account is, by "tool/name", for
	// Verify and sign-in. Missing: the account's recorded email.
	Identities map[string]accounts.Identity
	// Risky lists the tools whose live check can harm an idle account.
	Risky map[accounts.Tool]string
	// Stale lists the rule folders that are gone (folder not found) or on a
	// drive that is not connected (the value says which).
	Stale map[string]accounts.StaleKind
	// Gone lists folders that do not exist.
	Gone map[string]bool
	// Locked makes Hold fail as if another Devpit window held the lock.
	Locked bool
	// Recovered is what crash recovery "did" on open.
	Recovered []accounts.Recovered
	// Warning is the quarantined-file sentence of the next overview.
	Warning string
	// ShimRepairs are the shims "repaired" on open.
	ShimRepairs []accounts.Event
	// FailApplyAt makes the next Apply fail after this many steps (0: no
	// failure), putting everything back.
	FailApplyAt int
	// SignInEmail is who the next sign-in signs in as; "" cancels it.
	SignInEmail string
	// SignInWarnings are extra warning events of the next sign-in.
	SignInWarnings []accounts.Event
	// Found is what the import look "finds".
	Found importer.Found
	// Inventory is the Claude setup item list for any account.
	Inventory *claudeshare.Inventory
	// LinkRefused makes the next Claude apply fail with a *LinkError.
	LinkRefused bool
	// InUse makes the next Claude apply fail with an *InUseError.
	InUse bool
	// UndoByHand makes Undo refuse because a file was changed by hand.
	UndoByHand string
	// Commits and Pushes are the Git page's answers.
	Commits adapters.GitCommitIdentity
	Pushes  adapters.GitHubPush
	// Emails are GitHub's suggested commit addresses.
	Emails []adapters.EmailSuggestion
	// KeyExists makes GenerateSSHKey find a key already there.
	KeyExists bool

	// Calls records what the screens asked, for tests.
	Calls []string

	journal   []entry
	dismissed string
	held      bool
}

// entry is one change, for undo.
type entry struct {
	summary string
	before  *accounts.Store
	group   string
}

// New returns the demo machine: a work Claude Code account used under
// C:\Projects, two Git identities, two GitHub logins, and the other tools
// on their own sign-in.
func New() *Service {
	st := accounts.NewStore()
	add := func(a accounts.Account) {
		if err := st.AddAccount(a); err != nil {
			panic(err)
		}
	}
	add(accounts.Account{Tool: accounts.ToolClaude, Name: "work", Email: "you@work.example", Dir: Home + `\.devpit\accounts\claude\work`})
	add(accounts.Account{Tool: accounts.ToolGit, Name: "work", Label: "You", Email: "you@work.example"})
	add(accounts.Account{Tool: accounts.ToolGitHub, Name: "work", Label: "you-at-work"})
	add(accounts.Account{Tool: accounts.ToolVercel, Name: "client", Email: "you@client.example", Dir: Home + `\.devpit\accounts\vercel\client`})
	must(st.SetRule(Projects, accounts.ToolClaude, "work"))
	must(st.SetRule(Projects, accounts.ToolGitHub, "work"))
	must(st.SetDefaultEmail(accounts.ToolClaude, "you@personal.example"))
	must(st.SetDefaultEmail(accounts.ToolGitHub, "you-personal@users.example"))
	must(st.SetDefaultEmail(accounts.ToolVercel, "you@personal.example"))
	s := &Service{Store: st}
	s.Installed = map[accounts.Tool]bool{
		accounts.ToolClaude: true, accounts.ToolGit: true, accounts.ToolGitHub: true, accounts.ToolVercel: true,
		accounts.ToolCloudflare: true, accounts.ToolConvex: true,
	}
	s.Risky = map[accounts.Tool]string{accounts.ToolClaude: "Checking asks Claude Code itself. On some versions this can sign an idle account out (Claude Code issue #95822), so Devpit only checks when you ask."}
	s.SignInEmail = "you@side.example"
	s.Inventory = SampleInventory("new")
	s.Commits = adapters.GitCommitIdentity{
		Folder: Folder,
		Name:   adapters.GitValue{Value: "You", From: adapters.GitFromGlobal},
		Email:  adapters.GitValue{Value: "you@personal.example", From: adapters.GitFromGlobal},
		Repo:   adapters.GitRepo{IsRepo: true},
	}
	s.Pushes = adapters.GitHubPush{
		Folder: Folder, HasRemote: true, Via: adapters.PushViaDevpit,
		Remote: adapters.GitRemote{Name: "origin", URL: "https://github.com/you/quiz-slayer.git", Protocol: adapters.ProtocolHTTPS, Host: "github.com"},
	}
	s.Emails = []adapters.EmailSuggestion{
		{Email: "1234567+you-at-work@users.noreply.github.com", Why: "GitHub's private address for you-at-work: commits link to the account without showing your email", Verified: true},
		{Email: "you@work.example", Why: "your primary address on GitHub", Primary: true, Verified: true},
	}
	return s
}

// Empty is a machine with nothing set up yet.
func Empty() *Service {
	s := New()
	s.Store = accounts.NewStore()
	return s
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func (s *Service) note(f string, a ...any) {
	s.Calls = append(s.Calls, fmt.Sprintf(f, a...))
}

// Called reports how many calls started with prefix.
func (s *Service) Called(prefix string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, c := range s.Calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// installed reports whether t is on PATH.
func (s *Service) installed(t accounts.Tool) bool {
	return s.Installed == nil || s.Installed[t]
}

// display names an account and who it is, like the real engine.
func (s *Service) display(acct accounts.Account) string {
	if acct.Tool == accounts.ToolGit {
		if acct.IsDefault() {
			return "You <you@personal.example>"
		}
		a, _ := s.Store.Account(acct.Tool, acct.Name)
		return a.Label + " <" + a.Email + ">"
	}
	if acct.IsDefault() {
		if e := s.Store.DefaultEmail[acct.Tool]; e != "" {
			return "default (" + e + ")"
		}
		return "default (not checked yet)"
	}
	a, ok := s.Store.Account(acct.Tool, acct.Name)
	if !ok {
		return acct.Name
	}
	who := a.Email
	if acct.Tool == accounts.ToolGitHub && a.Label != "" {
		who = a.Label
	}
	if who == "" {
		return a.Name + " (no email recorded)"
	}
	return a.Name + " (" + who + ")"
}

func (s *Service) identity(acct accounts.Account) accounts.Identity {
	if id, ok := s.Identities[string(acct.Tool)+"/"+acct.Name]; ok {
		return id
	}
	a, _ := s.Store.Account(acct.Tool, acct.Name)
	email := a.Email
	if acct.IsDefault() {
		email = s.Store.DefaultEmail[acct.Tool]
	}
	return accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateRecorded, Email: email, Login: a.Label})
}

func (s *Service) status(t accounts.Tool, folder string) (service.ToolStatus, error) {
	res, err := accounts.Resolve(s.Store, t, folder, accounts.ResolveOptions{})
	if err != nil {
		return service.ToolStatus{}, err
	}
	ts := service.ToolStatus{
		Tool: t, Folder: res.Folder, Resolution: res, Account: res.Account, Installed: s.installed(t),
		Managed: s.Store.Manages(t), Supports: adapters.Supports(t), Why: res.Why(),
	}
	ts.Identity = s.identity(res.Account)
	ts.Display = s.display(res.Account)
	every := accounts.DefaultName
	if v, ok := s.Store.Everywhere[t]; ok {
		every = v
	}
	ts.EverywhereDisplay = s.display(accounts.Account{Tool: t, Name: every})
	if t == accounts.ToolConvex {
		ts.Display, ts.Why, ts.EverywhereDisplay = "project: quiz-slayer", "set by this project's .env.local", "each project picks its own"
	}
	if t == accounts.ToolCloudflare && res.Reason == accounts.ReasonEverywhere {
		ts.Why += " · beta"
	}
	for _, p := range res.Problems {
		ts.Problems = append(ts.Problems, service.Problem{Kind: string(p.Kind), Tool: string(t), Message: p.Message, Fix: p.Fix})
	}
	for _, sr := range s.staleRules() {
		if _, ok := sr.Rule.Accounts[t]; ok {
			ts.Problems = append(ts.Problems, service.Problem{Kind: string(sr.Kind), Tool: string(t), Message: sr.Message, Fix: accounts.StaleFix})
		}
	}
	ts.Problems = append(ts.Problems, s.Problems[t]...)
	return ts, nil
}

// --- the page ---

// Overview implements the screens' Service.
func (s *Service) Overview(_ context.Context, folder string) (service.Overview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("overview %s", folder)
	out := service.Overview{Folder: folder, Warning: s.Warning}
	s.Warning = ""
	for _, t := range accounts.Tools() {
		ts, err := s.status(t, folder)
		if err != nil {
			return service.Overview{}, err
		}
		out.Folder = ts.Folder
		out.Tools = append(out.Tools, ts)
	}
	return out, nil
}

// Status implements the screens' Service.
func (s *Service) Status(_ context.Context, t accounts.Tool, folder string) (service.ToolStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status(t, folder)
}

// List implements the screens' Service.
func (s *Service) List(_ context.Context, t accounts.Tool, folder string) (service.ListJSON, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := accounts.Resolve(s.Store, t, folder, accounts.ResolveOptions{})
	if err != nil {
		return service.ListJSON{}, err
	}
	every := accounts.DefaultName
	if v, ok := s.Store.Everywhere[t]; ok {
		every = v
	}
	out := service.ListJSON{Tool: string(t), Folder: res.Folder}
	for _, a := range adapters.DefaultAndStore(t, s.Store) {
		e := service.ListEntryJSON{
			Name: a.Name, Email: a.Email, Login: a.Label, Display: s.display(a),
			HereNow: strings.EqualFold(a.Name, res.Account.Name), Everywhere: strings.EqualFold(a.Name, every), Rules: []string{},
		}
		for _, r := range s.Store.RulesUsing(t, a.Name) {
			e.Rules = append(e.Rules, r.Folder)
		}
		out.Accounts = append(out.Accounts, e)
	}
	return out, nil
}

// Caps implements the screens' Service: the static table, or nothing for a
// tool that is not installed.
func (s *Service) Caps(_ context.Context, t accounts.Tool) adapters.Caps {
	if !s.installed(t) {
		return adapters.Caps{Why: t.DisplayName() + " is not installed."}
	}
	return adapters.Supports(t)
}

// LiveCheckRisk implements the screens' Service.
func (s *Service) LiveCheckRisk(t accounts.Tool) (bool, string) {
	why, ok := s.Risky[t]
	return ok, why
}

// Load implements the screens' Service.
func (s *Service) Load() (*accounts.Store, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Store.Clone(), "", nil
}

// FolderCheck implements the screens' Service.
func (s *Service) FolderCheck(folder string) string {
	if accounts.SameFolder(folder, Home) {
		return "You are in your home folder, so \"this folder\" would cover every project inside it."
	}
	if accounts.IsDriveRoot(folder) {
		return "You are at the root of " + folder + ", so \"this folder\" would cover the whole drive."
	}
	return ""
}

// SyncShims implements the screens' Service.
func (s *Service) SyncShims(emit func(accounts.Event)) {
	s.mu.Lock()
	evs := s.ShimRepairs
	s.ShimRepairs = nil
	s.note("sync shims")
	s.mu.Unlock()
	for _, ev := range evs {
		emit(ev)
	}
}

// RecoveryReport implements the screens' Service.
func (s *Service) RecoveryReport() ([]accounts.Recovered, error) { return s.Recovered, nil }

// Hold implements the screens' Service.
func (s *Service) Hold() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Locked {
		return accounts.ErrLocked
	}
	s.held = true
	return nil
}

// Release implements the screens' Service.
func (s *Service) Release() {
	s.mu.Lock()
	s.held = false
	s.mu.Unlock()
}

// Held reports whether the page holds the lock.
func (s *Service) Held() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.held
}

func (s *Service) staleRules() []accounts.StaleRule {
	var out []accounts.StaleRule
	for _, r := range s.Store.Rules {
		k, ok := s.Stale[strings.ToLower(r.Folder)]
		if !ok {
			continue
		}
		msg := r.Folder + ": folder not found. It may have been moved, renamed or deleted."
		if k == accounts.StaleDriveNotConnected {
			msg = r.Folder + ": drive " + r.Folder[:2] + " is not connected. The rule stays until you remove it, and works again when the drive is back."
		}
		out = append(out, accounts.StaleRule{Rule: r, Kind: k, Message: msg})
	}
	return out
}

// StaleRules implements the screens' Service.
func (s *Service) StaleRules() ([]accounts.StaleRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.staleRules(), nil
}

// DisplayPath implements the screens' Service.
func (s *Service) DisplayPath(p string) string {
	if strings.HasPrefix(strings.ToLower(p), strings.ToLower(Home)) {
		return "~" + strings.ReplaceAll(p[len(Home):], `\`, "/")
	}
	return p
}

// --- changes ---

// Plan implements the screens' Service with the real preview sentences.
func (s *Service) Plan(_ context.Context, c accounts.Change) (accounts.Preview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("plan %s %s %s %s", c.Tool, c.Account, c.Scope.Kind, c.Scope.Folder)
	caps := adapters.Supports(c.Tool)
	if !caps.Supports(c.Scope.Kind) && !c.Remove {
		return accounts.Preview{}, fmt.Errorf("%s: %w", strings.TrimRight(caps.Why, "."), accounts.ErrNotSupported)
	}
	in := accounts.PreviewInput{Store: s.Store, Change: c, StorePath: StorePath, Home: Home}
	if c.Tool == accounts.ToolGit {
		in.Display = func(a accounts.Account) string { return s.display(a) }
		in.Uses = func(_, account string) string { return "Git will commit as " + account }
	}
	return accounts.BuildPreview(in)
}

// Apply implements the screens' Service: the store changes for real (in
// memory), step by step.
func (s *Service) Apply(_ context.Context, p accounts.Preview) <-chan accounts.Event {
	s.mu.Lock()
	fail := s.FailApplyAt
	s.FailApplyAt = 0
	s.note("apply %s", p.Summary)
	s.mu.Unlock()
	ch := make(chan accounts.Event, 16)
	go func() {
		defer close(ch)
		steps := []string{"Recording the change so it can be undone", "Nothing to change in " + p.Change.Tool.DisplayName() + " itself"}
		for i, st := range steps {
			ch <- accounts.NewEvent(st, accounts.StepRunning, "")
			if fail == i+1 {
				ch <- accounts.Failed(st, errors.New("writing "+StorePath+": the file is in use. Everything done so far was put back"))
				return
			}
			ch <- accounts.NewEvent(st, accounts.StepDone, "")
		}
		s.mu.Lock()
		if accounts.StoreHash(s.Store) != p.BeforeHash {
			s.mu.Unlock()
			ch <- accounts.Failed("Saving the rule", accounts.ErrStalePreview)
			return
		}
		after, err := p.Change.ApplyTo(s.Store)
		if err == nil {
			s.journal = append(s.journal, entry{summary: p.Summary, before: s.Store, group: p.Group})
			s.Store = after
		}
		s.mu.Unlock()
		if err != nil {
			ch <- accounts.Failed("Saving the rule", err)
			return
		}
		ev := accounts.NewEvent("Saving the rule", accounts.StepDone, p.Summary)
		ev.Final, ev.EntryID = true, "demo"
		ch <- ev
	}()
	return ch
}

// ApplyGroup implements the screens' Service.
func (s *Service) ApplyGroup(ctx context.Context, ps []accounts.Preview) <-chan accounts.Event {
	out := make(chan accounts.Event, 16)
	go func() {
		defer close(out)
		g := accounts.NewGroup()
		for _, p := range ps {
			if p.NoChange {
				continue
			}
			fresh, err := s.Plan(ctx, p.Change)
			if err != nil {
				out <- accounts.Failed("Saving the change", err)
				return
			}
			fresh.Group = g
			for ev := range s.Apply(ctx, fresh) {
				if ev.Final && ev.State != accounts.StepDone {
					out <- ev
					return
				}
				ev.Final = false
				out <- ev
			}
		}
		ev := accounts.NewEvent("Saved", accounts.StepDone, "")
		ev.Final = true
		out <- ev
	}()
	return out
}

// Verify implements the screens' Service: the active account of every
// installed tool, and the idle ones with All (the risky ones only with
// Risky).
func (s *Service) Verify(_ context.Context, opt service.VerifyOptions) (service.VerifyReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("verify all=%v risky=%v", opt.All, opt.Risky)
	rep := service.VerifyReport{Folder: opt.Folder}
	for _, t := range accounts.Tools() {
		ts, err := s.status(t, opt.Folder)
		if err != nil {
			return rep, err
		}
		rep.Problems = append(rep.Problems, ts.Problems...)
		if !ts.Installed {
			rep.Checks = append(rep.Checks, service.VerifyCheck{Tool: t, Account: ts.Account.Name, Here: true, Expected: ts.Display, How: "not run", Status: service.VerifyNotInstalled})
			continue
		}
		rep.Checks = append(rep.Checks, s.check(t, ts.Account, true))
		if opt.All && t != accounts.ToolGit && t != accounts.ToolConvex {
			if why, risky := s.Risky[t]; risky && !opt.Risky {
				var names []string
				for _, a := range adapters.DefaultAndStore(t, s.Store) {
					if !strings.EqualFold(a.Name, ts.Account.Name) {
						names = append(names, a.Name)
					}
				}
				if len(names) > 0 {
					rep.Skipped = append(rep.Skipped, t.DisplayName()+" accounts not checked: "+strings.Join(names, ", ")+". "+why)
				}
				continue
			}
			for _, a := range adapters.DefaultAndStore(t, s.Store) {
				if !strings.EqualFold(a.Name, ts.Account.Name) {
					rep.Checks = append(rep.Checks, s.check(t, a, false))
				}
			}
		}
	}
	for _, c := range rep.Checks {
		if c.Status == service.VerifyMismatch {
			rep.Mismatch = true
		}
	}
	return rep, nil
}

func (s *Service) check(t accounts.Tool, a accounts.Account, here bool) service.VerifyCheck {
	c := service.VerifyCheck{Tool: t, Account: a.Name, Here: here, Expected: s.display(a), How: "live", Status: service.VerifyOK}
	id := s.identity(a)
	if fid, ok := s.Identities[string(t)+"/"+a.Name]; ok {
		id = fid
	}
	c.Actual = &id
	switch id.State() {
	case accounts.StateExpired:
		c.Status, c.ActualDisplay = service.VerifyMismatch, "expired"
		c.Notes = []string{"The sign-in has expired. Sign in again."}
	case accounts.StateNotSignedIn:
		c.Status, c.ActualDisplay = service.VerifyMismatch, "not signed in"
	default:
		c.ActualDisplay = "signed in as " + id.Who()
		if id.Who() == "" {
			c.ActualDisplay = "signed in"
		}
		if t == accounts.ToolGit {
			c.How, c.ActualDisplay = "offline", "commits as "+s.display(a)
		}
		want := a.Email
		if a.IsDefault() {
			want = s.Store.DefaultEmail[t]
		} else if st, ok := s.Store.Account(t, a.Name); ok {
			want = st.Email
		}
		if want != "" && id.Email() != "" && !strings.EqualFold(want, id.Email()) {
			c.Status = service.VerifyMismatch
			c.Notes = []string{fmt.Sprintf("Devpit expects %s to be %s, but %s says it is %s. Sign in again with: devpit %s add", a.Name, want, t.DisplayName(), id.Email(), t)}
		}
	}
	if t == accounts.ToolConvex {
		c.Status, c.How = service.VerifyInfo, "offline"
		c.ActualDisplay = "project: quiz-slayer"
	}
	return c
}

// UndoPreview implements the screens' Service.
func (s *Service) UndoPreview() (service.UndoInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.journal) == 0 {
		return service.UndoInfo{}, accounts.ErrNothingToUndo
	}
	if s.UndoByHand != "" {
		return service.UndoInfo{}, fmt.Errorf("%s was changed by hand since Devpit wrote it: %w", s.UndoByHand, accounts.ErrChangedByHand)
	}
	last := s.journal[len(s.journal)-1]
	at := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	info := service.UndoInfo{Entries: []accounts.Entry{{ID: "demo", Summary: last.summary, Time: at}}}
	info.Lines = []string{"Undo this change:", "  " + last.summary + " (2026-10-02 10:00)", "    puts back: " + StorePath, "accounts.toml goes back to how it was before."}
	return info, nil
}

// Undo implements the screens' Service.
func (s *Service) Undo(_ context.Context, emit func(accounts.Event)) (accounts.UndoResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("undo")
	if len(s.journal) == 0 {
		return accounts.UndoResult{}, accounts.ErrNothingToUndo
	}
	last := s.journal[len(s.journal)-1]
	n := 1
	for i := len(s.journal) - 2; i >= 0 && last.group != "" && s.journal[i].group == last.group; i-- {
		last = s.journal[i]
		n++
	}
	s.Store = last.before
	s.journal = s.journal[:len(s.journal)-n]
	return accounts.UndoResult{Entry: accounts.Entry{Summary: last.summary}}, nil
}

// --- accounts ---

// SignIn implements the screens' Service: the steps a real sign-in
// reports, then the account to save, or a cancelled sign-in.
func (s *Service) SignIn(_ context.Context, t accounts.Tool, req adapters.LoginRequest) (<-chan accounts.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("signin %s %s", t, req.Name)
	if err := s.Store.CheckNewName(t, req.Name); err != nil {
		return nil, err
	}
	email := s.SignInEmail
	if t == accounts.ToolGit {
		email = req.Email
	}
	warns := s.SignInWarnings
	ch := make(chan accounts.Event, 16)
	go func() {
		defer close(ch)
		if t == accounts.ToolGit {
			acct := accounts.Account{Tool: t, Name: req.Name, Email: email, Label: firstOf(req.DisplayName, "You")}
			ev := accounts.NewEvent("Adding the Git identity", accounts.StepDone, acct.Label+" <"+email+">")
			ev.Final, ev.Account = true, &acct
			ch <- ev
			return
		}
		ch <- accounts.NewEvent("Checking "+t.DisplayName(), accounts.StepDone, "version 1.0.0")
		ch <- accounts.NewEvent("Opening "+t.DisplayName()+"'s own sign-in", accounts.StepRunning, "")
		ch <- accounts.NewEvent("Waiting for you to finish signing in in the browser", accounts.StepWaiting, "")
		if email == "" {
			ch <- accounts.Failed("Signing in", fmt.Errorf("%w: %s says this folder is not signed in", accounts.ErrSignInCancelled, t.DisplayName()))
			return
		}
		id := accounts.NewIdentity(accounts.IdentityFields{State: accounts.StateSignedIn, Email: email})
		ev := accounts.NewEvent("Checking who is signed in", accounts.StepDone, "Signed in as "+email)
		ev.Identity = &id
		ch <- ev
		for _, w := range warns {
			ch <- w
		}
		acct := accounts.Account{Tool: t, Name: req.Name, Email: email, Dir: Home + `\.devpit\accounts\` + string(t) + `\` + req.Name}
		if t == accounts.ToolGitHub {
			acct.Dir, acct.Email, acct.Label = "", "", strings.SplitN(email, "@", 2)[0]
		}
		final := accounts.NewEvent("Signed in", accounts.StepDone, "Signed in as "+email)
		final.Final, final.Account, final.Identity = true, &acct, &id
		ch <- final
	}()
	return ch, nil
}

func firstOf(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// PrepareSignInAgain implements the screens' Service.
func (s *Service) PrepareSignInAgain(_ context.Context, t accounts.Tool, name string) (service.OnceCommand, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("sign in again %s %s", t, name)
	args := service.SignInAgainArgs(t)
	if args == nil {
		return service.OnceCommand{}, accounts.ErrNotSupported
	}
	return service.OnceCommand{Path: `C:\fake\` + args[0] + ".exe", Args: args[1:]}, nil
}

// DiscardSignIn implements the screens' Service.
func (s *Service) DiscardSignIn(acct accounts.Account) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("discard %s %s", acct.Tool, acct.Name)
	return "", nil
}

// SuggestName implements the screens' Service.
func (s *Service) SuggestName(t accounts.Tool, email string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return accounts.SuggestName(email, s.Store.Names(t))
}

// CheckNewName implements the screens' Service.
func (s *Service) CheckNewName(t accounts.Tool, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Store.CheckNewName(t, name)
}

// SaveAccount implements the screens' Service.
func (s *Service) SaveAccount(acct accounts.Account, name string) (accounts.Account, accounts.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("save %s %s", acct.Tool, name)
	if acct.Dir != "" {
		acct.Dir = Home + `\.devpit\accounts\` + string(acct.Tool) + `\` + name
	}
	acct.Name = name
	before := s.Store.Clone()
	if err := s.Store.AddAccount(acct); err != nil {
		return acct, accounts.Entry{}, err
	}
	sum := "Added " + acct.Tool.DisplayName() + " account " + name
	s.journal = append(s.journal, entry{summary: sum, before: before})
	return acct, accounts.Entry{ID: "demo", Summary: sum}, nil
}

// PlanAccountEdit implements the screens' Service.
func (s *Service) PlanAccountEdit(_ context.Context, e service.AccountEdit) (service.AccountEditPreview, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, err := s.Store.FindAccount(e.Tool, e.Name)
	if err != nil {
		return service.AccountEditPreview{}, err
	}
	p := service.AccountEditPreview{Edit: e, BeforeHash: accounts.StoreHash(s.Store)}
	tool := e.Tool.DisplayName()
	if e.Remove {
		p.TypedWord = service.RemoveWord
		p.Summary = "Removed " + tool + " account " + a.Name
		p.Sentences = []string{"Devpit will forget the " + tool + " account " + a.Name + "."}
		for _, r := range s.Store.RulesUsing(e.Tool, a.Name) {
			p.FallsBack = append(p.FallsBack, accounts.KeptRule{Folder: r.Folder, Account: "default", Display: s.display(accounts.Account{Tool: e.Tool, Name: "default"}) + ", like everywhere else"})
		}
		if len(p.FallsBack) > 0 {
			p.FallsBackIntro = "These folders used it and will use instead:"
		}
		if a.Dir != "" {
			p.Warnings = []string{"Its folder stays where it is, with its sign-in: " + a.Dir + ". Delete it yourself once you are sure you no longer need it."}
		}
	} else {
		if err := s.Store.CheckNewName(e.Tool, e.NewName); err != nil {
			return service.AccountEditPreview{}, err
		}
		p.Summary = "Renamed " + tool + " account " + a.Name + " to " + e.NewName
		p.Sentences = []string{"The " + tool + " account " + a.Name + " will be called " + e.NewName + "."}
	}
	p.Edits = []accounts.FileEdit{{Path: StorePath, Action: "changes", Lines: []string{"[[account]] " + string(e.Tool) + " " + a.Name}}}
	return p, nil
}

// ApplyAccountEdit implements the screens' Service.
func (s *Service) ApplyAccountEdit(_ context.Context, p service.AccountEditPreview) <-chan accounts.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("edit %s", p.Summary)
	before := s.Store.Clone()
	var err error
	if p.Edit.Remove {
		_, err = s.Store.RemoveAccount(p.Edit.Tool, p.Edit.Name)
	} else {
		err = s.Store.RenameAccount(p.Edit.Tool, p.Edit.Name, p.Edit.NewName)
	}
	if err == nil {
		s.journal = append(s.journal, entry{summary: p.Summary, before: before})
	}
	return events(err, "Saving accounts.toml", p.Summary)
}

// events is a short run: one step, then done or failed.
func events(err error, step, summary string) <-chan accounts.Event {
	ch := make(chan accounts.Event, 4)
	if err != nil {
		ch <- accounts.Failed(step, err)
		close(ch)
		return ch
	}
	ch <- accounts.NewEvent(step, accounts.StepDone, "")
	ev := accounts.NewEvent("Saved", accounts.StepDone, summary)
	ev.Final = true
	ch <- ev
	close(ch)
	return ch
}

// --- the Git page ---

// CommitsAs implements the screens' Service.
func (s *Service) CommitsAs(_ context.Context, folder string) (adapters.GitCommitIdentity, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Gone[strings.ToLower(folder)] {
		return adapters.GitCommitIdentity{}, fmt.Errorf("the folder %s was not found", folder)
	}
	c := s.Commits
	res, _ := accounts.Resolve(s.Store, accounts.ToolGit, folder, accounts.ResolveOptions{})
	c.Expected = res
	if !res.Account.IsDefault() {
		a, _ := s.Store.Account(accounts.ToolGit, res.Account.Name)
		c.Name = adapters.GitValue{Value: a.Label, From: adapters.GitFromRule}
		c.Email = adapters.GitValue{Value: a.Email, From: adapters.GitFromRule}
	}
	return c, nil
}

// PushesAs implements the screens' Service.
func (s *Service) PushesAs(_ context.Context, folder string) (adapters.GitHubPush, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Pushes
	p.Resolution, _ = accounts.Resolve(s.Store, accounts.ToolGitHub, folder, accounts.ResolveOptions{})
	return p, nil
}

// SuggestEmails implements the screens' Service.
func (s *Service) SuggestEmails(context.Context) ([]adapters.EmailSuggestion, error) {
	return s.Emails, nil
}

// SuggestedSSHKeyPath implements the screens' Service.
func (s *Service) SuggestedSSHKeyPath(name string) string {
	return Home + `\.ssh\id_ed25519_devpit_` + name
}

// GenerateSSHKey implements the screens' Service; it never overwrites.
func (s *Service) GenerateSSHKey(_ context.Context, path, comment string) (gitssh.KeygenResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("keygen %s", path)
	if s.KeyExists {
		return gitssh.KeygenResult{}, &gitssh.ExistsError{Path: path}
	}
	return gitssh.KeygenResult{Path: path, PublicKey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIDemoKeyForScreenshotsOnly0000000000000 " + comment}, nil
}

// PlanSSHKey implements the screens' Service.
func (s *Service) PlanSSHKey(folder, keyPath string) (adapters.GitSettingsPreview, error) {
	p := adapters.GitSettingsPreview{Summary: "SSH key for " + folder, BeforeHash: accounts.StoreHash(s.Store)}
	if keyPath == "" {
		p.Sentences = []string{"Git will push over SSH from " + folder + " with your usual SSH keys again."}
		p.Summary = "Removed the SSH key for " + folder
	} else {
		p.Sentences = []string{
			"In " + folder + " and every folder inside it, Git will push over SSH with the key " + keyPath + ", and only that key.",
			"GitHub then sees the account that key belongs to. Pushes over HTTPS are not affected.",
		}
	}
	p.Edits = []accounts.FileEdit{{Path: "~/.devpit/git/rules.gitconfig", Action: "changes", Lines: []string{`[includeIf "gitdir/i:C:/Projects/quiz-slayer/"]`, `path = "ssh-1a2b3c4d.gitconfig"`}}}
	return p, nil
}

// ApplyGitSettings implements the screens' Service.
func (s *Service) ApplyGitSettings(_ context.Context, p adapters.GitSettingsPreview) <-chan accounts.Event {
	s.mu.Lock()
	s.note("git settings %s", p.Summary)
	s.journal = append(s.journal, entry{summary: p.Summary, before: s.Store.Clone()})
	s.mu.Unlock()
	return events(nil, "Writing Devpit's Git files", p.Summary)
}

// --- Claude Code setup ---

// SampleInventory is the item list of the plan's section 6, for an account
// called target.
func SampleInventory(target string) *claudeshare.Inventory {
	share := []claudeshare.Mode{claudeshare.ModeShare, claudeshare.ModeCopy, claudeshare.ModeSkip}
	cp := []claudeshare.Mode{claudeshare.ModeCopy, claudeshare.ModeSkip}
	item := func(k claudeshare.Kind, l claudeshare.Label, def claudeshare.Mode, modes []claudeshare.Mode, n int, size int64) claudeshare.Item {
		return claudeshare.Item{
			Kind: k, Title: k.Title(), Label: l, Default: def, Modes: modes, LinkOK: true,
			Source: claudeshare.Side{Exists: true, Count: n, Size: size},
		}
	}
	inv := &claudeshare.Inventory{Roots: claudeshare.Roots{DefaultHome: Home + `\.claude`, Target: Home + `\.devpit\accounts\claude\` + target, TargetName: target}}
	skills := item(claudeshare.KindSkills, claudeshare.LabelSafe, claudeshare.ModeShare, share, 12, 340<<10)
	inv.Items = []claudeshare.Item{
		skills,
		item(claudeshare.KindAgents, claudeshare.LabelSafe, claudeshare.ModeShare, share, 5, 48<<10),
		item(claudeshare.KindCommands, claudeshare.LabelSafe, claudeshare.ModeShare, share, 9, 22<<10),
		item(claudeshare.KindClaudeMD, claudeshare.LabelSafe, claudeshare.ModeShare, share, 1, 4200),
		item(claudeshare.KindPlugins, claudeshare.LabelSafe, claudeshare.ModeSameList, []claudeshare.Mode{claudeshare.ModeSameList, claudeshare.ModeSkip}, 4, 0),
		item(claudeshare.KindSettings, claudeshare.LabelReview, claudeshare.ModeCopy, cp, 1, 2400),
		item(claudeshare.KindHooks, claudeshare.LabelReview, claudeshare.ModeCopy, cp, 3, 900),
		func() claudeshare.Item {
			it := item(claudeshare.KindMCP, claudeshare.LabelCareful, claudeshare.ModeCopy, cp, 3, 1200)
			it.Asks, it.Secrets = 2, []string{"github", "linear"}
			it.Note = "May hold API keys. Turning it on asks twice."
			return it
		}(),
		item(claudeshare.KindHistory, claudeshare.LabelReview, claudeshare.ModeSkip, cp, 0, 1288490188),
		{Kind: claudeshare.KindLogin, Title: claudeshare.KindLogin.Title(), Label: claudeshare.LabelLocked, Locked: true, Default: claudeshare.ModeLocked},
		{Kind: claudeshare.KindAccountInfo, Title: claudeshare.KindAccountInfo.Title(), Label: claudeshare.LabelLocked, Locked: true, Default: claudeshare.ModeLocked},
	}
	return inv
}

// ClaudeInventory implements the screens' Service.
func (s *Service) ClaudeInventory(name string) (*claudeshare.Inventory, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("inventory %s", name)
	inv := *s.Inventory
	inv.Roots.TargetName = name
	return &inv, nil
}

// PlanClaudeSetup implements the screens' Service: one step per item that
// is not skipped, in the engine's words.
func (s *Service) PlanClaudeSetup(inv *claudeshare.Inventory, sel claudeshare.Selection) (claudeshare.Plan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("plan claude secrets=%v copy=%v", sel.AllowSecrets, sel.CopyInsteadOfLinks)
	p := claudeshare.Plan{Roots: inv.Roots, Summary: "Brought the Claude Code setup to " + inv.Roots.TargetName}
	for _, it := range inv.Items {
		c, ok := sel.Choices[it.Kind]
		if !ok || c.Mode == claudeshare.ModeSkip || c.Mode == "" {
			continue
		}
		md := c.Mode
		if md == claudeshare.ModeShare && sel.CopyInsteadOfLinks {
			md = claudeshare.ModeCopy
		}
		var text string
		switch md {
		case claudeshare.ModeShare:
			text = "Share " + strings.ToLower(it.Title) + " with the default account (a link in " + inv.Roots.TargetName + ")"
		case claudeshare.ModeSameList:
			text = "Copy the list of enabled plugins and marketplaces; " + inv.Roots.TargetName + " installs its own copy"
		default:
			text = "Copy " + strings.ToLower(it.Title) + " into " + inv.Roots.TargetName
		}
		p.Steps = append(p.Steps, claudeshare.Step{Op: claudeshare.OpCopy, Kind: it.Kind, Text: text})
	}
	return p, nil
}

// PlanClaudeChange implements the screens' Service.
func (s *Service) PlanClaudeChange(name string, c service.ClaudeChange) (claudeshare.Plan, error) {
	text := map[service.ClaudeOp]string{
		service.ClaudeStopSharing:  "Stop sharing " + strings.Join(c.Names, ", ") + ": " + name + " gets its own copy",
		service.ClaudeShareInstead: "Share " + strings.Join(c.Names, ", ") + " with the default account",
		service.ClaudeRepair:       "Repair the link for " + strings.Join(c.Names, ", "),
	}[c.Op]
	return claudeshare.Plan{Summary: text, Steps: []claudeshare.Step{{Op: claudeshare.OpLink, Kind: c.Kind, Text: text}}}, nil
}

// ApplyClaudePlan implements the screens' Service.
func (s *Service) ApplyClaudePlan(_ context.Context, p claudeshare.Plan) <-chan accounts.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("apply claude %d", len(p.Steps))
	ch := make(chan accounts.Event, 32)
	defer close(ch)
	switch {
	case s.LinkRefused:
		s.LinkRefused = false
		ch <- accounts.Failed("Checking that links can be made here", &claudeshare.LinkError{Reason: "a policy on this PC does not allow junctions"})
		return ch
	case s.InUse:
		s.InUse = false
		ch <- accounts.NewEvent(p.Steps[0].Text, accounts.StepRunning, "")
		ch <- accounts.Failed(p.Steps[0].Text, &claudeshare.InUseError{Path: Home + `\.devpit\accounts\claude\work\skills`})
		return ch
	}
	for _, st := range p.Steps {
		ch <- accounts.NewEvent(st.Text, accounts.StepRunning, "")
		ch <- accounts.NewEvent(st.Text, accounts.StepDone, "")
	}
	s.journal = append(s.journal, entry{summary: p.Summary, before: s.Store.Clone()})
	final := accounts.NewEvent("Done", accounts.StepDone, fmt.Sprintf("%d step(s). Press u to undo.", len(p.Steps)))
	final.Final = true
	ch <- final
	return ch
}

// --- accounts already on this PC ---

// SampleFound is claude-acc with one account and six folder links.
func SampleFound() importer.Found {
	return importer.Found{ClaudeAcc: &importer.ClaudeAcc{
		Dir:      Home + `\.claude-switch`,
		Accounts: []importer.AccAccount{{Name: "client", Dir: Home + `\.claude-switch\accounts\client`, Email: "you@client.example", SignedIn: true}},
		Links: []importer.AccLink{
			{Folder: `C:\Clients\acme`, Account: "client"},
			{Folder: `C:\Clients\acme-api`, Account: "client"},
			{Folder: `C:\Clients\bolt`, Account: "client"},
			{Folder: `C:\Clients\bolt-web`, Account: "client"},
			{Folder: `D:\work\fixtures`, Account: "client"},
			{Folder: `D:\work\demo`, Account: "default"},
		},
		ProfileLines: []importer.ProfileLine{{File: Home + `\Documents\PowerShell\Microsoft.PowerShell_profile.ps1`, Line: 12, Text: "Invoke-Expression (& claude-acc init pwsh | Out-String)"}},
	}}
}

// DetectClaudeAcc implements the screens' Service.
func (s *Service) DetectClaudeAcc(context.Context) (importer.Found, bool, error) {
	return s.Found, s.Found.ClaudeAcc != nil, nil
}

// PlanImport implements the screens' Service.
func (s *Service) PlanImport(f importer.Found, _ importer.Options) (importer.Plan, error) {
	p := importer.Plan{Summary: "Imported from claude-acc", BeforeHash: accounts.StoreHash(s.Store)}
	if c := f.ClaudeAcc; c != nil {
		for _, a := range c.Accounts {
			p.Accounts = append(p.Accounts, accounts.Account{Tool: accounts.ToolClaude, Name: a.Name, Email: a.Email, Dir: a.Dir, ImportedFrom: importer.FromClaudeAcc})
			p.Sentences = append(p.Sentences, "Add the Claude Code account "+a.Name+" ("+a.Email+"), using its folder where it is: "+a.Dir+".")
		}
		for _, l := range c.Links {
			p.Rules = append(p.Rules, importer.PlannedRule{Folder: l.Folder, Tool: accounts.ToolClaude, Account: l.Account})
		}
		p.Sentences = append(p.Sentences, fmt.Sprintf("Add %d folder rules for Claude Code, one per claude-acc link.", len(c.Links)))
	}
	p.Notes = []string{"Nobody signs in again: the accounts keep their sign-ins where they are."}
	return p, nil
}

// ApplyImport implements the screens' Service.
func (s *Service) ApplyImport(p importer.Plan, _ func(accounts.Event)) (accounts.Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("import")
	before := s.Store.Clone()
	for _, a := range p.Accounts {
		if err := s.Store.AddAccount(a); err != nil {
			return accounts.Entry{}, err
		}
	}
	for _, r := range p.Rules {
		if err := s.Store.SetRule(r.Folder, r.Tool, r.Account); err != nil {
			return accounts.Entry{}, err
		}
	}
	s.journal = append(s.journal, entry{summary: p.Summary, before: before})
	return accounts.Entry{ID: "demo", Summary: p.Summary}, nil
}

// ImportDismissed implements the screens' Service.
func (s *Service) ImportDismissed(importer.Found) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dismissed != ""
}

// DismissImport implements the screens' Service.
func (s *Service) DismissImport(importer.Found) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.note("dismiss import")
	s.dismissed = "yes"
	return nil
}
