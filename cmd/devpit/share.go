package devpit

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/elevate"
	"github.com/zubairbinshaukat/devpit/internal/share/host"
)

// newShareCmd is the headless side of Share Files. Sharing and copying are
// screens, because they are interactive from start to end; what is worth a
// command is the one thing that can be needed with no screen: finishing the
// cleanup of a share that an earlier run left behind, from a script or after
// a crash.
func newShareCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "share",
		Short: "Share files between PCs on your network (open the app to do it)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return cmd.Help() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "cleanup",
		Short: "Remove a share that an earlier Devpit left behind",
		Long: "If Devpit or the PC stopped while a folder was shared, the share, its temporary login " +
			"and the firewall or network changes may still be there. This removes them. " +
			"Windows asks for permission once.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runShareCleanup(cmd.Context(), cmd.OutOrStdout(), host.StateDir(), host.RealLauncher)
		},
	})
	return cmd
}

// runShareCleanup finds a leftover share and removes it. It says in easy
// words what it did or why it could not. The directory and the launcher are
// parameters so a test can run it on a temporary directory and a fake worker.
func runShareCleanup(ctx context.Context, out io.Writer, dir string, launch host.Launcher) error {
	m, found, err := host.Leftover(dir, host.ProcessAlive, os.Getpid())
	if err != nil {
		return fmt.Errorf("reading the record of the old share: %w", err)
	}
	if !found {
		_, _ = fmt.Fprintln(out, "Nothing to clean up.")
		return nil
	}
	_, _ = fmt.Fprintf(out, "Removing the old share of %s. Windows will ask for permission.\n", m.Path)
	if err := host.CleanUp(ctx, dir, m, launch); err != nil {
		var declined *elevate.DeclinedError
		if errors.As(err, &declined) {
			_, _ = fmt.Fprintln(out, "You said no to the admin prompt. Nothing was changed.")
		}
		return err
	}
	_, _ = fmt.Fprintln(out, "Done. The old share is gone.")
	return nil
}
