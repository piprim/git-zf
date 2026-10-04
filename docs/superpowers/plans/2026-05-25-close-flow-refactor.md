# Close-Flow Refactor + E2E Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Context:** `cmd/issue/close.go` is the largest interactive path in the project — branch picker, strategy picker, merge confirm, commit message form, tracker status picker, and delete-branch prompt all sit inline in the same call chain. Today there are no end-to-end tests for this flow because every step blocks on `huh.NewForm(...).RunWithContext(ctx)`. The merge orchestration, store updates, tracker calls, and post-merge bookkeeping are therefore covered only by manual testing — which is exactly the layer most likely to silently regress when the merge strategies are refactored. This plan extracts a non-interactive core (`Close(ctx, deps, prompter)`) behind a `ClosePrompter` interface so each huh form has a 1:1 testable seam. A scripted prompter is added for tests; a `huhPrompter` keeps production behaviour identical.

**Goal:** Make the close flow end-to-end testable without driving a TUI, then ship 6+ E2E tests that exercise rebase / squash / classic happy paths plus the main failure modes. No user-visible behaviour change.

**Architecture:** Introduce `ClosePrompter` — a 6-method interface, one method per huh form in `cmd/issue/close.go`. Provide two implementations: `huhPrompter` (production — wraps the existing `tui.Issue*` constructors) and `scriptedPrompter` (tests — returns canned values). Lift `closeRunE`'s body into a new exported `Close(ctx context.Context, deps closeDeps, prompter ClosePrompter) error`. `closeRunE` becomes a 5-line wiring function that builds `deps` from the cobra command and constructs a `huhPrompter`. The strategy helpers (`doSquashCommit`, `doRebaseClose`, `doClassicClose`) and the secondary helpers (`getPickedBranch`, `doMerge`, `doDeleteBranch`, `updateStatus`/`closeTrackerIssue`) take `prompter ClosePrompter` instead of opening forms themselves. No new flags are added in this plan — `--strategy`, `--yes`, `--message-file` are a deliberate follow-up once the test scaffolding is in place.

**Tech Stack:** Go 1.23+, `spf13/cobra`, `charmbracelet/huh`, `go-git/v6`, `modernc.org/sqlite`, `net/http/httptest` for the tracker fake.

**Toolchain:** Go is managed by `mise`. **Always invoke Go via `mise exec -- go <command>`** (e.g. `mise exec -- go test ./...`). Never call bare `go`.

**Lint conventions to follow throughout the plan:**

- `wrapcheck` — every error returned from an external package must be wrapped with `fmt.Errorf("context: %w", err)`. Never return a bare external error.
- `nlreturn` — when a `return` statement is not the only statement in its block, put a blank line before it.
- `exec.CommandContext(ctx, ...)` — never `exec.Command(...)`. Threading `ctx` through `os/exec` is mandatory in this codebase.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `cmd/issue/close_prompter.go` | `ClosePrompter` interface + `huhPrompter` production implementation. |
| `cmd/issue/close_prompter_test.go` | Compile-time assertion that `scriptedPrompter` satisfies `ClosePrompter`, plus the `scriptedPrompter` test helper definition. |
| `cmd/issue/close_e2e_test.go` | End-to-end tests for the close flow (rebase / squash / classic + failure modes). Houses the shared `closeTestRig` helper. |
| `tracker/fake/fake.go` | In-process `tracker.Tracker` fake. Registers itself under type `"fake"` so `tracker.New(config.IssueTrackerConfig{Type: "fake"})` works in tests. Records every `UpdateIssueStatus` call for assertions. |

### Modified files

| Path | Change summary |
|---|---|
| `cmd/issue/close.go` | Add `closeDeps` struct + `buildCloseDeps`. Replace `closeRunE`'s body with `Close(ctx, deps, prompter)` and a thin wiring shim. Thread `prompter ClosePrompter` through `getPickedBranch`, `doMerge`, `doSquashCommit`, `doRebaseClose`, `doClassicClose`, `doDeleteBranch`, `updateStatus`/`closeTrackerIssue`. Replace four direct `huh.NewForm(...).RunWithContext(ctx)` calls and three `commitpkg.FillOutForm(...)` calls with prompter methods. |
| `cmd/issue/start.go` | No production change; `updateTrackerIssueStatus` stays as the start-path implementation (untouched). The close path now has its own status-picker call routed through the prompter — see Task 2. |
| `README.md` | Add a "Testing the close flow" note under the existing Testing section pointing at the new E2E tests. |

---

## Task 1: Define `ClosePrompter` interface + scripted test helper

This task lays down the interface and the in-test fake before touching any production code. No caller wiring yet — the build stays green because nothing references the new types except a single compile-time assertion in the test file.

**Files:**
- Create: `cmd/issue/close_prompter.go`
- Create: `cmd/issue/close_prompter_test.go`

- [ ] **Step 1: Create the interface file**

Write `cmd/issue/close_prompter.go`:

```go
package issue

import (
	"context"

	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// ClosePrompter resolves every user-facing decision in the close flow. The
// production implementation drives huh forms; the test implementation in
// close_prompter_test.go returns canned values.
//
// Each method maps 1:1 to a huh.NewForm call in the pre-refactor close.go.
// The order below mirrors the order calls happen in Close().
type ClosePrompter interface {
	// PickBranch is called only when at least one in-progress branch exists.
	// Cancellation (Esc/Ctrl+C) returns a wrapped huh error.
	PickBranch(ctx context.Context, branches []store.BranchRow, current string) (*store.BranchRow, error)

	// PickStrategy is called only when MergeDryRun reports no conflicts.
	PickStrategy(ctx context.Context, branch, base string) (MergeStrategy, error)

	// ConfirmMerge gates the actual merge. confirmed=false means the operator
	// declined; Close() prints "Aborted." and returns nil.
	ConfirmMerge(ctx context.Context, branch, base string, strategy MergeStrategy) (confirmed bool, err error)

	// ComposeMessage runs inline AFTER the strategy has staged its changes
	// (after MergeSquash / MergeRebase+soft-reset / MergeNoFFNoCommit). The
	// prefill is already populated with issue-derived type/scope and a
	// strategy-specific subject. The returned tui.CommitOption is passed to
	// git.Client.Commit verbatim.
	ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error)

	// PickTrackerStatus is called only when cfg.IssueTracker.Type != "" AND
	// the merge succeeded. Returning "" skips the tracker update (Close()
	// treats it the same as the operator cancelling the picker).
	PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

	// ConfirmDeleteBranch runs after a successful merge.
	ConfirmDeleteBranch(ctx context.Context, branchName string) (delete bool, err error)
}
```

- [ ] **Step 2: Create the test helper file**

Write `cmd/issue/close_prompter_test.go`:

```go
package issue

import (
	"context"
	"errors"

	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)

// Compile-time check: scriptedPrompter must satisfy ClosePrompter.
var _ ClosePrompter = (*scriptedPrompter)(nil)

// scriptedPrompter is the canned-response prompter used by close_e2e_test.go.
// Each field corresponds to one ClosePrompter method's return value.
//
// Unset fields produce zero values — useful for tests that exercise a single
// strategy and don't care about the rest of the surface. Fields are public to
// keep test setup readable (literal struct construction).
type scriptedPrompter struct {
	Branch        *store.BranchRow
	Strategy      MergeStrategy
	Confirm       bool
	Message       []byte
	MessageOpts   tui.CommitOption
	TrackerStatus string
	DeleteBranch  bool

	// Failure injection — when non-nil, that method returns this error
	// immediately. Useful for cancellation-style tests.
	BranchErr        error
	StrategyErr      error
	ConfirmErr       error
	MessageErr       error
	TrackerStatusErr error
	DeleteErr        error
}

func (s *scriptedPrompter) PickBranch(_ context.Context, _ []store.BranchRow, _ string) (*store.BranchRow, error) {
	if s.BranchErr != nil {
		return nil, s.BranchErr
	}

	if s.Branch == nil {
		return nil, errors.New("scriptedPrompter.Branch is nil — set it in the test")
	}

	return s.Branch, nil
}

func (s *scriptedPrompter) PickStrategy(_ context.Context, _, _ string) (MergeStrategy, error) {
	if s.StrategyErr != nil {
		return "", s.StrategyErr
	}

	return s.Strategy, nil
}

func (s *scriptedPrompter) ConfirmMerge(_ context.Context, _, _ string, _ MergeStrategy) (bool, error) {
	if s.ConfirmErr != nil {
		return false, s.ConfirmErr
	}

	return s.Confirm, nil
}

func (s *scriptedPrompter) ComposeMessage(_ context.Context, _ map[string]any) ([]byte, tui.CommitOption, error) {
	if s.MessageErr != nil {
		return nil, tui.CommitOption{}, s.MessageErr
	}

	return s.Message, s.MessageOpts, nil
}

func (s *scriptedPrompter) PickTrackerStatus(_ context.Context, _, _ string, _ []string) (string, error) {
	if s.TrackerStatusErr != nil {
		return "", s.TrackerStatusErr
	}

	return s.TrackerStatus, nil
}

func (s *scriptedPrompter) ConfirmDeleteBranch(_ context.Context, _ string) (bool, error) {
	if s.DeleteErr != nil {
		return false, s.DeleteErr
	}

	return s.DeleteBranch, nil
}
```

- [ ] **Step 3: Confirm the package still builds**

```bash
mise exec -- go build ./...
mise exec -- go test -count=1 -run NoSuchTest ./cmd/issue/...
```

Expected: clean build. The `-run NoSuchTest` invocation matches no tests but forces the test binary to compile, which proves the `var _ ClosePrompter = (*scriptedPrompter)(nil)` assertion holds.

- [ ] **Step 4: Commit**

```bash
git add cmd/issue/close_prompter.go cmd/issue/close_prompter_test.go
git commit -m "refactor(issue): introduce ClosePrompter interface + scripted test helper"
```

---

## Task 2: Implement `huhPrompter` (production)

This task adds the production implementation but does not yet wire it into `closeRunE`. After commit, `huhPrompter` exists, compiles, and is unused — Task 3 wires it.

**Files:**
- Modify: `cmd/issue/close_prompter.go`

- [ ] **Step 1: Append the huhPrompter implementation**

Append to `cmd/issue/close_prompter.go`:

```go
import (
	"context"
	"fmt"
	"log/slog"

	"github.com/charmbracelet/huh"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)
```

(Merge the import block at the top of the file rather than declaring a second one — Go won't compile a file with two `import (...)` blocks.)

Then below the `ClosePrompter` interface:

```go
// Compile-time check.
var _ ClosePrompter = (*huhPrompter)(nil)

// huhPrompter is the production ClosePrompter. It is constructed once per
// `issue close` invocation and holds the dependencies needed to drive the
// commit-message form (client for Authors(), store as the history backend,
// cfg for the form template).
type huhPrompter struct {
	client *git.Client
	store  *store.Store
	cfg    *config.AppConfig
}

func newHuhPrompter(client *git.Client, s *store.Store, cfg *config.AppConfig) *huhPrompter {
	return &huhPrompter{client: client, store: s, cfg: cfg}
}

func (p *huhPrompter) PickBranch(ctx context.Context, branches []store.BranchRow, current string) (*store.BranchRow, error) {
	var picked store.BranchRow
	if err := huh.NewForm(tui.IssueBranchPicker(branches, current, &picked)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("branch picker: %w", err)
	}

	return &picked, nil
}

func (p *huhPrompter) PickStrategy(ctx context.Context, _, _ string) (MergeStrategy, error) {
	var picked string
	form := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
		{
			Value: string(StrategyRebase),
			Label: "Rebase",
			Hint:  "Single clean commit on local base, submodule-safe (recommended)",
		},
		{
			Value: string(StrategySquash),
			Label: "Squash",
			Hint:  "git merge --squash — fast, but not submodule-safe",
		},
		{
			Value: string(StrategyClassic),
			Label: "Classic",
			Hint:  "git merge --no-ff with commitizen message — preserves full history",
		},
	})
	if err := huh.NewForm(form).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("strategy picker: %w", err)
	}

	return MergeStrategy(picked), nil
}

func (p *huhPrompter) ConfirmMerge(ctx context.Context, branch, base string, strategy MergeStrategy) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueMergeConfirm(branch, base, string(strategy), &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm form: %w", err)
	}

	return confirmed, nil
}

func (p *huhPrompter) ComposeMessage(ctx context.Context, prefill map[string]any) ([]byte, tui.CommitOption, error) {
	authors, err := p.client.Authors()
	if err != nil {
		slog.Warn("could not load author list", "error", err)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, p.cfg, defaults, p.store, prefill)
	if err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("fill commit form: %w", err)
	}

	return msg, opts, nil
}

func (p *huhPrompter) PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error) {
	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(issueID, trackerType, statuses, &selected)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("status picker form: %w", err)
	}

	return selected, nil
}

func (p *huhPrompter) ConfirmDeleteBranch(ctx context.Context, branchName string) (bool, error) {
	var shouldDelete bool
	if err := huh.NewForm(tui.IssueDeleteBranch(branchName, &shouldDelete)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("delete branch form: %w", err)
	}

	return shouldDelete, nil
}
```

- [ ] **Step 2: Build to verify the new code compiles**

```bash
mise exec -- go build ./...
```

Expected: clean build. The struct and its methods are defined but unused — Go allows that for top-level declarations.

- [ ] **Step 3: Run the full test suite to confirm nothing regressed**

```bash
mise exec -- go test ./...
```

Expected: every existing test continues to pass. No new tests yet.

- [ ] **Step 4: Commit**

```bash
git add cmd/issue/close_prompter.go
git commit -m "refactor(issue): add huhPrompter production implementation of ClosePrompter"
```

---

## Task 3: Extract `closeDeps` + `Close()` and thread `prompter` through every helper

This is the largest task in the plan — it's the actual refactor. The build must stay green throughout. The strategy helpers (`doSquashCommit`, `doRebaseClose`, `doClassicClose`) keep calling `commitpkg.FillOutForm` for now; Task 4 swaps those calls for `prompter.ComposeMessage`.

**Files:**
- Modify: `cmd/issue/close.go`

- [ ] **Step 1: Add `closeDeps` + `buildCloseDeps` near the top of close.go**

Insert below the existing `const shortSHALen = 7` declaration:

```go
// closeDeps bundles the long-lived dependencies the close flow needs.
// Production code builds it via buildCloseDeps; tests inject directly.
type closeDeps struct {
	client  *git.Client
	store   *store.Store
	cfg     *config.AppConfig
	tracker tracker.Tracker // nil ⇒ no tracker update will be attempted
}

// buildCloseDeps constructs the production closeDeps from a cobra command.
// Returns an error if the repo cannot be opened or the store cannot be
// initialised. When cfg.IssueTracker.Type == "" the returned deps.tracker is
// nil (Close() treats that as "skip tracker update").
func buildCloseDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig) (closeDeps, error) {
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return closeDeps{}, fmt.Errorf("failed to get store: %w", err)
	}

	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		_ = s.Close()

		return closeDeps{}, fmt.Errorf("not a git repository: %w", err)
	}

	if cfg.Branch.Remote != "" {
		client.SetRemote(cfg.Branch.Remote)
	}

	deps := closeDeps{client: client, store: s, cfg: cfg}

	if cfg.IssueTracker.Type != "" {
		t, err := tracker.New(cfg.IssueTracker)
		if err != nil {
			// Non-fatal: warn and continue with a nil tracker.
			fmt.Fprintf(client.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			deps.tracker = t
		}
	}

	return deps, nil
}
```

- [ ] **Step 2: Replace `closeRunE` with the thin wiring shim**

Replace the existing `closeRunE` function body (lines 58–130 in the current close.go) with:

```go
func (i Issue) closeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	deps, err := buildCloseDeps(ctx, cmd, i.appConfig)
	if err != nil {
		return err
	}
	defer func() { _ = deps.store.Close() }()

	return Close(ctx, deps, newHuhPrompter(deps.client, deps.store, i.appConfig))
}
```

- [ ] **Step 3: Add the new `Close()` function below `closeRunE`**

```go
// Close runs the full merge → store → tracker → delete-branch pipeline
// without opening any huh forms directly. All user-facing decisions are
// resolved by prompter. Used by both closeRunE (production) and the E2E
// tests (with a scripted prompter).
//
// Returns nil on the errFastForwardDeferred path — the commit landed and
// the operator just needs to fast-forward the local base manually; Close
// has already printed the recovery instructions.
func Close(ctx context.Context, deps closeDeps, prompter ClosePrompter) error {
	picked, err := getPickedBranch(ctx, deps.store, deps.client, prompter)
	if err != nil {
		return err
	}

	if picked == nil {
		return nil
	}

	base := deps.cfg.Branch.Base
	if base == "" {
		base, err = deps.client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	mc := mergeContext{
		client:       deps.client,
		pickedBranch: picked,
		baseBranch:   base,
		cfg:          deps.cfg,
		store:        deps.store,
	}

	strategy, aborted, err := doMerge(ctx, mc, prompter)
	if err != nil {
		if errors.Is(err, errFastForwardDeferred) {
			return nil
		}

		return err
	}

	if aborted {
		fmt.Fprintln(deps.client.IO().Out, "Aborted.")

		return nil
	}

	updateClosedStatus(ctx, deps, picked, prompter)

	if err := doDeleteBranch(ctx, deps.client, picked, strategy, prompter); err != nil {
		return err
	}

	fmt.Fprintf(deps.client.IO().Out, "Branch %q merged into %q and closed.\n", picked.BranchName, base)

	return nil
}
```

- [ ] **Step 4: Update `getPickedBranch` to use the prompter**

Replace the existing `getPickedBranch` (currently around line 133) with:

```go
// getPickedBranch returns (nil, nil) when there are no in-progress branches.
func getPickedBranch(ctx context.Context, s *store.Store, client *git.Client, prompter ClosePrompter) (*store.BranchRow, error) {
	branches, err := s.ListBranches(ctx, store.BranchStatusInProgress)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	if len(branches) == 0 {
		fmt.Fprintln(client.IO().Out, "No in-progress branches.")

		return nil, nil
	}

	currentBranch, err := client.CurrentBranch()
	if err != nil {
		currentBranch = ""
	}

	picked, err := prompter.PickBranch(ctx, branches, currentBranch)
	if err != nil {
		return nil, err
	}

	return picked, nil
}
```

(The `fmt.Println("No in-progress branches.")` call is replaced with `fmt.Fprintln(client.IO().Out, ...)` so test output capture works — see the project memory `feedback_respect_io_injection`.)

- [ ] **Step 5: Update `doMerge` to take and pass the prompter**

Replace `doMerge`'s signature and the three form calls inside it:

```go
func doMerge(ctx context.Context, mc mergeContext, prompter ClosePrompter) (strategy MergeStrategy, aborted bool, err error) {
	conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, mc.baseBranch)
	if err != nil {
		return "", false, fmt.Errorf("merge dry-run: %w", err)
	}

	if len(conflicts) > 0 {
		fmt.Fprintln(mc.client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(mc.client.IO().Out, "  "+f)
		}
		fmt.Fprintln(mc.client.IO().Out, "Aborting.")

		return "", false, fmt.Errorf("merge conflicts in branch %q", mc.pickedBranch.BranchName)
	}

	strategy, err = prompter.PickStrategy(ctx, mc.pickedBranch.BranchName, mc.baseBranch)
	if err != nil {
		return "", false, err
	}

	confirmed, err := prompter.ConfirmMerge(ctx, mc.pickedBranch.BranchName, mc.baseBranch, strategy)
	if err != nil {
		return "", false, err
	}

	if !confirmed {
		return strategy, true, nil
	}

	switch strategy {
	case StrategyClassic:
		if err := doClassicClose(ctx, mc, prompter); err != nil {
			return strategy, false, err
		}
	case StrategySquash:
		if err := doSquashCommit(ctx, mc, prompter); err != nil {
			return strategy, false, err
		}
	case StrategyRebase:
		if err := doRebaseClose(ctx, mc, prompter); err != nil {
			return strategy, false, err
		}
	default:
		return strategy, false, fmt.Errorf("unknown strategy %q", strategy)
	}

	return strategy, false, nil
}
```

- [ ] **Step 6: Add `prompter ClosePrompter` parameters to the three strategy helpers**

For each of `doSquashCommit`, `doRebaseClose`, `doClassicClose`: change the signature to add `prompter ClosePrompter` as the trailing parameter. **Leave the body unchanged** — the `commitpkg.FillOutForm(...)` calls inside stay as-is in this task; Task 4 swaps them for `prompter.ComposeMessage`.

```go
func doSquashCommit(ctx context.Context, mc mergeContext, prompter ClosePrompter) error {
	// body unchanged
}

func doRebaseClose(ctx context.Context, mc mergeContext, prompter ClosePrompter) (err error) {
	// body unchanged
}

func doClassicClose(ctx context.Context, mc mergeContext, prompter ClosePrompter) (err error) {
	// body unchanged
}
```

(The parameter is unused for now; Go will not complain about unused parameters. The linter might — if `unparam` or similar fires, add `_ = prompter` at the top of each body as a temporary placeholder until Task 4.)

- [ ] **Step 7: Replace `(i Issue) updateStatus` and `closeTrackerIssue` with a free function**

These methods used `i.appConfig.IssueTracker.Type` and constructed the tracker themselves. With the tracker now living on `deps`, lift them to a free function:

Delete the existing `func (i Issue) updateStatus(...)` (around line 293) and `func (i Issue) closeTrackerIssue(...)` (around line 325) entirely, and replace with:

```go
// updateClosedStatus marks the branch and issue as merged in the store and,
// when a tracker is configured, drives the status-picker form. Every error
// here is non-fatal — the merge already committed, so the operator must be
// able to clean up store/tracker drift manually.
func updateClosedStatus(ctx context.Context, deps closeDeps, picked *store.BranchRow, prompter ClosePrompter) {
	now := time.Now()
	if err := deps.store.UpdateBranchStatus(ctx, picked.BranchName, store.StatusIDMerged, &now); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: update branch status: %v\n", err)
	}

	if err := deps.store.UpdateIssueStatus(ctx, picked.IssueID, store.StatusIDMerged); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: update issue status: %v\n", err)
	}

	if deps.tracker == nil {
		return
	}

	statuses, err := deps.tracker.ListStatuses(ctx)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	selected, err := prompter.PickTrackerStatus(ctx, picked.IssueSlug, deps.cfg.IssueTracker.Type, statuses)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: status picker: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := deps.tracker.UpdateIssueStatus(ctx, picked.IssueSlug, selected); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: update tracker status: %v\n", err)
	}
}
```

(The behaviour matches the pre-refactor flow: warnings go to `Err`, every step is best-effort, and an empty status selection skips the tracker update. The only observable change is that warnings now go to `client.IO().Err` instead of `cmd.OutOrStderr()` — which is what the existing memory `feedback_respect_io_injection` asks for. Verify the existing manual tests still look right.)

- [ ] **Step 8: Update `doDeleteBranch` to use the prompter**

Replace `doDeleteBranch` (around line 308) with:

```go
func doDeleteBranch(ctx context.Context, c *git.Client, picked *store.BranchRow, strategy MergeStrategy, prompter ClosePrompter) error {
	shouldDelete, err := prompter.ConfirmDeleteBranch(ctx, picked.BranchName)
	if err != nil {
		return err
	}

	if !shouldDelete {
		return nil
	}

	force := strategy == StrategySquash || strategy == StrategyRebase
	if err := c.DeleteLocalBranch(ctx, picked.BranchName, force); err != nil {
		fmt.Fprintf(c.IO().Err, "warning: delete branch: %v\n", err)
	}

	return nil
}
```

- [ ] **Step 9: Build and run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, every existing test passes. The flow is now prompter-driven end-to-end (except for the `commitpkg.FillOutForm` calls inside the three strategy helpers — Task 4 handles those).

- [ ] **Step 10: Manual smoke test (recommended)**

In a scratch repo, run `git-zf issue close` and exercise one strategy to confirm the production TUI behaviour is unchanged.

- [ ] **Step 11: Commit**

```bash
git add cmd/issue/close.go
git commit -m "refactor(issue): extract Close() behind ClosePrompter interface"
```

---

## Task 4: Replace `FillOutForm` calls with `prompter.ComposeMessage`

The three strategy helpers each have a near-identical block: load authors, build defaults, call `commitpkg.FillOutForm`. The `huhPrompter.ComposeMessage` method already does exactly that (Task 2). Inline-replace each call site.

**Files:**
- Modify: `cmd/issue/close.go`

- [ ] **Step 1: Refactor `doSquashCommit`**

Find the block in `doSquashCommit` (currently around lines 259–274):

```go
	authors, authorsErr := mc.client.Authors()
	if authorsErr != nil {
		slog.Warn("could not load author list", "error", authorsErr)

		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
	if err != nil {
		return fmt.Errorf("fill commit form: %w", err)
	}
```

Replace with:

```go
	msg, opts, err := prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err
	}
```

- [ ] **Step 2: Refactor `doRebaseClose`**

Apply the same replacement at the equivalent block (currently around lines 389–404).

- [ ] **Step 3: Refactor `doClassicClose`**

Apply the same replacement at the equivalent block (currently around lines 496–511).

- [ ] **Step 4: Clean up unused imports**

After the three replacements, `commitpkg`, `tui`, and `log/slog` may no longer be used by close.go (they've migrated to close_prompter.go). Drop unused imports:

```bash
mise exec -- goimports -w cmd/issue/close.go
```

If `goimports` is not on the path, manually remove any imports the build complains about. `commitpkg.IssueHint` is still used inside each strategy helper for the prefill — that import stays. `log/slog` should be removable. `tui` should be removable.

- [ ] **Step 5: Build and run the full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, every existing test passes.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/close.go
git commit -m "refactor(issue): route commit-message form through ClosePrompter"
```

---

## Task 5: Tracker fake + E2E test rig

The E2E tests need three things production tests don't have: a real on-disk repo with a feature branch ahead of main, a seeded store, and a tracker fake. This task ships the shared scaffolding once so the per-strategy tests in Tasks 6–9 stay terse.

**Files:**
- Create: `tracker/fake/fake.go`
- Create: `cmd/issue/close_e2e_test.go` (rig only — assertions land in subsequent tasks)

- [ ] **Step 1: Write the tracker fake**

Create `tracker/fake/fake.go`:

```go
// Package fake provides an in-process tracker.Tracker for tests.
// It registers itself under type "fake" so close_e2e_test.go can wire it
// through config.IssueTrackerConfig like a real adapter.
package fake

import (
	"context"
	"sync"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

func init() {
	tracker.Register("fake", New)
}

// Tracker is the exposed concrete type so tests can read RecordedUpdates
// after Close() returns. Construct via New() to satisfy the registry signature.
type Tracker struct {
	mu              sync.Mutex
	Issues          []tracker.Issue
	Statuses        []string
	RecordedUpdates []Update
}

// Update captures one UpdateIssueStatus call.
type Update struct {
	IssueID    string
	StatusName string
}

// New is the tracker.Register factory. cfg is ignored — tests configure the
// returned *Tracker directly via field access.
func New(_ config.IssueTrackerConfig) (tracker.Tracker, error) {
	return &Tracker{
		Statuses: []string{"In Progress", "Closed"},
	}, nil
}

func (t *Tracker) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]tracker.Issue, len(t.Issues))
	copy(out, t.Issues)

	return out, nil
}

func (t *Tracker) ListStatuses(_ context.Context) ([]string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	out := make([]string, len(t.Statuses))
	copy(out, t.Statuses)

	return out, nil
}

func (t *Tracker) UpdateIssueStatus(_ context.Context, issueID, statusName string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.RecordedUpdates = append(t.RecordedUpdates, Update{IssueID: issueID, StatusName: statusName})

	return nil
}
```

- [ ] **Step 2: Wire the fake registration into the close package**

Edit `cmd/issue/issue.go`. Add a blank-import next to the existing tracker imports so the fake registers when the test binary is compiled. **Important:** this import must be in a `_test.go` file, not the production `issue.go`, so the fake is not embedded into production builds.

Create `cmd/issue/register_fake_tracker_test.go`:

```go
package issue

import (
	_ "github.com/piprim/git-zf/tracker/fake" // registers "fake" tracker for tests
)
```

- [ ] **Step 3: Write the rig**

Create `cmd/issue/close_e2e_test.go`:

```go
package issue

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// closeTestRig bundles a real on-disk git repo, a seeded store, and a fake
// tracker so each E2E test sets up state in one line.
type closeTestRig struct {
	dir     string
	client  *git.Client
	store   *store.Store
	tracker *fake.Tracker
	cfg     *config.AppConfig
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

// newCloseRig initialises a temp git repo with a "main" branch carrying one
// commit, a feature branch ABC-1@feat@add-thing one commit ahead of main, a
// store with the matching in-progress row, and a fake tracker.
//
// The returned deps embed the IO buffers so tests can assert on stdout/stderr.
func newCloseRig(t *testing.T) *closeTestRig {
	t.Helper()

	dir := t.TempDir()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("init", "-q", "-b", "main")
	runGit("config", "user.name", "Test User")
	runGit("config", "user.email", "test@test.com")
	runGit("config", "commit.gpgsign", "false")

	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit("add", "base.txt")
	runGit("commit", "-m", "chore: init")

	runGit("checkout", "-b", "ABC-1@feat@add-thing")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	runGit("add", "feature.txt")
	runGit("commit", "-m", "feat: add thing")
	runGit("checkout", "main")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	io := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(io, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "Add thing", StatusID: store.StatusIDInProgress, TrackerType: "fake"},
		&store.Branch{Name: "ABC-1@feat@add-thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"

	rawT, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	fakeT := rawT.(*fake.Tracker)

	return &closeTestRig{
		dir: dir, client: client, store: s, tracker: fakeT, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

func (r *closeTestRig) deps() closeDeps {
	return closeDeps{client: r.client, store: r.store, cfg: r.cfg, tracker: r.tracker}
}

// pickedBranchRow returns the BranchRow the picker would have returned for
// the seeded branch. Used by every scriptedPrompter setup.
func (r *closeTestRig) pickedBranchRow() *store.BranchRow {
	return &store.BranchRow{
		IssueID:    1, // first row in the empty store
		IssueSlug:  "ABC-1",
		Title:      "Add thing",
		BranchName: "ABC-1@feat@add-thing",
		Type:       "feat",
		Status:     store.BranchStatusInProgress,
	}
}

// assertHeadSubject asserts the subject line of HEAD on branch matches want.
func assertHeadSubject(t *testing.T, dir, branch, want string) {
	t.Helper()

	cmd := exec.CommandContext(context.Background(), "git", "log", "-1", "--format=%s", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git log %s: %v", branch, err)
	}
	got := string(bytes.TrimSpace(out))
	if got != want {
		t.Errorf("HEAD subject on %q = %q, want %q", branch, got, want)
	}
}

// assertBranchAbsent asserts that branch no longer exists locally.
func assertBranchAbsent(t *testing.T, c *git.Client, branch string) {
	t.Helper()

	exists, err := c.BranchExists(branch)
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if exists {
		t.Errorf("branch %q still exists; expected it to be deleted", branch)
	}
}

// silenceUnused keeps io.Discard reachable in case future tests need it.
var _ = io.Discard
```

(`store.Open(ctx, dir)` is the same constructor used by `openTestStore` in `store/store_test.go:11`. `git.NewClientAt` is the on-disk constructor used by `git/merge_test.go:43`. `BranchExists` was added in the deterministic-branch-naming plan — verify it's present in `git/git.go` before this task; if not, this test relies on it and the prior plan must have shipped.)

- [ ] **Step 4: Verify the rig compiles in isolation**

```bash
mise exec -- go test -count=1 -run NoSuchTest ./cmd/issue/...
mise exec -- go test -count=1 -run NoSuchTest ./tracker/fake/...
```

Expected: both compile clean. No test runs yet (no `Test*` functions exist in close_e2e_test.go).

- [ ] **Step 5: Commit**

```bash
git add tracker/fake/ cmd/issue/close_e2e_test.go cmd/issue/register_fake_tracker_test.go
git commit -m "test(issue): add tracker fake and close E2E test rig"
```

---

## Task 6: E2E test — Rebase happy path

**Files:**
- Modify: `cmd/issue/close_e2e_test.go`

- [ ] **Step 1: Append the test**

Append to `cmd/issue/close_e2e_test.go`:

```go
func TestClose_RebaseHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyRebase,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	if err := Close(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// main HEAD should carry the new commit subject (rebase strategy lands
	// the squashed merge directly on main via the post-commit fast-forward).
	assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")

	// Feature branch should have been deleted (force=true for rebase).
	assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")

	// Store should reflect Merged status for the branch and the parent issue.
	branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 || branches[0].BranchName != "ABC-1@feat@add-thing" {
		t.Errorf("expected one merged branch row for ABC-1@feat@add-thing, got %+v", branches)
	}

	// Tracker should have recorded exactly one UpdateIssueStatus call.
	if got, want := len(rig.tracker.RecordedUpdates), 1; got != want {
		t.Fatalf("RecordedUpdates len = %d, want %d", got, want)
	}
	if rig.tracker.RecordedUpdates[0] != (fake.Update{IssueID: "ABC-1", StatusName: "Closed"}) {
		t.Errorf("RecordedUpdates[0] = %+v, want {ABC-1 Closed}", rig.tracker.RecordedUpdates[0])
	}
}
```

(`store.BranchStatusMerged` is the existing enum value used elsewhere in the store package — verify the exact identifier with `grep -n "BranchStatusMerged\|StatusIDMerged" store/store.go` if it doesn't compile.)

- [ ] **Step 2: Run the test**

```bash
mise exec -- go test ./cmd/issue/... -run TestClose_RebaseHappyPath -v
```

Expected: PASS. If it fails on the HEAD subject assertion, the rebase strategy's post-commit fast-forward may have been deferred (`errFastForwardDeferred`) — inspect `rig.stderr.String()` to confirm. If the deferral fires in this minimal setup (no remote configured), the assertion should target the feature branch's HEAD instead — adjust accordingly.

- [ ] **Step 3: Commit**

```bash
git add cmd/issue/close_e2e_test.go
git commit -m "test(issue): add E2E test for close --rebase happy path"
```

---

## Task 7: E2E test — Squash happy path

**Files:**
- Modify: `cmd/issue/close_e2e_test.go`

- [ ] **Step 1: Append the test**

```go
func TestClose_SquashHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): squash-close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	if err := Close(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Squash strategy stages the merge into the CURRENT branch (the feature)
	// and commits there. Verify HEAD subject on the feature… BUT the branch
	// has just been deleted. The pre-delete HEAD is what matters: pick the
	// merge-base / reflog. Simplest: skip the head-subject assertion for
	// squash (the merge commit lives only on the deleted branch's reflog),
	// and assert the store/tracker state instead.

	assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")

	branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("expected one merged branch row, got %d", len(branches))
	}

	if len(rig.tracker.RecordedUpdates) != 1 {
		t.Fatalf("RecordedUpdates len = %d, want 1", len(rig.tracker.RecordedUpdates))
	}
}
```

(The comment in the test body explains the omitted assertion. If, during execution, you discover squash actually lands on `main` in this fixture, replace the comment with a `assertHeadSubject(t, rig.dir, "main", "feat(thing): squash-close ABC-1")` call. Verify via `git log main` in the temp dir mid-test if uncertain.)

- [ ] **Step 2: Run, then commit**

```bash
mise exec -- go test ./cmd/issue/... -run TestClose_SquashHappyPath -v
```

Expected: PASS.

```bash
git add cmd/issue/close_e2e_test.go
git commit -m "test(issue): add E2E test for close --squash happy path"
```

---

## Task 8: E2E test — Classic happy path

**Files:**
- Modify: `cmd/issue/close_e2e_test.go`

- [ ] **Step 1: Append the test**

```go
func TestClose_ClassicHappyPath(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyClassic,
		Confirm:       true,
		Message:       []byte("Merge ABC-1 into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false, // exercise the "don't delete" path
	}

	if err := Close(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Classic strategy: merge --no-ff lands the merge commit directly on main.
	assertHeadSubject(t, rig.dir, "main", "Merge ABC-1 into main")

	// Feature branch must STILL exist (DeleteBranch=false).
	exists, err := rig.client.BranchExists("ABC-1@feat@add-thing")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !exists {
		t.Error("feature branch was deleted; expected it to remain (DeleteBranch=false)")
	}

	// Store + tracker assertions stay the same.
	branches, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 1 {
		t.Fatalf("expected one merged branch row, got %d", len(branches))
	}
	if len(rig.tracker.RecordedUpdates) != 1 {
		t.Errorf("RecordedUpdates len = %d, want 1", len(rig.tracker.RecordedUpdates))
	}
}
```

- [ ] **Step 2: Run, then commit**

```bash
mise exec -- go test ./cmd/issue/... -run TestClose_ClassicHappyPath -v
```

Expected: PASS.

```bash
git add cmd/issue/close_e2e_test.go
git commit -m "test(issue): add E2E test for close --classic happy path"
```

---

## Task 9: E2E tests — Failure modes

Cover the three pre-merge exit conditions: no in-progress branches, operator aborts at the confirm prompt, and a dry-run conflict.

**Files:**
- Modify: `cmd/issue/close_e2e_test.go`

- [ ] **Step 1: No in-progress branches**

```go
func TestClose_NoInProgressBranches(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// Bare repo init — no seeded branch row.
	cmd := exec.CommandContext(t.Context(), "git", "init", "-q", "-b", "main", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	io := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(io, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"

	deps := closeDeps{client: client, store: s, cfg: cfg}

	// PickBranch should never be called — but if Close mis-routes, fail loudly.
	prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch should not be called when no branches exist")}

	if err := Close(t.Context(), deps, prompter); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "No in-progress branches") {
		t.Errorf("stdout = %q, want it to contain 'No in-progress branches'", got)
	}
}
```

Add `"errors"` and `"strings"` to the file's imports if not already present.

- [ ] **Step 2: Operator aborts at confirm**

```go
func TestClose_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:   rig.pickedBranchRow(),
		Strategy: StrategyRebase,
		Confirm:  false, // operator declines
	}

	if err := Close(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := rig.stdout.String(); !strings.Contains(got, "Aborted.") {
		t.Errorf("stdout = %q, want it to contain 'Aborted.'", got)
	}

	// No merge → branch must still exist on its original commit, no tracker update.
	exists, err := rig.client.BranchExists("ABC-1@feat@add-thing")
	if err != nil {
		t.Fatalf("BranchExists: %v", err)
	}
	if !exists {
		t.Error("feature branch was deleted on user-abort path")
	}
	if len(rig.tracker.RecordedUpdates) != 0 {
		t.Errorf("RecordedUpdates = %+v, want empty on abort", rig.tracker.RecordedUpdates)
	}
}
```

- [ ] **Step 3: Dry-run conflict aborts the close**

```go
func TestClose_ConflictAborts(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	// Create a conflicting change on main before calling Close.
	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	// Touch the same file on main with a different content, creating a dry-run conflict.
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("main-conflict\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt on main: %v", err)
	}
	runGit("add", "feature.txt")
	runGit("commit", "-m", "chore: conflict")

	prompter := &scriptedPrompter{
		Branch:  rig.pickedBranchRow(),
		Confirm: true,
		// Strategy/Message irrelevant — the dry-run fails before either is asked.
	}

	err := Close(t.Context(), rig.deps(), prompter)
	if err == nil {
		t.Fatal("Close: expected an error from the dry-run conflict, got nil")
	}
	if !strings.Contains(err.Error(), "merge conflicts") {
		t.Errorf("error = %v, want it to mention 'merge conflicts'", err)
	}

	if got := rig.stdout.String(); !strings.Contains(got, "Conflicts detected") {
		t.Errorf("stdout = %q, want it to mention 'Conflicts detected'", got)
	}
}
```

- [ ] **Step 4: Run all four**

```bash
mise exec -- go test ./cmd/issue/... -run "TestClose_NoInProgressBranches|TestClose_UserAbortsAtConfirm|TestClose_ConflictAborts" -v
```

Expected: all PASS.

- [ ] **Step 5: Final full-suite run**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, every test in the project still passes.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/close_e2e_test.go
git commit -m "test(issue): add E2E tests for close-flow failure modes"
```

---

## Task 10: Documentation

This task is mandatory — the refactor does not "ship" until the new public API surface (`Close`, `ClosePrompter`, `closeDeps`) is documented and contributors know the E2E tests exist.

**Files:**
- Modify: `cmd/issue/close.go` (docstrings on `Close`, `closeDeps`, `buildCloseDeps` — most already added in earlier tasks; verify completeness)
- Modify: `cmd/issue/close_prompter.go` (docstrings on `ClosePrompter`, each method, `huhPrompter`, `newHuhPrompter` — most already added; verify)
- Modify: `tracker/fake/fake.go` (docstrings on `Tracker`, `Update`, `New` — already added; verify)
- Modify: `README.md` (add a "Testing the close flow" subsection)

- [ ] **Step 1: Verify Go docstrings**

```bash
mise exec -- go doc ./cmd/issue Close
mise exec -- go doc ./cmd/issue ClosePrompter
mise exec -- go doc ./tracker/fake
```

For each function/interface/type listed, confirm the doc comment exists and matches the actual behaviour. Any missing or inaccurate comment: fix inline.

Checklist:
- `Close` — flow summary + `errFastForwardDeferred` contract + "nil deps.tracker = skip tracker update" contract.
- `closeDeps` — purpose + nil-tracker contract.
- `buildCloseDeps` — error semantics (store-open vs git-init).
- `ClosePrompter` — one-line summary; each method's purpose, when it's called, cancellation contract.
- `huhPrompter` — production wrapper; constructor argument order.
- `fake.Tracker`, `fake.Update`, `fake.New` — purpose + test-only nature.

- [ ] **Step 2: Add a "Testing the close flow" note to `README.md`**

Locate the existing Testing section (`grep -n "^## .*[Tt]est" README.md`). Add a subsection:

```markdown
### Testing the close flow

The close flow is end-to-end tested in `cmd/issue/close_e2e_test.go`. Tests
construct a real on-disk repo, a seeded SQLite store, and the in-process
tracker fake at `tracker/fake/`, then drive the flow with a `scriptedPrompter`
that returns canned answers instead of opening huh forms.

To exercise just the close-flow tests:

\`\`\`bash
mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v
\`\`\`

When adding a new merge strategy or changing the merge/store/tracker
sequencing, add a corresponding E2E test alongside the existing happy-path
and failure-mode tests.
```

(Escape the backticks correctly in the actual file — the example uses `\`` placeholders.)

- [ ] **Step 3: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_prompter.go tracker/fake/fake.go README.md
git commit -m "docs: document Close, ClosePrompter, and close-flow E2E tests"
```

---

## Final verification

- [ ] **Run the full suite cleanly**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
mise exec -- go vet ./...
```

Expected: clean build, every test PASS, no vet warnings introduced by this change.

- [ ] **Run the project's linter if configured**

```bash
ls .golangci.yml .golangci.yaml 2>/dev/null && mise exec -- golangci-lint run ./...
```

If a golangci-lint config is present, address `wrapcheck`, `nlreturn`, and any unused-import findings introduced by this change. Existing pre-change findings are out of scope.

- [ ] **Manual smoke test in a real repo**

In a scratch repo with `.git-zf.toml` and one in-progress branch:

```bash
mise exec -- go build -o ./bin/git-zf .
./bin/git-zf issue close
```

Exercise one happy path (e.g. Rebase) and one cancellation path (Esc at the strategy picker). Production behaviour must match the pre-refactor flow exactly — same prompts, same messages, same exit codes.

---

## Notes for the executor

- **`mise exec` is non-negotiable.** Calling bare `go` may pick up a stale Go from `$PATH` and produce subtle test failures (memory: `feedback_mise_exec`).
- **The user runs git themselves.** When an executor encounters a "Commit" step, surface the exact commands to the user rather than running them autonomously (memory: `feedback_no_git_commit`).
- **No `git add -A`.** Always pass explicit paths.
- **One commit per task.** Each commit message above is a single conventional-commit line; preserve the prefix (`refactor`, `test`, `docs`) so the project's existing log style is maintained.
- **Task 3 is the riskiest task.** It touches every function in close.go and threads a new parameter through six call sites. After Step 9, **run the full test suite plus a manual smoke test** before committing. Don't proceed to Task 4 until production behaviour is verified unchanged.
- **The fake tracker registers via `init()`.** It must only be imported by `_test.go` files. The blank import lives in `cmd/issue/register_fake_tracker_test.go` — do not move it to a production file, or production builds will embed the fake.
- **Squash strategy's HEAD location.** Task 7's test deliberately skips a HEAD-subject assertion because the squash commit lands on the deleted feature branch's reflog rather than on `main`. If the actual behaviour in this codebase puts the squash commit on `main` after deletion, tighten the assertion accordingly.
- **`branch.BranchExists`.** Task 5's rig calls `client.BranchExists(...)`, which was added in the deterministic-branch-naming plan. Confirm it has shipped to master before starting this plan; if not, this plan depends on it and the prior plan must merge first.
