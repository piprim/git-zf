# Design: Branch-prune refactor for end-to-end testability + non-interactive mode

**Date:** 2026-05-26
**Status:** approved

## Overview

`cmd/branch/branch.go`'s prune flow (`runPrune` + `executePrune`) has one inline `huh.NewForm(tui.BranchPruneConfirm(...))` call that gates a destructive store mutation (`s.DeleteBranch` + `s.UpdateBranchStatus`). Today, four unit tests in `branch_test.go` use a `fakePruner` to exercise the dry-run discovery logic, but the non-dry-run path — the actual confirm → execute pipeline — has no automated coverage. Manual smoke testing is the only thing keeping it honest.

This spec applies the same pattern that worked for `close.go` and `start.go`: extract a `PrunePrompter` interface, ship three implementations, and rewrite the test surface on a real-on-disk rig. As a small but useful side effect, the third implementation is an auto-confirm prompter wired by a new `--yes` / `-y` flag — closing a gap for CI and cron-driven prune jobs.

No user-visible behaviour change beyond the new `--yes` flag. Same prompts, same wording, same exit codes.

## What this refactor does NOT change

- The dry-run flag (`--dry-run`) and its semantics.
- The base-branch override (`--base`).
- The categorization logic (`pruneResult{toDelete, toMerge}`).
- The store-mutation order (delete first, mark-merged second) in `executePrune`.
- The `pruner` interface — already exists, already abstracts the git surface, already tested.
- The `tui.BranchPruneConfirm` form constructor.

## Prompter surface

A new `PrunePrompter` interface lives in `cmd/branch/prune_prompter.go`. One method, one contract:

```go
// PrunePrompter resolves the single user-facing decision in the prune flow:
// whether to proceed with the destructive store mutations after the summary
// has been printed.
type PrunePrompter interface {
    // ConfirmPrune is called only when there is at least one branch to delete
    // or to mark merged, AND the run is not a dry-run.
    ConfirmPrune(ctx context.Context, toDelete, toMerge int) (confirmed bool, err error)
}
```

Three implementations:

- **`huhPrunePrompter`** (production) — wraps the existing `huh.NewForm(tui.BranchPruneConfirm(...))` call.
- **`autoConfirmPrunePrompter`** (`--yes`) — `ConfirmPrune` returns `(true, nil)` unconditionally. Constructed when the operator passes `--yes` / `-y`.
- **`scriptedPrunePrompter`** (tests, in `prune_prompter_test.go`) — exposes `Confirm bool`, `ConfirmErr error`, plus a call-counter and last-args fields so tests can assert on the prompter contract.

### Why only one method

The prune flow is simpler than close or start. There's exactly one user decision point: "I see the summary; proceed?". A larger interface would invent ceremony for a flow that doesn't need it.

## `runPrune` signature change

```go
// Before:
func runPrune(ctx context.Context, w io.Writer, s *store.Store, pruner pruner, flags pruneFlags) error

// After:
func runPrune(ctx context.Context, w io.Writer, s *store.Store, pruner pruner, prompter PrunePrompter, flags pruneFlags) error
```

Inside `runPrune`, the inline form-opening block is replaced with `prompter.ConfirmPrune(...)`. The pre-confirm logic (dry-run check, "Nothing to prune" short-circuit, summary printing) stays exactly where it is.

## `--yes` flag

`pruneFlags` gains a `yes bool` field. `pruneCmd` registers `--yes` / `-y` via `cmd.Flags().BoolVarP(...)`. `pruneRunE` picks the prompter:

```go
var prompter PrunePrompter = newHuhPrunePrompter()
if flags.yes {
    prompter = newAutoConfirmPrunePrompter()
}
```

The interactive `branch` action menu (`tui.BranchActionNamePrune`) does NOT plumb `--yes` — it calls `b.pruneRunE(cmd, pruneFlags{})` with an empty flag set, which is correct: an operator in a TUI session is already interactive and doesn't need auto-confirm.

## IO routing fix on `executePrune`

`executePrune` currently writes its success line through `fmt.Printf` to `os.Stdout`:

```go
fmt.Printf("Pruned: %d deleted, %d marked merged.\n", ...)
```

That violates the project's IO-injection memory rule (`feedback_respect_io_injection`). The fix: thread the `w io.Writer` that `runPrune` already accepts down into `executePrune`, and replace `fmt.Printf` with `fmt.Fprintf(w, ...)`. This also makes the success line visible to test buffers — necessary for the E2E happy-path assertion.

## Test rig

Today's `TestRunBranchPrune` uses a `fakePruner` (an interface mock that returns canned `LocalBranchNames` / `IsMergedInto` / `DefaultBaseBranch` values) plus a real `*store.Store` opened in `t.TempDir()`. That tested the discovery + categorization logic, but never exercised the confirm gate or the destructive execute path.

The new rig (`pruneTestRig` in `cmd/branch/prune_e2e_test.go`) mirrors the close- and start-flow rigs:

- Real temp git repo with one commit on `master`
- `git.NewClientAt(&pkg.IO{...}, dir)` as the real production pruner
- `store.Open(t.Context(), dir)` for the real seeded store
- Helpers: `seedIssueAndBranch`, `createGitBranch`, `mergeBranchIntoMaster`
- Stdout/stderr buffers so tests assert on rendered output

The four existing dry-run subtests are **rewritten** on this rig, not added alongside. The `fakePruner` and `insertTestBranch` helpers in `branch_test.go` are deleted (unless `insertTestBranch` is used by `TestBranchList`, in which case it stays).

### Test coverage targets

Seven `Test*` functions, each wrapping its assertion blocks in named `t.Run` subtests (memory: `feedback_t_run`):

1. **`TestRunPrune_HappyPath_DeleteAndMerge`** — three branches seeded (delete / merge / active). Asserts store mutations, prompter was called once with the right counts, success line on stdout.

2. **`TestRunPrune_DryRun_ReportsDeletedBranch`** — equivalent of the current `dry-run reports branch missing from local refs` subtest.

3. **`TestRunPrune_DryRun_ReportsMergedBranch`** — equivalent of `dry-run reports branch merged into base`.

4. **`TestRunPrune_DryRun_NothingToPrune`** — equivalent of `dry-run prints 'Nothing to prune'`.

5. **`TestRunPrune_DryRun_MixedCategories`** — equivalent of `dry-run reports deleted and merged branches but omits active ones`.

6. **`TestRunPrune_UserAbortsAtConfirm`** — operator declines the confirm form. Asserts store unchanged, "Aborted." on stdout, prompter called exactly once.

7. **`TestRunPrune_YesFlagSkipsConfirm`** — uses `autoConfirmPrunePrompter` directly (mirroring what `pruneRunE` does when `--yes` is set). Asserts the destructive mutations ran without operator input.

## Risks and open questions

- **Risk: deleting `fakePruner` removes a useful unit-test seam.** Mitigation: the real-on-disk rig actually exercises the production `*git.Client` paths, so coverage is broader, not narrower. The `pruner` interface itself stays in the production code — tests just don't use a fake against it anymore.

- **Risk: `mergeBranchIntoMaster` rig helper does real git ops (commit, merge), slowing tests.** Mitigation: each test runs in its own `t.TempDir()`, so they parallelise cleanly. Empirically the close- and start-flow E2E tests run in ~200ms total — prune will be in the same range.

- **Open: should we also unwrap `executePrune`'s second return value (the success line) into structured event output (e.g., for `--json`)?** No — out of scope. The current text output is the supported interface; a JSON mode would be a separate ROADMAP item.

- **Open: `--yes` shorthand `-y` — does it collide with any existing flag?** Verified at design time: `pruneCmd` only registers `--dry-run` (`-d`-less long form) and `--base` (long form only). `-y` is free.

## Documentation updates

- `README.md`: add a "Testing the prune flow" subsection alongside the existing close/start subsections, including the `mise exec -- go test … -run "^TestRunPrune_"` invocation and a `branch prune --yes` example.
- `cmd/branch/branch.go`: docstrings on the modified `runPrune` and `executePrune`, plus a one-liner on the new `pruneFlags.yes` field.
- `cmd/branch/prune_prompter.go`: package- and interface-level docstrings (matching the level of detail in `cmd/issue/close_prompter.go`).
- `ROADMAP.md`: strike item 2 of "End-to-end testability of interactive flows" with a shipped-date suffix (matching item 1's pattern).
