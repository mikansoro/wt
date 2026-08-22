package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"wt/internal/git"
	"wt/internal/repo"
)

// defaultSlotCount is the fixed size of the slot pool created by `wt clone`. It is not
// user-configurable (see agent-plan.md §12).
const defaultSlotCount = 6

type cloneOptions struct {
	url string
	dir string
}

// NewCloneCommand builds `wt clone <repo-url> [dir]`.
func NewCloneCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "clone <repo-url> [dir]",
		Short: "Bare-clone a repo and create the main worktree plus the slot pool",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts := &cloneOptions{url: args[0]}
			if len(args) == 2 {
				opts.dir = args[1]
			}
			return opts.run(cmd)
		},
	}
}

func (o *cloneOptions) run(cmd *cobra.Command) error {
	url := o.url

	// Every git invocation runs with -C <target-dir>, which would make git resolve a
	// relative local path against the new pool directory instead of the caller's CWD; pin
	// local-path URLs to absolute before any directory changes hands.
	if _, err := os.Stat(url); err == nil {
		if abs, err := filepath.Abs(url); err == nil {
			url = abs
		}
	}

	dir := o.dir
	if dir == "" {
		dir = repoNameFromURL(url)
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	root, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolving path for %s: %w", dir, err)
	}

	if _, _, err := git.Run(root, "clone", "--bare", url, ".bare"); err != nil {
		return err
	}

	gitPointer := filepath.Join(root, ".git")
	if err := os.WriteFile(gitPointer, []byte("gitdir: ./.bare\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", gitPointer, err)
	}

	bareDir := filepath.Join(root, ".bare")

	// Bare clones don't map remote branches by default; fix the fetch refspec so ordinary
	// remote-tracking branches show up.
	if _, _, err := git.Run(bareDir, "config", "remote.origin.fetch", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	if _, _, err := git.Run(bareDir, "fetch", "origin"); err != nil {
		return err
	}

	// A fresh bare clone has no refs/remotes/origin/HEAD symref yet; set it explicitly so
	// repo.DefaultBranch can resolve it below.
	if _, _, err := git.Run(root, "remote", "set-head", "origin", "--auto"); err != nil {
		return err
	}

	branch, err := repo.DefaultBranch(root)
	if err != nil {
		return err
	}

	if _, _, err := git.Run(root, "worktree", "add", "main", branch); err != nil {
		return err
	}

	// The bare clone copied the default branch as a local head with no tracking config;
	// set the upstream a normal clone would have, so `wt list` reports main as clean.
	if _, _, err := git.Run(root, "branch", "--set-upstream-to", "origin/"+branch, branch); err != nil {
		return err
	}

	now := time.Now().UTC().Truncate(time.Second)
	st := &repo.State{Version: 1, Slots: map[string]repo.SlotEntry{}}

	for n := 1; n <= defaultSlotCount; n++ {
		slotName := fmt.Sprintf("slot-%d", n)

		if _, _, err := git.Run(root, "worktree", "add", "--detach", slotName, branch); err != nil {
			return err
		}

		st.Slots[slotName] = repo.SlotEntry{LastUsed: now}
	}

	if err := repo.SaveState(root, st); err != nil {
		return fmt.Errorf("saving slot state: %w", err)
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "cloned %s into %s\n", o.url, root)

	return nil
}

func repoNameFromURL(url string) string {
	trimmed := strings.TrimRight(url, "/")
	base := filepath.Base(trimmed)

	return strings.TrimSuffix(base, ".git")
}
