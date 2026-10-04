# Classic Close via Commitizen Form

**Date:** 2026-05-22
**Status:** Design — pending implementation plan

## Context

`git zf issue close` offers three merge strategies: **Squash**, **Rebase**, and **Classic**. Squash and Rebase both route the commit message through the commitizen TUI form. Classic does not: it runs `git merge --no-ff <feature>` on base and lets git auto-generate the merge subject as `"Merge branch '<feature>'"`. With this project's verbose issue-branch naming (e.g. `1102227@feat@newsletter-ct-dazur-corse@4fb00492`) the auto-message is both ugly and useless — the branch is deleted right after the close, so the named reference rots immediately.

The fix is to drive the Classic merge commit through the same commitizen form the other two strategies use, and to update the merge mechanic to leave the commit message in the operator's hands.

## Goals

- Classic strategy produces a merge commit on the **base** branch with a commitizen-formatted message supplied by the existing `tui.commit` form, pre-filled the same way Squash and Rebase pre-fill it.
- The merge commit on base remains a real two-parent `--no-ff` merge — Classic's whole point is preserving the feature branch's full history as a side parent in the graph.
- The flow must fail fast on conflicts and roll back atomically on any failure between the merge step and a successful commit.

## Non-goals

- Adding a new strategy. Classic is being **modified**, not duplicated.
- Fast-forwarding base **through** the integration commit. Classic by definition adds a merge commit on top of base; the FF-deferred sentinel used by Rebase does not apply to the integration step. (Local base IS fast-forwarded against `origin/<base>` as a separate sync step before the merge — see Section "End-to-end flow".)
- Pushing the resulting merge commit. The close flow remains local-only.
- End-to-end huh-form tests (same scope boundary as the squash-via-form and rebase-close specs).

## End-to-end flow

```
1. Pre-flight (shared with Rebase via rebasePreflight)
   - dirty check, checkout feature, resolve featureSHA = HEAD
   - resolve remoteName, fetch origin (no-op when no remote configured)
   - compute remoteBase = "<remote>/<base>" if remote else "<base>"
   - if IsAncestor(<feature>, remoteBase) → abort with
     "feature has no commits ahead of remoteBase — already integrated?"
   - mergeDryRun(<feature>, remoteBase) → abort with file list on conflict
   Returns plan{featureOrigSHA, remoteName, remoteBase}.
2. Sync local base with origin/<base> (skipped when no remote)
   - git checkout <base>
   - git merge --ff-only <remoteBase>
   - On FF refusal (local base diverged from origin/<base>) → abort with
     "local <base> has diverged from <remoteBase>; pull --ff-only first"
     before any merge mutation happens.
3. Resolve integration target
   - baseSHA = ResolveRef("refs/heads/<base>")   (post-sync; equals
     remoteBase SHA when a remote exists)
4. Execute
   - git merge --no-ff --no-commit <feature>
     (leaves MERGE_HEAD + MERGE_MSG; nothing committed yet)
5. TUI form (pre-filled)
   - subject = "Merge <featureSHA[:7]> into <baseSHA[:7]>."
   - type / scope / authors defaulting follows the same pattern as Rebase/Squash.
6. Commit
   - client.Commit(msg, opts) — git commit -F <msgfile> [opts]
   - MERGE_HEAD is present, so git produces a two-parent merge commit on base
     with the form-supplied message.
7. Bookkeeping
   - updateStatus (store + tracker) — unchanged from today.
   - delete-branch prompt — unchanged from today. Classic uses `-d` (safe).
```

The "nothing to merge" branch does not exist: the pre-flight ancestor check rejects that case before any mutation. The "stale local base" case is rejected explicitly in step 2 — Classic refuses to merge into a base that hasn't been reconciled with `origin/<base>` rather than silently producing a commit on a diverged ref.

The base sync step (step 2) uses `merge --ff-only` and is the **only** fast-forward in the Classic flow. The integration step (step 4) deliberately uses `--no-ff` so the feature branch's full history is preserved as a side parent in the graph.

### Why `--no-ff --no-commit` instead of `--no-ff -m <msg>`

`--no-ff -m <msg>` would create the merge commit in one git call, but the message comes from the form which has not run yet. Splitting into `--no-commit` then `commit -F` lets the form drive the message and runs every commit hook (pre-commit, commit-msg, post-commit) on the real commit exactly once — no double-hook surprise like an amend-after-commit would produce.

### Why `git merge --abort` instead of `ResetHard <baseOrigSHA>` for rollback

Between step 2 and step 4, base's branch ref is unchanged — only the working tree and index hold the merge state, plus the `MERGE_HEAD`/`MERGE_MSG` files. `git merge --abort` is the dedicated cleanup primitive for exactly this state: it clears `MERGE_HEAD`/`MERGE_MSG`, restores the working tree and index, and leaves the branch ref alone. `ResetHard` would also work but requires snapshotting `baseSHA` separately and is broader than the actual mutation surface.

## Architecture

### `git/merge.go` — replace `MergeNoFF`, add `AbortMerge`

`MergeNoFF` (which checks out base and runs `git merge --no-ff <feature>` in one shot) is replaced by:

```go
// MergeNoFFNoCommit checks out baseBranch and runs `git merge --no-ff
// --no-commit <featureBranch>`. Leaves MERGE_HEAD + MERGE_MSG in place
// so the caller can drive the commit step itself (typically via the
// commitizen TUI form). Caller is responsible for `git merge --abort`
// on TUI abort or commit failure.
func (c *Client) MergeNoFFNoCommit(ctx context.Context, featureBranch, baseBranch string) error
```

And a new helper for the rollback path:

```go
// AbortMerge runs `git merge --abort`. Used after a TUI abort or
// commit failure in the Classic close flow to clear MERGE_HEAD /
// MERGE_MSG and restore the working tree. Returns the git error
// as-is so callers can decide whether to treat a no-active-merge
// failure as fatal.
func (c *Client) AbortMerge(ctx context.Context) error
```

`MergeRebase`, `MergeSquash`, `FastForwardOnly`, `ResetHard` are unchanged.

### `cmd/issue/close.go` — new orchestrator + dispatch change

Dispatch in `doMerge` changes from a direct call to `MergeNoFF` to a new orchestrator:

```go
case StrategyClassic:
    if err := doClassicClose(ctx, mc); err != nil {
        return strategy, false, err
    }
```

The orchestrator reuses `rebasePreflight` verbatim (shared with Rebase) and follows the same defer-after-merge rollback pattern:

```go
func doClassicClose(ctx context.Context, mc mergeContext) (err error) {
    plan, err := rebasePreflight(ctx, mc)
    if err != nil {
        return err
    }

    // Step 2: sync local base with origin/<base> (no-op when no remote).
    // Fails fast on diverged history so the integration commit never
    // lands on a stale base.
    if plan.remoteName != "" {
        if err := mc.client.FastForwardOnly(ctx, plan.remoteBase, mc.baseBranch); err != nil {
            return fmt.Errorf("local %s diverged from %s — `git pull --ff-only` first: %w",
                mc.baseBranch, plan.remoteBase, err)
        }
    } else {
        if err := mc.client.Checkout(ctx, mc.baseBranch); err != nil {
            return fmt.Errorf("checkout %s: %w", mc.baseBranch, err)
        }
    }

    // Step 3: resolve the integration target SHA for the prefill subject.
    baseSHA, err := mc.client.ResolveRef("refs/heads/" + mc.baseBranch)
    if err != nil {
        return fmt.Errorf("resolve %s: %w", mc.baseBranch, err)
    }

    // Step 4: stage the merge without committing.
    if err := mc.client.MergeNoFFNoCommit(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
        return fmt.Errorf("merge --no-ff --no-commit: %w", err)
    }

    defer func() {
        if err == nil {
            return
        }
        if abErr := mc.client.AbortMerge(ctx); abErr != nil {
            err = fmt.Errorf("merge --abort after %w failed: %v", err, abErr)
        }
    }()

    // Steps 5 + 6: TUI form (pre-filled) → commit.
    hint := commitpkg.IssueHint{
        IssueID:    mc.pickedBranch.IssueSlug,
        BranchType: mc.pickedBranch.Type,
    }
    prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
    prefill["subject"] = fmt.Sprintf("Merge %s into %s.",
        plan.featureOrigSHA.String()[:shortSHALen], baseSHA.String()[:shortSHALen])

    authors, authorsErr := mc.client.Authors()
    if authorsErr != nil {
        slog.Warn("could not load author list", "error", authorsErr)
        authors = []string{}
    }

    defaults := tui.CommitOption{Authors: authors}
    if len(authors) > 0 {
        defaults.Author = authors[0]
    }

    msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
    if err != nil {
        return fmt.Errorf("fill commit form: %w", err)
    }

    if err := mc.client.Commit(ctx, msg, git.CommitOptions{
        All:        opts.All,
        Amend:      opts.Amend,
        NoVerify:   opts.NoVerify,
        Signoff:    opts.Signoff,
        AllowEmpty: opts.AllowEmpty,
        Author:     opts.Author,
    }); err != nil {
        return fmt.Errorf("commit: %w", err)
    }

    return nil
}
```

No Classic-specific preflight is introduced. `rebasePreflight` already provides everything Classic needs: dirty check, feature checkout + SHA, remote detection, fetch (or skip when no remote), `remoteBase` computation, ancestor check, and dry-run. Classic does not consume `plan.featureOrigSHA` for rollback (it uses `AbortMerge`); only the prefill subject uses it.

`doDeleteBranch` is unchanged: Classic still uses `-d` (safe delete), which works because base now contains feature's commits as ancestors of the new merge commit.

### `tui/issue.go` — strategy hint updated

```go
{Value: string(StrategyClassic), Label: "Classic",
 Hint: "git merge --no-ff with commitizen message — preserves full history"},
```

Label stays `Classic`. The hint now reflects the commitizen-driven message.

### Shared helpers (no changes)

- `rebasePreflight` — reused verbatim by both `doRebaseClose` and `doClassicClose`. Returns the same `rebasePlan{featureOrigSHA, remoteName, remoteBase}` shape. Classic ignores `featureOrigSHA`'s rollback role (uses `AbortMerge` instead) and consumes it only for the prefill subject.
- `mergeDryRun` — already exists; reused for the feature-vs-remoteBase dry-run inside `rebasePreflight`.
- `FastForwardOnly` — already exists; reused for the new step 2 sync (local base → remoteBase).
- `IsDirty`, `IsAncestor`, `ResolveRef`, `Authors`, `Commit`, `Checkout`, `Fetch`, `Remote` — all unchanged.

## Error handling

| Failure point | Behavior |
|---|---|
| Working tree dirty (preflight) | Abort before any mutation. No rollback needed. |
| `Fetch` fails (preflight) | Abort. No mutation happened. |
| `ResolveRef`, `Remote`, or `Checkout(feature)` fails (preflight) | Abort. No mutation happened. |
| `IsAncestor` reports already-integrated | Abort with `"%q has no commits ahead of remoteBase — already integrated?"`. |
| `mergeDryRun` reports conflicts | Print conflicting files, abort. No mutation happened. |
| `FastForwardOnly(remoteBase → base)` fails (local base diverged from `origin/<base>`) | Abort with `"local <base> has diverged from <remoteBase> — pull --ff-only first"`. No `MERGE_HEAD` created. No rollback needed. |
| `Checkout(base)` fails in the no-remote path (e.g. base checked out in another worktree) | Abort. No `MERGE_HEAD` created. No rollback needed. |
| `ResolveRef("refs/heads/<base>")` fails after sync | Abort. No `MERGE_HEAD` created. No rollback needed. |
| `MergeNoFFNoCommit` fails partway | Wrapped error propagates; `defer` runs `git merge --abort` to clear state. |
| `FillOutForm` returns `huh.ErrUserAborted` | `defer` runs `git merge --abort`. Operator is on base, pre-merge state. |
| `client.Commit` fails (hook rejection, signing failure, etc.) | `defer` runs `git merge --abort`. Hook stderr surfaces through the wrapped error. |
| `AbortMerge` itself fails during rollback | Wrap both the original error and the abort error so the user knows the repo may be in a half-merged state and what triggered it. |

The `defer` uses a named return value and a closure so the rollback observes the actual `err` at function exit, not at the moment the `defer` was declared — same pattern as `doRebaseClose`.

The defer is registered only **after** `MergeNoFFNoCommit` returns successfully. All failures in steps 1–3 (preflight, base sync, baseSHA resolve) happen before `MERGE_HEAD` could be created, so there is nothing to abort. This matches `doRebaseClose`'s defer placement.

## Edge cases

- **Base is checked out in another worktree.** Step 2's `git checkout <base>` (via `FastForwardOnly` or the no-remote fallback) refuses with git's native error. Surfaces to the operator before any mutation.
- **Local base is behind `origin/<base>` (FF-able).** Step 2 fast-forwards local base to `origin/<base>` before the merge. Operator gets a fresh integration base automatically.
- **Local base has diverged from `origin/<base>`.** Step 2's `merge --ff-only` refuses. Classic aborts with a remediation message pointing at `git pull --ff-only` on base before retrying close. No `MERGE_HEAD` is created.
- **No remote configured.** Step 2's sync is skipped — Classic merges into local base directly. The dry-run in step 1 still ran against local base via the no-remote branch of `rebasePreflight`.
- **Submodule pointers.** Classic uses a real `git merge` (not `--squash`), which handles gitlinks correctly. Unchanged from today.
- **Pre-commit hook rejects the commit.** `defer` runs `git merge --abort`. Hook's stderr surfaces via the wrapped error. Operator is on base, pre-merge.
- **Commit-msg hook rewrites the message.** Falls through normally — `git commit -F` honors the standard hook chain.
- **Untracked file would be overwritten by checkout/merge.** Git refuses the step before any mutation with `"untracked working tree files would be overwritten by …"`. Native error surfaces.
- **Repo is inside a `git worktree`.** All commands use `git -C <root>` already; `git merge --abort` is worktree-safe. No special handling required.
- **`MergeNoFFNoCommit` produces a fast-forward** (would happen if base is an ancestor of feature with no divergence). `--no-ff` forces a merge commit anyway. The two parents are `baseSHA` and `featureSHA`; history is preserved.

## Testing

### Unit tests (`git/merge_test.go`)

- `TestMergeNoFFNoCommit_clean` — base + feature diverged. Call `MergeNoFFNoCommit`. Assert:
  - HEAD on baseBranch.
  - `<gitdir>/MERGE_HEAD` exists.
  - `git status --porcelain` shows the merged changes staged.
  - No new commit yet on base (`base` ref unchanged).
- `TestMergeNoFFNoCommit_conflict` — divergent edits to the same file. Call `MergeNoFFNoCommit`. Assert it returns an error and that the working tree contains conflict markers.
- `TestMergeNoFFNoCommit_ffOnly` — base is ancestor of feature (no divergence). Call `MergeNoFFNoCommit`. Assert `MERGE_HEAD` exists (because `--no-ff` forces a merge commit) and the resulting staged state is correct.
- `TestAbortMerge_active` — start a merge with `MergeNoFFNoCommit`, call `AbortMerge`, assert `MERGE_HEAD` is gone, working tree clean, HEAD unchanged on base.
- `TestAbortMerge_noActiveMerge` — call `AbortMerge` when no merge is in progress, assert the documented behavior (error returned but non-fatal in the orchestrator).

Delete `TestMergeNoFF` (function being replaced).

### TUI tests

- No changes; the strategy picker test already exercises the option list shape. The hint string change is cosmetic.

### `cmd/issue/close.go`

- No automated end-to-end (huh forms remain un-mocked, same scope boundary as Rebase/Squash).
- The `defer`-rollback logic is covered by manual end-to-end.

### Manual end-to-end

```bash
mise exec -- go test ./...
mise exec -- go build -o ./bin/git-zf .
make install

# In a repo where origin/<base> has advanced since the feature branched
# (so the close produces a real merge commit), on an in-progress feature:
git zf issue close

# Scenarios to verify:
# 1. Clean Classic close (remote in sync)
#    - Local base already at origin/<base>. Pick Classic, confirm.
#    - Step 2 sync is a no-op FF. Form opens pre-filled with
#      "Merge <feat-sha[:7]> into <base-sha[:7]>." Submit.
#    - Two-parent merge commit lands on local base with form message.
#    - Feature branch unchanged; delete-branch prompt fires.
# 2. Clean Classic close (local base behind origin)
#    - Local base lags origin/<base> by N commits (FF-able).
#    - Step 2 fast-forwards local base to origin/<base>. Continue as scenario 1.
# 3. Local base diverged from origin/<base>
#    - Commit on local base that doesn't exist on origin.
#    - Step 2 FF refuses; Classic aborts with the pull --ff-only message.
#    - No MERGE_HEAD, no rollback, base unchanged.
# 4. No-remote repo
#    - Repo with zero remotes. Pick Classic.
#    - Step 2 sync skipped; checkout base directly; merge proceeds.
# 5. TUI abort
#    - Pick Classic, confirm. In the commit form, press Esc.
#    - `git merge --abort` runs; HEAD on base; working tree clean.
# 6. Pre-commit hook rejection
#    - Same as 1 with a failing pre-commit hook.
#    - `git merge --abort` runs; hook error visible; base clean.
# 7. Already-integrated abort
#    - Feature has 0 commits ahead of origin/<base>.
#    - Pre-flight aborts before any mutation.
# 8. Dirty working tree
#    - Pre-flight aborts before any mutation.
# 9. Conflict on dry-run
#    - Divergent file edits between feature and origin/<base>.
#    - Pre-flight prints conflict list and aborts.
# 10. --no-ff with no divergence
#    - Feature is just ahead of base (no commits on origin/<base> since branch).
#    - Classic still produces a merge commit (not a FF) because of --no-ff.
```

## Rejected alternatives

### Shape B — merge commit on feature, base fast-forwards

Original sketch in this brainstorming. Mechanic: `checkout feature; git merge --no-ff --no-commit remoteBase; form; commit; FF base`. Rejected because:
- Backwards parent ordering: the resulting merge commit has `[feature, remoteBase]` as parents, semantically reading "feature merged remoteBase into itself" — opposite of the gitflow convention "main merged feature".
- Requires an extra step (`FastForwardOnly`) and an extra error sentinel (`errFastForwardDeferred`) to handle the post-commit FF.
- Introduces an awkward "nothing to merge" sub-case requiring `--allow-empty` to keep the commit shape consistent.

Shape A (merge on base) matches conventional gitflow and removes all three of those.

### Amend-after-commit (`--no-ff -m <placeholder>` + `commit --amend -F`)

Single git call to merge, then rewrite the message after the form. Rejected because every commit hook (pre-commit, commit-msg, post-commit) would run twice with different inputs — surprising for setups with signing or message-validation hooks.

### Fold Classic into a parametrized Rebase

`MergeRebase(squash bool)` with `squash=false` skipping the soft reset. Rejected because the resulting git shapes are distinct enough that a single API obscures intent more than it shares code. The strategy picker would still need two entries.

### Merge feature into local base without fetching first

Original draft of this spec. Matched today's `MergeNoFF` semantics: no fetch, no sync, merge directly into whatever local base happens to be. Rejected because today's `MergeNoFF` is the broken thing being fixed — preserving its "operate on stale local base" behavior reintroduces a silent failure mode where the close commit lands on a base ref that has nothing to do with the actual remote head. The base-sync step plus hard refusal on diverged history closes that gap without complicating the operator workflow.

### Merge feature directly into `origin/<base>` (detached HEAD)

Checkout `origin/<base>`, merge, commit, then move local base ref to the new commit. Avoids any local-base-staleness concern entirely. Rejected as a surprising mechanic — operators expect their local base branch to be visibly checked out throughout a close, and using a detached HEAD for the merge would also complicate hook environments that key off the branch name.

## Documentation updates

Files to update as part of implementation, with the specific edits required:

### `README.md`

- **Line ~113** (close-flow narrative): currently reads "For Rebase and Squash, the final commit is composed through the commitizen TUI form, pre-filled from the branch's issue ID and type." Add Classic to this list — all three strategies now use the form.
- **Line ~124** (strategy comparison table): the "Classic" row's mechanism column says `git merge --no-ff`. Update to mention the form-driven message and the pre-merge FF-sync, e.g. `git merge --no-ff --no-commit` + commitizen form (FF-sync of local base against origin/<base> before merge).
- **Line ~197** (when-to-choose-each-strategy): the Classic description focuses on history preservation. Add that the merge commit message now goes through the commitizen form (same UX as Rebase/Squash) and that local base is FF-synced against `origin/<base>` first — Classic refuses to merge into a base that has diverged from origin.
- A new sub-section parallel to the existing "Rebase strategy — detailed flow" sub-section, titled **"Classic strategy — detailed flow"**, walking through the seven-step flow defined here (pre-flight, sync, resolve baseSHA, merge --no-ff --no-commit, form, commit, bookkeeping). Mirror the existing Rebase prose style.

### `CLAUDE.md`

No edits expected. CLAUDE.md describes build/test/configuration conventions and does not document the merge strategies. Implementation should confirm by `grep`.

### Inline godoc / package docs

- `git/merge.go`: remove `MergeNoFF`'s godoc when the function is deleted. Add godoc for `MergeNoFFNoCommit` and `AbortMerge` per Section "Architecture".
- `cmd/issue/close.go`: `doClassicClose` godoc summarising the seven steps (preflight, sync, resolve, merge, form, commit, return).
- `tui/issue.go`: the picker hint string itself is the user-facing description; no separate godoc update needed.

### Other docs

No CHANGELOG / release notes file is present in the repo; nothing to update there. Anything under `docs/superpowers/specs/` is design history only — no edits to prior specs.

## Out of scope (flagged for follow-up)

- Automated end-to-end tests of the close flow (needs a huh-form mock harness — separate work, same boundary as the squash-via-form and rebase-close specs).
- Auto-syncing local base with `origin/<base>` before the Classic merge. A `--sync-base` flag could be added later without disturbing this design.
- Pushing the resulting base branch with `--force-with-lease` or otherwise. The close flow remains local-only; pushing is the operator's responsibility.
