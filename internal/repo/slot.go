package repo

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"wt/internal/git"
)

var slotNameRe = regexp.MustCompile(`^slot-([0-9]+)$`)

// SlotNumber reports whether name matches the slot-N naming scheme and, if so, its
// numeric suffix (used for LRU tie-breaking).
func SlotNumber(name string) (int, bool) {
	m := slotNameRe.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}

	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}

	return n, true
}

// PickSlot selects the least-recently-used slot to assign a branch to, considering only
// slot-* worktrees that are direct children of root (never main, never other worktrees).
// Idle (detached) slots are preferred over occupied ones; within each group, older
// last_used timestamps sort first, and slots absent from state sort as oldest. Ties break
// by ascending slot number.
func PickSlot(root string, wts []git.Worktree, s *State) (string, bool) {
	type candidate struct {
		name     string
		number   int
		lastUsed time.Time
	}

	var idle, occupied []candidate

	for _, wt := range wts {
		if filepath.Dir(wt.Path) != root {
			continue
		}

		name := filepath.Base(wt.Path)
		number, ok := SlotNumber(name)
		if !ok {
			continue
		}

		var lastUsed time.Time
		if entry, exists := s.Slots[name]; exists {
			lastUsed = entry.LastUsed
		}

		c := candidate{name: name, number: number, lastUsed: lastUsed}
		if wt.Detached {
			idle = append(idle, c)
		} else {
			occupied = append(occupied, c)
		}
	}

	byRecencyThenNumber := func(cands []candidate) (string, bool) {
		if len(cands) == 0 {
			return "", false
		}
		sort.Slice(cands, func(i, j int) bool {
			if !cands[i].lastUsed.Equal(cands[j].lastUsed) {
				return cands[i].lastUsed.Before(cands[j].lastUsed)
			}
			return cands[i].number < cands[j].number
		})
		return cands[0].name, true
	}

	if name, ok := byRecencyThenNumber(idle); ok {
		return name, true
	}

	return byRecencyThenNumber(occupied)
}

// SafetyReport is the outcome of the §6.2 safety check for one slot.
type SafetyReport struct {
	DirtyFiles      []string
	UnpushedCommits []string
	HasUpstream     bool
}

// Clean reports whether reusing this slot would be silent (no prompt). For a detached
// slot only the dirty check applies; for a slot on a branch, all three checks must pass.
func (r *SafetyReport) Clean(detached bool) bool {
	if detached {
		return len(r.DirtyFiles) == 0
	}
	return len(r.DirtyFiles) == 0 && r.HasUpstream && len(r.UnpushedCommits) == 0
}

// SlotSafetyReport runs the §6.2 safety check table against slotPath. Detached slots skip
// the upstream/unpushed checks entirely, since they have no branch to check.
func SlotSafetyReport(slotPath string, detached bool) (*SafetyReport, error) {
	report := &SafetyReport{}

	stdout, _, err := git.Run(slotPath, "status", "--porcelain")
	if err != nil {
		return nil, err
	}
	report.DirtyFiles = git.SplitLines(stdout)

	if detached {
		return report, nil
	}

	_, _, err = git.Run(slotPath, "rev-parse", "--abbrev-ref", "@{u}")
	report.HasUpstream = err == nil

	if report.HasUpstream {
		stdout, _, err = git.Run(slotPath, "log", "@{u}..HEAD", "--oneline")
		if err != nil {
			return nil, err
		}
		report.UnpushedCommits = git.SplitLines(stdout)
	}

	return report, nil
}

// OverwritePrompt renders the §6.2 confirmation text shown before reusing a dirty or
// unpushed slot.
func OverwritePrompt(slotName, branch string, detached bool, report *SafetyReport) string {
	var b strings.Builder

	if detached {
		fmt.Fprintf(&b, "%s is idle but has uncommitted changes\n", slotName)
	} else {
		fmt.Fprintf(&b, "%s currently holds branch '%s'\n", slotName, branch)
	}

	if len(report.DirtyFiles) > 0 {
		fmt.Fprintf(&b, "  - %d uncommitted files (%s)\n", len(report.DirtyFiles), strings.Join(report.DirtyFiles, ", "))
	}

	if !detached {
		if !report.HasUpstream {
			fmt.Fprint(&b, "  - branch has no upstream\n")
		} else if len(report.UnpushedCommits) > 0 {
			fmt.Fprintf(&b, "  - %d unpushed commit(s): %s\n", len(report.UnpushedCommits), strings.Join(report.UnpushedCommits, "; "))
		}
	}

	fmt.Fprint(&b, "Overwrite this slot? Uncommitted/untracked work will be lost;\n")
	fmt.Fprint(&b, "the branch ref itself survives. [y/N] ")

	return b.String()
}

// DeleteBranchPrompt renders the confirmation text for --delete-branch when the branch has
// no upstream or carries commits the remote doesn't have.
func DeleteBranchPrompt(branch string, report *SafetyReport) string {
	var b strings.Builder

	fmt.Fprintf(&b, "branch '%s' ", branch)

	switch {
	case !report.HasUpstream && len(report.UnpushedCommits) > 0:
		fmt.Fprintf(&b, "has no upstream and %d unpushed commit(s)", len(report.UnpushedCommits))
	case !report.HasUpstream:
		fmt.Fprint(&b, "has no upstream")
	default:
		fmt.Fprintf(&b, "has %d unpushed commit(s)", len(report.UnpushedCommits))
	}

	fmt.Fprint(&b, "\nDelete it anyway? This cannot be undone. [y/N] ")

	return b.String()
}
