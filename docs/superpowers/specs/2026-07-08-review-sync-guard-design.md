# Review sync guard — incorporate reviewer commits before new work lands

**Date:** 2026-07-08
**Status:** validated with user (brainstorming session)

## Problem

Failure scenario observed with the peer-to-peer review flow:

1. Developer D runs `review request` on `42@feat@title` (round 1, branch locked).
2. Reviewer R does an *active* review: `review start` creates `42@review`, R
   pushes fix commits to it, then rejects (`review reject`) — the branch is
   kept because it carries commits.
3. D fetches, sees the unlock, and keeps developing on `42@feat@title`,
   forgetting the commits sitting on `origin/42@review`. The branches diverge.
4. The reviewer is gone (or approves orally); the ref eventually reads
   `approved`.
5. `issue close` hits `reviewPreflight` (`cmd/issue/close.go`), which tries
   `git merge --ff-only 42@review` into the feature branch. The branches have
   diverged → ff-only fails → **close is blocked with no guided way out**.

A second hole sits on the reject path: `review request` for round N+1
(`cmd/review/request.go`, "delete any stale review branch") deletes
`42@review` local **and** remote without checking whether its commits were
ever incorporated — silent loss of the reviewer's work.

## Root cause and invariant

Reviewer commits are incorporated *lazily* (at close), long after the context
is gone. The fix enforces one invariant, early:

> Once a reviewer decision exists (`approved` or `changes_requested`), the
> reviewer's commits on `<slug>@review` must be contained in the feature
> branch before any new commit lands on it.

## Decisions (user-validated)

| Question | Decision |
|---|---|
| Scope | All three: commit-time guard, close-time recovery, request round-N+1 safety |
| Enforcement point | `git zf commit` flow **and** a pre-commit hook installed by `git zf init` |
| Guard UX in `git zf commit` | Offer inline merge (confirm prompt); decline → abort with sync hint |
| Command shape | Extend `review sync` (no new subcommand) |
| Merge conflicts on the review-branch merge | Leave the merge in progress (standard git conflict markers), do **not** abort |

## Design

### Shared detection helper

New helper in `cmd/review` (e.g. `pending.go`), used by every enforcement
point:

```
pendingReviewCommits(ctx, client, slug, featureBranch)
    → (effectiveRef string, n int, ok bool)
```

- Resolves the *effective review ref*: local `<slug>@review` if it exists,
  else `origin/<slug>@review` (remote-tracking), else not-ok. When **both**
  exist and the remote-tracking ref has commits not in the local branch (the
  reviewer pushed or force-pushed after the developer's checkout), the
  remote-tracking ref wins — the reviewer's copy is authoritative. A local
  branch ahead of the remote (the reviewer's own machine) keeps winning.
  Still offline: this compares two already-fetched local refs.
- Reads the **local** copy of `refs/zf/reviews/<slug>`. The guard is armed
  only when the ref exists and its status is `approved` or
  `changes_requested`:
  - `in_review` → reviewer hasn't decided; nothing to incorporate yet.
  - No ref (e.g. stale `origin/<slug>@review` left after a close) → pass.
- `n` = `CommitsAhead(effectiveRef, featureBranch)`; the guard trips when
  `n > 0`.
- **No network calls.** It only reads refs already fetched — cheap enough for
  a hook, works offline, and matches the scenario (D fetched and continued).

Universal exemptions (guard passes silently — fail-open philosophy, same as
the pre-push `guard`):

- current branch ends in `@review` (the reviewer's own commits),
- a merge is in progress (`MERGE_HEAD` exists) — concluding a merge is
  exactly how incorporation happens; without this exemption the
  resolve-conflicts-then-commit path would deadlock,
- detached HEAD, branch not tracked in the store, store unreadable, ref
  unreadable.

### Enforcement point 1 — `git zf commit` guard

At the start of the commit flow (before the TUI form): run the helper on the
current branch.

- Guard trips → confirm prompt:
  `"42@review has N reviewer commit(s) not in your branch — merge now?"`
  - **Accept** → run the review-branch merge (step 1 of sync, below), then
    continue into the commit form.
  - **Decline** → abort with: run `git zf review sync` (or bypass with
    `git zf commit --no-verify`).
- `git zf commit --no-verify` (existing flag) skips this guard too — same
  intent as bypassing the hook.
- Note on dirty state: `git merge` refuses to run when staged/overlapping
  local changes exist. Do **not** detect this by parsing git's error output —
  the wording varies across git versions and locales. Instead, pre-flight
  before attempting the merge with `git status --porcelain
  --untracked-files=no` (the exact pattern the Rebase close strategy already
  uses): if the tree is dirty, skip the merge entirely and abort the commit
  with the precise hint — `git stash`, then `git zf review sync`, then
  `git stash pop`, then retry the commit. A merge failure *after* a clean
  pre-flight is genuinely unexpected and is surfaced raw. No stash
  automation — predictable over magical.
- The prompt goes through a prompter interface so E2E tests can script it
  (same pattern as the close/start/prune flows).

### Enforcement point 2 — pre-commit hook (`git zf init`)

- `git zf init` now installs **two** hooks: the existing `pre-push` and a new
  `pre-commit` that calls a hidden subcommand `git zf review guard-commit`
  (sibling of `review guard`).
- `guard-commit` takes no args, resolves HEAD itself, runs the shared helper,
  and exits non-zero with the sync hint when the guard trips. All exemptions
  above apply. Bypass: `git commit --no-verify`.
- Hook management policy is identical per hook and mirrors today's pre-push
  logic: byte-identical → "already up to date"; foreign hook → never
  overwrite, print the snippet to add manually; missing → write it. Init
  reports per-hook status.
- This catches plain `git commit`; `git zf commit` also runs hooks, so the
  hook is the backstop and the TUI guard is the friendly front door.

### Enforcement point 3 — `review sync` extended

`review sync` drops its "sub-tasks only" restriction and becomes "bring my
feature branch up to date":

- **Candidates** (picker, current branch pre-selected): in-progress branches
  that have a parent issue **or** pending review commits.
- **Fetch first**: `FetchReviewRefs` + `git fetch <remote>` (as `review
  start` does) so `origin/<slug>@review` and `origin/<parent>` are current.
- **Step 1 — review-branch merge** (when the guard trips): merge the
  effective review ref into the feature branch using a new primitive that
  does *not* abort on conflict:
  - clean → merge commit created, continue;
  - conflicts → leave the merge in progress and print: resolve the conflict
    markers, then `git zf commit` to conclude the merge. Stop (skip step 2).
- **Step 2 — parent merge** (when the issue is a sub-task): unchanged
  behavior, including abort-on-conflict.
- Neither branch nor no pending review commits → "Nothing to sync."
- Sync does **not** delete `<slug>@review` or the review ref after a
  successful merge. Cleanup stays where it is today: at close (approved
  path) or at the next `review request` — which becomes safe, because after
  incorporation `CommitsAhead == 0`.

### Enforcement point 4 — close-time recovery

`reviewPreflight` (approved status, review commits exist) stops try-and-catch
on ff-only. It computes both directions:

- `N = CommitsAhead(effectiveReview, feature)` — reviewer commits pending.
- `M = CommitsAhead(feature, effectiveReview)` — developer commits since.

Then:

- `N == 0` → nothing to incorporate (today's behavior).
- `N > 0, M == 0` → fast-forward (today's behavior).
- `N > 0, M > 0` (diverged) → `git merge-tree` dry-run:
  - clean → real merge automatically, close proceeds (the merge commit is
    harmless: Rebase/Squash strategies collapse it; Classic keeps history
    anyway);
  - conflicts → **refuse close** with the conflicting-file list and the hint
    "run `git zf review sync`, resolve conflicts, then close". Close never
    leaves `MERGE_HEAD` behind.

### Request round-N+1 safety

In `runReviewRequest`, before deleting a stale `<slug>@review`: run the
helper. If it still has unincorporated commits:

- interactive path: offer the same "merge now?" confirm; accepted + clean →
  merge, then proceed with the request; declined or merge conflicted →
  refuse the request with the sync hint;
- the refusal also lives in `runReviewRequest` itself (not just the
  interactive wrapper) as the safety net.

Deleting proceeds as today only when the commits are contained.

### New `git.Client` primitives (`git/merge.go`)

- `MergeLeaveConflicts(ctx, source, target)` — like `MergeForward` but on
  conflict returns a typed `ErrMergeConflicts` and leaves the merge in
  progress instead of aborting. Failure classification never parses git's
  message text: non-zero exit + `MERGE_HEAD` present → `ErrMergeConflicts`;
  non-zero exit without `MERGE_HEAD` → raw error passed through.
- `MergeInProgress()` — reports whether `MERGE_HEAD` exists.
- The dirty-tree pre-flight already exists: `IsDirty(ctx)` (`git/git.go:175`)
  wraps `git status --porcelain --untracked-files=no`. The commit guard,
  `review sync`, and the request offer call it to detect the dirty-tree case
  *before* attempting a merge. No new primitive needed.

### Documentation (same change, not optional)

- **README — Review section**: new sync semantics (any branch, review-branch
  merge, conflict behavior), request refusal, `guard-commit` mention.
- **README — Init section**: init installs two hooks; per-hook policy.
- **README — Commit section**: the guard prompt and its bypass.

## Error handling summary

| Situation | Behavior |
|---|---|
| Store/ref unreadable, untracked branch, detached HEAD | Guard passes (fail-open) |
| `@review` branch | Guard passes |
| Merge in progress | Guard passes (deadlock prevention) |
| Dirty tree detected by pre-flight (`WorkingTreeDirty`) | No merge attempted; abort with the exact `git stash` → `git zf review sync` → `git stash pop` hint |
| Merge fails after a clean pre-flight (non-conflict) | Unexpected — raw git error surfaced, no hint |
| Review-branch merge conflicts (sync) | Merge left in progress + resolve-then-`git zf commit` hint |
| Parent merge conflicts (sync) | Abort merge, report (unchanged) |
| Close with diverged, conflicting review branch | Refuse close, repo left clean, sync hint |
| `git commit --no-verify` | Bypasses the hook (documented) |

## Testing

E2E-first, mirroring the existing patterns (real on-disk repo, seeded store,
scripted prompters). Every assertion in a named `t.Run`.

1. **Sync E2E** (`cmd/review/sync_e2e_test.go`): clean review merge
   incorporates commits; conflicting merge leaves `MERGE_HEAD` and prints the
   resolve hint; `in_review` status does not merge; sub-task runs review
   merge then parent merge; conflicted step 1 skips step 2; "nothing to
   sync".
2. **`guard-commit`**: trips on `approved`/`changes_requested` with
   unincorporated commits; passes on `in_review`, no ref, commits contained,
   untracked branch, `@review` branch, merge in progress, missing store.
3. **Commit-flow guard E2E** (`cmd/commit`): accept → merge runs, form
   continues; decline → abort with sync hint; dirty tree → pre-flight catches
   it, no merge attempted, stash → sync → pop hint printed.
4. **Close E2E** (extend `close_e2e_test.go`): ff-able unchanged;
   diverged-clean auto-merges and closes (branch + ref cleaned up);
   diverged-conflicting refuses and leaves the repo clean.
5. **Request E2E**: round-N+1 with unincorporated commits → offer; decline →
   refused and branch **not** deleted; after incorporation → stale delete
   proceeds.
6. **Init**: writes both hooks; idempotent; foreign pre-commit preserved.

## Out of scope

- Stash automation around dirty-tree merges.
- Changing the parent-merge conflict behavior (stays abort-on-conflict).
- Any push behavior changes (pushflow untouched).
- Blocking commits during `in_review` (only push is locked, as today).
