package devpit

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/accounts"
	"github.com/zubairbinshaukat/devpit/internal/accounts/adapters"
	"github.com/zubairbinshaukat/devpit/internal/accounts/service"
)

// newAccountsCommands is the Accounts part of the command line: one command
// per tool, all built by newToolCmd so the eight are the same, plus
// `devpit use`, `devpit undo`, `devpit accounts`, `devpit agent` and the
// hidden `devpit git-credential`.
func newAccountsCommands(env accountsEnv) []*cobra.Command {
	var out []*cobra.Command
	for _, t := range accounts.Tools() {
		out = append(out, newToolCmd(env, t))
	}
	return append(out,
		newUseCmd(env),
		newUndoCmd(env),
		newAccountsCmd(env),
		newAgentCmd(env),
		newGitCredentialCmd(),
	)
}

// supportText says, for --help, what Devpit can do with a tool. It is the
// static table: help never runs anything.
func supportText(t accounts.Tool) string {
	c := adapters.Supports(t)
	if c.ShowOnly {
		return t.DisplayName() + " is show-only: " + c.Why + " `use`, `run` and `add` stop with that reason (exit 1)."
	}
	var can []string
	if c.FolderRules {
		can = append(can, "folder rules")
	}
	if c.Everywhere {
		can = append(can, "an account for everywhere")
	}
	if c.JustOnce {
		can = append(can, "just this once (`run`)")
	}
	s := "Devpit can set " + strings.Join(can, ", ") + " for " + t.DisplayName() + "."
	if c.Beta {
		s += " Beta: it relies on a feature the tool itself calls experimental."
	}
	if c.Why != "" {
		s += " " + c.Why
	}
	return s
}

func newToolCmd(env accountsEnv, t accounts.Tool) *cobra.Command {
	var asJSONFlag bool
	var folder string
	name := string(t)
	cmd := &cobra.Command{
		Use:   name,
		Short: "Show the " + t.DisplayName() + " account used here, and why",
		Long: "Shows which " + t.DisplayName() + " account is used in this folder (or --folder), who it is, why " +
			"(a folder rule or \"everywhere\"), the account used everywhere else, and anything wrong with a fix. " +
			"It never runs the tool's sign-in check.\n\n" + supportText(t),
		Example: fmt.Sprintf("devpit %s\ndevpit %s --json\ndevpit %s use work\ndevpit %s list", name, name, name, name),
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			err := runToolStatus(cmd, env, t, folder, asJSONFlag)
			return asJSON(asJSONFlag, accountsError(err))
		},
	}
	switch t {
	case accounts.ToolGitHub:
		cmd.Aliases = []string{"gh"}
	case accounts.ToolCloudflare:
		cmd.Aliases = []string{"wrangler"}
	}
	cmd.Flags().BoolVar(&asJSONFlag, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&folder, "folder", "", "the folder to look at instead of the current one")
	cmd.AddCommand(newToolUseCmd(env, t), newToolRunCmd(env, t), newToolListCmd(env, t), newToolAddCmd(env, t))
	if t == accounts.ToolClaude {
		cmd.AddCommand(newClaudeImportCmd(env))
	}
	return cmd
}

func runToolStatus(cmd *cobra.Command, env accountsEnv, t accounts.Tool, folderFlag string, js bool) error {
	folder, err := env.folder(folderFlag)
	if err != nil {
		return err
	}
	s, err := env.openService(cmd, js)
	if err != nil {
		return err
	}
	st, err := s.Status(cmd.Context(), t, folder)
	if err != nil {
		return err
	}
	if js {
		return printJSON(cmd.OutOrStdout(), st.JSON())
	}
	say(cmd.OutOrStdout(), st.Lines())
	return nil
}

// scopeFlags are the "where" flags of use.
type scopeFlags struct {
	everywhere bool
	folder     string
	yes        bool
}

func (f *scopeFlags) register(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&f.everywhere, "everywhere", false, "use it wherever no folder rule says otherwise")
	cmd.Flags().StringVar(&f.folder, "folder", "", "the folder (and every folder inside it) to use it in, instead of the current one")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "make the change without asking; only after the user agreed")
	cmd.MarkFlagsMutuallyExclusive("everywhere", "folder")
}

// scope works out where the change applies. From the home folder or a drive
// root without --folder or --everywhere it asks for a folder at a terminal
// and fails with the reason otherwise.
func (f *scopeFlags) scope(cmd *cobra.Command, env accountsEnv, s *service.Service, p *prompter) (accounts.Scope, bool, error) {
	if f.everywhere {
		return accounts.EverywhereScope(), true, nil
	}
	if f.folder != "" {
		dir, err := env.folder(f.folder)
		return accounts.FolderScope(dir), true, err
	}
	dir, err := env.getwd()
	if err != nil {
		return accounts.Scope{}, false, err
	}
	if reason := s.FolderCheck(dir); reason != "" {
		if !isInteractive(cmd) {
			return accounts.Scope{}, false, withCode(ExitUsage, errors.New(reason+" Pass --folder <path> for the folder you mean, or --everywhere."))
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), reason)
		ans, ok := p.line("Which folder should it apply to? (full path, Enter to stop) ")
		if !ok || ans == "" {
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Nothing changed.")
			return accounts.Scope{}, false, nil
		}
		dir, err = env.folder(ans)
		if err != nil {
			return accounts.Scope{}, false, err
		}
	}
	return accounts.FolderScope(dir), true, nil
}

func newToolUseCmd(env accountsEnv, t accounts.Tool) *cobra.Command {
	var f scopeFlags
	name := string(t)
	cmd := &cobra.Command{
		Use:   "use <name>",
		Short: "Use an account in this folder and every folder inside it (or everywhere)",
		Long: "Shows in plain words what would change and the exact files Devpit writes, asks [y/N] at a terminal " +
			"(default No), then makes the change step by step. It can be undone with `devpit undo`. " +
			"A unique start of a name works (\"wo\" for \"work\"); \"default\" is the tool's own sign-in.\n\n" +
			"With nobody at a terminal it changes nothing and exits 3 unless --yes is given. " + supportText(t),
		Example: fmt.Sprintf("devpit %s use work\ndevpit %s use work --folder C:\\Work --yes\ndevpit %s use default --everywhere", name, name, name),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return accountsError(runToolUse(cmd, env, t, args[0], &f))
		},
	}
	f.register(cmd)
	return cmd
}

func runToolUse(cmd *cobra.Command, env accountsEnv, t accounts.Tool, name string, f *scopeFlags) error {
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	p := newPrompter(cmd)
	scope, ok, err := f.scope(cmd, env, s, p)
	if err != nil || !ok {
		return err
	}
	pv, err := s.Plan(cmd.Context(), accounts.Change{Tool: t, Account: name, Scope: scope})
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	say(out, pv.Lines())
	if pv.NoChange {
		return nil
	}
	_, _ = fmt.Fprintln(out)
	ok, err = agree(cmd, p, f.yes, "which account "+t.DisplayName()+" uses", "Make this change?")
	if err != nil || !ok {
		return err
	}
	if pv.DriveRoot && !f.yes && !p.confirm(pv.SecondConfirm) {
		_, _ = fmt.Fprintln(out, "Nothing changed.")
		return nil
	}
	if last := drain(out, s.Apply(cmd.Context(), pv)); last.State != accounts.StepDone {
		if last.Err != nil {
			return last.Err
		}
		return errors.New("the change did not finish")
	}
	_, _ = fmt.Fprintln(out, "Done. Undo it with: devpit undo")
	return nil
}

func newToolRunCmd(env accountsEnv, t accounts.Tool) *cobra.Command {
	name := string(t)
	return &cobra.Command{
		Use:   "run <name> -- <command> [args...]",
		Short: "Run one command with an account, just this once (nothing is saved)",
		Long: "Starts the command with the account applied to it and to everything it starts. Nothing is " +
			"saved, so nothing needs undoing and no --yes is asked for. The exit code is the command's own. " +
			"Vercel, Firebase and Cloudflare take the account as a command-line option, so for them the " +
			"command must start with the tool (or npx and the tool).\n\n" + supportText(t),
		Example: fmt.Sprintf("devpit %s run work -- %s", name, onceExample(t)),
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.ArgsLenAtDash() != 1 || len(args) < 2 {
				return errors.New("give the account name, then -- and the command, for example: devpit " + name + " run work -- " + onceExample(t))
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := env.openService(cmd, false)
			if err != nil {
				return accountsError(err)
			}
			oc, err := s.PrepareOnce(cmd.Context(), t, args[0], args[1:])
			if err != nil {
				return accountsError(err)
			}
			code, err := service.RunOnce(oc)
			if err != nil {
				return withCode(ExitFailed, err)
			}
			if code != 0 {
				return withCode(ExitCode(code), nil)
			}
			return nil
		},
	}
}

func onceExample(t accounts.Tool) string {
	switch t {
	case accounts.ToolGitHub:
		return "gh pr list"
	case accounts.ToolGit:
		return "git commit"
	case accounts.ToolClaude:
		return "claude -p \"summarise this repo\""
	case accounts.ToolCloudflare:
		return "wrangler deploy"
	}
	return t.Binary() + " deploy"
}

func newToolListCmd(env accountsEnv, t accounts.Tool) *cobra.Command {
	var js bool
	var folder string
	name := string(t)
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List the " + t.DisplayName() + " accounts",
		Long:    "Lists \"default\" and every named account, who it is, where it is used, and accounts the tool itself has that Devpit does not list yet. The one used in this folder is marked.",
		Example: fmt.Sprintf("devpit %s list\ndevpit %s list --json", name, name),
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
				l, err := s.List(cmd.Context(), t, dir)
				if err != nil {
					return err
				}
				if js {
					return printJSON(cmd.OutOrStdout(), l)
				}
				say(cmd.OutOrStdout(), service.ListLines(l))
				return nil
			}()
			return asJSON(js, accountsError(err))
		},
	}
	cmd.Flags().BoolVar(&js, "json", false, "print machine-readable output")
	cmd.Flags().StringVar(&folder, "folder", "", "mark the account used in this folder instead of the current one")
	return cmd
}

type addFlags struct {
	name, email, displayName string
	console, yes             bool
}

func newToolAddCmd(env accountsEnv, t accounts.Tool) *cobra.Command {
	var f addFlags
	name := string(t)
	long := "Runs " + t.DisplayName() + "'s own sign-in for a new account, then asks for a name (suggested from the email). " +
		"A sign-in that does not finish leaves nothing behind. With nobody at a terminal, --name and --yes are needed."
	example := fmt.Sprintf("devpit %s add\ndevpit %s add --name work --yes", name, name)
	switch t {
	case accounts.ToolGit:
		long = "Adds a Git identity (Git has no sign-in): the email, the name commits are made under, and a name for it in Devpit."
		example = "devpit git add\ndevpit git add --name work --email zubair@work.com --display-name \"Zubair\" --yes"
	case accounts.ToolCloudflare:
		long += " Wrangler names the profile when it is made, so the name is asked first."
	}
	cmd := &cobra.Command{
		Use:     "add",
		Short:   "Sign in to another " + t.DisplayName() + " account and name it",
		Long:    long + "\n\n" + supportText(t),
		Example: example,
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return accountsError(runToolAdd(cmd, env, t, &f))
		},
	}
	cmd.Flags().StringVar(&f.name, "name", "", "the name for the new account in Devpit")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "add it without asking; only after the user agreed")
	if t == accounts.ToolGit {
		cmd.Flags().StringVar(&f.email, "email", "", "the email Git commits under")
		cmd.Flags().StringVar(&f.displayName, "display-name", "", "the name Git commits under (default: the one in your global Git config)")
	} else {
		cmd.Flags().StringVar(&f.email, "email", "", "pre-fill the sign-in page with this email, where the tool supports it")
	}
	if t == accounts.ToolClaude {
		cmd.Flags().BoolVar(&f.console, "console", false, "sign in with an Anthropic Console account (API billing)")
	}
	return cmd
}

func runToolAdd(cmd *cobra.Command, env accountsEnv, t accounts.Tool, f *addFlags) error {
	out := cmd.OutOrStdout()
	interactive := isInteractive(cmd)
	if !interactive && f.name == "" {
		return withCode(ExitUsage, errors.New("--name is needed when nobody is at a terminal"))
	}
	if t == accounts.ToolGit && !interactive && f.email == "" {
		return withCode(ExitUsage, errors.New("--email is needed when nobody is at a terminal"))
	}
	if err := requireYes(cmd, f.yes, "your "+t.DisplayName()+" accounts"); err != nil {
		return err
	}
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	p := newPrompter(cmd)
	st, _, err := s.Load()
	if err != nil {
		return err
	}
	askName := func(suggest string) (string, error) {
		for {
			q := "Name for the new account: "
			if suggest != "" {
				q = "Name for the new account [" + suggest + "]: "
			}
			ans, ok := p.line(q)
			if ans == "" {
				ans = suggest
			}
			if !ok && ans == "" {
				return "", errors.New("no name was given, so nothing was saved")
			}
			if nerr := st.CheckNewName(t, ans); nerr != nil {
				_, _ = fmt.Fprintln(out, glyphWarn+" "+nerr.Error())
				if !ok {
					return "", nerr
				}
				continue
			}
			return ans, nil
		}
	}
	req := adapters.LoginRequest{Name: f.name, Email: f.email, DisplayName: f.displayName, Console: f.console, Store: st}
	if t == accounts.ToolGit && req.Email == "" {
		req.Email, _ = p.line("Email Git should commit under: ")
		if req.DisplayName == "" {
			req.DisplayName, _ = p.line("Name Git should commit under (Enter keeps the one in your global Git config): ")
		}
	}
	if req.Name == "" {
		if service.NameBeforeSignIn(t) {
			if req.Name, err = askName(s.SuggestName(t, req.Email)); err != nil {
				return err
			}
		} else {
			req.Name = service.PlaceholderName()
		}
	} else if nerr := st.CheckNewName(t, req.Name); nerr != nil {
		return nerr
	}
	if interactive {
		req.Stdin, req.Stdout, req.Stderr = cmd.InOrStdin(), out, cmd.ErrOrStderr()
	}
	ch, err := s.SignIn(cmd.Context(), t, req)
	if err != nil {
		return err
	}
	last := drain(out, ch)
	if last.State != accounts.StepDone || last.Account == nil {
		if last.Err != nil {
			return last.Err
		}
		return accounts.ErrSignInCancelled
	}
	acct := *last.Account
	final := req.Name
	if final == acct.Name && strings.HasPrefix(final, "new-") && f.name == "" {
		if final, err = askName(s.SuggestName(t, acct.Email)); err != nil {
			return err
		}
	}
	saved, _, err := s.SaveAccount(acct, final)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Saved the %s account %s. Use it here with: devpit %s use %s\n", t.DisplayName(), saved.Display(), t, saved.Name)
	if t == accounts.ToolClaude {
		_, _ = fmt.Fprintln(out, "Your Claude Code setup (skills, agents, commands, CLAUDE.md, settings…) can be brought over to it from the app: open devpit › Accounts › Claude Code › "+saved.Name+".")
	}
	return nil
}

func newClaudeImportCmd(env accountsEnv) *cobra.Command {
	var from string
	var yes bool
	cmd := &cobra.Command{
		Use:   "import --from claude-acc",
		Short: "Bring claude-acc's accounts and folder links into Devpit",
		Long: "Reads claude-acc's own files (its accounts, folder links and default account), shows what Devpit would add, " +
			"and after a yes adds them as one change `devpit undo` takes back. Accounts stay where they are, so nobody " +
			"signs in again. Then it prints the steps to retire claude-acc safely.",
		Example: "devpit claude import --from claude-acc\ndevpit claude import --from claude-acc --yes",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if from != "claude-acc" {
				return withCode(ExitUsage, fmt.Errorf("devpit can import from claude-acc only (--from claude-acc), not %q", from))
			}
			return accountsError(runClaudeImport(cmd, env, yes))
		},
	}
	cmd.Flags().StringVar(&from, "from", "", "where to import from: claude-acc")
	cmd.Flags().BoolVar(&yes, "yes", false, "import without asking; only after the user agreed")
	_ = cmd.MarkFlagRequired("from")
	return cmd
}

func runClaudeImport(cmd *cobra.Command, env accountsEnv, yes bool) error {
	out := cmd.OutOrStdout()
	s, err := env.openService(cmd, false)
	if err != nil {
		return err
	}
	found, ok, err := s.DetectClaudeAcc(cmd.Context())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("claude-acc was not found on this PC (there is no .claude-switch folder in your home folder)")
	}
	plan, err := s.PlanImport(found, importerOptions())
	if err != nil {
		return err
	}
	say(out, plan.Lines())
	if !plan.NoChange {
		_, _ = fmt.Fprintln(out)
		ok, err := agree(cmd, newPrompter(cmd), yes, "your accounts and folder rules", "Import this?")
		if err != nil || !ok {
			return err
		}
		if _, err := s.ApplyImport(plan, func(ev accounts.Event) { printEvent(out, ev) }); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(out, glyphOK+" Imported. Undo it with: devpit undo")
	}
	guide := importGuide(found)
	if len(guide) > 0 {
		_, _ = fmt.Fprintln(out, "\nTo retire claude-acc safely:")
		say(out, guide)
	}
	return nil
}
