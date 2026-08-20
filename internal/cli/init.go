package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// NewInitCommand builds `wt init`.
func NewInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Print shell integration (wrapper function and tab completion)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runInit(cmd)
			return nil
		},
	}
}

func runInit(cmd *cobra.Command) {
	shellName := filepath.Base(os.Getenv("SHELL"))

	switch shellName {
	case "zsh":
		printInit(cmd, "~/.zshrc", zshCompletion)
	case "bash":
		printInit(cmd, "~/.bashrc", bashCompletion)
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "wt: unrecognized shell %q, emitting bash integration\n", shellName)
		printInit(cmd, "your shell rc file", bashCompletion)
	}
}

// printInit writes the wrapper function and completion script to stdout, preceded by
// install instructions as "#" comment lines so `eval "$(wt init)"` remains valid shell.
func printInit(cmd *cobra.Command, rcFile, completion string) {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "# add the following to %s:\n", rcFile)
	fmt.Fprintln(out, "#   eval \"$(wt init)\"")
	fmt.Fprintln(out)
	fmt.Fprint(out, wrapperFunction)
	fmt.Fprintln(out)
	fmt.Fprint(out, completion)
}
