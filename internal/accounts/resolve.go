package accounts

import (
	"fmt"
	"slices"
	"strings"
)

// Reason says why a tool uses the account it does.
type Reason string

const (
	// ReasonEverywhere: no folder rule for this tool covers the folder, so
	// the "everywhere" choice applies (the default account when none is
	// set).
	ReasonEverywhere Reason = "everywhere"
	// ReasonFolderRule: the nearest folder rule for this tool won.
	ReasonFolderRule Reason = "folder rule"
)

// MatchedVia says which spelling of the folder a rule matched.
type MatchedVia string

const (
	// ViaGiven: the folder as given (typed, or the current directory).
	ViaGiven MatchedVia = "as given"
	// ViaResolved: the folder after junctions, symlinks and subst drives.
	ViaResolved MatchedVia = "resolved"
)

// ProblemKind names a problem the resolver stepped around.
type ProblemKind string

const (
	// ProblemMissingAccount: a rule names an account that no longer exists.
	// The rule is skipped and the next rule out (or "everywhere") applies.
	ProblemMissingAccount ProblemKind = "missing account"
	// ProblemEverywhereMissing: the "everywhere" choice names an account
	// that no longer exists, so the default account applies.
	ProblemEverywhereMissing ProblemKind = "everywhere names a missing account"
	// ProblemEnvOverride: a variable in this terminal, set by something
	// other than Devpit, decides or overrides the tool's account (a stray
	// CLAUDE_CONFIG_DIR, a GH_TOKEN). Found by adapters.CheckEnv.
	ProblemEnvOverride ProblemKind = "set in this terminal"
)

// Problem is something wrong in the store that resolution stepped around.
type Problem struct {
	Kind    ProblemKind
	Tool    Tool
	Folder  string // the rule's folder; empty for "everywhere"
	Account string // the name that no longer exists
	// Variable is the environment variable, for ProblemEnvOverride.
	Variable string
	Message  string // a sentence for a person
	// Fix says, in one sentence, what to do about it.
	Fix string
}

// ChainLink is one rule that covers the folder for the tool.
type ChainLink struct {
	Folder  string
	Account string
	// Won is true for the one rule that decided the account.
	Won bool
	// Skipped is true for a rule whose account no longer exists.
	Skipped bool
}

// Resolution is which account a tool uses in a folder, and why.
type Resolution struct {
	Tool Tool
	// Folder is the folder asked about, normalized.
	Folder string
	// ResolvedFolder is where Folder really is after junctions, symlinks and
	// subst drives, when that differs; "" otherwise.
	ResolvedFolder string
	// Account is the account in use. Its Name is "default" when nothing
	// else applies.
	Account Account
	// Reason is ReasonFolderRule or ReasonEverywhere.
	Reason Reason
	// RuleFolder is the folder of the winning rule, for ReasonFolderRule.
	RuleFolder string
	// Via says whether the rule matched the folder as given or its resolved
	// path. Empty for ReasonEverywhere.
	Via MatchedVia
	// Chain lists every rule for this tool that covers the folder, outer
	// first, with the winner marked: what a screen shows for nested rules.
	Chain []ChainLink
	// Problems lists rules that were skipped and why.
	Problems []Problem
}

// IsDefault reports whether the tool uses its default account, so the shim
// starts it untouched.
func (r Resolution) IsDefault() bool { return r.Account.IsDefault() }

// Why is the Accounts page's "why" column: "everywhere", or "folder rule:
// C:\Work", plus the resolved path when only that matched.
func (r Resolution) Why() string {
	if r.Reason != ReasonFolderRule {
		return string(ReasonEverywhere)
	}
	s := "folder rule: " + r.RuleFolder
	if r.Via == ViaResolved && r.ResolvedFolder != "" {
		s += " (through " + r.ResolvedFolder + ")"
	}
	return s
}

// ResolveOptions tunes [Resolve].
type ResolveOptions struct {
	// Base resolves a relative folder. Usually the current directory.
	Base string
	// RealPath resolves junctions, symlinks and subst drives. nil skips that
	// second look; the shim and the app pass [RealPath].
	RealPath func(string) (string, error)
}

// Resolve returns the account tool uses in folder. The nearest rule that
// names tool wins, one tool at a time: a rule that sets only github does not
// change claude. A rule naming an account that no longer exists is skipped,
// reported in Problems, and the next rule out applies. The folder does not
// need to exist.
//
// The folder is matched as given first. Only if no rule matches it that way
// is its resolved path (through junctions and subst drives) tried, and Via
// says which one matched.
func Resolve(s *Store, tool Tool, folder string, opt ResolveOptions) (Resolution, error) {
	if s == nil {
		s = NewStore()
	}
	norm, err := NormalizeFolder(folder, opt.Base)
	if err != nil {
		return Resolution{}, err
	}
	res := Resolution{Tool: tool, Folder: norm}

	chain, won, probs := ruleChain(s, tool, norm)
	via := ViaGiven
	if won < 0 && opt.RealPath != nil && hasRulesFor(s, tool) {
		if resolved, rerr := opt.RealPath(norm); rerr == nil && resolved != "" && !SameFolder(resolved, norm) {
			res.ResolvedFolder = resolved
			rc, rw, rp := ruleChain(s, tool, resolved)
			if rw >= 0 {
				chain, won, probs, via = rc, rw, rp, ViaResolved
			}
		}
	}
	res.Chain = chain
	res.Problems = probs

	if won >= 0 {
		link := chain[won]
		a, _ := s.Account(tool, link.Account)
		res.Account = a
		res.Reason = ReasonFolderRule
		res.RuleFolder = link.Folder
		res.Via = via
		return res, nil
	}

	res.Reason = ReasonEverywhere
	res.Account = Account{Tool: tool, Name: DefaultName}
	if v, ok := s.Everywhere[tool]; ok && !IsDefault(v) {
		if a, ok := s.Account(tool, v); ok {
			res.Account = a
		} else {
			res.Problems = append(res.Problems, Problem{
				Kind: ProblemEverywhereMissing, Tool: tool, Account: v,
				Message: fmt.Sprintf("%s is set to use %q everywhere, but that account no longer exists, so the default account is used.",
					tool.DisplayName(), v),
				Fix: fmt.Sprintf("Pick the account to use everywhere again: devpit %s use <name> --everywhere", tool),
			})
		}
	}
	return res, nil
}

// ResolveAll resolves every tool for one folder, in display order.
func ResolveAll(s *Store, folder string, opt ResolveOptions) ([]Resolution, error) {
	out := make([]Resolution, 0, 8)
	for _, t := range Tools() {
		r, err := Resolve(s, t, folder, opt)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func hasRulesFor(s *Store, tool Tool) bool {
	for _, r := range s.Rules {
		if _, ok := r.Accounts[tool]; ok {
			return true
		}
	}
	return false
}

// ruleChain collects the rules for tool that cover folder, outer first, and
// returns the index of the winner (the innermost rule whose account exists)
// or -1.
func ruleChain(s *Store, tool Tool, folder string) ([]ChainLink, int, []Problem) {
	fk := folderKey(folder)
	type hit struct {
		rule  Rule
		depth int
	}
	var hits []hit
	for _, r := range s.Rules {
		if _, ok := r.Accounts[tool]; !ok || !keyContains(folderKey(r.Folder), fk) {
			continue
		}
		hits = append(hits, hit{rule: r, depth: folderDepth(r.Folder)})
	}
	slices.SortStableFunc(hits, func(a, b hit) int { return a.depth - b.depth })

	chain := make([]ChainLink, 0, len(hits))
	var probs []Problem
	won := -1
	for i, h := range hits {
		name := h.rule.Accounts[tool]
		link := ChainLink{Folder: h.rule.Folder, Account: name}
		if _, ok := s.Account(tool, name); ok {
			won = i
		} else {
			link.Skipped = true
			probs = append(probs, Problem{
				Kind: ProblemMissingAccount, Tool: tool, Folder: h.rule.Folder, Account: name,
				Message: fmt.Sprintf("The rule on %s says %s should use %q, but that account no longer exists, so the rule is skipped.",
					h.rule.Folder, tool.DisplayName(), name),
				Fix: fmt.Sprintf("Point the folder at an account that exists: devpit %s use <name> --folder \"%s\", or open devpit › Accounts › Browse folders to remove the rule.",
					tool, h.rule.Folder),
			})
		}
		chain = append(chain, link)
	}
	if won >= 0 {
		chain[won].Won = true
		// Use the canonical spelling of the account name.
		if a, ok := s.Account(tool, chain[won].Account); ok {
			chain[won].Account = a.Name
		}
	}
	return chain, won, probs
}

// String is a one-line summary, for logs and tests.
func (r Resolution) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s in %s: %s (%s)", r.Tool, r.Folder, r.Account.Name, r.Why())
	return b.String()
}
