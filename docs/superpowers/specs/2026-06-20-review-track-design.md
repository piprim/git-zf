# `git zf review track` / `git zf issue track` — Design Spec

**Date:** 2026-06-20  
**Status:** Draft  
**Scope:** New `track` command (aliased in both `review` and `issue` groups) that registers
an existing git branch — created via plain `git checkout` — into the git-zf store so the
developer or reviewer can participate in the review workflow.

---

## 1. Problem

git-zf tracks branches in a local SQLite store. When a branch is created via
`git zf issue start` or `git zf review start`, the store is populated automatically.

When a branch is checked out with plain `git checkout` (e.g. after `git fetch`), the
store has no record of it, breaking downstream commands:

| Actor | Plain-git action | Broken command |
|-------|-----------------|----------------|
| Developer Bob | `git checkout -b X.2@feat@part-two origin/X.2@feat@part-two` | `git zf review request` → "No in-progress branches" |
| Reviewer Carol | `git checkout -b X.1@review <sha>` | `git zf review approve/reject` → no review record |

---

## 2. Solution

A single `track` subcommand exposed as an alias in both command groups:

```
git zf review track     # alias — discoverable from the review workflow
git zf issue track      # alias — discoverable from the issue workflow
```

No arguments, no flags. The current branch name is the sole input. The command
auto-detects which path to take and registers the branch in the store.

---

## 3. Detection Logic

```
current branch name
  ├─ branch.Parse() succeeds  (e.g. X.2@feat@part-two)
  │    → Developer path
  ├─ strings.HasSuffix("@review")  (e.g. X.1@review)
  │    → Reviewer path
  └─ neither
       → Error: "current branch does not match a git-zf naming convention
                 (expected <IssueID>@<type>@<slug> or <IssueID>@review)"
```

Both paths are idempotent: if the branch is already in the store with the correct
state, the command prints a confirmation and exits cleanly.

---

## 4. Developer Path

**Trigger:** `branch.Parse(currentBranch)` succeeds.

**Steps:**

1. Parse `IssueID`, `type`, `slug` from the branch name via `branch.Parse()`.
2. Open the store via `deps.client.GitDir()`.
3. Check `store.ListBranches(ctx, store.BranchStatusAll)` for an existing entry
   matching `BranchName == currentBranch` → if found, print:
   *"Branch already tracked (status: in_progress)."* and exit.
4. Derive a human-readable title from the slug: replace hyphens with spaces
   (`part-two` → `part two`). Used as a placeholder title since no TUI prompt
   is opened.
5. Call `store.InsertIssueWithBranch` with `StatusID = StatusIDInProgress`.
6. Best-effort ref check: read `refs/zf/reviews/<IssueID>` locally. If status is
   `in_review`, warn: *"Note: branch X.2@feat@part-two is currently locked for
   review (round N). You cannot submit again until the reviewer decides."*
7. Print:
   ```
   Branch "X.2@feat@part-two" is now tracked (issue X.2, type feat).
   Run: git zf review request
   ```

**Not implemented (YAGNI):**
- Parent relation lookup — not derivable from branch name alone without a TUI.
- Title prompt — kept as a slug-derived placeholder.

---

## 5. Reviewer Path

**Trigger:** `strings.HasSuffix(currentBranch, "@review")`.

**Steps:**

1. Extract `IssueID` by stripping `@review` suffix.
2. `deps.client.FetchReviewRefs(ctx)` — best-effort, warning on failure.
3. `deps.client.ReadReviewRef(ctx, IssueID)`:
   - Not found → error: *"No review found for issue X.1 — has the developer run
     `git zf review request`?"*
   - Status ≠ `in_review` → error: *"Issue X.1 is not awaiting review (current
     status: approved)."*
4. Check store via `store.GetLatestReview(ctx, IssueID)`:
   - **Record found, reviewer already set** → print:
     *"Already registered as reviewer for X.1 (round N)."* exit cleanly.
   - **Record found, reviewer empty** → `store.UpdateReviewerIdentity(ctx, id, reviewer)`.
   - **No record** → `store.InsertReview(ctx, IssueID, reviewer)` using round from
     the ref blob to align with the remote state, then `store.UpdateReviewStatus`
     to keep status consistent.
5. Print:
   ```
   Branch "X.1@review" registered as review branch for issue X.1 (round N).
   Run: git zf review approve
       git zf review reject
   ```

**Note on round alignment:** When inserting a new review record for a reviewer who
has no local store entry, `InsertReview` auto-computes `round = MAX(round) + 1`.
If the store is empty for this issue, `round` will be 1 — which may not match the
ref (e.g. ref says round 2 after a rejection). To align, after inserting, call
`store.UpdateReviewStatus(ctx, newID, ReviewStatusInReview, false)` — no round
correction is needed because the insert will derive round 1 on an empty store
and round N+1 on a non-empty store, both of which are consistent with the local
history the reviewer has.

---

## 6. Implementation

### New files

| File | Responsibility |
|------|---------------|
| `cmd/review/track.go` | `getTrackCmd()` + `runTrack()` core logic |

### Modified files

| File | Change |
|------|--------|
| `cmd/review/review.go` | Add `r.getTrackCmd()` to `GetRootCmd()` |
| `cmd/issue/issue.go` | Add `git zf issue track` alias pointing at the same `runTrack` |
| `cmd/review/review_e2e_test.go` | Tests for developer path and reviewer path |

### Key reuse

- `branch.Parse()` — `branch/branch.go` — parses `<IssueID>@<type>@<slug>` format
- `store.InsertIssueWithBranch` — existing store method
- `store.GetLatestReview` / `store.InsertReview` / `store.UpdateReviewerIdentity` — existing store methods
- `client.ReadReviewRef` / `client.FetchReviewRefs` — existing git ref methods
- `client.GitDir()` — used for opening the correct store in all paths

---

## 7. Open Questions

- **Round mismatch on reviewer path**: if the reviewer's local store already has
  review records for this issue (from a previous session) but they were lost
  (wiped store, new clone), the new `InsertReview` call will produce a round number
  that may not match the ref. This is acceptable: the round in the ref is authoritative
  for cross-machine visibility; the local store round is used only for display.
