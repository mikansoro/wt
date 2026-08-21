package repo

import (
	"strconv"
	"strings"

	"wt/internal/git"
)

// WorktreeStatus is a lightweight, one-subprocess status for a single worktree. `wt list`
// uses it instead of SafetyReport because it only needs to answer "clean or not," not the
// file and commit lists that the destructive-action prompts render.
type WorktreeStatus struct {
	Dirty       bool
	HasUpstream bool
	Ahead       int
}

// QuickStatus runs a single `git status --porcelain=v2 --branch` against path.
// --no-optional-locks keeps concurrent QuickStatus calls, which `wt list` fans out one per
// worktree, from contending over the same repo's index-refresh lock. For a detached
// worktree, upstream and ahead are meaningless and are forced to their zero values
// regardless of what the porcelain output happens to contain.
func QuickStatus(path string, detached bool) (*WorktreeStatus, error) {
	stdout, _, err := git.Run(path, "--no-optional-locks", "status", "--porcelain=v2", "--branch")
	if err != nil {
		return nil, err
	}

	status := parseQuickStatus(stdout)
	if detached {
		status.HasUpstream = false
		status.Ahead = 0
	}

	return &status, nil
}

// parseQuickStatus parses `git status --porcelain=v2 --branch` output into a WorktreeStatus.
// It is a pure function so the porcelain grammar can be unit-tested without invoking git.
//
// Header lines start with "# ": branch.upstream signals that an upstream is configured, and
// branch.ab (only emitted when an upstream exists) carries the ahead count. Every other
// non-empty line is a change record — prefixes "1 " (ordinary), "2 " (renamed/copied), "u "
// (unmerged), "? " (untracked), or "! " (ignored, only present with --ignored) — and any of
// them marks the worktree dirty.
func parseQuickStatus(output string) WorktreeStatus {
	var status WorktreeStatus

	for _, line := range git.SplitLines(output) {
		switch {
		case strings.HasPrefix(line, "# branch.upstream "):
			status.HasUpstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				if n, err := strconv.Atoi(strings.TrimPrefix(fields[2], "+")); err == nil {
					status.Ahead = n
				}
			}
		case strings.HasPrefix(line, "# "):
			// branch.oid, branch.head, or any other header: informational only.
		default:
			status.Dirty = true
		}
	}

	return status
}
