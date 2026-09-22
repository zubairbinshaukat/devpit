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
func NewRootCmd() *cobra.Command {
	flags := &rootFlags{}

	root := &cobra.Command{
		Use:   "devpit",
		Short: "A pit stop for your dev machine",
		Long: "Devpit frees disk space, fixes stuck ports, and keeps your developer tools up to " +
			"date, from one terminal menu.\n\nRun it with no arguments to open the app.",
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

	root.AddCommand(
		newVersionCmd(),
		newCleanCmd(),
		newPortsCmd(),
		newUpdateCmd(),
		newFontCmd(),
		newSettingsCmd(),
	)
	return root
}

// Execute runs the command tree and returns the process exit code.
func Execute() int {
	if err := fang.Execute(
		context.Background(),
		NewRootCmd(),
		fang.WithVersion(version.Short()),
		fang.WithCommit(version.Commit),
	); err != nil {
		return 1
	}
	return 0
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
	})

	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithFPS(30))
	_, err = p.Run()
	return err
}
