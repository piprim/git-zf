# Issue-Start Refactor + E2E Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** [`docs/superpowers/specs/2026-05-26-issue-start-refactor-design.md`](../specs/2026-05-26-issue-start-refactor-design.md)

**Goal:** Make `cmd/issue/start.go` end-to-end testable without driving a TUI, mirroring the `ClosePrompter` pattern already shipped for `cmd/issue/close.go`. Then ship 8 E2E tests that exercise branch / worktree happy paths and the main failure modes (collision, abort, tracker fallback).

**Architecture:** Introduce `StartPrompter` — a 9-method interface, one method per huh form across `cmd/issue/start.go`, `cmd/issue/conflict.go`, and `issue/issue.go`. Provide two implementations: `huhStartPrompter` (production — wraps the existing `tui.*` constructors) and `scriptedStartPrompter` (tests — returns canned values). Lift `(i Issue) RunIssueStart` into a free function `RunIssueStart(ctx, deps, prompter)`. `startRunE` (`issue start`) and `newRunE` (`branch new`) become thin shims that build `startDeps` and construct a `huhStartPrompter`. The conflict-resolution loop in `cmd/issue/conflict.go` migrates into the prompter as a single `ResolveBranchConflict` method (one method covers the loop atomically; the pure `rebuildVariantBranch` helper stays in place so its existing tests don't churn). `issue.GetFromUser` and `issue.GetFromTracker` gain a `Prompter` parameter (small sub-interface) so they too can be driven without a TUI.

**Tech Stack:** Go 1.23+, `spf13/cobra`, `charmbracelet/huh`, `go-git/v6`, `modernc.org/sqlite`. Reuses the existing `tracker/fake/` from the close-flow refactor.

**Toolchain:** Go is managed by `mise`. **Always invoke Go via `mise exec -- go <command>`** (e.g. `mise exec -- go test ./...`). Never call bare `go`.

**Lint conventions to follow throughout the plan:**

- `wrapcheck` — every error from an external package wrapped with `fmt.Errorf("context: %w", err)`. Never bare external errors.
- `nlreturn` — blank line before `return` when it is not the only statement in its block.
- `exec.CommandContext(ctx, ...)` — never `exec.Command(...)`.
- `t.Run` — every distinct assertion or scenario in a test must be wrapped in a named `t.Run("descriptive label", func(t *testing.T) { ... })`. Applies even to single-block tests.
- IO injection — all writes go through `client.IO().Out` / `client.IO().Err`, never `fmt.Println` or `cmd.OutOrStderr()`.

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `cmd/issue/start_prompter.go` | `StartPrompter` interface + `huhStartPrompter` production implementation. |
| `cmd/issue/start_prompter_test.go` | Compile-time assertion that `scriptedStartPrompter` satisfies `StartPrompter`, plus the `scriptedStartPrompter` test helper. |
| `cmd/issue/start_e2e_test.go` | `startTestRig` helper + 8 E2E tests covering happy paths and failure modes. |

### Modified files

| Path | Change summary |
|---|---|
| `issue/issue.go` | Add `Prompter` sub-interface (3 methods: `PickIssueFromUser`, `PickIssueFromTracker`, `NotifyTrackerError`). Change `GetFromUser` and `GetFromTracker` signatures to accept a `Prompter`. |
| `cmd/issue/start.go` | Add `startDeps` + `buildStartDeps`. Lift `RunIssueStart` to a free function. Thread `prompter StartPrompter` through `createBranch`, `createWorktree`, `updateTrackerIssueStatus`, and through the calls to `issue.GetFromUser`/`GetFromTracker`. Replace four direct `huh.NewForm(...).RunWithContext(ctx)` calls with prompter methods. Migrate output routing from `fmt.Println` / `cmd.OutOrStderr()` to `deps.client.IO().Out` / `Err`. |
| `cmd/issue/conflict.go` | DELETE the `resolveBranchConflict` function (its body migrates into `huhStartPrompter.ResolveBranchConflict`). KEEP `rebuildVariantBranch` — the prompter implementation reuses it, and its existing test stays green. |
| `cmd/branch/branch.go` | Adapt `newRunE` to the new free-function entry point: build `startDeps` with `TrackerFirst: false`, construct a `huhStartPrompter`, call `RunIssueStart(ctx, deps, prompter)`. Drop the `issuecmd.New(b.appConfig)` indirection. |
| `README.md` | Add a "Testing the start flow" subsection alongside the existing "Testing the close flow" subsection. |
| `ROADMAP.md` | Mark item (1) of "End-to-end testability of interactive flows" as done. |

---

## Task 1: Define `StartPrompter` interface + `issue.Prompter` sub-interface + scripted test helper

This task lays down the contract before any caller wiring. No production code changes; nothing references the new types except a single compile-time assertion in the test file. The build stays green.

**Files:**
- Create: `cmd/issue/start_prompter.go`
- Create: `cmd/issue/start_prompter_test.go`
- Modify: `issue/issue.go`

- [ ] **Step 1: Add the `Prompter` sub-interface to `issue/issue.go`**

Edit `/workspace/issue/issue.go`. Add this interface near the top of the file, after the imports but before `IssueStartFlags`:

```go
// Prompter is the sub-interface of cmd/issue.StartPrompter that this package
// uses to drive issue-input forms. It exists so GetFromUser and GetFromTracker
// can be invoked from tests without opening huh forms.
type Prompter interface {
	// PickIssueFromUser opens the manual issue-input form (issue ID, subject, type).
	PickIssueFromUser(ctx context.Context, allowedTypes []string) (*Issue, error)

	// PickIssueFromTracker opens the picker over a pre-fetched issues list.
	PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*Issue, error)

	// NotifyTrackerError shows a one-line error note when the tracker errors
	// out or returns no open issues. Returning a non-nil error aborts the flow.
	NotifyTrackerError(ctx context.Context, message string) error
}
```

Do NOT change `GetFromUser` / `GetFromTracker` signatures yet — Task 3 handles that. This step adds the interface only.

- [ ] **Step 2: Create `cmd/issue/start_prompter.go`**

Write `/workspace/cmd/issue/start_prompter.go`:

```go
package issue

import (
	"context"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// BranchExistenceChecker is the slice of *git.Client that ResolveBranchConflict
// needs. Declared as a small interface so tests can substitute a fake without
// constructing a full *git.Client.
type BranchExistenceChecker interface {
	BranchExists(name string) (bool, error)
	Checkout(ctx context.Context, branch string) error
	IO() *pkg.IO
}

// StartPrompter resolves every user-facing decision in the issue-start flow.
// The production implementation drives huh forms; the test implementation in
// start_prompter_test.go returns canned values.
//
// Each method maps 1:1 to a huh.NewForm call in the pre-refactor flow. The
// order below mirrors the order calls happen in RunIssueStart.
type StartPrompter interface {
	// issuepkg.Prompter contributes the three issue-input methods used by
	// issuepkg.GetFromUser and issuepkg.GetFromTracker.
	issuepkg.Prompter

	// PickUseTracker drives the "fetch from tracker?" toggle. Called only when
	// cfg.IssueTracker.Type != "". trackerFirst controls the pre-selected
	// option (true for `issue start`, false for `branch new`).
	PickUseTracker(ctx context.Context, trackerType string, trackerFirst bool) (use bool, err error)

	// PickUseWorktree drives the "worktree vs plain branch?" toggle. Called
	// only when cfg.Branch.UseWorktree == nil.
	PickUseWorktree(ctx context.Context) (use bool, err error)

	// ConfirmCreateBranch gates client.CreateBranch. message is the full
	// human-readable "Create branch %q based on %q?" string.
	ConfirmCreateBranch(ctx context.Context, message string) (confirmed bool, err error)

	// ConfirmCreateWorktree gates client.CreateWorktree. message is the full
	// "Create worktree %q at %q based on %q?" string.
	ConfirmCreateWorktree(ctx context.Context, message string) (confirmed bool, err error)

	// PickTrackerStatus drives the tracker status-picker form. An empty return
	// signals "no selection" — the caller decides whether to treat that as
	// skip or abort.
	PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error)

	// ResolveBranchConflict owns the conflict-resolution loop. It checks
	// whether b's name already exists locally and, if so, drives the operator
	// through a picker (checkout / variant / abort). Returns (b, nil) on the
	// clean no-collision path. Returns (nil, nil) to signal "stop here, do
	// not create or persist" (operator chose abort or checked out the
	// existing branch). Returns (nil, err) on input or git failures.
	ResolveBranchConflict(ctx context.Context, client BranchExistenceChecker, b *branch.Branch, picked *issuepkg.Issue) (*branch.Branch, error)
}

// Compile-time check that *git.Client satisfies BranchExistenceChecker (the
// production caller). Catches accidental signature drift on *git.Client.
var _ BranchExistenceChecker = (*git.Client)(nil)
```

- [ ] **Step 3: Create `cmd/issue/start_prompter_test.go`**

Write `/workspace/cmd/issue/start_prompter_test.go`:

```go
package issue

import (
	"context"

	"github.com/piprim/git-zf/branch"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// Compile-time check: scriptedStartPrompter must satisfy StartPrompter.
var _ StartPrompter = (*scriptedStartPrompter)(nil)

// scriptedStartPrompter is the canned-response prompter used by
// start_e2e_test.go. Each field corresponds to one StartPrompter method's
// return value; *Err fields inject errors.
type scriptedStartPrompter struct {
	// Issue-input return values.
	IssueFromUser    *issuepkg.Issue
	IssueFromTracker *issuepkg.Issue

	// Toggle return values.
	UseTracker  bool
	UseWorktree bool

	// Confirm return values.
	ConfirmBranch   bool
	ConfirmWorktree bool

	// Tracker status picker return value (empty = skip).
	TrackerStatus string

	// ResolveBranchConflict return — set Branch to the (possibly variant)
	// *branch.Branch to proceed, or leave nil + ConflictAbort=true to signal
	// "stop, no error" (operator aborted or checked out existing).
	ConflictBranch *branch.Branch
	ConflictAbort  bool

	// Counter set by NotifyTrackerError so tests can assert "fallback fired".
	TrackerErrorNotifications int

	// Error injection — when non-nil, the corresponding method returns this
	// error immediately.
	IssueFromUserErr     error
	IssueFromTrackerErr  error
	TrackerErrorErr      error
	UseTrackerErr        error
	UseWorktreeErr       error
	ConfirmBranchErr     error
	ConfirmWorktreeErr   error
	TrackerStatusErr     error
	ConflictErr          error
}

func (s *scriptedStartPrompter) PickIssueFromUser(_ context.Context, _ []string) (*issuepkg.Issue, error) {
	if s.IssueFromUserErr != nil {
		return nil, s.IssueFromUserErr
	}

	return s.IssueFromUser, nil
}

func (s *scriptedStartPrompter) PickIssueFromTracker(_ context.Context, _ []tracker.Issue, _ []string) (*issuepkg.Issue, error) {
	if s.IssueFromTrackerErr != nil {
		return nil, s.IssueFromTrackerErr
	}

	return s.IssueFromTracker, nil
}

func (s *scriptedStartPrompter) NotifyTrackerError(_ context.Context, _ string) error {
	s.TrackerErrorNotifications++

	return s.TrackerErrorErr
}

func (s *scriptedStartPrompter) PickUseTracker(_ context.Context, _ string, _ bool) (bool, error) {
	if s.UseTrackerErr != nil {
		return false, s.UseTrackerErr
	}

	return s.UseTracker, nil
}

func (s *scriptedStartPrompter) PickUseWorktree(_ context.Context) (bool, error) {
	if s.UseWorktreeErr != nil {
		return false, s.UseWorktreeErr
	}

	return s.UseWorktree, nil
}

func (s *scriptedStartPrompter) ConfirmCreateBranch(_ context.Context, _ string) (bool, error) {
	if s.ConfirmBranchErr != nil {
		return false, s.ConfirmBranchErr
	}

	return s.ConfirmBranch, nil
}

func (s *scriptedStartPrompter) ConfirmCreateWorktree(_ context.Context, _ string) (bool, error) {
	if s.ConfirmWorktreeErr != nil {
		return false, s.ConfirmWorktreeErr
	}

	return s.ConfirmWorktree, nil
}

func (s *scriptedStartPrompter) PickTrackerStatus(_ context.Context, _, _ string, _ []string) (string, error) {
	if s.TrackerStatusErr != nil {
		return "", s.TrackerStatusErr
	}

	return s.TrackerStatus, nil
}

func (s *scriptedStartPrompter) ResolveBranchConflict(_ context.Context, _ BranchExistenceChecker, b *branch.Branch, _ *issuepkg.Issue) (*branch.Branch, error) {
	if s.ConflictErr != nil {
		return nil, s.ConflictErr
	}

	if s.ConflictAbort {
		return nil, nil
	}

	if s.ConflictBranch != nil {
		return s.ConflictBranch, nil
	}

	return b, nil
}
```

- [ ] **Step 4: Confirm the package still builds**

```bash
mise exec -- go build ./...
mise exec -- go test -count=1 -run NoSuchTest ./cmd/issue/... ./issue/...
```

Expected: clean build. The `-run NoSuchTest` invocation matches no tests but forces the test binary to compile — that's what exercises the `var _ StartPrompter = (*scriptedStartPrompter)(nil)` assertion.

- [ ] **Step 5: Commit**

```bash
git add issue/issue.go cmd/issue/start_prompter.go cmd/issue/start_prompter_test.go
git commit -m "refactor(issue): introduce StartPrompter interface and scripted test helper"
```

---

## Task 2: Implement `huhStartPrompter` (production)

Add the production implementation. After this task `huhStartPrompter` exists, compiles, and is unused — Tasks 4–6 wire it.

**Files:**
- Modify: `cmd/issue/start_prompter.go`

- [ ] **Step 1: Extend the import block**

Edit `/workspace/cmd/issue/start_prompter.go`. Replace the existing import block with:

```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
)
```

(`errors`, `fmt`, `huh`, `tui` are the new additions.)

- [ ] **Step 2: Append `huhStartPrompter` to the same file**

Append below the existing `var _ BranchExistenceChecker = (*git.Client)(nil)` line:

```go
// Compile-time check.
var _ StartPrompter = (*huhStartPrompter)(nil)

// huhStartPrompter is the production StartPrompter. It opens real huh forms.
// Constructed once per `issue start` (or `branch new`) invocation.
type huhStartPrompter struct{}

func newHuhStartPrompter() *huhStartPrompter {
	return &huhStartPrompter{}
}

func (p *huhStartPrompter) PickIssueFromUser(ctx context.Context, allowedTypes []string) (*issuepkg.Issue, error) {
	var got issuepkg.Issue
	if err := huh.NewForm(
		tui.IssueInput(&got.ID, &got.Subject, &got.Type, allowedTypes),
	).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("issue form: %w", err)
	}

	return &got, nil
}

func (p *huhStartPrompter) PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*issuepkg.Issue, error) {
	var got issuepkg.Issue
	var pickedIssue tracker.Issue

	picker := tui.IssueTrackerPicker(issues, &pickedIssue, allowedTypes, &got.Type)
	if err := huh.NewForm(picker).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	got.ID = pickedIssue.ID
	got.Subject = pickedIssue.Subject
	got.TrackerType = pickedIssue.TrackerType

	return &got, nil
}

func (p *huhStartPrompter) NotifyTrackerError(ctx context.Context, message string) error {
	if err := huh.NewForm(tui.IssueTrackerError(message)).RunWithContext(ctx); err != nil {
		return fmt.Errorf("error note: %w", err)
	}

	return nil
}

func (p *huhStartPrompter) PickUseTracker(ctx context.Context, trackerType string, trackerFirst bool) (bool, error) {
	var use bool
	form := tui.IssueTrackerToggle(&use, trackerFirst, trackerType)
	if err := huh.NewForm(form).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("tracker toggle: %w", err)
	}

	return use, nil
}

func (p *huhStartPrompter) PickUseWorktree(ctx context.Context) (bool, error) {
	var use bool
	if err := huh.NewForm(tui.WorktreeToggle(&use)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("worktree toggle: %w", err)
	}

	return use, nil
}

func (p *huhStartPrompter) ConfirmCreateBranch(ctx context.Context, message string) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(message, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm branch: %w", err)
	}

	return confirmed, nil
}

func (p *huhStartPrompter) ConfirmCreateWorktree(ctx context.Context, message string) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(message, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm worktree: %w", err)
	}

	return confirmed, nil
}

func (p *huhStartPrompter) PickTrackerStatus(ctx context.Context, issueID, trackerType string, statuses []string) (string, error) {
	var selected string
	if err := huh.NewForm(tui.IssueStatusPicker(issueID, trackerType, statuses, &selected)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("status picker form: %w", err)
	}

	return selected, nil
}

// ResolveBranchConflict is the production conflict-loop. It is the body of
// the pre-refactor cmd/issue/conflict.go:resolveBranchConflict, lifted
// verbatim onto the prompter so tests can substitute a scripted result.
func (p *huhStartPrompter) ResolveBranchConflict(ctx context.Context, client BranchExistenceChecker, b *branch.Branch, picked *issuepkg.Issue) (*branch.Branch, error) {
	for {
		exists, err := client.BranchExists(b.Name())
		if err != nil {
			return nil, fmt.Errorf("check branch exists: %w", err)
		}

		if !exists {
			return b, nil
		}

		var action string
		if err := huh.NewForm(tui.BranchConflictPicker(b.Name(), &action)).RunWithContext(ctx); err != nil {
			return nil, fmt.Errorf("conflict picker: %w", err)
		}

		switch action {
		case "checkout":
			if err := client.Checkout(ctx, b.Name()); err != nil {
				return nil, fmt.Errorf("checkout existing: %w", err)
			}

			fmt.Fprintf(client.IO().Out, "Switched to existing branch %q\n", b.Name())

			return nil, nil
		case "abort":
			fmt.Fprintln(client.IO().Out, "Aborted.")

			return nil, nil
		case "variant":
			var label string
			if err := huh.NewForm(tui.VariantLabelInput(&label)).RunWithContext(ctx); err != nil {
				return nil, fmt.Errorf("variant input: %w", err)
			}

			newB, err := rebuildVariantBranch(picked, label)
			if err != nil {
				return nil, err
			}

			b = newB
		default:
			return nil, fmt.Errorf("unknown conflict action %q", action)
		}
	}
}

// silenceUnused keeps errors imported until Task 4+ may need it.
var _ = errors.New
```

(`tracker` import is used by `PickIssueFromTracker`; if your IDE/goimports thinks `errors` is unused, drop the `var _ = errors.New` line.)

- [ ] **Step 3: Verify build + full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, every existing test passes. `huhStartPrompter` is defined but unused — Go allows that for top-level declarations.

- [ ] **Step 4: Commit**

```bash
git add cmd/issue/start_prompter.go
git commit -m "refactor(issue): add huhStartPrompter production implementation"
```

---

## Task 3: Update `issue.GetFromUser` / `issue.GetFromTracker` to accept the `Prompter`

This task changes the public API of the `issue` package. The two callers in `cmd/issue/start.go` get updated in the same task to keep the build green at commit time.

**Files:**
- Modify: `issue/issue.go`
- Modify: `cmd/issue/start.go`

- [ ] **Step 1: Change `GetFromUser` to take a `Prompter`**

Edit `/workspace/issue/issue.go`. Replace the existing `GetFromUser` body:

```go
// GetFromUser drives the manual issue-input flow via p.PickIssueFromUser.
func GetFromUser(ctx context.Context, p Prompter, allowedTypes []string) (*Issue, error) {
	out, err := p.PickIssueFromUser(ctx, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("issue input: %w", err)
	}

	return out, nil
}
```

- [ ] **Step 2: Change `GetFromTracker` to take a `Prompter`**

Replace the existing `GetFromTracker` body:

```go
// GetFromTracker fetches issues via t.ListIssues, then either falls back to
// the manual path (PickIssueFromUser) on error/empty-list, or drives the
// tracker picker (PickIssueFromTracker). All form opening is delegated to p.
func GetFromTracker(ctx context.Context, p Prompter, t tracker.Tracker, allowedTypes []string) (*Issue, error) {
	errMsg := ""
	issues, listErr := t.ListIssues(ctx)
	if listErr != nil {
		errMsg = listErr.Error()
	}

	if listErr == nil && len(issues) == 0 {
		errMsg = "no open issues assigned to you"
	}

	if errMsg != "" {
		if err := p.NotifyTrackerError(ctx, errMsg); err != nil {
			return nil, fmt.Errorf("notify tracker error: %w", err)
		}

		return GetFromUser(ctx, p, allowedTypes)
	}

	out, err := p.PickIssueFromTracker(ctx, issues, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	return out, nil
}
```

The `huh` import becomes unused — drop it. The remaining imports are `context`, `fmt`, `tracker`. `tui` may also become unused — verify with `mise exec -- go build ./issue/...` and remove imports as needed.

- [ ] **Step 3: Update the two call sites in `cmd/issue/start.go`**

Both call sites currently pass `(ctx, allowedTypes)` or `(ctx, t, allowedTypes)`. They need a `Prompter` argument.

For now (Task 3), the `huhStartPrompter` exists but `RunIssueStart` doesn't yet take a prompter (that's Task 4). So we construct one inline at the top of `RunIssueStart`:

In `RunIssueStart` (currently around line 50), find this block:

```go
if trackerCfg.Type != "" {
    t, err = tracker.New(trackerCfg)
    if err != nil {
        return fmt.Errorf("failed get tracker: %w", err)
    }

    pickedIssue, err = i.getFromTracker(ctx, t, flags, allowedBranchTypes)
    if err != nil {
        return fmt.Errorf("failed to retreive issue from tracker: %w", err)
    }
} else {
    pickedIssue, err = issue.GetFromUser(ctx, allowedBranchTypes)
    if err != nil {
        return fmt.Errorf("failed to retreive issue from user: %w", err)
    }
}
```

Replace with:

```go
prompter := newHuhStartPrompter()

if trackerCfg.Type != "" {
    t, err = tracker.New(trackerCfg)
    if err != nil {
        return fmt.Errorf("failed get tracker: %w", err)
    }

    pickedIssue, err = i.getFromTracker(ctx, t, prompter, flags, allowedBranchTypes)
    if err != nil {
        return fmt.Errorf("failed to retreive issue from tracker: %w", err)
    }
} else {
    pickedIssue, err = issue.GetFromUser(ctx, prompter, allowedBranchTypes)
    if err != nil {
        return fmt.Errorf("failed to retreive issue from user: %w", err)
    }
}
```

Then update the `(i Issue) getFromTracker` helper's signature and body (currently around line 106):

```go
func (i Issue) getFromTracker(
	ctx context.Context,
	t tracker.Tracker,
	prompter StartPrompter,
	flags issue.IssueStartFlags,
	allowedBranchTypes []string,
) (*issue.Issue, error) {
	useTracker, err := prompter.PickUseTracker(ctx, i.appConfig.IssueTracker.Type, flags.TrackerFirst)
	if err != nil {
		return nil, err
	}

	if useTracker {
		got, err := issue.GetFromTracker(ctx, prompter, t, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("failed to retreive issue from tracker: %w", err)
		}

		return got, nil
	}

	return nil, nil
}
```

(The function previously opened the tracker-toggle huh form itself; that now goes through `prompter.PickUseTracker`. The previous `nil, nil` fall-through semantics — operator declined the tracker toggle — are preserved.)

- [ ] **Step 4: Verify**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, all existing tests pass.

- [ ] **Step 5: Commit**

```bash
git add issue/issue.go cmd/issue/start.go
git commit -m "refactor(issue): route GetFromUser/GetFromTracker through Prompter"
```

---

## Task 4: Extract `startDeps` + `buildStartDeps`; lift `RunIssueStart` to a free function

The largest task in the plan. Touches the heart of `cmd/issue/start.go`. The build must stay green at commit time.

**Files:**
- Modify: `cmd/issue/start.go`

- [ ] **Step 1: Add `startDeps` + `buildStartDeps`**

Insert at the top of `cmd/issue/start.go`, after the imports and before `getStartCmd`:

```go
// startDeps bundles the long-lived dependencies the start flow needs.
// Production code builds it via buildStartDeps; tests inject directly.
type startDeps struct {
	client  *git.Client
	cfg     *config.AppConfig
	tracker tracker.Tracker // nil when cfg.IssueTracker.Type == "" OR factory failed (warn)
	flags   issue.IssueStartFlags
}

// buildStartDeps constructs the production startDeps from a cobra command.
// Returns an error if the repo cannot be opened. When cfg.IssueTracker.Type
// == "" the returned deps.tracker is nil (RunIssueStart treats that as "no
// tracker available"). When the tracker factory fails, the error is
// non-fatal — buildStartDeps warns and returns deps with a nil tracker.
func buildStartDeps(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig, flags issue.IssueStartFlags) (startDeps, error) {
	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return startDeps{}, fmt.Errorf("not a git repository: %w", err)
	}

	if cfg.Branch.Remote != "" {
		client.SetRemote(cfg.Branch.Remote)
	}

	deps := startDeps{client: client, cfg: cfg, flags: flags}

	if cfg.IssueTracker.Type != "" {
		t, err := tracker.New(cfg.IssueTracker)
		if err != nil {
			fmt.Fprintf(client.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			deps.tracker = t
		}
	}

	return deps, nil
}
```

You will need to add the `"github.com/piprim/git-zf/internal/pkg"` import (the `pkg.IO` struct comes from there).

- [ ] **Step 2: Replace `(i Issue) RunIssueStart` with a free function `RunIssueStart`**

Replace the existing method `func (i Issue) RunIssueStart(cmd *cobra.Command, flags issue.IssueStartFlags) error` (currently around lines 50-104) with the free function below. Note: this is a major rewrite — the function changes signature and threads `prompter` and `deps` throughout.

```go
// RunIssueStart is the prompter-driven core of the issue-start flow. Called
// from closeRunE (production, via a huhStartPrompter) and from tests (via a
// scriptedStartPrompter). trackerFirst is carried inside deps.flags.
func RunIssueStart(ctx context.Context, deps startDeps, prompter StartPrompter) error {
	allowedBranchTypes := make([]string, 0, len(deps.cfg.CommitTypes))
	for _, t := range deps.cfg.CommitTypes {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}

	if len(allowedBranchTypes) == 0 {
		return errors.New("config: no commit types found")
	}

	pickedIssue, err := pickIssue(ctx, deps, prompter, allowedBranchTypes)
	if err != nil {
		return err
	}

	if pickedIssue == nil {
		return nil
	}

	useWorktree, err := resolveUseWorktree(ctx, deps, prompter)
	if err != nil {
		return err
	}

	if useWorktree {
		return createWorktreeFlow(ctx, deps, prompter, pickedIssue)
	}

	return createBranchFlow(ctx, deps, prompter, pickedIssue)
}

// pickIssue chooses between tracker-driven and user-driven issue input. A
// (nil, nil) return signals "operator declined the tracker toggle and there
// is no manual fallback path" (currently unreachable — the tracker-toggle
// flow always falls through to GetFromUser); the caller still guards
// against it.
func pickIssue(ctx context.Context, deps startDeps, prompter StartPrompter, allowedBranchTypes []string) (*issue.Issue, error) {
	if deps.tracker == nil {
		got, err := issue.GetFromUser(ctx, prompter, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("issue from user: %w", err)
		}

		return got, nil
	}

	useTracker, err := prompter.PickUseTracker(ctx, deps.cfg.IssueTracker.Type, deps.flags.TrackerFirst)
	if err != nil {
		return nil, err
	}

	if !useTracker {
		got, err := issue.GetFromUser(ctx, prompter, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("issue from user: %w", err)
		}

		return got, nil
	}

	got, err := issue.GetFromTracker(ctx, prompter, deps.tracker, allowedBranchTypes)
	if err != nil {
		return nil, fmt.Errorf("issue from tracker: %w", err)
	}

	return got, nil
}

// resolveUseWorktree consults the config override; falls back to the prompter
// only when the override is absent (nil).
func resolveUseWorktree(ctx context.Context, deps startDeps, prompter StartPrompter) (bool, error) {
	if deps.cfg.Branch.UseWorktree != nil {
		return *deps.cfg.Branch.UseWorktree, nil
	}

	use, err := prompter.PickUseWorktree(ctx)
	if err != nil {
		return false, err
	}

	return use, nil
}
```

- [ ] **Step 3: Rewrite the createBranch / createWorktree helpers as free functions**

The two methods `(i Issue) createBranch` and `(i Issue) createWorktree` lose their receiver. Replace them with these free functions (delete the old methods entirely):

```go
func createBranchFlow(ctx context.Context, deps startDeps, prompter StartPrompter, picked *issue.Issue) error {
	b, base, err := prepareBranch(deps, picked)
	if err != nil {
		return err
	}

	b, err = prompter.ResolveBranchConflict(ctx, deps.client, b, picked)
	if err != nil {
		return err
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	confirmed, err := prompter.ConfirmCreateBranch(ctx,
		fmt.Sprintf("Create branch %q based on %q?", branchName, base))
	if err != nil {
		return err
	}

	if !confirmed {
		fmt.Fprintln(deps.client.IO().Out, "Aborted.")

		return nil
	}

	if err := deps.client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	var tt *string
	if picked.TrackerType != "" {
		tt = &deps.cfg.IssueTracker.Type
	}

	if err := persist(ctx, b, picked.Subject, tt); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Fprintf(deps.client.IO().Out, "Switched to new branch %q (based on %q)\n", branchName, base)

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}

func createWorktreeFlow(ctx context.Context, deps startDeps, prompter StartPrompter, picked *issue.Issue) error {
	b, base, err := prepareBranch(deps, picked)
	if err != nil {
		return err
	}

	b, err = prompter.ResolveBranchConflict(ctx, deps.client, b, picked)
	if err != nil {
		return err
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	repoRoot, err := deps.client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	repoName, err := deps.client.RepoName()
	if err != nil {
		return fmt.Errorf("resolve repo name: %w", err)
	}

	path := worktreePath(repoRoot, deps.cfg.Branch.WorktreeDir, repoName, branchName)

	confirmed, err := prompter.ConfirmCreateWorktree(ctx,
		fmt.Sprintf("Create worktree %q at %q based on %q?", branchName, path, base))
	if err != nil {
		return err
	}

	if !confirmed {
		fmt.Fprintln(deps.client.IO().Out, "Aborted.")

		return nil
	}

	if err := deps.client.CreateWorktree(ctx, branchName, base, path); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	var tt *string
	if picked.TrackerType != "" {
		tt = &deps.cfg.IssueTracker.Type
	}

	if err := persist(ctx, b, picked.Subject, tt); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: worktree created but store record failed: %v\n", err)
	}

	fmt.Fprintf(deps.client.IO().Out, "Created worktree %q at %q (based on %q)\n", branchName, path, base)
	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	fmt.Fprintln(deps.client.IO().Out, hintStyle.Render("Run 'cd "+path+"' to begin working."))

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}
```

- [ ] **Step 4: Rewrite `prepareBranch` as a free function**

Replace `(i Issue) prepareBranch` with:

```go
// prepareBranch assembles the branch and resolves the base branch. Shared by
// createBranchFlow and createWorktreeFlow.
func prepareBranch(deps startDeps, picked *issue.Issue) (b *branch.Branch, base string, err error) {
	b, err = branch.New(picked.ID, picked.Type, picked.Subject, deps.flags.Variant)
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}

	base = deps.cfg.Branch.Base
	if base == "" {
		base, err = deps.client.DefaultBaseBranch()
		if err != nil {
			return nil, "", fmt.Errorf("detect base branch: %w", err)
		}
	}

	return b, base, nil
}
```

- [ ] **Step 5: Rewrite `updateTrackerIssueStatus` as a free function**

Replace `(i Issue) updateTrackerIssueStatus` with:

```go
// updateTrackerStatus runs the tracker status-picker form and applies the
// chosen status. All errors are non-fatal warnings (the branch was already
// created — the operator must be able to clean up tracker drift manually).
func updateTrackerStatus(ctx context.Context, deps startDeps, prompter StartPrompter, issueID string) {
	if deps.tracker == nil {
		return
	}

	statuses, err := deps.tracker.ListStatuses(ctx)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	selected, err := prompter.PickTrackerStatus(ctx, issueID, deps.cfg.IssueTracker.Type, statuses)
	if err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: status picker: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := deps.tracker.UpdateIssueStatus(ctx, issueID, selected); err != nil {
		fmt.Fprintf(deps.client.IO().Err, "warning: could not update tracker status: %v\n", err)
	}
}
```

- [ ] **Step 6: Shrink `startRunE` to a thin shim**

Replace the existing `startRunE` with:

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

	return RunIssueStart(cmd.Context(), deps, newHuhStartPrompter())
}
```

- [ ] **Step 7: Delete the now-stale `(i Issue) getFromTracker` helper**

The pickIssue function in Step 2 replaces it. Delete the `func (i Issue) getFromTracker(...) {...}` block entirely from `cmd/issue/start.go`.

- [ ] **Step 8: Adjust imports**

After all the edits, the following imports must be present in `cmd/issue/start.go`:

```go
import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/mitchellh/go-homedir"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)
```

(`huh` and `tui` are dropped — every form call now goes through `prompter`. `pkg`, `config` added — used by `buildStartDeps`.)

- [ ] **Step 9: Verify**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
mise exec -- go vet ./...
```

Expected: clean build, all existing tests pass, no vet warnings.

- [ ] **Step 10: Commit**

```bash
git add cmd/issue/start.go
git commit -m "refactor(issue): extract RunIssueStart behind StartPrompter interface"
```

---

## Task 5: Delete the standalone `resolveBranchConflict`

After Task 2, `huhStartPrompter.ResolveBranchConflict` is the canonical implementation. After Task 4, both `createBranchFlow` and `createWorktreeFlow` call `prompter.ResolveBranchConflict`. The original `resolveBranchConflict` in `conflict.go` is now dead code.

**Files:**
- Modify: `cmd/issue/conflict.go`

- [ ] **Step 1: Delete `resolveBranchConflict` from `conflict.go`**

Edit `/workspace/cmd/issue/conflict.go`. Delete the entire `func resolveBranchConflict(...) {...}` block (currently lines 15-74). **Keep** the `rebuildVariantBranch` function — `huhStartPrompter.ResolveBranchConflict` calls it.

After the deletion, the file should consist of:
- The package declaration + imports
- The `rebuildVariantBranch` function only

Clean up unused imports. After deleting `resolveBranchConflict`, `huh`, `git`, `issue`, `tui`, and `context` may be unused. The minimum import set for `rebuildVariantBranch` is:

```go
import (
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/issue"
)
```

- [ ] **Step 2: Verify**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, all tests pass — including the existing `conflict_test.go` tests (`TestRebuildVariantBranch_valid` etc.), which still reference `rebuildVariantBranch` and that function is intact.

- [ ] **Step 3: Commit**

```bash
git add cmd/issue/conflict.go
git commit -m "refactor(issue): inline resolveBranchConflict into huhStartPrompter"
```

---

## Task 6: Adapt `cmd/branch/branch.go`'s `newRunE` to the new entry point

`cmd/branch new` shares the issue-start flow via the old `(i Issue) RunIssueStart` method. After Task 4 lifted that to a free function, `newRunE` must be updated.

**Files:**
- Modify: `cmd/branch/branch.go`

- [ ] **Step 1: Locate `newRunE` in `cmd/branch/branch.go`**

Use `grep -n "newRunE" /workspace/cmd/branch/branch.go` to find the function. Read it in full plus its imports.

- [ ] **Step 2: Replace the body of `newRunE`**

The previous implementation called `issuecmd.New(b.appConfig)` to build an `Issue` instance and then invoked its `RunIssueStart` method. Replace with the free-function entry point:

```go
func (b Branch) newRunE(cmd *cobra.Command, _ []string) error {
	variant, err := cmd.Flags().GetString("variant")
	if err != nil {
		return fmt.Errorf("read --variant flag: %w", err)
	}

	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: variant}
	deps, err := issuecmd.BuildStartDeps(cmd.Context(), cmd, b.appConfig, flags)
	if err != nil {
		return err
	}

	if err := issuecmd.RunIssueStart(cmd.Context(), deps, issuecmd.NewHuhStartPrompter()); err != nil {
		return fmt.Errorf("failed to run issueStart: %w", err)
	}

	return nil
}
```

Note: `BuildStartDeps`, `RunIssueStart`, and `NewHuhStartPrompter` must be **exported** from `cmd/issue` for `cmd/branch` to import them. Go back to Task 4 Step 1 and rename `buildStartDeps` → `BuildStartDeps`. Go back to Task 4 Step 2 — `RunIssueStart` is already exported. Go back to Task 2 Step 2 — rename `newHuhStartPrompter` → `NewHuhStartPrompter`. Similarly, `startDeps` must be exported as `StartDeps`. Do these renames consistently across the codebase before running the build below.

(If during Task 4/6 the engineer prefers to keep `startDeps`/`buildStartDeps`/`newHuhStartPrompter` unexported and instead re-export via small wrappers in cmd/issue, that's acceptable — the spec only requires `cmd/branch` can call them. The simplest path is exporting; preserve the export decisions consistently.)

- [ ] **Step 3: Check imports**

The new `newRunE` body needs:

```go
import (
	// ...existing imports...
	issuecmd "github.com/piprim/git-zf/cmd/issue"
	"github.com/piprim/git-zf/issue"
)
```

(`issuecmd` may already be aliased — check existing imports first. Drop any imports that became unused after the change, especially `issuecmd.New` if it's no longer referenced.)

- [ ] **Step 4: Verify**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
mise exec -- go vet ./...
```

Expected: clean build, all tests pass.

Smoke-test by hand if a sandbox repo is available:

```bash
mise exec -- go build -o ./bin/git-zf .
./bin/git-zf branch new --help    # should still list the --variant flag
```

- [ ] **Step 5: Commit**

```bash
git add cmd/branch/branch.go cmd/issue/start.go cmd/issue/start_prompter.go
git commit -m "refactor(branch): adapt newRunE to free-function RunIssueStart entry point"
```

(The `start.go` and `start_prompter.go` changes are the export renames from Step 2.)

---

## Task 7: `startTestRig` + three happy-path E2E tests

**Files:**
- Create: `cmd/issue/start_e2e_test.go`

- [ ] **Step 1: Verify prerequisite identifiers**

Confirm these are present in the codebase before writing the rig — STOP and report BLOCKED if any are missing:

- `git.NewClientAt(io, dir)` — see `git/git.go:51`
- `git.Client.BranchExists(name)` — see `git/git.go:436`
- `store.Open(ctx, dir)` — see `store/store.go:94`
- `tracker.New(config.IssueTrackerConfig{Type: "fake"})` — `tracker/fake/fake.go` should auto-register the `"fake"` adapter (already shipped in Task 5 of the close-flow plan)

Also verify the fake-tracker registration helper exists at `cmd/issue/register_fake_tracker_test.go` (also shipped in close-flow plan). If absent, add it:

```go
// /workspace/cmd/issue/register_fake_tracker_test.go
package issue

import (
	_ "github.com/piprim/git-zf/tracker/fake"
)
```

- [ ] **Step 2: Write the rig**

Create `/workspace/cmd/issue/start_e2e_test.go`:

```go
package issue

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/internal/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// startTestRig bundles a real on-disk git repo (one initial commit on main)
// and a fake tracker so each E2E test sets up state in one line. Unlike the
// close-flow rig, no branch is pre-seeded — start CREATES the branch.
type startTestRig struct {
	dir     string
	client  *git.Client
	tracker *fake.Tracker
	cfg     *config.AppConfig
	stdout  *bytes.Buffer
	stderr  *bytes.Buffer
}

func newStartRig(t *testing.T) *startTestRig {
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

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	ioStreams := &pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}

	client, err := git.NewClientAt(ioStreams, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"
	cfg.CommitTypes = []config.CommitType{
		{Name: "feat", Description: "feature"},
		{Name: "fix", Description: "fix"},
	}

	rawT, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	fakeT, ok := rawT.(*fake.Tracker)
	if !ok {
		t.Fatalf("tracker.New returned %T, want *fake.Tracker", rawT)
	}

	return &startTestRig{
		dir: dir, client: client, tracker: fakeT, cfg: cfg,
		stdout: stdout, stderr: stderr,
	}
}

// deps returns a StartDeps wired with the rig's client + fake tracker.
// flags lets the test choose Variant / TrackerFirst per case.
func (r *startTestRig) deps(flags issue.IssueStartFlags) StartDeps {
	return StartDeps{client: r.client, cfg: r.cfg, tracker: r.tracker, flags: flags}
}

// noTrackerDeps returns a StartDeps with deps.tracker = nil (manual-only path).
func (r *startTestRig) noTrackerDeps(flags issue.IssueStartFlags) StartDeps {
	return StartDeps{client: r.client, cfg: r.cfg, tracker: nil, flags: flags}
}

// silenceUnused keeps io.Discard reachable in case future tests need it.
var _ = io.Discard
```

(`config.CommitType` field names — verify against `/workspace/config/config.go`. If the struct field is `Type` not `Name`, adjust here.)

(The `startDeps` field accessor — depending on how Task 4 / Task 6 settled the export question, the struct may be `startDeps` (unexported) or `StartDeps` (exported). The rig must match. If unexported and the rig is in the same package, lowercase `startDeps` is fine.)

- [ ] **Step 3: Verify the rig compiles**

```bash
mise exec -- go test -count=1 -run NoSuchTest ./cmd/issue/...
```

Expected: clean compile, no test runs yet.

- [ ] **Step 4: Append `TestRunIssueStart_BranchHappyPath_NoTracker`**

Append to `/workspace/cmd/issue/start_e2e_test.go`:

```go
func TestRunIssueStart_BranchHappyPath_NoTracker(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: ""}

	pickedIssue := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:      "ABC-2",
			Subject: "Add login form",
		},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: pickedIssue,
		UseWorktree:   false,
		ConfirmBranch: true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-2@feat@add-login-form"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("stdout reports the switch", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte(wantBranch)) {
			t.Errorf("stdout = %q, want it to mention %q", got, wantBranch)
		}
	})

	t.Run("tracker was not called", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates len = %d, want 0 on no-tracker path", got)
		}
	})
}
```

- [ ] **Step 5: Append `TestRunIssueStart_BranchHappyPath_WithTracker`**

```go
func TestRunIssueStart_BranchHappyPath_WithTracker(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: true, Variant: ""}

	pickedFromTracker := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:          "ABC-3",
			Subject:     "Implement OAuth",
			TrackerType: "fake",
		},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromTracker: pickedFromTracker,
		UseWorktree:      false,
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-3@feat@implement-oauth"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("tracker received one UpdateIssueStatus call", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 1 {
			t.Fatalf("RecordedUpdates len = %d, want 1", got)
		}
		got := rig.tracker.RecordedUpdates[0]
		want := fake.Update{IssueID: "ABC-3", StatusName: "In Progress"}
		if got != want {
			t.Errorf("RecordedUpdates[0] = %+v, want %+v", got, want)
		}
	})
}
```

- [ ] **Step 6: Append `TestRunIssueStart_WorktreeHappyPath`**

```go
func TestRunIssueStart_WorktreeHappyPath(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: ""}

	pickedIssue := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:      "ABC-4",
			Subject: "Add metrics",
		},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser:   pickedIssue,
		UseWorktree:     true,
		ConfirmWorktree: true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	wantBranch := "ABC-4@feat@add-metrics"

	t.Run("branch ref exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Errorf("branch %q was not created", wantBranch)
		}
	})

	t.Run("worktree directory exists", func(t *testing.T) {
		// worktreePath sibling-of-repo-root convention.
		parent := filepath.Dir(rig.dir)
		repoName := filepath.Base(rig.dir)
		wantPath := filepath.Join(parent, repoName+"--"+wantBranch)
		if _, err := os.Stat(wantPath); err != nil {
			t.Errorf("worktree dir %q not found: %v", wantPath, err)
		}
	})

	t.Run("stdout mentions the cd hint", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte("Run 'cd")) {
			t.Errorf("stdout = %q, want it to contain the 'Run cd' hint", got)
		}
	})
}
```

- [ ] **Step 7: Run all three happy-path tests**

```bash
mise exec -- go test -run "TestRunIssueStart_(BranchHappyPath_NoTracker|BranchHappyPath_WithTracker|WorktreeHappyPath)" -v ./cmd/issue/... -count=1
```

Expected: all three PASS with their named subtests.

If any subtest fails on an unexpected branch name, the most likely cause is the slug being computed differently — verify with `mise exec -- go run /workspace/branch/cmd/...` or by running `branch.Slug("Add login form")` in a scratch test.

- [ ] **Step 8: Commit**

```bash
git add cmd/issue/start_e2e_test.go
git commit -m "test(issue): add start-flow E2E rig and three happy-path tests"
```

---

## Task 8: Five failure-mode E2E tests

**Files:**
- Modify: `cmd/issue/start_e2e_test.go`

- [ ] **Step 1: `TestRunIssueStart_BranchUserAbortsAtConfirm`**

Append:

```go
func TestRunIssueStart_BranchUserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: ""}

	picked := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{ID: "ABC-5", Subject: "Add cache"},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: picked,
		UseWorktree:   false,
		ConfirmBranch: false, // operator declines
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("branch not created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-5@feat@add-cache")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if exists {
			t.Error("branch was created despite operator abort")
		}
	})

	t.Run("stdout shows Aborted message", func(t *testing.T) {
		if got := rig.stdout.String(); !bytes.Contains([]byte(got), []byte("Aborted.")) {
			t.Errorf("stdout = %q, want it to mention 'Aborted.'", got)
		}
	})
}
```

- [ ] **Step 2: `TestRunIssueStart_VariantOnCollision`**

Append:

```go
func TestRunIssueStart_VariantOnCollision(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: ""}

	// Pre-create the deterministic branch on disk so RunIssueStart hits
	// the conflict path.
	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("branch", "ABC-6@feat@add-search", "main")

	picked := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{ID: "ABC-6", Subject: "Add search"},
	}

	// The scripted prompter's ResolveBranchConflict returns a variant branch
	// rather than the original. Build it ahead of time.
	variantBranch, err := rebuildVariantBranch(picked, "spike")
	if err != nil {
		t.Fatalf("rebuildVariantBranch: %v", err)
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser:  picked,
		ConflictBranch: variantBranch,
		UseWorktree:    false,
		ConfirmBranch:  true,
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("variant branch exists", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-6@feat@add-search@spike")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("variant branch ABC-6@feat@add-search@spike was not created")
		}
	})

	t.Run("original branch is untouched", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-6@feat@add-search")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("pre-existing branch ABC-6@feat@add-search was deleted unexpectedly")
		}
	})
}
```

- [ ] **Step 3: `TestRunIssueStart_AbortOnCollision`**

Append:

```go
func TestRunIssueStart_AbortOnCollision(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: false, Variant: ""}

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = rig.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("branch", "ABC-7@feat@add-export", "main")

	picked := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{ID: "ABC-7", Subject: "Add export"},
	}

	prompter := &scriptedStartPrompter{
		IssueFromUser: picked,
		ConflictAbort: true, // scripted prompter returns (nil, nil) from ResolveBranchConflict
	}

	if err := RunIssueStart(t.Context(), rig.noTrackerDeps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("no new branch was created", func(t *testing.T) {
		// The pre-existing branch is still there; check no variant was made.
		exists, err := rig.client.BranchExists("ABC-7@feat@add-export@spike")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if exists {
			t.Error("variant branch was unexpectedly created on abort path")
		}
	})

	t.Run("tracker untouched", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates = %d, want 0 on abort path", got)
		}
	})
}
```

- [ ] **Step 4: `TestRunIssueStart_TrackerListErrorFallsBackToManual`**

Append:

```go
func TestRunIssueStart_TrackerListErrorFallsBackToManual(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: true, Variant: ""}

	// Force the fake tracker to report "no open issues". The fake currently
	// has an empty Issues slice by default — that's what we need.

	manualPicked := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{ID: "ABC-8", Subject: "Manual fallback"},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:    true, // operator accepts the toggle
		IssueFromUser: manualPicked,
		UseWorktree:   false,
		ConfirmBranch: true,
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("NotifyTrackerError was called once", func(t *testing.T) {
		if got := prompter.TrackerErrorNotifications; got != 1 {
			t.Errorf("TrackerErrorNotifications = %d, want 1", got)
		}
	})

	t.Run("manual fallback branch was created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-8@feat@manual-fallback")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("manual fallback branch was not created")
		}
	})
}
```

- [ ] **Step 5: `TestRunIssueStart_NoTrackerStatusUpdate`**

Append:

```go
func TestRunIssueStart_NoTrackerStatusUpdate(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issue.IssueStartFlags{TrackerFirst: true, Variant: ""}

	// Seed one tracker issue so the picker path is exercised.
	rig.tracker.Issues = []tracker.Issue{
		{ID: "ABC-9", Subject: "Add lints", TrackerType: "fake"},
	}

	pickedFromTracker := &issue.Issue{
		Type: "feat",
		Issue: tracker.Issue{
			ID:          "ABC-9",
			Subject:     "Add lints",
			TrackerType: "fake",
		},
	}

	prompter := &scriptedStartPrompter{
		UseTracker:       true,
		IssueFromTracker: pickedFromTracker,
		UseWorktree:      false,
		ConfirmBranch:    true,
		TrackerStatus:    "", // empty = skip
	}

	if err := RunIssueStart(t.Context(), rig.deps(flags), prompter); err != nil {
		t.Fatalf("RunIssueStart: %v", err)
	}

	t.Run("branch was still created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-9@feat@add-lints")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !exists {
			t.Error("branch was not created when tracker status was skipped")
		}
	})

	t.Run("tracker received no UpdateIssueStatus calls", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates = %d, want 0 when TrackerStatus is empty", got)
		}
	})
}
```

- [ ] **Step 6: Run all five failure-mode tests**

```bash
mise exec -- go test -run "TestRunIssueStart_(BranchUserAbortsAtConfirm|VariantOnCollision|AbortOnCollision|TrackerListErrorFallsBackToManual|NoTrackerStatusUpdate)" -v ./cmd/issue/... -count=1
```

Expected: all five PASS with their named subtests.

- [ ] **Step 7: Run the full project test suite to confirm no regressions**

```bash
mise exec -- go test ./... -count=1
```

Expected: every package green.

- [ ] **Step 8: Commit**

```bash
git add cmd/issue/start_e2e_test.go
git commit -m "test(issue): add five start-flow failure-mode E2E tests"
```

---

## Task 9: Documentation

**Files:**
- Modify: `cmd/issue/start.go` (verify docstrings)
- Modify: `cmd/issue/start_prompter.go` (verify docstrings)
- Modify: `issue/issue.go` (verify docstrings on `Prompter`, updated `GetFromUser`, `GetFromTracker`)
- Modify: `README.md` (add "Testing the start flow" subsection)
- Modify: `ROADMAP.md` (mark item 1 as done)

- [ ] **Step 1: Verify Go docstrings**

```bash
mise exec -- go doc -all github.com/piprim/git-zf/cmd/issue RunIssueStart
mise exec -- go doc -all github.com/piprim/git-zf/cmd/issue StartPrompter
mise exec -- go doc -all github.com/piprim/git-zf/issue Prompter
```

For each symbol below, confirm the doc comment exists and matches the actual behaviour. Add or fix as needed:

- `RunIssueStart` — flow summary (pick issue → resolve worktree toggle → resolve conflict → create branch/worktree → persist → update tracker), `(nil, nil)` short-circuit from `pickIssue` and `ResolveBranchConflict`, error semantics.
- `StartDeps` (or `startDeps`) — bundles long-lived deps; nil-tracker contract.
- `BuildStartDeps` (or `buildStartDeps`) — error semantics (git-init vs tracker-factory).
- `StartPrompter` — one-line summary; each of the 9 methods has its own doc comment (verify).
- `NewHuhStartPrompter` (or `newHuhStartPrompter`) — production prompter, opens real huh forms.
- `issue.Prompter` (in `issue/issue.go`) — sub-interface for issue-input forms.
- `issue.GetFromUser`, `issue.GetFromTracker` — now take a Prompter; verify the doc comment matches.

- [ ] **Step 2: Add a "Testing the start flow" subsection to `README.md`**

Locate the existing "Testing the close flow" subsection and insert this immediately after:

```markdown
#### Testing the start flow

The issue-start flow (used by both `issue start` and `branch new`) is
end-to-end tested in `cmd/issue/start_e2e_test.go`. The pattern mirrors the
close-flow tests: a real on-disk repo, the fake tracker at `tracker/fake/`,
and a `scriptedStartPrompter` that returns canned answers for every huh form.

To exercise just the start-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_" -v

When adding a new toggle, confirm, or picker form, extend `StartPrompter` and
add a matching E2E test alongside the existing happy-path and failure-mode
tests.
```

- [ ] **Step 3: Update `ROADMAP.md`**

Locate the "End-to-end testability of interactive flows" section. The first bullet (`cmd/issue/start.go`) is now done. Strike it through or move it to a "Done" subsection:

```markdown
### End-to-end testability of interactive flows

The close flow was the first command refactored behind a prompter interface (`ClosePrompter`) ...

#### Done

- ~~`cmd/issue/start.go`~~ — shipped 2026-05-26. See `cmd/issue/start_e2e_test.go` for the 8 E2E tests.

#### Remaining

1. (was 2) **`cmd/branch/branch.go` — prune flow** — ...
2. (was 3) **`cmd/issue/conflict.go`** — folded into the start refactor. The conflict loop now lives in `huhStartPrompter.ResolveBranchConflict`.
```

(Adjust the prose to match the existing tone — the above is illustrative.)

- [ ] **Step 4: Run final build, vet, and test suite**

```bash
mise exec -- go build ./...
mise exec -- go vet ./...
mise exec -- go test ./... -count=1
```

All three must pass.

- [ ] **Step 5: Smoke-test the production flows**

```bash
mise exec -- go build -o ./bin/git-zf .
./bin/git-zf issue start --help
./bin/git-zf branch new --help
```

Both should show the `--variant` flag. If a sandbox repo is available, exercise one happy path of each (e.g., manual issue input → branch creation) to confirm production behaviour is unchanged.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/start.go cmd/issue/start_prompter.go issue/issue.go README.md ROADMAP.md
git commit -m "docs: document RunIssueStart, StartPrompter, and start-flow E2E tests"
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

- [ ] **Cross-check the spec checklist**

Open `docs/superpowers/specs/2026-05-26-issue-start-refactor-design.md` § "Test coverage targets". Confirm each of the 8 tests in that list has a matching `Test*` function in `cmd/issue/start_e2e_test.go`:

1. `TestRunIssueStart_BranchHappyPath_NoTracker` ✓
2. `TestRunIssueStart_BranchHappyPath_WithTracker` ✓
3. `TestRunIssueStart_WorktreeHappyPath` ✓
4. `TestRunIssueStart_BranchUserAbortsAtConfirm` ✓
5. `TestRunIssueStart_VariantOnCollision` ✓
6. `TestRunIssueStart_AbortOnCollision` ✓
7. `TestRunIssueStart_TrackerListErrorFallsBackToManual` ✓
8. `TestRunIssueStart_NoTrackerStatusUpdate` ✓

---

## Notes for the executor

- **`mise exec` is non-negotiable.** Calling bare `go` may pick up a stale Go from `$PATH` and produce subtle test failures (memory: `feedback_mise_exec`).
- **The user runs git themselves.** When an executor encounters a "Commit" step, surface the exact commands to the user rather than running them autonomously (memory: `feedback_no_git_commit`). The user may also opt for a single end-of-run commit; respect their choice.
- **No `git add -A`.** Always pass explicit paths.
- **One commit per task by default.** Each commit message above is a single conventional-commit line; preserve the prefix (`refactor`, `test`, `docs`) so the project's existing log style is maintained.
- **Task 4 is the riskiest task.** It lifts `RunIssueStart` to a free function, threads `prompter` and `deps` through every helper, and migrates all stdout/stderr routing through `client.IO()`. After Step 9, **run the full test suite plus a manual smoke test** before committing. If anything looks structurally wrong, STOP and report BLOCKED.
- **Task 6 has a hidden dependency on Task 4's export decisions.** If Task 4 left `startDeps` / `buildStartDeps` / `newHuhStartPrompter` unexported, `cmd/branch/branch.go` cannot reach them. Resolve the export question (rename to `StartDeps` / `BuildStartDeps` / `NewHuhStartPrompter`) BEFORE the Task 4 commit, not afterward, to avoid a churn commit between Tasks 4 and 6.
- **The fake tracker is shared with the close-flow tests.** The blank-import file `cmd/issue/register_fake_tracker_test.go` already exists (shipped in the close-flow plan). Do not create a duplicate.
- **`scriptedStartPrompter.ConflictAbort` is a separate field from `ConflictBranch == nil`.** This is intentional — a nil ConflictBranch with ConflictAbort=false means "use the provided branch as-is" (the no-collision happy path); ConflictAbort=true means "the operator chose abort". Tests that just want the happy path leave both unset.
- **Every test uses `t.Run` per assertion block** (memory: `feedback_t_run`). The plan's test code already reflects this; do not collapse subtests when implementing.
