# Roadmap

## Delete branch related to closed issue in the tracker

## Enhancement

### End-to-end testability of interactive flows

The close flow was the first command refactored behind a prompter interface (`ClosePrompter`) so its merge / store / tracker pipeline could be exercised end-to-end without driving a real TUI. The same shape applies to the other interactive commands. Ranked by ROI:

1. ~~**`cmd/issue/start.go`** — the biggest gap. Five huh forms (worktree toggle, tracker toggle, two `IssueConfirm` calls, status picker) plus the conflict-resolution loop in `cmd/issue/conflict.go` and the issue-input forms in `issue/issue.go`. Today the only E2E coverage is `worktreePath` (a pure helper). Extracting a `StartPrompter` interface + `RunIssueStart(ctx, deps, prompter)` would unlock E2E tests for branch creation, worktree creation, tracker round-trip, and the deterministic-branch-naming conflict matrix. Spec: [`docs/superpowers/specs/2026-05-26-issue-start-refactor-design.md`](./docs/superpowers/specs/2026-05-26-issue-start-refactor-design.md).~~ (shipped 2026-05-26 — see `cmd/issue/start_e2e_test.go`)

2. ~~**`cmd/branch/branch.go` — prune flow specifically.** `executePrune` is the only place in the `branch` command with destructive store mutations (`DeleteBranch` / `UpdateBranchStatus`) gated behind a single confirm form. A small `PrunePrompter` with one method (`ConfirmPrune(toDelete, toMerge int) bool`) would let the executor reuse the same `tracker/fake` + tempdir-store rig the close flow already ships. Lower payoff than start; could also be served by a `--yes` flag.~~ (shipped 2026-05-26 — see `cmd/branch/prune_e2e_test.go`)

3. ~~**`cmd/issue/conflict.go`** — `resolveBranchConflict` loops between two forms (action picker + variant input) and re-checks branch existence. The existing test only covers the pure `rebuildVariantBranch` helper; the loop is uncovered. Worth doing in the same refactor as (1), since they share a prompter surface.~~ (shipped 2026-05-26 — the loop now lives in `HuhStartPrompter.ResolveBranchConflict` and is exercised by `TestRunIssueStart_VariantOnCollision` + `TestRunIssueStart_AbortOnCollision`; `rebuildVariantBranch` remains in `cmd/issue/conflict.go` with its existing unit tests.)

Lower priority (manual testing is fine, or surface is too small): `cmd/commit/commit.go` (thin wrapper around `commitpkg.FillOutForm`, already tested), `cmd/config/init.go` (config-file ops with low blast radius), `cmd/issue/issue.go` (top-level action dispatcher).

## Bug

### ~~invalid memory address~~ (fixed 2026-05-26)

Pre-refactor, `getFromTracker` returned `(nil, nil)` when the operator declined the "Fetch issues from TRACKER" toggle. `RunIssueStart` then handed the nil `pickedIssue` to `prepareBranch`, which dereferenced `pickedIssue.ID` and panicked.

The post-refactor `pickIssue` (in `cmd/issue/start.go`) explicitly falls through to `issue.GetFromUser` when the tracker toggle is declined, so the manual-input flow runs instead. `TestRunIssueStart_DeclinesTrackerTogglesToManual` is the regression guard.

### Open: `git zf branch merge`

Still a placeholder (`cmd/branch/branch.go:mergeRunE`). The original bug-section note about the `issue close` UX ("it is not an issue, tell the user to use `git zf branch merge`") is the design seam: `branch merge` should take over the merge surface for non-issue branches, and `issue close` should redirect when invoked on one.
