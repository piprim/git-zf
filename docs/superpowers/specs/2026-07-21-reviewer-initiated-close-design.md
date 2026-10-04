# Reviewer-initiated close — Design Spec

**Date:** 2026-07-21
**Status:** Draft
**Scope:** Let a reviewer (or any team member) close an issue they did not start,
without first running `git zf issue track`. `issue close` falls back to
`refs/zf/branches/*` (already fetched by the close flow) when the store has no
matching in-progress row — the same cross-machine fallback already used for
parent-slug resolution — and materializes the feature branch from the remote so
the merge can run.

---

## 1. Problem

`git zf issue close` → `runClose` → `getPickedBranch` lists in-progress
branches **from the local SQLite store only**
(`store.ListBranches(ctx, BranchStatusInProgress)`). A reviewer's clone has no
store row for a teammate's feature branch, so:

1. **The picker is empty.** Close prints `No in-progress branches.` and exits
   before any merge logic runs.
2. **Even with a row, the merge would fail.** All three strategies need the
   feature branch `XX@feat@slug` resolvable as a **local** branch:
   - Squash: `git merge --squash XX@feat@slug` (bare name must resolve).
   - Rebase / Classic: `git checkout XX@feat@slug`.

   On a reviewer's clone the feature branch exists only as
   `origin/XX@feat@slug` (fetched when they set up their `XX@review` branch),
   not as a local branch. The reviewer's own checked-out branch is
   `XX@review`, which is **not** the branch to merge.

Today the only workaround is `git zf issue track`, but that command keys off the
*current* branch (`branch.Parse(currentBranch)`), which for a reviewer is
`XX@review` — the wrong input. There is no path for a reviewer to close a
teammate's feature branch.

---

## 2. Solution overview

Three coordinated changes, all mirroring patterns already in the codebase
(`issueflow.ResolveParentBranch`'s ref-fallback, `issueflow.ReconcileMergedFromRefs`,
`git.ListReviewRefs`):

1. **Picker source = union of store + branch refs.** `getPickedBranch` offers
   every store in-progress branch **plus** every `refs/zf/branches/*` entry with
   `merged=false` that has no matching store row and whose feature branch
   resolves. Keyed by issue slug.
2. **Auto-track on pick.** When the reviewer picks a ref-derived (untracked)
   branch, insert the issue + branch rows into the local store so it becomes
   consistent (`git zf issue list` shows it; no per-call "no branch with name"
   warnings downstream).
3. **Materialize the feature branch.** Create the local feature branch from
   `origin/<feature>` when it is absent, so the merge strategies resolve it.

The rest of `runClose` (review preflight, base resolution, merge, close status,
delete-branch, push) is unchanged: it already tolerates cross-machine state.

---

## 3. Detection & selection logic

```
getPickedBranch:
  ReconcileMergedFromRefs(store, client)   # existing; runs FetchBranchRefs
  candidates = CloseCandidates(store, client)
  if candidates empty -> print "No in-progress branches." ; return (nil, nil)
  picked = prompter.PickBranch(candidates, currentBranch)

runClose (new step, immediately after a non-nil pick, BEFORE reviewPreflight):
  picked = MaterializeAndTrack(store, client, picked)
```

`CloseCandidates` returns `[]store.BranchRow`:

- Every row from `store.ListBranches(ctx, BranchStatusInProgress)` (unchanged
  semantics — these are the developer's own started branches).
- Plus, for each `BranchRef` from `client.ListBranchRefs(ctx)` where **all** of:
  - `ref.Merged == false`, **and**
  - `ref.IssueSlug` has no in-progress store row already in the list, **and**
  - the feature branch resolves — `client.ResolveBranchRef(ref.BranchName)`
    succeeds (local `refs/heads/<name>` or remote `origin/<name>`).

  a synthesized `BranchRow` is appended:

  | Field        | Value                                             |
  |--------------|---------------------------------------------------|
  | `IssueID`    | `0` — sentinel: "not yet tracked in this store"   |
  | `IssueSlug`  | `ref.IssueSlug`                                    |
  | `BranchName` | `ref.BranchName`                                  |
  | `Type`       | `branch.Parse(ref.BranchName).Type()` (fallback `""` if parse fails) |
  | `Title`      | slug-derived: hyphens → spaces (same as `issue track`) |
  | `Status`     | `BranchStatusInProgress`                          |
  | `CreatedAt`  | parsed from `ref.CreatedAt` (zero value on parse failure) |

**Why filter unresolvable-feature candidates out** (rather than show-then-error):
a ref whose feature branch cannot be resolved locally or via origin cannot be
closed anyway, so offering it in the picker would guarantee a downstream error.
Filtering keeps the picker honest. The check is a cheap `ResolveBranchRef` per
candidate.

**Dedupe** is by issue slug: a slug already present as a store in-progress row is
never added a second time from the refs.

---

## 4. Auto-track & materialize on pick

`MaterializeAndTrack(ctx, store, client, picked) (store.BranchRow, error)`:

- If `picked.IssueID != 0` → already tracked (store-derived pick). Return it
  unchanged. (A store in-progress row implies the developer started the branch
  locally, so the feature branch already exists; no materialization needed.)
- If `picked.IssueID == 0` → ref-derived, untracked:
  1. **Materialize**: if `refs/heads/<picked.BranchName>` does not exist but the
     branch resolves via origin, `client.CreateLocalBranch(ctx,
     picked.BranchName, "<remote>/<picked.BranchName>")`.
  2. **Auto-track** (idempotent, mirrors `runTrackDeveloper`): if a store row
     with `BranchName == picked.BranchName` already exists, skip the insert;
     otherwise read the branch ref once (`client.ReadBranchRef(ctx,
     picked.IssueSlug)`) for its `TrackerType`, then `store.InsertIssueWithBranch`
     with:
     - Issue: `IDSlug = picked.IssueSlug`, `Title = picked.Title`,
       `StatusID = StatusIDInProgress`, `TrackerType` mapped from the ref:
       `ref.TrackerType == ""` → `nil` (manual), non-empty → `&ref.TrackerType`
       (`store.Issue.TrackerType` is a `*string` where nil means manual). This
       makes `git zf issue list` on the reviewer's clone classify the issue
       correctly instead of showing it as manual. The tracker-prompt gate and
       the tracker update in `updateClosedStatus` read `TrackerType` from the
       branch **ref** and the local tracker **config** — not this store row — so
       this refinement is purely for local-list accuracy and does not change the
       tracker-update behavior. When the ref cannot be read (best-effort miss),
       fall back to `nil`.
     - Branch: `Name = picked.BranchName`, `Type = picked.Type`,
       `StatusID = StatusIDInProgress`.
  3. **Re-read**: look up the branch row from the store
     (`ListBranches(BranchStatusAll)` → match by `BranchName`) and return it so
     `IssueID` is populated for downstream (`updateClosedStatus` uses
     `picked.IssueID`).

Ordering rationale: this step runs **before** `reviewPreflight`, whose
approved-path fast-forward (`FastForwardOnly` / `MergeForward` onto the feature
branch) also needs the local feature branch present.

---

## 5. Downstream flow (unchanged, already cross-machine tolerant)

After `MaterializeAndTrack`, `runClose` proceeds unmodified:

- `reviewPreflight` — reads the review ref (authoritative), incorporates any
  approved reviewer commits into the now-local feature branch, cleans up.
- `resolveDefaultBase` → `issueflow.ResolveParentBranch` — already falls back to
  the branch ref's `ParentSlug` when the store has no parent relation.
- `chooseMergeTarget`, `doMerge` — feature branch now resolves locally, so all
  three strategies work.
- `updateClosedStatus` — reads `TrackerType` from the branch ref (not the store),
  so a reviewer correctly gets (or, for a manual issue, does not get) the tracker
  status prompt. Stamps `Merged=true` on the ref and pushes it, so the original
  developer's clone reconciles the close via `ReconcileMergedFromRefs`.
- `doDeleteBranch`, `proposeClosePush` — unchanged.

---

## 6. Implementation

### New files

| File | Responsibility |
|------|----------------|
| `cmd/issueflow/closecandidates.go` | `CloseCandidates` + `MaterializeAndTrack` |
| `cmd/issueflow/closecandidates_test.go` | Unit tests for both helpers |

### Modified files

| File | Change |
|------|--------|
| `git/branch_ref.go` | Add `ListBranchRefs(ctx) ([]BranchRef, error)` (parallel of `ListReviewRefs`) |
| `git/git.go` | Add `CreateLocalBranch(ctx, name, startPoint) error` (thin `git branch <name> <startPoint>`) |
| `cmd/issue/close.go` | `getPickedBranch` calls `CloseCandidates`; `runClose` inserts the `MaterializeAndTrack` step after the pick |
| `cmd/issue/close_e2e_test.go` | New `TestClose_ReviewerInitiated` |
| `git/branch_ref_test.go` | Unit tests for `ListBranchRefs` |
| `git/git_test.go` (or equivalent) | Unit test for `CreateLocalBranch` |
| `README.md` | `issue close` description gains a line about closing teammate/reviewer branches from fetched refs (no new flags) |

### New git primitives

```go
// ListBranchRefs returns all locally available branch refs (refs/zf/branches/*).
// Call FetchBranchRefs first to refresh the namespace. Returns an empty slice
// (not an error) when none exist. Malformed blobs are skipped. Mirrors
// ListReviewRefs.
func (c *Client) ListBranchRefs(ctx context.Context) ([]BranchRef, error)

// CreateLocalBranch creates refs/heads/<name> pointing at startPoint
// (e.g. "origin/X.1@feat@slug"). Used to materialize a feature branch that
// exists only as a remote-tracking ref on a reviewer's clone. Does not switch
// the working tree.
func (c *Client) CreateLocalBranch(ctx context.Context, name, startPoint string) error
```

### Key reuse

- `issueflow.ResolveParentBranch` ref-fallback pattern — the model for reading
  branch metadata from git when the store has no row.
- `git.ListReviewRefs` — the template for `ListBranchRefs`.
- `runTrackDeveloper` (`cmd/review/track.go`) — the model for the idempotent
  insert (title-from-slug, `InsertIssueWithBranch`).
- `git.ResolveBranchRef` — local-then-`origin/` resolution, used both as the
  picker filter and the materialize existence check.

---

## 7. Error handling

- **No remote / no origin feature ref** — the candidate is filtered out of the
  picker (`ResolveBranchRef` fails), so it is never offered. A local-only repo
  simply sees store rows, exactly as today.
- **`CreateLocalBranch` failure** (e.g. name already exists as a different ref) —
  wrapped error returned from `MaterializeAndTrack`; close aborts before touching
  the working tree. Non-destructive.
- **Auto-track insert** — idempotent: an existing store row for the same branch
  name skips the insert, so re-running after a partial failure is safe.
- **`ListBranchRefs`** — best-effort like the other ref reads: an enumeration
  error degrades to store-only candidates (never fatal to close).

---

## 8. Testing

Per `CLAUDE.md`, close is E2E-tested with a real on-disk repo, a seeded store,
the `tracker/fake/` tracker, and a `scriptedPrompter`. Every distinct assertion
is wrapped in its own `t.Run`.

**`git` unit tests:**
- `ListBranchRefs`: empty namespace → empty slice; multiple refs → all parsed;
  a malformed blob is skipped, not fatal.
- `CreateLocalBranch`: creates `refs/heads/<name>` from a remote-tracking ref;
  the new branch resolves to the same SHA as the start point.

**`issueflow` unit tests (`CloseCandidates`):**
- store-only (no refs) → store rows unchanged.
- ref-only (empty store) → synthesized rows, `IssueID == 0`, `Type`/`Title`
  derived.
- union: a slug in both store and refs appears once (store row wins).
- a ref with `Merged == true` is excluded.
- a ref whose feature branch does not resolve (no local, no origin) is excluded.

**`issueflow` unit tests (`MaterializeAndTrack`):**
- ref-derived pick: creates the local feature branch from origin, inserts the
  store rows, returns a row with a non-zero `IssueID`.
- tracker-type carried: a ref with `TrackerType = "github"` produces an inserted
  issue row whose `TrackerType` is non-nil `"github"`; a ref with an empty
  `TrackerType` produces a nil (manual) issue row.
- idempotent: a second call (row already present) does not double-insert and
  returns the tracked row.
- store-derived pick (`IssueID != 0`): returned unchanged, no materialization.

**E2E (`cmd/issue/close_e2e_test.go`):**
- `TestClose_ReviewerInitiated`: a reviewer clone with an **empty store**, a
  fetched `refs/zf/branches/<slug>` (merged=false), and `origin/<feature>`
  present. Drive close with the scripted prompter and assert:
  - the merge lands on the base branch,
  - the local store becomes consistent (issue + branch rows exist, marked merged
    after close),
  - the branch ref is stamped `Merged=true`.

---

## 9. Out of scope (YAGNI)

- **Parent/child relation inserts on auto-track.** Not written to the store,
  matching `issue track`'s existing decision. `ResolveParentBranch` reads the
  parent from the ref, so a child close still resolves the parent integration
  branch. A reviewer closing a **parent** issue whose children are absent from
  their store is a pre-existing edge case this change does not address.
- **New CLI flags.** Selection is automatic; no `--reviewer`/`--all` surface.
- **Review-branch cleanup semantics.** Unchanged — `reviewPreflight` owns the
  approved-path incorporation and cleanup exactly as today.
