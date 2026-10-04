# Design: `git zf issue close`

**Date:** 2026-05-04

## Overview

`git zf issue close` lets the user pick an in-progress branch, merge it into the base branch (squash or classic), update the local store and the remote tracker, then optionally delete the local branch.

## Architecture

No new packages. Follows the same layering as `issue start`:

| Layer | File | Responsibility |
|---|---|---|
| CLI | `cmd/issue/close.go` | Cobra command + orchestration |
| TUI | `tui/issue.go` | New TUI components (branch picker, merge strategy picker, author picker, confirmations) |
| Git | `git/git.go` | Two new methods: `MergeDryRun`, `MergeSquash` |
| Store | existing | `UpdateBranchStatus`, `UpdateIssueStatus`, `DeleteBranch` (no changes needed) |

All subprocess calls use `exec.CommandContext(ctx, ...)`.

## Step-by-step Flow

1. Open store, load `in_progress` branches. If empty → print "No in-progress branches." and return.
2. Detect current branch name (`git rev-parse --abbrev-ref HEAD` subprocess).
3. **TUI — branch picker:** list of in-progress branches; current branch pre-selected.
4. Resolve base branch via `client.DefaultBaseBranch()`.
5. **Merge dry-run:** `MergeDryRun(ctx, branch, base)` — checks out base, runs `git merge --no-commit --no-ff <branch>`, always aborts with `git merge --abort`. Returns the list of conflicting files (or nil).
6. If conflicts → print each conflicting file, print "Conflicts detected. Aborting.", return error.
7. **TUI — merge strategy:** choose **Squash** (default) or **Classic** (`--no-ff`).
   - If **Squash** → show author picker (pre-filled with `Authors()[0]`, i.e. git config identity).
   - If **Classic** → skip author picker.
8. **TUI — confirm** the merge (branch name + strategy + author if squash).
9. Execute merge:
   - Squash: `MergeSquash(ctx, branch, base, author)` — runs `git merge --squash <branch>` then `git commit --author=<author>`.
   - Classic: `git merge --no-ff <branch>`.
10. Update store: `UpdateBranchStatus(uuid, merged, &now)` + `UpdateIssueStatus(issueID, closedStatusID)`.
11. Tracker status update — reuse `updateTrackerIssueStatus` from `start.go`.
12. **TUI — confirm delete local branch** → if yes, `git branch -d <branch>`.

## New Git Client Methods

```go
// MergeDryRun checks whether branchName merges cleanly into baseBranch.
// It runs merge --no-commit --no-ff and always aborts after.
// Returns the list of conflicting file paths, or nil if clean.
MergeDryRun(ctx context.Context, branchName, baseBranch string) (conflicts []string, err error)

// MergeSquash squash-merges branchName into baseBranch and commits.
// author is the "Name <email>" string; empty falls back to git config identity.
MergeSquash(ctx context.Context, branchName, baseBranch, author string) error

// MergeNoFF runs a classic --no-ff merge of branchName into baseBranch.
MergeNoFF(ctx context.Context, branchName, baseBranch string) error

// CurrentBranch returns the name of the currently checked-out branch.
CurrentBranch(ctx context.Context) (string, error)
```

## New TUI Components

- `IssueBranchPicker(rows []store.BranchRow, current string, selected *string) *huh.Group` — select from in-progress branches, current pre-selected.
- `IssueMergeStrategy(squash *bool) *huh.Group` — Squash (default) vs Classic.
- `IssueMergeAuthor(authors []string, author *string) *huh.Group` — author select, shown only when squash chosen.
- `IssueMergeConfirm(branch, base, strategy, author string, confirmed *bool) *huh.Group` — summary confirm.
- `IssueDeleteBranch(branch string, confirmed *bool) *huh.Group` — delete local branch confirm.

## Error Handling

- Conflicts detected → print conflict list + "Aborting." → non-zero exit. Store and tracker are not touched.
- Merge fails after confirmation → print error, leave store/tracker unchanged, user must resolve manually.
- Store update fails after successful merge → print warning (same pattern as `issue start`).
- Tracker update fails → print warning, do not abort (same pattern as `issue start`).
- Branch delete fails → print warning, do not abort (branch can be deleted manually).

## Testing

- `git.MergeDryRun`: unit test with a real in-memory repo (two branches with intentional conflict vs clean merge).
- `git.MergeSquash` / `git.MergeNoFF`: unit test verifying resulting commit history.
- `store` methods: already tested; no new store code.
- TUI components: unit tests for pre-selection logic (current branch selected by default, `Authors()[0]` pre-filled).
