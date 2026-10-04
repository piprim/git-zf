# Branch-Prune Refactor + E2E Test Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Spec:** [`docs/superpowers/specs/2026-05-26-branch-prune-refactor-design.md`](../specs/2026-05-26-branch-prune-refactor-design.md) (to be written alongside this plan)

**Context (compiled from the user-confirmed design decisions):**

`cmd/branch/branch.go`'s prune flow has one inline `huh.NewForm(tui.BranchPruneConfirm(...))` call inside `runPrune` that gates a destructive store mutation (`s.DeleteBranch` + `s.UpdateBranchStatus`). Today the unit tests use a `fakePruner` to exercise the dry-run discovery logic, but the non-dry-run path (confirm → execute) has no automated coverage and only the manual smoke test verifies it. The same pattern that worked for `close.go` and `start.go` — extract a `PrunePrompter` interface, ship three implementations (production huh, scripted test, auto-confirm `--yes`) — closes the gap and adds a useful non-interactive mode in one shot.

**Goal:** Extract a `PrunePrompter` interface so `runPrune` is TUI-free, add a `--yes` flag for CI/cron use, and ship E2E tests that exercise the full discovery → confirm → execute pipeline against a real on-disk repo.

**Architecture:** Three-method-free interface (`PrunePrompter` has exactly one method: `ConfirmPrune(ctx, toDelete, toMerge int) (bool, error)`). Three implementations — `huhPrunePrompter` (production), `scriptedPrunePrompter` (tests), `autoConfirmPrunePrompter` (always returns `true, nil`, wired by `--yes`). `runPrune` gains a `prompter PrunePrompter` parameter; the four existing dry-run unit tests get rewritten on top of a new `pruneTestRig` (real temp git repo + seeded store) so the test surface matches the close/start-flow precedent. Production behaviour: identical except for the new `--yes` flag.

**Tech Stack:** Go 1.23+, `spf13/cobra`, `charmbracelet/huh`, `go-git/v6`, `modernc.org/sqlite`. Reuses no external infra beyond what close + start ship.

**Toolchain:** Go is managed by `mise`. **Always invoke Go via `mise exec -- go <command>`.**

**Lint conventions:**
- `wrapcheck` — wrap external errors with `fmt.Errorf("ctx: %w", err)`.
- `nlreturn` — blank line before non-sole `return`.
- `exec.CommandContext(ctx, ...)` — never `exec.Command(...)`.
- `t.Run` — every distinct assertion or scenario wrapped in a named subtest (memory: `feedback_t_run`).
- IO injection — writes go through `client.IO().Out` / `.Err` (memory: `feedback_respect_io_injection`).

---

## File Structure

### New files

| Path | Responsibility |
|---|---|
| `cmd/branch/prune_prompter.go` | `PrunePrompter` interface, `huhPrunePrompter` production impl, `autoConfirmPrunePrompter` for `--yes`. |
| `cmd/branch/prune_prompter_test.go` | `scriptedPrunePrompter` test fake + compile-time assertion. |
| `cmd/branch/prune_e2e_test.go` | `pruneTestRig` + E2E tests for the full prune pipeline (replaces the existing dry-run-only `TestRunBranchPrune` subtests). |

### Modified files

| Path | Change summary |
|---|---|
| `cmd/branch/branch.go` | Add `yes bool` to `pruneFlags`. Register `--yes`/`-y` on `pruneCmd`. `pruneRunE` picks the prompter (`autoConfirm` when `--yes`, else `huh`) and passes it to `runPrune`. `runPrune` signature gains `prompter PrunePrompter`; replace the inline `huh.NewForm(...)` call. `executePrune`'s success-line `fmt.Printf` migrates to `w` (the existing writer parameter) for IO discipline. |
| `cmd/branch/branch_test.go` | Delete the four existing `TestRunBranchPrune` subtests — they're rewritten in `prune_e2e_test.go` against the real-on-disk rig. Delete `fakePruner` and `insertTestBranch` helpers if no other test uses them (verify before removing). |
| `README.md` | Add a "Testing the prune flow" subsection alongside the existing close/start subsections. Document the `--yes` flag in the `branch prune` command line. |
| `ROADMAP.md` | Mark item 2 of "End-to-end testability of interactive flows" as done (strikethrough + shipped-date suffix, matching item 1's pattern). |

---

## Task 1: Define `PrunePrompter` interface + scripted + autoConfirm impls

**Files:**
- Create: `cmd/branch/prune_prompter.go`
- Create: `cmd/branch/prune_prompter_test.go`

- [ ] **Step 1: Create the interface file**

Write `/workspace/cmd/branch/prune_prompter.go`:

```go
package branch

import "context"

// PrunePrompter resolves the single user-facing decision in the prune flow:
// whether to proceed with the destructive store mutations after the summary
// has been printed. The production implementation drives a huh form; the
// auto-confirm implementation (wired via --yes) returns true unconditionally;
// the scripted implementation in prune_prompter_test.go returns canned values
// for tests.
type PrunePrompter interface {
	// ConfirmPrune is called only when there is at least one branch to delete
	// or to mark merged, AND the run is not a dry-run.
	ConfirmPrune(ctx context.Context, toDelete, toMerge int) (confirmed bool, err error)
}

// autoConfirmPrunePrompter unconditionally returns (true, nil). It is wired
// by the --yes / -y flag on `branch prune`.
type autoConfirmPrunePrompter struct{}

func newAutoConfirmPrunePrompter() *autoConfirmPrunePrompter {
	return &autoConfirmPrunePrompter{}
}

func (p *autoConfirmPrunePrompter) ConfirmPrune(_ context.Context, _, _ int) (bool, error) {
	return true, nil
}
```

- [ ] **Step 2: Create the test fake**

Write `/workspace/cmd/branch/prune_prompter_test.go`:

```go
package branch

import "context"

// Compile-time check: scriptedPrunePrompter must satisfy PrunePrompter.
var _ PrunePrompter = (*scriptedPrunePrompter)(nil)

// scriptedPrunePrompter is the canned-response prompter used by
// prune_e2e_test.go. Each field corresponds to one return value; *Err fields
// inject errors.
type scriptedPrunePrompter struct {
	Confirm    bool
	ConfirmErr error

	// Call-counter so tests can assert ConfirmPrune was (or was not) called.
	ConfirmCalls int

	// LastToDelete + LastToMerge capture the most recent call's arguments
	// so tests can assert the values handed to the prompter.
	LastToDelete int
	LastToMerge  int
}

func (s *scriptedPrunePrompter) ConfirmPrune(_ context.Context, toDelete, toMerge int) (bool, error) {
	s.ConfirmCalls++
	s.LastToDelete = toDelete
	s.LastToMerge = toMerge

	if s.ConfirmErr != nil {
		return false, s.ConfirmErr
	}

	return s.Confirm, nil
}
```

- [ ] **Step 3: Verify build is green**

```bash
mise exec -- go build ./...
mise exec -- go test -count=1 -run NoSuchTest ./cmd/branch/...
```

Both must succeed. The second command compiles the test binary, exercising the compile-time assertion.

- [ ] **Step 4: Commit**

```bash
git add cmd/branch/prune_prompter.go cmd/branch/prune_prompter_test.go
git commit -m "refactor(branch): introduce PrunePrompter interface, scripted + auto-confirm impls"
```

---

## Task 2: Implement `huhPrunePrompter` (production)

**Files:**
- Modify: `cmd/branch/prune_prompter.go`

- [ ] **Step 1: Extend the import block**

Edit `/workspace/cmd/branch/prune_prompter.go`. Replace the `import "context"` line with:

```go
import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
)
```

- [ ] **Step 2: Append the huhPrunePrompter implementation**

Append below the existing `autoConfirmPrunePrompter` methods:

```go
// Compile-time check.
var _ PrunePrompter = (*huhPrunePrompter)(nil)

// huhPrunePrompter is the production PrunePrompter — opens a real huh form
// asking the operator to confirm. Constructed once per `branch prune`
// invocation.
type huhPrunePrompter struct{}

func newHuhPrunePrompter() *huhPrunePrompter {
	return &huhPrunePrompter{}
}

func (p *huhPrunePrompter) ConfirmPrune(ctx context.Context, toDelete, toMerge int) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.BranchPruneConfirm(toDelete, toMerge, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm prune form: %w", err)
	}

	return confirmed, nil
}
```

- [ ] **Step 3: Verify**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Both must succeed. The new prompter is not yet wired into `runPrune` — Task 3 does that.

- [ ] **Step 4: Commit**

```bash
git add cmd/branch/prune_prompter.go
git commit -m "refactor(branch): add huhPrunePrompter production implementation"
```

---

## Task 3: Thread `prompter` through `runPrune` + add `--yes` flag

**Files:**
- Modify: `cmd/branch/branch.go`

- [ ] **Step 1: Extend `pruneFlags` with `yes`**

In `/workspace/cmd/branch/branch.go`, locate the `pruneFlags` struct (currently around line 206) and add the `yes` field:

```go
type pruneFlags struct {
	dryRun bool
	base   string
	yes    bool
}
```

- [ ] **Step 2: Register the `--yes` flag on `pruneCmd`**

In `pruneCmd` (around line 217), add the flag registration alongside the existing `--dry-run` and `--base` flags:

```go
cmd.Flags().BoolVarP(&flags.yes, "yes", "y", false, "skip the confirmation prompt (CI-friendly)")
```

- [ ] **Step 3: Pick the prompter in `pruneRunE`**

In `pruneRunE` (around line 246), construct the prompter before the call to `runPrune`. Replace the existing tail call:

```go
return runPrune(ctx, os.Stdout, s, c, flags)
```

with:

```go
var prompter PrunePrompter = newHuhPrunePrompter()
if flags.yes {
	prompter = newAutoConfirmPrunePrompter()
}

return runPrune(ctx, os.Stdout, s, c, prompter, flags)
```

- [ ] **Step 4: Update `runPrune` signature + replace the inline huh call**

Change the signature of `runPrune` to accept a `prompter PrunePrompter`:

```go
func runPrune(ctx context.Context, w io.Writer, s *store.Store, pruner pruner, prompter PrunePrompter, flags pruneFlags) error {
```

Inside the body, find the inline confirm block (currently around lines 330-334):

```go
var confirmed bool
err = huh.NewForm(tui.BranchPruneConfirm(len(result.toDelete), len(result.toMerge), &confirmed)).RunWithContext(ctx)
if err != nil {
	return fmt.Errorf("confirm: %w", err)
}
```

Replace with:

```go
confirmed, err := prompter.ConfirmPrune(ctx, len(result.toDelete), len(result.toMerge))
if err != nil {
	return fmt.Errorf("confirm prune: %w", err)
}
```

- [ ] **Step 5: Migrate `executePrune`'s success line through `w`**

In `executePrune` (around line 378), the existing call writes to `os.Stdout`:

```go
fmt.Printf("Pruned: %d deleted, %d marked merged.\n", len(result.toDelete), len(result.toMerge))
```

That's a memory-rule violation (`feedback_respect_io_injection`). To fix it cleanly, change `executePrune`'s signature to accept the same `w io.Writer` that `runPrune` uses, and route the success line through it:

```go
func executePrune(ctx context.Context, w io.Writer, s *store.Store, result pruneResult) error {
	// ... existing body unchanged ...

	fmt.Fprintf(w, "Pruned: %d deleted, %d marked merged.\n", len(result.toDelete), len(result.toMerge))

	return nil
}
```

Update the single caller in `runPrune` (around line 342):

```go
return executePrune(ctx, w, s, result)
```

- [ ] **Step 6: Drop the now-unused `huh` and `tui` imports if applicable**

After the edits, `runPrune` no longer references `huh.NewForm` or `tui.BranchPruneConfirm`. Run `mise exec -- go build ./...` — Go will fail if either import is unused. Check `cmd/branch/branch.go`'s other functions (`BranchActionSelect`, `BranchStatusFilter` are used elsewhere in the file) to confirm whether `huh` and `tui` are still needed. Drop only what's truly unused.

- [ ] **Step 7: Verify build + existing tests still pass**

```bash
mise exec -- go build ./...
mise exec -- go test ./... -count=1
mise exec -- go vet ./...
```

The existing `TestRunBranchPrune` subtests will FAIL because `runPrune`'s signature changed and they don't pass a prompter. That's expected — Task 5 rewrites them. For Task 3 to commit cleanly, you need to update those test call sites with a minimal stub. Easiest: inject `&scriptedPrunePrompter{}` (already exported via Task 1's test file) into each of the four subtests' `runPrune` calls. They're dry-run tests, so `ConfirmPrune` is never invoked — the stub is sufficient and tests stay green.

The four call sites to patch in `branch_test.go` (around lines 151, 178, 199, 222) — change from:

```go
if err := runPrune(t.Context(), &buf, s, pruner, pruneFlags{dryRun: true}); err != nil {
```

to:

```go
if err := runPrune(t.Context(), &buf, s, pruner, &scriptedPrunePrompter{}, pruneFlags{dryRun: true}); err != nil {
```

(Task 5 will delete these tests entirely; we just keep them green for the in-between commit.)

- [ ] **Step 8: Commit**

```bash
git add cmd/branch/branch.go cmd/branch/branch_test.go
git commit -m "refactor(branch): thread PrunePrompter through runPrune; add --yes flag"
```

---

## Task 4: `pruneTestRig` + E2E happy-path test

**Files:**
- Create: `cmd/branch/prune_e2e_test.go`

- [ ] **Step 1: Verify prerequisite identifiers**

Confirm these exist before writing the rig:

- `git.NewClientAt(io, dir)` — `git/git.go:51`
- `git.Client.LocalBranchNames`, `IsMergedInto`, `DefaultBaseBranch` — `git/git.go:301, 331, 390`
- `store.Open(ctx, dir)` — `store/store.go`
- `store.BranchStatusInProgress`, `store.BranchStatusMerged`, `store.BranchStatusAll` — `store/store.go`

If any signature differs from the plan's assumptions, STOP and report BLOCKED.

- [ ] **Step 2: Write the rig**

Create `/workspace/cmd/branch/prune_e2e_test.go`:

```go
package branch

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

// pruneTestRig bundles a real on-disk git repo + seeded store so prune E2E
// tests share setup. The repo starts with one commit on master; tests seed
// additional branches (deleted / merged / active) via the rig's helpers.
type pruneTestRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

func newPruneRig(t *testing.T) *pruneTestRig {
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

	runGit("init", "-q", "-b", "master")
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

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return &pruneTestRig{
		dir: dir, client: client, store: s,
		stdout: stdout, stderr: stderr,
	}
}

// seedIssueAndBranch inserts an Issue + Branch row into the store with the
// in-progress status. issueSlug is the human-readable ID (e.g. "ABC-1"),
// branchName the full git ref name.
func (r *pruneTestRig) seedIssueAndBranch(t *testing.T, issueSlug, branchName, branchType string) {
	t.Helper()

	if err := r.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: issueSlug, Title: issueSlug, StatusID: store.StatusIDInProgress},
		&store.Branch{Name: branchName, Type: branchType, StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed %q: %v", branchName, err)
	}
}

// createGitBranch creates a real local git branch pointing at master's HEAD.
// Use for branches that should be picked up by LocalBranchNames.
func (r *pruneTestRig) createGitBranch(t *testing.T, branchName string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "branch", branchName, "master")
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch %q: %v\n%s", branchName, err, out)
	}
}

// mergeBranchIntoMaster creates a branch with one extra commit, then
// fast-forward-merges it into master. After this call, the branch's tip is
// reachable from master — IsMergedInto returns true.
func (r *pruneTestRig) mergeBranchIntoMaster(t *testing.T, branchName, fileContent string) {
	t.Helper()

	runGit := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = r.dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit("checkout", "-q", "-b", branchName)

	fname := branchName + ".txt"
	if err := os.WriteFile(filepath.Join(r.dir, fname), []byte(fileContent), 0o644); err != nil {
		t.Fatalf("write %s: %v", fname, err)
	}

	runGit("add", fname)
	runGit("commit", "-m", "feat: "+branchName)
	runGit("checkout", "-q", "master")
	runGit("merge", "--ff", branchName)
}
```

- [ ] **Step 3: Append the happy-path test**

```go
func TestRunPrune_HappyPath_DeleteAndMerge(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)

	// Seed three branches:
	//   DEL-1 — store row only, no git branch → "to delete"
	//   MRG-1 — store row + git branch + merged into master → "to merge"
	//   ACT-1 — store row + git branch + NOT merged → no action
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")
	rig.seedIssueAndBranch(t, "MRG-1", "MRG-1@fix@done", "fix")
	rig.seedIssueAndBranch(t, "ACT-1", "ACT-1@feat@active", "feat")
	rig.mergeBranchIntoMaster(t, "MRG-1@fix@done", "merged\n")
	rig.createGitBranch(t, "ACT-1@feat@active")

	prompter := &scriptedPrunePrompter{Confirm: true}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 row removed from store", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		for _, r := range rows {
			if r.BranchName == "DEL-1@feat@gone" {
				t.Errorf("DEL-1@feat@gone still present after prune")
			}
		}
	})

	t.Run("MRG-1 status flipped to merged", func(t *testing.T) {
		merged, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		found := false
		for _, r := range merged {
			if r.BranchName == "MRG-1@fix@done" {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("MRG-1@fix@done not flagged as merged")
		}
	})

	t.Run("ACT-1 left in-progress", func(t *testing.T) {
		inProgress, err := rig.store.ListBranches(t.Context(), store.BranchStatusInProgress)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		found := false
		for _, r := range inProgress {
			if r.BranchName == "ACT-1@feat@active" {
				found = true
				break
			}
		}

		if !found {
			t.Errorf("ACT-1@feat@active should still be in-progress")
		}
	})

	t.Run("prompter was called once with the right counts", func(t *testing.T) {
		if prompter.ConfirmCalls != 1 {
			t.Errorf("ConfirmCalls = %d, want 1", prompter.ConfirmCalls)
		}

		if prompter.LastToDelete != 1 {
			t.Errorf("LastToDelete = %d, want 1", prompter.LastToDelete)
		}

		if prompter.LastToMerge != 1 {
			t.Errorf("LastToMerge = %d, want 1", prompter.LastToMerge)
		}
	})

	t.Run("stdout shows the summary", func(t *testing.T) {
		got := rig.stdout.String()
		if !bytes.Contains([]byte(got), []byte("Pruned: 1 deleted, 1 marked merged")) {
			t.Errorf("stdout = %q, want it to contain 'Pruned: 1 deleted, 1 marked merged'", got)
		}
	})
}
```

- [ ] **Step 4: Verify the test passes**

```bash
mise exec -- go test -run TestRunPrune_HappyPath -v ./cmd/branch/... -count=1
```

Expected: PASS with all 5 subtests.

If the test fails, READ the failure output. The most likely sources of failure:
- Git merge command shape (e.g., `--ff` vs `--ff-only` semantics).
- Branch existence: confirm `LocalBranchNames` returns the names you expect.
- Store status enum names — verify `BranchStatusInProgress`, `BranchStatusMerged`, `BranchStatusAll` match the actual `/workspace/store/store.go` constants.

NEVER weaken assertions to silence failures. If the production behaviour is wrong, STOP and report BLOCKED.

- [ ] **Step 5: Commit**

```bash
git add cmd/branch/prune_e2e_test.go
git commit -m "test(branch): add prune-flow E2E rig and happy-path test"
```

---

## Task 5: Migrate the existing four dry-run tests onto the rig + delete legacy helpers

**Files:**
- Modify: `cmd/branch/prune_e2e_test.go`
- Modify: `cmd/branch/branch_test.go`

- [ ] **Step 1: Append the four migrated dry-run tests to `prune_e2e_test.go`**

Each test below is the rig-based equivalent of one of the existing `TestRunBranchPrune` subtests in `branch_test.go`. Same observable behaviour, different setup mechanism.

```go
func TestRunPrune_DryRun_ReportsDeletedBranch(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "ABC-1", "ABC-1@feat@gone", "feat")

	prompter := &scriptedPrunePrompter{} // unused on dry-run

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output mentions the deleted branch", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("ABC-1@feat@gone")) {
			t.Errorf("stdout = %q, want it to mention 'ABC-1@feat@gone'", rig.stdout.String())
		}
	})

	t.Run("store is unchanged after dry-run", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("store rows = %d, want 1 (unchanged)", len(rows))
		}
	})

	t.Run("prompter was not invoked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0 on dry-run", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_DryRun_ReportsMergedBranch(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "XY-1", "XY-1@fix@bug", "fix")
	rig.mergeBranchIntoMaster(t, "XY-1@fix@bug", "bugfix\n")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output mentions the merged branch", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("XY-1@fix@bug")) {
			t.Errorf("stdout = %q, want it to mention 'XY-1@fix@bug'", rig.stdout.String())
		}
	})

	t.Run("prompter was not invoked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0 on dry-run", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_DryRun_NothingToPrune(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "Z-1", "Z-1@feat@active", "feat")
	rig.createGitBranch(t, "Z-1@feat@active")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("output says 'Nothing to prune.'", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Nothing to prune.")) {
			t.Errorf("stdout = %q, want it to contain 'Nothing to prune.'", rig.stdout.String())
		}
	})

	t.Run("prompter was not invoked", func(t *testing.T) {
		if prompter.ConfirmCalls != 0 {
			t.Errorf("ConfirmCalls = %d, want 0 when nothing to prune", prompter.ConfirmCalls)
		}
	})
}

func TestRunPrune_DryRun_MixedCategories(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")
	rig.seedIssueAndBranch(t, "MRG-1", "MRG-1@fix@done", "fix")
	rig.seedIssueAndBranch(t, "ACT-1", "ACT-1@feat@active", "feat")
	rig.mergeBranchIntoMaster(t, "MRG-1@fix@done", "merged\n")
	rig.createGitBranch(t, "ACT-1@feat@active")

	prompter := &scriptedPrunePrompter{}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{dryRun: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	out := rig.stdout.String()

	t.Run("output mentions the deleted branch", func(t *testing.T) {
		if !bytes.Contains([]byte(out), []byte("DEL-1@feat@gone")) {
			t.Errorf("stdout = %q, want it to mention 'DEL-1@feat@gone'", out)
		}
	})

	t.Run("output mentions the merged branch", func(t *testing.T) {
		if !bytes.Contains([]byte(out), []byte("MRG-1@fix@done")) {
			t.Errorf("stdout = %q, want it to mention 'MRG-1@fix@done'", out)
		}
	})

	t.Run("output does NOT mention the active branch", func(t *testing.T) {
		if bytes.Contains([]byte(out), []byte("ACT-1@feat@active")) {
			t.Errorf("active branch should not appear in dry-run output, got: %q", out)
		}
	})

	t.Run("store is unchanged", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 3 {
			t.Errorf("store rows = %d, want 3 (unchanged)", len(rows))
		}
	})
}
```

- [ ] **Step 2: Delete the four old `TestRunBranchPrune` subtests + the `fakePruner` and `insertTestBranch` helpers**

In `/workspace/cmd/branch/branch_test.go`:

1. Delete the entire `TestRunBranchPrune` function (and its four subtests).
2. Delete the `fakePruner` struct + its three methods.
3. Check whether `insertTestBranch` is used anywhere else: `grep -n "insertTestBranch" /workspace/cmd/branch/branch_test.go`. If it's only referenced inside the deleted `TestRunBranchPrune`, delete it too. Otherwise leave it.
4. Drop the patch from Task 3 Step 7 (the `&scriptedPrunePrompter{}` injections) — those lines are deleted with `TestRunBranchPrune`.

- [ ] **Step 3: Run the full test suite to confirm migration is faithful**

```bash
mise exec -- go test ./... -count=1
mise exec -- go test -run "^TestRunPrune_" -v ./cmd/branch/... -count=1
```

The first command shows the suite green (no regressions). The second shows all 5 new `TestRunPrune_*` parent tests passing with their subtests.

- [ ] **Step 4: Commit**

```bash
git add cmd/branch/prune_e2e_test.go cmd/branch/branch_test.go
git commit -m "test(branch): migrate dry-run prune tests onto the real-on-disk rig"
```

---

## Task 6: User-aborts-at-confirm + --yes-skips-confirm E2E tests

**Files:**
- Modify: `cmd/branch/prune_e2e_test.go`

- [ ] **Step 1: Append the abort test**

```go
func TestRunPrune_UserAbortsAtConfirm(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")

	// Operator declines the confirm — Confirm:false makes ConfirmPrune return (false, nil).
	prompter := &scriptedPrunePrompter{Confirm: false}

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 row still present in store", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 1 {
			t.Errorf("store rows = %d, want 1 (unchanged on abort)", len(rows))
		}
	})

	t.Run("stdout shows 'Aborted.'", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Aborted.")) {
			t.Errorf("stdout = %q, want 'Aborted.'", rig.stdout.String())
		}
	})

	t.Run("prompter was called exactly once", func(t *testing.T) {
		if prompter.ConfirmCalls != 1 {
			t.Errorf("ConfirmCalls = %d, want 1", prompter.ConfirmCalls)
		}
	})
}
```

- [ ] **Step 2: Append the --yes test**

```go
func TestRunPrune_YesFlagSkipsConfirm(t *testing.T) {
	t.Parallel()

	rig := newPruneRig(t)
	rig.seedIssueAndBranch(t, "DEL-1", "DEL-1@feat@gone", "feat")

	// Inject a scripted prompter that would fail if called — but the --yes
	// path uses autoConfirmPrunePrompter, NOT this one. The plumbing is
	// `pruneRunE -> picks autoConfirm when flags.yes==true`, but the unit
	// runPrune accepts whatever prompter the caller passes. To assert the
	// `--yes` wiring end-to-end, we mirror what pruneRunE does: pass the
	// autoConfirm prompter directly.
	prompter := newAutoConfirmPrunePrompter()

	if err := runPrune(t.Context(), rig.stdout, rig.store, rig.client, prompter, pruneFlags{yes: true}); err != nil {
		t.Fatalf("runPrune: %v", err)
	}

	t.Run("DEL-1 row removed (auto-confirm executed)", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusAll)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}

		for _, r := range rows {
			if r.BranchName == "DEL-1@feat@gone" {
				t.Errorf("DEL-1@feat@gone still present despite --yes")
			}
		}
	})

	t.Run("stdout shows the success line", func(t *testing.T) {
		if !bytes.Contains(rig.stdout.Bytes(), []byte("Pruned: 1 deleted")) {
			t.Errorf("stdout = %q, want 'Pruned: 1 deleted'", rig.stdout.String())
		}
	})
}
```

- [ ] **Step 3: Run all 7 prune tests**

```bash
mise exec -- go test -run "^TestRunPrune_" -v ./cmd/branch/... -count=1
```

Expected: all 7 parent tests PASS with their subtests. (HappyPath, DryRun_ReportsDeletedBranch, DryRun_ReportsMergedBranch, DryRun_NothingToPrune, DryRun_MixedCategories, UserAbortsAtConfirm, YesFlagSkipsConfirm.)

- [ ] **Step 4: Run the full suite**

```bash
mise exec -- go test ./... -count=1
mise exec -- go vet ./...
```

Both clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/branch/prune_e2e_test.go
git commit -m "test(branch): add prune abort + --yes E2E tests"
```

---

## Task 7: Documentation

**Files:**
- Modify: `cmd/branch/branch.go` (verify docstrings on `runPrune`, `executePrune`, `pruneFlags`, `pruneCmd`)
- Modify: `cmd/branch/prune_prompter.go` (verify the prompter docstrings)
- Modify: `README.md` (add "Testing the prune flow" subsection; mention `--yes`)
- Modify: `ROADMAP.md` (strike through item 2)

- [ ] **Step 1: Verify Go docstrings**

```bash
mise exec -- go doc -all github.com/piprim/git-zf/cmd/branch PrunePrompter
mise exec -- go doc -all github.com/piprim/git-zf/cmd/branch runPrune
mise exec -- go doc -all github.com/piprim/git-zf/cmd/branch executePrune
```

Each should print a doc comment. Fix any missing or stale comments:
- `PrunePrompter` — already added in Task 1; verify.
- `huhPrunePrompter`, `autoConfirmPrunePrompter`, their constructors — verify each has a one-line doc.
- `runPrune` — should describe the dry-run vs confirm-and-execute branching and the prompter parameter.
- `executePrune` — should describe the destructive nature and the new `w` parameter.
- `pruneFlags.yes` field — add a one-line comment explaining the flag.

- [ ] **Step 2: Add `--yes` to the `pruneCmd` Long description**

In `pruneCmd` (around line 217 of `cmd/branch/branch.go`), if there's a `Long:` description, append a sentence mentioning `--yes`. If there's no `Long:` field, add one:

```go
Long: `Compare each in-progress branch in the store against the local refs and
remove store rows whose branches are either gone or already merged into the
base branch. Use --dry-run to preview, --yes to skip the confirm prompt.`,
```

- [ ] **Step 3: Add a "Testing the prune flow" subsection to `README.md`**

Locate the existing "Testing the start flow" subsection. Insert this immediately after, at the same heading level (`####`):

```markdown
#### Testing the prune flow

The branch-prune flow is end-to-end tested in `cmd/branch/prune_e2e_test.go`.
Tests construct a real on-disk repo + seeded store, then drive the flow with
a `scriptedPrunePrompter` (canned confirm responses) or
`autoConfirmPrunePrompter` (mirrors `--yes`).

To exercise just the prune-flow tests:

    mise exec -- go test ./cmd/branch/... -run "^TestRunPrune_" -v

For non-interactive use (CI, cron), pass `--yes` to skip the confirmation
prompt:

    git zf branch prune --yes
```

- [ ] **Step 4: Update `ROADMAP.md`**

Locate the "End-to-end testability of interactive flows" section. Strike through item 2 with the same pattern item 1 uses (markdown strikethrough + `(shipped 2026-05-26 — see ...)` suffix pointing at `cmd/branch/prune_e2e_test.go`).

- [ ] **Step 5: Final verification**

```bash
mise exec -- go build ./...
mise exec -- go vet ./...
mise exec -- go test ./... -count=1
mise exec -- go test ./cmd/branch/... -run '^TestRunPrune_' -v -count=1
```

All four must pass. The last shows 7 parent tests + subtests all PASS.

- [ ] **Step 6: Smoke-test the `--yes` flag**

```bash
mise exec -- go build -o /tmp/git-zf-T7 .
/tmp/git-zf-T7 branch prune --help | grep -A 1 yes
```

Expected output mentions `-y, --yes` and "skip the confirmation prompt".

- [ ] **Step 7: Commit**

```bash
git add cmd/branch/branch.go cmd/branch/prune_prompter.go README.md ROADMAP.md
git commit -m "docs: prune-flow refactor and --yes flag"
```

---

## Final verification

- [ ] **Run the full suite cleanly**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
mise exec -- go vet ./...
```

Expected: clean build, all tests PASS, no vet warnings.

- [ ] **Cross-check the spec checklist**

Open the spec (`docs/superpowers/specs/2026-05-26-branch-prune-refactor-design.md`) and confirm each requirement maps to a task above:

- `PrunePrompter` interface — T1
- `huhPrunePrompter` + `scriptedPrunePrompter` + `autoConfirmPrunePrompter` — T1+T2
- `--yes` flag — T3
- `runPrune` signature change — T3
- IO discipline fix on `executePrune` — T3 Step 5
- E2E rig + happy/dry-run/abort/`--yes` tests — T4-T6
- Legacy `fakePruner` removal — T5
- Docs — T7

---

## Notes for the executor

- **`mise exec` is non-negotiable.** Calling bare `go` may pick up a stale Go from `$PATH` (memory: `feedback_mise_exec`).
- **The user runs git themselves.** When you encounter a "Commit" step, surface the exact commands rather than running them autonomously (memory: `feedback_no_git_commit`). The user may choose to defer commits to the end of the run.
- **No `git add -A`.** Pass explicit paths.
- **Task 5 deletes the existing `TestRunBranchPrune`.** Make sure Task 4's happy-path test is GREEN before deleting the old tests — that way you keep coverage continuous. If T4 has a failing assertion, FIX IT before moving on.
- **`branch.go` line numbers are approximate** — read the file before each edit.
- **Every test uses `t.Run` per assertion block** (memory: `feedback_t_run`). The plan's test code already conforms; don't collapse subtests when implementing.
- **The fake tracker isn't needed.** The prune flow makes zero tracker calls — `pruneTestRig` does not construct one.
