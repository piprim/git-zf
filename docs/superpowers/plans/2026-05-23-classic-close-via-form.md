# Classic Close via Commitizen Form — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route the Classic merge strategy through the commitizen TUI form (matching Squash/Rebase), with a pre-flight FF-sync of local base against `origin/<base>`, so the close commit message stops being `"Merge branch '<long-branch-name>'"` and becomes a proper commitizen-formatted entry.

**Architecture:** Replace the existing one-shot `git/Client.MergeNoFF` with a two-call pair (`MergeNoFFNoCommit` + `AbortMerge`). Add a new `doClassicClose` orchestrator in `cmd/issue/close.go` that reuses `rebasePreflight`, FF-syncs local base, stages the merge, opens the commitizen form pre-filled, commits, and uses `git merge --abort` for atomic rollback on any failure between merge and commit. No new strategy is added — Classic is modified in place.

**Tech Stack:**
- Go (managed by mise — always invoke `mise exec -- go …`)
- `github.com/go-git/go-git/v6` (high-level git ops)
- `os/exec` with `exec.CommandContext` for shelling out to system git (for hook + worktree compatibility)
- `github.com/charmbracelet/huh` (TUI forms — not mocked in tests, manual verification only for orchestration)
- stdlib `testing` (no testify)

**Spec:** `docs/superpowers/specs/2026-05-22-classic-close-via-form-design.md` — read it before starting.

**User-handled steps:** Per user preference, the implementing agent must **never** run `git add` / `git commit`. Each task's "commit" step is presented as a command the user will run themselves after reviewing the diff.

---

## File Structure

Files changed and their responsibilities:

- `git/merge.go` — git client primitives. **Delete** `MergeNoFF`. **Add** `MergeNoFFNoCommit` (checkout base + `git merge --no-ff --no-commit feature`, leaves `MERGE_HEAD`) and `AbortMerge` (`git merge --abort`).
- `git/merge_test.go` — primitive tests. **Delete** `TestMergeNoFF`. **Update** `TestDeleteLocalBranch` (currently uses `MergeNoFF` for setup) to merge via direct `git` CLI instead. **Add** `TestMergeNoFFNoCommit_clean`, `TestMergeNoFFNoCommit_conflict`, `TestMergeNoFFNoCommit_ffOnly`, `TestAbortMerge_active`, `TestAbortMerge_noActiveMerge`.
- `cmd/issue/close.go` — close orchestrator. **Replace** the `StrategyClassic` dispatch with a call to a new `doClassicClose` helper that reuses `rebasePreflight` and follows the same defer-after-merge pattern as `doRebaseClose`.
- `tui/issue.go` — strategy picker hint. **Update** the Classic option's `Hint` string to reflect the form-driven mechanic.
- `README.md` — user-facing docs. **Update** lines ~113, ~124, ~197 per spec, and **add** a new "Classic strategy — detailed flow" sub-section parallel to the existing Rebase one.

No new packages, no new files, no go.mod changes.

---

## Task 1: Add `MergeNoFFNoCommit` primitive

**Files:**
- Modify: `git/merge.go` (add new method)
- Test: `git/merge_test.go` (add three test functions)

- [ ] **Step 1: Write the failing test `TestMergeNoFFNoCommit_clean`**

Append to `git/merge_test.go`:

```go
func TestMergeNoFFNoCommit_clean(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write feat.go: %v", err)
	}
	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")

	// Base advances independently while feature is alive.
	run("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write base.go: %v", err)
	}
	run("add", "base.go")
	run("commit", "-m", "chore: base advances")

	// Pre-state: switch back to feature so the helper has to checkout base itself.
	run("checkout", "feature")

	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFFNoCommit: %v", err)
	}

	// HEAD must be on main (helper checked out base).
	var headBuf bytes.Buffer
	headCmd := exec.CommandContext(t.Context(), "git", "rev-parse", "--abbrev-ref", "HEAD")
	headCmd.Dir = dir
	headCmd.Stdout = &headBuf
	if err := headCmd.Run(); err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	if got := strings.TrimSpace(headBuf.String()); got != "main" {
		t.Errorf("HEAD after MergeNoFFNoCommit = %q, want %q", got, "main")
	}

	// MERGE_HEAD must exist (commit pending).
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Errorf("MERGE_HEAD missing after MergeNoFFNoCommit: %v", err)
	}

	// feat.go must be staged on main.
	var statusBuf bytes.Buffer
	statusCmd := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusCmd.Stdout = &statusBuf
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if !strings.Contains(statusBuf.String(), "feat.go") {
		t.Errorf("status missing feat.go: %q", statusBuf.String())
	}

	// No new commit on main yet.
	var logBuf bytes.Buffer
	logCmd := exec.CommandContext(t.Context(), "git", "log", "--oneline", "main")
	logCmd.Dir = dir
	logCmd.Stdout = &logBuf
	if err := logCmd.Run(); err != nil {
		t.Fatalf("log: %v", err)
	}
	if strings.Contains(logBuf.String(), "Merge") {
		t.Errorf("unexpected merge commit on main: %q", logBuf.String())
	}
}
```

- [ ] **Step 2: Run the test and verify it fails with "undefined MergeNoFFNoCommit"**

Run: `mise exec -- go test ./git/ -run TestMergeNoFFNoCommit_clean -v`

Expected: build failure — `client.MergeNoFFNoCommit undefined (type *git.Client has no field or method MergeNoFFNoCommit)`.

- [ ] **Step 3: Implement `MergeNoFFNoCommit`**

Edit `git/merge.go`. Add the method (anywhere after `MergeRebase`, before `MergeNoFF` for now — `MergeNoFF` is removed in Task 4):

```go
// MergeNoFFNoCommit checks out baseBranch and runs `git merge --no-ff
// --no-commit <featureBranch>`. Leaves MERGE_HEAD + MERGE_MSG in place
// so the caller can drive the commit step itself (typically via the
// commitizen TUI form). Caller is responsible for `git merge --abort`
// on TUI abort or commit failure.
func (c *Client) MergeNoFFNoCommit(ctx context.Context, featureBranch, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-ff", "--no-commit", featureBranch); err != nil {
		return fmt.Errorf("merge --no-ff --no-commit %s: %w", featureBranch, err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `mise exec -- go test ./git/ -run TestMergeNoFFNoCommit_clean -v`

Expected: `--- PASS: TestMergeNoFFNoCommit_clean`.

- [ ] **Step 5: Add `TestMergeNoFFNoCommit_conflict`**

Append to `git/merge_test.go`:

```go
func TestMergeNoFFNoCommit_conflict(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "conflict.go"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "conflict.go")
	run("commit", "-m", "feat: feature side")

	run("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "conflict.go"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "conflict.go")
	run("commit", "-m", "feat: base side")

	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err == nil {
		t.Fatal("MergeNoFFNoCommit: expected conflict error, got nil")
	}

	contents, readErr := os.ReadFile(filepath.Join(dir, "conflict.go"))
	if readErr != nil {
		t.Fatalf("read conflict.go: %v", readErr)
	}
	if !strings.Contains(string(contents), "<<<<<<<") {
		t.Errorf("conflict.go missing conflict markers: %q", string(contents))
	}
}
```

- [ ] **Step 6: Add `TestMergeNoFFNoCommit_ffOnly`**

Append to `git/merge_test.go`:

```go
func TestMergeNoFFNoCommit_ffOnly(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "ahead.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "ahead.go")
	run("commit", "-m", "feat: only on feature")

	// main is now an ancestor of feature — no divergence.
	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFFNoCommit: %v", err)
	}

	// --no-ff must force MERGE_HEAD even in a fast-forwardable situation.
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Errorf("MERGE_HEAD missing in FF-able scenario: %v", err)
	}
}
```

- [ ] **Step 7: Run all three new tests and verify they pass**

Run: `mise exec -- go test ./git/ -run TestMergeNoFFNoCommit -v`

Expected: three `--- PASS` lines.

- [ ] **Step 8: Commit (user runs)**

User runs:

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add MergeNoFFNoCommit primitive"
```

---

## Task 2: Add `AbortMerge` primitive

**Files:**
- Modify: `git/merge.go` (add new method)
- Test: `git/merge_test.go` (add two test functions)

- [ ] **Step 1: Write the failing test `TestAbortMerge_active`**

Append to `git/merge_test.go`:

```go
func TestAbortMerge_active(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("checkout", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "feat.go")
	run("commit", "-m", "feat: add feat.go")

	run("checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	run("add", "base.go")
	run("commit", "-m", "chore: base advances")

	if err := client.MergeNoFFNoCommit(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeNoFFNoCommit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("MERGE_HEAD missing before abort: %v", err)
	}

	if err := client.AbortMerge(t.Context()); err != nil {
		t.Fatalf("AbortMerge: %v", err)
	}

	// MERGE_HEAD must be gone.
	if _, err := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); !os.IsNotExist(err) {
		t.Errorf("MERGE_HEAD still present after AbortMerge: %v", err)
	}

	// Working tree must be clean.
	var statusBuf bytes.Buffer
	statusCmd := exec.CommandContext(t.Context(), "git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusCmd.Stdout = &statusBuf
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.TrimSpace(statusBuf.String()) != "" {
		t.Errorf("dirty status after AbortMerge: %q", statusBuf.String())
	}
}
```

- [ ] **Step 2: Run the test and verify it fails**

Run: `mise exec -- go test ./git/ -run TestAbortMerge_active -v`

Expected: build failure — `client.AbortMerge undefined`.

- [ ] **Step 3: Implement `AbortMerge`**

Edit `git/merge.go`. Add the method after `MergeNoFFNoCommit`:

```go
// AbortMerge runs `git merge --abort`. Used after a TUI abort or
// commit failure in the Classic close flow to clear MERGE_HEAD /
// MERGE_MSG and restore the working tree. Returns the git error
// as-is so callers can decide whether to treat a no-active-merge
// failure as fatal.
func (c *Client) AbortMerge(ctx context.Context) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--abort"); err != nil {
		return fmt.Errorf("merge --abort: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `mise exec -- go test ./git/ -run TestAbortMerge_active -v`

Expected: `--- PASS: TestAbortMerge_active`.

- [ ] **Step 5: Add `TestAbortMerge_noActiveMerge`**

Append to `git/merge_test.go`:

```go
func TestAbortMerge_noActiveMerge(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)

	if err := client.AbortMerge(t.Context()); err == nil {
		t.Error("AbortMerge with no active merge: expected error, got nil")
	}
}
```

- [ ] **Step 6: Run both AbortMerge tests and verify they pass**

Run: `mise exec -- go test ./git/ -run TestAbortMerge -v`

Expected: two `--- PASS` lines.

- [ ] **Step 7: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): add AbortMerge primitive"
```

---

## Task 3: Implement `doClassicClose` orchestrator and wire dispatch

**Files:**
- Modify: `cmd/issue/close.go` (add new function, change dispatch)
- Test: none (orchestrator is verified via build + manual end-to-end; same scope boundary as `doRebaseClose` per the spec)

- [ ] **Step 1: Add the `doClassicClose` orchestrator**

Edit `cmd/issue/close.go`. Add the function immediately after `doRebaseClose`'s closing brace (so Classic and Rebase orchestrators sit side-by-side):

```go
// doClassicClose drives the Classic strategy: shared rebasePreflight,
// FF-sync of local base against origin/<base> (or direct checkout when no
// remote), real --no-ff --no-commit merge on base, commitizen form,
// commit. Rollback on any failure between MergeNoFFNoCommit and a
// successful Commit runs `git merge --abort` to clear MERGE_HEAD /
// MERGE_MSG and restore the working tree. The defer uses a named return
// + closure so it observes the actual err at function exit.
func doClassicClose(ctx context.Context, mc mergeContext) (err error) {
	plan, err := rebasePreflight(ctx, mc)
	if err != nil {
		return err
	}

	// Step 2: sync local base with origin/<base> (no-op when no remote).
	if plan.remoteName != "" {
		if err := mc.client.FastForwardOnly(ctx, plan.remoteBase, mc.baseBranch); err != nil {
			return fmt.Errorf("local %s diverged from %s — `git pull --ff-only` first: %w",
				mc.baseBranch, plan.remoteBase, err)
		}
	} else {
		if err := mc.client.Checkout(ctx, mc.baseBranch); err != nil {
			return fmt.Errorf("checkout %s: %w", mc.baseBranch, err)
		}
	}

	// Step 3: resolve integration target SHA for the prefill subject.
	baseSHA, err := mc.client.ResolveRef("refs/heads/" + mc.baseBranch)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", mc.baseBranch, err)
	}

	// Step 4: stage the merge without committing.
	if err := mc.client.MergeNoFFNoCommit(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return fmt.Errorf("merge --no-ff --no-commit: %w", err)
	}

	defer func() {
		if err == nil {
			return
		}

		if abErr := mc.client.AbortMerge(ctx); abErr != nil {
			err = fmt.Errorf("merge --abort after %w failed: %v", err, abErr)
		}
	}()

	// Steps 5 + 6: TUI form (pre-filled) → commit.
	hint := commitpkg.IssueHint{
		IssueID:    mc.pickedBranch.IssueSlug,
		BranchType: mc.pickedBranch.Type,
	}
	prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
	prefill["subject"] = fmt.Sprintf("Merge %s into %s.",
		plan.featureOrigSHA.String()[:shortSHALen], baseSHA.String()[:shortSHALen])

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

	if err := mc.client.Commit(ctx, msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}
```

- [ ] **Step 2: Update the `StrategyClassic` branch in `doMerge`**

In `cmd/issue/close.go`, find the existing dispatch (it currently reads):

```go
case StrategyClassic:
	if err := mc.client.MergeNoFF(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
		return strategy, false, fmt.Errorf("merge no-ff: %w", err)
	}
```

Replace with:

```go
case StrategyClassic:
	if err := doClassicClose(ctx, mc); err != nil {
		return strategy, false, err
	}
```

- [ ] **Step 3: Build to verify it compiles**

Run: `mise exec -- go build ./...`

Expected: no output (success). `MergeNoFF` is still defined at this point — Task 4 deletes it.

- [ ] **Step 4: Run the full test suite to confirm nothing regressed**

Run: `mise exec -- go test ./...`

Expected: all tests pass.

- [ ] **Step 5: Commit (user runs)**

```bash
git add cmd/issue/close.go
git commit -m "feat(issue): route Classic close through commitizen form"
```

---

## Task 4: Delete legacy `MergeNoFF` and its test

**Files:**
- Modify: `git/merge.go` (delete method)
- Modify: `git/merge_test.go` (delete `TestMergeNoFF`, update `TestDeleteLocalBranch` setup)

- [ ] **Step 1: Replace `MergeNoFF` usage in `TestDeleteLocalBranch`**

In `git/merge_test.go`, find the sub-test `"safe delete removes a merged branch"`. It currently calls `client.MergeNoFF(t.Context(), "feature", "main")` after checking out main. Replace that line with a direct git CLI merge so the test no longer depends on the soon-to-be-deleted helper:

Find:

```go
// Classic merge so safe -d works.
if err := client.MergeNoFF(t.Context(), "feature", "main"); err != nil {
	t.Fatalf("MergeNoFF: %v", err)
}
```

Replace with:

```go
// Classic --no-ff merge so safe -d works.
run("merge", "--no-ff", "--no-edit", "feature")
```

(`run` is the local helper already defined at the top of this sub-test that wraps `exec.CommandContext` for `git ...`.)

- [ ] **Step 2: Delete `TestMergeNoFF`**

In `git/merge_test.go`, delete the entire `TestMergeNoFF` function (lines starting at `func TestMergeNoFF(t *testing.T) {` through its closing `}`).

- [ ] **Step 3: Delete `MergeNoFF` method**

In `git/merge.go`, delete the entire `MergeNoFF` method (its godoc block and the function body).

- [ ] **Step 4: Build to verify nothing else references `MergeNoFF`**

Run: `mise exec -- go build ./...`

Expected: success. If a build error mentions `MergeNoFF`, grep the codebase (`grep -rn MergeNoFF .`) and replace remaining usages.

- [ ] **Step 5: Run the full test suite**

Run: `mise exec -- go test ./...`

Expected: all tests pass, including the updated `TestDeleteLocalBranch`.

- [ ] **Step 6: Commit (user runs)**

```bash
git add git/merge.go git/merge_test.go
git commit -m "refactor(git): drop unused MergeNoFF in favor of MergeNoFFNoCommit"
```

---

## Task 5: Update the Classic strategy picker hint

**Files:**
- Modify: `tui/issue.go` (one string literal in `doMerge`'s strategy options — but the strategy options are defined in `cmd/issue/close.go`, not `tui/issue.go`)

Note: per the existing architecture (rebase-close spec section "tui/issue.go — strategy picker becomes ternary, agnostic"), the strategy option list is owned by `cmd/issue/close.go`, not `tui/issue.go`. The hint string lives in that option list.

- [ ] **Step 1: Update the Classic hint string**

In `cmd/issue/close.go`, find the strategy options inside `doMerge`:

```go
strategyForm := tui.IssueMergeStrategy(&picked, []tui.StrategyOption{
	{
		Value: string(StrategyRebase),
		Label: "Rebase",
		Hint:  "Single clean commit on local base, submodule-safe (recommended)",
	},
	{Value: string(StrategySquash), Label: "Squash", Hint: "git merge --squash — fast, but not submodule-safe"},
	{Value: string(StrategyClassic), Label: "Classic", Hint: "git merge --no-ff — preserves full history"},
})
```

Update the `Classic` line's `Hint` to:

```go
{Value: string(StrategyClassic), Label: "Classic", Hint: "git merge --no-ff with commitizen message — preserves full history"},
```

- [ ] **Step 2: Build and run the TUI package tests**

Run: `mise exec -- go test ./tui/... ./cmd/issue/...`

Expected: all tests pass. The strategy picker test does not assert on the hint string (it asserts on the option list shape), so no test update needed.

- [ ] **Step 3: Commit (user runs)**

```bash
git add cmd/issue/close.go
git commit -m "docs(issue): update Classic strategy hint for commitizen flow"
```

---

## Task 6: Update README documentation

**Files:**
- Modify: `README.md` (three line-level edits + one new sub-section)

- [ ] **Step 1: Update the close-flow narrative (around line 113)**

In `README.md`, find:

```
2. Choose merge strategy: **Rebase** (default, recommended — single clean commit, submodule-safe), **Squash** (`git merge --squash`, fast but not submodule-safe), or **Classic** (`--no-ff`, preserves full history). For Rebase and Squash, the final commit is composed through the commitizen TUI form, pre-filled from the branch's issue ID and type.
```

Replace with:

```
2. Choose merge strategy: **Rebase** (default, recommended — single clean commit, submodule-safe), **Squash** (`git merge --squash`, fast but not submodule-safe), or **Classic** (`--no-ff`, preserves full history). For all three strategies the final commit is composed through the commitizen TUI form, pre-filled from the branch's issue ID and type.
```

- [ ] **Step 2: Update the strategy comparison table (around line 124)**

In `README.md`, find the row:

```
| **Classic** | `git merge --no-ff` | merge commit + full feature history | ✅ yes |
```

Replace with:

```
| **Classic** | `git merge --no-ff --no-commit` + commitizen form (FF-syncs local base against `origin/<base>` first) | merge commit + full feature history | ✅ yes |
```

- [ ] **Step 3: Update the "When to choose each strategy" Classic bullet (around line 197)**

In `README.md`, find:

```
- **Classic** — you want to preserve the feature's full commit history on the base branch via a merge commit. Use when intermediate commits have value (large features, bisect surface, audit trail).
```

Replace with:

```
- **Classic** — you want to preserve the feature's full commit history on the base branch via a merge commit. The merge commit's message is composed through the commitizen TUI form (same UX as Rebase/Squash). Local base is FF-synced against `origin/<base>` before the merge; Classic refuses to merge into a base that has diverged from origin (operator runs `git pull --ff-only` and retries). Use when intermediate commits have value (large features, bisect surface, audit trail).
```

- [ ] **Step 4: Add the "Classic strategy — detailed flow" sub-section**

In `README.md`, locate the existing `#### Rebase strategy — detailed flow` sub-section (around line 126). Find its closing point — it ends just before the line `##### When to choose each strategy`. Immediately before that `##### When to choose each strategy` line, insert:

```markdown
#### Classic strategy — detailed flow

1. **Pre-flight** (shared with Rebase):
   - Refuses if the working tree has tracked modifications or staged changes.
   - Checks out the feature branch, captures its tip SHA.
   - Detects the configured remote and runs `git fetch <remote>` (no-op when no remote is configured).
   - Computes `remoteBase` as `<remote>/<base>` (or `<base>` when local-only).
   - Aborts if the feature branch has no commits ahead of `remoteBase` (already integrated).
   - Runs a `merge-tree` dry-run of feature vs `remoteBase`; aborts with the conflict file list if any conflict is predicted.

2. **Sync local base** (skipped when no remote):
   - Checks out the local base branch.
   - Runs `git merge --ff-only <remoteBase>` to bring local base up to `origin/<base>`.
   - Refuses with a remediation message if local base has diverged from `origin/<base>` — the operator runs `git pull --ff-only` and retries.

3. **Resolve integration target**: looks up `refs/heads/<base>` for the prefill subject SHA.

4. **Stage the merge**: runs `git merge --no-ff --no-commit <feature>`. `MERGE_HEAD` / `MERGE_MSG` are left in place; nothing is committed yet.

5. **TUI form** (pre-filled):
   - subject: `Merge <feature-tip-short> into <base-tip-short>.`
   - type / scope / authors default from the branch metadata, identical to Rebase / Squash.

6. **Commit**: `git commit -F <msgfile>` plus options from the form. Because `MERGE_HEAD` is present, Git produces a two-parent merge commit on base with the form-supplied message.

7. **Bookkeeping**: updates the local store and (optionally) the tracker, then offers the delete-branch prompt — unchanged from the other strategies. Classic uses safe delete (`-d`) since feature is now an ancestor of base.

If any step between 4 and a successful commit fails — TUI abort, pre-commit hook rejection, signing failure — `git merge --abort` is run automatically to clear `MERGE_HEAD` / `MERGE_MSG` and restore the working tree. The operator is left on base in a clean state.
```

- [ ] **Step 5: Sanity-check the README renders**

Run: `mise exec -- go build ./...` (no-op for README but confirms the repo still builds).

Skim the rendered Markdown for the four edits: open `README.md` in an editor or markdown viewer and confirm the narrative reads correctly. There's no project-side markdown linter to run.

- [ ] **Step 6: Commit (user runs)**

```bash
git add README.md
git commit -m "docs(readme): document Classic close via commitizen form"
```

---

## Task 7: Manual end-to-end verification

No code changes — this task runs the spec's manual end-to-end scenarios against the built binary.

- [ ] **Step 1: Build the binary**

```bash
mise exec -- go build -o ./bin/git-zf .
```

Expected: no output (success).

- [ ] **Step 2: Run the full test suite one more time**

```bash
mise exec -- go test ./...
```

Expected: all tests pass.

- [ ] **Step 3: Execute each scenario from the spec's "Manual end-to-end" section**

The scenarios are listed in `docs/superpowers/specs/2026-05-22-classic-close-via-form-design.md` under "## Testing → Manual end-to-end". For convenience, the ten scenarios are:

1. Clean Classic close (remote in sync) → two-parent merge commit on local base, form-supplied message, feature branch unchanged.
2. Clean Classic close (local base behind origin, FF-able) → step 2 FFs local base, then continues like scenario 1.
3. Local base diverged from origin/<base> → step 2 refuses with `git pull --ff-only` message, no `MERGE_HEAD`, base unchanged.
4. No-remote repo → step 2 sync is skipped, direct checkout + merge.
5. TUI abort (Esc in form) → `git merge --abort` runs, base clean.
6. Pre-commit hook rejection → `git merge --abort` runs, hook stderr visible.
7. Already-integrated abort (feature has zero commits ahead of origin/<base>) → pre-flight aborts before any mutation.
8. Dirty working tree → pre-flight aborts.
9. Conflict on dry-run → conflict file list printed, pre-flight aborts.
10. `--no-ff` with no divergence → merge commit still produced (not a fast-forward) because of `--no-ff`.

For each scenario, set up the repo state (instructions inline in the spec), run `./bin/git-zf issue close`, choose `Classic`, and verify the observed behavior matches the spec.

- [ ] **Step 4: Report results**

Note any scenario whose behavior deviates from the spec. If everything matches, the implementation is complete.

No commit for this task — verification only.

---

## Self-review

**Spec coverage check** (every requirement in `docs/superpowers/specs/2026-05-22-classic-close-via-form-design.md` should map to a task):

- "Pre-flight shared with Rebase via rebasePreflight" → Task 3 step 1 calls `rebasePreflight`.
- "Sync local base with origin/<base>" → Task 3 step 1 (inside `doClassicClose` body, branch on `plan.remoteName`).
- "Resolve integration target SHA" → Task 3 step 1 (`ResolveRef("refs/heads/" + mc.baseBranch)`).
- "Stage the merge without committing" → Task 1 (new primitive) + Task 3 step 1 (call site).
- "TUI form pre-filled with `Merge <feat> into <base>.`" → Task 3 step 1 (`prefill["subject"]`).
- "Commit via `git commit -F`" → Task 3 step 1 (`mc.client.Commit(...)` call).
- "Bookkeeping unchanged" → no task needed (existing `closeRunE` flow runs after `doMerge` returns).
- "`MergeNoFFNoCommit` primitive" → Task 1.
- "`AbortMerge` primitive" → Task 2.
- "Replace existing `MergeNoFF`" → Task 4.
- "Update strategy picker hint" → Task 5.
- "README updates" → Task 6.
- "Manual end-to-end scenarios" → Task 7.
- "Error handling: abort runs `git merge --abort` on TUI abort / hook rejection / commit failure" → Task 3 step 1 (defer closure).
- "Error handling: AbortMerge failure compounds with the original error" → Task 3 step 1 (defer closure uses `fmt.Errorf("merge --abort after %w failed: %v", err, abErr)`).
- "Defer uses named return + closure pattern" → Task 3 step 1 (`func doClassicClose(...) (err error)` with closure).
- "godoc on `MergeNoFFNoCommit` / `AbortMerge` / `doClassicClose`" → Tasks 1, 2, 3 (each includes godoc in the implementation step).

No spec requirement is missing a task. The "documentation updates" section's `CLAUDE.md` no-op claim is satisfied by the spec itself (no edit needed); no task added since no change is expected.

**Placeholder scan:** no "TBD", no "implement later", no "appropriate error handling" hand-waving — every code block is the actual code to paste.

**Type / name consistency:**
- `MergeNoFFNoCommit(ctx, featureBranch, baseBranch)` — same signature in Task 1 implementation, Task 3 call site, Task 1 test setups.
- `AbortMerge(ctx)` — same signature in Task 2 implementation, Task 3 defer, Task 2 tests.
- `doClassicClose(ctx, mc mergeContext) (err error)` — used in Task 3 implementation and dispatch.
- `plan.remoteName`, `plan.remoteBase`, `plan.featureOrigSHA` — match the existing `rebasePlan` struct.
- `mc.client`, `mc.pickedBranch.BranchName`, `mc.baseBranch`, `mc.cfg`, `mc.store` — match the existing `mergeContext` struct.

All consistent.

---

## Execution choice

Plan complete and saved to `docs/superpowers/plans/2026-05-23-classic-close-via-form.md`. Two execution options:

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
