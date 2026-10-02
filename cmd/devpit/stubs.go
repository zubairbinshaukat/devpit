package devpit

import (
	"github.com/spf13/cobra"
)

// notAvailableError is what a reserved command returns.
type notAvailableError struct {
	// name is the command path after "devpit", such as "ports kill".
	name string
}

// Error implements error.
func (e notAvailableError) Error() string {
	return "devpit " + e.name + " is not available from the command line yet — open `devpit` and use the menu."
}

// notImplemented builds the RunE of a subcommand that is reserved but not
// built yet. It fails with ExitFailed rather than printing and exiting zero:
// scripts and agents trust exit codes, and "nothing happened" must never
// read as success.
func notImplemented(name string) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		return withCode(ExitFailed, notAvailableError{name: name})
	}
}

// newCleanCmd is the headless way to free up disk space. Reserved.
func newCleanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "clean [path]",
		Short:   "Free up disk space without opening the app (not implemented yet)",
		Example: "devpit clean D:\\work --dry-run\ndevpit clean --resume",
		Args:    cobra.MaximumNArgs(1),
		RunE:    notImplemented("clean"),
	}
	cmd.Flags().Bool("dry-run", false, "report what would be deleted, delete nothing")
	cmd.Flags().Bool("yes", false, "skip the confirmation prompt")
	cmd.Flags().Bool("json", false, "print machine-readable output")
	cmd.Flags().Bool("resume", false, "finish tombstones left by an interrupted run")
	return cmd
}

// newPortsCmd is the headless port tool. Reserved.
func newPortsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "ports",
		Short:   "List or free busy dev ports (not implemented yet)",
		Example: "devpit ports",
		Args:    cobra.NoArgs,
		RunE:    notImplemented("ports"),
	}
	kill := &cobra.Command{
		Use:     "kill <port>",
		Short:   "Stop whatever is listening on a port (not implemented yet)",
		Example: "devpit ports kill 3000",
		Args:    cobra.ExactArgs(1),
		RunE:    notImplemented("ports kill"),
	}
	cmd.AddCommand(kill)
	return cmd
}

// newUpdateCmd is the headless updater. Reserved.
func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "update",
		Short:   "Update tools through every detected package manager (not implemented yet)",
		Example: "devpit update",
		Args:    cobra.NoArgs,
		RunE:    notImplemented("update"),
	}
}

// newSettingsCmd edits settings from a script. Reserved.
func newSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "settings [key] [value]",
		Short:   "Read or change a setting without opening the app (not implemented yet)",
		Example: "devpit settings\ndevpit settings theme",
		Args:    cobra.MaximumNArgs(2),
		RunE:    notImplemented("settings"),
	}
}
