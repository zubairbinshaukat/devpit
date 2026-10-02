package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/shims"
)

// Plan builds the preview of a change: the plain-words sentences (Git shows
// its exact config lines; "everywhere" lists the folder rules that still
// win), every file it writes, and its warnings. It changes nothing. A scope
// the tool does not support fails with the tool's own reason, wrapping
// accounts.ErrNotSupported.
func (s *Service) Plan(ctx context.Context, c accounts.Change) (accounts.Preview, error) {
	st, _, err := s.Load()
	if err != nil {
		return accounts.Preview{}, err
	}
	return s.plan(ctx, st, c)
}

func (s *Service) plan(ctx context.Context, st *accounts.Store, c accounts.Change) (accounts.Preview, error) {
	a, err := s.Adapter(c.Tool)
	if err != nil {
		return accounts.Preview{}, err
	}
	caps := a.Capabilities(ctx)
	if !caps.Supports(c.Scope.Kind) && !c.Remove {
		why := caps.Why
		if why == "" {
			why = "Devpit cannot do this for " + c.Tool.DisplayName() + "."
		}
		return accounts.Preview{}, fmt.Errorf("%s: %w", strings.TrimRight(why, "."), accounts.ErrNotSupported)
	}
	p, err := a.Plan(accounts.PreviewInput{
		Store: st, Change: c, StorePath: s.DisplayPath(s.Deps.Paths.Store), Home: s.Deps.Home,
	})
	if err != nil || p.NoChange {
		return p, err
	}
	if err := s.shimGuard(st, c); err != nil {
		return accounts.Preview{}, err
	}
	return p, nil
}

// shimGuard stops a change before anything is written when it needs a shim
// for its tool and none can be made: devpit-shim.exe is missing next to
// devpit.exe and the tool has no shim yet, so the rule could never apply.
// "Just this once" and removing a rule need no shim.
func (s *Service) shimGuard(st *accounts.Store, c accounts.Change) error {
	if s.Shims == nil || c.Remove || c.Scope.Kind == accounts.ScopeOnce || c.Tool.ShimName() == "" {
		return nil
	}
	after, err := c.ApplyTo(st)
	if err != nil || !after.NeedsShim(c.Tool) {
		return nil //nolint:nilerr // the adapter's own plan reports a bad change
	}
	if _, err := os.Stat(s.Shims.ShimPath(c.Tool)); err == nil {
		return nil // the shim is already there; the rule works
	}
	if err := s.Shims.CheckSource(); err != nil {
		return fmt.Errorf("nothing was changed: %w", err)
	}
	return nil
}

// Apply makes a previewed change and reports each step; the last event is
// Final and carries the journal entry id for undo. After the change is
// saved, Devpit's shims are brought in line (a shim for every tool that now
// needs one, the shim folder first on the user PATH), each as its own step.
// A shim problem is reported as a warning: the change itself stands.
func (s *Service) Apply(ctx context.Context, p accounts.Preview) <-chan accounts.Event {
	out := make(chan accounts.Event, 16)
	go func() {
		defer close(out)
		a, err := s.Adapter(p.Change.Tool)
		if err != nil {
			out <- accounts.Failed("Saving the change", err)
			return
		}
		if !p.NoChange {
			st, _, lerr := s.Load()
			if lerr == nil {
				lerr = s.shimGuard(st, p.Change)
			}
			if lerr != nil {
				out <- accounts.Failed("Saving the change", lerr)
				return
			}
		}
		var final *accounts.Event
		for ev := range adapters.Apply(ctx, s.Engine, a, p) {
			if ev.Final {
				e := ev
				final = &e
				continue
			}
			out <- ev
		}
		if final == nil {
			out <- accounts.Failed("Saving the change", fmt.Errorf("the change ended without a result"))
			return
		}
		if final.State == accounts.StepDone {
			s.syncShims(func(ev accounts.Event) { out <- ev })
		}
		out <- *final
	}()
	return out
}

// SyncShims makes the shims match accounts.toml and reports what it did as
// events (nil emit drops them). Apply, Undo, an import and a new account all
// call it; the Accounts page should call it when it opens, which repairs a
// missing or out-of-date shim.
func (s *Service) SyncShims(emit func(accounts.Event)) {
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	s.syncShims(emit)
}

func (s *Service) syncShims(emit func(accounts.Event)) {
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	if s.Shims == nil {
		return
	}
	st, _, err := s.Load()
	if err != nil {
		return
	}
	need := false
	for _, t := range accounts.Tools() {
		need = need || st.NeedsShim(t)
	}
	recorded, _, _ := s.Shims.Added()
	if !need && len(recorded) == 0 {
		return
	}
	shimWarning := func(err error) {
		msg := "The shims could not be set up: " + accounts.Scrub(err.Error()) + ". Run `devpit accounts verify` to see what is missing."
		if errors.Is(err, shims.ErrProgramMissing) {
			msg = "devpit-shim.exe is missing next to devpit.exe, so Devpit's shims cannot be made or refreshed. " + shims.ProgramMissingFix
		}
		emit(accounts.NewEvent("Setting up Devpit's shims", accounts.StepWarning, msg))
	}
	var created []accounts.Tool
	pathChanged := false
	if need {
		had := map[accounts.Tool]bool{}
		for _, t := range accounts.Tools() {
			if t.ShimName() != "" {
				_, serr := os.Stat(s.Shims.ShimPath(t))
				had[t] = serr == nil
			}
		}
		created, pathChanged, err = s.Shims.Sync(st)
		for _, t := range created {
			verb := "Added"
			if had[t] {
				verb = "Updated" // an older devpit-shim.exe, after an update
			}
			emit(accounts.NewEvent(verb+" Devpit's shim for "+t.DisplayName(), accounts.StepDone, s.Shims.ShimPath(t)))
		}
		if err != nil {
			shimWarning(err)
			return
		}
	}
	// Every shim Devpit made, needed now or not, is a copy of
	// devpit-shim.exe: after an update it is replaced by the new one.
	refreshed, err := s.Shims.RefreshUnneeded(st)
	for _, t := range refreshed {
		emit(accounts.NewEvent("Updated Devpit's shim for "+t.DisplayName(), accounts.StepDone, s.Shims.ShimPath(t)))
	}
	if err != nil {
		shimWarning(err)
		return
	}
	if !need {
		return
	}
	if pathChanged {
		emit(accounts.NewEvent("Put Devpit's shim folder first on your user PATH", accounts.StepDone, s.Shims.Dir))
	}
	if !shimDirOnPath(s.getenv("PATH"), s.Shims.Dir) {
		emit(accounts.NewEvent("Open a new terminal", accounts.StepInfo,
			"Terminals that are already open keep their old PATH, so the rule applies in new ones."))
	}
}

// ToolsWithAccount lists the tools that have an account called name
// (ignoring case), for `devpit use <name>`. "default" names every tool.
func ToolsWithAccount(st *accounts.Store, name string) []accounts.Tool {
	var out []accounts.Tool
	for _, t := range accounts.Tools() {
		if accounts.IsDefault(name) {
			if t != accounts.ToolConvex {
				out = append(out, t)
			}
			continue
		}
		if _, ok := st.Account(t, name); ok {
			out = append(out, t)
		}
	}
	return out
}

// PlanAll previews one account name for several tools at once (`devpit use
// work`), each against the accounts as they are now. Tools that cannot take
// the scope are left out with their reason in skipped.
func (s *Service) PlanAll(ctx context.Context, name string, scope accounts.Scope, tools []accounts.Tool) (previews []accounts.Preview, skipped []string, err error) {
	st, _, err := s.Load()
	if err != nil {
		return nil, nil, err
	}
	for _, t := range tools {
		p, perr := s.plan(ctx, st, accounts.Change{Tool: t, Account: name, Scope: scope})
		if perr != nil {
			skipped = append(skipped, t.DisplayName()+": "+accounts.Scrub(perr.Error()))
			continue
		}
		previews = append(previews, p)
	}
	return previews, skipped, nil
}

// ApplyAll applies previews made by PlanAll as one group, so one Undo takes
// them all back. Each is planned again right before it is applied (an
// earlier tool may have rewritten a shared file, such as Git's and GitHub's
// rules file); if the new plan says something else in plain words, it stops
// and says so. emit gets every step.
func (s *Service) ApplyAll(ctx context.Context, previews []accounts.Preview, emit func(accounts.Event)) error {
	group := accounts.NewGroup()
	for i, shown := range previews {
		p := shown
		if i > 0 {
			fresh, err := s.Plan(ctx, shown.Change)
			if err != nil {
				return err
			}
			if !slices.Equal(fresh.Sentences, shown.Sentences) {
				return fmt.Errorf("what %s would do changed after the earlier steps: %w", shown.Change.Tool.DisplayName(), accounts.ErrStalePreview)
			}
			if fresh.NoChange {
				continue
			}
			p = fresh
		}
		if p.NoChange {
			continue
		}
		p.Group = group
		var last accounts.Event
		for ev := range s.Apply(ctx, p) {
			emit(ev)
			last = ev
		}
		if last.State != accounts.StepDone {
			if last.Err != nil {
				return last.Err
			}
			return fmt.Errorf("%s: the change did not finish", p.Change.Tool.DisplayName())
		}
	}
	return nil
}

// FolderCheck says whether folder needs the person to confirm it is really
// meant: the home folder ("this folder" would cover every project in it)
// and a drive root. reason is a sentence; "" means fine.
func (s *Service) FolderCheck(folder string) (reason string) {
	norm, err := accounts.NormalizeFolder(folder, "")
	if err != nil {
		return ""
	}
	if s.Deps.Home != "" && accounts.SameFolder(norm, s.Deps.Home) {
		return "You are in your home folder, so \"this folder\" would cover every project inside it."
	}
	if accounts.IsDriveRoot(norm) {
		return "You are at the root of " + norm + ", so \"this folder\" would cover the whole drive."
	}
	return ""
}

func shimDirOnPath(path, dir string) bool {
	for _, e := range strings.Split(path, ";") {
		if accounts.SameFolder(strings.Trim(strings.TrimSpace(e), `"`), dir) {
			return true
		}
	}
	return false
}
