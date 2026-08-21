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

	return FindRepoRootFrom(dir)
}

// FindRepoRootFrom walks upward from dir looking for the first directory that contains
// both a .bare directory and a .git regular file — the unique fingerprint of a repo laid
// out by `wt clone` (see agent-plan.md §5). FindRepoRoot is the common case, starting from
// the current working directory; `wt adopt` uses this directly to detect whether a target
// path or one of its ancestors is already fingerprinted.
//
// dir is canonicalized first: os.Getwd returns the shell's symlinked $PWD spelling, while
// `git worktree list` reports canonical paths, and every "is this worktree a child of
// root?" comparison downstream assumes the two agree.
func FindRepoRootFrom(dir string) (string, error) {
	dir = git.CanonicalPath(dir)

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

// DefaultBranch determines the repo's default branch, assuming a remote named "origin".
// It is a thin wrapper around DefaultBranchFrom for the common case; `wt adopt` calls
// DefaultBranchFrom directly to support repos adopted under a different remote name.
func DefaultBranch(root string) (string, error) {
	return DefaultBranchFrom(root, "origin")
}

// DefaultBranchFrom determines the repo's default branch using remote as the
// remote-tracking namespace. A fresh bare clone has no refs/remotes/<remote>/HEAD symref
// until one is set explicitly, so a failed lookup is retried once after
// `git remote set-head <remote> --auto`, and finally falls back to the root worktree's own
// HEAD.
func DefaultBranchFrom(root, remote string) (string, error) {
	if branch, ok := tryDefaultBranchRef(root, remote); ok {
		return branch, nil
	}

	if _, _, err := git.Run(root, "remote", "set-head", remote, "--auto"); err == nil {
		if branch, ok := tryDefaultBranchRef(root, remote); ok {
			return branch, nil
		}
	}

	stdout, _, err := git.Run(root, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(stdout), nil
}

func tryDefaultBranchRef(root, remote string) (string, bool) {
	stdout, _, err := git.Run(root, "symbolic-ref", "--short", "refs/remotes/"+remote+"/HEAD")
	if err != nil {
		return "", false
	}

	return strings.TrimPrefix(strings.TrimSpace(stdout), remote+"/"), true
}
