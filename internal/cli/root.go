// Package cli wires up wt's Cobra command tree. Command RunE methods return errors rather
// than printing and exiting directly, so cmd/wt/main.go is the sole place that writes to
// stderr and chooses a process exit code.
package cli

import "github.com/spf13/cobra"

const usageText = `usage: wt <command> [args]

commands:
  clone <repo-url> [dir]   bare-clone a repo and create the slot pool
  go <branch>              assign a branch to a slot (alias: g)
  list                     show worktree status (aliases: ls, status)
  release [slot|branch]    return a slot to the idle pool (alias: free)
  init                     print shell integration
  version                  print the build version`

// NewRootCommand builds the wt command tree.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "wt",
		Short:         "Manage git worktrees as a fixed pool of reusable slots",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errUsage
		},
	}
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(NewCloneCommand())
	root.AddCommand(NewGoCommand())
	root.AddCommand(NewListCommand())
	root.AddCommand(NewReleaseCommand())
	root.AddCommand(NewInitCommand())
	root.AddCommand(NewVersionCommand())

	return root
}

type usageError struct{}

func (usageError) Error() string { return usageText }

var errUsage = usageError{}
