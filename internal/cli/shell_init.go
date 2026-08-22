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
			return runShellInit(cmd)
		},
	}
}

func runShellInit(cmd *cobra.Command) error {
	shellName := filepath.Base(os.Getenv("SHELL"))

	switch shellName {
	case "zsh":
		return printShellInit(cmd, "~/.zshrc", zshCompletion)
	case "bash":
		return printShellInit(cmd, "~/.bashrc", bashCompletion)
	default:
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "wt: unrecognized shell %q, emitting bash integration\n", shellName)
		return printShellInit(cmd, "your shell rc file", bashCompletion)
	}
}

// printShellInit writes the wrapper function and completion script to stdout, preceded by
// install instructions as "#" comment lines so `eval "$(wt shell-init)"` remains valid shell.
func printShellInit(cmd *cobra.Command, rcFile, completion string) error {
	_, err := fmt.Fprintf(cmd.OutOrStdout(),
		"# add the following to %s:\n#   eval \"$(wt shell-init)\"\n\n%s\n%s",
		rcFile, wrapperFunction, completion)
	return err
}
