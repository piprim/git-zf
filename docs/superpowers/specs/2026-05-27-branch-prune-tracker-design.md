# Design: `git zf branch prune-tracker` — reap branches whose tracker issue is closed

**Date:** 2026-05-27
**Status:** proposed
**Source ROADMAP entry:** `## Related branch to closed issue in the tracker`

## Overview

When an issue is closed out-of-band (i.e. via the tracker's web UI, not via `git zf issue close`), the local state in git-zf is left dangling: the store still marks the corresponding branch as `in_progress`, and the local git ref still exists. The existing `git zf branch prune` cannot catch this case — its discovery is purely local (store row + local ref reachability from base).

This spec introduces a new sibling subcommand, `git zf branch prune-tracker`, whose discovery is **tracker-driven**: for each local git ref whose name parses as an issue identifier, it asks the configured tracker whether that issue is closed, and if so offers per-branch reaping actions and flips the store row to a new `closed` status.

The existing `branch prune` is untouched.

## What this design does NOT do

- Modify `branch prune`'s behavior, flags, or semantics.
- Introduce parallel tracker API calls (serial only, matches the rest of the codebase).
- Add a bulk "list all closed issues" path on the `Tracker` interface (per-issue lookup is more accurate and simpler).
- Promote the issue-ID regex into `AppConfig` (intentionally file-private for v1; trivial to lift later).
- Build a custom horizontal-radio bubbletea component for the per-branch prompt (native huh stacked-Selects layout is sufficient).
- Touch local refs created outside git-zf that don't parse as an issue ID (silently skipped).

## Command surface

```
git zf branch prune-tracker [flags]

Flags:
  --dry-run                  Show what would be done; no prompts, no mutations.
  --base string              Base branch (default: auto-detect) — used by --safe-delete's ancestry check.
  --safe-delete              Non-interactive: try `git branch -d` on every match; skip with warning if -d refuses.
  --force-delete             Non-interactive: `git branch -D` on every match.
  --skip-delete              Non-interactive: never touch local refs; only flip store status to closed.

  (--safe-delete / --force-delete / --skip-delete are mutually exclusive
   and produce a cobra usage error if combined.)
```

When no action flag is set, the command opens a single huh form with one stacked `Select` per candidate, with `safe-delete` pre-selected.

The command is wired into `Branch.GetRootCmd()` alongside `pruneCmd()` and into the interactive `branch` action menu as `tui.BranchActionNamePruneTracker`.

## Tracker interface change

One new method on `tracker.Tracker`:

```go
// IsIssueClosed reports whether the tracker considers issueID to be closed.
// Returns ErrIssueNotFound for HTTP 404 / missing-issue cases so the caller
// can format that warning distinctly from transport/auth failures.
IsIssueClosed(ctx context.Context, issueID string) (bool, error)
```

```go
// In package tracker.
var ErrIssueNotFound = errors.New("tracker: issue not found")
```

Adapter implementations:

| Adapter | Implementation |
|---|---|
| `tracker/github/github.go` | One `GET /repos/{owner}/{repo}/issues/{number}`. `state == "closed"` → `(true, nil)`. HTTP 404 → `(false, ErrIssueNotFound)`. Other errors → wrapped with `fmt.Errorf("github: lookup %q: %w", id, err)`. |
| `tracker/redmine/redmine.go` | One `GET /issues/{id}.json`. Reads `status.is_closed` (Redmine exposes this on the embedded status object). HTTP 404 → `(false, ErrIssueNotFound)`. Other errors wrapped. |
| `tracker/fake/fake.go` | Test fake gains three maps: `closed map[string]bool`, `unknown map[string]bool` (returns `ErrIssueNotFound`), `errors map[string]error` (returns a custom error). Drives every E2E branch. |

Both adapter unit-test suites (`github_test.go`, `redmine_test.go`) gain `IsIssueClosed` tests against `httptest` fixtures covering: closed, open, 404 → `ErrIssueNotFound`, 500 → wrapped error.

## Discovery

Default issue-ID extractor — a file-private regex in `cmd/branch/prune_tracker.go`:

```go
// First non-empty capture wins.
//   Pattern A: leading numeric ID before a delimiter      ([0-9]+)[-@_|+=.]
//   Pattern B: alphanumeric ID before @ or |              ([^@|]+)[@|]
var defaultIssueIDPattern = regexp.MustCompile(
    `^(?:([0-9]+)[-@_|+=.]|([^@|]+)[@|]).+`,
)

func extractIssueID(branchName string) (id string, ok bool) { ... }
```

The pattern is intentionally narrow for v1 — Pattern A matches Redmine-style numeric IDs (`42@feat@...` or `42-feat-...`); Pattern B matches GitHub-style prefixed IDs (`ABC-42@feat@add-oauth-login@550e8400`). A future iteration can lift it to `AppConfig.Branch.IssueIDPattern` (single-line change in this file; spec out of scope for v1).

Discovery flow:

1. `localNames, _ := c.LocalBranchNames()` — reuse the existing `pruner` git-side abstraction.
2. Load `allRows, _ := s.ListBranches(ctx, store.BranchStatusAll)` once, build `map[string]*store.BranchRow` keyed by branch name. No new SQL.
3. For each `name` in `localNames`:
   - Skip if `name == base` (defensive).
   - `id, ok := extractIssueID(name)`; skip if `!ok` (manual branches, unparseable names).
   - `closed, err := tr.IsIssueClosed(ctx, id)`:
     - `errors.Is(err, tracker.ErrIssueNotFound)` → append `WARN: %s not found in tracker — skipping` to warnings, continue.
     - other `err != nil` → append `WARN: %s lookup failed: %v — skipping` to warnings, continue.
     - `closed == false` → continue silently.
   - Append `trackerCandidate{BranchName: name, IssueID: id, StoreRow: storeByName[name]}` to candidates.
4. Sort candidates by `BranchName` for deterministic output and reproducible E2E tests.

Candidate type:

```go
type trackerCandidate struct {
    BranchName string
    IssueID    string
    StoreRow   *store.BranchRow // nil → branch unknown to git-zf, no store flip after delete
}
```

## Prompter

New interface in `cmd/branch/prune_tracker_prompter.go`, parallel to `PrunePrompter`:

```go
// TrackerPrunePrompter resolves the per-branch reap action for the whole
// candidate batch in a single call.
type TrackerPrunePrompter interface {
    // DecideReap returns a map from candidate BranchName to action,
    // where action is one of: "safe", "force", "skip".
    // Caller must not invoke DecideReap with an empty candidates slice.
    DecideReap(ctx context.Context, candidates []trackerCandidate) (map[string]string, error)
}
```

Three implementations:

| Type | Wired when | Behavior |
|---|---|---|
| `huhTrackerPrunePrompter` | default (no action flag) | One `huh.Form` with one `huh.Group` containing N stacked `huh.NewSelect[string]` fields. `"safe"` pre-selected via `Value(...)`. Tab/Shift-Tab navigates between branches; ↑/↓ changes the selected option. |
| `fixedActionPrunePrompter{action}` | `--safe-delete` / `--force-delete` / `--skip-delete` | Returns `map[name] → action` directly without any UI. |
| scripted (test-only) | E2E tests | Returns a canned per-branch map injected by the test. |

`--dry-run` bypasses the prompter entirely: render summary and return before constructing it.

Per-branch action pointer-binding for the huh `Select.Value(...)` will be handled with a per-iteration `string` variable (`act := "safe"; ... .Value(&act); decisions[name] = func() string { return act }`) — no shared pointer footgun.

## Execution

After the prompter returns:

```go
type trackerPruneResult struct {
    Candidates []trackerCandidate
    Decisions  map[string]string // branchName → "safe"|"force"|"skip"
    Warnings   []string          // tracker lookup + git refusal lines
}
```

Per candidate, in `Candidates`-order (deterministic by branch name):

1. **Ref action:**
   - `"safe"` → `c.SafeDeleteBranch(name)`. If returns `git.ErrBranchNotMerged` → append `WARN: kept %s — git refused safe-delete (not merged into base)` to warnings AND skip the store flip (we won't lie about state). Continue.
   - `"force"` → `c.ForceDeleteBranch(name)`. Any error → wrapped and returned (treated as fatal — force-delete should not fail under normal circumstances).
   - `"skip"` → no ref action.
2. **Store flip:**
   - If `candidate.StoreRow != nil` → `s.UpdateBranchStatus(ctx, name, store.StatusIDClosed, &now)`.
   - If `candidate.StoreRow == nil` → no-op (branch unknown to git-zf; only the ref existed).
3. **Final summary line:** `Tracker-pruned: N safe, M forced, K skipped, W kept (refused).` followed by any accumulated warnings.

## New git client wrappers

Two new methods on `git.Client`, exposed through an expanded `pruner` interface in `cmd/branch/prune_tracker.go`:

```go
// SafeDeleteBranch wraps `git branch -d <name>`. Returns git.ErrBranchNotMerged
// when git refuses on "not fully merged" grounds; other errors are wrapped.
func (c *Client) SafeDeleteBranch(name string) error

// ForceDeleteBranch wraps `git branch -D <name>`. Any error is wrapped.
func (c *Client) ForceDeleteBranch(name string) error

// In package git.
var ErrBranchNotMerged = errors.New("git: branch not fully merged into HEAD/upstream")
```

The local `trackerPruner` interface in `prune_tracker.go` is the test seam (same shape as the existing `pruner` interface for `prune`):

```go
type trackerPruner interface {
    DefaultBaseBranch() (string, error)
    LocalBranchNames() ([]string, error)
    SafeDeleteBranch(name string) error
    ForceDeleteBranch(name string) error
}
```

## Store schema change

New migration `store/migrations/0005_add_closed_status.sql`:

```sql
INSERT INTO statuses (id, name) VALUES (3, 'closed');
```

New constants in `store/store.go`:

```go
const StatusIDClosed int64 = 3
const BranchStatusClosed BranchStatus = "closed"
```

`branch list --status=closed` is wired by extending `toStoreStatus` in `cmd/branch/branch.go` and adding `"closed"` to the options in `tui.BranchStatusFilter`. `--status=all` continues to include closed rows.

The existing `branch prune` "tip reachable from base" outcome continues to use `StatusIDMerged`. Only `branch prune-tracker` produces `StatusIDClosed`. The two statuses carry distinct meaning:

| Status | Meaning |
|---|---|
| `in_progress` | Active work; local ref exists; tracker issue (if any) is open. |
| `merged` | Work landed on base; tip reachable from base in git. |
| `closed` | Tracker issue closed; local ref retired by `prune-tracker` regardless of git merge state. |

## Testing strategy

Mirrors the existing prune E2E + unit split (see `cmd/branch/prune_e2e_test.go` and `branch_test.go`):

| File | Purpose |
|---|---|
| `cmd/branch/prune_tracker_test.go` | Pure unit tests: `extractIssueID` table-driven cases; `runPruneTracker` discovery against fake `trackerPruner` + fake `Tracker`; decision execution. Each scenario wrapped in `t.Run`. |
| `cmd/branch/prune_tracker_prompter_test.go` | Scripted prompter conformance + `fixedActionPrunePrompter` smoke tests. |
| `cmd/branch/prune_tracker_e2e_test.go` | One top-level `Test_PruneTracker_E2E` with shared setup (temp git repo + temp SQLite + fake tracker), nested `t.Run` per scenario: `--dry-run`; `--safe-delete` happy path (all branches merged into base, all deleted, store flipped to closed); `--safe-delete` with one branch unmerged (kept + warned, store NOT flipped for that one); `--force-delete`; `--skip-delete` (store flipped only, refs untouched); regex no-match (silently skipped); tracker transport error (warn + skip); 404 → `ErrIssueNotFound` (warn + skip); zero candidates (`"Nothing to prune from tracker."`); branch known to git but not in store (ref deleted, no store action). |
| `tracker/fake/fake.go` | Extend with `closed`, `unknown`, `errors` maps as described in the tracker-interface section. |
| `tracker/github/github_test.go`, `tracker/redmine/redmine_test.go` | `IsIssueClosed` against `httptest` fixtures: closed, open, 404→`ErrIssueNotFound`, 500→wrapped. |

`t.Run` is used per assertion / scenario throughout, per the project-wide Go testing convention.

## Files touched (preview)

```
NEW: cmd/branch/prune_tracker.go
NEW: cmd/branch/prune_tracker_prompter.go
NEW: cmd/branch/prune_tracker_test.go
NEW: cmd/branch/prune_tracker_prompter_test.go
NEW: cmd/branch/prune_tracker_e2e_test.go
NEW: store/migrations/0005_add_closed_status.sql

MODIFIED: cmd/branch/branch.go              (wire subcommand, extend toStoreStatus, add `closed` case)
MODIFIED: tui/branch.go                     (add BranchActionNamePruneTracker constant + option in BranchActionSelect; add "closed" option in BranchStatusFilter)
MODIFIED: tracker/tracker.go                (add IsIssueClosed, export ErrIssueNotFound)
MODIFIED: tracker/github/github.go          (implement IsIssueClosed)
MODIFIED: tracker/github/github_test.go     (test IsIssueClosed)
MODIFIED: tracker/redmine/redmine.go        (implement IsIssueClosed)
MODIFIED: tracker/redmine/redmine_test.go   (test IsIssueClosed)
MODIFIED: tracker/fake/fake.go              (extend with closed/unknown/errors maps + IsIssueClosed)
MODIFIED: git/client.go                     (SafeDeleteBranch, ForceDeleteBranch, ErrBranchNotMerged)
MODIFIED: store/store.go                    (StatusIDClosed, BranchStatusClosed)
MODIFIED: ROADMAP.md                        (strike the "Related branch to closed issue" entry)
```
