// Package devpit is Devpit's command line. Running the binary with no
// arguments opens the TUI; the subcommands are the headless equivalents, for
// scripts and for CI.
//
// Nothing in this package runs at import time and nothing here execs a process
// or opens a socket before the user asks for it.
package devpit

import (
	"context"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/fang"
	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/app"
	"github.com/zubairbinshaukat/devpit/internal/config"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
	"github.com/zubairbinshaukat/devpit/internal/version"
)

// rootFlags holds the persistent flags of the root command.
type rootFlags struct {
	// ascii forces the lowest icon tier.
	ascii bool
	// elevatedWorker is reserved for the hidden elevated helper process that
	// milestone 5 introduces. It is accepted and ignored today so that a
	// future worker launched by an older binary fails loudly rather than
	// silently opening a second TUI.
	elevatedWorker bool
	// pipe is the named pipe the elevated worker talks over. Also reserved.
	pipe string
}

// NewRootCmd builds the command tree.
func NewRootCmd() *cobra.Command { return newRootCmdWith(defaultAccountsEnv()) }

// newRootCmdWith builds the command tree with the given way to reach
// Accounts, so tests run every accounts command on temporary folders.
func newRootCmdWith(acc accountsEnv) *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:     "devpit",
		Example: "devpit\ndevpit --ascii\ndevpit accounts --json\ndevpit claude use work --yes\ndevpit version --short",
		Short:   "A pit stop for your dev machine",
		Long: "Devpit is a toolkit for developers on Windows: the right account in every folder, " +
			"disk space back, stuck ports freed, tools updated and big folders moved between PCs, " +
			"from one menu.\n\nRun it with no arguments to open the app.\n\n" +
			"For scripts and AI agents: every read command takes --json and never asks anything. " +
			"A command that would change something asks first at a terminal (default No); with nobody " +
			"at a terminal it changes nothing and exits 3 unless --yes is given, and --yes is only for " +
			"after the user agreed. Agents: run `devpit agent install` for a skill with these rules.\n\n" + exitCodeHelp(),
		Version:       version.String(),
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if flags.elevatedWorker {
				return runElevatedWorker(cmd.Context(), flags)
			}
			return runTUI(cmd.Context(), flags)
		},
	}

	root.PersistentFlags().BoolVar(&flags.ascii, "ascii", false, "force the plain ASCII icon tier")
	registerWorkerFlags(root, flags)

	// Add new commands to this list. Help lists them alphabetically, so the
	// order here does not matter.
	root.AddCommand(
		newVersionCmd(),
		newCleanCmd(),
		newPortsCmd(),
		newUpdateCmd(),
		newFontCmd(),
		newSettingsCmd(),
		newShareCmd(),
	)
	root.AddCommand(newAccountsCommands(acc)...)

	// Last, once the tree is complete: every argument or flag mistake in
	// any command, including the ones above, exits with ExitUsage.
	markUsageErrors(root)
	return root
}

// Execute runs the command tree and returns the process exit code: one of
// the ExitCode values, ExitFailed for any error that does not carry its own.
func Execute() int {
	return int(execute(context.Background(), NewRootCmd(), nil))
}

// execute runs root under fang and turns the outcome into an exit code. A
// nil args means the process's own arguments. Tests call it with a root
// whose streams are buffers.
func execute(ctx context.Context, root *cobra.Command, args []string) ExitCode {
	if args != nil {
		root.SetArgs(args)
	}
	err := fang.Execute(
		ctx,
		root,
		fang.WithVersion(version.Short()),
		fang.WithCommit(version.Commit),
		fang.WithErrorHandler(errorHandler(root, os.LookupEnv)),
	)
	return codeOf(err)
}

// runTUI loads the configuration and starts the Bubble Tea program. The config
// read is the only I/O on this path.
func runTUI(ctx context.Context, flags *rootFlags) error {
	res, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "devpit:", err)
	}

	m := app.New(app.Options{
		Config:     res.Config,
		Warning:    res.Warning,
		ForceASCII: flags.ascii,
		Version:    version.Short(),
	})

	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithFPS(30))
	_, err = p.Run()
	// However the program ended (q, Ctrl+C, a termination signal), a running
	// share is taken down here, so leaving Devpit never leaves a share, a
	// temporary login or a firewall change behind. If Devpit is killed
	// outright, its elevated worker does the same when the pipe closes, and
	// the next launch offers to finish any cleanup that is left.
	if serr := host.ShutdownAll(); serr != nil {
		fmt.Fprintln(os.Stderr, "devpit: could not remove everything that sharing set up:", serr)
	}
	return err
}
