# Commit–Issue Link Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When `git zf commit` is run on an issue branch (`issueID@type@slug@uuid`), automatically pre-populate one commit message field with the issue ID and pre-select the commit type to match the branch type. Zero config: it just works on an issue branch; silently skipped otherwise.

**Architecture:** A new `git.Client.CurrentBranch()` reads HEAD. `cmd/commit` calls `branch.Parse` on it and produces an `IssueHint{IssueID, BranchType}`. The hint is passed through `commit.FillOutForm` → `loadForm`, which mutates a clone of `cfg.CommitMessage.Items` (fallback chain: `scope` → `footer` → `subject`) and pre-selects the commit type if it matches a configured type. All failures degrade silently to "no pre-population".

**Tech Stack:** Go 1.25, `go-git/v6`, `charmbracelet/huh`, `spf13/cobra`. Toolchain via mise — always run `mise exec -- go ...`.

---

## File Map

| File | Status | Responsibility |
|------|--------|----------------|
| `git/git.go` | modify | Add `CurrentBranch()` method on `Client` |
| `git/git_test.go` | modify | Add `TestCurrentBranch_*` tests |
| `commit/form.go` | modify | Add `IssueHint`, `setItemValue`, `applyIssueHint`, `isValidCommitType`; update `FillOutForm` + `loadForm` to accept hint |
| `commit/form_test.go` | modify | Add tests for the three new helpers |
| `cmd/commit/commit.go` | modify | Add `issueHintFromClient`; pass hint to `FillOutForm` |
| `cmd/commit/commit_test.go` | create | Add `TestIssueHintFromClient_*` tests |

---

## Task 1: `git.Client.CurrentBranch()`

**Files:**
- Modify: `git/git.go` (add new method on `*Client`, after `WorkingTreeRoot`)
- Modify: `git/git_test.go` (add tests using existing `newTestRepo` in-memory pattern)

- [ ] **Step 1: Write the failing tests**

Append to `git/git_test.go`:

```go
func TestCurrentBranch_master(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	client := &Client{repo: repo}

	got, err := client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if got != "master" {
		t.Errorf("CurrentBranch = %q, want %q", got, "master")
	}
}

func TestCurrentBranch_issueBranch(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	client := &Client{repo: repo}

	const name = "ABC-42@feat@add-oauth-login@a1b2c3d4"
	if err := client.CreateBranch(name, "master"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	got, err := client.CurrentBranch()
	if err != nil {
		t.Fatalf("CurrentBranch: %v", err)
	}
	if got != name {
		t.Errorf("CurrentBranch = %q, want %q", got, name)
	}
}

func TestCurrentBranch_emptyRepo(t *testing.T) {
	t.Parallel()

	repo, err := gogit.Init(memory.NewStorage(), gogit.WithWorkTree(memfs.New()))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	client := &Client{repo: repo}

	if _, err := client.CurrentBranch(); err == nil {
		t.Error("CurrentBranch on empty repo: expected error, got nil")
	}
}
```

`gogit`, `memory`, and `memfs` are already imported by the existing tests in this file (`github.com/go-git/go-git/v6`, `.../storage/memory`, `github.com/go-git/go-billy/v6/memfs`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./git/... -run TestCurrentBranch -v`

Expected: build failure — `client.CurrentBranch undefined`.

- [ ] **Step 3: Implement `CurrentBranch`**

In `git/git.go`, add after `WorkingTreeRoot` (before `Authors`):

```go
// CurrentBranch returns the short name of the branch HEAD points to.
// On a detached HEAD the returned name will not parse as an issue branch,
// so callers can simply ignore it.
func (c *Client) CurrentBranch() (string, error) {
	head, err := c.repo.Head()
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}

	return head.Name().Short(), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./git/... -run TestCurrentBranch -v`

Expected: all three tests PASS.

- [ ] **Step 5: Run full git package tests**

Run: `mise exec -- go test ./git/...`

Expected: all PASS, no regressions.

- [ ] **Step 6: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add CurrentBranch method"
```

---

## Task 2: `setItemValue` and `isValidCommitType` helpers

**Files:**
- Modify: `commit/form.go` (add two unexported helpers)
- Modify: `commit/form_test.go` (add unit tests)

- [ ] **Step 1: Write the failing tests**

The literal `"ABC-1"` would otherwise repeat 4+ times across the new tests in this file, which `add-constant` (revive) flags above 3. Define a file-local constant once.

Append to `commit/form_test.go`:

```go
const testIssueID = "ABC-1"

func TestSetItemValue_found(t *testing.T) {
	items := []config.CommitItem{
		{Name: "scope"},
		{Name: "subject"},
	}

	ok := setItemValue(items, "scope", testIssueID)
	if !ok {
		t.Fatal("setItemValue: returned false, want true")
	}
	if items[0].Value != testIssueID {
		t.Errorf("items[0].Value = %q, want %q", items[0].Value, testIssueID)
	}
}

func TestSetItemValue_notFound(t *testing.T) {
	items := []config.CommitItem{
		{Name: "subject"},
	}
	original := items[0]

	ok := setItemValue(items, "scope", testIssueID)
	if ok {
		t.Error("setItemValue: returned true, want false")
	}
	if items[0] != original {
		t.Errorf("items[0] mutated: got %+v, want %+v", items[0], original)
	}
}

func TestIsValidCommitType_match(t *testing.T) {
	types := []config.CommitTypeOption{
		{Name: "feat"},
		{Name: "fix"},
	}
	if !isValidCommitType(types, "feat") {
		t.Error("isValidCommitType(feat) = false, want true")
	}
}

func TestIsValidCommitType_noMatch(t *testing.T) {
	types := []config.CommitTypeOption{{Name: "feat"}}
	if isValidCommitType(types, "wip") {
		t.Error("isValidCommitType(wip) = true, want false")
	}
}

func TestIsValidCommitType_emptyTypes(t *testing.T) {
	if isValidCommitType(nil, "feat") {
		t.Error("isValidCommitType(nil, feat) = true, want false")
	}
}
```

The test file already imports `"testing"` and uses `bytes`. Add `"github.com/piprim/git-zf/config"` to the import list.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./commit/... -run "TestSetItemValue|TestIsValidCommitType" -v`

Expected: build failure — `setItemValue undefined`, `isValidCommitType undefined`.

- [ ] **Step 3: Implement the helpers**

In `commit/form.go`, add after the `assembleMessage` function (before `loadForm`):

```go
// setItemValue finds the first item with the given name and sets its Value.
// Reports whether a match was found.
func setItemValue(items []config.CommitItem, name string, value string) bool {
	for i := range items {
		if items[i].Name == name {
			items[i].Value = value

			return true
		}
	}

	return false
}

// isValidCommitType reports whether name matches a configured commit type.
func isValidCommitType(types []config.CommitTypeOption, name string) bool {
	return slices.ContainsFunc(types, func(t config.CommitTypeOption) bool {
		return t.Name == name
	})
}
```

`slices` is already imported in `form.go`. `config` is also already imported.

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./commit/... -run "TestSetItemValue|TestIsValidCommitType" -v`

Expected: all five tests PASS.

- [ ] **Step 5: Commit**

```bash
git add commit/form.go commit/form_test.go
git commit -m "feat(commit): add setItemValue and isValidCommitType helpers"
```

---

## Task 3: `IssueHint` struct and `applyIssueHint`

**Files:**
- Modify: `commit/form.go` (add `IssueHint` type and `applyIssueHint`)
- Modify: `commit/form_test.go` (add table-driven tests)

- [ ] **Step 1: Write the failing tests**

Append to `commit/form_test.go`:

```go
func TestApplyIssueHint(t *testing.T) {
	tests := []struct {
		name      string
		items     []config.CommitItem
		hint      IssueHint
		wantField string // empty = no field set
		wantValue string
	}{
		{
			name: "scope_wins_over_footer_and_subject",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "scope"}, {Name: "footer"},
			},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "scope",
			wantValue: testIssueID,
		},
		{
			name: "footer_used_when_scope_missing",
			items: []config.CommitItem{
				{Name: "subject"}, {Name: "footer"},
			},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "footer",
			wantValue: "Refs: " + testIssueID,
		},
		{
			name:      "subject_used_when_only_subject_present",
			items:     []config.CommitItem{{Name: "subject"}},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "subject",
			wantValue: "(" + testIssueID + ")",
		},
		{
			name:      "no_matching_field_no_change",
			items:     []config.CommitItem{{Name: "body"}},
			hint:      IssueHint{IssueID: testIssueID},
			wantField: "",
		},
		{
			name:      "empty_issue_id_early_return",
			items:     []config.CommitItem{{Name: "scope"}, {Name: "footer"}},
			hint:      IssueHint{IssueID: ""},
			wantField: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Snapshot original to ensure applyIssueHint never mutates the input.
			originalCopy := slices.Clone(tt.items)

			got := applyIssueHint(tt.items, tt.hint)

			assertNoInputMutation(t, tt.items, originalCopy)

			if tt.wantField == "" {
				assertNoFieldSet(t, got)

				return
			}

			assertFieldValue(t, got, tt.wantField, tt.wantValue)
		})
	}
}

func assertNoInputMutation(t *testing.T, got, want []config.CommitItem) {
	t.Helper()

	for i := range got {
		if got[i] != want[i] {
			t.Errorf("input mutated at [%d]: got %+v, want %+v",
				i, got[i], want[i])
		}
	}
}

func assertNoFieldSet(t *testing.T, items []config.CommitItem) {
	t.Helper()

	for _, item := range items {
		if item.Value != "" {
			t.Errorf("expected no field set, but %q = %q", item.Name, item.Value)
		}
	}
}

func assertFieldValue(t *testing.T, items []config.CommitItem, name, want string) {
	t.Helper()

	for _, item := range items {
		if item.Name != name {
			continue
		}
		if item.Value != want {
			t.Errorf("%s.Value = %q, want %q", name, item.Value, want)
		}

		return
	}

	t.Errorf("field %q not found in result", name)
}
```

This decomposition keeps each helper at a single responsibility (`focused-functions`) and pulls the 3-deep nesting in the original assertion loop down to 2 levels (`max-control-nesting` = 3 stays comfortably under the cap). Add `"slices"` to the imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./commit/... -run TestApplyIssueHint -v`

Expected: build failure — `IssueHint undefined`, `applyIssueHint undefined`.

- [ ] **Step 3: Implement `IssueHint` and `applyIssueHint`**

In `commit/form.go`, add after the `assembleMessage` function and before `setItemValue`:

```go
// IssueHint carries issue context detected from the current branch.
// Zero value means no issue branch — form fields are left unchanged.
type IssueHint struct {
	IssueID    string
	BranchType string
}

// applyIssueHint pre-populates one field using the fallback chain:
// scope → footer (Refs: ID) → subject ((ID)).
// Returns a clone of items; never mutates the original slice.
func applyIssueHint(items []config.CommitItem, hint IssueHint) []config.CommitItem {
	if hint.IssueID == "" {
		return items
	}

	out := slices.Clone(items)

	if setItemValue(out, "scope", hint.IssueID) {
		return out
	}
	if setItemValue(out, "footer", "Refs: "+hint.IssueID) {
		return out
	}
	if setItemValue(out, "subject", "("+hint.IssueID+")") {
		return out
	}

	return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `mise exec -- go test ./commit/... -run TestApplyIssueHint -v`

Expected: all five subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add commit/form.go commit/form_test.go
git commit -m "feat(commit): add IssueHint and applyIssueHint"
```

---

## Task 4: Wire `IssueHint` through `FillOutForm` and `loadForm`

**Files:**
- Modify: `commit/form.go` (`FillOutForm` and `loadForm` signatures + body)
- Modify: `cmd/commit/commit.go` (caller passes zero hint to keep build green)

This task is a pure refactor: the API gains a parameter but no caller exercises pre-population yet. Task 5 wires the real hint.

- [ ] **Step 1: Update `FillOutForm` signature and body**

In `commit/form.go`, replace the `FillOutForm` function with:

```go
// FillOutForm presents the commit TUI form.
// Group 1: type select + commit message fields (scope, subject, body, footer).
// Group 2: commit options (author, all, amend, no-verify, signoff, allow-empty).
//
//	Group 2 is skipped when defaults.AnyOptionSet() is true (flags were passed).
//
// The hint pre-populates one message field and pre-selects the commit type
// when it matches a configured type. Zero hint = no pre-population.
//
// Returns the assembled commit message bytes and the (possibly user-modified) options.
func FillOutForm(cfg *config.AppConfig, defaults tui.CommitOption, hint IssueHint) ([]byte, tui.CommitOption, error) {
	form, extractMsg, extractOpts := loadForm(cfg, defaults, hint)
	tmplText := cfg.CommitMessage.Template

	if err := form.Run(); err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("failed to run the form: %w", err)
	}

	answers := extractMsg()
	opts := extractOpts()

	var buf bytes.Buffer
	if err := assembleMessage(&buf, tmplText, answers); err != nil {
		log.Printf("assemble failed, err=%v\n", err)

		return nil, tui.CommitOption{}, fmt.Errorf("assemble message: %w", err)
	}

	return buf.Bytes(), opts, nil
}
```

- [ ] **Step 2: Update `loadForm` signature and body**

In `commit/form.go`, replace the `loadForm` function with:

```go
func loadForm(
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	hint IssueHint,
) (form *huh.Form, extractMsg func() map[string]any, extractOpts func() tui.CommitOption) {
	log.Printf("message tmpl: %s", cfg.CommitMessage.Template)

	items := applyIssueHint(cfg.CommitMessage.Items, hint)

	var selectedType string
	if isValidCommitType(cfg.CommitTypes, hint.BranchType) {
		selectedType = hint.BranchType
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

Key changes from the previous version:
- New `hint IssueHint` parameter.
- `items := applyIssueHint(...)` produces a clone with the hint applied; the form binds to `items` (not `cfg.CommitMessage.Items` directly), so the user's config is never mutated.
- `selectedType` is initialised from `hint.BranchType` when valid.
- `extractMsg` reads from `items`, not `cfg.CommitMessage.Items`.

- [ ] **Step 3: Update the only caller to keep the build green**

In `cmd/commit/commit.go`, find the `FillOutForm` call inside `runE` and update it:

```go
msg, opts, err := commitpkg.FillOutForm(c.appConfig, defaults, commitpkg.IssueHint{})
```

- [ ] **Step 4: Build to verify the refactor compiles**

Run: `mise exec -- go build ./...`

Expected: no errors.

- [ ] **Step 5: Run all tests to verify no regressions**

Run: `mise exec -- go test ./...`

Expected: all tests PASS, including the existing `TestAssembleMessage` and `TestBuildAuthorList_*` suites.

- [ ] **Step 6: Commit**

```bash
git add commit/form.go cmd/commit/commit.go
git commit -m "refactor(commit): thread IssueHint through FillOutForm"
```

---

## Task 5: `issueHintFromClient` and wiring

**Files:**
- Modify: `cmd/commit/commit.go` (add `issueHintFromClient`, replace zero hint in `runE`)
- Create: `cmd/commit/commit_test.go` (test `issueHintFromClient` with on-disk repos)

- [ ] **Step 1: Write the failing tests**

Create `cmd/commit/commit_test.go`:

```go
package commit

import (
	"context"
	"os/exec"
	"testing"

	"github.com/piprim/git-zf/git"
)

const (
	testIssueID     = "ABC-1"
	testIssueBranch = "ABC-1@feat@my-feat@a1b2c3d4"
)

// initRepoOnDisk creates a real on-disk git repo at dir with one commit on master.
// Uses the system git binary because git.NewClientAt opens an on-disk repo,
// and CurrentBranch reads HEAD via go-git.
func initRepoOnDisk(t *testing.T, dir string) {
	t.Helper()

	ctx := t.Context()
	run := func(args ...string) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	run("init", "--initial-branch=master")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	run("commit", "--allow-empty", "-m", "chore: init")
}

// gitCheckoutNewBranch runs `git checkout -b name` in dir.
func gitCheckoutNewBranch(t *testing.T, ctx context.Context, dir, name string) {
	t.Helper()

	cmd := exec.CommandContext(ctx, "git", "checkout", "-b", name)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("checkout: %v\n%s", err, out)
	}
}

func TestIssueHintFromClient_issueBranch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initRepoOnDisk(t, dir)
	gitCheckoutNewBranch(t, t.Context(), dir, testIssueBranch)

	client, err := git.NewClientAt(dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	hint := issueHintFromClient(client)
	if hint.IssueID != testIssueID {
		t.Errorf("IssueID = %q, want %q", hint.IssueID, testIssueID)
	}
	if hint.BranchType != "feat" {
		t.Errorf("BranchType = %q, want %q", hint.BranchType, "feat")
	}
}

func TestIssueHintFromClient_nonIssueBranch(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	initRepoOnDisk(t, dir)

	client, err := git.NewClientAt(dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	hint := issueHintFromClient(client)
	if hint.IssueID != "" || hint.BranchType != "" {
		t.Errorf("expected zero IssueHint on master, got %+v", hint)
	}
}

func TestIssueHintFromClient_emptyRepo(t *testing.T) {
	t.Parallel()

	// Init a repo with no commits — CurrentBranch will fail to read HEAD,
	// exercising issueHintFromClient's graceful-degradation path.
	dir := t.TempDir()
	ctx := t.Context()

	cmd := exec.CommandContext(ctx, "git", "init", "--initial-branch=master")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}

	client, err := git.NewClientAt(dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	hint := issueHintFromClient(client)
	if hint.IssueID != "" || hint.BranchType != "" {
		t.Errorf("expected zero IssueHint when HEAD unreadable, got %+v", hint)
	}
}
```

Notes:
- All `exec.CommandContext` (per `golang-modern-go`: never `exec.Command`) — context comes from `t.Context()` (Go 1.24+, automatically cancelled at test end).
- `testIssueID` and `testIssueBranch` are file-local constants to satisfy `add-constant`.
- The package is `commit` (matches the directory under `cmd/`), so unexported `issueHintFromClient` is callable directly.

- [ ] **Step 2: Run tests to verify they fail**

Run: `mise exec -- go test ./cmd/commit/... -run TestIssueHintFromClient -v`

Expected: build failure — `issueHintFromClient undefined`.

- [ ] **Step 3: Implement `issueHintFromClient` and wire it into `runE`**

In `cmd/commit/commit.go`:

1. Add the import `"github.com/piprim/git-zf/branch"` to the import block.

2. Add the helper function below `printCommitSummary` (or anywhere at file scope after `runE`):

```go
// issueHintFromClient detects whether the current branch is an issue branch
// and returns the corresponding IssueHint. All failure modes (detached HEAD,
// non-issue branch name) collapse to the zero value, leaving the form unchanged.
func issueHintFromClient(c *git.Client) commitpkg.IssueHint {
	name, err := c.CurrentBranch()
	if err != nil {
		return commitpkg.IssueHint{}
	}

	b, err := branch.Parse(name)
	if err != nil {
		return commitpkg.IssueHint{}
	}

	return commitpkg.IssueHint{IssueID: b.IssueID(), BranchType: b.Type()}
}
```

3. In `runE`, replace the `FillOutForm` line (added in Task 4) with:

```go
hint := issueHintFromClient(client)
msg, opts, err := commitpkg.FillOutForm(c.appConfig, defaults, hint)
```

- [ ] **Step 4: Run the new tests to verify they pass**

Run: `mise exec -- go test ./cmd/commit/... -run TestIssueHintFromClient -v`

Expected: all three tests PASS.

- [ ] **Step 5: Run all tests to verify no regressions**

Run: `mise exec -- go test ./...`

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/commit/commit.go cmd/commit/commit_test.go
git commit -m "feat(commit): pre-populate fields from current issue branch"
```

---

## Final verification

- [ ] **Step 1: Build the binary**

Run: `mise exec -- go build -o ./bin/git-zf .`

Expected: no errors.

- [ ] **Step 2: Vet**

Run: `mise exec -- go vet ./...`

Expected: no warnings.

- [ ] **Step 3: golangci-lint (mandatory before declaring done)**

Run: `mise exec -- golangci-lint run ./...`

Expected: zero findings. Per the `golang-linter-rules` skill, every violation must be fixed before reporting the task complete.

- [ ] **Step 4: Full test pass**

Run: `mise exec -- go test -race ./...`

Expected: all PASS, no race detector warnings.

- [ ] **Step 5: Manual smoke test (optional, if a real repo is at hand)**

```bash
# In a sandbox repo:
./bin/git-zf branch new   # create an issue branch (interactive)
# stage a change, then:
./bin/git-zf commit       # observe the scope/footer/subject pre-fill
```

Expected: the form opens with one of `scope`, `footer`, or `subject` already filled with the issue ID, and the type select pre-positioned on the branch type if it matches a configured commit type.

---

## Notes for the executing agent

- **Mandatory Go skills (project-local, in `.claude/skills/learned/`):** before writing or editing any Go in this plan, invoke every skill in `golang-mandatory.md` — at minimum `focused-functions`, `golang-linter-rules`, `golang-modern-go`, `golang-naming`, `golang-revive-rules`, `golang-data-structures`, and `golang-testing`. Their rules are baked into the code blocks in this plan; preserve them when transcribing.
- **Linter discipline (from `golang-linter-rules`):**
  - `nlreturn` — blank line before every `return` that is not the sole statement in its `{}` block.
  - `wrapcheck` — wrap external errors with `fmt.Errorf("context: %w", err)`. The `c.repo.Head()` and `gogit.Init` calls in this plan already comply.
  - `add-constant` — no string literal repeated 4+ times. The `testIssueID` and `testIssueBranch` constants are introduced for this reason.
  - `error-strings` — lowercase first letter, no trailing punctuation, no newlines.
- **Modern Go (from `golang-modern-go`):** use `exec.CommandContext` (never `exec.Command`); use `slices.Clone`, `slices.ContainsFunc`; use `any` not `interface{}`. The plan already follows this.
- **Revive thresholds (from `golang-revive-rules`):** `argument-limit ≤ 5`, `function-result-limit ≤ 3`, `function-length ≤ 50/150`, `file-length-limit ≤ 500`, `line-length-limit ≤ 124`, `cyclomatic ≤ 15`, `cognitive-complexity ≤ 20`, `max-control-nesting ≤ 3`. None of the new code in this plan exceeds these — keep it that way as you implement.
- **Naming (from `golang-naming`):** subtest names use underscores, not spaces (so `-run` filtering works). Receivers stay 1-2 chars (`c *Client`). `IssueHint`, `IssueID`, `BranchType` follow `MixedCaps` and the `ID` initialism rule.
- **Testing (from `golang-testing`):** every new test uses `t.Parallel()` (no shared state); the table-driven test in Task 3 is the project default shape. Test helpers call `t.Helper()` so failures point at the caller.
- **Toolchain:** always run Go via `mise exec -- go ...`. Plain `go ...` may pick up the wrong version.
- **Do not run git commands on the user's behalf** unless explicitly instructed — the plan lists commit commands as guidance; the user (or an executing agent under explicit instructions) runs them.
