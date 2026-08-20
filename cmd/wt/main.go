// Command wt manages git worktrees as a fixed pool of reusable slots. See agent-plan.md
// for the full specification; this file is intentionally minimal wiring only.
package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"

	"wt/internal/cli"
	"wt/internal/prompt"
)

func main() {
	if _, err := exec.LookPath("git"); err != nil {
		fmt.Fprintln(os.Stderr, "wt requires git on your PATH")
		os.Exit(1)
	}

	root := cli.NewRootCommand()

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)

		if errors.Is(err, prompt.ErrAborted) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
