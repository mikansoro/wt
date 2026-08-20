// Package repo implements repo-root discovery, default-branch resolution, slot pool
// bookkeeping (wt.json), LRU slot selection, and the safety checks that gate slot reuse.
// Git remains the single source of truth for worktree/branch state; this package only
// persists LRU recency.
package repo

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"wt/internal/git"
)

// FindRepoRoot walks upward from the current working directory looking for the first
// directory that contains both a .bare directory and a .git regular file — the unique
// fingerprint of a repo laid out by `wt clone` (see agent-plan.md §5).
func FindRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getting working directory: %w", err)
	}

	for {
		bareInfo, bareErr := os.Stat(filepath.Join(dir, ".bare"))
		gitInfo, gitErr := os.Stat(filepath.Join(dir, ".git"))

		if bareErr == nil && bareInfo.IsDir() && gitErr == nil && !gitInfo.IsDir() {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("not a wt-managed repository (run inside a repo cloned with 'wt clone')")
		}
		dir = parent
	}
}

// DefaultBranch determines the repo's default branch. A fresh bare clone has no
// refs/remotes/origin/HEAD symref until one is set explicitly, so a failed lookup is
// retried once after `git remote set-head origin --auto`, and finally falls back to the
// root worktree's own HEAD.
func DefaultBranch(root string) (string, error) {
	if branch, ok := tryDefaultBranchRef(root); ok {
		return branch, nil
	}

	if _, _, err := git.Run(root, "remote", "set-head", "origin", "--auto"); err == nil {
		if branch, ok := tryDefaultBranchRef(root); ok {
			return branch, nil
		}
	}

	stdout, _, err := git.Run(root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(stdout), nil
}

func tryDefaultBranchRef(root string) (string, bool) {
	stdout, _, err := git.Run(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", false
	}

	return strings.TrimPrefix(strings.TrimSpace(stdout), "origin/"), true
}
