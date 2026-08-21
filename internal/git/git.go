// Package git shells out to the git binary and parses its porcelain output. wt never
// reimplements git logic; this package is the only place that invokes the git binary.
package git

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Error wraps a failed git invocation. Its Error method returns git's stderr text
// verbatim (trimmed of a trailing newline), falling back to the wrapped Go error only
// when git produced no diagnostic text of its own. This lets callers propagate it
// directly as a Cobra RunE error and have main print git's own message, not a paraphrase.
type Error struct {
	Stderr string
	err    error
}

func (e *Error) Error() string {
	msg := strings.TrimRight(e.Stderr, "\n")
	if msg == "" {
		return e.err.Error()
	}
	return msg
}

func (e *Error) Unwrap() error { return e.err }

// Run runs `git -C dir <args...>`, capturing stdout and stderr separately.
func Run(dir string, args ...string) (stdout, stderr string, err error) {
	fullArgs := append([]string{"-C", dir}, args...)
	cmd := exec.Command("git", fullArgs...)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	runErr := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if runErr != nil {
		wrapped := fmt.Errorf("git %s: %w", strings.Join(args, " "), runErr)
		return stdout, stderr, &Error{Stderr: stderr, err: wrapped}
	}

	return stdout, stderr, nil
}

// SplitLines splits porcelain output into lines, dropping a single trailing newline and
// returning nil — not a one-element slice holding "" — for empty input.
func SplitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}

	return strings.Split(s, "\n")
}

// Worktree describes one entry from `git worktree list --porcelain`.
type Worktree struct {
	Path     string // absolute path
	Head     string // commit SHA
	Branch   string // short branch name; "" if detached
	Detached bool
}

// GetWorktrees runs `git -C root worktree list --porcelain` and parses it. Each record is:
//
//	worktree <path>
//	HEAD <sha>
//	branch refs/heads/<name>   (or a bare "detached" line)
//	<blank line ends the record>
func GetWorktrees(root string) ([]Worktree, error) {
	stdout, _, err := Run(root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}

	var worktrees []Worktree
	var current *Worktree

	for _, line := range strings.Split(stdout, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current != nil {
				worktrees = append(worktrees, *current)
			}
			current = &Worktree{Path: CanonicalPath(strings.TrimPrefix(line, "worktree "))}
		case strings.HasPrefix(line, "HEAD "):
			if current != nil {
				current.Head = strings.TrimPrefix(line, "HEAD ")
			}
		case strings.HasPrefix(line, "branch "):
			if current != nil {
				ref := strings.TrimPrefix(line, "branch ")
				current.Branch = strings.TrimPrefix(ref, "refs/heads/")
			}
		case line == "detached":
			if current != nil {
				current.Detached = true
			}
		case line == "":
			if current != nil {
				worktrees = append(worktrees, *current)
				current = nil
			}
		}
	}

	if current != nil {
		worktrees = append(worktrees, *current)
	}

	return worktrees, nil
}

// CanonicalPath returns p with every symlink component resolved, or p unchanged when
// resolution fails (for example when the path does not exist). Shells leave $PWD — and
// therefore os.Getwd — pointing at the symlinked spelling of a directory, while git
// reports worktree paths canonically, so any path that will be compared against another
// must pass through here first.
func CanonicalPath(p string) string {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return resolved
}
