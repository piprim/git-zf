# `git zf review request/approve/reject` — tracker status update — Design Spec

**Date:** 2026-06-22  
**Status:** Draft  
**Scope:** Make the three review-lifecycle commands prompt to update the
originating tracker's issue status (as `issue close` already does), gated on the
issue being tracker-born — with that origin stored in the **git object**
(`BranchRef`) rather than the local SQLite store, so it works on a reviewer's
clone with an empty store.

---

## 1. Problem

`git zf issue close` prompts the operator to update the originating tracker's
issue status after a successful merge. The three review-lifecycle commands —
`review request`, `review approve`, `review reject` — perform the analogous
state transitions but never offer the same update, so a tracker issue advances
through "in review → approved → closed" inside git-zf while its tracker status
goes stale.

The hard part is **gating**. The update must fire only for issues that were
*created from a tracker*, and `approve`/`reject` are typically run by a
**reviewer on a fresh clone whose local SQLite store has no row for that
issue**. So the per-issue "is this tracker-born?" signal cannot live in the
local store — the reviewer's store can't answer the question.

| Actor | Command | Has store row? | Needs to know origin? |
|-------|---------|----------------|-----------------------|
| Developer | `review request` | yes | yes |
| Reviewer (fresh clone) | `review approve` / `review reject` | **no** | yes |

---

## 2. Solution

Follow git-bug's architecture — the git refs are the source of truth, not the
local DB. Store the **originating tracker type in the git object**: the
`BranchRef` blob at `refs/zf/branches/<issueSlug>`, which is already written and
pushed at issue creation and fetched by every clone. The review commands read
it back cross-machine to decide whether to prompt.

The SQLite `TrackerType` column stays as a local cache; this change makes the
git ref the authority for the origin signal.

For tracker-born issues the `issueSlug` already **is** the tracker issue ID
(`b.IssueID() == picked.ID`), so no separate ID needs to be stored — the review
commands already hold `issueSlug` and pass it straight to the tracker.

The prompt UX **mirrors `issue close`**: fetch the tracker's full status list
and free-pick one (or cancel to skip). It reuses the same
`issueflow.ApplyTrackerStatus` helper, so "empty = skip" and non-fatal error
handling are identical by construction.

---

## 3. Part 1 — Store the tracker origin in the git object

**`git/branch_ref.go`** — add a field to `BranchRef`:

```go
TrackerType string `json:"tracker_type,omitempty"` // "" = manual; else the tracker that created the issue
```

`omitempty` keeps pre-existing refs backward-compatible: an absent field
unmarshals to `""`, which every reader treats as "manual → don't prompt".

**`cmd/issueflow/start.go`** — populate it at creation. Lines 295–316 already
derive the origin for the store write (`tt = &deps.Cfg.IssueTracker.Type` when
`picked.TrackerType != ""`). Compute one `trackerType` string and use it for
both the store write and the ref:

- `writePushBranchRef(ctx, deps, issueSlug, branchName string)` →
  `writePushBranchRef(ctx, deps, issueSlug, branchName, trackerType string)`,
  setting `ref.TrackerType = trackerType` in the `git.BranchRef{}` literal
  (start.go:473 — the only non-test construction site).
- At the call site (start.go:310) pass the derived `trackerType`
  (`deps.Cfg.IssueTracker.Type` when `picked.TrackerType != ""`, else `""`).

No change in `cmd/issue/close.go`: `updateClosedStatus` stamps `Merged:true` by
copying `*existing` (close.go:478–483), so `TrackerType` is preserved on the
merged ref automatically.

---

## 4. Part 2 — Review commands read the origin and prompt

**`cmd/review/deps.go`** — add `tracker tracker.Tracker` to `reviewDeps` and
build it in `buildReviewDeps`, mirroring `buildCloseDeps` (close.go:37–63):
when `cfg.IssueTracker.Type != ""`, call `tracker.New(cfg.IssueTracker)`; on
error warn and leave `tracker` nil (non-fatal). Add the `tracker` import.

**`cmd/review/prompter.go`** — add to the `ReviewPrompter` interface:

```go
PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)
```

- `huhReviewPrompter`: implement identically to `huhPrompter.PickTrackerStatus`
  (close_prompter.go:132) — reuse `tui.IssueStatusPicker`.
- `scriptedReviewPrompter`: add canned fields (`TrackerStatus string`,
  `TrackerStatusErr error`) and return them.

**`cmd/review/tracker.go`** (new, ~25 lines) — one shared helper so the three
commands stay DRY:

```go
func maybeUpdateTrackerStatus(ctx context.Context, deps reviewDeps, prompter ReviewPrompter, issueSlug string) {
    if deps.tracker == nil { return }                  // no tracker on this clone
    _ = deps.client.FetchBranchRefs(ctx)               // best-effort, cross-machine
    ref, err := deps.client.ReadBranchRef(ctx, issueSlug)
    if err != nil { /* warn */ return }
    if ref == nil || ref.TrackerType == "" { return }  // manual / pre-origin ref
    issueflow.ApplyTrackerStatus(ctx, deps.tracker, deps.client.IO().Err,
        issueSlug, deps.cfg.IssueTracker.Type, prompter.PickTrackerStatus)
}
```

**Call sites** — invoke it from the three `...Interactive` wrappers
(`request.go` / `approve.go` / `reject.go`), which already hold the `prompter`.
Each currently ends `return runReviewXxx(ctx, deps, picked.IssueSlug)`; change
to: run the transition, return on error, else
`maybeUpdateTrackerStatus(ctx, deps, prompter, picked.IssueSlug)` then
`return nil`.

Rationale for the wrapper (not the core `runReviewXxx`): the prompt is an
interactive concern and the prompter already lives at the Interactive layer.
This leaves the `runReviewXxx` signatures and the many direct-call
parallel-scenario tests untouched.

---

## 5. Data flow

```
issue start (developer)
  picked.TrackerType != "" ──► trackerType = cfg.IssueTracker.Type
        │                              │
        ▼                              ▼
  store.Issue.TrackerType        BranchRef.TrackerType  ──push──► refs/zf/branches/<slug>
   (local cache)                  (git object = source of truth)
                                          │
                            fetch ────────┘
                                          ▼
review request/approve/reject (any clone)
  ReadBranchRef(slug).TrackerType != "" AND deps.tracker != nil
        │
        ▼
  ApplyTrackerStatus → PickTrackerStatus (free pick, like issue close)
        │
        ▼
  tracker.UpdateIssueStatus(slug, selected)
```

---

## 6. Testing

Per the project convention, wrap every distinct assertion in a named `t.Run`.

- **`git/branch_ref_test.go`** — a `t.Run` asserting `TrackerType` round-trips
  through `WriteBranchRef` → `ReadBranchRef`.
- **`cmd/review/` E2E** (extend `review_e2e_test.go` or a new
  `tracker_e2e_test.go`) — build a rig with a `fake.Tracker` (pattern from
  `cmd/issue/close_e2e_test.go:101–115`; `deps()` includes `tracker`). Seed a
  tracker-born ref via
  `client.WriteBranchRef(ctx, slug, git.BranchRef{..., TrackerType: "fake"})`,
  drive each `...Interactive` with
  `scriptedReviewPrompter{Branch:..., TrackerStatus:"In Progress"}`, and assert
  on `rig.tracker.RecordedUpdates`. One `t.Run` each for:
  - `request` / `approve` / `reject` on a tracker-born issue → records
    `{IssueID: slug, StatusName: "In Progress"}`.
  - manual issue (ref `TrackerType == ""`) → no update recorded.
  - no tracker configured (`deps.tracker == nil`) → no update, no panic.
  - absent BranchRef → no update.

---

## 7. Verification

- `mise exec -- go build ./...`
- `mise exec -- go vet ./...`
- `mise exec -- go test ./...`
- Targeted: `mise exec -- go test ./git/... -run TestBranchRef -v` and
  `mise exec -- go test ./cmd/review/... -v`
- Per AGENTS.md: run GitNexus `impact` on `BranchRef`, `writePushBranchRef`,
  `reviewDeps`, and `ReviewPrompter` before editing each; run `detect_changes`
  before finishing to confirm only the expected symbols/flows changed.
- Remove the completed bullet from `ROADMAP.md` (line 5).

---

## 8. Files

| File | Change |
|------|--------|
| `git/branch_ref.go` | add `TrackerType` field + doc |
| `cmd/issueflow/start.go` | thread `trackerType` into `writePushBranchRef` |
| `cmd/review/deps.go` | `reviewDeps.tracker` + build in `buildReviewDeps` |
| `cmd/review/prompter.go` | `PickTrackerStatus` on interface + both impls |
| `cmd/review/tracker.go` | new `maybeUpdateTrackerStatus` helper |
| `cmd/review/request.go`, `approve.go`, `reject.go` | call helper from `...Interactive` wrappers |
| `git/branch_ref_test.go`, `cmd/review/*_e2e_test.go` | tests |
| `ROADMAP.md` | remove line 5 |
