# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

`wt` is a single-binary CLI (Go, standard library only) that manages git worktrees as a **fixed pool of reusable slots** rather than one worktree per branch. `agent-plan.md` is the authoritative, section-numbered specification; consult it before implementing anything. This file summarizes how to work on this repo, the plan file holds the project requirements for implementation. 

## Commands

The build/test tooling described below comes from `agent-plan.md` §9/§11 and applies once `go.mod` and sources exist (`module wt`, Go 1.22+).

```bash
go build -o wt .                 # local build (static by default; no cgo)
go test ./...                    # full test suite (stdlib testing only)
go test -run TestName ./...      # single test
```

Cross-compiled release binaries are built with `CGO_ENABLED=0` and `-ldflags "-s -w -X main.version=$(git describe --tags --always)"` for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64` (see §9). `main.version` is a package-level `var` stamped at build time and printed by `wt version`.

## Architecture

**Never reimplement git logic.** `wt` shells out to the `git` binary via `os/exec`, composes porcelain commands, and layers slot bookkeeping, safety checks, and shell integration on top. The only runtime dependency is `git` on `$PATH`.

**Slot pool model.** `wt clone` produces a repo root that is *not* itself a worktree: a `.bare/` bare repo, a `.git` regular file containing `gitdir: ./.bare`, a `main/` worktree, and `slot-1/`…`slot-6/` worktrees (count is the constant `defaultSlotCount = 6`, not configurable). Idle slots sit at **detached HEAD**. `wt go <branch>` assigns a branch to the least-recently-used slot; `wt release` returns a slot to the idle pool.

**Git is the single source of truth.** Live worktree/branch state always comes from `git worktree list --porcelain`. The only metadata `wt` persists is LRU recency in `.bare/wt.json` (`{"slots": {"slot-N": {"last_used": <RFC3339 UTC>}}}`). Treat `wt.json` as hints only: tolerate it missing or naming slots that no longer exist, and never crash if a slot was removed out-of-band. Writes are atomic (`wt.json.tmp` + `os.Rename`). `main` is never in `slots` and never eligible for LRU selection.

**Repo root discovery** (works from root, `main/`, or any slot): walk upward to the first directory containing **both** a `.bare/` directory and a `.git` regular file. Every git invocation then uses `git -C <explicit-path>` so behavior never depends on CWD.

**Output contract (critical).** A subprocess can't change its parent shell's CWD, so `wt go` prints the target slot's absolute path as the *only* thing on **stdout**; all human chatter goes to **stderr**. `wt init` emits a shell wrapper function that captures stdout and runs `cd`. Prompts must read/write `/dev/tty` directly (never stdin/stdout), so they work while stdout is captured.

**Parse only porcelain output** (`git worktree list --porcelain`, `git status --porcelain`) — never human-facing git output. On any git failure, print git's stderr verbatim and exit 1.

## Invariants to preserve (see §7)

- Never mutate a slot without a clean safety report or an explicit `y`; every destructive prompt defaults to **No**.
- Never bypass git's single-checkout rule (no `--force`/`--ignore-other-worktrees`). If a branch is already checked out somewhere, route to that worktree instead of erroring.
- LRU selection never touches `main` or any non-`slot-*` worktree.
- Exit codes: `0` success, `1` user error (bad args, not a wt repo, git failure), `2` aborted at a safety prompt.

## Planned file layout (§10)

`main.go` (dispatch + startup checks), `git.go` (`runGit` wrapper, `Worktree` type, porcelain parsers), `repo.go` (root discovery, default branch, state load/save), `cmd_{clone,go,list,release,init}.go`, `shell.go` (wrapper + completion scripts as const strings). Subcommand dispatch is a plain `switch os.Args[1]` with per-command `flag.NewFlagSet` — no CLI framework. Target size ~700–900 lines including tests.

Tests build fixtures in `t.TempDir()`: create a local bare "remote", seed a commit on `main`, then invoke the compiled `wt` binary against it.
