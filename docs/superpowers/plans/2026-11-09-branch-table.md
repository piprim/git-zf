# Branch Table Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `git zf branch` opens a table of every local branch (tracked and plain) with filters and a per-row action menu, replacing the subcommand menu.

**Architecture:** `branch.TableRows` merges git's branch list with the tracked records into one `branch.Row` per branch. `tui.BranchTableModel` shows them with S/T/R filters and quits with a `BranchPick`; `cmd/branch/table.go` loops table → menu → action like `cmd/issue/list.go` does, dispatching to the existing commands (`runCloseBranch`, `runMerge`, `runPr`, `runPrune`, `review.TrackBranch`, `issue.CloseIssue`, `log.RunLog`). No new state: the records stay in `refs/zf/branches`.

**Tech Stack:** Go (mise), cobra v1.1.3, bubbletea, bubbles/table, huh, lipgloss/table. Tests: real on-disk repos, scripted prompters, `t.Run` per assertion.

**Spec:** `docs/superpowers/specs/2026-11-08-branch-table-design.md`

**Commits:** the user commits themselves (on `wip`). No commit steps below; stop at the end of each task with tests green and say what changed.

## Global Constraints

- Run Go through mise: `mise exec -- go test ./...`, `mise exec -- go build -o ./bin/git-zf .`.
- Every test assertion sits in its own `t.Run("...")`.
- Forms the table opens are built with `tui.Form` (esc aborts), never `huh.NewForm`.
- A subcommand run from the table reads string flags with `cmdutil.StringFlag`, never `cmd.Flags().GetString`.
- The table never fetches or reconciles (`branch.TableRows` calls `branch.List`, not `ListRows`). Actions fetch themselves.
- `Row.BranchName` is always the short name; `<remote>/` is display only.
- Seed tracked branches in tests with `branchtest.Seed`, never by writing refs.
- `branch list --json`, `--stdout` and its no-TTY fallback keep `ListRows` + `tty.RenderBranchTable` (tracked rows only).
- Run `make docs` after changing a cobra `Long` (the reference pages under `website/docs/reference/commands/` are generated).
- Before editing a function, run the GitNexus impact check the project CLAUDE.md requires (`node .gitnexus/run.cjs impact "<symbol>" --direction upstream --repo .`); before the user commits, `node .gitnexus/run.cjs detect-changes --scope all --repo .`.

## Review Focus

Inputs the spec implies but no spec test names. Each has its test added to the owning task.

1. A detached HEAD: `CurrentBranch` returns "HEAD" or ""; no row is marked current and every row keeps checkout. (Task 1)
2. A tracked record whose branch exists neither locally nor on the remote: no row (the record is prune's business). (Task 1)
3. A remote-only branch whose name also exists locally: one local row, not two. (Task 1)
4. The S filter with nothing checked: the table shows "No branch matches." and the footer still shows the keys, `S` reopens the picker. (Task 2)
5. `trackPlain` when the subject slugs to a name already taken by another branch: nothing renamed, nothing recorded, no issue published. (Task 5)

---

### Task 1: `branch.TableRows`

**Files:**
- Modify: `branch/record.go` (the `Row` struct, line 162)
- Create: `branch/table.go`
- Modify: `git/git.go` (after `RemoteBranchNames`, line 751)
- Test: `branch/table_test.go`, `git/git_test.go`

**Interfaces:**
- Consumes: `branch.List(ctx, c)`, `c.LocalBranchNames()`, `c.RemoteBranchNames()`, `c.CurrentBranch()`, `c.Remote()`, `TitleFromName`.
- Produces:
  - `branch.Row` gains `Plain bool`, `Current bool`, `RemoteOnly bool`, `LastCommit time.Time` (all `json:"-"`).
  - `func TableRows(ctx context.Context, c *git.Client, remote string) ([]Row, error)` in `branch/table.go`: one row per local branch in name order, then one per remote-only branch (a name not local) in name order. A tracked branch takes its `Rows` fields; a plain one has `Plain: true`, empty IssueSlug/IssueID/Type/Status/Title/Author, zero CreatedAt. `Current` is set on the row equal to `CurrentBranch()` when that is neither "" nor "HEAD". Records whose branch is in neither list produce no row. `remote` is the name the caller got from `c.Remote()`; "" skips the remote list.
  - `func (c *Client) CommitterDates(ctx context.Context, refPrefixes ...string) (map[string]time.Time, error)` in `git/git.go`: one `git for-each-ref --format=%(refname) %(committerdate:unix) <prefixes...>` call, map keyed by full refname. `TableRows` reads `refs/heads/` and `refs/remotes/<remote>/` and fills `LastCommit` (`refs/heads/<name>` for local rows, `refs/remotes/<remote>/<name>` for remote-only ones).

- [ ] **Step 1: Write the failing git test** in `git/git_test.go`

```go
func TestCommitterDates(t *testing.T) {
	// rig: a repo with master and one branch "feat-x" one commit later (reuse the file's existing repo helper).
	dates, err := c.CommitterDates(ctx, "refs/heads/")
	t.Run("returns no error", ...)
	t.Run("has one entry per local branch", func(t *testing.T) { len(dates) == 2 })
	t.Run("keys are full refnames", func(t *testing.T) { _, ok := dates["refs/heads/feat-x"] })
	t.Run("feat-x is not before master", func(t *testing.T) { !dates["refs/heads/feat-x"].Before(dates["refs/heads/master"]) })
	t.Run("an unknown prefix gives an empty map and no error", ...)
}
```

- [ ] **Step 2: Run it, expect a compile failure** (`CommitterDates` undefined)

`mise exec -- go test ./git/... -run TestCommitterDates -v`

- [ ] **Step 3: Implement `CommitterDates`** with `c.output`; parse each line as `<refname> <unix>`; `strconv.ParseInt` → `time.Unix(n, 0)`.

- [ ] **Step 4: Run the git test, expect PASS**

- [ ] **Step 5: Write the failing `TableRows` test** in `branch/table_test.go`. Rig: copy the pattern of `branch/repo_test.go` (temp repo, `git.NewClientAt`), with a bare origin added before the client is created. Seed with `branchtest.Seed`: `42@feat@assign` in progress, `51@fix@prune` merged (both as real local branches too), one tracked record `77@feat@gone` with no git branch anywhere, a plain local branch `spike`, and a remote-only branch `remote-thing` pushed to origin then deleted locally, plus `spike` also pushed (exists locally and remotely).

```go
func TestTableRows(t *testing.T) {
	rows, err := branch.TableRows(ctx, c, "origin")
	t.Run("no error", ...)
	t.Run("one row per local branch in name order, then remote-only", func(t *testing.T) {
		names := []string{"42@feat@assign", "51@fix@prune", "master", "spike", "remote-thing"}  // compare BranchName sequence
	})
	t.Run("a tracked row carries its record", func(t *testing.T) { row("42@feat@assign").IssueSlug == "42" && Status == branch.StatusInProgress && !Plain })
	t.Run("a plain row is Plain with empty record fields", func(t *testing.T) { row("spike").Plain && IssueSlug == "" && Status == "" && CreatedAt.IsZero() })
	t.Run("the checked out branch is Current", func(t *testing.T) { row("master").Current && !row("spike").Current })
	t.Run("a record whose branch is gone everywhere has no row", func(t *testing.T) { no row named "77@feat@gone" })
	t.Run("a branch both local and remote is one local row", func(t *testing.T) { count("spike") == 1 && !row("spike").RemoteOnly })
	t.Run("a remote-only branch is RemoteOnly with its short name", func(t *testing.T) { row("remote-thing").RemoteOnly && BranchName == "remote-thing" })
	t.Run("every row has a LastCommit", func(t *testing.T) { none zero })
	t.Run("an empty remote skips the remote list", func(t *testing.T) { rows2, _ := TableRows(ctx, c, ""); no RemoteOnly row })
	t.Run("a detached HEAD marks no row current", func(t *testing.T) { git checkout --detach; rerun; no Current })
}
```

- [ ] **Step 6: Run it, expect a compile failure**

`mise exec -- go test ./branch/... -run TestTableRows -v`

- [ ] **Step 7: Add the four `Row` fields and implement `TableRows`.** Build `map[name]Row` from `Rows(states, StatusAll)` (first entry per name wins); iterate `LocalBranchNames()` (sort them), then `RemoteBranchNames()` minus the local set.

- [ ] **Step 8: Run `./branch/...` and `./git/...`, expect PASS** (the JSON tests of `TestBranchList` must still pass: new fields are `json:"-"`).

---

### Task 2: `tui.BranchTableModel`

**Files:**
- Modify: `tui/branch.go` (replace `BranchTableModel`, `branchTableModel`, delete `BranchStatusFilter`)
- Test: `tui/branch_test.go`

**Interfaces:**
- Consumes: `branch.Row` from Task 1, `tui.Form`, `NewForm`, `dialogStyle`, `activeTabStyle`, `inactiveTabStyle` (all in package `tui`).
- Produces:
  ```go
  type BranchPick int
  const (
      BranchPickNone BranchPick = iota // q, ctrl+c
      BranchPickRow                    // enter: the row under the cursor
      BranchPickNew                    // n
      BranchPickPrune                  // p
      BranchPickPruneTracker           // P
  )
  // BranchStatusPlain is the S option for rows with no record.
  const BranchStatusPlain = "plain"
  // BranchTypeNone is the T option for rows with no record.
  const BranchTypeNone = "none"
  type BranchTableOptions struct {
      Types    []string // the T options, from cfg.CommitTypes; "none" is appended by the model
      Statuses []string // S options checked at start; nil = all four
      Remote   string   // shown before a RemoteOnly row's name; "" hides the R key
  }
  func BranchTableModel(rows []branch.Row, opts BranchTableOptions) tea.Model
  func PickedBranch(m tea.Model) (BranchPick, *branch.Row)  // the row is a copy of the model's row
  ```
  Columns in order: Branch (`* ` prefix when Current, `<remote>/` prefix when RemoteOnly), Issue, Type, Status, Title, Created (`2006-01-02`, blank when zero), Last commit (relative, `tui.relativeTime(t, now)` producing "2h ago", "1d ago", "3w ago", "5mo ago", "2y ago"; blank when zero).
  Keys: `S` status multi-select (plain, in_progress, merged, closed), `T` type multi-select (Types + "none"), `R` toggle RemoteOnly rows (off at start), `/` text filter (exact behaviour of the issue table's `/`), `enter` → `BranchPickRow` + quit, `n`/`p`/`P` → their pick + quit, `q`/ctrl+c → `BranchPickNone` + quit, `j`/`k`/arrows move.
  Footer: a line `S status  T type  R remotes  / filter  enter actions  n new  p prune  P prune-tracker  q quit`, and above it the active filters `status: …  type: …  remotes: on` only when one differs from the default. An empty filtered table shows `No branch matches.` in place of the rows.
  Row matching: status = `BranchStatusPlain` when `Plain`, else `Status`; type = `BranchTypeNone` when `Plain`, else `Type`. Both filters are sets of checked values.
  Nested forms: `S`/`T` set `m.form = Form(huh.NewGroup(huh.NewMultiSelect[string]()...))` and the view renders `table.View() + "\n\n" + form.View()`; `huh.StateCompleted` applies, `StateAborted`/esc keeps the previous set (copy the `updateEditing` pattern of `tui/issue.go:433-455`).

- [ ] **Step 1: Write the failing tests** in `tui/branch_test.go` (keep the two existing tests). Helper rows: `master` current, `42@feat@assign` in_progress feat, `51@fix@prune` merged fix, `spike` plain, `remote-thing` RemoteOnly plain.

```go
func TestRelativeTime(t *testing.T)  // table: 2h, 1d, 3w, 5mo, 2y, zero → ""
func TestBranchTable_Rows(t *testing.T) {
	m := BranchTableModel(rows, BranchTableOptions{Types: []string{"feat","fix"}, Remote: "origin"})
	t.Run("remote-only rows are hidden at start", ...)       // View() lacks "remote-thing"
	t.Run("R shows them with the remote prefix", ...)         // after 'R': contains "origin/remote-thing"
	t.Run("the current branch is starred", ...)               // contains "* master"
	t.Run("a plain row has blank Created", ...)
	t.Run("the footer lists the keys", ...)                   // contains "S status" and "P prune-tracker"
}
func TestBranchTable_Filters(t *testing.T) {
	t.Run("/ narrows by text", ...)                            // '/', "spi", enter → only spike visible
	t.Run("S opens a form rendered under the table", ...)      // 'S' → View() contains both "Branch" header and "in_progress" option
	t.Run("esc on the S form keeps the previous statuses", ...)
	t.Run("statuses applied from options hide the others", ...) // Statuses: []string{"merged"} → only 51@fix@prune
	t.Run("plain is a status", ...)                             // Statuses: {"plain"} → only spike (and master)
	t.Run("none is a type", ...)                                // Types form: pick only "none" via the applied set helper → spike, master
	t.Run("no status checked shows No branch matches and the footer", ...)
	t.Run("active filters are named above the footer", ...)     // contains "status: merged"
}
func TestBranchTable_Picks(t *testing.T) {
	t.Run("enter picks the row under the cursor", ...)         // PickedBranch → BranchPickRow, row.BranchName == rows[0].BranchName
	t.Run("n picks new", ...) t.Run("p picks prune", ...) t.Run("P picks prune-tracker", ...)
	t.Run("q picks nothing", ...)
	t.Run("enter on an empty table picks nothing and does not quit", ...)
}
```
To apply a multi-select in a test without driving huh, expose `func (m *branchTableModel) setStatuses(s []string)` / `setTypes` (unexported, same package) and call them; the form test only checks the form opens and esc cancels.

- [ ] **Step 2: Run, expect compile failures**

`mise exec -- go test ./tui/... -run "TestRelativeTime|TestBranchTable_" -v`

- [ ] **Step 3: Implement** `relativeTime`, the model (`allRows`, `visible`, `table`, `filter textinput`, `filtering`, `statuses map[string]bool`, `types map[string]bool`, `remotes bool`, `form *huh.Form`, `formKind` ("S"/"T"), `pending []string` the form writes into, `pick`, `picked *branch.Row`, `opts`), `refreshRows`, `Update`, `View`. Delete `BranchStatusFilter` and its constants that nothing else uses (grep first).

- [ ] **Step 4: Run `./tui/...`, expect PASS**

---

### Task 3: the table loop in `cmd/branch`

**Files:**
- Create: `cmd/branch/table.go`, `cmd/branch/table_prompter.go`
- Modify: `cmd/branch/branch.go` (`GetRootCmd` RunE line 65, `runList` TUI path lines 143-171, `listCmd` Long)
- Create: `tty/branch_rows.go` (`RenderBranchRows`)
- Test: `cmd/branch/table_test.go` (pure), `cmd/branch/table_e2e_test.go`, `tty/branch_test.go`

**Interfaces:**
- Consumes: Task 1 `branch.TableRows`, Task 2 `tui.BranchTableModel`/`PickedBranch`/`BranchTableOptions`, existing `runCloseBranch`, `runPr`, `runPrune`, `runPruneTracker`, `issueflow.RunIssueStart`, `client.Checkout`, `client.Worktrees`, `client.DeleteLocalBranch`, `git.ErrBranchNotMerged`.
- Produces:
  ```go
  // table.go
  type branchAction string
  const (
      actionCheckout   branchAction = "checkout"
      actionIssueClose branchAction = "issue close (merge)"
      actionMerge      branchAction = "merge into current"
      actionPR         branchAction = "pull request"
      actionAbandon    branchAction = "abandon (record closed, no merge)"
      actionTrack      branchAction = "track"
      actionLog        branchAction = "log"
      actionDelete     branchAction = "delete"
  )
  // menuEntriesFor is pure: RemoteOnly first, then Current, then the record.
  func menuEntriesFor(row branch.Row) []branchAction
  type tableDeps struct {
      client     *git.Client
      cfg        *config.AppConfig
      stdout     io.Writer
      stderr     io.Writer
      // the actions other tasks wire; nil disables the entry (it is not offered)
      merge      func(ctx context.Context, source SourceBranch) error             // Task 4
      track      func(ctx context.Context, name string) error                     // Task 5
      closeIssue func(ctx context.Context, issueSlug, branchName string) error    // Task 6
      log        func(ctx context.Context, branchName string) error               // Task 7
      pr         func(ctx context.Context, branchName string) error
      newBranch  func(ctx context.Context) error
      prune      func(ctx context.Context) error
      pruneTracker func(ctx context.Context) error
  }
  // runBranchTable loops table → menu → action until the table quits or an
  // action that moves HEAD runs. stdout=true prints the rows (no TTY).
  func runBranchTable(ctx context.Context, d tableDeps, p BranchMenuPrompter, opts tui.BranchTableOptions, stdout bool) error
  // branchAction runs one entry; reopen is false for checkout, issue close and new.
  func runBranchAction(ctx context.Context, d tableDeps, p BranchMenuPrompter, a branchAction, row branch.Row) (reopen bool, err error)
  func checkoutBranch(ctx context.Context, c *git.Client, name string) error     // wt, _ := c.HoldingWorktree(ctx, name): nil → `git checkout`; wt.Prunable → "branch %q is held by a stale worktree entry (%s): run `git worktree prune`"; !git.SamePath(wt.Path, c.WorkingTreeRoot()) → "branch %q is checked out in worktree %s" (same path = the current branch, already without a checkout entry); mirrors mergeflow.SourceTree
  func deleteBranch(ctx context.Context, c *git.Client, p BranchMenuPrompter, row branch.Row) error
      // after a successful delete of a row with Status == branch.StatusInProgress, prints to c.IO().Out:
      // "Branch %q deleted. Its record is still in progress: p (prune) records what happened to it (closed, or merged when its tip is in the base)."
      // The record itself is untouched: a local delete does not say whether a teammate still works on the branch; prune checks the remote.

  // table_prompter.go
  type BranchMenuPrompter interface {
      PickAction(ctx context.Context, title string, entries []branchAction) (branchAction, error) // huh.ErrUserAborted on esc
      ConfirmForceDelete(ctx context.Context, name string) (bool, error)
  }
  type huhBranchMenuPrompter struct{}   // tui.Form(tui.MenuSelect(title, opts, &picked))
  ```
  ```go
  // tty/branch_rows.go
  func RenderBranchRows(w io.Writer, rows []branch.Row, remote string)  // seven headers: "BRANCH", "ISSUE", "TYPE", "STATUS", "TITLE", "CREATED", "LAST COMMIT"; the BRANCH cell is "* <name>" for Current, "  <name>" otherwise (as tui.BranchTableModel renders it); blank zero dates; remote-only names prefixed
  ```
  Entry table (the spec's, with Task 1's fields):
  - RemoteOnly → checkout, log
  - Plain → checkout, track, merge into current, pull request, log, delete; `merge into current` is dropped when `branch.Parse(row.BranchName)` succeeds (an untracked `<id>@<type>@<slug>` branch: `runMerge` refuses issue-looking names).
  - in_progress → checkout, issue close (merge), pull request, abandon, log, delete
  - merged → checkout, log, delete
  - closed → checkout, track, log, delete
  - Current removes checkout, merge into current, delete.
  Entries whose `tableDeps` func is nil are removed from the offered list in `runBranchTable` (so Task 3 ships with merge/track/issue close/log absent until wired).

  `GetRootCmd`: RunE builds `tableDeps` with the production funcs (`pr` → `runPr` with the `prDeps` of `prCmd`'s RunE extracted into `func (b Branch) prDepsFor(cmd) (prDeps, error)`; `prune` → `runPrune(ctx, os.Stdout, c, &huhPrunePrompter{}, pruneFlags{})`; `pruneTracker` → `runPruneTracker` as `pruneTrackerRunE` builds it, error when no tracker configured; `newBranch` → `b.newRunE(cmd, "")`), `opts.Types` from `cfg.CommitTypes` names, `opts.Remote` from `c.Remote()`, `stdout = !term.IsTerminal(int(os.Stdin.Fd()))`. `runList`'s TUI path calls the same with `opts.Statuses` from `--status` (`in_progress|merged|closed` → that one; "" or "all" → nil) and the status form deleted.

- [ ] **Step 1: Write the pure test** `cmd/branch/table_test.go`

```go
func TestMenuEntriesFor(t *testing.T) {
	// table rows: kind → want []branchAction, exact order, covering the five kinds and each with Current=true
	// plus "plain row with a parseable name omits merge": Row{BranchName: "42@feat@untracked", Plain: true} has no actionMerge
}
```

- [ ] **Step 2: Write the E2E test** `cmd/branch/table_e2e_test.go` on `newPruneRigWithOrigin(t, newPruneOrigin(t))` plus: `createGitBranch(t, "spike")` (plain, unmerged), `seedIssueAndBranch(t, "42", "42@feat@assign", "feat")` + `createGitBranch`, `mergeBranchIntoMaster(t, "51@fix@prune", ...)` seeded merged, a remote-only branch (push `remote-thing`, delete locally). A `scriptedBranchMenuPrompter{Actions []branchAction; Force bool; Titles []string}` returning the next action each call. The table itself cannot run (no TTY): drive `runBranchAction` directly and `runBranchTable(..., stdout=true)` for the print path.

```go
func TestRunBranchAction(t *testing.T) {
	t.Run("checkout switches to a plain branch and does not reopen", ...)        // reopen false, CurrentBranch()=="spike"
	t.Run("checkout refuses a branch held by a worktree", ...)                  // git worktree add; error contains "worktree"
	t.Run("checkout of a remote-only branch creates it with an upstream", ...)  // git rev-parse --abbrev-ref remote-thing@{upstream} == origin/remote-thing
	t.Run("delete removes a merged branch", ...)                                 // reopen true, branch gone
	t.Run("delete of an unmerged branch asks and force-deletes on yes", ...)     // Force true → gone
	t.Run("delete of an unmerged branch keeps it on no", ...)
	t.Run("delete of a worktree-held branch does not ask", ...)                  // prompter.ForceAsked false, error returned
	t.Run("delete of an in-progress tracked branch keeps its record and names prune", ...) // statusOf == in_progress; stdout contains "still in progress" and "prune"
	t.Run("delete of a plain branch prints no prune reminder", ...)
	t.Run("abandon records the row closed without merging", ...)                 // statusOf == closed, branch exists
	t.Run("new, prune and prune-tracker call their funcs", ...)                   // recorder funcs; new reopen false, others true
	t.Run("pr calls its func with the branch", ...)
	t.Run("an entry with no func is not offered", ...)                            // runBranchTable offered list via the prompter's recorded entries: build with nil track → "track" absent; this needs the menu step, so test offeredEntries(d, row) helper directly
}
func TestRunBranchTable_NoTTY(t *testing.T) {
	t.Run("prints every local branch with the seven columns", ...)   // contains "BRANCH", "LAST COMMIT", "spike", "42@feat@assign", "* " before master
	t.Run("does not print remote-only rows", ...)
	t.Run("a plain row has a blank Created cell", ...)              // the spike line lacks "0001-01-01"
}
```

- [ ] **Step 3: Write `tty/branch_test.go`'s `TestRenderBranchRows`** (3 subtests: header, star, blank date) and run all three files, expect compile failures.

`mise exec -- go test ./cmd/branch/... ./tty/... -run "TestMenuEntriesFor|TestRunBranchAction|TestRunBranchTable_|TestRenderBranchRows" -v`

- [ ] **Step 4: Implement** `menuEntriesFor`, `offeredEntries(d tableDeps, row) []branchAction` (drops nil-func entries), `runBranchTable` (loop: rows ← `branch.TableRows(ctx, d.client, remote)`; no rows → print `No branches found.`; model → `tea.NewProgram(m, tea.WithContext(ctx)).Run()`; pick switch; `BranchPickRow` → `p.PickAction(ctx, row.BranchName, offered)`; `huh.ErrUserAborted` → reopen; action error → `fmt.Fprintln(d.stderr, "error:", err)` and reopen), `runBranchAction`, `checkoutBranch`, `deleteBranch`, `huhBranchMenuPrompter`, `RenderBranchRows`, and the `branch.go` wiring (`GetRootCmd` RunE, `runList`, delete the `BranchStatusFilter` call).

- [ ] **Step 5: Run `./cmd/branch/... ./tty/... ./tui/...`, expect PASS.** `TestBranchList` JSON subtests must still pass. `cmd/root_test.go` `TestGetRootCmd_menu` must still pass (the root menu still lists "branch").

- [ ] **Step 6: Build and smoke** `mise exec -- go build -o ./bin/git-zf . && echo | ./bin/git-zf branch` prints the rows of this repo.

---

### Task 4: merge into current

**Files:**
- Modify: `cmd/branch/merge.go:72-100` (`runMerge`)
- Modify: `cmd/branch/branch.go` (wire `tableDeps.merge`)
- Test: `cmd/branch/merge_e2e_test.go`, `cmd/branch/table_e2e_test.go`

**Interfaces:**
- Produces: `func runMergeSource(ctx context.Context, d mergeDeps, prompter MergePrompter, target string, source SourceBranch) error`, the body of `runMerge` from the `branch.Parse` refusal on. `runMerge` becomes: resolve target, `collectSources`, `PickSource`, `runMergeSource`.
- Wiring: `tableDeps.merge = func(ctx, source) error { target, err := c.CurrentBranch(); …; return runMergeSource(ctx, d, newHuhMergePrompter(c, cfg), target, source) }` with `mergeDeps` built as `mergeRunE` builds it (extract `func (b Branch) mergeDepsFor(cmd) mergeDeps`). The table passes `SourceBranch{Name: row.BranchName, RemoteOnly: row.RemoteOnly}`.

- [ ] **Step 1: Add to `merge_e2e_test.go`**

```go
func TestRunMergeSource_SkipsThePicker(t *testing.T) {
	// rig as TestRunMerge_HappyPath_Squash, prompter with PickSource returning an error
	t.Run("merges the given source without PickSource", ...)   // headSubject(master) is the squash subject; prompter.PickSourceCalled false
	t.Run("refuses an issue branch source", ...)              // source "7@feat@x" → nil error, stdout contains "issue close"
}
```
and to `table_e2e_test.go` `TestRunBranchAction`: `t.Run("merge into current merges the plain row", ...)` through a `tableDeps.merge` recorder asserting the `SourceBranch` received.

- [ ] **Step 2: Run, expect compile failure.** `mise exec -- go test ./cmd/branch/... -run "TestRunMergeSource|TestRunBranchAction" -v`

- [ ] **Step 3: Extract `runMergeSource`; wire it.**

- [ ] **Step 4: Run the whole `TestRunMerge_` suite and the table tests, expect PASS.**

---

### Task 5: track, including a free-named branch

**Files:**
- Modify: `cmd/review/track.go:38-56` (`runTrack` → `TrackBranch`)
- Modify: `cmd/issueflow/start_prompter.go:25,101` (`PickIssueFromUser` subject), `cmd/issueflow/start.go:224`, `cmd/issue/start_prompter_test.go:62`
- Modify: `git/git.go` (`RenameBranch`), `branch/record.go` (`TitleFromName` fallback)
- Create: `cmd/branch/track.go`
- Test: `cmd/review/track_test.go` (or the existing review E2E file), `git/git_test.go`, `branch/record_test.go`, `cmd/branch/track_e2e_test.go`

**Interfaces:**
- Produces:
  ```go
  // cmd/review
  var ErrNotTrackable = errors.New("branch name is not <id>@<type>@<slug> or <slug>@review")
  func TrackBranch(ctx context.Context, cfg *config.AppConfig, client *git.Client, name string) error
      // IsReviewBranch → runTrackReviewer; Parse ok → runTrackDeveloper; else ErrNotTrackable. reviewDeps built as buildReviewDeps does but from cfg+client (extract `newReviewDeps(client, cfg) reviewDeps` that sets tracker like buildReviewDeps; push fields zero).
  // runTrack calls TrackBranch with the current branch.

  // cmd/issueflow
  PickIssueFromUser(ctx context.Context, id, subject string, allowedTypes []string) (*issuepkg.Issue, error)
      // huh: got.ID = id; got.Subject = subject

  // git
  func (c *Client) RenameBranch(ctx context.Context, oldName, newName string) error   // git branch -m old new

  // branch
  func TitleFromName(name string) string  // unchanged for a parseable name; otherwise the name with "-" and "_" replaced by " " (no longer "")

  // cmd/branch/track.go
  func trackBranch(ctx context.Context, d tableDeps, sp issueflow.StartPrompter, name string) error
      // err := review.TrackBranch(...); errors.Is(err, review.ErrNotTrackable) → trackPlain; else return err
  func trackPlain(ctx context.Context, c *git.Client, cfg *config.AppConfig, sp issueflow.StartPrompter, name string) error
  ```
  `trackPlain`, in order: (1) `c.RemoteBranchExists(ctx, name)` → `fmt.Errorf("branch %q exists on %s: delete %s/%s first or keep the branch plain", ...)`; (2) `types` from `cfg.CommitTypes` names; `got, err := sp.PickIssueFromUser(ctx, "", branch.TitleFromName(name), types)`; nil `got` → return nil; (3) `got.ID == ""` → `recID, _ := issue.Prepare(ctx, c, issue.NewIssue{Title: got.Subject, BranchType: got.Type})`, slug = `(&issue.Record{ID: recID}).DisplayID()`, else slug = `got.ID`, recID = ""; (4) `b, _ := branch.New(slug, got.Type, got.Subject, "")`; `c.BranchExists(b.Name())` → `fmt.Errorf("branch %q already exists", ...)` before any change; (5) `c.RenameBranch(ctx, name, b.Name())`; (6) if recID != "": `issue.Publish` then `issue.Push` (warnings on error, as `publishRepoIssue`); (7) `branch.Start(ctx, c, slug, &branch.Op{Branch: b.Name(), BranchType: got.Type, Title: got.Subject, IssueID: recID})`, `branch.Push` (warning); print `Branch %q renamed to %q and tracked (issue %s).`
  `TitleFromName` callers: `Rows` uses it inside `cmp.Or(st.Title, TitleFromName(e.Name), st.Slug)` for tracked branches only, so the fallback changes nothing there; grep the other callers and confirm they pass parseable names.
  Wiring: `tableDeps.track = func(ctx, name) error { return trackBranch(ctx, d, issueflow.NewHuhStartPrompter(), name) }`. `huh.ErrUserAborted` from the form reaches `runBranchTable`, which reopens quietly.

- [ ] **Step 1: Failing tests**

`branch/record_test.go`:
```go
func TestTitleFromName_PlainName(t *testing.T) {
	t.Run("dashes and underscores become spaces", func(t *testing.T) { TitleFromName("spike_log-colors") == "spike log colors" })
	t.Run("a parseable name still gives its slug", func(t *testing.T) { TitleFromName("42@feat@add-login") == "add login" })
}
```
`git/git_test.go`: `TestRenameBranch` (renames; a checked-out branch is renamed and HEAD follows; renaming onto an existing name errors).
`cmd/review/*_test.go`: `TestTrackBranch` (review name → reviewer path error message as `runTrackReviewer` gives without a review; parseable name → recorded in progress; `spike` → `errors.Is(err, ErrNotTrackable)`).
`cmd/issue/start_prompter_test.go`: `scriptedStartPrompter.PickIssueFromUser` records `OfferedSubject`.
`cmd/branch/track_e2e_test.go` on the prune rig with origin, a `scriptedStartPrompter`-like `scriptedTrackPrompter` implementing `issueflow.StartPrompter` (only `PickIssueFromUser` matters; the others return zero values):
```go
func TestTrackPlain(t *testing.T) {
	t.Run("renames with a typed ID and records the branch", ...)      // spike → "X7@feat@spike"; statusOf == in_progress; IssueSlug == "X7"; old name gone
	t.Run("prefills the subject from the name", ...)                  // OfferedSubject == "spike log colors" for spike-log-colors
	t.Run("an empty ID mints a repo issue linked to the branch", ...) // issue.List has one record titled got.Subject; the row's IssueID == that record's ID; name starts with its DisplayID
	t.Run("a branch on the remote is refused untouched", ...)         // push spike first; error contains "exists on origin"; still named spike; no record
	t.Run("a name clash changes nothing and publishes no issue", ...) // existing "X7@feat@spike"; error contains "already exists"; issue.List empty; spike still there
	t.Run("a branch held by a worktree is renamed in place", ...)     // git worktree add ../wt spike; after: worktree list shows the new name
	t.Run("esc on the form leaves the branch alone", ...)             // prompter returns huh.ErrUserAborted; errors.Is; spike untouched
	t.Run("a parseable name is tracked without the form", ...)        // trackBranch on "9@feat@thing" → in_progress, form not opened
}
```

- [ ] **Step 2: Run, expect compile failures** across `./branch/... ./git/... ./cmd/review/... ./cmd/issue/... ./cmd/branch/...`

- [ ] **Step 3: Implement** in the order: `TitleFromName`, `RenameBranch`, `PickIssueFromUser` signature (+ its three callers: `cmd/issueflow/start_prompter.go`, `cmd/issueflow/start.go`, `scriptedStartPrompter` in `cmd/issue/start_prompter_test.go`; `grep -rn PickIssueFromUser --include=*.go` finds no other implementer of `issueflow.StartPrompter`, re-run it before Step 4), `TrackBranch`/`ErrNotTrackable`/`newReviewDeps`, `trackBranch`/`trackPlain`, wiring.

- [ ] **Step 4: Run `mise exec -- go test ./...`, expect PASS** (the start E2E suites compile against the new `PickIssueFromUser`).

---

### Task 6: issue close from a row

**Files:**
- Modify: `cmd/issue/close.go` (`closeDeps` line 27, `getPickedBranch` line 677, new `CloseIssue`)
- Modify: `cmd/branch/branch.go` (wire `tableDeps.closeIssue`)
- Test: `cmd/issue/close_e2e_test.go`, `cmd/branch/table_e2e_test.go`

**Interfaces:**
- Produces:
  ```go
  // closeDeps gains
  //   branchName string // with issueID: the one candidate; "" keeps every branch of the issue
  // getPickedBranch(ctx, client, invokedBranch, issueID, branchName string, prompter) : after the slug filter, when branchName != "" keep only r.BranchName == branchName; zero left → fmt.Errorf("branch %q of issue %s is not in progress", branchName, issueID)
  func CloseIssue(ctx context.Context, cmd *cobra.Command, cfg *config.AppConfig, issueSlug, branchName string) error
      // deps := buildCloseDeps(ctx, cmd, cfg); deps.pushConfirm = pushflow.NewHuhConfirm(); deps.push, deps.noPush = pushflow.ReadFlags(cmd); deps.issueID, deps.branchName = issueSlug, branchName; runClose(ctx, deps, newHuhPrompter(deps.client, cfg))
  ```
  Wiring in `GetRootCmd`: `tableDeps.closeIssue = func(ctx, slug, name string) error { return issue.CloseIssue(ctx, cmd, b.appConfig, slug, name) }` (`cmd/branch` imports `github.com/piprim/git-zf/cmd/issue`; `mise exec -- go build ./...` proves no cycle).

- [ ] **Step 1: Failing test in `cmd/issue/close_e2e_test.go`**

```go
func TestClose_ByBranchName(t *testing.T) {
	// newCloseRig + a second in-progress variant "ABC-1@feat@add-thing@spike" seeded and created one commit ahead
	// deps.issueID = "ABC-1"; deps.branchName = "ABC-1@feat@add-thing@spike"; prompter.Branch = nil (PickBranch must not be called: make the scripted prompter fail the test if it is)
	t.Run("merges the named branch without the picker", ...)   // main contains the spike commit; the first branch stays in_progress
	t.Run("a name that is not in progress is an error", ...)   // branchName "ABC-1@feat@nope" → error contains "not in progress"
}
```
and in `cmd/branch/table_e2e_test.go` `TestRunBranchAction`: `t.Run("issue close calls its func with the slug and branch and does not reopen", ...)`.

- [ ] **Step 2: Run, expect failures.** `mise exec -- go test ./cmd/issue/... -run "^TestClose_ByBranchName" -v`

- [ ] **Step 3: Implement and wire.**

- [ ] **Step 4: Run `./cmd/issue/... ./cmd/branch/...`, expect PASS**, the whole `^TestClose_` suite included.

---

### Task 7: the log entry

**Files:**
- Modify: `cmd/log/log.go:52-60` (`runLog`), new `RunLog`
- Modify: `cmd/branch/branch.go` (wire `tableDeps.log`)
- Test: `cmd/log/log_test.go`, `cmd/branch/table_e2e_test.go`

**Interfaces:**
- Produces:
  ```go
  // RunLog is runLog for a caller outside the package: with branch != "" every
  // command runs on that branch alone (its --all, --branches, --remotes and
  // --tags arguments dropped, --exclude kept, the branch appended).
  func RunLog(ctx context.Context, client *git.Client, cfg config.LogConfig, branch string, isTTY bool) error
  func withoutAllRefs(args []string) []string   // drops exactly "--all", "--branches", "--remotes", "--tags" and their "=<glob>" forms
  ```
  `runLog(ctx, client, cfg, extra, isTTY)` keeps its signature and gains a `branch string` parameter internally via a shared `run(ctx, client, cfg, extra, branch, isTTY)`; the cobra RunE passes `branch = ""`.
  Wiring: `tableDeps.log = func(ctx, name) error { return log.RunLog(ctx, c, b.appConfig.Log, name, term.IsTerminal(int(os.Stdout.Fd()))) }` (`cmd/branch` imports `github.com/piprim/git-zf/cmd/log`; no cycle, `cmd/log` imports no `cmd/*` but `cmdutil`).

- [ ] **Step 1: Failing tests in `cmd/log/log_test.go`**

```go
func TestWithoutAllRefs(t *testing.T) {
	t.Run("drops --all", ...) t.Run("drops --branches=glob and --tags", ...) t.Run("keeps --exclude and the format", ...)
}
func TestRunLog_Branch(t *testing.T) {
	// newLogRig plus a branch "side" with one commit not on master, master with "feat: second commit"
	t.Run("prints only the branch's history", ...)        // RunLog(..., "side", false): contains the side subject, lacks "feat: second commit"
	t.Run("the default graph command loses --all", ...)   // same output lacks master's tip although the default cmd has --all
}
```
and the table test `t.Run("log calls its func with the branch and reopens", ...)`.

- [ ] **Step 2: Run, expect compile failure.** `mise exec -- go test ./cmd/log/... -run "TestWithoutAllRefs|TestRunLog_Branch" -v`

- [ ] **Step 3: Implement and wire.**

- [ ] **Step 4: Run `./cmd/log/... ./cmd/branch/...`, expect PASS.**

---

### Task 8: command surface and docs

**Files:**
- Modify: `cmd/branch/branch.go` (`GetRootCmd` Long lines 49-64, `listCmd` Long lines 83-88)
- Modify: `website/docs/concepts/issues-and-branches.md` (the paragraph that describes the branch menu / `branch list`, if any), regenerate `website/docs/reference/commands/`
- Modify: `CLAUDE.md` (the "Testing the menus" section: `git zf branch` no longer opens a menu; add a "Testing the branch table" section)
- Test: `cmd/branch/branch_test.go`, `cmd/root_test.go`

- [ ] **Step 1: Rewrite the `Long` of `branch`**: the table (every local branch, tracked rows with their record), the keys `S`, `T`, `R`, `/`, enter and the per-row entries in one sentence each, `n`, `p`, `P`, `q`; "Without a terminal the rows are printed." Rewrite `list`'s Long: "--status preselects the status filter; --stdout and --json print the tracked branches only." In `cmd/root.go` (Long, lines 60-61) replace "git zf branch and git zf review do the same one level down; git zf issue opens the issue table." with "git zf review does the same one level down; git zf issue and git zf branch open their tables." so `git_zf.md` stays accurate after `make docs`.

- [ ] **Step 2: Test** in `cmd/branch/branch_test.go`:
```go
func TestBranchRootCmd_Long(t *testing.T) {
	t.Run("describes the table, not a menu", func(t *testing.T) { !strings.Contains(long, "a menu offers") && strings.Contains(long, "S ") })
}
```
Run `mise exec -- go test ./cmd/... -run "TestBranchRootCmd_Long|TestGetRootCmd_menu" -v`, expect PASS.

- [ ] **Step 3: `make docs`**, confirm `website/docs/reference/commands/git_zf.md`, `git_zf_branch.md` and `git_zf_branch_list.md` changed and nothing else under `commands/` did (`git status`).

- [ ] **Step 4: CLAUDE.md**: in "Testing the menus" replace the `git zf branch` menu sentences by a pointer to the new section; add:

```
### Testing the branch table

`git zf branch` opens a table of every local branch (`branch.TableRows`,
`tui.BranchTableModel`): S/T/R filter, enter opens a per-row menu
(`menuEntriesFor`, `cmd/branch/table.go`) and `runBranchAction` runs the
entry with the terminal free, as `listAction` does for the issue table. The
actions of other packages are injected through `tableDeps` funcs; a nil func
removes its entry. `trackPlain` (`cmd/branch/track.go`) renames a free-named
branch to `<id>@<type>@<slug>` after the `branch new` form.

    mise exec -- go test ./branch/... -run "TestTableRows" -v
    mise exec -- go test ./tui/... -run "TestBranchTable_|TestRelativeTime" -v
    mise exec -- go test ./cmd/branch/... -run "^TestMenuEntriesFor|^TestRunBranchAction|^TestRunBranchTable_|^TestTrackPlain" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_ByBranchName" -v
    mise exec -- go test ./cmd/log/... -run "TestRunLog_Branch|TestWithoutAllRefs" -v
```

- [ ] **Step 5: Full run** `mise exec -- go test ./...` and `mise exec -- go vet ./...`, expect PASS; `node .gitnexus/run.cjs detect-changes --scope all --repo .` and report its summary.

- [ ] **Step 6: Smoke** `./bin/git-zf branch` in this repository (TTY): open, `S`, `T`, `R`, `/`, enter on a plain branch shows the plain entries, esc returns, `q`.

---

## Self-review notes

- Spec coverage: screen/columns (T1, T2, T3), filters (T2), menu table and each entry (T3, T4, T5, T6, T7), reopen rules (T3, T6), command surface (T3, T8), architecture names (T1-T7), tests (each task), docs (T8). The spec's `menuEntriesFor` test moves from `tui` to `cmd/branch`, where the entries live; the spec's `TableRows(ctx, c, includeRemote)` became `TableRows(ctx, c, remote)` with `R` a pure filter in the model (one extra `for-each-ref` over `refs/remotes`, no reload on toggle). The Created date of a plain branch is dropped (the spec's allowed call).
- Type consistency: `branchAction` strings are the menu labels; `tableDeps` func names match between T3 and T4-T7; `BranchTableOptions.Statuses` is what `runList` fills from `--status`.
