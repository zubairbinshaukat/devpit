package devpit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/gitcred"
	"github.com/zubairbinshaukat/devpit/internal/accounts/importer"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

func importerOptions() importer.Options { return importer.Options{ClaudeAcc: true} }

func importGuide(f importer.Found) []string {
	var out []string
	for i, st := range importer.RemovalGuide(f) {
		out = append(out, fmt.Sprintf("%d. %s", i+1, st.Title))
		for _, d := range st.Detail {
			out = append(out, "   "+d)
		}
	}
	return out
}

func newUseCmd(env accountsEnv) *cobra.Command {
	var f scopeFlags
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Switch every tool that has an account with this name",
		Long: "For every tool with an account called <name> (for example \"work\" in Claude Code, GitHub and Vercel), " +
			"uses that account in this folder (or --folder, or --everywhere). It shows every change first and asks once " +
			"[y/N]; `devpit undo` takes all of them back together. With nobody at a terminal it changes nothing and " +
			"exits 3 unless --yes is given.",
		Example: "devpit use work\ndevpit use work --folder C:\\Work --yes\ndevpit use default --everywhere",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return accountsError(runUseAll(cmd, env, args[0], &f))
		},
	}
	f.register(cmd)
	return cmd
}

func runUseAll(cmd *cobra.Command, env accountsEnv, name string, f *scopeFlags) error {
	out := cmd.OutOrStdout()
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	st, _, err := s.Load()
	if err != nil {
		return err
	}
	tools := service.ToolsWithAccount(st, name)
	if len(tools) == 0 {
		return withCode(ExitUsage, fmt.Errorf("no tool has an account called %q. See them with: devpit accounts", name))
	}
	p := newPrompter(cmd)
	scope, ok, err := f.scope(cmd, env, s, p)
	if err != nil || !ok {
		return err
	}
	previews, skipped, err := s.PlanAll(cmd.Context(), name, scope, tools)
	if err != nil {
		return err
	}
	changes := 0
	for _, pv := range previews {
		_, _ = fmt.Fprintln(out, "— "+pv.Change.Tool.DisplayName())
		say(out, pv.Lines())
		_, _ = fmt.Fprintln(out)
		if !pv.NoChange {
			changes++
		}
	}
	for _, sk := range skipped {
		_, _ = fmt.Fprintln(out, glyphNote+" Left out: "+sk)
	}
	if changes == 0 {
		_, _ = fmt.Fprintln(out, "Nothing to change.")
		return nil
	}
	ok, err = agree(cmd, p, f.yes, "which accounts your tools use", fmt.Sprintf("Make these %d change(s)?", changes))
	if err != nil || !ok {
		return err
	}
	for _, pv := range previews {
		if pv.DriveRoot && !f.yes && !p.confirm(pv.SecondConfirm) {
			_, _ = fmt.Fprintln(out, "Nothing changed.")
			return nil
		}
	}
	if err := s.ApplyAll(cmd.Context(), previews, func(ev accounts.Event) { printEvent(out, ev) }); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, "Done. Undo all of it with: devpit undo")
	return nil
}

func newUndoCmd(env accountsEnv) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "undo",
		Short: "Undo the last account change",
		Long: "Shows what the last account change was and what undoing it puts back, asks [y/N] (default No), then " +
			"puts every file back exactly as it was. If Devpit's files were changed by hand since, it stops and says " +
			"which, and changes nothing. Account folders that hold a sign-in are never deleted.",
		Example: "devpit undo\ndevpit undo --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsError(runUndo(cmd, env, yes))
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "undo without asking; only after the user agreed")
	return cmd
}

func runUndo(cmd *cobra.Command, env accountsEnv, yes bool) error {
	out := cmd.OutOrStdout()
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	info, err := s.UndoPreview()
	if errors.Is(err, accounts.ErrNothingToUndo) {
		return errors.New("there is no account change to undo")
	}
	if err != nil {
		return err
	}
	say(out, info.Lines)
	_, _ = fmt.Fprintln(out)
	ok, err := agree(cmd, newPrompter(cmd), yes, "your accounts back to how they were before the last change", "Undo it?")
	if err != nil || !ok {
		return err
	}
	res, err := s.Undo(cmd.Context(), func(ev accounts.Event) { printEvent(out, ev) })
	if err != nil {
		return err
	}
	for _, n := range res.Notes {
		_, _ = fmt.Fprintln(out, glyphNote+" "+n)
	}
	_, _ = fmt.Fprintf(out, "%s Undone: %s\n", glyphOK, res.Entry.Summary)
	return nil
}

func newAccountsCmd(env accountsEnv) *cobra.Command {
	var js bool
	var folder string
	cmd := &cobra.Command{
		Use:   "accounts",
		Short: "Show every tool's account in this folder",
		Long: "The Accounts table for this folder (or --folder): every tool, the account it uses, why, and anything " +
			"wrong with its fix. It never runs a tool's sign-in check.\n\n--json shape: {folder, tools: [{tool, " +
			"tool_name, folder, account: {name, email, login, display}, why, reason, rule_folder, everywhere, " +
			"chain: [{folder, account, won, skipped}], installed, managed, supports: {folder_rules, everywhere, " +
			"just_once, add_account, show_only, beta, why}, problems: [{kind, tool, message, fix}]}], warning}.",
		Example: "devpit accounts\ndevpit accounts --json\ndevpit accounts verify",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := func() error {
				dir, err := env.folder(folder)
				if err != nil {
					return err
				}
				s, err := env.openService(cmd, js)
				if err != nil {
					return err
				}
				ov, err := s.Overview(cmd.Context(), dir)
				if err != nil {
					return err
				}
				if js {
					return printJSON(cmd.OutOrStdout(), ov.JSON())
				}
				say(cmd.OutOrStdout(), ov.Lines())
				return nil
			}()
			return asJSON(js, accountsError(err))
		},
	}
	cmd.Flags().BoolVar(&js, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&folder, "folder", "", "the folder to look at instead of the current one")
	cmd.AddCommand(newVerifyCmd(env), newCleanupCmd(env))
	return cmd
}

func newVerifyCmd(env accountsEnv) *cobra.Command {
	var js, all, yes bool
	var folder string
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check, for every tool, that the expected account is the one in use",
		Long: "For every installed tool, compares the account Devpit expects in this folder with what the tool says " +
			"(Git and GitHub pushes offline, the others by asking the tool who is signed in), and lists every problem " +
			"with its fix. Exit code 4 when anything does not match.\n\nBy default only the account used in this " +
			"folder is asked. --all asks every account; a check that can sign an idle account out (Claude Code) is " +
			"skipped, with the reason, unless you agree at the prompt or pass --yes after the user agreed.\n\n" +
			"--json shape: {folder, mismatch, checks: [{tool, account, here, expected, actual: {state, email, login, " +
			"name, org, plan, method, note}, says, how, status, notes}], problems: [{kind, tool, message, fix}], skipped}. " +
			"status is ok, mismatch, info, not installed or error.",
		Example: "devpit accounts verify\ndevpit accounts verify --json\ndevpit accounts verify --all",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := func() error {
				dir, err := env.folder(folder)
				if err != nil {
					return err
				}
				s, err := env.openService(cmd, js)
				if err != nil {
					return err
				}
				risky := all && yes
				if all && !yes && !js && isInteractive(cmd) && hasIdleRisky(s, dir) {
					risky = newPrompter(cmd).confirm("Checking an idle Claude Code account can sign it out on some versions (Claude Code issue #95822). Check the idle ones too?")
				}
				rep, err := s.Verify(cmd.Context(), service.VerifyOptions{Folder: dir, All: all, Risky: risky})
				if err != nil {
					return err
				}
				if js {
					if err := printJSON(cmd.OutOrStdout(), rep.JSON()); err != nil {
						return err
					}
				} else {
					say(cmd.OutOrStdout(), rep.Lines())
				}
				if rep.Mismatch {
					return withCode(ExitMismatch, nil)
				}
				return nil
			}()
			return asJSON(js, accountsError(err))
		},
	}
	cmd.Flags().BoolVar(&js, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&folder, "folder", "", "the folder to check instead of the current one")
	cmd.Flags().BoolVar(&all, "all", false, "also check every account not used in this folder")
	cmd.Flags().BoolVar(&yes, "yes", false, "with --all, also run checks that can sign an idle account out; only after the user agreed")
	return cmd
}

// hasIdleRisky reports whether --all would skip a risky check.
func hasIdleRisky(s *service.Service, folder string) bool {
	st, _, err := s.Load()
	if err != nil {
		return false
	}
	for _, t := range accounts.Tools() {
		a, err := s.Adapter(t)
		if err != nil {
			continue
		}
		if risky, _ := a.LiveCheckRisk(); risky && len(st.AccountsFor(t)) > 0 {
			return true
		}
	}
	_ = folder
	return false
}

func newCleanupCmd(env accountsEnv) *cobra.Command {
	var yes, copyLinks bool
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove everything Devpit added for accounts, and nothing else",
		Long: "Lists exactly what Devpit added (the folder rules, the shims and their PATH entry, its Git files, its " +
			"[include] block and the github.com sign-in helper, the Wrangler bindings it made, the agent skill) and, " +
			"after a yes, removes only that. Account folders and sign-ins are never deleted. --copy-links also turns a " +
			"Claude Code account's links to your default setup into its own copies, so it keeps working without them.",
		Example: "devpit accounts cleanup\ndevpit accounts cleanup --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsError(runCleanup(cmd, env, yes, copyLinks))
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "remove without asking; only after the user agreed")
	cmd.Flags().BoolVar(&copyLinks, "copy-links", false, "also turn shared Claude Code links into each account's own copies")
	return cmd
}

func runCleanup(cmd *cobra.Command, env accountsEnv, yes, copyLinks bool) error {
	out := cmd.OutOrStdout()
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	plan, err := s.PlanCleanup(cmd.Context())
	if err != nil {
		return err
	}
	say(out, plan.Lines())
	if plan.Empty {
		return nil
	}
	if len(plan.Shared) > 0 {
		names := make([]string, 0, len(plan.Shared))
		for _, a := range plan.Shared {
			names = append(names, a.Name)
		}
		_, _ = fmt.Fprintf(out, "\nThe Claude Code account(s) %s link to your default setup. They keep working after cleanup; --copy-links turns the links into their own copies.\n", strings.Join(names, ", "))
	}
	_, _ = fmt.Fprintln(out)
	p := newPrompter(cmd)
	ok, err := agree(cmd, p, yes, "what Devpit set up for accounts", "Remove it?")
	if err != nil || !ok {
		return err
	}
	if len(plan.Shared) > 0 && !copyLinks && !yes {
		copyLinks = p.confirm("Also turn the shared links into each account's own copies?")
	}
	if err := s.ApplyCleanup(cmd.Context(), plan, copyLinks, func(ev accounts.Event) { printEvent(out, ev) }); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(out, glyphOK+" Cleaned up. `devpit undo` brings the rules back.")
	return nil
}

func newAgentCmd(env accountsEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Teach AI agents to use Devpit safely",
		Long: "`devpit agent install` writes a Devpit skill (SKILL.md) into Claude Code's skills folder and into each " +
			"Claude Code account Devpit manages, and prints an AGENTS.md section for other agents. `devpit agent remove` " +
			"takes out only the files it wrote.",
		Example: "devpit agent install\ndevpit agent remove",
		Args:    cobra.NoArgs,
		RunE:    func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	var yesI, yesR bool
	install := &cobra.Command{
		Use:     "install",
		Short:   "Write the Devpit skill for Claude Code and print an AGENTS.md section",
		Long:    "Writes skills\\devpit\\SKILL.md into ~/.claude and into each Devpit-managed Claude Code account that does not already get it through a shared link. A devpit skill Devpit did not write is never overwritten.",
		Example: "devpit agent install\ndevpit agent install --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsError(runAgentInstall(cmd, env, yesI))
		},
	}
	install.Flags().BoolVar(&yesI, "yes", false, "write without asking; only after the user agreed")
	remove := &cobra.Command{
		Use:     "remove",
		Short:   "Remove the Devpit skill files Devpit wrote",
		Example: "devpit agent remove\ndevpit agent remove --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsError(runAgentRemove(cmd, env, yesR))
		},
	}
	remove.Flags().BoolVar(&yesR, "yes", false, "remove without asking; only after the user agreed")
	cmd.AddCommand(install, remove)
	return cmd
}

func runAgentInstall(cmd *cobra.Command, env accountsEnv, yes bool) error {
	out := cmd.OutOrStdout()
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	ts, err := s.AgentTargets()
	if err != nil {
		return err
	}
	say(out, service.AgentLines(ts))
	todo := 0
	for _, t := range ts {
		if t.State == service.AgentCreate || t.State == service.AgentUpdate {
			todo++
		}
	}
	if todo > 0 {
		_, _ = fmt.Fprintln(out)
		ok, err := agree(cmd, newPrompter(cmd), yes, "files in your Claude Code folders", "Write the skill?")
		if err != nil || !ok {
			return err
		}
		done, err := s.AgentInstall(ts)
		for _, f := range done {
			_, _ = fmt.Fprintln(out, glyphOK+" Wrote "+f)
		}
		if err != nil {
			return err
		}
	}
	_, _ = fmt.Fprintln(out, "\nFor other agents, add this to AGENTS.md in your repository (Devpit does not write into your repos):")
	_, _ = fmt.Fprintln(out)
	_, _ = fmt.Fprint(out, service.AgentsSnippet)
	return nil
}

func runAgentRemove(cmd *cobra.Command, env accountsEnv, yes bool) error {
	out := cmd.OutOrStdout()
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	files, err := s.AgentInstalled()
	if err != nil {
		return err
	}
	if len(files) == 0 {
		_, _ = fmt.Fprintln(out, "Devpit's agent skill is not installed anywhere.")
		return nil
	}
	for _, f := range files {
		_, _ = fmt.Fprintln(out, "Remove "+f)
	}
	_, _ = fmt.Fprintln(out)
	ok, err := agree(cmd, newPrompter(cmd), yes, "files in your Claude Code folders", "Remove them?")
	if err != nil || !ok {
		return err
	}
	done, err := s.AgentRemove(files)
	for _, f := range done {
		_, _ = fmt.Fprintln(out, glyphOK+" Removed "+f)
	}
	return err
}

// newGitCredentialCmd is Git's credential helper for github.com while Devpit
// manages pushes. Git runs it; people never do, so it is hidden.
func newGitCredentialCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "git-credential <get|store|erase>",
		Short:              "Git's sign-in helper for github.com (run by Git)",
		Hidden:             true,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return gitcred.Run(cmd.Context(), args, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
}
