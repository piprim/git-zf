# Design: Issue-start refactor for end-to-end testability

**Date:** 2026-05-26
**Status:** draft

## Overview

`cmd/issue/start.go` is the most complex interactive path in the project. It drives five huh forms directly (`WorktreeToggle`, `IssueTrackerToggle`, two `IssueConfirm` calls, `IssueStatusPicker`), delegates to two more in `cmd/issue/conflict.go` (`BranchConflictPicker`, `VariantLabelInput`), and to three more in `issue/issue.go` (`tui.IssueInput`, `tui.IssueTrackerError`, `tui.IssueTrackerPicker`). Together they wire branch creation, worktree creation, store seeding, and a tracker status update — but today the only automated coverage of `RunIssueStart` is `TestWorktreePath`, which tests a pure path-computation helper. Everything between the form prompts and the on-disk side effects is verified by manual smoke testing.

This spec extracts a `StartPrompter` interface so the whole flow becomes end-to-end testable without driving a real TUI, mirroring the `ClosePrompter` pattern already shipped for `cmd/issue/close.go`. After the refactor: production callers wire a `huhPrompter`; tests inject a `scriptedPrompter` and assert on observable side effects (created branch ref, store row, tracker call, worktree directory).

No user-visible behaviour change. Same prompts, same messages, same exit codes. The shape of the refactor is deliberately the same as the close-flow refactor so a future contributor reading both files sees one pattern, not two.

## What the refactor does NOT change

- The set of huh forms, their wording, or their ordering.
- The flag surface (`--variant` stays, no new flags in this spec; non-interactive flags like `--yes` are a deferred follow-up once the test scaffolding lands).
- The `branch` package or store schema.
- The `tracker` package or any of the adapters.
- The conflict-resolution UX (still a 3-option picker with the same labels).
- The behaviour-difference between `issue start` (tracker-first toggle) and `branch new` (manual-first toggle) — both still go through `RunIssueStart`.

## Prompter surface

A single `StartPrompter` interface lives in `cmd/issue/start_prompter.go`. Each method maps 1:1 to a form call in the pre-refactor flow.

```go
// StartPrompter resolves every user-facing decision in the issue-start flow.
// Production wires huhPrompter; tests wire scriptedPrompter.
type StartPrompter interface {
    // PickUseTracker drives the "fetch from tracker?" toggle. Called only
    // when cfg.IssueTracker.Type != "". The trackerFirst arg controls the
    // pre-selected option (true for `issue start`, false for `branch new`).
    PickUseTracker(ctx context.Context, trackerType string, trackerFirst bool) (use bool, err error)

    // PickIssueFromUser drives the manual issue-input form when no tracker
    // is configured OR the operator declined PickUseTracker.
    PickIssueFromUser(ctx context.Context, allowedTypes []string) (*issue.Issue, error)

    // PickIssueFromTracker drives the tracker-list picker form.
    PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*issue.Issue, error)

    // NotifyTrackerError shows the "tracker returned an error / empty list"
    // note before falling back to PickIssueFromUser. Returning a non-nil error
    // aborts the flow.
    NotifyTrackerError(ctx context.Context, message string) error

    // PickUseWorktree drives the "worktree vs plain branch?" toggle. Called
    // only when cfg.Branch.UseWorktree == nil (config override is absent).
    PickUseWorktree(ctx context.Context) (use bool, err error)

    // ConfirmCreateBranch gates `client.CreateBranch`. message is the full
    // "Create branch %q based on %q?" string.
    ConfirmCreateBranch(ctx context.Context, message string) (confirmed bool, err error)

    // ConfirmCreateWorktree gates `client.CreateWorktree`. message is the
    // full "Create worktree %q at %q based on %q?" string.
    ConfirmCreateWorktree(ctx context.Context, message string) (confirmed bool, err error)

    // PickTrackerStatus is identical in shape to ClosePrompter.PickTrackerStatus.
    PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

    // ResolveBranchConflict drives the conflict-picker loop. Implementations
    // own the loop: when the user picks "variant", they re-prompt for a label
    // and re-check existence. Returning (nil, nil) means "abort cleanly"
    // (operator chose Abort or the existing branch was checked out and the
    // caller should stop without creating anything).
    ResolveBranchConflict(ctx context.Context, client BranchExistenceChecker, b *branch.Branch, picked *issue.Issue) (*branch.Branch, error)
}

// BranchExistenceChecker is the slice of *git.Client the prompter needs for
// conflict resolution. Defined to keep the prompter contract decoupled from
// the full git.Client surface for tests.
type BranchExistenceChecker interface {
    BranchExists(name string) (bool, error)
    Checkout(ctx context.Context, branch string) error
    IO() *pkg.IO
}
```

### Why `ResolveBranchConflict` stays a single method

The conflict flow is a loop: check existence → pick action → if "variant", input label → re-check. Splitting it into per-form methods would force the test to script the same sequence the production code already encodes. Keeping the loop owned by the prompter means:

- The production `huhPrompter.ResolveBranchConflict` is the existing `resolveBranchConflict` function body, lifted verbatim.
- The test `scriptedPrompter.ResolveBranchConflict` returns a pre-computed `*branch.Branch` (or nil for abort) without re-implementing the loop.

This deviates from the close-flow pattern (where every form is a separate prompter method), but the close flow's forms are independent — there's no loop. For start, atomic-loop semantics matter.

### Why `issue.GetFromUser` / `issue.GetFromTracker` change

These two functions in `issue/issue.go` currently open huh forms themselves. To E2E-test the start flow without a TUI driver, they MUST be threaded through the prompter. Two options were considered:

1. **Move them into `cmd/issue/`** as methods that take a `StartPrompter`. Cleanest separation but pulls helpers across package boundaries.
2. **Add a `Prompter` parameter to both** (typed as an interface satisfied by `StartPrompter`). Keeps the `issue` package's API surface; just inverts control.

This spec chooses (2). The `issue` package gains a small `Prompter` interface with three methods (`PickIssueFromUser`, `PickIssueFromTracker`, `NotifyTrackerError`); `StartPrompter` embeds it (or vice versa). The functions become:

```go
// issue/issue.go
type Prompter interface {
    PickIssueFromUser(ctx context.Context, allowedTypes []string) (*Issue, error)
    PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*Issue, error)
    NotifyTrackerError(ctx context.Context, message string) error
}

func GetFromUser(ctx context.Context, p Prompter, allowedTypes []string) (*Issue, error)
func GetFromTracker(ctx context.Context, p Prompter, t tracker.Tracker, allowedTypes []string) (*Issue, error)
```

The two callers in `cmd/issue/start.go` (`getFromTracker` and the no-tracker fallback) pass the prompter; the rest of their signatures stay the same.

## `startDeps` struct

Same shape as `closeDeps`. Built once per invocation by `buildStartDeps`:

```go
type startDeps struct {
    client  *git.Client
    cfg     *config.AppConfig
    tracker tracker.Tracker // nil when cfg.IssueTracker.Type == "" OR factory failed (warn)
    flags   issue.IssueStartFlags
}

func buildStartDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig, flags issue.IssueStartFlags) (startDeps, error)
```

Note: unlike `closeDeps`, no `store *store.Store` field — `persist` continues to open its own short-lived store via `store.OpenRepo`. Reason: `persist` is called from two paths (`createBranch`, `createWorktree`) and only briefly; lifting the store into `startDeps` would force callers to manage its lifecycle for the full RunIssueStart duration. Acceptable as-is. (Tests inject the test directory via the rig and `persist` finds it via `store.OpenRepo` — same way the production code does.)

## Entry point

`closeRunE` shrinks to a thin shim; `RunIssueStart` becomes the non-interactive core:

```go
func (i Issue) startRunE(cmd *cobra.Command, _ []string) error {
    variant, err := cmd.Flags().GetString("variant")
    if err != nil {
        return fmt.Errorf("read --variant flag: %w", err)
    }

    flags := issue.IssueStartFlags{TrackerFirst: true, Variant: variant}
    deps, err := buildStartDeps(cmd.Context(), cmd, i.appConfig, flags)
    if err != nil {
        return err
    }

    return RunIssueStart(cmd.Context(), deps, newHuhStartPrompter(deps.client, i.appConfig))
}

// RunIssueStart is the prompter-driven core. Exported because cmd/branch/branch.go
// also calls it (with trackerFirst=false). Tests call it directly with a
// scriptedPrompter.
func RunIssueStart(ctx context.Context, deps startDeps, prompter StartPrompter) error {
    // (full body — see plan)
}
```

The method `(i Issue) RunIssueStart` becomes a free function `RunIssueStart` taking `startDeps`. `cmd/branch/branch.go`'s `newRunE` adapts:

```go
func (b Branch) newRunE(cmd *cobra.Command, _ []string) error {
    variant, err := cmd.Flags().GetString("variant")
    if err != nil {
        return fmt.Errorf("read --variant flag: %w", err)
    }

    flags := issue.IssueStartFlags{TrackerFirst: false, Variant: variant}
    deps, err := buildStartDeps(cmd.Context(), cmd, b.appConfig, flags)
    if err != nil {
        return err
    }

    return RunIssueStart(cmd.Context(), deps, newHuhStartPrompter(deps.client, b.appConfig))
}
```

The `issuecmd.New(b.appConfig)` indirection in `cmd/branch/branch.go` goes away — there's no longer an `Issue` struct method to host `RunIssueStart`, so the `cmd/branch` package imports the free function directly.

## Side-effect routing

Every print currently in `start.go` that goes through `fmt.Println` or `cmd.OutOrStderr()` migrates to `deps.client.IO().Out` / `deps.client.IO().Err`, following the project rule (memory: `feedback_respect_io_injection`). Concretely:

- `fmt.Println("Aborted.")` → `fmt.Fprintln(deps.client.IO().Out, "Aborted.")`
- `fmt.Printf("Switched to new branch ...\n", ...)` → `fmt.Fprintf(deps.client.IO().Out, ...)`
- `fmt.Fprintf(cmd.OutOrStderr(), "warning: ...")` → `fmt.Fprintf(deps.client.IO().Err, "warning: ...")`

This is a side-effect of the refactor, not a goal — but it's necessary because tests inject `pkg.IO{Out: stdout, Err: stderr}` through `git.NewClientAt` and assert on the buffers.

## Test rig

`cmd/issue/start_e2e_test.go` reuses the same tracker fake (`tracker/fake/`) and the same approach as `cmd/issue/close_e2e_test.go`, with one difference: the rig sets up a repo WITHOUT a seeded branch (since start CREATES the branch). The rig file exports:

```go
type startTestRig struct {
    dir     string
    client  *git.Client
    tracker *fake.Tracker
    cfg     *config.AppConfig
    stdout  *bytes.Buffer
    stderr  *bytes.Buffer
}

func newStartRig(t *testing.T) *startTestRig
func (r *startTestRig) deps(flags issue.IssueStartFlags) startDeps
```

The rig creates a temp git repo with one initial commit on `main`, an empty store directory (the start flow seeds the store via `persist`), and a fake tracker. Tests configure the `scriptedPrompter` and call `RunIssueStart(t.Context(), rig.deps(flags), prompter)`.

### Test coverage targets

Each test wraps assertion blocks in named `t.Run` subtests (memory: `feedback_t_run`).

1. **`TestRunIssueStart_BranchHappyPath_NoTracker`** — manual issue input, no tracker, plain branch (not worktree). Asserts: branch ref exists, store row exists with the expected name/type/status, no tracker calls.

2. **`TestRunIssueStart_BranchHappyPath_WithTracker`** — tracker configured, operator accepts the toggle, picks an issue from the fake tracker, plain branch. Asserts: branch ref, store row, exactly one `UpdateIssueStatus` recorded with the picked status.

3. **`TestRunIssueStart_WorktreeHappyPath`** — same as (1) but `UseWorktree: true`. Asserts: worktree directory exists at the computed path, branch ref points to it, store row, output mentions the `cd <path>` hint.

4. **`TestRunIssueStart_BranchUserAbortsAtConfirm`** — operator says "no" at the create-branch confirm. Asserts: no branch ref, no store row, no tracker call, stdout contains "Aborted.".

5. **`TestRunIssueStart_VariantOnCollision`** — pre-seed the deterministic branch on disk, then run start; the scripted prompter's `ResolveBranchConflict` returns a 4-part branch. Asserts: 4-part ref exists, store row carries the 4-part name.

6. **`TestRunIssueStart_AbortOnCollision`** — same setup; prompter's `ResolveBranchConflict` returns `(nil, nil)` (operator chose Abort). Asserts: no new ref, no store row, no tracker call.

7. **`TestRunIssueStart_TrackerListErrorFallsBackToManual`** — fake tracker returns an error from `ListIssues`. Asserts: `NotifyTrackerError` was called, then `PickIssueFromUser` was called, then the flow proceeds normally.

8. **`TestRunIssueStart_NoTrackerStatusUpdate`** — tracker is configured but `PickTrackerStatus` returns `""`. Asserts: branch is created normally, tracker has zero `RecordedUpdates`.

## Migration plan summary (deferred to the implementation plan)

The implementation plan will land in `docs/superpowers/plans/2026-05-26-issue-start-refactor.md` and decompose this spec into ~10 tasks roughly mirroring the close-flow plan:

1. `StartPrompter` interface + `scriptedPrompter` test helper
2. `issue.Prompter` sub-interface and updated `GetFromUser` / `GetFromTracker` signatures
3. `huhStartPrompter` production implementation
4. Extract `startDeps` + `buildStartDeps`; lift `RunIssueStart` to a free function; thread prompter through `createBranch`, `createWorktree`, `updateTrackerIssueStatus`
5. Migrate `resolveBranchConflict` into `huhStartPrompter.ResolveBranchConflict`
6. Adapt `cmd/branch/branch.go`'s `newRunE` to the new entry point
7. Tracker fake — already exists, no work needed
8. `startTestRig` + the 8 E2E tests above
9. Documentation: docstrings on `RunIssueStart`, `StartPrompter`, `huhStartPrompter`, `startDeps`, `buildStartDeps`; README "Testing the start flow" subsection

## Risks and open questions

- **Risk: changing `issue.GetFromUser` / `issue.GetFromTracker` signatures is a breaking change to the `issue` package's exported API.** Mitigation: both callers are in this repo (`cmd/issue/start.go`). External consumers are unlikely (this is a CLI binary, not a library). Acceptable break.

- **Risk: `RunIssueStart` becoming a free function changes its identifier from `(i Issue) RunIssueStart` to just `RunIssueStart`.** Mitigation: only one external caller (`cmd/branch/branch.go`) and it must already be updated anyway. The method-vs-free distinction adds no value once `i.appConfig` moves into `startDeps.cfg`.

- **Open: should `persist` migrate into `startDeps`?** Currently it opens its own short-lived store via `store.OpenRepo` on each call. Tests work either way — the store lives in the temp repo dir which `store.OpenRepo` will discover. Leaving `persist` as-is in the first cut; a follow-up can lift the store into `startDeps` if it stops being one-shot (e.g. multi-branch creation).

- **Open: should the conflict resolver be its own `ConflictPrompter` interface rather than a method on `StartPrompter`?** Argument for splitting: `branch new` and `issue start` share the conflict flow, and a future "checkout existing branch" command might want the same picker. Argument against: there's exactly one caller chain today; splitting now would be speculative. Leaving as a method on `StartPrompter` in the first cut, with an explicit note in the implementation plan that future commands needing the picker should factor it out at that point.

## Documentation updates

- `README.md`: add a "Testing the start flow" subsection alongside the existing "Testing the close flow" subsection.
- `cmd/issue/start.go`: docstrings on `RunIssueStart`, `startDeps`, `buildStartDeps`.
- `cmd/issue/start_prompter.go`: package- and interface-level docstrings; one per method explaining when it's called (matching the level of detail in `close_prompter.go`).
- `issue/issue.go`: docstring on the new `Prompter` interface.
- `ROADMAP.md`: move the (1) bullet from "End-to-end testability" to a "done" marker (or strike through) once the implementation plan ships.
