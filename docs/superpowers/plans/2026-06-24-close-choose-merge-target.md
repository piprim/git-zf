# Choosable Merge Target for `git issue close` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the operator choose which branch `git issue close` merges/rebases into, defaulting to today's auto-computed target (parent integration branch, else configured/default base), with a `--base` flag for non-interactive use.

**Architecture:** `runClose` already computes a "smart default" base (config/default base, redirected to the parent integration branch). We extract that into `resolveDefaultBase`, then pass it through a new `chooseMergeTarget` step: `--base` (a new `closeDeps.baseOverride` field) wins outright after validation; otherwise, when more than one candidate local branch exists, an interactive picker is shown with the default pre-selected. The picker reuses the existing `tui.BaseBranchPicker` via a new `ClosePrompter.PickBaseBranch` method.

**Tech Stack:** Go, Cobra (CLI), huh (TUI forms), go-git, SQLite store. Tests are E2E against a real on-disk repo with a scripted prompter.

## Global Constraints

- Go is managed by **mise**: run Go tooling as `mise exec -- go ...`.
- Build: `mise exec -- go build -o ./bin/git-zf .`
- Tests: `mise exec -- go test ./cmd/issue/...`
- Lint: `mise exec -- golangci-lint run ./...` (or `make lint`).
- **Every distinct assertion/scenario is wrapped in its own `t.Run("descriptive label", ...)`** (repo + user convention).
- The branch being closed is never offered as a merge target (a branch cannot merge into itself).
- The smart default must remain selectable even when it is a remote-only branch (a parent integration branch the operator never checked out).
- Do not auto-commit unless explicitly instructed by the operator running this plan; each task lists its commit step for when commits are wanted.

---

### Task 1: Extend `ClosePrompter` with `PickBaseBranch`

Add the prompter method that drives the base-branch picker. No call site yet — this task only makes both implementations satisfy the interface so later tasks can call it. Mirrors the existing `StartPrompter.PickBaseBranch` signature exactly (`cmd/issueflow/start_prompter.go:163`).

**Files:**
- Modify: `cmd/issue/close_prompter.go` (interface + `huhPrompter`)
- Modify: `cmd/issue/close_prompter_test.go` (`scriptedPrompter`)

**Interfaces:**
- Produces: `ClosePrompter.PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error)`
- Produces (test): `scriptedPrompter` fields `Base string`, `BaseErr error`, `BaseCalled bool`; `PickBaseBranch` returns `Base` when set, else echoes `defaultBase`, and records `BaseCalled = true`.

- [ ] **Step 1: Add the method to the `ClosePrompter` interface**

In `cmd/issue/close_prompter.go`, inside the `ClosePrompter` interface (after `ConfirmDeleteBranch`), add:

```go
	// PickBaseBranch lets the operator choose the merge target. It is called
	// only when no --base override was given AND more than one candidate branch
	// exists. defaultBase is pre-selected; branches is the candidate list.
	PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error)
```

- [ ] **Step 2: Implement `huhPrompter.PickBaseBranch`**

In `cmd/issue/close_prompter.go`, add this method (reuses the existing `tui.BaseBranchPicker` from `tui/branch.go:182`):

```go
func (p *huhPrompter) PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (string, error) {
	var picked string
	if err := huh.NewForm(tui.BaseBranchPicker(defaultBase, branches, &picked)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("base branch picker: %w", err)
	}

	return picked, nil
}
```

- [ ] **Step 3: Extend `scriptedPrompter`**

In `cmd/issue/close_prompter_test.go`, add three fields to the `scriptedPrompter` struct (next to the other canned-response fields):

```go
	Base       string
	BaseErr    error
	BaseCalled bool
```

Then add the method (after `ConfirmDeleteBranch`):

```go
func (s *scriptedPrompter) PickBaseBranch(_ context.Context, defaultBase string, _ []string) (string, error) {
	s.BaseCalled = true
	if s.BaseErr != nil {
		return "", s.BaseErr
	}
	if s.Base == "" {
		return defaultBase, nil
	}

	return s.Base, nil
}
```

- [ ] **Step 4: Verify it builds and existing tests still pass**

Run: `mise exec -- go build ./... && mise exec -- go test ./cmd/issue/...`
Expected: PASS. The compile-time checks `var _ ClosePrompter = (*huhPrompter)(nil)` and `var _ ClosePrompter = (*scriptedPrompter)(nil)` now also cover `PickBaseBranch`. No behavior changed yet (nothing calls the method).

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/close_prompter.go cmd/issue/close_prompter_test.go
git commit -m "feat(issue): add PickBaseBranch to ClosePrompter"
```

---

### Task 2: Select the merge target in `runClose`

Extract the smart-default computation, then apply the `--base` override or the picker. This is the behavioral core.

**Files:**
- Modify: `cmd/issue/close.go` (`closeDeps` struct + `runClose` + new helpers)
- Modify: `cmd/issue/close_e2e_test.go` (rig helper + new tests)

**Interfaces:**
- Consumes: `ClosePrompter.PickBaseBranch` (Task 1); `git.Client.LocalBranchNames() ([]string, error)`, `BranchExists(string) (bool, error)`, `Remote() (string, error)`, `ResolveRef(string) (plumbing.Hash, error)`.
- Produces: `closeDeps.baseOverride string`; `resolveDefaultBase(ctx, deps, picked) (string, error)`; `chooseMergeTarget(ctx, deps, picked, defaultBase, prompter) (string, error)`; `mergeTargetCandidates(locals []string, closing, defaultBase string) []string`; `baseBranchResolves(c *git.Client, name string) (bool, error)`.
- Produces (test): `(*closeTestRig).addBranch(t, name)`.

- [ ] **Step 1: Add the `addBranch` test helper**

In `cmd/issue/close_e2e_test.go`, add after `pickedBranchRow` (around line 141):

```go
// addBranch creates a local branch off main (no extra commits) so tests can
// exercise the multi-candidate merge-target picker.
func (r *closeTestRig) addBranch(t *testing.T, name string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "branch", name, "main")
	cmd.Dir = r.dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git branch %s: %v\n%s", name, err, out)
	}
}
```

- [ ] **Step 2: Write the failing tests**

Append these four tests to `cmd/issue/close_e2e_test.go`:

```go
func TestClose_BaseOverrideMergesIntoChosenBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	rig.addBranch(t, "release-1.0")

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyClassic,
		Confirm:       true,
		Message:       []byte("Merge ABC-1 into release-1.0\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	deps := rig.deps()
	deps.baseOverride = "release-1.0"

	if err := runClose(t.Context(), deps, prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("release-1.0 HEAD carries the merge commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "release-1.0", "Merge ABC-1 into release-1.0")
	})

	t.Run("picker is bypassed by the flag", func(t *testing.T) {
		if prompter.BaseCalled {
			t.Error("PickBaseBranch was called; --base must bypass the picker")
		}
	})

	t.Run("main is untouched", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
}

func TestClose_BaseOverrideRejectsUnknownBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:  rig.pickedBranchRow(),
		Confirm: true,
	}

	deps := rig.deps()
	deps.baseOverride = "no-such-branch"

	err := runClose(t.Context(), deps, prompter)

	t.Run("returns an error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error for unknown --base branch, got nil")
		}
	})

	t.Run("error names the bad branch", func(t *testing.T) {
		if err != nil && !strings.Contains(err.Error(), "no-such-branch") {
			t.Errorf("error %q does not mention the bad branch", err)
		}
	})

	t.Run("main is untouched", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "chore: init")
	})
}

func TestClose_PickerOffersMergeTargetWhenMultipleCandidates(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	rig.addBranch(t, "release-1.0")

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyClassic,
		Confirm:       true,
		Base:          "release-1.0",
		Message:       []byte("Merge ABC-1 into release-1.0\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("picker was shown", func(t *testing.T) {
		if !prompter.BaseCalled {
			t.Error("PickBaseBranch was not called despite >1 candidate")
		}
	})

	t.Run("merge landed on the picked branch", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "release-1.0", "Merge ABC-1 into release-1.0")
	})
}

func TestClose_PickerSkippedWhenSingleCandidate(t *testing.T) {
	t.Parallel()

	// Default rig has main + the feature branch; the feature branch is excluded
	// as a target, leaving only main => single candidate => no picker.
	rig := newCloseRig(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      StrategyClassic,
		Confirm:       true,
		Base:          "should-not-be-used",
		Message:       []byte("Merge ABC-1 into main\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  false,
	}

	if err := runClose(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runClose: %v", err)
	}

	t.Run("picker was not shown", func(t *testing.T) {
		if prompter.BaseCalled {
			t.Error("PickBaseBranch was called with only one candidate")
		}
	})

	t.Run("merge landed on the smart default (main)", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "Merge ABC-1 into main")
	})
}
```

- [ ] **Step 3: Run the new tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_(BaseOverride|Picker)" -v`
Expected: FAIL — `deps.baseOverride` is an unknown field (compile error). This confirms the tests exercise the not-yet-built field/logic.

- [ ] **Step 4: Add the `baseOverride` field to `closeDeps`**

In `cmd/issue/close.go`, update the `closeDeps` struct (around line 26):

```go
// closeDeps bundles the long-lived dependencies the close flow needs.
// Production code builds it via buildCloseDeps; tests inject directly.
type closeDeps struct {
	client  *git.Client
	store   *store.Store
	cfg     *config.AppConfig
	tracker tracker.Tracker // nil ⇒ no tracker update will be attempted

	// baseOverride is the --base flag value (empty ⇒ smart default + picker).
	// Set per-invocation by closeRunE; left empty by the E2E tests that drive
	// the default/picker paths.
	baseOverride string
}
```

- [ ] **Step 5: Replace the inline base computation in `runClose` with the two helper calls**

In `cmd/issue/close.go`, in `runClose`, delete the existing base-resolution block (the code from `base := deps.cfg.Branch.Base` through the end of the parent-redirect block, i.e. lines 142-185 — everything from `base := deps.cfg.Branch.Base` up to and including the closing `}` of `if parentSlug != "" {`). Replace it with:

```go
	base, err := resolveDefaultBase(ctx, deps, picked)
	if err != nil {
		return err
	}

	base, err = chooseMergeTarget(ctx, deps, picked, base, prompter)
	if err != nil {
		return err
	}
```

Leave the `reconcileChildrenFromRefs(...)` call and the `ChildrenAllMerged` guard that follow exactly as they are. (The `FetchBranchRefs` side-effect that `reconcileChildrenFromRefs` relies on now happens inside `resolveDefaultBase`, which runs first — ordering is preserved.)

- [ ] **Step 6: Add the four helper functions**

In `cmd/issue/close.go`, add these functions immediately after `runClose`:

```go
// resolveDefaultBase computes the smart-default merge target: the configured
// base (or DefaultBaseBranch) redirected to the parent integration branch when
// the picked issue has a parent. This is the value pre-selected in the picker.
//
// The store is checked first for the parent relation; on a cross-machine clone
// where the store has no record, the refs/zf/branches/<slug> git ref is the
// fallback. FetchBranchRefs runs here (best-effort) for the top-level case so a
// later reconcileChildrenFromRefs sees fresh refs.
func resolveDefaultBase(ctx context.Context, deps closeDeps, picked *store.BranchRow) (string, error) {
	base := deps.cfg.Branch.Base
	if base == "" {
		detected, err := deps.client.DefaultBaseBranch()
		if err != nil {
			return "", fmt.Errorf("detect base branch: %w", err)
		}
		base = detected
	}

	parentSlug, err := deps.store.GetParentIssue(ctx, picked.IssueSlug)
	if err != nil {
		return "", fmt.Errorf("check parent issue: %w", err)
	}
	if parentSlug == "" {
		// One fetch retrieves all refs/zf/branches/* atomically.
		_ = deps.client.FetchBranchRefs(ctx)
		if br, _ := deps.client.ReadBranchRef(ctx, picked.IssueSlug); br != nil {
			parentSlug = br.ParentSlug
		}
	}
	if parentSlug == "" {
		return base, nil
	}

	// Try store first for the parent branch name.
	parentBranches, listErr := deps.store.ListBranches(ctx, store.BranchStatusAll)
	if listErr != nil {
		return "", fmt.Errorf("list branches for parent %q: %w", parentSlug, listErr)
	}
	for _, b := range parentBranches {
		if b.IssueSlug == parentSlug {
			return b.BranchName, nil
		}
	}
	// Store miss — read the parent's branch ref for the branch name.
	if parentBR, _ := deps.client.ReadBranchRef(ctx, parentSlug); parentBR != nil {
		return parentBR.BranchName, nil
	}

	return base, nil
}

// chooseMergeTarget refines the smart-default base into the final merge target.
// When deps.baseOverride is set (from --base) it is validated and used directly.
// Otherwise, when more than one candidate branch exists, the picker is shown
// with defaultBase pre-selected; with a single candidate the default is used
// unchanged. The branch being closed is never a candidate.
func chooseMergeTarget(
	ctx context.Context,
	deps closeDeps,
	picked *store.BranchRow,
	defaultBase string,
	prompter ClosePrompter) (string, error) {
	if deps.baseOverride != "" {
		ok, err := baseBranchResolves(deps.client, deps.baseOverride)
		if err != nil {
			return "", fmt.Errorf("validate --base %q: %w", deps.baseOverride, err)
		}
		if !ok {
			return "", fmt.Errorf("--base %q does not resolve to a local or remote branch", deps.baseOverride)
		}

		return deps.baseOverride, nil
	}

	locals, err := deps.client.LocalBranchNames()
	if err != nil {
		return "", fmt.Errorf("list local branches: %w", err)
	}

	candidates := mergeTargetCandidates(locals, picked.BranchName, defaultBase)
	if len(candidates) <= 1 {
		return defaultBase, nil
	}

	chosen, err := prompter.PickBaseBranch(ctx, defaultBase, candidates)
	if err != nil {
		return "", err //nolint:wrapcheck // prompter error already wrapped
	}

	return chosen, nil
}

// mergeTargetCandidates returns the branches offerable as merge targets: every
// local branch except the one being closed, with defaultBase guaranteed present
// (it may be a remote-only parent integration branch absent from locals).
// defaultBase is placed first so it leads the picker list.
func mergeTargetCandidates(locals []string, closing, defaultBase string) []string {
	out := make([]string, 0, len(locals)+1)
	seen := make(map[string]bool)
	add := func(name string) {
		if name == "" || name == closing || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	add(defaultBase)
	for _, name := range locals {
		add(name)
	}

	return out
}

// baseBranchResolves reports whether name is a usable merge target: a local
// branch, or <remote>/<name> when a remote is configured.
func baseBranchResolves(c *git.Client, name string) (bool, error) {
	exists, err := c.BranchExists(name)
	if err != nil {
		return false, fmt.Errorf("branch exists %q: %w", name, err)
	}
	if exists {
		return true, nil
	}

	remote, err := c.Remote()
	if err != nil {
		return false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote != "" {
		if _, err := c.ResolveRef("refs/remotes/" + remote + "/" + name); err == nil {
			return true, nil
		}
	}

	return false, nil
}
```

- [ ] **Step 7: Run the new tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_(BaseOverride|Picker)" -v`
Expected: PASS (all `t.Run` subtests green).

- [ ] **Step 8: Run the full close suite + lint to confirm no regressions**

Run: `mise exec -- go test ./cmd/issue/... && mise exec -- golangci-lint run ./cmd/issue/...`
Expected: PASS, no lint findings. (Existing close tests still pass because the default rig has a single candidate after excluding the feature branch, so the picker is never invoked and `base` stays `main`.)

- [ ] **Step 9: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "feat(issue): choose merge target on close (smart default + picker + --base)"
```

---

### Task 3: Wire the `--base` flag onto the command

Expose the override on the cobra command and thread it into `closeDeps`.

**Files:**
- Modify: `cmd/issue/close.go` (`getCloseCmd`, `closeRunE`)
- Modify: `cmd/issue/close_e2e_test.go` (flag-registration test)

**Interfaces:**
- Consumes: `closeDeps.baseOverride` (Task 2); `Issue.New`/`getCloseCmd` (`cmd/issue/issue.go`).

- [ ] **Step 1: Write the failing flag-registration test**

Append to `cmd/issue/close_e2e_test.go`:

```go
func TestCloseCmd_RegistersBaseFlag(t *testing.T) {
	t.Parallel()

	cmd := New(&config.AppConfig{}).getCloseCmd()

	t.Run("--base flag is registered", func(t *testing.T) {
		if cmd.Flags().Lookup("base") == nil {
			t.Fatal("expected --base flag on `issue close`")
		}
	})
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestCloseCmd_RegistersBaseFlag$" -v`
Expected: FAIL — the `base` flag is not registered yet (`Lookup` returns nil).

- [ ] **Step 3: Register the flag in `getCloseCmd`**

In `cmd/issue/close.go`, change `getCloseCmd` to register the flag before returning:

```go
func (i Issue) getCloseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "close",
		Short: "Close an issue (merge branch, update store and tracker)",
		Long: `Pick an in-progress branch, merge it into the base branch (rebase, squash, or classic),
update the local store, update the remote tracker, then optionally delete the local branch.`,
		RunE: i.closeRunE,
	}

	cmd.Flags().String("base", "",
		"merge target branch (default: parent integration branch or base, with an interactive picker)")

	return cmd
}
```

- [ ] **Step 4: Read the flag in `closeRunE` and set `deps.baseOverride`**

In `cmd/issue/close.go`, update `closeRunE`:

```go
func (i Issue) closeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	baseOverride, err := cmd.Flags().GetString("base")
	if err != nil {
		return fmt.Errorf("read --base flag: %w", err)
	}

	deps, err := buildCloseDeps(ctx, cmd, i.appConfig)
	if err != nil {
		return err
	}
	defer func() { _ = deps.store.Close() }()

	deps.baseOverride = baseOverride

	return runClose(ctx, deps, newHuhPrompter(deps.client, deps.store, i.appConfig))
}
```

- [ ] **Step 5: Run the flag test + full build to verify**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestCloseCmd_RegistersBaseFlag$" -v && mise exec -- go build -o ./bin/git-zf .`
Expected: PASS and a successful build.

- [ ] **Step 6: Final full suite + lint**

Run: `mise exec -- go test ./... && mise exec -- golangci-lint run ./...`
Expected: PASS, no lint findings.

- [ ] **Step 7: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "feat(issue): add --base flag to issue close"
```

---

## Self-Review

**Spec coverage:**
- Smart default (config/default base + parent redirect) → `resolveDefaultBase` (Task 2, Step 6). ✓
- Picker shown only when >1 candidate, default pre-selected → `chooseMergeTarget` + `mergeTargetCandidates` (Task 2); tests `TestClose_PickerOffers...` / `TestClose_PickerSkipped...`. ✓
- Candidate set = local branches, default force-included even if remote-only → `mergeTargetCandidates` (`add(defaultBase)` before locals; dedup). ✓
- Branch being closed excluded → `mergeTargetCandidates` `name == closing` guard. ✓
- `--base` flag, validated, skips picker → `closeDeps.baseOverride` + `baseBranchResolves` + flag wiring (Tasks 2 & 3); tests `TestClose_BaseOverride...`. ✓
- Confirmation surfaces the target → unchanged `ConfirmMerge` (already prints `branch → base`); no work needed. ✓
- Parent/child guard + merged-bookkeeping unchanged → `reconcileChildrenFromRefs` and `ChildrenAllMerged` left in place (Task 2, Step 5). ✓
- E2E tests with one `t.Run` per assertion → all new tests follow the pattern. ✓

**Placeholder scan:** No TBD/TODO; every code step shows complete code; every command shows expected output. ✓

**Type consistency:** `PickBaseBranch(ctx, defaultBase string, branches []string) (string, error)` is identical across the interface (Task 1 Step 1), `huhPrompter` (Task 1 Step 2), and `scriptedPrompter` (Task 1 Step 3). `closeDeps.baseOverride string` defined in Task 2 Step 4, consumed in `chooseMergeTarget` (Step 6) and set in `closeRunE` (Task 3 Step 4). `mergeTargetCandidates(locals []string, closing, defaultBase string) []string` and `baseBranchResolves(c *git.Client, name string) (bool, error)` match their call sites in `chooseMergeTarget`. ✓
