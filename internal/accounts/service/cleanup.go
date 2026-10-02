package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/claudeshare"
)

// CleanupPlan is everything `devpit accounts cleanup` would remove: only
// what Devpit added. Account folders and sign-ins are never deleted.
type CleanupPlan struct {
	// Accounts is the journalled part: rules, Git files and include,
	// credential helper, Wrangler bindings (adapters.PlanCleanup).
	Accounts adapters.Cleanup
	// Shims and PathEntry are the shim files and the PATH entry Devpit
	// recorded adding.
	Shims     []string
	PathEntry string
	// AgentSkills are the SKILL.md files `devpit agent install` wrote.
	AgentSkills []string
	// Shared are the Claude Code accounts with links to the default
	// account's setup, which can be turned into their own copies (optional,
	// asked separately; see claudeshare.PlanMaterialiseAll).
	Shared []accounts.Account
	// Empty: nothing at all to remove.
	Empty bool
}

// PlanCleanup works out the cleanup. It changes nothing.
func (s *Service) PlanCleanup(ctx context.Context) (CleanupPlan, error) {
	st, _, err := s.Load()
	if err != nil {
		return CleanupPlan{}, err
	}
	var p CleanupPlan
	if p.Accounts, err = adapters.PlanCleanup(ctx, s.Deps, st); err != nil {
		return CleanupPlan{}, err
	}
	if s.Shims != nil {
		if p.Shims, p.PathEntry, err = s.Shims.Added(); err != nil {
			return CleanupPlan{}, err
		}
	}
	if p.AgentSkills, err = s.AgentInstalled(); err != nil {
		return CleanupPlan{}, err
	}
	for _, a := range st.AccountsFor(accounts.ToolClaude) {
		if a.Dir == "" {
			continue
		}
		mp, perr := claudeshare.PlanMaterialiseAll(claudeshare.RootsFor(s.Deps.Home, a))
		if perr == nil && !mp.Empty() {
			p.Shared = append(p.Shared, a)
		}
	}
	p.Empty = p.Accounts.Empty && len(p.Shims) == 0 && p.PathEntry == "" && len(p.AgentSkills) == 0 && len(p.Shared) == 0
	return p, nil
}

// Lines is the plan in plain words.
func (p CleanupPlan) Lines() []string {
	if p.Empty {
		return []string{"Devpit has added nothing for accounts on this PC. Nothing to remove."}
	}
	out := []string{"Devpit will remove only what it added:"}
	for _, s := range p.Accounts.Sentences {
		out = append(out, "  "+s)
	}
	for _, f := range p.Shims {
		out = append(out, "  Remove the shim "+f)
	}
	if p.PathEntry != "" {
		out = append(out, "  Take "+p.PathEntry+" off your user PATH")
	}
	for _, f := range p.AgentSkills {
		out = append(out, "  Remove the agent skill "+f)
	}
	if len(p.Accounts.Edits) > 0 {
		out = append(out, "", "Files it changes:")
		for _, e := range p.Accounts.Edits {
			out = append(out, "  "+e.Path+" ("+e.Action+")")
			for _, l := range e.Lines {
				out = append(out, "    "+l)
			}
		}
	}
	for _, n := range p.Accounts.Notes {
		out = append(out, "", n)
	}
	out = append(out, "", "Account folders and sign-ins are kept. `devpit undo` brings the rules, Git files and bindings back.")
	return trimLines(out)
}

// ApplyCleanup removes what the plan lists: the journalled part first (one
// undoable change), then the shims and PATH entry, then the agent skills.
// With materialise, each account in plan.Shared gets its links turned into
// its own copies (each its own undoable change), so it keeps working
// without the shared folder.
func (s *Service) ApplyCleanup(ctx context.Context, p CleanupPlan, materialise bool, emit func(accounts.Event)) error {
	if emit == nil {
		emit = func(accounts.Event) {}
	}
	var errs []error
	if materialise {
		for _, a := range p.Shared {
			step := "Turning the shared links of " + a.Name + " into its own copies"
			emit(accounts.NewEvent(step, accounts.StepRunning, ""))
			if _, err := claudeshare.MaterialiseAll(ctx, s.Engine, claudeshare.RootsFor(s.Deps.Home, a), emit); err != nil && !errors.Is(err, claudeshare.ErrNothingToDo) {
				errs = append(errs, err)
				continue
			}
			emit(accounts.NewEvent(step, accounts.StepDone, ""))
		}
	}
	if !p.Accounts.Empty {
		emit(accounts.NewEvent("Removing rules, Git files and Wrangler bindings", accounts.StepRunning, ""))
		if _, err := adapters.ApplyCleanup(ctx, s.Engine, s.Deps, p.Accounts, emit); err != nil {
			emit(accounts.Failed("Removing rules, Git files and Wrangler bindings", err))
			return err // nothing else is touched when this part fails
		}
		emit(accounts.NewEvent("Removing rules, Git files and Wrangler bindings", accounts.StepDone, ""))
	}
	if s.Shims != nil && (len(p.Shims) > 0 || p.PathEntry != "") {
		rep, err := s.Shims.Cleanup()
		for _, f := range rep.Removed {
			emit(accounts.NewEvent("Removed the shim", accounts.StepDone, f))
		}
		for _, k := range rep.Kept {
			emit(accounts.NewEvent("Left in place", accounts.StepWarning, k))
		}
		if rep.PathOff {
			emit(accounts.NewEvent("Took Devpit's shim folder off your user PATH", accounts.StepDone, s.Shims.Dir))
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(p.AgentSkills) > 0 {
		done, err := s.AgentRemove(p.AgentSkills)
		for _, f := range done {
			emit(accounts.NewEvent("Removed the agent skill", accounts.StepDone, f))
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("cleanup finished with problems: %w", errors.Join(errs...))
	}
	return nil
}
