package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"wt/internal/git"
	"wt/internal/prompt"
	"wt/internal/repo"
)

// NewGoCommand builds `wt go <branch>`.
func NewGoCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "go <branch>",
		Aliases: []string{"g"},
		Short:   "Assign a branch to a slot and print its path",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGo(cmd, args[0])
		},
	}
}

func runGo(cmd *cobra.Command, branch string) error {
	root, err := repo.FindRepoRoot()
	if err != nil {
		return err
	}

	wts, err := git.GetWorktrees(root)
	if err != nil {
		return err
	}

	// Git refuses a duplicate checkout outright; routing to the existing worktree instead
	// matches the user's actual intent and avoids ever needing --force/--ignore-other-worktrees.
	for _, wt := range wts {
		if !wt.Detached && wt.Branch == branch {
			fmt.Fprintf(cmd.ErrOrStderr(), "branch '%s' is already checked out at %s\n", branch, wt.Path)
			fmt.Fprintln(cmd.OutOrStdout(), wt.Path)
			return nil
		}
	}

	st, err := repo.LoadState(root)
	if err != nil {
		return err
	}

	slotName, ok := repo.PickSlot(root, wts, st)
	if !ok {
		return fmt.Errorf("no slots available (repository has no slot-* worktrees)")
	}
	slotPath := filepath.Join(root, slotName)

	detached, currentBranch := slotOccupant(wts, slotPath)

	report, err := repo.SlotSafetyReport(slotPath, detached)
	if err != nil {
		return err
	}

	if !report.Clean(detached) {
		proceed, err := prompt.Confirm(repo.OverwritePrompt(slotName, currentBranch, detached, report))
		if err != nil {
			return err
		}
		if !proceed {
			return prompt.ErrAborted
		}

		if _, _, err := git.Run(slotPath, "reset", "--hard"); err != nil {
			return err
		}
		if _, _, err := git.Run(slotPath, "clean", "-fd"); err != nil {
			return err
		}
	}

	if err := checkoutBranch(root, slotPath, branch); err != nil {
		return err
	}

	st.Slots[slotName] = repo.SlotEntry{LastUsed: time.Now().UTC().Truncate(time.Second)}
	if err := repo.SaveState(root, st); err != nil {
		return err
	}

	runPostActivateHook(cmd, root, slotPath)

	fmt.Fprintf(cmd.ErrOrStderr(), "checked out '%s' in %s\n", branch, slotName)
	fmt.Fprintln(cmd.OutOrStdout(), slotPath)

	return nil
}

// slotOccupant reports whether the slot at slotPath is currently detached, and the branch
// it holds if not. A slot with no matching worktree entry is treated as detached.
func slotOccupant(wts []git.Worktree, slotPath string) (detached bool, branch string) {
	clean := filepath.Clean(slotPath)
	for _, wt := range wts {
		if filepath.Clean(wt.Path) == clean {
			return wt.Detached, wt.Branch
		}
	}

	return true, ""
}

// checkoutBranch checks out branch in slotPath, following the §6.2 step 5 precedence: an
// existing local branch, then a single unambiguous remote match, then a new branch cut
// from the default branch.
func checkoutBranch(root, slotPath, branch string) error {
	if _, _, err := git.Run(slotPath, "show-ref", "--verify", "refs/heads/"+branch); err == nil {
		if _, _, err := git.Run(slotPath, "checkout", branch); err != nil {
			return err
		}

		// A bare clone maps every remote head to a local branch with no tracking config, so a
		// branch that existed at clone time lands here rather than on the --track path below.
		// Restore the upstream that a normal checkout DWIM would have configured; without it
		// the safety check reports "no upstream" forever and every reuse prompts.
		if _, _, err := git.Run(slotPath, "rev-parse", "--abbrev-ref", "@{u}"); err == nil {
			return nil
		}
		if remote := singleRemoteMatch(root, branch); remote != "" {
			_, _, err := git.Run(slotPath, "branch", "--set-upstream-to", remote, branch)
			return err
		}

		return nil
	}

	if remote := singleRemoteMatch(root, branch); remote != "" {
		_, _, err := git.Run(slotPath, "checkout", "--track", remote)
		return err
	}

	base, err := repo.DefaultBranch(root)
	if err != nil {
		return err
	}

	_, _, err = git.Run(slotPath, "checkout", "-b", branch, base)
	return err
}

// singleRemoteMatch returns the remote-tracking ref (e.g. "origin/feat-a") when exactly one
// remote has a branch by this name, or "" otherwise.
func singleRemoteMatch(root, branch string) string {
	stdout, _, err := git.Run(root, "branch", "-r", "--list", "*/"+branch)
	if err != nil {
		return ""
	}

	var remotes []string
	for _, line := range git.SplitLines(stdout) {
		if r := strings.TrimSpace(line); r != "" {
			remotes = append(remotes, r)
		}
	}

	if len(remotes) != 1 {
		return ""
	}

	return remotes[0]
}

// runPostActivateHook runs <root>/.wt-hooks/post-activate, if present and executable, with
// the slot's absolute path as argv[1]. Its exit code and any error running it are ignored;
// its stdout and stderr are wired to wt's own stderr since only the slot path may appear
// on stdout.
func runPostActivateHook(cmd *cobra.Command, root, slotPath string) {
	hookPath := filepath.Join(root, ".wt-hooks", "post-activate")

	info, err := os.Stat(hookPath)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return
	}

	hook := exec.Command(hookPath, slotPath)
	hook.Dir = slotPath
	hook.Stdout = cmd.ErrOrStderr()
	hook.Stderr = cmd.ErrOrStderr()

	_ = hook.Run()
}
