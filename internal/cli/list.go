package cli

import (
	"fmt"
	"path/filepath"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"wt/internal/git"
	"wt/internal/repo"
)

// NewListCommand builds `wt list`.
func NewListCommand() *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls", "status"},
		Short:   "Show worktree to branch mapping and cleanliness",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runList(cmd)
		},
	}
}

func runList(cmd *cobra.Command) error {
	root, err := repo.FindRepoRoot()
	if err != nil {
		return err
	}

	wts, err := git.GetWorktrees(root)
	if err != nil {
		return err
	}

	st, err := repo.LoadState(root)
	if err != nil {
		return err
	}

	var displayed []git.Worktree
	for _, wt := range wts {
		if filepath.Dir(wt.Path) != root {
			continue
		}

		name := filepath.Base(wt.Path)
		if name != "main" {
			if _, ok := repo.SlotNumber(name); !ok {
				continue
			}
		}

		displayed = append(displayed, wt)
	}

	// One QuickStatus subprocess per displayed worktree, fanned out concurrently: each
	// goroutine writes to its own index, so no lock is needed to guard the slice.
	statuses := make([]*repo.WorktreeStatus, len(displayed))
	errs := make([]error, len(displayed))

	var wg sync.WaitGroup
	for i, wt := range displayed {
		wg.Add(1)
		go func(i int, wt git.Worktree) {
			defer wg.Done()
			statuses[i], errs[i] = repo.QuickStatus(wt.Path, wt.Detached)
		}(i, wt)
	}
	wg.Wait()

	// Report the first failure in display order, not whichever goroutine happened to lose
	// the race, so the error a user sees for a given repo state is deterministic.
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "WORKTREE\tBRANCH\tSTATE\tLAST USED")

	now := time.Now()

	for i, wt := range displayed {
		name := filepath.Base(wt.Path)

		branchDisplay := wt.Branch
		if wt.Detached {
			branchDisplay = "(idle)"
		}

		lastUsed := "—"
		if name != "main" {
			if entry, ok := st.Slots[name]; ok {
				lastUsed = humanDuration(now.Sub(entry.LastUsed))
			}
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, branchDisplay, stateLabel(statuses[i], wt.Detached), lastUsed)
	}

	return w.Flush()
}

// stateLabel renders the STATE column: clean, dirty, unpushed, or dirty, unpushed, with a
// trailing "*" when reusing the slot would trigger the §6.2 overwrite prompt. A branch
// with no upstream counts as unpushed.
func stateLabel(status *repo.WorktreeStatus, detached bool) string {
	unpushed := !detached && (!status.HasUpstream || status.Ahead > 0)
	clean := !status.Dirty && (detached || (status.HasUpstream && status.Ahead == 0))

	label := "clean"
	switch {
	case status.Dirty && unpushed:
		label = "dirty, unpushed"
	case status.Dirty:
		label = "dirty"
	case unpushed:
		label = "unpushed"
	}

	if !clean {
		label += "*"
	}

	return label
}

// humanDuration renders a duration as a short relative age.
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	default:
		return fmt.Sprintf("%dd ago", int(d/(24*time.Hour)))
	}
}
