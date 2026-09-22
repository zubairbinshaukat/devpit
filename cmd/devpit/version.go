package devpit

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/zubairbinshaukat/devpit/internal/version"
)

// newVersionCmd prints the build identity. It is the one subcommand that is
// fully implemented in milestone 0.
func newVersionCmd() *cobra.Command {
	var short bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the Devpit version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if short {
				fmt.Fprintln(cmd.OutOrStdout(), version.Short())
				return nil
			}
			fmt.Fprintf(cmd.OutOrStdout(), "devpit %s\n", version.String())
			return nil
		},
	}
	cmd.Flags().BoolVar(&short, "short", false, "print only the version number")
	return cmd
}
