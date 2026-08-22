# wt

A git-worktree manager, written in Go.

`wt` manages a fixed pool of reusable worktrees ("slots") instead of one worktree per
branch: each repo gets a `main` worktree plus six numbered slots, and `wt go <branch>`
assigns a branch to the least-recently-used slot, reusing slots after a safety check.

## Install

Build from source:

```bash
make build        # builds ./wt with the version stamped from `git describe`
# or, without make:
go build -o wt ./cmd/wt
```

Or install straight from the flake, no clone needed — it exposes the package as both
`packages.default` and `packages.wt` for x86_64/aarch64 Linux and macOS:

```bash
# flox (https://flox.dev): add wt to the current flox environment
flox install github:mikansoro/wt

# nix with flakes enabled
nix profile install github:mikansoro/wt
```

Flake installs pin the revision they were installed from; `flox upgrade wt` (or
`nix profile upgrade wt`) rebuilds from the latest commit. Either way, finish with the
[shell integration](#shell-integration) below — with flox, put the `eval` line after
`flox activate` in your shell rc so `wt` is on `PATH` when it runs.

## Commands

| Command | Aliases | Purpose |
|---|---|---|
| `wt adopt [path]` | — | Convert an existing bare+worktrees repo into wt's layout, in place |
| `wt clone <repo-url> [dir]` | — | Bare-clone a repo, create `main` + 6 slot worktrees |
| `wt go <branch>` | `wt g` | Assign `<branch>` to a slot, print the slot path on stdout (for `cd`) |
| `wt list` | `wt ls`, `wt status` | Show worktree → branch mapping and cleanliness |
| `wt release [slot\|branch]` | `wt free` | Return a slot to the idle pool |
| `wt shell-init` | — | Print shell integration (wrapper function + tab completion) |
| `wt version` | — | Print build version |

Destructive steps (reusing a slot with uncommitted or unpushed work, deleting an
unpushed branch) ask for confirmation on the terminal and default to **No**. `wt go`
and `wt release` accept `-y`/`--yes` to assume "yes" at those prompts, for scripts and
other non-interactive use where no terminal is available.

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

A subprocess can't change its parent shell's working directory, so `wt go` on its own
can only print the target slot's path. Add this to `~/.zshrc` or `~/.bashrc` to get a
`wt` that actually `cd`s there:

```bash
eval "$(wt shell-init)"
```

`wt shell-init` detects `zsh` or `bash` from `$SHELL` and prints a `wt()` wrapper
function plus a matching completion script to stdout; everything else (install notes,
unrecognized-shell warnings) goes to stderr, so it stays visible even though `eval`
captures stdout.

The wrapper function only special-cases `go`: it runs `command wt go "$@"`, captures its
stdout (the slot path), and `cd`s to it, while letting stderr chatter pass straight
through to the terminal. Every other subcommand is passed through to the real `wt`
binary unchanged.

Once loaded, tab completion covers subcommand names, branch names (for `go`/`g` and
`release`/`free`), and slot names (`main`, `slot-1`…`slot-6`, for `release`/`free`), for
both bash and zsh. Subcommands also have short aliases: `g` for `go`, `ls`/`status` for
`list`, `free` for `release`. For example:

```bash
wt g my-branch     # assign my-branch to a slot and cd into it
wt free my-branch  # return that slot to the idle pool
```

## Development

A `flake.nix` is provided for a reproducible toolchain; Nix is optional, `go build`
works fine on its own.

```bash
nix develop    # drop into a shell with go, gopls, golangci-lint, gnumake, and git
nix build      # build ./result/bin/wt (version stamped from the flake's git revision)
nix run . -- version
```

`nix build` also runs the full test suite (`go test ./...`) as part of its sandboxed
check phase, so a successful build implies passing tests.
