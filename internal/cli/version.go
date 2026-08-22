package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"wt/internal/version"
)

// NewVersionCommand builds `wt version`.
func NewVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the build version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Cobra's own Print helpers fall back to stderr; the version string is data and
			// belongs on stdout.
			_, err := fmt.Fprintln(cmd.OutOrStdout(), version.String())
			return err
		},
	}
}
