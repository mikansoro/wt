# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project status

`wt` is a single-binary CLI (Go; Cobra for the command tree, standard library for everything else) that manages git worktrees as a **fixed pool of reusable slots** rather than one worktree per branch. `agent-plan.md` is the authoritative, section-numbered specification for *behavior*; its file layout and stdlib-only notes are superseded by the Go CLI standards imported above. Consult the plan before changing any command's semantics.

## Commands

```bash
make build                       # go build with the version ldflags stamp → ./wt
make test                        # go test -count=1 ./... (see note below)
make lint                        # golangci-lint if installed, else go vet
go test -count=1 -run TestName ./tests   # single test
```

Always pass `-count=1` when running tests directly: the integration tests exec a binary they build at run time, so Go's test cache cannot see that they depend on the source packages and will happily report a stale `(cached)` pass.

Cross-compiled release binaries are built with `CGO_ENABLED=0` and `-ldflags "-s -w -X wt/internal/version.Version=$(git describe --tags --always)"` for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`. `wt version` prints the stamped value, falling back to `debug.ReadBuildInfo` for `go install` builds.

## Architecture

**Never reimplement git logic.** `wt` shells out to the `git` binary via `os/exec`, composes porcelain commands, and layers slot bookkeeping, safety checks, and shell integration on top. The only runtime dependency is `git` on `$PATH`.

**Slot pool model.** `wt clone` produces a repo root that is *not* itself a worktree: a `.bare/` bare repo, a `.git` regular file containing `gitdir: ./.bare`, a `main/` worktree, and `slot-1/`…`slot-6/` worktrees (count is the constant `defaultSlotCount = 6`, not configurable). Idle slots sit at **detached HEAD**. `wt go <branch>` assigns a branch to the least-recently-used slot; `wt release` returns a slot to the idle pool.

**Git is the single source of truth.** Live worktree/branch state always comes from `git worktree list --porcelain`. The only metadata `wt` persists is LRU recency in `.bare/wt.json` (`{"slots": {"slot-N": {"last_used": <RFC3339 UTC>}}}`). Treat `wt.json` as hints only: tolerate it missing or naming slots that no longer exist, and never crash if a slot was removed out-of-band. Writes are atomic (`wt.json.tmp` + `os.Rename`). `main` is never in `slots` and never eligible for LRU selection.

**Repo root discovery** (works from root, `main/`, or any slot): walk upward to the first directory containing **both** a `.bare/` directory and a `.git` regular file. Every git invocation then uses `git -C <explicit-path>` so behavior never depends on CWD.

**Output contract (critical).** A subprocess can't change its parent shell's CWD, so `wt go` prints the target slot's absolute path as the *only* thing on **stdout**; all human chatter goes to **stderr**. `wt shell-init` emits a shell wrapper function that captures stdout and runs `cd`. Prompts must read/write `/dev/tty` directly (never stdin/stdout), so they work while stdout is captured.

**Parse only porcelain output** (`git worktree list --porcelain`, `git status --porcelain`) — never human-facing git output. On any git failure, print git's stderr verbatim and exit 1.

## Invariants to preserve (see §7)

- Never mutate a slot without a clean safety report or an explicit confirmation — an interactive `y`, or the `--yes`/`-y` flag on `go`/`release` for non-interactive use; every destructive prompt defaults to **No**.
- Never bypass git's single-checkout rule (no `--force`/`--ignore-other-worktrees`). If a branch is already checked out somewhere, route to that worktree instead of erroring.
- LRU selection never touches `main` or any non-`slot-*` worktree.
- Exit codes: `0` success, `1` user error (bad args, not a wt repo, git failure), `2` aborted at a safety prompt.

## File layout

- `cmd/wt/main.go` — minimal wiring: git-on-PATH check, `Execute()`, error → stderr, exit-code mapping (`prompt.ErrAborted` → 2, anything else → 1)
- `internal/cli/` — one Cobra `NewXCommand()` factory per subcommand (`adopt`, `clone`, `go`, `list`, `release`, `shell-init`, `version`), `root.go` (SilenceErrors/SilenceUsage, default completion cmd disabled), `shell.go` (wrapper + completion scripts as const strings)
- `internal/git/` — `git.Run` wrapper (`Error.Error()` returns git's stderr verbatim), `Worktree` type, porcelain parsers
- `internal/repo/` — root discovery, default branch, `wt.json` state, LRU `PickSlot`, `SafetyReport`
- `internal/prompt/` — `/dev/tty` confirmation, `ErrAborted` sentinel
- `internal/version/` — ldflags-stamped version vars
- `tests/` — black-box integration tests; they exec the compiled binary, never internal functions

Tests build fixtures in `t.TempDir()`: create a local bare "remote", seed a commit on `main`, then invoke the compiled `wt` binary against it. Subprocesses run with `Setsid` so `/dev/tty` prompts deterministically abort (exit 2) instead of blocking.
