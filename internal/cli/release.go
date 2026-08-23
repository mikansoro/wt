package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"wt/internal/git"
	"wt/internal/prompt"
	"wt/internal/repo"
)

type releaseOptions struct {
	deleteBranch bool
	assumeYes    bool
}

// NewReleaseCommand builds `wt release [slot|branch]`.
func NewReleaseCommand() *cobra.Command {
	opts := &releaseOptions{}

	cmd := &cobra.Command{
		Use:     "release [slot|branch]",
		Aliases: []string{"free"},
		Short:   "Return a slot to the idle pool",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var arg string
			if len(args) == 1 {
				arg = args[0]
			}
			return opts.run(cmd, arg)
		},
	}

	cmd.Flags().BoolVar(&opts.deleteBranch, "delete-branch", false, "delete the branch after releasing the slot")
	cmd.Flags().BoolVarP(&opts.assumeYes, "yes", "y", false, `assume "yes" at safety prompts, for non-interactive use`)

	return cmd
}

func (o *releaseOptions) run(cmd *cobra.Command, arg string) error {
	root, err := repo.FindRepoRoot()
	if err != nil {
		return err
	}

	wts, err := git.GetWorktrees(root)
	if err != nil {
		return err
	}

	var target *git.Worktree
	if arg != "" {
		target, err = resolveReleaseTarget(root, wts, arg)
	} else {
		target, err = worktreeForCWD(wts)
	}
	if err != nil {
		return err
	}

	name := filepath.Base(target.Path)
	if name == "main" {
		return fmt.Errorf("refusing to release 'main'")
	}
	if _, ok := repo.SlotNumber(name); !ok {
		return fmt.Errorf("refusing to release non-slot worktree '%s'", name)
	}

	report, err := repo.SlotSafetyReport(target.Path, target.Detached)
	if err != nil {
		return err
	}

	branch := target.Branch

	if !report.Clean(target.Detached) {
		proceed := o.assumeYes
		if !proceed {
			proceed, err = prompt.Confirm(repo.OverwritePrompt(name, branch, target.Detached, report))
			if err != nil {
				return err
			}
		}
		if !proceed {
			return prompt.ErrAborted
		}

		if _, _, err := git.Run(target.Path, "reset", "--hard"); err != nil {
			return err
		}
		if _, _, err := git.Run(target.Path, "clean", "-fd"); err != nil {
			return err
		}
	}

	base, err := repo.DefaultBranch(root)
	if err != nil {
		return err
	}

	if _, _, err := git.Run(target.Path, "checkout", "--detach", base); err != nil {
		return err
	}

	if o.deleteBranch {
		if branch == "" {
			return fmt.Errorf("no branch to delete: slot was already idle")
		}

		if !report.HasUpstream || len(report.UnpushedCommits) > 0 {
			proceed := o.assumeYes
			if !proceed {
				proceed, err = prompt.Confirm(repo.DeleteBranchPrompt(branch, report))
				if err != nil {
					return err
				}
			}
			if !proceed {
				return prompt.ErrAborted
			}
		}

		if _, _, err := git.Run(root, "branch", "-D", branch); err != nil {
			return err
		}
	}

	st, err := repo.LoadState(root)
	if err != nil {
		return err
	}
	st.Slots[name] = repo.SlotEntry{LastUsed: time.Now().UTC().Truncate(time.Second)}
	if err := repo.SaveState(root, st); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "released %s\n", name)

	return nil
}

// resolveReleaseTarget resolves the wt release argument to a worktree: either a direct
// slot/worktree name (a child of root) or a branch name mapped via `git worktree list`.
func resolveReleaseTarget(root string, wts []git.Worktree, arg string) (*git.Worktree, error) {
	for i := range wts {
		if filepath.Dir(wts[i].Path) == root && filepath.Base(wts[i].Path) == arg {
			return &wts[i], nil
		}
	}

	for i := range wts {
		if !wts[i].Detached && wts[i].Branch == arg {
			return &wts[i], nil
		}
	}

	return nil, fmt.Errorf("no slot or branch named '%s'", arg)
}

// worktreeForCWD finds the worktree containing the current working directory, used when
// `wt release` is invoked with no argument.
func worktreeForCWD(wts []git.Worktree) (*git.Worktree, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting working directory: %w", err)
	}
	cwd = filepath.Clean(cwd)

	for i := range wts {
		wtPath := filepath.Clean(wts[i].Path)
		if cwd == wtPath || strings.HasPrefix(cwd, wtPath+string(filepath.Separator)) {
			return &wts[i], nil
		}
	}

	return nil, fmt.Errorf("current directory is not inside a slot")
}
