package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// VerifyStatus is the outcome of one check.
type VerifyStatus string

const (
	// VerifyOK: what is set up is what should be.
	VerifyOK VerifyStatus = "ok"
	// VerifyMismatch: it is not, and Notes say why and what to do.
	VerifyMismatch VerifyStatus = "mismatch"
	// VerifyInfo: nothing to compare (a tool Devpit does not manage, Convex).
	VerifyInfo VerifyStatus = "info"
	// VerifyNotInstalled: the tool is not on PATH.
	VerifyNotInstalled VerifyStatus = "not installed"
	// VerifyError: the check itself failed.
	VerifyError VerifyStatus = "error"
)

// VerifyOptions says what Verify checks.
type VerifyOptions struct {
	// Folder is the folder to check, normally the current one.
	Folder string
	// All also checks every other account of every tool, not only the one
	// active in Folder.
	All bool
	// Risky allows, with All, the live checks a tool says can harm an idle
	// account (Claude Code: LiveCheckRisk). Only after the person agreed.
	Risky bool
}

// VerifyCheck is one account checked.
type VerifyCheck struct {
	Tool    accounts.Tool
	Account string
	// Here: the account active in the folder (else one checked for --all).
	Here bool
	// Expected is who Devpit expects ("work (z@work.com)").
	Expected string
	// Actual is who the tool says it is; nil when nothing was asked.
	Actual *accounts.Identity
	// ActualDisplay says it in words ("signed in as z@work.com").
	ActualDisplay string
	// How: "live" (the tool was asked), "offline" (config files read), or
	// "not run".
	How    string
	Status VerifyStatus
	Notes  []string
}

// VerifyReport is everything Verify found.
type VerifyReport struct {
	Folder string
	Checks []VerifyCheck
	// Problems are every problem found, for every tool, with its fix.
	Problems []Problem
	// Skipped says, per tool, which accounts were not checked live and why,
	// and how to check them anyway.
	Skipped []string
	// Mismatch is set when anything is not as it should be: the command line
	// exits 4.
	Mismatch bool
}

// Verify compares, for every installed tool, the account Devpit expects in
// the folder with what the tool says, and lists every problem (an override
// variable, a rule naming a missing account, a stale rule, a shim shadowed
// on PATH, Wrangler's bindings out of step, Git's include not last…).
//
// It asks each tool only what is safe: the account active in the folder,
// and with All every other account whose live check cannot harm it. A
// risky live check of an idle account (Claude Code, see LiveCheckRisk)
// runs only with All and Risky; otherwise Skipped says why and how.
func (s *Service) Verify(ctx context.Context, opt VerifyOptions) (VerifyReport, error) {
	st, _, err := s.Load()
	if err != nil {
		return VerifyReport{}, err
	}
	shimProbs := s.shimProblems(st)
	rep := VerifyReport{Folder: opt.Folder}
	for _, t := range accounts.Tools() {
		ts, err := s.status(ctx, st, t, opt.Folder, shimProbs)
		if err != nil {
			return VerifyReport{}, err
		}
		rep.Folder = ts.Folder
		a, err := s.Adapter(t)
		if err != nil {
			return VerifyReport{}, err
		}
		rep.Problems = append(rep.Problems, ts.Problems...)
		if ts.Managed && len(ts.Problems) > 0 {
			rep.Mismatch = true
		}
		if !ts.Installed {
			c := VerifyCheck{Tool: t, Account: ts.Account.Name, Here: true, Expected: ts.Display, How: "not run", Status: VerifyNotInstalled}
			if ts.Managed {
				c.Notes = append(c.Notes, t.DisplayName()+" is not installed; its rules are kept and apply once it is.")
			}
			rep.Checks = append(rep.Checks, c)
			continue
		}
		var c VerifyCheck
		switch t {
		case accounts.ToolConvex:
			c = VerifyCheck{
				Tool: t, Account: ts.Display, Here: true, Expected: ts.Display, How: "offline", Status: VerifyInfo,
				Notes: []string{ts.Why + ". Convex picks the account per project; Devpit shows it but does not switch it."},
			}
		case accounts.ToolGit:
			c = s.verifyGit(ctx, a, st, ts)
		default:
			c = s.verifyLive(ctx, a, st, ts.Account, ts, true)
			if t == accounts.ToolGitHub {
				s.addPushNotes(ctx, a, st, ts, &c)
			}
		}
		if t == accounts.ToolCloudflare {
			drift, derr := adapters.CloudflareDrift(s.Deps, st)
			if derr != nil {
				c.Notes = append(c.Notes, accounts.Scrub(derr.Error()))
			}
			for _, p := range drift {
				rep.Problems = append(rep.Problems, Problem{
					Kind: string(p.Kind), Tool: string(t), Message: p.Message,
					Fix: "Run `devpit cloudflare use <account> --folder <folder> --yes` to set the binding again, or remove a binding made by hand with `wrangler auth deactivate <folder>`.",
				})
				rep.Mismatch = true
				if c.Status == VerifyOK {
					c.Status = VerifyMismatch
				}
			}
		}
		if c.Status == VerifyMismatch || (c.Status == VerifyError && ts.Managed) {
			rep.Mismatch = true
		}
		rep.Checks = append(rep.Checks, c)

		if opt.All && t != accounts.ToolGit && t != accounts.ToolConvex {
			s.verifyOthers(ctx, a, st, ts, opt, &rep)
		}
	}
	return rep, nil
}

// verifyOthers checks the accounts of a tool that are not active here.
func (s *Service) verifyOthers(ctx context.Context, a adapters.Adapter, st *accounts.Store, ts ToolStatus, opt VerifyOptions, rep *VerifyReport) {
	others := []accounts.Account{}
	for _, acct := range adapters.DefaultAndStore(ts.Tool, st) {
		if !strings.EqualFold(acct.Name, ts.Account.Name) {
			others = append(others, acct)
		}
	}
	if len(others) == 0 {
		return
	}
	if risky, why := a.LiveCheckRisk(); risky && !opt.Risky {
		names := make([]string, 0, len(others))
		for _, o := range others {
			names = append(names, o.Name)
		}
		rep.Skipped = append(rep.Skipped, fmt.Sprintf("%s accounts not checked: %s. %s To check them anyway: devpit accounts verify --all (it asks first), or add --yes once the user agrees.",
			ts.Tool.DisplayName(), strings.Join(names, ", "), why))
		return
	}
	for _, o := range others {
		c := s.verifyLive(ctx, a, st, o, ts, false)
		if c.Status == VerifyMismatch {
			rep.Mismatch = true
		}
		rep.Checks = append(rep.Checks, c)
	}
}

// verifyLive asks the tool who acct is and compares it with what Devpit
// recorded.
func (s *Service) verifyLive(ctx context.Context, a adapters.Adapter, st *accounts.Store, acct accounts.Account, ts ToolStatus, here bool) VerifyCheck {
	c := VerifyCheck{Tool: ts.Tool, Account: acct.Name, Here: here, How: "live", Status: VerifyOK}
	c.Expected = s.display(st, acct, a.Cached(st, acct))
	id, err := a.WhoAmI(ctx, acct)
	c.Actual = &id
	if err != nil {
		c.Status = VerifyError
		c.ActualDisplay = "could not check"
		c.Notes = append(c.Notes, accounts.Scrub(err.Error()))
		if errors.Is(err, accounts.ErrNotFoundTool) {
			c.Status = VerifyNotInstalled
		}
		return c
	}
	managed := ts.Managed
	switch id.State() {
	case accounts.StateSignedIn:
		c.ActualDisplay = "signed in as " + id.Who()
		want, got := acct.Email, id.Email()
		if ts.Tool == accounts.ToolGitHub {
			want, got = acct.Label, id.Login()
		}
		if !acct.IsDefault() && want != "" && got != "" && !strings.EqualFold(want, got) {
			c.Status = VerifyMismatch
			c.Notes = append(c.Notes, fmt.Sprintf("Devpit expects %s to be %s, but %s says it is %s. Sign in again with: devpit %s add",
				acct.Name, want, ts.Tool.DisplayName(), got, ts.Tool))
		}
		if acct.IsDefault() && st.DefaultEmail[ts.Tool] != "" && got != "" && !strings.EqualFold(st.DefaultEmail[ts.Tool], got) {
			c.Notes = append(c.Notes, fmt.Sprintf("Devpit last recorded the default account as %s.", st.DefaultEmail[ts.Tool]))
		}
	default:
		c.ActualDisplay = string(id.State())
		if n := id.Fields().Note; n != "" {
			c.Notes = append(c.Notes, n)
		}
		if managed {
			c.Status = VerifyMismatch
		} else {
			c.Status = VerifyInfo
		}
	}
	if !ts.Managed && c.Status == VerifyOK {
		c.Status = VerifyInfo
		c.Notes = append(c.Notes, "Devpit does not manage "+ts.Tool.DisplayName()+"; it uses its own sign-in everywhere.")
	}
	return c
}

// verifyGit asks Git, offline, who it would commit as in the folder.
func (s *Service) verifyGit(ctx context.Context, a adapters.Adapter, st *accounts.Store, ts ToolStatus) VerifyCheck {
	c := VerifyCheck{Tool: accounts.ToolGit, Account: ts.Account.Name, Here: true, Expected: ts.Display, How: "offline", Status: VerifyOK}
	g, ok := a.(*adapters.GitAdapter)
	if !ok {
		c.Status, c.How = VerifyInfo, "not run"
		return c
	}
	if fi, err := os.Stat(ts.Folder); err != nil || !fi.IsDir() {
		c.Status = VerifyInfo
		c.Notes = append(c.Notes, ts.Folder+" was not found, so Git was not asked.")
		return c
	}
	ci, err := g.CommitsAs(ctx, st, ts.Folder)
	if err != nil {
		c.Status = VerifyError
		c.Notes = append(c.Notes, accounts.Scrub(err.Error()))
		return c
	}
	if ci.Email.Value != "" {
		c.ActualDisplay = "commits as " + strings.TrimSpace(ci.Name.Value+" <"+ci.Email.Value+">") + " (from " + string(ci.Email.From) + ")"
	} else {
		c.ActualDisplay = "no user.email set"
	}
	if ci.Mismatch {
		c.Status = VerifyMismatch
	}
	c.Notes = append(c.Notes, ci.Notes...)
	if !ts.Managed && c.Status == VerifyOK {
		c.Status = VerifyInfo
	}
	return c
}

// addPushNotes adds which account a push from the folder uses.
func (s *Service) addPushNotes(ctx context.Context, a adapters.Adapter, st *accounts.Store, ts ToolStatus, c *VerifyCheck) {
	h, ok := a.(*adapters.GitHubAdapter)
	if !ok {
		return
	}
	if fi, err := os.Stat(ts.Folder); err != nil || !fi.IsDir() {
		return
	}
	push, err := h.PushesAs(ctx, st, ts.Folder)
	if err != nil {
		if !errors.Is(err, accounts.ErrNotFoundTool) {
			c.Notes = append(c.Notes, "Pushes: "+accounts.Scrub(err.Error()))
		}
		return
	}
	c.Notes = append(c.Notes, "Pushes go through "+string(push.Via)+".")
	c.Notes = append(c.Notes, push.Notes...)
	if ts.Managed && !ts.Account.IsDefault() && (push.Via == adapters.PushViaHelper || push.Via == adapters.PushViaRepoHelper) {
		c.Status = VerifyMismatch
	}
}

// Lines is the report as `devpit accounts verify` prints it.
func (r VerifyReport) Lines() []string {
	out := []string{"Verify in " + r.Folder, ""}
	mark := map[VerifyStatus]string{VerifyOK: "✔", VerifyMismatch: "✘", VerifyInfo: "·", VerifyNotInstalled: "·", VerifyError: "!"}
	for _, c := range r.Checks {
		who := c.Expected
		if !c.Here {
			who += " (not used here)"
		}
		line := fmt.Sprintf("%s %-11s %s", mark[c.Status], c.Tool.DisplayName(), who)
		switch {
		case c.Status == VerifyNotInstalled:
			line += " — not installed"
		case c.ActualDisplay != "":
			line += " — " + c.ActualDisplay
		}
		out = append(out, line)
		for _, n := range c.Notes {
			out = append(out, "    "+n)
		}
	}
	if len(r.Problems) > 0 {
		out = append(out, "")
		for _, p := range r.Problems {
			out = append(out, strings.Split(p.Line(), "\n")...)
		}
	}
	if len(r.Skipped) > 0 {
		out = append(out, "")
		for _, s := range r.Skipped {
			out = append(out, "· "+s)
		}
	}
	out = append(out, "")
	if r.Mismatch {
		out = append(out, "Something is not as it should be; see above.")
	} else {
		out = append(out, "Everything checked is as it should be.")
	}
	return out
}
