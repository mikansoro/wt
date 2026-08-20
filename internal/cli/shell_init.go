package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// NewShellInitCommand builds `wt shell-init`.
func NewShellInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "shell-init",
		Short: "Print shell integration (wrapper function and tab completion)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			runShellInit(cmd)
			return nil
		},
	}
}

func runShellInit(cmd *cobra.Command) {
	shellName := filepath.Base(os.Getenv("SHELL"))

	switch shellName {
	case "zsh":
		printShellInit(cmd, "~/.zshrc", zshCompletion)
	case "bash":
		printShellInit(cmd, "~/.bashrc", bashCompletion)
	default:
		fmt.Fprintf(cmd.ErrOrStderr(), "wt: unrecognized shell %q, emitting bash integration\n", shellName)
		printShellInit(cmd, "your shell rc file", bashCompletion)
	}
}

// printShellInit writes the wrapper function and completion script to stdout, preceded by
// install instructions as "#" comment lines so `eval "$(wt shell-init)"` remains valid shell.
func printShellInit(cmd *cobra.Command, rcFile, completion string) {
	out := cmd.OutOrStdout()

	fmt.Fprintf(out, "# add the following to %s:\n", rcFile)
	fmt.Fprintln(out, "#   eval \"$(wt shell-init)\"")
	fmt.Fprintln(out)
	fmt.Fprint(out, wrapperFunction)
	fmt.Fprintln(out)
	fmt.Fprint(out, completion)
}
