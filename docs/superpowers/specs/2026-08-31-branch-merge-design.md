# Design: `git zf branch merge` + shared `mergeflow` engine

**Date:** 2026-08-31
**Status:** draft

## Overview

`git zf branch merge` is a wired-but-empty stub (`cmd/branch/branch.go:mergeRunE`
→ "Not yet implemented."). Meanwhile `issue close` already contains a complete,
three-strategy merge implementation (Classic / Squash / Rebase) with dry-run,
confirm, rollback, and post-merge push — but it is entangled with issue-specific
concerns (store, tracker, parent-slug base resolution, review incorporation).

This spec does two things:

1. **Extracts** the generic merge orchestration out of `cmd/issue/close.go` into
   a new `cmd/mergeflow` package — a reusable engine that takes
   `(source, target, strategy)` and knows nothing about issues, stores, or
   trackers.
2. **Implements** `branch merge` as a thin driver over that engine: pick a
   **non-issue** source branch (local **or** remote-only, materializing the
   latter), merge it **into the current branch** (`git merge <branch>` mental
   model), then offer delete-source / propose-push. Issue branches are **refused**
   with a redirect to `issue close` — see Part 3 — so `branch merge` can never
   bypass the issue-close safety steps (review incorporation, sub-task guard,
   tracker update).

A minimal third change improves the `issue close` empty-state so it points a user
standing on a non-issue branch toward `branch merge`.

`issue close` behaviour is otherwise unchanged. Its existing E2E suite
(`cmd/issue/close_e2e_test.go`) is the regression net for the extraction and must
stay green **unmodified** except for one added empty-state case.

### GitNexus impact check

Ran `impact(doMerge, upstream)` and `impact(runClose, upstream)` — both **LOW /
exact**. `doMerge` and the strategy executors it dispatches are private to the
close flow (`doMerge` → only `runClose` → `closeRunE` → `runE`); nothing outside
`cmd/issue` references them. The extraction's entire blast radius is the close
flow itself.

## What this does NOT change

- The three merge strategies or their git mechanics (`git/merge.go` is untouched).
- `issue close`'s issue-specific pre-steps: materialize ref-derived branch,
  `reviewPreflight`, `resolveDefaultBase` / `chooseMergeTarget`, children-all-merged
  guard.
- `issue close`'s issue-specific post-steps: `trackPickedCandidate`,
  `updateClosedStatus` (store + tracker), branch-ref stamping.
- The close commit-message content: the issue-flavored prefill
  (`IssueHint{...}.Prefill`) moves verbatim into close's `PrefillFunc`.
- The close picker path (`getPickedBranch` → `CloseCandidates`): only the *empty*
  branch is changed.
- The `pushflow` / `pushConfirm` machinery.

## Part 1 — The `cmd/mergeflow` engine

New package `cmd/mergeflow`, paralleling `cmd/issueflow` / `pushflow`. It owns the
generic middle of today's `doMerge`:
`dry-run conflict check → pick strategy → confirm → execute (classic/squash/rebase)`.

Delete-source and propose-push are **not** in the engine — they are post-steps
each caller runs under its own conditions. The engine's single responsibility is
"produce the merge commit that advances Target with Source's work."

### Invariant

The engine **advances `Target`**; `Source` is merged into it. This generalizes
close's feature→base orientation:

| Caller | Source | Target |
|---|---|---|
| `issue close` | picked issue branch | resolved parent base |
| `branch merge` | picked branch | current branch (HEAD) |

### Surface

```go
package mergeflow

// Params identify WHAT to merge. No store/issue knowledge.
type Params struct {
    Source string // branch/ref merged in
    Target string // branch advanced (must be checkout-able; HEAD for branch merge)

    // SourceMaterialized widens abort-rollback: when true, staged residue is
    // discarded on abort (the source branch is disposable / reproducible from
    // origin). close sets this for ref-derived picks; branch merge never does.
    SourceMaterialized bool
}

// Prompter is the generic subset of today's ClosePrompter. close's huhPrompter
// already satisfies it; branch merge ships its own implementation.
type Prompter interface {
    PickStrategy(ctx context.Context) (commit.MergeStrategy, error)
    ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (confirmed bool, err error)
    ComposeMessage(ctx context.Context, prefill map[string]any) (msg []byte, opts tui.CommitOption, err error)
}

// PrefillFunc is the ONE coupling the extraction breaks. The engine resolves the
// source and target tip hashes, then asks the caller for the commit-message
// prefill map. close returns IssueHint{...}.Prefill(...); branch merge returns a
// plain "Merge <source> into <target>" prefill.
type PrefillFunc func(s commit.MergeStrategy, sourceTip, targetTip plumbing.Hash) map[string]any

// Result reports what happened so the caller can run its own post-steps.
type Result struct {
    Strategy            commit.MergeStrategy
    Aborted             bool // user declined at ConfirmMerge
    FastForwardDeferred bool // rebase committed on Source but post-FF of Target failed
}

func Run(
    ctx context.Context,
    client *git.Client,
    p Params,
    prompter Prompter,
    prefill PrefillFunc,
) (Result, error)
```

### Function inventory (what moves from `close.go`)

Moved into `cmd/mergeflow` and generalized (`pickedBranch.BranchName`→`Source`,
`baseBranch`→`Target`, `mergeContext`→`Params`+args):

| From `cmd/issue/close.go` | To `cmd/mergeflow` |
|---|---|
| `doMerge` | `Run` (top-level orchestration) |
| `doClassicClose` | classic executor (unexported) |
| `doSquashCommit` | squash executor (unexported) |
| `doRebaseClose` | rebase executor (unexported) |
| `rebasePreflight` + `rebasePlan` | preflight helper + type |
| `composeAndCommit` | `composeAndCommit` — now takes the prefill **map**, not `*IssueCloseInfo` |
| `errFastForwardDeferred` (sentinel) | internal sentinel; surfaced as `Result.FastForwardDeferred` |

`mergeContext` is retired; its fields become `Params` + the `client` argument.
Its `store` field is not read by any executor (only `updateClosedStatus` &
friends used the store, and those stay in close), so it drops cleanly.

### How the three subtleties are preserved

1. **Issue-flavored commit message.** `composeAndCommit` today calls
   `IssueHint{IssueID, BranchType, IssueSubject, Closing}.Prefill(cfg.CommitMessage)`
   to build the prefill, then `prompter.ComposeMessage(prefill)` then
   `client.Commit(...)`. In the engine, `composeAndCommit` receives the prefill
   **map** (already built) and does the ComposeMessage→Commit tail. The map is
   produced by the caller's `PrefillFunc`. close's `PrefillFunc` closure captures
   the picked `store.BranchRow` and `cfg` and calls the same `IssueHint.Prefill`.
2. **`errFastForwardDeferred`.** Stops being an error the caller must
   `errors.Is`. The rebase executor still uses an internal sentinel; `Run`
   translates it to `Result{FastForwardDeferred: true}, nil`. `runClose` reads
   the flag instead of matching the error.
3. **`materialized` rollback.** Threaded through `Params.SourceMaterialized`;
   the squash/rebase abort-discard `defer`s read it exactly as they read
   `mc.materialized` today.

## Part 2 — `runClose` rewiring

`runClose` keeps every issue-specific step. The `doMerge(ctx, mc, prompter)` call
site becomes:

```go
prefill := func(s commit.MergeStrategy, sourceTip, targetTip plumbing.Hash) map[string]any {
    return commitpkg.IssueHint{
        IssueID:      picked.IssueSlug,
        BranchType:   picked.Type,
        IssueSubject: picked.Title,
        Closing:      &commitpkg.IssueCloseInfo{FromHash: sourceTip, ToHash: targetTip, Strategy: s},
    }.Prefill(deps.cfg.CommitMessage)
}

res, err := mergeflow.Run(ctx, deps.client,
    mergeflow.Params{Source: picked.BranchName, Target: base, SourceMaterialized: createdBranch},
    prompter, prefill)
```

The existing branches map over directly:

- `res.FastForwardDeferred` → today's `errors.Is(err, errFastForwardDeferred)`
  branch (mark `mergeCommitted`, track candidate, return nil).
- `res.Aborted` → "Aborted." + return nil.
- otherwise → `updateClosedStatus` / `doDeleteBranch` / `proposeClosePush` as now.

`ClosePrompter` keeps its full interface; the compile-time assertion
`var _ mergeflow.Prompter = (*huhPrompter)(nil)` is added so the same production
prompter drives both flows.

## Part 3 — The `branch merge` command

New files, mirroring the `close` / `prune` file shape:

- `cmd/branch/merge.go` — command + `mergeRunE` + `runMerge` orchestration.
- `cmd/branch/merge_prompter.go` — `MergePrompter` interface + `huhMergePrompter`.
- `cmd/branch/merge_prompter_test.go` — scripted prompter for tests.
- `cmd/branch/merge_e2e_test.go` — E2E rig.

`mergeCmd()` in `branch.go` gains `pushflow.AddFlags(cmd)` and a `--strategy`
convenience is **out of scope** (strategy is picked interactively, matching close).

### `runMerge` flow

```
1. Open store, build *git.Client.
2. target = client.CurrentBranch()
   - detached HEAD → error: "checkout a branch before merging into it".
3. localSet = LocalBranchNames(); remoteOnly = RemoteBranchNames() \ localSet
   sources = (localSet ∪ remoteOnly) minus target, deduped, remote-only flagged
   - empty → "No other branches to merge." and return.
4. source = prompter.PickSource(ctx, sources)   // huh select; label remote-only picks
5. REFUSE issue-branch sources (workflow guard — BEFORE any side effect):
   if parsed, err := branch.Parse(source.Name); err == nil {   // parses ⇒ git-zf issue branch
       print: `%q is an issue branch (%s). Use "git zf issue close" to merge it
               safely — that runs review incorporation, the sub-task guard, and
               the tracker update, none of which branch merge performs.`
       return   // no merge, no materialize
   }
6. mergeCommitted := false
   created := false
   if source.RemoteOnly:                       // materialize (mirrors close's ref-derived path)
       created, err = issueflow.MaterializeBranch(ctx, client, rowFor(source.Name))
   defer if created && !mergeCommitted {        // abort-rollback — same guard as runClose:194
       DeleteLocalBranchSafe(source.Name, target)   // removes the orphaned materialized branch
   }
7. res, err := mergeflow.Run(ctx, client,
       mergeflow.Params{Source: source.Name, Target: target, SourceMaterialized: created},
       prompter, plainPrefill)
8. Terminal dispatch (set mergeCommitted BEFORE returning where the branch must survive):
   err != nil:                 return err        // pre-commit failure → defer fires (rollback)
   res.FastForwardDeferred:    mergeCommitted = true   // err==nil; commit landed on source ONLY.
                               report `commit landed on %q; fast-forward %q into it manually`
                               return nil         // ← defer MUST no-op: deleting source loses the rebased commits
   res.Aborted:                "Aborted." ; return nil  // declined at confirm → defer fires (rollback)
   success:                    mergeCommitted = true    // defer no-ops
9. Post-merge (two follow-ups; a tracked issue branch can never reach here — step 5):
   a. delete source — if prompter.ConfirmDeleteSource(ctx, source.Name):
                      DeleteLocalBranch(force = squash|rebase);
                      delete remote branch if RemoteBranchExists.
   b. propose push  — pushflow.Propose on target (reads --push/--no-push +
                      cfg.Push.Propose, same as proposeClosePush).
10. "Branch %q merged into %q.\n"
```

**Step 8 mirrors `runClose` exactly** (`close.go:265-269`): `FastForwardDeferred`
means the strategy committed the rebased work onto `source` but the final
fast-forward of `target` did not land, so `mergeCommitted` is set true and the
materialized-branch rollback is suppressed — the user keeps the commits they just
resolved conflicts for and finishes the fast-forward by hand. Only a
*pre-commit* failure or an explicit decline lets the rollback delete a
materialized source.

`MaterializeBranch` takes a `store.BranchRow`; for a non-issue remote branch
`branch merge` builds a synthetic row (`BranchName: source.Name`, `IssueID: 0`) —
the helper only reads `BranchName` and resolves it via `ResolveBranchRef`'s origin
fallback, so no store entry is required.

**Import note:** the parse gate uses the domain package
`github.com/piprim/git-zf/branch`, but `cmd/branch` is itself `package branch`, so
`merge.go` imports it aliased — `branchpkg "github.com/piprim/git-zf/branch"` — and
calls `branchpkg.Parse(...)`.

`plainPrefill` is a closure built inside `runMerge` (capturing `source`/`target`,
mirroring how `runClose` builds its issue closure), returning a merge-flavored
prefill:

```go
plainPrefill := func(_ commit.MergeStrategy, _, _ plumbing.Hash) map[string]any {
    return map[string]any{"subject": fmt.Sprintf("Merge %q into %q", source, target)}
}
```

(See open question on commit-message shape.)

### `MergePrompter`

```go
// SourceBranch is one pickable merge source. RemoteOnly marks a branch that
// exists on origin but not locally, so the picker can label it (e.g.
// "feature-x (origin)") and runMerge knows to materialize it.
type SourceBranch struct {
    Name       string
    RemoteOnly bool
}

type MergePrompter interface {
    mergeflow.Prompter // PickStrategy, ConfirmMerge, ComposeMessage
    PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error)
    ConfirmDeleteSource(ctx context.Context, source string) (delete bool, err error)
}
```

`huhMergePrompter` reuses the **same** `tui` form constructors close uses for
`PickStrategy` / `ConfirmMerge` / `ComposeMessage`, plus a branch select for
`PickSource` (remote-only entries labelled) and a confirm for
`ConfirmDeleteSource`. Constructed with `(client, store, cfg)` like `huhPrompter`.

## Part 4 — `issue close` empty-state redirect

The only change to close behaviour. In `getPickedBranch`, the current empty-list
branch:

```go
if len(branches) == 0 {
    fmt.Fprintln(client.IO().Out, "No branches available to close.")
    return nil, nil
}
```

becomes: if the current branch is **not** merged into its base
(`DefaultBaseBranch` + `IsMergedInto`, both existing), print the redirect
instead:

```
You're on "<branch>", which has unmerged commits but isn't an issue branch.
To merge it:  git zf branch merge
```

Otherwise keep the existing message. No up-front gate; the non-empty picker path
is untouched. Errors from the base/merge helpers degrade to the plain message
(best-effort, never block).

## Testing

Per CLAUDE.md's E2E conventions and the global `t.Run`-per-assertion rule.

**`cmd/mergeflow` engine** (`merge_test.go`, real on-disk repo + scripted
`mergeflow.Prompter`, styled after `git/merge_test.go`):

- `TestRun_Classic` — merge commit lands on Target, Source unchanged.
- `TestRun_Squash` — single squashed commit on Target.
- `TestRun_Rebase` — Target fast-forwarded to rebased Source.
- `TestRun_ConflictAborts` — `MergeDryRun` reports conflicts → returns error,
  Target unchanged, `Result.Aborted == false`.
- `TestRun_UserDeclinesConfirm` → `Result.Aborted == true`, no commit.
- `TestRun_FastForwardDeferred` → `Result.FastForwardDeferred == true`, commit
  kept on Source.
- `TestRun_MaterializedDiscardsResidueOnAbort` — squash abort with
  `SourceMaterialized: true` leaves index/worktree clean.

**`cmd/branch` merge E2E** (`merge_e2e_test.go`, `scriptedMergePrompter`, real
repo + seeded store + fake tracker, mirroring the prune/close rigs):

- `TestRunMerge_HappyPath_Classic/Squash/Rebase` — commit lands on current;
  correct post-state.
- `TestRunMerge_DeletesSource` / `_KeepsSourceWhenDeclined`.
- `TestRunMerge_DeletesRemoteSourceBranch` — source with an origin counterpart is
  removed on both sides under the delete confirm.
- `TestRunMerge_RefusesIssueBranchSource` — a `branch.Parse`-able source
  (both a store-tracked one and a **remote-only** issue branch) is refused: the
  redirect to `issue close` is printed, **no merge and no materialize happen**,
  and the store/clone are untouched.
- `TestRunMerge_ProposesPush`.
- `TestRunMerge_RemoteOnlySource_Materializes` — a (non-issue) branch only on
  origin is offered, materialized, and merged into current.
- `TestRunMerge_RemoteOnlySource_AbortRollsBackMaterialized` — decline/pre-commit
  failure after materialize leaves no orphaned local branch (clone unchanged).
- `TestRunMerge_RemoteOnlyRebase_FFDeferred_KeepsMaterialized` — Rebase on a
  materialized remote-only source, FF of target deferred: the materialized branch
  **survives** with its rebased commits, and the "fast-forward manually" message
  is printed (guards the data-loss edge case).
- `TestRunMerge_DetachedHead_Errors`.
- `TestRunMerge_NoOtherBranches`.
- `TestRunMerge_AbortAtConfirm`.

**`cmd/issue` close** — suite stays green unmodified as the extraction net; add
`TestClose_EmptyState_RedirectsToBranchMerge` (on a non-issue branch with
unmerged commits, asserts the redirect line) and keep a case for the plain
"No branches available to close." message.

## Documentation updates

- `CLAUDE.md`: add a "Testing the merge flow" subsection (both the `mergeflow`
  engine tests and the `branch merge` E2E tests), alongside close/start/prune.
- `README.md`: the `git zf branch merge` line already exists; add a short
  description of the pick-source→into-current behaviour and post-steps.
- `ROADMAP.md`: strike the "Open: `git zf branch merge`" section with a
  shipped-date suffix.
- Docstrings on `cmd/mergeflow` (package + `Run` + `Prompter` + `PrefillFunc`),
  matching the detail level in `cmd/issue/close_prompter.go`.

## Risks and open questions

- **Risk — extraction changes close's behaviour subtly.** The three subtleties
  (issue prefill, FF-deferred, materialized rollback) are the danger. Mitigation:
  the full close E2E suite runs unmodified against the rewired `runClose`; any
  drift fails a test. Do the extraction as a **behaviour-preserving move first**
  (close still the only caller, suite green), then add `branch merge` on top.

- **Risk — orientation generalization.** close's executors assume
  feature→base; `branch merge` uses source→current. The engine's stated invariant
  ("advance Target") holds for both because every executor already parameterizes
  on `(source, target)`; the engine tests exercise the branch-merge orientation
  directly.

- **Open — commit-message shape for non-issue merges.** Recommendation: reuse the
  same conventional-commit `ComposeMessage` form with a `"Merge <source> into
  <target>"` subject prefill (least new code). Alternative: a dedicated plain
  one-line merge message with no type/scope. Decide during implementation; the
  `PrefillFunc` seam makes either a one-line change.

- **Resolved — remote source + remote deletion.** `branch merge` picks local ∪
  remote-only sources (materializing remote-only ones via close's proven
  `MaterializeBranch` path), and the delete-source step removes the remote
  counterpart under the same confirm (aligns with ROADMAP "Delete the remote
  branches deleting the local branches"). If the single confirm proves noisy,
  splitting out a second remote-delete confirm is a later tweak.

- **Risk — materialize rollback in `branch merge`.** Supporting remote-only
  sources adds an abort-rollback path (delete the just-materialized local branch
  when the merge doesn't commit). Mitigation: it is the *same* `mergeCommitted`
  guard + `DeleteLocalBranchSafe` defer that `runClose` already ships and that the
  close E2E suite covers; the two new `_RemoteOnlySource_` E2E tests assert the
  clone is left unchanged on abort.

- **Resolved — `branch merge` REFUSES issue-branch sources (safety).** Merging a
  tracked issue branch via `branch merge` would bypass every issue-close
  guarantee: `reviewPreflight` (silently dropping a reviewer's un-incorporated
  commits, or merging an `in_review`-locked branch), the `ChildrenAllMerged`
  guard (merging a parent while sub-tasks are open), the tracker update (the
  remote issue never closes), and the conventional-commit message. So step 5 of
  `runMerge` refuses any source where `branch.Parse` succeeds and redirects to
  `issue close` — the mirror image of the empty-state redirect close gains. The
  gate is `branch.Parse`, not a store lookup, so it also catches **remote-only**
  issue branches the local store has never seen. Consequence: the earlier
  "update store if tracked" post-step is now unreachable and is dropped.

- **Note — engine framing is intact.** Refusing issue branches at the *command*
  layer does not weaken the "core engine" design: `mergeflow` stays fully
  general (it still merges any source into any target); the issue-branch policy
  lives in `branch merge`'s driver, exactly where `issue close`'s issue policy
  lives. The engine remains the single shared mechanism.
