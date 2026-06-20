# Roadmap

## Enhancement

- `git zf review request` should ask to update the issue status in the tracker like `issue start` does.

### CODE SMELLS

The single inventory of issues. Each row links to the fix in **RECOMMENDATIONS**
(high-level) or **REFACTORING SUGGESTIONS** (concrete, `R*`) below. Severity tags
risk × yield: "High (latent bug)" is a correctness hazard; a plain "High" on a
duplication row means high churn/yield, not danger.

#### Duplication (highest-yield category)

| # | Smell | Location(s) | Severity |
|---|-------|-------------|----------|
| 1 | `git.NewClient(&pkg.IO{In/Out/Err})` + `SetRemote` preamble copy-pasted | `cmd/issue/start.go:41-53`, `cmd/issue/close.go:41-54`, `cmd/branch/branch.go:264-275`, `cmd/branch/prune_tracker.go:271-282`; `cmd/commit/commit.go:75` (NewClient only — no SetRemote) | High |
| 2 | `git.CommitOptions{All, Amend, …}` field-by-field map from `tui.CommitOption` | `cmd/issue/close.go:299-307, 435-443, 530-538` | High |
| 3 | `IssueHint{…}` + `Prefill()` + `prefill["subject"] = fmt.Sprintf(...)` block | `cmd/issue/close.go:286-292, 422-428, 517-523` | Medium |
| 4 | Compose-message-then-commit tail (identical 8 lines) | end of `doSquashCommit`, `doRebaseClose`, `doClassicClose` | Medium |
| 5 | "fetch statuses → pick → update, all non-fatal" tracker routine | `cmd/issue/close.go:331-351` vs `cmd/issue/start.go:335-361` | Medium |
| 6 | `createBranchFlow` vs `createWorktreeFlow` — same prepare→resolve→confirm→persist→tracker shape | `cmd/issue/start.go:196-245` vs `247-311` | Medium |
| 7 | Row-scan loop (`Scan` + `parseSQLiteTime` + append) + near-identical SELECT | `store/store.go:237-264` vs `295-320` (+ query at `216-219`/`275-280`) | Medium |
| 8 | `includeProj := len(m.projects) > 1; m.table.SetRows(applyFilters(...))` repeated | `tui/issue.go:396-397, 423-424, 436-437, 447-448` (+ `tui/issue.go:208`, the same shape outside `Update`) | Medium |

#### Other smells

| # | Smell | Location | Severity |
|---|-------|----------|----------|
| 9 | Magic constant `2` instead of `store.StatusIDMerged` | `cmd/branch/branch.go:394` | High (latent bug) |
| 10 | `git.Client` god object — ~30 methods, file split by file not concern | `git/git.go` (556 ln) + `merge.go` + `system.go` | Low–Med |
| 11 | Long method `issueTableModel.Update` — nested modal state machine, 90+ lines | `tui/issue.go:384-477` | Medium |
| 12 | cmd→cmd coupling: `cmd/branch` imports `cmd/issue` for `BuildStartDeps`/`RunIssueStart` | `cmd/branch/branch.go:14` | Medium (architectural) |
| 13 | Repeated `//nolint:wrapcheck // prompter error already wrapped` on every prompter return | `close.go` ×6, throughout | Low (convention worth a doc, not 6 inline comments) |
| 14 | config/store reach into `git` for repo discovery — `config.RepoDir/RepoPath` and `store.OpenRepo` build a full `git.Client`, making go-git a transitive dependency of nearly the whole tree | `config/config.go:163-164, 219`; `store/store.go:360-361` | Medium (architectural) |
| 15 | Domain/transport leakage — `issue.Issue` embeds `tracker.Issue`, and the issue domain package also defines the `Prompter` interface + `GetFromUser/GetFromTracker`. Three Issue shapes (`tracker.Issue`, `issue.Issue`, `store.Issue`) + `store.IssueRow`, with undocumented mapping | `issue/issue.go:31`; `tracker/tracker.go:13`; `store/store.go:27` | Medium (architectural) |
| 16 | Global Viper singleton — `cmd/root` mutates the package-global viper and `config.Load()` reads it; load order-dependent and hard to exercise in isolation (vs. `viper.New()` per load) | `cmd/root.go:117-137`; `config/config.go:98+` | Low–Med (testability) |

### RECOMMENDATIONS

1. Extract the `issue-start/close` use cases out of `cmd/`. Move `RunIssueStart`, `StartDeps`, `StartPrompter`
   (and the close equivalents) into an application package, e.g. `app/issueflow` (or fold into the issue domain package if you keep it framework-free).
   Both `cmd/issue` and `cmd/branch` then depend on app,
   eliminating the cmd→cmd import and giving you one obvious home for the flow.
   Rationale: removes the only inappropriate layering edge and clarifies that these functions are application services, not CLI glue.
2. Introduce a `repo/discovery` helper for `git-dir` resolution and have config and store depend on that
   narrow function rather than constructing a full `git.Client`. Even a single `func GitDir() (string, error)`
   interface would shrink the dependency surface.
   Rationale: stops go-git leaking transitively through config into the whole tree.
3. Collapse the duplicated tracker-status update into one helper (e.g. `tracker.ApplyStatus(ctx, t, io, issueID, trackerType, pick func(...)...))` 
   and add a `git.CommitOptions.FromTUI(opts)` constructor  (or have `ComposeMessage` return `git.CommitOptions` directly) to kill the 3× field mapping.
   Merge `createBranchFlow/createWorktreeFlow` behind a creator strategy.
   Rationale: each duplication is a place a future field gets added in 2 of 3 sites.
4. Replace the literal 2 in `executePrune` with `store.StatusIDMerged`.
   Rationale: trivial fix, removes a real correctness hazard if status IDs ever change.
5. Optionally segment `git.Client` into role interfaces (`Committer`, `Merger`, `BranchManager`,
   `RemoteResolver`) defined where consumed. You already do this ad-hoc; formalizing it would let `merge.go` evolve independently.
   Lower priority — the consumer interfaces already deliver most of the benefit.
6. Document the three Issue types and consider moving the `issue.Prompter` interface + `GetFromUser/GetFromTracker`
   into the application layer so the issue domain package holds only the entity
   and branch-naming-adjacent logic. Rationale: keeps the domain free of
   UI-orchestration concerns.
7. Use `viper.New()` per config load threaded through `initConfig/config.Load` instead of the global.
   Rationale: removes hidden global state; makes config fully unit-testable.

### REFACTORING SUGGESTIONS

#### R1 — Extract client/deps construction (smell #1)

The four call sites differ only in error message and whether a tracker is built. buildCloseDeps and
BuildStartDeps are already 90% identical.

Before (repeated 5×):

```go
client, err := git.NewClient(&pkg.IO{
    In:  cmd.InOrStdin(),
    Out: cmd.OutOrStdout(),
    Err: cmd.ErrOrStderr(),
})
if err != nil {
    return ..., fmt.Errorf("not a git repository: %w", err)
}
if cfg.Branch.Remote != "" {
    client.SetRemote(cfg.Branch.Remote)
}
```

After — one helper (e.g. `internal/pkg` or a small `cmdutil`):

```go
// NewClientForCmd builds a git client wired to the command's IO streams and
// pins the configured remote.
func NewClientForCmd(cmd *cobra.Command, cfg *config.AppConfig) (*git.Client, error) {
    c, err := git.NewClient(&pkg.IO{
        In: cmd.InOrStdin(), Out: cmd.OutOrStdout(), Err: cmd.ErrOrStderr(),
    })
    if err != nil {
        return nil, fmt.Errorf("not a git repository: %w", err)
    }
    if cfg.Branch.Remote != "" {
        c.SetRemote(cfg.Branch.Remote)
    }
    return c, nil
}
```

#### R2 — git.CommitOptions constructor (smell #2) [DONE]

Before (3×):

```go
if err := mc.client.Commit(ctx, msg, git.CommitOptions{
    All: opts.All, Amend: opts.Amend, NoVerify: opts.NoVerify,
    Signoff: opts.Signoff, AllowEmpty: opts.AllowEmpty, Author: opts.Author,
}); err != nil { ... }
```

After — add to git (or have `ComposeMessage` return `git.CommitOptions` directly, since `tui.CommitOption`
has no other consumer at the commit site):

`func CommitOptionsFromTUI(o tui.CommitOption) CommitOptions { ... }`

This is the single highest-risk duplication: a new commit flag added to one of the three strategies and forgotten in the others is silent. (ROADMAP rec #3.)

#### R3 — Extract the strategy commit tail (smells #3 + #4) [DONE]

All three do*Close helpers end with: build IssueHint → Prefill → set subject → ComposeMessage → Commit.
Extract:

```go
// composeAndCommit builds the prefill from issue context + a strategy subject,
// drives the commit form, and commits.
func composeAndCommit(ctx context.Context, mc mergeContext, prompter ClosePrompter, subject string) error {
    hint := commitpkg.IssueHint{IssueID: mc.pickedBranch.IssueSlug, BranchType: mc.pickedBranch.Type}
    prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
    prefill["subject"] = subject

    msg, opts, err := prompter.ComposeMessage(ctx, prefill)
    if err != nil {
        return err //nolint:wrapcheck
    }
    return mc.client.Commit(ctx, msg, git.CommitOptionsFromTUI(opts))
}
```

Each strategy then computes only its own subject string and calls this. Removes ~30 duplicated lines and folds in R2.

#### R4 — Single tracker-status helper (smell #5)

`updateClosedStatus` (close) and `updateTrackerStatus` (start) share the exact `ListStatuses` → `PickTrackerStatus` → `UpdateIssueStatus`, all-non-fatal sequence.
Extract a free function:

```go
func applyTrackerStatus(ctx context.Context, t tracker.Tracker, errW io.Writer,
    issueID, trackerType string,
    pick func(ctx context.Context, issueID, trackerType string, statuses []string) (string, error))
{
    // fetch → pick → update, warn-and-return on each error
}
```

Both `ClosePrompter.PickTrackerStatus` and `StartPrompter.PickTrackerStatus` have identical signatures,
so pick binds cleanly. (ROADMAP rec #3.)

#### R5 — store scan helper (smell #7)

`ListBranches` and `ListBranchesByIssueSlugs` share the per-row scan. Extract:

```go
func scanBranchRow(rows *sql.Rows) (BranchRow, error) {
    var r BranchRow
    var createdAt string
    if err := rows.Scan(&r.IssueID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status,
&createdAt); err != nil {
        return r, fmt.Errorf("scan branch row: %w", err)
    }
    t, err := parseSQLiteTime(createdAt)
    if err != nil {
        return r, fmt.Errorf("parse branch created_at %q: %w", createdAt, err)
    }
    r.CreatedAt = t
    return r, nil
}
```

Also hoist the shared SELECT body to a `const branchSelectBase`. Keeps the two queries from drifting in column order (a real scan-misalignment hazard).

#### R6 — TUI refreshRows method (smell #8)

Before (4×, inside Update):
```go
includeProj := len(m.projects) > 1;
m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))
```

After:

```go
func (m *issueTableModel) refreshRows() {
    includeProj := len(m.projects) > 1
    m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))
}
```

Then split Update into `updatePicking` / `updateFiltering` / `updateNormal` key handlers to tame smell #11.

#### R7 — Magic constant (smell #9) [DONE]

`cmd/branch/branch.go:394`: `s.UpdateBranchStatus(ctx, ..., 2, &now)` → `store.StatusIDMerged`. One-line, removes a correctness hazard if IDs ever change. Do this first.

#### R8 — Merge createBranchFlow/createWorktreeFlow (smell #6)

Both follow prepare→resolve-conflict→(confirm)→create→persist→tracker.
Extract the shared head (prepare + resolve + nil-guard + branchName) and tail (persist + tracker), leaving only the create-vs-worktree confirm/exec middle.
Slightly more involved than R1–R7 because the confirm message and success output differ; do it after the mechanical wins.

#### PRIORITY ORDER

1. ~~R7 — magic 2 → `StatusIDMerged` — trivial, fixes a latent correctness bug.~~ [DONE]
2. ~~R2 + R3 — `CommitOptionsFromTUI` + `composeAndCommit` — kills the most dangerous duplication (silent missing commit flag) and 30 lines.~~ [DONE]
3. R1 — `NewClientForCmd` — mechanical, 5 sites, low risk.
4. R4 — `applyTrackerStatus` — removes the second cross-file duplication.
5. R5 — `scanBranchRow` — guards against column drift.
6. R6 — `refreshRows` + split Update — readability of the TUI state machine.
7. R8 — merge create flows — higher effort, lower urgency.
8. (Architectural, separate effort) ROADMAP rec #1/#2 (smells #12, #14): lift `RunIssueStart/close` use-cases into an
   `app/` package to break the `cmd/branch` → `cmd/issue` import, and introduce a narrow `GitDir()` discovery
   helper so config/store stop pulling in go-git transitively. Smells #15 (Issue-type leakage, rec #6) and
   #16 (viper global, rec #7) ride along with this layering pass.

#### DEPENDENCIES

- R2 must land before/with R3 — `composeAndCommit` calls `CommitOptionsFromTUI`. Do them in one change.
- R3 depends on R2's decision: if you instead make `ComposeMessage` return `git.CommitOptions` directly,
  R2 disappears into the prompter signature change — pick one approach, not both.
- R6's two parts are independent but naturally land together (extract `refreshRows`, then split Update).
- R1 and R4 are independent of everything and of each other — parallelizable.
- R8 builds on the codebase as-is; if you do the architectural move (#8) first, do R1/R4 inside the
  new app/ package to avoid redoing them.
- Each of R1–R7 is covered by existing E2E tests (`close_e2e_test.go`, `start_e2e_test.go`,
  `prune_e2e_test.go`, `store_test.go`) — `run mise exec -- go test ./...` after each; no test changes
  should be required since these are behavior-preserving extractions.

### End-to-end testability of interactive flows

Low priority (manual testing is fine, or surface is too small): `cmd/commit/commit.go` (thin wrapper around `commitpkg.FillOutForm`, already tested), `cmd/config/init.go` (config-file ops with low blast radius), `cmd/issue/issue.go` (top-level action dispatcher).

### Open: `git zf branch merge`

Still a placeholder (`cmd/branch/branch.go:mergeRunE`). The original bug-section note about the `issue close` UX ("it is not an issue, tell the user to use `git zf branch merge`") is the design seam: `branch merge` should take over the merge surface for non-issue branches, and `issue close` should redirect when invoked on one.
