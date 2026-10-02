package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// Problem is something wrong for a tool in a folder, in plain words, with
// its one-sentence fix. Kind is an accounts.ProblemKind, a shim issue kind
// ("real tool ahead of the shim", "new terminal needed"…) or a stale rule
// kind ("folder not found", "drive not connected").
type Problem struct {
	Kind    string `json:"kind"`
	Tool    string `json:"tool,omitempty"`
	Message string `json:"message"`
	Fix     string `json:"fix,omitempty"`
}

// Line is the problem as the command line prints it.
func (p Problem) Line() string {
	if p.Fix == "" {
		return "! " + p.Message
	}
	return "! " + p.Message + "\n  Fix: " + p.Fix
}

// ToolStatus is which account one tool uses in one folder, why, and what is
// wrong. Nothing in it was asked of the tool except, for Git, its offline
// config; who an account is comes from what Devpit recorded.
type ToolStatus struct {
	Tool accounts.Tool
	// Folder is the folder asked about.
	Folder string
	// Resolution is the account, the reason and the rule chain.
	Resolution accounts.Resolution
	// Account is the account in use ("default" when nothing else applies).
	Account accounts.Account
	// Identity is who that account is as far as Devpit knows.
	Identity accounts.Identity
	// Display names the account and who it is, always together:
	// "work (zubair@work.com)", "Zubair <zubair@work.com>",
	// "default (not checked yet)", "project: quiz-slayer".
	Display string
	// Why is the reason, in the Accounts page's words: "folder rule:
	// C:\Work", "everywhere", "set by this project's .env.local".
	Why string
	// EverywhereDisplay is the account used wherever no folder rule applies.
	EverywhereDisplay string
	// Installed: found on PATH outside the shim folder (not run). Path is
	// where.
	Installed bool
	Path      string
	// Managed: Devpit has an account, a rule or an "everywhere" choice for
	// this tool. Unmanaged tools are left fully untouched.
	Managed bool
	// Supports is what Devpit can do with this tool (static; see
	// adapters.Supports).
	Supports adapters.Caps
	// Problems are the problems for this tool here, each with its fix.
	Problems []Problem
}

// Status says which account tool uses in folder, and why. It never runs a
// tool except Git's offline config read for Git's default identity.
func (s *Service) Status(ctx context.Context, tool accounts.Tool, folder string) (ToolStatus, error) {
	st, _, err := s.Load()
	if err != nil {
		return ToolStatus{}, err
	}
	return s.status(ctx, st, tool, folder, s.shimProblems(st))
}

// Overview is the whole Accounts table for one folder.
type Overview struct {
	Folder string
	Tools  []ToolStatus
	// Warning is set once when accounts.toml was unusable and moved aside.
	Warning string
}

// Overview builds the table for folder, one row per tool in display order.
// Like Status, it never runs Claude Code or any other tool's sign-in check.
func (s *Service) Overview(ctx context.Context, folder string) (Overview, error) {
	st, warn, err := s.Load()
	if err != nil {
		return Overview{}, err
	}
	shimProbs := s.shimProblems(st)
	out := Overview{Folder: folder, Warning: warn}
	for _, t := range accounts.Tools() {
		ts, err := s.status(ctx, st, t, folder, shimProbs)
		if err != nil {
			return Overview{}, err
		}
		out.Folder = ts.Folder
		out.Tools = append(out.Tools, ts)
	}
	return out, nil
}

func (s *Service) status(ctx context.Context, st *accounts.Store, tool accounts.Tool, folder string, shimProbs map[accounts.Tool][]Problem) (ToolStatus, error) {
	a, err := s.Adapter(tool)
	if err != nil {
		return ToolStatus{}, err
	}
	res, err := accounts.Resolve(st, tool, folder, accounts.ResolveOptions{RealPath: accounts.RealPath})
	if err != nil {
		return ToolStatus{}, err
	}
	ts := ToolStatus{
		Tool: tool, Folder: res.Folder, Resolution: res, Account: res.Account,
		Managed: st.Manages(tool), Supports: adapters.Supports(tool), Why: res.Why(),
	}
	bins := []string{tool.Binary()}
	if tool == accounts.ToolConvex {
		bins = append(bins, "npx")
	}
	for _, b := range bins {
		if p, err := s.lookPath(b); err == nil {
			ts.Installed, ts.Path = true, p
			break
		}
	}
	ts.Identity = a.Cached(st, res.Account)
	if tool == accounts.ToolGit && res.Account.IsDefault() && ts.Installed {
		// Git's own identity is read offline from its config: harmless.
		if id, werr := a.WhoAmI(ctx, res.Account); werr == nil {
			ts.Identity = id
		}
	}
	ts.Display = s.display(st, res.Account, ts.Identity)
	ts.EverywhereDisplay = s.everywhereDisplay(st, tool)

	if tool == accounts.ToolConvex {
		if info, cerr := adapters.ConvexProject(res.Folder); cerr == nil {
			ts.Identity = info.Identity()
			ts.Display, ts.Why = info.Identity().Fields().Name, info.Why()
			if !info.Found {
				ts.Display = "no project here"
			}
		}
		ts.EverywhereDisplay = "each project picks its own"
	}
	if tool == accounts.ToolCloudflare && res.Reason == accounts.ReasonEverywhere {
		ts.Why += " · beta"
	}

	for _, p := range res.Problems {
		ts.Problems = append(ts.Problems, fromAccounts(p))
	}
	for _, p := range adapters.CheckEnv(tool, s.getenv) {
		ts.Problems = append(ts.Problems, fromAccounts(p))
	}
	for _, sr := range accounts.StaleRules(st, nil) {
		if _, ok := sr.Rule.Accounts[tool]; ok {
			ts.Problems = append(ts.Problems, Problem{Kind: string(sr.Kind), Tool: string(tool), Message: sr.Message, Fix: accounts.StaleFix})
		}
	}
	ts.Problems = append(ts.Problems, shimProbs[tool]...)
	return ts, nil
}

func fromAccounts(p accounts.Problem) Problem {
	return Problem{Kind: string(p.Kind), Tool: string(p.Tool), Message: p.Message, Fix: p.Fix}
}

// shimProblems checks the shims of every tool that needs one. Problems that
// are not about one tool (the folder is not on PATH) are listed under each
// tool that needs a shim.
func (s *Service) shimProblems(st *accounts.Store) map[accounts.Tool][]Problem {
	var need []accounts.Tool
	for _, t := range accounts.Tools() {
		if st.NeedsShim(t) {
			need = append(need, t)
		}
	}
	out := map[accounts.Tool][]Problem{}
	if len(need) == 0 || s.Shims == nil {
		return out
	}
	for _, is := range s.Shims.Check(need) {
		p := Problem{Kind: string(is.Kind), Tool: string(is.Tool), Message: is.Message, Fix: is.Fix}
		if is.Tool != "" {
			out[is.Tool] = append(out[is.Tool], p)
			continue
		}
		for _, t := range need {
			p.Tool = string(t)
			out[t] = append(out[t], p)
		}
	}
	return out
}

// display names an account and who it is, always together.
func (s *Service) display(st *accounts.Store, acct accounts.Account, id accounts.Identity) string {
	who := id.Who()
	switch acct.Tool {
	case accounts.ToolGit:
		f := id.Fields()
		switch {
		case f.Name != "" && f.Email != "":
			return f.Name + " <" + f.Email + ">"
		case f.Email != "":
			return "<" + f.Email + ">"
		}
	case accounts.ToolGitHub:
		if acct.Label != "" {
			who = acct.Label
		}
	}
	if who == "" {
		if acct.IsDefault() {
			if e := st.DefaultEmail[acct.Tool]; e != "" {
				return accounts.DefaultName + " (" + e + ")"
			}
			return accounts.DefaultName + " (not checked yet)"
		}
		return acct.Name + " (no email recorded)"
	}
	return acct.Name + " (" + who + ")"
}

// everywhereDisplay is the account a tool uses wherever no rule applies.
func (s *Service) everywhereDisplay(st *accounts.Store, tool accounts.Tool) string {
	name := accounts.DefaultName
	if v, ok := st.Everywhere[tool]; ok {
		name = v
	}
	acct, ok := st.Account(tool, name)
	if !ok {
		return name + " (no such account; default is used)"
	}
	a, err := s.Adapter(tool)
	if err != nil {
		return name
	}
	d := s.display(st, acct, a.Cached(st, acct))
	if tool == accounts.ToolCloudflare && acct.IsDefault() {
		d += ", Wrangler's own login"
	}
	return d
}

// Lines is the status as `devpit <tool>` prints it: the tool and the
// account, the reason, the account used everywhere else, then the problems.
func (ts ToolStatus) Lines() []string {
	name := ts.Tool.DisplayName()
	w := max(utf8.RuneCountInString(name), len("Everywhere")) + 2
	pad := func(s string) string { return s + strings.Repeat(" ", w-utf8.RuneCountInString(s)) }
	acct := ts.Display
	if !ts.Installed {
		acct += " — " + name + " is not installed"
		if ts.Managed {
			acct += "; its rules are kept"
		}
	}
	out := []string{pad(name) + acct, pad("Why") + ts.Why, pad("Everywhere") + ts.EverywhereDisplay}
	if ts.Supports.ShowOnly || ts.Supports.Beta {
		out = append(out, pad("Note")+ts.Supports.Why)
	}
	for _, p := range ts.Problems {
		out = append(out, strings.Split(p.Line(), "\n")...)
	}
	return out
}

// Text is Lines joined with newlines.
func (ts ToolStatus) Text() string { return strings.Join(ts.Lines(), "\n") }

// Lines is the table as `devpit accounts` prints it.
func (o Overview) Lines() []string {
	out := []string{"Accounts in " + o.Folder, ""}
	if o.Warning != "" {
		out = append([]string{"! " + o.Warning, ""}, out...)
	}
	toolW, acctW := 0, 0
	cells := make([][3]string, 0, len(o.Tools))
	for _, ts := range o.Tools {
		acct, why := ts.Display, ts.Why
		if !ts.Installed {
			acct, why = "not installed", ""
			if ts.Managed {
				why = "rules kept"
			}
		}
		c := [3]string{ts.Tool.DisplayName(), acct, why}
		cells = append(cells, c)
		toolW = max(toolW, utf8.RuneCountInString(c[0]))
		acctW = max(acctW, utf8.RuneCountInString(c[1]))
	}
	for _, c := range cells {
		line := c[0] + strings.Repeat(" ", toolW-utf8.RuneCountInString(c[0])+2) + c[1]
		if c[2] != "" {
			line += strings.Repeat(" ", acctW-utf8.RuneCountInString(c[1])+2) + c[2]
		}
		out = append(out, strings.TrimRight(line, " "))
	}
	seen := map[string]bool{}
	first := true
	for _, ts := range o.Tools {
		for _, p := range ts.Problems {
			k := p.Kind + "\x00" + p.Message
			if seen[k] {
				continue
			}
			seen[k] = true
			if first {
				out = append(out, "")
				first = false
			}
			out = append(out, strings.Split(p.Line(), "\n")...)
		}
	}
	return out
}

// Text is Lines joined with newlines.
func (o Overview) Text() string { return strings.Join(o.Lines(), "\n") }

// ParseTool turns what a person typed ("claude", "gh", "wrangler") into a
// tool, with an error that lists the tools.
func ParseTool(s string) (accounts.Tool, error) {
	t, err := accounts.ParseTool(s)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrUnknownTool, err)
	}
	return t, nil
}

// IsUsageError reports whether err is about what was typed (an unknown tool,
// an account name that matches nothing or more than one), which the command
// line reports with its "wrong usage" exit code.
func IsUsageError(err error) bool {
	var amb *accounts.AmbiguousNameError
	var unk *accounts.UnknownNameError
	return errors.Is(err, ErrUnknownTool) || errors.As(err, &amb) || errors.As(err, &unk) ||
		errors.Is(err, accounts.ErrInvalidName) || errors.Is(err, accounts.ErrReservedName) || errors.Is(err, accounts.ErrRelativePath)
}
