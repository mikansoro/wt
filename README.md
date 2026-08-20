# wt

A git-worktree manager, written in Go.

`wt` manages a fixed pool of reusable worktrees ("slots") instead of one worktree per
branch: each repo gets a `main` worktree plus six numbered slots, and `wt go <branch>`
assigns a branch to the least-recently-used slot, reusing slots after a safety check.

## Install

```bash
make build        # builds ./wt with the version stamped from `git describe`
# or, without make:
go build -o wt ./cmd/wt
```

## Commands

| Command | Aliases | Purpose |
|---|---|---|
| `wt clone <repo-url> [dir]` | — | Bare-clone a repo, create `main` + 6 slot worktrees |
| `wt go <branch>` | `wt g` | Assign `<branch>` to a slot, print the slot path on stdout (for `cd`) |
| `wt list` | `wt ls`, `wt status` | Show worktree → branch mapping and cleanliness |
| `wt release [slot\|branch]` | `wt free` | Return a slot to the idle pool |
| `wt init` | — | Print shell integration (wrapper function + tab completion) |
| `wt version` | — | Print build version |

## Repo layout

`wt clone` produces a repo root that is not itself a worktree:

```
myrepo/                       ← repo root (plain directory, NOT itself a worktree)
├── .bare/                    ← the bare repository
├── .git                      ← regular FILE containing: gitdir: ./.bare
├── main/                     ← worktree checked out to the default branch
├── slot-1/ … slot-6/         ← worktrees, initially detached at the default branch HEAD
```

Slots start at a detached HEAD (idle). `git worktree list --porcelain` is always the
source of truth for worktree/branch state; `wt` only persists LRU recency, in
`.bare/wt.json`.

## Shell integration

`wt go` can only print its target path — a subprocess can't change its parent shell's
working directory — so add the wrapper function and tab completion to your shell rc file:

```bash
eval "$(wt init)"
```

`wt init` detects `zsh` or `bash` from `$SHELL` and prints the wrapper function plus a
matching completion script to stdout; everything else goes to stderr as install notes.
