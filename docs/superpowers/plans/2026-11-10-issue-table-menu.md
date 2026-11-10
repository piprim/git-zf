# Issue and Branch Tables: One Design — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enter opens a per-row menu in the issue table, the branch table gets a shortcut key per row action, both tables write their keys as `[S]tatus` / `[/]-Filter` on two lines, and the issue table reopens as it was left.

**Architecture:** Everything visible lives in `tui/` (models, hints, menu, arming, notices); `cmd/issue` and `cmd/branch` only map what the table was quit with to the code that already runs today. The issue menu is a nested `huh` form inside the table program; the branch menu stays outside its program, its keys quit the program with the key pressed.

**Tech Stack:** Go, bubbletea, bubbles/table, huh (through `tui.Form`), cobra.

**Spec:** `docs/superpowers/specs/2026-11-10-issue-table-menu-design.md` — read it with this plan.

## Global Constraints

- Run Go through mise: `mise exec -- go test ./...`.
- Tests: every assertion or scenario in a named `t.Run` (single-assertion tests included).
- Before editing a function or method, run the GitNexus impact analysis on it (`impact({target, direction: "upstream"})`) and report HIGH/CRITICAL; before the user commits, `detect_changes({scope: "all"})`.
- No commits: the user commits themselves, on `wip`, in place. A task ends when its tests pass.
- Forms are built with `tui.Form`, never `huh.NewForm` (esc must abort).
- Hints are plain text, no color. Two spaces between hints. No `·` left in the two tables' key lines.
- No key changes meaning or case: `S T R / n p P q enter` in the branch table, every key of the issue table.
- `d` keeps scrolling the branch table; delete is `D`. `u` becomes pull request.
- The dialogs' own hint lines (`enter: save · esc: cancel`, `↑↓←→ scroll · e: edit · esc: back`, `(esc: clear  enter: confirm)`) and `git zf log` are not touched.
- Nothing in `issueflow`, `issue` or `git` changes.

## Review Focus

1. Enter in list mode on a table with no visible row (a filter matching nothing): no menu, no panic, no quit. → Task 3.
2. A row left with no entry (not a repository issue, closed, `IssueActions.Show` nil): enter opens nothing. → Task 3.
3. A table rebuilt from a state that no longer fits: cursor past the rows, a project that is gone. The cursor is clamped, the project falls back to all. → Task 2.
4. A key typed while something else has the keyboard: `q` or `a` inside the open menu, `D` while typing a `/` filter. The table must not quit, arm or act. → Tasks 3 and 5.
5. Arming, moving, pressing again: `a`, down, `a` on the branch table must arm the new row, not abandon it. → Task 5.

## File Structure

- Create `tui/keyhint.go`, `tui/keyhint_test.go`: `keyHint`, the one notation.
- Modify `tui/issue.go`: hints, `IssueTableOptions` / `IssueTableState`, the menu, the abandon confirm, `IssuePickComment`.
- Modify `tui/branch.go`: two hint lines, action keys, arming, notice, `Offers`, `BranchPickKey`.
- Modify `cmd/issue/list.go`: the state across the loop, the comment pick, the Long.
- Modify `cmd/branch/table.go`, `cmd/branch/branch.go`: key → `branchAction`, `Offers`, the Long.
- Modify `CLAUDE.md`; regenerate `website/docs/reference/commands/` with `make docs-gen`.

---

### Task 1: `keyHint` and the existing keys in the new notation

**Files:**
- Create: `tui/keyhint.go`, `tui/keyhint_test.go`
- Modify: `tui/issue.go` (`filterHint`, `hint`), `tui/branch.go` (the end of `View`)
- Test: `tui/issue_test.go`, `tui/branch_test.go`

**Interfaces:**
- Produces: `func keyHint(key, label string) string`; `func keyHints(pairs ...string) string` (key, label, key, label… joined by two spaces).

- [ ] **Step 1: Write the failing test** `TestKeyHint` in `tui/keyhint_test.go`, one `t.Run` per row:

| key | label | want |
|---|---|---|
| `S` | `status` | `[S]tatus` |
| `n` | `new` | `[n]ew` |
| `C` | `checkout` | `[C]heckout` |
| `c` | `close` | `[c]lose` |
| `P` | `prune-tracker` | `[P]rune-tracker` |
| `r` | `sync` | `[r]-Sync` |
| `/` | `filter` | `[/]-Filter` |
| `enter` | `actions` | `[enter]-Actions` |
| `tab` | `status` | `[tab]-Status` |
| `u` | `pull request` | `[u]-Pull request` |

Plus `keyHints("S", "status", "/", "filter")` = `[S]tatus  [/]-Filter`.

- [ ] **Step 2:** Run `mise exec -- go test ./tui/... -run TestKeyHint -v`. Expected: FAIL, `keyHint` undefined.

- [ ] **Step 3: Implement** in `tui/keyhint.go`. Rule: `len(key) == 1 && strings.EqualFold(key, label[:1])` gives `"[" + key + "]" + label[1:]`; otherwise `"[" + key + "]-"` + the label with its first letter upper-cased.

- [ ] **Step 4: Update the view assertions** to the new lines, then make them pass:
  - `tui/issue_test.go:642` → `[enter]-Show  [e]dit  [s]tart`; `:645` → `[enter]-Pick  [q]uit`, and not `[n]ew`; `:653` → `[tab]-Status  [m]ine  [/]-Filter`; `:796` → `[n]ew  [c]lose  [a]bandon  [l]abel  [r]-Sync`; every `"enter: show"` used as "the table is showing" (`:341`, `:382`, `:485`, `:502`, `:522`, `:532`) → `[enter]-Show`.
  - `tui/branch_test.go:113` → `[S]tatus`, `[T]ype`, `[R]emotes`, `[P]rune-tracker`, and no `quit`; `:174` → `[S]tatus`.

- [ ] **Step 5: Implement the lines** with `keyHints`:
  - issue `filterHint`: `[tab]-Status  [p]roject  [m]ine  [/]-Filter` (`[p]roject` only with projects).
  - issue `hint`: list `[enter]-Show  [e]dit  [s]tart  [n]ew  [c]lose  [a]bandon  [l]abel  [r]-Sync`; pick `[enter]-Pick  [q]uit`.
  - branch `View`: line 1 `[S]tatus  [T]ype  [R]emotes  [/]-Filter` (`[R]emotes` only with `opts.Remote`), newline, line 2 `[enter]-Actions  [n]ew  [p]rune  [P]rune-tracker`. No `q`.

- [ ] **Step 6:** Run `mise exec -- go test ./tui/...`. Expected: PASS.

---

### Task 2: The issue table reopens as it was left

**Files:**
- Modify: `tui/issue.go`, `cmd/issue/list.go`
- Test: `tui/issue_test.go`

**Interfaces:**
- Produces:
  - `type IssueTableOptions struct { Status, Project, Filter string; Mine bool; Cursor int }`
  - `func IssueTableModelWith(rows []issue.Row, opts IssueTableOptions, actions IssueActions) (tea.Model, error)`
  - `func IssueTableState(m tea.Model) IssueTableOptions` (zero value for a model that is not the issue table)
  - `IssueTableModel(rows, initialStatus, actions)` keeps its signature and calls `IssueTableModelWith(rows, IssueTableOptions{Status: initialStatus}, actions)`.
  - `issueListInfra.table *tui.IssueTableOptions`: nil on the first pass.

- [ ] **Step 1: Write the failing test** `TestIssueTableState` in `tui/issue_test.go` (rows spanning two projects, at least three open rows):
  - "a table rebuilt from its state is as it was left": press tab (closed), `m`, type `/` + text + enter, move down; `st := IssueTableState(m)`; `IssueTableModelWith(rows, st, a)` has the same `View()` as `m` and `IssueTableState` of it equals `st`.
  - "the project is kept": set the project through the `p` picker, rebuild, `IssueTableState(...).Project` is that project.
  - "a cursor past the rows is clamped": `IssueTableOptions{Status: "open", Cursor: 99}` → the table's cursor is the last row, enter/`s` acts on the last row.
  - "a project that is gone falls back to all": `Project: "nope"` → every project's rows show.
  - "an empty status is open": `IssueTableOptions{}` shows what `IssueTableModel(rows, "", a)` shows.
  - "a model that is not the table gives the zero state".

- [ ] **Step 2:** Run `mise exec -- go test ./tui/... -run TestIssueTableState -v`. Expected: FAIL, undefined.

- [ ] **Step 3: Implement** `IssueTableOptions`, `IssueTableModelWith`, `IssueTableState` in `tui/issue.go`, on the model of `BranchTableOptions` / `BranchTableState` (`tui/branch.go:63-87`): the constructor sets the status, the project (when in `projects`), mine and the filter text before the first `refreshRows`, then the cursor clamped to `len(visible)-1`.

- [ ] **Step 4:** Run the test. Expected: PASS.

- [ ] **Step 5: Wire the loop** in `cmd/issue/list.go`: `issueListRunE` sets `infra.table = new(tui.IssueTableOptions)` seeded with `Status: flags.status` before the loop; `runList` builds the table with `IssueTableModelWith(rows, *infra.table, actions)` when `infra.table != nil` (else `IssueTableOptions{Status: flags.status}`) and, after `Run`, stores `tui.IssueTableState(final)` back through the pointer. No test: `runList`'s table branch needs a terminal; the state itself is covered by Step 1.

- [ ] **Step 6:** Run `mise exec -- go test ./tui/... ./cmd/issue/...`. Expected: PASS.

---

### Task 3: Enter opens the issue's menu

**Files:**
- Modify: `tui/issue.go`
- Test: `tui/issue_test.go`

**Interfaces:**
- Consumes: `keyHints` (Task 1); `tui.Form`, `tui.MenuSelect(title string, opts []MenuOption, value *string) *huh.Group` (`tui/menu.go`).
- Produces:
  - `IssuePickComment`, appended after `IssuePickSync` in the `IssuePick` constants (existing values do not move).
  - `type issueMenuEntry string` with `issueEntryShow = "show"`, `issueEntryStart = "start"`, `issueEntryClose = "close (merge)"`, `issueEntryEdit = "edit"`, `issueEntryLabel = "label"`, `issueEntryComment = "comment"`, `issueEntryAbandon = "abandon (no undo)"`.
  - `func issueMenuEntries(row *issue.Row) []issueMenuEntry` (pure).

- [ ] **Step 1: Write the failing test** `TestIssueMenuEntries`, table-driven, one `t.Run` per case. With `open := row.MatchesStatus("open")`, `repo := row.State != ""`, `inProgress := row.Branch != nil && row.Branch.Status == branch.StatusInProgress`:

| Case | Row | Want |
|---|---|---|
| repository issue, open, no branch | `State: "open"` | show, start, edit, label, comment, abandon (no undo) |
| repository issue, open, branch in progress | `State: "open"`, branch in_progress | show, start, close (merge), edit, label, comment |
| repository issue, open, merged branch is no branch | `State: "open"`, branch merged | show, start, edit, label, comment, abandon (no undo) |
| repository issue, closed | `State: "closed"` | show, edit, label, comment |
| not a repository issue, open, no branch | no state, no branch | show, start |
| not a repository issue, branch in progress | no state, branch in_progress | show, start, close (merge) |
| not a repository issue, merged branch | no state, branch merged | show |
| not a repository issue, closed branch | no state, branch closed | show |

- [ ] **Step 2:** Run `mise exec -- go test ./tui/... -run TestIssueMenuEntries -v`. Expected: FAIL, undefined.

- [ ] **Step 3: Implement `issueMenuEntries`:** show always; start if `open`; close if `open && inProgress`; edit, label, comment if `repo`; abandon if `repo && open && !inProgress`; in that order.

- [ ] **Step 4:** Run it. Expected: PASS.

- [ ] **Step 5: Write the failing test** `TestIssueTable_Menu` (drive the nested huh form as `TestIssueTable_EditDialog` does for the edit form; rows: an open repository issue without a branch, one with a branch in progress, a closed one):
  - "enter opens the menu under the table": the view still holds the table's rows, holds `Issue <slug>: <title>` and `show`; the command does not quit.
  - "esc closes the menu and the table is as it was": same `View()` as before enter.
  - "start / close (merge) / label / comment quit with their pick and the row's slug": `PickedIssue` = `IssuePickRow` / `IssuePickClose` / `IssuePickLabel` / `IssuePickComment`.
  - "show opens the show dialog" (the `Show` text is in the view); "edit opens the edit form".
  - "abandon asks, no leaves the table": the view holds `Abandon <slug>? There is no undo.`; no → no quit, table back. "abandon, yes quits with IssuePickAbandon".
  - "enter in pick mode picks the row and opens no menu" (`IssueActions{Pick: true}`).
  - "the hint names the menu": the view holds `[enter]-Actions`, not `[enter]-Show`.
  - "without a shower the menu has no show", "without Load and Edit the menu has no edit" (the view of the open menu).
  - Review Focus 1: "enter on an empty table opens nothing" (filter text matching no row): no menu, no quit.
  - Review Focus 2: "a row with no entry opens nothing": non-repository row with a merged branch, `IssueActions{}`.
  - Review Focus 4: "q and a go to the menu, not the table": with the menu open, `q` does not quit and `a` shows no arming notice.

- [ ] **Step 6:** Run `mise exec -- go test ./tui/... -run TestIssueTable_Menu -v`. Expected: FAIL.

- [ ] **Step 7: Implement** in `tui/issue.go`:
  - Model fields: `formKind string` (`"edit"`, `"menu"`, `"abandon"`), `menuSlug string`, `menuPicked string`, `abandonOK bool`. `openEdit` sets `formKind = "edit"`.
  - `openMenu() tea.Cmd`: the row under the cursor; entries = `issueMenuEntries(row)` minus show when `actions.Show == nil`, minus edit when `Load` or `Edit` is nil; none left → nil. Form: `Form(MenuSelect("Issue "+row.IssueSlug+": "+row.Title, opts, &m.menuPicked)).WithWidth(m.dialog.Width)`.
  - `updateNormal` `"enter"`: pick mode unchanged; else `return m, m.openMenu(), true`.
  - `updateEditing` (rename to `updateForm`): on `StateCompleted`, by `formKind`: edit → `saveEdit` as today; menu → run the entry; abandon → `abandonOK` true quits through `pickRow(IssuePickAbandon)`, false closes.
  - Running an entry: show → `openDialog`; edit → `openEdit`; start → `pickRow(IssuePickRow)`; close → `pickRow(IssuePickClose)`; label → `pickRow(IssuePickLabel)`; comment → `pickRow(IssuePickComment)`; abandon → open `Form(huh.NewGroup(huh.NewConfirm().Title("Abandon "+slug+"? There is no undo.").Value(&m.abandonOK)))`, `abandonOK` reset to false first.
  - `View`: `formKind == "edit"` keeps today's dialog; menu and abandon draw `m.table.View() + "\n\n" + m.form.View()` (as `tui/branch.go` does for `S`/`T`).
  - `hint`: `[enter]-Actions` replaces `[enter]-Show`. Update the doc comments of `IssueTableModel`, `IssuePick`, `IssueActions`.

- [ ] **Step 8: Rewrite `TestIssueTable_EnterOpensDialog`** as "show from the menu opens the dialog" (enter, enter): its scroll/esc/`e` sub-cases keep their assertions; "without a shower enter does nothing" becomes the no-show-entry case of Step 5. Replace the remaining `[enter]-Show` assertions from Task 1 by `[enter]-Actions`.

- [ ] **Step 9:** Run `mise exec -- go test ./tui/...`. Expected: PASS, `TestIssueTable_ActionKeys` untouched and green.

---

### Task 4: The comment pick, the `list` Long, the docs

**Files:**
- Modify: `cmd/issue/list.go` (`listAction`, the Long of `getIssueListCmd`), `CLAUDE.md` ("Testing the menus")
- Test: `cmd/issue/record_ops_e2e_test.go` (`TestListAction`), `cmd/issue/issue_test.go`
- Regenerate: `website/docs/reference/commands/git_zf_issue_list.md`

**Interfaces:**
- Consumes: `tui.IssuePickComment` (Task 3); `runComment(ctx, client, p, args []string, message string, m *issuepkg.Mirror) error` (`cmd/issue/comment.go:46`).

- [ ] **Step 1: Write the failing tests:**
  - In `TestListAction`, next to the label case: "the comment pick runs the comment form on the row's issue and reopens" — the scripted prompter answers the comment form; afterwards the record holds the comment, `reopen` is true, `err` nil. "esc on the comment form reopens quietly" — the prompter returns `huh.ErrUserAborted`; `reopen` true, `err` nil, no comment.
  - `TestIssueListCmd_Long` in `cmd/issue/issue_test.go`, on the model of `TestBranchRootCmd_Long` (`cmd/branch/branch_test.go:219`): the Long of `list` contains `Enter opens the actions` and `comment`.

- [ ] **Step 2:** Run `mise exec -- go test ./cmd/issue/... -run "TestListAction|TestIssueListCmd_Long" -v`. Expected: FAIL.

- [ ] **Step 3: Implement:** `case tui.IssuePickComment: err = runComment(ctx, infra.client, p, []string{row.IssueSlug}, "", infra.mirror)` in `listAction`. Rewrite the `Keys:` paragraph of the Long: first "Enter opens the actions of the issue under the cursor: show, start, close, edit, label, comment, abandon, as its state allows.", then the keys as shortcuts (today's text minus "enter shows the issue"), then "After an action the table reopens as it was left."

- [ ] **Step 4:** Run the tests. Expected: PASS.

- [ ] **Step 5: Docs.** `make docs-gen`; check `git diff --stat website/` touches only `git_zf_issue_list.md` (and `git_zf_issue.md` if it embeds the Long). In `CLAUDE.md`, "Testing the menus": enter opens the row's menu (`issueMenuEntries`, `TestIssueMenuEntries`, `TestIssueTable_Menu`), the picks list gains comment, the table reopens from `tui.IssueTableState`.

- [ ] **Step 6:** Run `mise exec -- go test ./cmd/issue/... ./tui/...`. Expected: PASS.

---

### Task 5: Action keys in the branch table model

**Files:**
- Modify: `tui/branch.go`
- Test: `tui/branch_test.go`

**Interfaces:**
- Consumes: `keyHints` (Task 1).
- Produces:
  - `BranchPickKey`, appended after `BranchPickPruneTracker`.
  - `func PickedBranchKey(m tea.Model) string`: the action key of a `BranchPickKey` quit, else `""`. `PickedBranch` still returns the row.
  - `BranchTableOptions.Offers func(row branch.Row, key string) bool`: nil offers every key.
  - The action keys and their labels, one ordered table in `tui/branch.go`: `C` checkout, `c` close, `m` merge, `u` pull request, `a` abandon, `t` track, `l` log, `D` delete.

- [ ] **Step 1: Write the failing test** `TestBranchTable_ActionKeys` (`branchTestRows`, `runeKey`, `quitsCmd` exist):
  - one `t.Run` per key of `C c m u t l`: "X quits with the key and the row" — `quitsCmd`, `PickedBranch` = `BranchPickKey` and the row under the cursor, `PickedBranchKey` = the key.
  - "a once arms and shows the notice": no quit, the view holds `a again abandons <branch> (no undo)`. "a twice quits with a". Same pair for `D` with `D again deletes <branch>`.
  - "another key disarms": `a`, `j`, `k`, `a` → armed again, no quit.
  - Review Focus 5: "arming does not follow the cursor": `a`, down, `a` → no quit, the notice names the second row.
  - "a key the row does not offer shows the notice and stays": `Offers` returning false for `m` → no quit, `BranchPickNone`, the view holds `no merge for this branch`.
  - "a and D not offered are refused on the first press": `Offers` false for `a` → the view holds `no abandon for this branch`, and a second `a` still does not quit.
  - "the notice goes with the next key".
  - "an action key on an empty table does nothing" (`setStatuses([]string{})`, then `c`).
  - Review Focus 4: "D typed in the filter is text": `/`, `D`, `D` → no quit, the filter holds `DD`.
  - "d still scrolls": `d` does not quit and sets no notice.
  - "the view has the two key lines": line `[S]tatus  [T]ype  [R]emotes  [/]-Filter` and line `[enter]-Actions  [C]heckout  [c]lose  [m]erge  [u]-Pull request  [a]bandon  [t]rack  [l]og  [D]elete  [n]ew  [p]rune  [P]rune-tracker`.
  - "the state keeps Offers": `BranchTableState(m).Offers != nil` when it was set.

- [ ] **Step 2:** Run `mise exec -- go test ./tui/... -run TestBranchTable_ActionKeys -v`. Expected: FAIL.

- [ ] **Step 3: Implement** in `tui/branch.go`: model fields `notice string`, `armKey`, `armBranch string`, `pickedKey string`. In `Update`, on a key outside the form and the filter: clear the notice; disarm unless the key is the armed one. In `updateNormal`, for an action key: no row → handled, nothing; not offered → notice `no <label> for this branch`; `a` / `D` not armed on this branch → arm with the notice above; otherwise set `picked`, `pickedKey` and `quitWith(BranchPickKey)`. `View`: the action line built from the key table, the notice on its own line under it. Update the doc comment of `BranchTableModel`.

- [ ] **Step 4:** Run `mise exec -- go test ./tui/...`. Expected: PASS (`TestBranchTable_Rows` now asserts the full action line from Step 1; adjust its Task 1 assertion if it pinned line 2).

---

### Task 6: Wire the branch keys to the actions, the Long, the docs

**Files:**
- Modify: `cmd/branch/table.go` (`runBranchTable`, a key map), `cmd/branch/branch.go` (the Long, where the options are built), `CLAUDE.md` ("Testing the branch table")
- Test: `cmd/branch/table_test.go`, `cmd/branch/table_e2e_test.go`, `cmd/branch/branch_test.go` (`TestBranchRootCmd_Long`)
- Regenerate: `website/docs/reference/commands/git_zf_branch.md`

**Interfaces:**
- Consumes: `tui.BranchPickKey`, `tui.PickedBranchKey`, `BranchTableOptions.Offers` (Task 5); `offeredEntries(d, row)`, `runBranchAction(ctx, d, p, a, row)` (existing).
- Produces:
  - `var keyActions = map[string]branchAction{"C": actionCheckout, "c": actionIssueClose, "m": actionMerge, "u": actionPR, "a": actionAbandon, "t": actionTrack, "l": actionLog, "D": actionDelete}`
  - `func offersKey(d tableDeps, row branch.Row, key string) bool`
  - `func runKey(ctx context.Context, d tableDeps, p BranchMenuPrompter, key string, row branch.Row) (reopen bool, err error)`

- [ ] **Step 1: Write the failing tests:**
  - `TestKeyActions` in `cmd/branch/table_test.go`: "every menu entry has one key" (the values of `keyActions` are exactly the eight `branchAction` constants); `TestOffersKey`: for the rows of `TestMenuEntriesFor`, `offersKey(d, row, key)` is true exactly when `keyActions[key]` is in `offeredEntries(d, row)`, including "a nil func removes its key" (`d.merge = nil` → `m` not offered) and "an unknown key is not offered".
  - `TestRunKey` in `cmd/branch/table_e2e_test.go`, on the rig of `TestRunBranchAction`: one `t.Run` per key asserting the same effect and `reopen` as the matching `runBranchAction` case (`C` checks out and does not reopen; `c` calls `closeIssue` with the row's slug and branch and does not reopen; `m`, `u`, `t`, `l` call their func and reopen; `a` records the branch closed; `D` deletes the branch); "a key the row does not offer runs nothing" (returns `reopen == true`, an error naming the key — the model normally filters it before).
  - `TestBranchRootCmd_Long`: the Long contains `C checks out`, `D twice deletes`, `u opens a pull request`.

- [ ] **Step 2:** Run `mise exec -- go test ./cmd/branch/... -run "TestKeyActions|TestOffersKey|TestRunKey|TestBranchRootCmd_Long" -v`. Expected: FAIL.

- [ ] **Step 3: Implement:** `keyActions`, `offersKey` (lookup, then membership in `offeredEntries`), `runKey` (refuse when not offered, else `runBranchAction`). In `runBranchTable`: before the loop `opts.Offers = func(row branch.Row, key string) bool { return offersKey(d, row, key) }` (it survives `BranchTableState`); a `case tui.BranchPickKey:` calling `runKey(ctx, d, p, tui.PickedBranchKey(m), *row)`. Extend the Long of the branch root: after the enter sentence, the shortcut keys (`C` checks out, `c` closes the issue by merging, `m` merges into the current branch, `u` opens a pull request, `a` twice abandons, `t` tracks, `l` shows the log, `D` twice deletes), and that a key that does not apply to the row says so.

- [ ] **Step 4:** Run the tests. Expected: PASS.

- [ ] **Step 5: Docs.** `make docs-gen`; `git diff --stat website/` touches only the branch command page(s). `CLAUDE.md`, "Testing the branch table": the keys (`keyActions`, `offersKey`, `runKey`, `TestBranchTable_ActionKeys`), with the test regexps extended by `^TestKeyActions|^TestOffersKey|^TestRunKey`.

- [ ] **Step 6: Full run.** `mise exec -- go test ./...`, then `make`. Expected: PASS, binary built. Then GitNexus `detect_changes({scope: "all"})` and report.

- [ ] **Step 7: Hand to the user for the terminal smoke** (no TTY in tests): `./bin/git-zf issue` — enter, each entry, esc, a filter kept after a comment; `./bin/git-zf branch` — the two lines, `C`, `a a`, `D D`, a key on a row that refuses it, `d` scrolling.
