package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"wt/internal/git"
	"wt/internal/repo"
)

type adoptOptions struct {
	path   string
	remote string
	dryRun bool
}

// NewAdoptCommand builds `wt adopt [path]`.
func NewAdoptCommand() *cobra.Command {
	opts := &adoptOptions{}

	cmd := &cobra.Command{
		Use:   "adopt [path]",
		Short: "Convert an existing bare+worktrees repo into wt's layout, in place",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.path = "."
			if len(args) == 1 {
				opts.path = args[0]
			}
			return opts.run(cmd)
		},
	}

	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "show what adopt would do without changing anything")
	cmd.Flags().StringVar(&opts.remote, "remote", "origin",
		"name of the remote to configure and resolve the default branch from")

	return cmd
}

func (o *adoptOptions) run(cmd *cobra.Command) error {
	target, err := filepath.Abs(o.path)
	if err != nil {
		return fmt.Errorf("resolving path for %s: %w", o.path, err)
	}

	// Shape (a): the target or one of its ancestors already carries the wt fingerprint.
	// Reuse the same upward walk `wt go`/`wt list` use, so "already adopted" is judged
	// identically everywhere.
	if root, err := repo.FindRepoRootFrom(target); err == nil {
		return adoptInPlace(cmd, root, o.remote, o.dryRun)
	}

	isBare, err := isBareRepo(target)
	if err == nil && isBare {
		return fmt.Errorf(
			"%s is itself a bare repository with worktrees inside it; adopt does not support "+
				"bare-as-root layouts — see docs/adopting-existing-repos.md for the manual recipe",
			target,
		)
	}

	bareChild, err := findSingleBareChild(target)
	if err != nil {
		return err
	}
	if bareChild == "" {
		return fmt.Errorf(
			"%s is not a bare+worktrees layout adopt can convert; use 'wt clone' for a fresh "+
				"checkout, or see docs/adopting-existing-repos.md for the manual recipe",
			target,
		)
	}

	return convertShapeB(cmd, target, bareChild, o.remote, o.dryRun)
}

// adoptInPlace handles shape (a): target (or an ancestor) already has the wt fingerprint.
// The rename/pointer/repair steps never apply here; only the idempotent fill-in does.
func adoptInPlace(cmd *cobra.Command, root, remoteName string, dryRun bool) error {
	remotes, err := listRemotes(root)
	if err != nil {
		return err
	}
	if err := checkRemoteOnly(remotes, remoteName); err != nil {
		return err
	}

	actions, err := fillIn(cmd, root, root, remoteName, dryRun, remotes)
	if err != nil {
		return err
	}

	if actions == 0 {
		fmt.Fprintln(cmd.ErrOrStderr(), "nothing to do")
	}

	return nil
}

// convertShapeB handles shape (b): target contains exactly one bare repo not named .bare,
// alongside worktrees or unrelated files. It renames the bare repo to .bare, writes the
// pointer file, repairs the worktree links broken by the rename, and then runs the same
// idempotent fill-in that shape (a) uses.
func convertShapeB(cmd *cobra.Command, target, bareDir, remoteName string, dryRun bool) error {
	stderr := cmd.ErrOrStderr()

	// Worktree paths must be inventoried before the rename: git stores absolute paths in
	// both directions, so `git worktree list` against the old location is the last chance
	// to learn them before `worktree repair` is needed to fix them back up.
	existingPaths, err := existingWorktreePaths(bareDir)
	if err != nil {
		return err
	}

	remotes, err := listRemotes(bareDir)
	if err != nil {
		return err
	}
	if err := checkRemoteOnly(remotes, remoteName); err != nil {
		return err
	}

	bareName := filepath.Base(bareDir)
	newBareDir := filepath.Join(target, ".bare")
	gitDir := bareDir

	if dryRun {
		fmt.Fprintf(stderr, "would: rename %s to .bare\n", bareName)
		fmt.Fprintln(stderr, "would: write .git pointer file")
		if len(existingPaths) > 0 {
			fmt.Fprintf(stderr, "would: repair %d existing worktree link(s)\n", len(existingPaths))
		}
	} else {
		if err := os.Rename(bareDir, newBareDir); err != nil {
			return fmt.Errorf("renaming %s to .bare: %w", bareName, err)
		}
		fmt.Fprintf(stderr, "renamed %s to .bare\n", bareName)

		gitPointer := filepath.Join(target, ".git")
		if err := os.WriteFile(gitPointer, []byte("gitdir: ./.bare\n"), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", gitPointer, err)
		}
		fmt.Fprintln(stderr, "wrote .git pointer file")

		if len(existingPaths) > 0 {
			args := append([]string{"worktree", "repair"}, existingPaths...)
			if _, _, err := git.Run(newBareDir, args...); err != nil {
				return err
			}
			fmt.Fprintf(stderr, "repaired %d existing worktree link(s)\n", len(existingPaths))
		}

		gitDir = target
	}

	_, err = fillIn(cmd, target, gitDir, remoteName, dryRun, remotes)
	return err
}

// fillIn runs the remaining conversion steps that apply identically whether the repo just
// got its .bare/.git laid down (shape b) or already had them (shape a): remote-format
// fixes, HEAD alignment, the main worktree, the slot pool, and wt.json bookkeeping. It
// returns the number of structural actions taken (or, under dryRun, that would be taken),
// so callers can report "nothing to do" when a shape (a) target is already fully adopted.
//
// gitDir is where read-only git queries run; it differs from root only for a shape (b)
// dry-run, where root (the future .bare-holding directory) isn't a git repo yet and reads
// must go against the bare repo at its original location instead. Every mutation always
// targets root, and only ever runs when dryRun is false.
func fillIn(cmd *cobra.Command, root, gitDir, remoteName string, dryRun bool, remotes []string) (int, error) {
	stderr := cmd.ErrOrStderr()
	actionsTaken := 0

	hasRemote := slices.Contains(remotes, remoteName)

	if hasRemote {
		wantFetch := fmt.Sprintf("+refs/heads/*:refs/remotes/%s/*", remoteName)
		current, _ := getConfigValue(gitDir, fmt.Sprintf("remote.%s.fetch", remoteName))

		if current != wantFetch {
			if dryRun {
				fmt.Fprintf(stderr, "would: fix %s fetch refspec\n", remoteName)
			} else {
				fetchKey := fmt.Sprintf("remote.%s.fetch", remoteName)
				if _, _, err := git.Run(root, "config", fetchKey, wantFetch); err != nil {
					return actionsTaken, err
				}
				fmt.Fprintf(stderr, "fixed %s fetch refspec\n", remoteName)
			}
			actionsTaken++
		}

		if !dryRun {
			if _, _, err := git.Run(root, "fetch", remoteName); err != nil {
				return actionsTaken, err
			}
			if _, _, err := git.Run(root, "remote", "set-head", remoteName, "--auto"); err != nil {
				return actionsTaken, err
			}
		}
	}

	var branch string
	var err error
	if dryRun {
		branch, err = peekDefaultBranch(gitDir, remoteName)
	} else {
		branch, err = repo.DefaultBranchFrom(root, remoteName)
	}
	if err != nil {
		return actionsTaken, fmt.Errorf("resolving default branch: %w", err)
	}

	// Align the bare repo's own HEAD to the resolved default branch. This is what
	// `repo.DefaultBranch`'s final fallback reads, and what a fresh clone of this bare repo
	// would check out; a repo adopted from an arbitrary hand-rolled bare clone may have it
	// pointed anywhere. Unconditional and idempotent, so it is safe to run every time.
	needsHeadAlign, err := headNeedsAlignment(gitDir, branch)
	if err != nil {
		return actionsTaken, fmt.Errorf("checking repository HEAD: %w", err)
	}
	if needsHeadAlign {
		if dryRun {
			fmt.Fprintf(stderr, "would: align repository HEAD to %s\n", branch)
		} else {
			if _, _, err := git.Run(root, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
				return actionsTaken, err
			}
			fmt.Fprintf(stderr, "aligned repository HEAD to %s\n", branch)
		}
		actionsTaken++
	}

	wts, err := git.GetWorktrees(gitDir)
	if err != nil {
		return actionsTaken, err
	}
	hasMain, slotPresent, all := classifyWorktrees(root, wts)

	if !hasMain {
		if elsewhere, ok := branchCheckedOutElsewhere(all, branch); ok {
			fmt.Fprintf(stderr,
				"warning: default branch '%s' is already checked out at %s; not creating main/\n",
				branch, elsewhere)
		} else if dryRun {
			fmt.Fprintf(stderr, "would: create main worktree on %s\n", branch)
			actionsTaken++
		} else {
			if _, _, err := git.Run(root, "worktree", "add", "main", branch); err != nil {
				return actionsTaken, err
			}

			// A bare clone copies the default branch as a local head with no tracking
			// config; restore the upstream a normal clone would have, unless adopt found
			// one already configured (this branch may have existed long before adoption).
			if hasRemote {
				mainPath := filepath.Join(root, "main")
				if _, _, err := git.Run(mainPath, "rev-parse", "--abbrev-ref", "@{u}"); err != nil {
					upstream := remoteName + "/" + branch
					if _, _, err := git.Run(root, "branch", "--set-upstream-to", upstream, branch); err != nil {
						return actionsTaken, err
					}
				}
			}

			fmt.Fprintf(stderr, "created main worktree on %s\n", branch)
			actionsTaken++
		}
	}

	st, err := repo.LoadState(root)
	if err != nil {
		return actionsTaken, err
	}

	now := time.Now().UTC().Truncate(time.Second)
	stateChanged := false

	for n := 1; n <= defaultSlotCount; n++ {
		slotName := fmt.Sprintf("slot-%d", n)

		if slotNameBranchExists(gitDir, slotName) {
			fmt.Fprintf(stderr,
				"warning: branch '%s' exists; slot names must never be used as branch names\n",
				slotName)
		}

		if slotPresent[slotName] {
			if _, ok := st.Slots[slotName]; ok {
				continue
			}

			if dryRun {
				fmt.Fprintf(stderr, "would: record LRU entry for %s\n", slotName)
			} else {
				st.Slots[slotName] = repo.SlotEntry{LastUsed: now}
				stateChanged = true
				fmt.Fprintf(stderr, "recorded LRU entry for %s\n", slotName)
			}
			actionsTaken++
			continue
		}

		if dryRun {
			fmt.Fprintf(stderr, "would: create %s\n", slotName)
		} else {
			if _, _, err := git.Run(root, "worktree", "add", "--detach", slotName, branch); err != nil {
				return actionsTaken, err
			}
			st.Slots[slotName] = repo.SlotEntry{LastUsed: now}
			stateChanged = true
			fmt.Fprintf(stderr, "created %s\n", slotName)
		}
		actionsTaken++
	}

	if stateChanged {
		if err := repo.SaveState(root, st); err != nil {
			return actionsTaken, fmt.Errorf("saving slot state: %w", err)
		}
	}

	return actionsTaken, nil
}

// isBareRepo reports whether dir is itself a git repository and, if so, whether it is
// bare. A non-git dir surfaces as an error, which callers treat as "not bare" rather than
// fatal, since ruling out shape (c) this way is exactly how shape (b)/(d) get considered.
func isBareRepo(dir string) (bool, error) {
	stdout, _, err := git.Run(dir, "rev-parse", "--is-bare-repository")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(stdout) == "true", nil
}

// findSingleBareChild looks for exactly one immediate subdirectory of target, other than
// .bare itself, that is a bare git repository. It returns "" if none is found (shape d),
// and an error if more than one is found (an ambiguous layout adopt refuses to guess at).
func findSingleBareChild(target string) (string, error) {
	entries, err := os.ReadDir(target)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", target, err)
	}

	var found []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == ".bare" {
			continue
		}

		childPath := filepath.Join(target, e.Name())
		isBare, err := isBareRepo(childPath)
		if err != nil || !isBare {
			continue
		}

		found = append(found, childPath)
	}

	switch len(found) {
	case 0:
		return "", nil
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf(
			"found multiple bare repositories under %s (%s); adopt needs exactly one — see "+
				"docs/adopting-existing-repos.md for the manual recipe",
			target, strings.Join(found, ", "),
		)
	}
}

// existingWorktreePaths lists the absolute paths of every worktree linked to the bare repo
// at bareDir, excluding the bare repo's own porcelain entry (its path is bareDir itself,
// which is about to be renamed, not a linked worktree needing repair).
func existingWorktreePaths(bareDir string) ([]string, error) {
	wts, err := git.GetWorktrees(bareDir)
	if err != nil {
		return nil, err
	}

	var paths []string
	for _, w := range wts {
		if filepath.Clean(w.Path) == filepath.Clean(bareDir) {
			continue
		}
		paths = append(paths, w.Path)
	}

	return paths, nil
}

// classifyWorktrees filters wts down to the ones that are direct children of root, dropping
// the bare repo's own porcelain entry, and reports whether a "main" worktree is among them
// plus which slot-N worktrees are present. all is every remaining child worktree, used to
// check whether the default branch is checked out somewhere other than "main".
func classifyWorktrees(root string, wts []git.Worktree) (
	hasMain bool, slotPresent map[string]bool, all []git.Worktree,
) {
	slotPresent = map[string]bool{}

	for _, w := range wts {
		if filepath.Dir(w.Path) != root {
			continue
		}

		name := filepath.Base(w.Path)
		if name == ".bare" {
			continue
		}

		all = append(all, w)

		if name == "main" {
			hasMain = true
			continue
		}
		if _, ok := repo.SlotNumber(name); ok {
			slotPresent[name] = true
		}
	}

	return hasMain, slotPresent, all
}

// branchCheckedOutElsewhere reports the path of the first non-detached worktree checked
// out on branch, if any.
func branchCheckedOutElsewhere(wts []git.Worktree, branch string) (string, bool) {
	for _, w := range wts {
		if !w.Detached && w.Branch == branch {
			return w.Path, true
		}
	}

	return "", false
}

// slotNameBranchExists reports whether a local or remote-tracking branch literally named
// slotName exists. wt never creates or checks out such a branch, but adopt only warns about
// one it finds; per the slot pool's invariants, it must never touch it.
func slotNameBranchExists(dir, slotName string) bool {
	if _, _, err := git.Run(dir, "show-ref", "--verify", "--quiet", "refs/heads/"+slotName); err == nil {
		return true
	}

	stdout, _, err := git.Run(dir, "branch", "-r", "--list", "*/"+slotName)
	if err != nil {
		return false
	}

	return len(git.SplitLines(stdout)) > 0
}

// listRemotes returns the configured remote names for dir, one per line of `git remote`.
func listRemotes(dir string) ([]string, error) {
	stdout, _, err := git.Run(dir, "remote")
	if err != nil {
		return nil, err
	}

	return git.SplitLines(stdout), nil
}

// checkRemoteOnly enforces that adopt only ever half-configures a repo that has no
// remotes at all, or one whose chosen remote already exists. A repo with remotes, none of
// which match, is refused outright: skipping the refspec fix for it would silently
// reintroduce the shadow-branch trap documented in docs/adopting-existing-repos.md.
func checkRemoteOnly(remotes []string, remoteName string) error {
	if len(remotes) == 0 || slices.Contains(remotes, remoteName) {
		return nil
	}

	return fmt.Errorf(
		"no remote named '%s' found (remotes: %s); rerun with 'wt adopt --remote <name>', or "+
			"see docs/adopting-existing-repos.md for the manual recipe",
		remoteName, strings.Join(remotes, ", "),
	)
}

// getConfigValue reads a git config key from dir. A missing key reports ok=false rather
// than an error, since "not set" is the expected steady state before adopt's first run.
func getConfigValue(dir, key string) (value string, ok bool) {
	stdout, _, err := git.Run(dir, "config", "--get", key)
	if err != nil {
		return "", false
	}

	return strings.TrimSpace(stdout), true
}

// peekDefaultBranch resolves the default branch without mutating anything, for --dry-run
// planning against a repo that doesn't have its remote-tracking HEAD set yet. It mirrors
// repo.DefaultBranchFrom's precedence but never calls `git remote set-head`, since --dry-run
// must not touch the repository at all.
func peekDefaultBranch(dir, remote string) (string, error) {
	if stdout, _, err := git.Run(dir, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD"); err == nil {
		return strings.TrimPrefix(strings.TrimSpace(stdout), remote+"/"), nil
	}

	stdout, _, err := git.Run(dir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(stdout), nil
}

// headNeedsAlignment reports whether the repo's own HEAD symref points somewhere other
// than refs/heads/branch.
func headNeedsAlignment(dir, branch string) (bool, error) {
	stdout, _, err := git.Run(dir, "symbolic-ref", "HEAD")
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(stdout) != "refs/heads/"+branch, nil
}
