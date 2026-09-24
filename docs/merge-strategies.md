# Merge strategies — design notes

`git zf issue close` and `git zf branch merge` share one merge engine
(`cmd/mergeflow`). This document describes how each strategy works under the
hood and what happens on failure. For day-to-day usage see the
[README](../README.md#merge-strategies).

| Strategy | Mechanism | History on base | Submodule-safe |
|---|---|---|---|
| **Rebase** *(default)* | Real `git merge <remote>/<base>` + `git reset --soft <remote>/<base>` | one clean commit | ✅ yes |
| **Squash** | `git merge --squash` | one commit, no merge parent | ⚠️ no — `--squash` is known to mishandle submodule gitlinks |
| **Classic** | `git merge --no-ff --no-commit` + commitizen form (FF-syncs local base against `origin/<base>` first) | merge commit + full feature history | ✅ yes |

## Shared pre-flight

Every strategy starts the same way:

1. `git status --porcelain --untracked-files=no` — abort if the working tree
   has tracked modifications or staged changes. Untracked files are ignored on
   purpose: they survive rollback and do not put work at risk.
2. Checkout the feature branch (idempotent, no `post-checkout` hook fires when
   already there) and capture its tip SHA for rollback.
3. `git fetch <remote>` to pick up the latest remote base. No-op when no remote
   is configured (see remote auto-detection in the README); in that case
   `<remote>/<base>` below means the local `<base>` branch.
4. `git merge-base --is-ancestor feature <remote>/<base>` — abort cleanly if the
   feature has no commits ahead of the base ("already integrated?"). This avoids
   producing an empty commit.
5. `git merge-tree` dry-run of feature vs `<remote>/<base>` — abort with the
   conflict file list if the endpoint cannot merge cleanly. Nothing has been
   touched at this point.

## Rebase

Rebase produces the same end state as Squash (one clean commit on the local
base branch) but uses a mechanic that handles submodule pointers correctly. The
work happens on the feature branch, then fast-forwards onto local base.

```
1. Execute
   - `git merge --no-edit <remote>/<base>` — a *real* merge, which resolves
     submodule gitlinks three-way like any other file. `--no-edit` suppresses
     $EDITOR for the transient merge-commit message.
   - `git reset --soft <remote>/<base>` — collapse the merge into one staged
     diff. HEAD moves back, the working tree stays at the merged state, the
     index is fully staged.

2. TUI commit form
   - The commitizen form opens pre-filled with type, scope, and the subject
     `Squashed close of <feature-tip> into <base-tip>.`
   - Submit → `git commit` lands one clean commit on the feature branch.
   - Esc / Ctrl+C / hook rejection → atomic rollback (see below).

3. Deploy
   - Checkout local `<base>` (idempotent).
   - `git merge --ff-only feature` to land the new commit.

4. Bookkeeping
   - Update the local store and (if configured) the tracker; offer to remove
     the worktree and delete the feature branch.
```

When the feature branch is checked out in a linked worktree, steps 1 and 2 run
in that worktree and step 3 runs in the main checkout.

### Why a real `git merge` instead of `git rebase`

A `git rebase` would replay the feature's commits one at a time. If commit #2
conflicts but commit #4 fixes it, the merge-tree dry-run reports the endpoint
as clean, yet the rebase still halts on commit #2 and leaves a detached HEAD
with conflict markers. A real merge looks at the *endpoint* of both branches,
which is exactly what merge-tree predicts, so the dry-run's "clean" verdict is
a guarantee.

Submodules work because the gitlink quirk only affects `merge --squash`. Plain
`git merge` resolves submodule pointers three-way like any other file.

### Rollback semantics

The orchestrator captures the feature ref's SHA *before* mutating anything.
From that point until the final commit lands, any failure — TUI abort,
pre-commit hook rejection, commit-msg hook rejection, signing failure — runs
`git reset --hard <featureOrigSHA>` on the feature branch. The feature is
restored atomically and `Rolled back: feature branch "<name>" restored to
<sha>` is printed on stderr. If the rollback itself fails, both the original
error and the rollback error are surfaced so the operator knows the repo is in
a half-state and why.

The *post-commit* failure mode is different. If the commit landed on the
feature branch but `git merge --ff-only` refuses (local `<base>` has diverged
from the remote), the commit is **not** rolled back — it already exists as a
clean, valid commit on the feature branch. Instead:

```
Commit created on "<feature>" but local <base> has diverged from <remote>/<base>.
Run `git pull --ff-only` on <base>, then `git merge --ff-only <feature>` to land it.
```

The store and tracker are not updated, the delete-branch prompt is skipped,
and the close exits cleanly. The operator reconciles local base by hand and
fast-forwards the feature commit.

## Squash

After the shared pre-flight: checkout local `<base>`, best-effort
`git merge --ff-only <remote>/<base>` so the squash commit lands on the current
remote tip, then `git merge --squash <feature>`, open the commitizen form with
the same prefill as Rebase, and commit. Squash always runs in the main
checkout.

On a TUI abort or hook rejection the staged squash diff is left on `<base>`
for inspection (`git reset --hard` discards it). The exception is a source
branch that was materialized from `origin/<branch>` for the merge: there the
engine resets `<base>` to its captured tip itself, so the clone is left as it
was found.

## Classic

1. **Sync local base** (skipped when no remote): checkout local `<base>` and
   run `git merge --ff-only <remote>/<base>`. Refuses with a remediation
   message if local base has diverged from the remote — run `git pull
   --ff-only` and retry.
2. **Stage the merge**: `git merge --no-ff --no-commit <feature>`. `MERGE_HEAD`
   and `MERGE_MSG` are left in place; nothing is committed yet.
3. **TUI form**, pre-filled with the subject `Merge <feature-tip-short> into
   <base-tip-short>.` and the same type / scope / authors defaults as the
   other strategies.
4. **Commit**: `git commit -F <msgfile>` plus the form options. Because
   `MERGE_HEAD` is present, git produces a two-parent merge commit on base.
5. **Bookkeeping**: same as Rebase. Classic uses safe delete (`git branch -d`)
   since the feature is now an ancestor of base; Rebase and Squash need
   `-D` because neither preserves ancestry.

If any step between 2 and a successful commit fails, `git merge --abort` runs
automatically to clear `MERGE_HEAD` / `MERGE_MSG` and restore the working tree.
The operator is left on base in a clean state. Classic always runs in the main
checkout.

## Reviewer commits

When the issue has a decided review (`approved` or `changes_requested`) and
`<IssueID>@review` carries commits not yet in the feature branch, `issue close`
incorporates them before the strategy runs: a fast-forward when the feature
branch has not moved, otherwise an automatic merge. If that merge conflicts the
close refuses with a hint to run `git zf review sync`, resolve, and retry —
close never leaves a merge in progress.
