# Adopting an existing bare+worktrees repo

`wt clone` builds its layout from scratch. A repo you already maintain as a bare checkout with worktrees
can be converted by hand. The recipe below was verified end-to-end against the common hand-rolled layouts.

## The layout wt requires

`wt` recognizes a repo root by one fingerprint: a directory containing both a `.bare/` bare repository and
a `.git` regular file pointing at it. No other layout is recognized, from any working directory.

```
myrepo/                       ← repo root (plain directory, NOT itself a worktree)
├── .bare/                    ← the bare repository
├── .git                      ← regular FILE containing: gitdir: ./.bare
├── main/                     ← worktree checked out to the default branch
├── slot-1/ … slot-6/         ← detached worktrees forming the slot pool
```

## Converting an existing repo

Run these from the directory that will become the repo root.

1. Rename the bare repository to `.bare` if it is named anything else:

   ```bash
   mv repo.git .bare
   ```

   If the bare repo currently *is* the root directory, with worktrees living inside it, create a wrapping
   directory first. Move the bare repo into it as `.bare`, then move each worktree out to be a sibling.

2. Write the pointer file:

   ```bash
   printf 'gitdir: ./.bare\n' > .git
   ```

3. Repair the worktree links. This step is mandatory after any rename or move. Git stores absolute paths
   in both directions, so every existing worktree is broken until repaired:

   ```bash
   git -C .bare worktree repair /abs/path/to/each/worktree ...
   ```

4. Fix the remote-tracking configuration. Do this before creating any slots:

   ```bash
   git -C .bare config remote.origin.fetch '+refs/heads/*:refs/remotes/origin/*'
   git -C .bare fetch origin
   git remote set-head origin --auto
   ```

   Do not skip this. A plain bare clone maps no remote-tracking branches. Without the refspec fix,
   `wt go some-branch` on a branch pushed after the clone silently creates a new local branch cut from
   the default branch: a same-named shadow branch with none of the real history, and no warning.

5. Create the slot pool, detached so no branch is consumed:

   ```bash
   for n in 1 2 3 4 5 6; do git worktree add --detach slot-$n <default-branch>; done
   ```

6. Ensure a `main/` worktree exists, either by renaming an existing default-branch worktree (repair
   again after the rename) or by creating one:

   ```bash
   git worktree add main <default-branch>
   ```

## What to expect afterward

- `wt` manages only `main` and `slot-*` worktrees. Extra branch-named worktrees keep working:
  `wt go <branch>` still routes to them. `wt list` and LRU selection ignore them, and `wt release`
  refuses them.
- `.bare/wt.json` does not need to be seeded. It is created on first use, and missing entries are fine.
- Partial pools work. Fewer than six `slot-N` worktrees, or more, are all picked up automatically.

## Sharp edges

- A `wt list` that prints only the header row usually means broken worktree links. Run
  `git -C .bare worktree repair` with the worktree paths.
- The remote is assumed to be named `origin`. A repo whose remote has another name cannot resolve its
  default branch from the remote, though the local HEAD fallback still works.
- A repo with no remote at all works, but every reuse of an occupied slot prompts for confirmation,
  because no branch can ever have an upstream.
