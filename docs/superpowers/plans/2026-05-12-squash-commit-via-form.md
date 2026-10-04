# Squash Commit via Pre-Filled `tui.commit` Form — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Route the squash close commit through the existing `tui.commit` form (pre-filled with type, scope, subject, author) so squash-close commits follow the same commitizen convention as `git zf commit`.

**Architecture:** Split `MergeSquash` into staging-only (keeps `git/` focused on git mechanics); strip the issue-aware `hint` parameter out of `FillOutForm` (caller composes its own `initialPrefill map[string]any`); add a small `IssueHint.Prefill(items)` helper so both `cmd/commit` and `cmd/issue/close` build prefill maps the same way; rewire `cmd/issue/close.go` to call the new staging `MergeSquash`, build the prefill (including a "Squashed merge of <bsha> into <basesha>." subject), run the form, then commit via `client.Commit`.

**Tech Stack:** Go 1.x, `mise exec -- go …` for the toolchain (per project memory). `huh` for forms. `go-git/v6` for refs. Tests follow this repo's existing patterns (`testify` is **not** used here; plain `t.Fatalf`/`t.Errorf`). The reviewers in this repo enforce `nlreturn` (blank line before `return` when it's not the sole statement) and `wrapcheck` (`fmt.Errorf("ctx: %w", err)` for errors crossing package boundaries). Spec lives at `docs/superpowers/specs/2026-05-12-squash-commit-via-form-design.md`.

**Conventions for every task in this plan:**
- Use `mise exec -- go …` for all `go test`, `go build`, `go vet` calls.
- This repo's user does git commits manually. Each task ends with a "Checkpoint" — verify tests green, then stop. **Do not run `git add`/`git commit`.**
- When the implementer is Claude Code: before writing any new Go code in a task, invoke the `cc-skills-golang:golang-code-style` and `cc-skills-golang:golang-naming` skills once per session.

---

## File map

| File | Disposition |
|---|---|
| `commit/form.go` | Modify: add `IssueHint.Prefill` method; change `FillOutForm`/`loadForm` signatures; delete `applyIssueHint`. |
| `commit/form_test.go` | Modify: add `TestIssueHint_Prefill`, add `TestFillOutForm_initialPrefill`, update existing `FillOutForm` callers to new signature, delete `TestApplyIssueHint`. |
| `git/merge.go` | Modify: `MergeSquash` drops `author` param and final commit step; drops SHA resolution (moves to caller); drops `plumbing` import if otherwise unused. |
| `git/merge_test.go` | Modify: `TestMergeSquash` drops `author` arg, no longer asserts commit message, follows up with manual commit; `TestDeleteLocalBranch_forceDelete` same follow-up pattern. |
| `tui/issue.go` | Modify: `IssueMergeConfirm` drops `author` parameter and its display line. |
| `cmd/commit/commit.go` | Modify: build prefill via `hint.Prefill(items)`; pass to new `FillOutForm` signature. |
| `cmd/issue/close.go` | Modify: delete `pickSquashAuthor` + its call site; in the squash branch resolve branch/base SHAs, call new `MergeSquash`, build `IssueHint{...}.Prefill(items)` + overlay subject + author defaults, call `FillOutForm` + `client.Commit`. |

---

## Task 1: Add `IssueHint.Prefill` method (additive — `applyIssueHint` stays for now)

**Files:**
- Modify: `commit/form.go` (add method, do not delete existing function yet)
- Test: `commit/form_test.go`

- [ ] **Step 1: Write the failing test**

Append to `commit/form_test.go`:

```go
func TestIssueHint_Prefill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		items []config.CommitItem
		hint  IssueHint
		want  map[string]any
	}{
		{
			name: "scope_present_uses_scope",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "scope"}, {Name: "footer"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"scope": testIssueID,
				"type":  "fix",
			},
		},
		{
			name: "footer_used_when_scope_missing",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "footer"},
			},
			hint: IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"footer": "Refs: " + testIssueID,
				"type":   "fix",
			},
		},
		{
			name:  "subject_used_when_only_subject_present",
			items: []config.CommitItem{{Name: "subject"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"subject": "(" + testIssueID + ")",
				"type":    "fix",
			},
		},
		{
			name:  "no_matching_field_only_type",
			items: []config.CommitItem{{Name: "body"}},
			hint:  IssueHint{IssueID: testIssueID, BranchType: "fix"},
			want: map[string]any{
				"type": "fix",
			},
		},
		{
			name:  "empty_issue_id_only_type",
			items: []config.CommitItem{{Name: "scope"}},
			hint:  IssueHint{BranchType: "fix"},
			want: map[string]any{
				"type": "fix",
			},
		},
		{
			name:  "empty_branchtype_only_issue",
			items: []config.CommitItem{{Name: "scope"}},
			hint:  IssueHint{IssueID: testIssueID},
			want: map[string]any{
				"scope": testIssueID,
			},
		},
		{
			name:  "fully_empty_hint_empty_map",
			items: []config.CommitItem{{Name: "scope"}},
			hint:  IssueHint{},
			want:  map[string]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.hint.Prefill(tt.items)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Prefill() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

Make sure `"reflect"` is in the test file imports.

- [ ] **Step 2: Run the test to verify it fails**

```bash
mise exec -- go test ./commit/... -run TestIssueHint_Prefill
```

Expected: compile error or `t.hint.Prefill undefined`.

- [ ] **Step 3: Implement `Prefill` method**

In `commit/form.go`, immediately after the existing `IssueHint` struct definition (~line 265), add:

```go
// Prefill returns the issue-hint contribution to a FillOutForm prefill map.
// IssueID populates one field using the fallback chain
//   "scope" → "footer" (as "Refs: <id>") → "subject" (as "(<id>)").
// BranchType is emitted as "type" when non-empty; loadForm validates it
// against cfg.CommitTypes and silently ignores an unconfigured value.
func (h IssueHint) Prefill(items []config.CommitItem) map[string]any {
	out := map[string]any{}

	if h.IssueID != "" {
		switch {
		case hasItem(items, "scope"):
			out["scope"] = h.IssueID
		case hasItem(items, "footer"):
			out["footer"] = "Refs: " + h.IssueID
		case hasItem(items, "subject"):
			out["subject"] = "(" + h.IssueID + ")"
		}
	}

	if h.BranchType != "" {
		out["type"] = h.BranchType
	}

	return out
}

// hasItem reports whether items contains an entry with the given Name.
func hasItem(items []config.CommitItem, name string) bool {
	for i := range items {
		if items[i].Name == name {
			return true
		}
	}

	return false
}
```

- [ ] **Step 4: Run the test to verify it passes**

```bash
mise exec -- go test ./commit/... -run TestIssueHint_Prefill -v
```

Expected: PASS for all subtests.

- [ ] **Step 5: Verify the package still builds and the rest of `./commit/...` is green**

```bash
mise exec -- go build ./... && mise exec -- go test ./commit/... && mise exec -- go vet ./...
```

Expected: no failures (pre-existing failures in unrelated packages may exist; only `./commit/...` matters for this task).

- [ ] **Step 6: Checkpoint**

Stop and let the user commit. Do NOT run `git add` or `git commit`.

---

## Task 2: Replace `hint` parameter with `initialPrefill` in `FillOutForm`/`loadForm`

**Files:**
- Modify: `commit/form.go` (`FillOutForm`, `loadForm`)
- Modify: `commit/form_test.go` (update 4 existing `FillOutForm` callsites)
- Modify: `cmd/commit/commit.go` (call site)

- [ ] **Step 1: Update the existing `FillOutForm` test callers to the new signature (so failures point at signatures, not behaviour)**

In `commit/form_test.go`, find every call of the form:

```go
FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{...}, IssueHint{}, hs)
```

and rewrite to:

```go
FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{...}, hs, nil)
```

Four callsites today (in `TestFillOutForm_savesHistoryAfterCompletion`, `TestFillOutForm_mainFormAbortPropagates`, `TestFillOutForm_emptyHistoryPreservesAnswers`, `TestFillOutForm_pickerAbortExitsFlow`).

- [ ] **Step 2: Run tests to see the expected compile failure**

```bash
mise exec -- go test ./commit/... 2>&1 | head -30
```

Expected: build error in `commit/form.go` because `FillOutForm` still has old signature — that's fine, drives us forward.

- [ ] **Step 3: Update `FillOutForm` signature and body in `commit/form.go`**

Replace the current declaration around line 165:

```go
func FillOutForm(
	ctx context.Context,
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	hs historyStore,
	initialPrefill map[string]any,
) ([]byte, tui.CommitOption, error) {
	prefill := initialPrefill

	for {
		form, extractMsg, extractOpts := loadForm(cfg, defaults, prefill)

		runner, err := runFormFn(form)
		if err != nil {
			return nil, tui.CommitOption{}, fmt.Errorf("failed to run the form: %w", err)
		}

		if runner.WantHistory() {
			prefill, err = runHistoryPicker(ctx, cfg.CommitMessage.Template, hs)
			if errors.Is(err, errNoHistory) {
				prefill = extractMsg()

				continue
			}
			if err != nil {
				return nil, tui.CommitOption{}, err
			}

			continue
		}

		answers := extractMsg()
		opts := extractOpts()

		var buf bytes.Buffer
		if err := assembleMessage(&buf, cfg.CommitMessage.Template, answers); err != nil {
			return nil, tui.CommitOption{}, fmt.Errorf("assemble message: %w", err)
		}

		if saveErr := hs.InsertCommandHistory(ctx, historyCmd, answers); saveErr != nil {
			slog.Warn("could not save commit history", "error", saveErr)
		}

		return buf.Bytes(), opts, nil
	}
}
```

- [ ] **Step 4: Update `loadForm` signature and body in `commit/form.go`**

Replace the current `loadForm` (~line 315):

```go
func loadForm(
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	prefill map[string]any,
) (form *huh.Form, extractMsg func() map[string]any, extractOpts func() tui.CommitOption) {
	slog.Debug("message template", "template", cfg.CommitMessage.Template)

	items := slices.Clone(cfg.CommitMessage.Items)

	var selectedType string
	if prefill != nil {
		items = applyPayload(items, prefill)
		if t, ok := prefill["type"].(string); ok && isValidCommitType(cfg.CommitTypes, t) {
			selectedType = t
		}
	}

	extractMsg = func() map[string]any {
		m := make(map[string]any, len(items)+1)
		m["type"] = selectedType

		for i := range items {
			m[items[i].Name] = items[i].Value
		}

		return m
	}

	groups := []*huh.Group{tui.CommitMessageGroup(cfg.CommitTypes, items, &selectedType)}

	opts := defaults
	if !defaults.AnyOptionSet() {
		groups = append(groups, tui.CommitOptionsGroup(&opts))
	}

	extractOpts = func() tui.CommitOption { return opts }

	return huh.NewForm(groups...), extractMsg, extractOpts
}
```

Confirm `"slices"` is already imported in `commit/form.go` (it is — used by `applyIssueHint` today; keep the import).

- [ ] **Step 5: Update `cmd/commit/commit.go` caller**

Locate the call:

```go
msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, hint, s)
```

Replace with:

```go
prefill := hint.Prefill(c.appConfig.CommitMessage.Items)

msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, s, prefill)
```

- [ ] **Step 6: Run all tests in commit/ and verify build**

```bash
mise exec -- go build ./... && mise exec -- go test ./commit/... -count=1
```

Expected: PASS. Existing `TestFillOutForm_*` tests still pass with `nil` prefill. `TestApplyIssueHint` may still pass since `applyIssueHint` function is still defined (but no longer called by `loadForm`). We'll delete it in Task 4.

- [ ] **Step 7: Verify `cmd/commit` still wires up correctly by running its package tests**

```bash
mise exec -- go test ./cmd/commit/... -count=1
```

Expected: PASS.

- [ ] **Step 8: Checkpoint**

Stop and let the user commit.

---

## Task 3: Add `TestFillOutForm_initialPrefill` to lock in the new behaviour

**Files:**
- Test: `commit/form_test.go`

- [ ] **Step 1: Write the failing test**

Append to `commit/form_test.go`:

```go
func TestFillOutForm_initialPrefillReachesForm(t *testing.T) {
	swapRunFormFn(t, func(_ *huh.Form) (formRunner, error) {
		return &stubFormRunner{}, nil
	})

	hs := &fakeHistoryStore{}
	prefill := map[string]any{
		"subject": "Squashed merge of abc1234 into def5678.",
		"type":    "fix",
	}

	msg, _, err := FillOutForm(context.Background(), minimalCfg(), tui.CommitOption{All: true}, hs, prefill)
	if err != nil {
		t.Fatalf("FillOutForm: %v", err)
	}

	got := string(msg)
	if !strings.Contains(got, "Squashed merge of abc1234 into def5678.") {
		t.Errorf("rendered message %q does not contain prefilled subject", got)
	}
	if !strings.HasPrefix(got, "fix") {
		t.Errorf("rendered message %q does not start with prefilled type 'fix'", got)
	}

	if len(hs.inserts) != 1 {
		t.Fatalf("inserts: got %d, want 1", len(hs.inserts))
	}
	if hs.inserts[0]["subject"] != "Squashed merge of abc1234 into def5678." {
		t.Errorf("saved subject = %q", hs.inserts[0]["subject"])
	}
	if hs.inserts[0]["type"] != "fix" {
		t.Errorf("saved type = %q", hs.inserts[0]["type"])
	}
}
```

Make sure `"strings"` is in the test file imports (it is — used widely).

- [ ] **Step 2: Run it**

```bash
mise exec -- go test ./commit/... -run TestFillOutForm_initialPrefillReachesForm -v
```

Expected: PASS (Task 2 already implemented the behaviour).

If `minimalCfg()` returns a config whose items don't include a `subject` entry seeded with `"my change"`, the assertions above need an adjustment: read `minimalCfg()` in `commit/form_test.go` and confirm the `subject` item is what gets overwritten by `applyPayload`. If `minimalCfg`'s default `subject` is `"my change"`, our prefill must overwrite it for this test to pass — that's the contract under test.

- [ ] **Step 3: Re-run the full commit/ test suite**

```bash
mise exec -- go test ./commit/... -count=1
```

Expected: PASS.

- [ ] **Step 4: Checkpoint**

Stop and let the user commit.

---

## Task 4: Remove now-dead `applyIssueHint` and its test

**Files:**
- Modify: `commit/form.go` (delete `applyIssueHint`)
- Modify: `commit/form_test.go` (delete `TestApplyIssueHint` and its helpers if unused elsewhere)

- [ ] **Step 1: Confirm `applyIssueHint` has no callers**

```bash
grep -n "applyIssueHint" /workspace/commit/form.go /workspace/commit/form_test.go
```

Expected: matches in `form.go` only inside the `applyIssueHint` function itself (definition), and in `form_test.go` inside `TestApplyIssueHint` and its helpers. No production caller in `loadForm` anymore (verify visually).

- [ ] **Step 2: Delete `applyIssueHint` function from `commit/form.go`**

Remove lines ~270–292 (the doc comment plus the `func applyIssueHint(...) []config.CommitItem { ... }` block).

`setItemValue` STAYS — it's still used by `applyPayload` (line ~57 of `form.go`).

- [ ] **Step 3: Delete `TestApplyIssueHint` and its helpers from `commit/form_test.go`**

Remove:
- `func TestApplyIssueHint(t *testing.T) { ... }` (~lines 232–297)
- `func assertNoInputMutation(...)` (~lines 299–310) — only used by `TestApplyIssueHint`; double-check no other test uses it via `grep -n assertNoInputMutation /workspace/commit/form_test.go` before deleting.
- `func assertNoFieldSet(...)` (~lines 312–320) — same check.
- `func assertFieldValue(...)` (~lines 322–337) — same check.

If `grep` shows other callers of any helper, leave it in place.

- [ ] **Step 4: Verify build, vet, tests**

```bash
mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./commit/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Checkpoint**

Stop and let the user commit.

---

## Task 5: Refactor `MergeSquash` to stage-only (drop `author` param and the commit step)

**Files:**
- Modify: `git/merge.go`
- Modify: `git/merge_test.go`

- [ ] **Step 1: Update `TestMergeSquash` to the new signature first**

Open `git/merge_test.go`. Locate `TestMergeSquash` (~line 197). Replace its body. The new contract: `MergeSquash` stages the squash and stops; the test materializes the commit itself.

```go
func TestMergeSquash(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)

	run := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
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
	run("checkout", "main")

	if err := client.MergeSquash(t.Context(), "feature", "main"); err != nil {
		t.Fatalf("MergeSquash: %v", err)
	}

	// After MergeSquash the working tree must have staged the squash but NOT yet
	// committed. Verify the staged tree contains feat.go and the branch tip is
	// still the pre-merge commit.
	var statusBuf bytes.Buffer
	statusCmd := exec.Command("git", "status", "--porcelain")
	statusCmd.Dir = dir
	statusCmd.Stdout = &statusBuf
	if err := statusCmd.Run(); err != nil {
		t.Fatalf("git status: %v", err)
	}
	if !strings.Contains(statusBuf.String(), "feat.go") {
		t.Errorf("status missing feat.go after MergeSquash: %q", statusBuf.String())
	}

	// Caller materializes the commit (mirrors close.go's responsibility).
	run("commit", "-m", "feat: squash test")

	// Verify feat.go exists on main.
	if _, err := os.Stat(filepath.Join(dir, "feat.go")); err != nil {
		t.Error("feat.go not found on main after squash merge")
	}

	var squashBuf bytes.Buffer
	logCmd := exec.Command("git", "log", "--oneline", "-1")
	logCmd.Dir = dir
	logCmd.Stdout = &squashBuf
	if err := logCmd.Run(); err != nil {
		t.Fatalf("git log: %v", err)
	}
	if !strings.Contains(squashBuf.String(), "feat: squash test") {
		t.Errorf("tip commit subject not found: %q", squashBuf.String())
	}
}
```

- [ ] **Step 2: Update `TestDeleteLocalBranch_forceDelete` to materialize the commit after `MergeSquash`**

Same file, `TestDeleteLocalBranch_forceDelete` (~line 336). Find the `client.MergeSquash(...)` call. Drop the `author` arg and add a follow-up `git commit`:

```go
if err := client.MergeSquash(t.Context(), "feature", "main"); err != nil {
	t.Fatalf("MergeSquash: %v", err)
}

// Materialize the squash commit so the branch becomes deletable with -D.
commitCmd := exec.Command("git", "commit", "-m", "feat: squash test")
commitCmd.Dir = dir
if out, err := commitCmd.CombinedOutput(); err != nil {
	t.Fatalf("git commit: %v\n%s", err, out)
}
```

- [ ] **Step 3: Run tests to see the expected compile failure**

```bash
mise exec -- go test ./git/... 2>&1 | head -20
```

Expected: build error in `git/merge.go` because `MergeSquash` signature doesn't match the new callers.

- [ ] **Step 4: Refactor `MergeSquash` in `git/merge.go`**

Replace the entire `MergeSquash` function (~lines 59–98). Drop SHA resolution and the commit step. Net body:

```go
// MergeSquash checks out baseBranch and squash-merges branchName into it,
// leaving the squashed changes staged. The caller is responsible for the
// follow-up commit (so it can drive an interactive commit form).
func (c *Client) MergeSquash(ctx context.Context, branchName, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", baseBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--squash", branchName); err != nil {
		return fmt.Errorf("merge --squash %s: %w", branchName, err)
	}

	return nil
}
```

- [ ] **Step 5: Clean up imports**

`MergeSquash` no longer references `plumbing`. Check whether `plumbing` is used elsewhere in `git/merge.go`:

```bash
grep -n "plumbing\." /workspace/git/merge.go
```

If no other reference, remove the import line `"github.com/go-git/go-git/v6/plumbing"` from `git/merge.go`.

- [ ] **Step 6: Run the git package tests**

```bash
mise exec -- go test ./git/... -count=1
```

Expected: PASS for both `TestMergeSquash` and `TestDeleteLocalBranch_forceDelete`.

- [ ] **Step 7: Full build and vet**

```bash
mise exec -- go build ./... && mise exec -- go vet ./...
```

Expected: vet may flag the same pre-existing issues in other packages; `git/` itself must be clean.

The build WILL fail in `cmd/issue/close.go` because it still passes `author` to `MergeSquash`. That's intentional — Task 7 fixes it. For now this task ends in a known broken state for `cmd/issue/`.

- [ ] **Step 8: Confirm only `cmd/issue/close.go` is broken**

```bash
mise exec -- go build ./... 2>&1 | grep -v "cmd/issue" | head
```

Expected: no errors outside `cmd/issue/`. If something else breaks, stop and investigate.

- [ ] **Step 9: Checkpoint**

Stop and let the user commit. (The repo is mid-refactor; the user knows the close.go callsite will be wired up in Tasks 6–7.)

---

## Task 6: Drop `author` parameter from `tui.IssueMergeConfirm`

**Files:**
- Modify: `tui/issue.go`

- [ ] **Step 1: Search for callers**

```bash
grep -rn "IssueMergeConfirm" /workspace --include="*.go"
```

Expected: one caller in `cmd/issue/close.go`. (Task 7 updates it.)

- [ ] **Step 2: Update `tui/issue.go`**

Replace the function (~lines 593–607):

```go
// IssueMergeConfirm shows a merge summary and asks for final confirmation.
func IssueMergeConfirm(branchName, baseBranch, strategy string, confirmed *bool) *huh.Group {
	desc := fmt.Sprintf("%s → %s (%s)", branchName, baseBranch, strategy)

	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Merge %q into %q?", branchName, baseBranch)).
			Description(desc).
			Value(confirmed),
	)
}
```

- [ ] **Step 3: Build expectations**

```bash
mise exec -- go build ./... 2>&1 | head
```

Expected: still failing only in `cmd/issue/close.go` (now for two reasons — `MergeSquash` signature plus `IssueMergeConfirm` signature). Task 7 fixes both.

- [ ] **Step 4: Run tui tests**

```bash
mise exec -- go test ./tui/... -count=1
```

Expected: PASS.

- [ ] **Step 5: Checkpoint**

Stop and let the user commit.

---

## Task 7: Wire up the new flow in `cmd/issue/close.go`

**Files:**
- Modify: `cmd/issue/close.go`

This is the largest single edit. It removes `pickSquashAuthor`, replaces the squash branch of `doMerge` with the new form-driven sequence, and updates the `IssueMergeConfirm` callsite.

- [ ] **Step 1: Read the current `doMerge` and `pickSquashAuthor` to confirm the existing structure**

```bash
mise exec -- go vet ./cmd/issue/... 2>&1 | head
```

Take stock of all the symbols you'll need:
- `picked.BranchName`, `picked.IssueSlug`, `picked.Type` are on `*store.BranchRow`.
- The store `s` is in scope inside `closeRunE` and is passed through `doMerge`? — check; if not, thread it through. (It is currently opened in `closeRunE` line 30 and not passed into `doMerge`; we will widen `doMerge`'s signature.)
- `i.appConfig` (the `Issue` receiver's config) needs to reach `doMerge`.

- [ ] **Step 2: Update the imports in `cmd/issue/close.go`**

Add:

```go
"github.com/go-git/go-git/v6/plumbing"

commitpkg "github.com/piprim/git-zf/commit"
```

(Keep existing imports — `git`, `huh`, `store`, `tracker`, `tui`, etc.)

- [ ] **Step 3: Widen `doMerge` to accept config and store**

Change the signature from:

```go
func doMerge(
	ctx context.Context,
	c *git.Client,
	pickedBranch *store.BranchRow,
	baseBranch string,
) (squash, aborted bool, err error) {
```

to:

```go
func doMerge(
	ctx context.Context,
	c *git.Client,
	pickedBranch *store.BranchRow,
	baseBranch string,
	cfg *config.AppConfig,
	s *store.Store,
) (squash, aborted bool, err error) {
```

Update the single caller in `closeRunE` to pass `i.appConfig` and `s`. (Import `"github.com/piprim/git-zf/config"` if not already present.)

- [ ] **Step 4: Replace the squash branch and remove `pickSquashAuthor` / `IssueMergeAuthor` usage**

Inside `doMerge`, remove the block:

```go
var author string
if squash {
	if err := pickSquashAuthor(c, &author); err != nil {
		return false, false, err
	}
}
```

Update the strategy variable line (`strategy := "no-ff" / ... = "squash"`) — it's still needed for the confirm form.

Update the confirm form construction to drop `author`:

```go
form := tui.IssueMergeConfirm(pickedBranch.BranchName, baseBranch, strategy, &confirmed)
if err := huh.NewForm(form).Run(); err != nil {
	return false, false, fmt.Errorf("confirm form: %w", err)
}

if !confirmed {
	return squash, true, nil
}
```

Replace the existing post-confirm action block:

```go
if squash {
	if err := c.MergeSquash(ctx, pickedBranch.BranchName, baseBranch, author); err != nil {
		return false, false, fmt.Errorf("merge squash: %w", err)
	}
} else {
	if err := c.MergeNoFF(ctx, pickedBranch.BranchName, baseBranch); err != nil {
		return false, false, fmt.Errorf("merge no-ff: %w", err)
	}
}
```

with:

```go
if squash {
	if err := doSquashCommit(ctx, c, pickedBranch, baseBranch, cfg, s); err != nil {
		return false, false, err
	}
} else {
	if err := c.MergeNoFF(ctx, pickedBranch.BranchName, baseBranch); err != nil {
		return false, false, fmt.Errorf("merge no-ff: %w", err)
	}
}
```

- [ ] **Step 5: Add the new `doSquashCommit` helper inside `cmd/issue/close.go`**

Append below `doMerge` (or in whatever local convention the file uses for helpers):

```go
// doSquashCommit resolves the tip SHAs of the source and base branches, runs
// `git merge --squash` (which stages but does not commit), then opens the
// pre-filled commit form and records the squash commit. The form's submission
// supplies the final commit message and option flags; Esc/Ctrl+C aborts and
// leaves the staged squash in place for manual inspection or reset.
func doSquashCommit(
	ctx context.Context,
	c *git.Client,
	pickedBranch *store.BranchRow,
	baseBranch string,
	cfg *config.AppConfig,
	s *store.Store,
) error {
	branchRef, err := c.Repo().Reference(plumbing.ReferenceName("refs/heads/"+pickedBranch.BranchName), true)
	if err != nil {
		return fmt.Errorf("resolve branch %q: %w", pickedBranch.BranchName, err)
	}

	baseRef, err := c.Repo().Reference(plumbing.ReferenceName("refs/heads/"+baseBranch), true)
	if err != nil {
		return fmt.Errorf("resolve base %q: %w", baseBranch, err)
	}

	if err := c.MergeSquash(ctx, pickedBranch.BranchName, baseBranch); err != nil {
		return fmt.Errorf("merge squash: %w", err)
	}

	hint := commitpkg.IssueHint{
		IssueID:    pickedBranch.IssueSlug,
		BranchType: pickedBranch.Type,
	}
	prefill := hint.Prefill(cfg.CommitMessage.Items)
	prefill["subject"] = fmt.Sprintf("Squashed merge of %s into %s.",
		branchRef.Hash().String()[:7], baseRef.Hash().String()[:7])

	authors, authorsErr := c.Authors()
	if authorsErr != nil {
		authors = []string{}
	}

	defaults := tui.CommitOption{Authors: authors}
	if len(authors) > 0 {
		defaults.Author = authors[0]
	}

	msg, opts, err := commitpkg.FillOutForm(ctx, cfg, defaults, s, prefill)
	if err != nil {
		return fmt.Errorf("fill commit form: %w", err)
	}

	if _, err := c.Commit(ctx, msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	}); err != nil {
		return fmt.Errorf("commit squash: %w", err)
	}

	return nil
}
```

Note: the new helper calls `c.Repo()`. `git.Client` does not currently expose its underlying `*gogit.Repository`. Add a getter — see Step 6.

- [ ] **Step 6: Add a `Repo()` accessor on `git.Client`**

In `/workspace/git/git.go`, immediately below the existing `WorkingTreeRoot` method, add:

```go
// Repo returns the underlying go-git repository. Use it for read-only ref
// resolution from callers that need plumbing.ReferenceName lookups.
func (c *Client) Repo() *gogit.Repository {
	return c.repo
}
```

(`gogit` is already aliased in `git/git.go` per the existing import.)

Alternative if you'd rather not expose `Repo()`: add a dedicated method `(c *Client) ResolveRef(name string) (plumbing.Hash, error)` that wraps the call, and have `doSquashCommit` use that instead. Pick whichever is more idiomatic for this codebase — `Repo()` is shorter but `ResolveRef` keeps the abstraction tighter. **Default to `ResolveRef`** unless the codebase already exposes the raw repo elsewhere (it does not, per the `git.Client` struct).

If you go with `ResolveRef`, the helper signature is:

```go
func (c *Client) ResolveRef(name string) (plumbing.Hash, error) {
	ref, err := c.repo.Reference(plumbing.ReferenceName(name), true)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve ref %q: %w", name, err)
	}

	return ref.Hash(), nil
}
```

And `doSquashCommit` calls:

```go
branchHash, err := c.ResolveRef("refs/heads/" + pickedBranch.BranchName)
if err != nil { return err }

baseHash, err := c.ResolveRef("refs/heads/" + baseBranch)
if err != nil { return err }

// ... later:
prefill["subject"] = fmt.Sprintf("Squashed merge of %s into %s.",
    branchHash.String()[:7], baseHash.String()[:7])
```

In that case `cmd/issue/close.go` does NOT need the `plumbing` import.

- [ ] **Step 7: Delete the now-unused `pickSquashAuthor` function**

Remove `func pickSquashAuthor(client *git.Client, author *string) error { ... }` and its `tui.IssueMergeAuthor` invocation from `cmd/issue/close.go`. The `tui.IssueMergeAuthor` helper itself stays in `tui/issue.go` — it has no other production caller today, but the spec scopes this change to the squash close flow; out-of-scope cleanup of `IssueMergeAuthor` belongs in a separate task.

(If a `grep -rn "IssueMergeAuthor" /workspace --include="*.go"` confirms zero remaining callers, you may opt to delete `IssueMergeAuthor` too — but only if the user is comfortable widening scope. Default: leave it alone.)

- [ ] **Step 8: Build, vet, test**

```bash
mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...
```

Expected: PASS across all packages. The pre-existing vet failures (if any) should be the same set as before this plan — we have not changed any test files outside the ones explicitly covered.

- [ ] **Step 9: Checkpoint**

Stop and let the user commit.

---

## Task 8: Manual smoke test

**Files:** none modified.

- [ ] **Step 1: Rebuild the local binary**

```bash
mise exec -- go build -o /workspace/bin/git-zf /workspace
```

- [ ] **Step 2: Install (caller decides)**

The user runs `make install` from `/workspace` in their own terminal, then exercises `git zf issue close --debug` in a repo with at least one in-progress issue branch.

Expected user-facing flow:

1. Branch picker shows in-progress branches.
2. Dry-run completes instantly (merge-tree based — already shipped).
3. Strategy form → select `squash`.
4. Confirm form shows `<branch> → <base> (squash)` and **no** "Author:" line.
5. Commit form opens with: `type` pre-selected to the branch's type (e.g. `fix`); `scope` pre-filled with the issue ID; `subject` pre-filled with `"Squashed merge of <bsha[:7]> into <basesha[:7]>."`; `body` and `footer` empty; author dropdown defaulted to the current git identity.
6. Submitting the form produces a commit like `fix(1138611): Squashed merge of abc1234 into def5678.` and the rest of the close flow (status updates, optional branch deletion) proceeds unchanged.
7. Pressing Esc/Ctrl+C in the form aborts the close with a non-zero exit; the squashed changes remain staged for the operator to inspect/reset.

- [ ] **Step 3: Final summary back to user**

Report which files moved, link to the spec, surface the spec's "out of scope" follow-ups (no e2e form test harness, `git zf commit` default-author pre-fill, `MergeNoFF` symmetric routing) so they can decide whether to schedule any of them.

---

## Self-review notes (author's check before handing off)

- Spec section "Final commit-message shape" → covered by Task 7 (subject pre-fill) plus Task 1's `Prefill` test cases for type/scope.
- Spec section "Architecture / call sequence" → covered by Tasks 5, 6, 7.
- Spec section "File-level changes" → every file enumerated in the spec has its own task or step here.
- Spec section "Error handling" → covered by Task 7's `doSquashCommit` (errors from `c.Repo()/ResolveRef`, `MergeSquash`, `FillOutForm`, `Commit` all use `fmt.Errorf("ctx: %w", err)` per the wrapcheck convention).
- Spec section "Testing" → Tasks 1, 3, 5 add/update the listed tests. Manual e2e is Task 8. No automated form-mock harness is in scope (matches spec's "Not in this change" clause).
- Signature consistency: `MergeSquash(ctx, branchName, baseBranch)` (Task 5) is what `doSquashCommit` calls (Task 7). `FillOutForm(..., hs, prefill)` (Task 2) is what `cmd/commit` (Task 2 Step 5) and `doSquashCommit` (Task 7) both call. `IssueMergeConfirm(branchName, baseBranch, strategy, *bool)` (Task 6) matches the Task 7 call site.
- No placeholders, no "TBD", no "similar to Task N".
