package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
)

// AccountEdit is renaming or removing a named account: the two changes the
// Accounts page's "manage accounts" makes that are not an accounts.Change.
// NewName set renames; Remove removes.
type AccountEdit struct {
	Tool    accounts.Tool
	Name    string
	NewName string
	Remove  bool
}

// RemoveWord is the word typed to confirm removing an account.
const RemoveWord = "REMOVE"

// AccountEditPreview is what the person sees before an AccountEdit: the
// sentences, the folder rules that fall back and to what, the exact files
// written, and anything to do first. Apply it with ApplyAccountEdit.
type AccountEditPreview struct {
	Edit AccountEdit
	// Sentences say what happens, in plain words.
	Sentences []string
	// FallsBackIntro introduces FallsBack, or is "" when it is empty.
	FallsBackIntro string
	// FallsBack are the folder rules that named the account: Display is
	// what each folder uses afterwards.
	FallsBack []accounts.KeptRule
	// Edits are the files written, accounts.toml first.
	Edits []accounts.FileEdit
	// Before is what to do before removing (the tool's own sign-out), and
	// Warnings anything else worth knowing.
	Before   []string
	Warnings []string
	// TypedWord is the word the person types to confirm; "" for a rename.
	TypedWord string
	Summary   string
	// BeforeHash is the store the preview was built from.
	BeforeHash string
	// gitEdits are the Git files written (Edits after accounts.toml).
	gitEdits []accounts.FileEdit
}

// Lines renders the preview as text.
func (p AccountEditPreview) Lines() []string {
	out := append([]string(nil), p.Sentences...)
	if len(p.FallsBack) > 0 {
		out = append(out, p.FallsBackIntro)
		for _, k := range p.FallsBack {
			out = append(out, "  "+k.Folder+"   "+k.Display)
		}
	}
	out = append(out, p.Before...)
	out = append(out, p.Warnings...)
	if len(p.Edits) > 0 {
		out = append(out, "", "Devpit will write:")
		for _, e := range p.Edits {
			out = append(out, "  "+e.Path+" ("+e.Action+")")
			for _, l := range e.Lines {
				out = append(out, "    "+l)
			}
		}
	}
	return out
}

// mutate is the edit applied to a store.
func (e AccountEdit) mutate(s *accounts.Store) error {
	if e.Remove {
		_, err := s.RemoveAccount(e.Tool, e.Name)
		return err
	}
	return s.RenameAccount(e.Tool, e.Name, e.NewName)
}

// touchesGit reports whether the edit changes Devpit's Git files: a Git
// identity has a file of its own, and GitHub's sign-in helper is there only
// while a GitHub rule is.
func (e AccountEdit) touchesGit() bool {
	return e.Tool == accounts.ToolGit || e.Tool == accounts.ToolGitHub
}

// PlanAccountEdit previews renaming or removing an account. It changes
// nothing. Removing lists every folder rule that named the account and what
// that folder uses afterwards; the account's folder (and the sign-in in it)
// is always kept.
func (s *Service) PlanAccountEdit(ctx context.Context, e AccountEdit) (AccountEditPreview, error) {
	st, _, err := s.Load()
	if err != nil {
		return AccountEditPreview{}, err
	}
	acct, err := st.FindAccount(e.Tool, e.Name)
	if err != nil {
		return AccountEditPreview{}, err
	}
	if acct.IsDefault() {
		return AccountEditPreview{}, fmt.Errorf("the default account is the tool's own sign-in; it cannot be renamed or removed: %w", accounts.ErrReservedName)
	}
	e.Name = acct.Name
	tool := e.Tool.DisplayName()
	p := AccountEditPreview{Edit: e, BeforeHash: accounts.StoreHash(st)}

	if !e.Remove {
		e.NewName = strings.TrimSpace(e.NewName)
		p.Edit.NewName = e.NewName
		if e.Tool == accounts.ToolCloudflare {
			return AccountEditPreview{}, fmt.Errorf("Wrangler names its profile when the account is added, so a Cloudflare account cannot be renamed. Remove it and add it again under the new name: %w", accounts.ErrNotSupported) //nolint:revive,staticcheck // a product name
		}
		if strings.EqualFold(e.NewName, acct.Name) && e.NewName == acct.Name {
			return AccountEditPreview{}, errors.New("that is already its name")
		}
		if !strings.EqualFold(e.NewName, acct.Name) {
			if err := st.CheckNewName(e.Tool, e.NewName); err != nil {
				return AccountEditPreview{}, err
			}
		}
		p.Summary = fmt.Sprintf("Renamed %s account %s to %s", tool, acct.Name, e.NewName)
		p.Sentences = []string{fmt.Sprintf("The %s account %s will be called %s.", tool, acct.Name, e.NewName)}
		if n := len(st.RulesUsing(e.Tool, acct.Name)); n > 0 {
			p.Sentences = append(p.Sentences, fmt.Sprintf("%s that use it will use it under its new name. Nothing else changes.", plural(n, "The folder rule", "The %d folder rules")))
		}
	} else {
		if e.Tool == accounts.ToolCloudflare && len(st.RulesUsing(e.Tool, acct.Name)) > 0 {
			return AccountEditPreview{}, fmt.Errorf("folder rules still use the Cloudflare account %s. Remove them first in Browse folders, so Wrangler's own folder bindings are taken off too: %w", acct.Name, accounts.ErrNotSupported)
		}
		p.TypedWord = RemoveWord
		p.Summary = fmt.Sprintf("Removed %s account %s", tool, acct.Name)
		p.Sentences = []string{fmt.Sprintf("Devpit will forget the %s account %s.", tool, acct.Name)}
		used := st.RulesUsing(e.Tool, acct.Name)
		if v, ok := st.Everywhere[e.Tool]; ok && strings.EqualFold(v, acct.Name) {
			p.Sentences = append(p.Sentences, fmt.Sprintf("Everywhere, %s goes back to its default account.", tool))
		}
		after := st.Clone()
		if _, err := after.RemoveAccount(e.Tool, acct.Name); err != nil {
			return AccountEditPreview{}, err
		}
		for _, r := range used {
			res, rerr := accounts.Resolve(after, e.Tool, r.Folder, accounts.ResolveOptions{})
			if rerr != nil {
				continue
			}
			a, _ := s.Adapter(e.Tool)
			d := res.Account.Name
			if a != nil {
				d = s.display(after, res.Account, a.Cached(after, res.Account))
			}
			why := "like everywhere else"
			if res.Reason == accounts.ReasonFolderRule {
				why = "from the rule on " + res.RuleFolder
			}
			p.FallsBack = append(p.FallsBack, accounts.KeptRule{Folder: r.Folder, Account: res.Account.Name, Display: d + ", " + why})
		}
		if n := len(p.FallsBack); n > 0 {
			p.FallsBackIntro = plural(n, "This folder used it and will use instead:", "These %d folders used it and will use instead:")
		}
		if acct.Dir != "" {
			p.Warnings = append(p.Warnings, "Its folder stays where it is, with its sign-in: "+acct.Dir+". Delete it yourself once you are sure you no longer need it.")
		}
		if hint := signOutHint(acct); hint != "" {
			p.Before = append(p.Before, "To sign it out first: "+hint)
		}
	}

	after := st.Clone()
	if err := e.mutate(after); err != nil {
		return AccountEditPreview{}, err
	}
	p.Edits = []accounts.FileEdit{{Path: s.DisplayPath(s.Deps.Paths.Store), Action: "changes", Lines: storeLines(after, e)}}
	if e.touchesGit() {
		if g, _, gerr := s.gitAdapters(); gerr == nil {
			gp, perr := g.PlanSettings(after, adapters.GitSettingsChange{})
			switch {
			case perr == nil && !gp.NoChange:
				p.gitEdits = gp.Edits
				p.Edits = append(p.Edits, gp.Edits...)
			case perr != nil && !errors.Is(perr, accounts.ErrNotFoundTool) && !errors.Is(perr, accounts.ErrTooOld):
				return AccountEditPreview{}, perr
			}
		}
	}
	_ = ctx
	return p, nil
}

// storeLines is what accounts.toml says about the account afterwards, for
// the preview.
func storeLines(after *accounts.Store, e AccountEdit) []string {
	if e.Remove {
		return []string{"[[account]] " + string(e.Tool) + " " + e.Name + " is removed, with every rule that named it"}
	}
	out := []string{"[[account]] " + string(e.Tool) + " " + e.Name + " is renamed " + e.NewName}
	for _, r := range after.RulesUsing(e.Tool, e.NewName) {
		out = append(out, "[[rule]] "+r.Folder+": "+string(e.Tool)+" = \""+e.NewName+"\"")
	}
	return out
}

// signOutHint is the tool's own sign-out for an account, in the words to
// type, or "" when the tool has none Devpit knows.
func signOutHint(a accounts.Account) string {
	switch a.Tool {
	case accounts.ToolClaude:
		return "devpit claude run " + a.Name + " -- claude auth logout"
	case accounts.ToolGitHub:
		if a.Label != "" {
			return "gh auth logout --hostname github.com --user " + a.Label
		}
	case accounts.ToolVercel:
		return "devpit vercel run " + a.Name + " -- vercel logout"
	case accounts.ToolFirebase:
		if a.Email != "" {
			return "firebase logout " + a.Email
		}
	case accounts.ToolSupabase:
		return "devpit supabase run " + a.Name + " -- supabase logout"
	}
	return ""
}

// ApplyAccountEdit makes a previewed rename or removal as one undoable
// change: accounts.toml and, for Git and GitHub, Devpit's Git files, each
// step reported. It stops with accounts.ErrStalePreview when anything
// changed since the preview.
func (s *Service) ApplyAccountEdit(ctx context.Context, p AccountEditPreview) <-chan accounts.Event {
	return adapters.Stream("Saving the change", func(emit func(accounts.Event)) error {
		emit(accounts.NewEvent("Recording the change so it can be undone", accounts.StepRunning, ""))
		txn, err := s.Engine.Begin(p.Summary, func(st *accounts.Store) error {
			if accounts.StoreHash(st) != p.BeforeHash {
				return accounts.ErrStalePreview
			}
			return p.Edit.mutate(st)
		})
		if err != nil {
			return err
		}
		emit(accounts.NewEvent("Recording the change so it can be undone", accounts.StepDone, ""))
		if len(p.gitEdits) > 0 {
			g, _, gerr := s.gitAdapters()
			if gerr == nil {
				pv := accounts.Preview{
					Change: accounts.Change{Tool: accounts.ToolGit, Scope: accounts.EverywhereScope()},
					Edits:  append([]accounts.FileEdit{p.Edits[0]}, p.gitEdits...),
				}
				gerr = g.Apply(ctx, txn, pv, emit)
			}
			if gerr != nil {
				if rerr := txn.Rollback(); rerr != nil {
					return fmt.Errorf("%w; putting things back also failed: %w", gerr, rerr)
				}
				return gerr
			}
		}
		emit(accounts.NewEvent("Saving accounts.toml", accounts.StepRunning, ""))
		entry, err := txn.Commit()
		if err != nil {
			return err
		}
		emit(accounts.NewEvent("Saving accounts.toml", accounts.StepDone, entry.Summary))
		s.syncShims(emit)
		ev := accounts.NewEvent("Saved", accounts.StepDone, entry.Summary)
		ev.Final, ev.EntryID = true, entry.ID
		emit(ev)
		return nil
	})
}

// plural picks one or many; many may hold a %d for n.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	if strings.Contains(many, "%d") {
		return fmt.Sprintf(many, n)
	}
	return many
}
