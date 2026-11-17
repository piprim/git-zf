# Merge Commit Options Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On `issue close` and `branch merge`, the commit form's options page shows only Author, `--no-verify`, `--signoff`.

**Architecture:** A `MergeOnly` flag on `tui.CommitOption` makes `commit.loadForm` build a three-field `tui.MergeOptionsGroup` instead of the seven-field `tui.CommitOptionsGroup`. `mergeflow.HuhPrompter.ComposeMessage`, shared by both commands, sets the flag.

**Tech Stack:** Go, `charmbracelet/huh` v1.0.0, bubbletea v1.3.6.

**Spec:** `docs/superpowers/specs/2026-11-16-merge-commit-options-design.md`

## Global Constraints

- Run Go through mise: `mise exec -- go test ./...`.
- Every distinct assertion or scenario in a test is wrapped in a named `t.Run`.
- Before editing a function, run GitNexus impact on it (`impact({target, direction: "upstream"})`) and report HIGH/CRITICAL risk; before handing over, run `detect_changes({scope: "all"})`. Without the MCP server: `node .gitnexus/run.cjs impact "<symbol>" --direction upstream --repo .` and `node .gitnexus/run.cjs detect-changes --scope all --repo .`.
- Field order and titles on the merge page are those of `git zf commit`: Author, `Skip hooks? (--no-verify)`, `Add Signed-off-by trailer? (--signoff)`.
- `git zf commit` behaviour is unchanged.
- No commits: the user commits themselves, in place on `wip`. Each task ends with the working tree left as is.

## Review Focus

- A repository with no authors (fresh repo, `Authors` empty): the merge page shows `(no authors found)` and `Author` stays `""` → Task 1.
- `Skip` and `MergeOnly` both set: no options page at all → Task 2.
- ctrl+r (history) pressed during a close: the reopened form still has the merge page, not the full one → Task 2.
- A merge page answer can never produce `--amend`, `--all`, `--include-untracked`, `--allow-empty`: those four stay false in the returned options → Task 2.

---

### Task 1: The merge options group

**Files:**
- Modify: `tui/commit.go` (`CommitOption`, `CommitOptionsGroup`)
- Create: `tui/commit_test.go`

**Interfaces:**
- Produces: `CommitOption.MergeOnly bool`; `func MergeOptionsGroup(opt *CommitOption) *huh.Group`; unexported `func authorSelect(opt *CommitOption) *huh.Select[string]`.

- [ ] **Step 1: Write the failing tests** in `tui/commit_test.go` (package `tui`)

`TestMergeOptionsGroup`, rendering with `f := NewForm(MergeOptionsGroup(&opt)); f.Init(); view := f.View()`:

```go
opt := CommitOption{Authors: []string{"Ann <a@x>", "Bob <b@x>"}}
```

- `t.Run("shows author, no-verify, signoff in the commit order")`: `strings.Index(view, "Author:") < strings.Index(view, "--no-verify") < strings.Index(view, "--signoff")`, none `-1`.
- `t.Run("hides the staging and amend options")`: view contains none of `--all`, `--include-untracked`, `--amend`, `--allow-empty`.

- `t.Run("each confirm is bound to its own field")`: two renders, `CommitOptions: git.CommitOptions{NoVerify: true}` and `{Signoff: true}`; the two views differ (a confirm bound to the wrong pointer renders both the same).

`TestAuthorSelect`:

- `t.Run("defaults to the first author")`: after `authorSelect(&opt)` with the opt above and `Author == ""`, `opt.Author == "Ann <a@x>"`.
- `t.Run("keeps a preset author")`: `Author: "Bob <b@x>"` stays.
- `t.Run("no authors")`: `Authors: nil` → `opt.Author == ""` and `NewForm(MergeOptionsGroup(&opt))` view contains `(no authors found)`.

- [ ] **Step 2: Run to verify they fail**

Run: `mise exec -- go test ./tui/... -run "TestMergeOptionsGroup|TestAuthorSelect" -v`
Expected: build failure, `undefined: MergeOptionsGroup`.

- [ ] **Step 3: Implement in `tui/commit.go`**

- Add `MergeOnly bool` to `CommitOption`, documented: "show only the options that fit a merge commit (author, no-verify, signoff)".
- Move the author-option building, the `(no authors found)` fallback and the `opt.Author = opt.Authors[0]` default out of `CommitOptionsGroup` into `authorSelect`; `CommitOptionsGroup` calls it and is otherwise unchanged.
- `MergeOptionsGroup` = `authorSelect(opt)` + the no-verify confirm + the signoff confirm, same titles as in `CommitOptionsGroup`.

- [ ] **Step 4: Run to verify they pass**

Run: `mise exec -- go test ./tui/... -v -run "TestMergeOptionsGroup|TestAuthorSelect"` then `mise exec -- go test ./tui/...`
Expected: PASS.

---

### Task 2: Wire the merge page into the form and the merge prompter

**Files:**
- Modify: `commit/form.go` (`loadForm`, around line 455)
- Modify: `cmd/mergeflow/huh_prompter.go:71-74` (`ComposeMessage`)
- Test: `commit/form_test.go`

**Interfaces:**
- Consumes: `tui.CommitOption.MergeOnly`, `tui.MergeOptionsGroup(opt *tui.CommitOption) *huh.Group` (Task 1).

- [ ] **Step 1: Write the failing tests** in `commit/form_test.go`

`TestLoadForm_OptionsPage`, with `minimalCfg()` and a helper that returns the view of page 2:

```go
// huh v1.0.0: NextGroup() runs Update(nextGroup()) itself, no need to step the cmd.
form, _, extractOpts := loadForm(cfg, defaults, prefill)
form.Init()
form.NextGroup()
view := form.View()
```

If the message page's required fields block the move, pass a `prefill` that fills them.

- `t.Run("MergeOnly shows the merge page")`: `defaults := tui.CommitOption{MergeOnly: true, Authors: []string{"Ann <a@x>"}}` → view contains `--signoff` and `--no-verify`, not `--amend`, `--all`, `--include-untracked`, `--allow-empty`.
- `t.Run("MergeOnly leaves the staging and amend options false")`: `extractOpts()` has `All`, `IncludeUntracked`, `Amend`, `AllowEmpty` all false.
- `t.Run("default shows the full page")`: `tui.CommitOption{Authors: ...}` → view contains `--amend`.
- `t.Run("Skip wins over MergeOnly")`: `Skip: true, MergeOnly: true` → view after `NextGroup()` contains neither `--signoff` nor `--amend`.

`TestFillOutForm_MergeOnlySurvivesHistory`, `t.Run("the form reopened after ctrl+r keeps the merge page")`: with `swapRunFormFn` and an empty `fakeHistoryStore`, the runner is called three times, as in the existing "preserves original answers when history is empty" case: call 1 the main form (`WantHistory()` true), call 2 the "No commit history yet." dialog, call 3 the reopened main form (both false). Keep the `*huh.Form` of every call; assert there were 3 calls, then advance the third as above and assert `--signoff` present and `--amend` absent.

- [ ] **Step 2: Run to verify they fail**

Run: `mise exec -- go test ./commit/... -run "TestLoadForm_OptionsPage|TestFillOutForm_MergeOnlySurvivesHistory" -v`
Expected: FAIL on the `MergeOnly` cases (view contains `--amend`).

- [ ] **Step 3: Implement**

`commit/form.go` `loadForm`, replacing the `if !defaults.Skip` block:

```go
switch {
case defaults.Skip:
case defaults.MergeOnly:
	groups = append(groups, tui.MergeOptionsGroup(&opts))
default:
	groups = append(groups, tui.CommitOptionsGroup(&opts))
}
```

`cmd/mergeflow/huh_prompter.go` `ComposeMessage`: `defaults := tui.CommitOption{Authors: authors, MergeOnly: true}`; delete the `if len(authors) > 0 { defaults.Author = authors[0] }` block (`authorSelect` does it).

- [ ] **Step 4: Run to verify they pass, and the regression nets**

Run:
```
mise exec -- go test ./commit/... ./tui/... ./cmd/mergeflow/... -v -run "TestLoadForm_OptionsPage|TestFillOutForm|TestMergeOptionsGroup|TestAuthorSelect|TestRun"
mise exec -- go test ./...
```
Expected: PASS, including the close (`^TestClose_`) and merge (`^TestRunMerge_`) E2E suites.

- [ ] **Step 5: Graph check and hand over**

Run `detect_changes({scope: "all"})`; expected: changes confined to `tui/commit.go`, `commit/form.go`, `cmd/mergeflow/huh_prompter.go` and their tests. Leave everything uncommitted; TTY smoke (`git zf issue close` on a scratch branch, page 2 shows three fields) is the user's.
