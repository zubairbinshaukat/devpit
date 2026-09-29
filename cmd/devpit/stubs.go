package devpit

import (
	"fmt"

	"github.com/spf13/cobra"
)

// notImplemented builds the RunE of a subcommand that is reserved but not
// built yet. It fails rather than printing to stdout and exiting zero, so a
// script that pipes Devpit's output cannot mistake "nothing happened" for
// success.
func notImplemented(name string, milestone int) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, _ []string) error {
		fmt.Fprintf(cmd.ErrOrStderr(),
			"devpit %s: not implemented yet, it arrives in milestone %d.\nRun `devpit` with no arguments for what does work today.\n",
			name, milestone)
		return nil
	}
}

// newCleanCmd is the headless cleaner. Milestone 1 fills it in.
func newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "clean [path]",
		Short: "Scan and clean dev junk without opening the app (not implemented yet)",
		Args:  cobra.MaximumNArgs(1),
		RunE:  notImplemented("clean", 1),
	}
	cmd.Flags().Bool("dry-run", false, "report what would be deleted, delete nothing")
	cmd.Flags().Bool("yes", false, "skip the confirmation prompt")
	cmd.Flags().Bool("json", false, "print machine-readable output")
	cmd.Flags().Bool("resume", false, "finish tombstones left by an interrupted run")
	return cmd
}

// newPortsCmd is the headless port tool. Milestone 3 fills it in.
func newPortsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ports",
		Short: "List or free busy dev ports (not implemented yet)",
		Args:  cobra.NoArgs,
		RunE:  notImplemented("ports", 3),
	}
	kill := &cobra.Command{
		Use:   "kill <port>",
		Short: "Stop whatever is listening on a port (not implemented yet)",
		Args:  cobra.ExactArgs(1),
		RunE:  notImplemented("ports kill", 3),
	}
	cmd.AddCommand(kill)
	return cmd
}

// newUpdateCmd is the headless updater. Milestone 5 fills it in.
func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Update tools through every detected package manager (not implemented yet)",
		Args:  cobra.NoArgs,
		RunE:  notImplemented("update", 5),
	}
}

// newSettingsCmd edits settings from a script. Milestone 6 fills it in.
func newSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "settings [key] [value]",
		Short: "Read or change a setting without opening the app (not implemented yet)",
		Args:  cobra.MaximumNArgs(2),
		RunE:  notImplemented("settings", 6),
	}
}
