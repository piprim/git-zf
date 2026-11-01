# `issue close` merges, `issue abandon` closes without a merge — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `git zf issue close [<id>]` and the table's `c` always run the merge flow; the "close without a merge" behaviour moves to `git zf issue abandon [<id>]` (a picker of the repository issues without an ID) and the table's `a`.

**Architecture:** The close-by-ID fork in `closeRunE` goes away: an ID becomes a field of `closeDeps` that `getPickedBranch` uses to filter `issueflow.CloseCandidates` (one candidate skips the picker, none prints a hint). `runCloseByID` is renamed `runAbandon` with new copy and gets its own cobra command. The table gains `IssuePickAbandon`; `c` picks at once, `a` arms. `listAction` runs `runClose` for `c` and takes a `ClosePrompter` so tests can script it. No change to the merge engine, the record fold or the mirror.

**Tech Stack:** Go (mise), cobra, bubbletea/huh, the existing E2E rigs (`newCloseRig`, `newRecordRig`, `newMirrorRig`, `scriptedPrompter`, `scriptedRecordPrompter`).

**Spec:** `docs/superpowers/specs/2026-10-29-issue-close-merges-abandon-design.md`

## Global Constraints

- Run Go through mise: `mise exec -- go test ./...`.
- Every test assertion lives in its own `t.Run` (global `CLAUDE.md`).
- Before editing a symbol run `impact({target, direction: "upstream"})`; before committing run `detect_changes()` (project `CLAUDE.md`).
- The committed reference pages must match `make docs-gen` output (`go test ./internal/docgen/...` enforces it).
- No alias for the old `issue close <id>` meaning; no change to `branch close`, `branch merge`, the merge engine, the review preflight, the tracker status step or the mirror.
- Exact copy from the spec:
  - no branch: `Issue <id> has no branch in progress. To close it without a merge: git zf issue abandon <id>` on stdout, exit 0.
  - abandon refusal: ``issue <id> has a branch in progress (<names>): merge it with `git zf issue close <id>`, or abandon the branch with `git zf branch close <branch-name>`, then abandon the issue``.
  - abandon success: `Abandoned issue <id>: <title>`. Already closed: `Issue <id> is already closed.`
  - table notice on the first `a`: `a again abandons <id> (no undo)`.
  - Shorts: close `Close an issue by merging its branch`; abandon `Close an issue without merging anything`.

## Review Focus

1. `issue close <id>` with an ID the repository does not know (a tracker number with the mirror off, a branch slug typed by hand): the candidates must be filtered by the typed ID itself, not fail on `ErrIssueNotFound`. Pinned in Task 1 (`one in-progress branch is merged without the picker`: `ABC-1` is a branch slug only, not a repository issue, in `newCloseRig`).
2. `issue close <id>` where the ID is a tracker number and the branch was started under the record's short hash before the export: both slugs must match. Pinned in Task 1 (`short-hash branch of an exported issue`).
3. `c` on a table row with no branch must leave the record open and reopen the table. Pinned in Task 4 (`c on a row with no branch`).
4. `--base` with an ID still validates the target (a `--base` naming the branch being closed is refused). Covered by the existing `chooseMergeTarget` tests; the ID path only changes the picking step, pinned in Task 1 (`--base is accepted with an ID`).
5. `issue abandon` with a tracker number that is not in the repository must fail like `issue show`, never touch the tracker. Pinned in Task 2 (`an unknown ID is an error`, kept from the old suite). A picker left with esc must change nothing: pinned in Task 2 (`a picker left with esc abandons nothing`).

---

### Task 1: `issue close [<id>]` filters the candidates by the issue

**Files:**
- Modify: `cmd/issue/close.go` (`closeDeps`, `getCloseCmd`, `closeRunE`, delete `closeByIDRunE`, `runClose`, `getPickedBranch`)
- Test: `cmd/issue/close_e2e_test.go` (new `TestClose_ByID`)
- Test: `cmd/issue/record_ops_e2e_test.go` (`TestCloseCmd_ByID` rewritten)
- Test: `cmd/root_test.go:111` (the `issue close` Long row)

**Interfaces:**
- Consumes: `issueflow.CloseCandidates(ctx, client) ([]branch.Row, error)`, `issuepkg.Resolve(ctx, client, query) (Record, error)`, `git.ErrIssueNotFound`, `issueflow.FetchIssues(ctx, client)`, `branch.Fetch(ctx, client)`.
- Produces:
  - `closeDeps.issueID string` — the `<id>` argument, `""` without one.
  - `func issueSlugsOf(ctx context.Context, client *git.Client, id string) ([]string, error)` — the chain slugs a branch of `id` may sit under: `slices.Compact([]string{rec.DisplayID(), rec.ShortID()})` of the record `id` resolves to; `[]string{id}` when `errors.Is(err, git.ErrIssueNotFound)`; any other error returned.
  - `func getPickedBranch(ctx context.Context, client *git.Client, invokedBranch, issueID string, prompter ClosePrompter) (*branch.Row, error)` — one more parameter.
  - `const noBranchHint = "Issue %s has no branch in progress. To close it without a merge: git zf issue abandon %s\n"`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "getPickedBranch", direction: "upstream"})` and `impact({target: "closeRunE", direction: "upstream"})`. Report the blast radius in the task notes.

- [ ] **Step 2: Write the failing E2E test**

Append to `cmd/issue/close_e2e_test.go`. `newCloseRig` seeds `ABC-1@feat@add-thing` (slug `ABC-1`, tracker-born) with one commit on it; the rig's `deps()` returns `closeDeps` without `issueID`.

```go
func TestClose_ByID(t *testing.T) {
	t.Parallel()

	t.Run("one in-progress branch is merged without the picker", func(t *testing.T) {
		t.Parallel()
		rig := newCloseRig(t)
		deps := rig.deps()
		deps.issueID = "ABC-1"
		prompter := &scriptedPrompter{
			BranchErr:     errors.New("PickBranch must not be called for one candidate"),
			Strategy:      commitpkg.MergeStrategyRebase,
			Confirm:       true,
			Message:       []byte("feat(thing): close ABC-1\n"),
			TrackerStatus: "Closed",
			DeleteBranch:  true,
		}
		if err := runClose(t.Context(), deps, prompter); err != nil {
			t.Fatalf("runClose: %v", err)
		}
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
		rows, err := branch.ListRows(t.Context(), rig.client, branch.StatusMerged)
		if err != nil || len(rows) != 1 || rows[0].BranchName != "ABC-1@feat@add-thing" {
			t.Errorf("merged rows = %+v (%v)", rows, err)
		}
	})

	t.Run("no branch in progress prints the abandon hint and merges nothing", func(t *testing.T) {
		t.Parallel()
		rig := newCloseRig(t)
		deps := rig.deps()
		deps.issueID = "ZZZ-9"
		prompter := &scriptedPrompter{BranchErr: errors.New("PickBranch must not be called")}
		if err := runClose(t.Context(), deps, prompter); err != nil {
			t.Fatalf("runClose: %v", err)
		}
		want := "Issue ZZZ-9 has no branch in progress. To close it without a merge: git zf issue abandon ZZZ-9"
		if got := rig.stdout.String(); !strings.Contains(got, want) {
			t.Errorf("stdout = %q, want %q", got, want)
		}
		rows, _ := branch.ListRows(t.Context(), rig.client, branch.StatusInProgress)
		if len(rows) != 1 {
			t.Errorf("in-progress rows = %+v, want the seeded one untouched", rows)
		}
	})

	t.Run("two variants reach the picker as the only candidates", func(t *testing.T) {
		t.Parallel()
		rig := newCloseRig(t)
		rig.addBranch(t, "ABC-1@feat@variant")
		branchtest.Seed(t, rig.client, branch.Op{Branch: "ABC-1@feat@variant", Title: "Add thing"}, branch.StatusInProgress)
		rig.addBranch(t, "DEF-2@feat@other")
		branchtest.Seed(t, rig.client, branch.Op{Branch: "DEF-2@feat@other", Title: "Other"}, branch.StatusInProgress)
		deps := rig.deps()
		deps.issueID = "ABC-1"
		prompter := &scriptedPrompter{BranchErr: errors.New("stop at the picker")}
		_ = runClose(t.Context(), deps, prompter)
		names := make([]string, len(prompter.PickBranchSeen))
		for i, r := range prompter.PickBranchSeen {
			names[i] = r.BranchName
		}
		slices.Sort(names)
		if want := []string{"ABC-1@feat@add-thing", "ABC-1@feat@variant"}; !slices.Equal(names, want) {
			t.Errorf("picker offered %v, want %v", names, want)
		}
	})

	t.Run("a short-hash branch of an exported issue is found by the tracker number", func(t *testing.T) {
		t.Parallel()
		rig := newCloseRig(t)
		rec, err := issuepkg.Create(t.Context(), rig.client, issuepkg.NewIssue{Title: "Early", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		name := rec.ShortID() + "@feat@early"
		rig.addBranch(t, name)
		branchtest.Seed(t, rig.client, branch.Op{Branch: name, Title: "Early", IssueID: rec.ID}, branch.StatusInProgress)
		link := &issuepkg.Op{Type: issuepkg.OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "11"}
		if err := issuepkg.Append(t.Context(), rig.client, rec.ID, link); err != nil {
			t.Fatalf("Append: %v", err)
		}
		deps := rig.deps()
		deps.issueID = "11"
		prompter := &scriptedPrompter{BranchErr: errors.New("one candidate: no picker")}
		_ = runClose(t.Context(), deps, prompter)
		if got := rig.stdout.String(); strings.Contains(got, "no branch in progress") {
			t.Errorf("stdout = %q: branch %s was not matched to issue 11", got, name)
		}
	})

	t.Run("--base is accepted with an ID", func(t *testing.T) {
		t.Parallel()
		rig := newCloseRig(t)
		deps := rig.deps()
		deps.issueID = "ABC-1"
		deps.baseOverride = "main"
		prompter := &scriptedPrompter{Strategy: commitpkg.MergeStrategyClassic, Confirm: false}
		if err := runClose(t.Context(), deps, prompter); err != nil {
			t.Fatalf("runClose: %v", err)
		}
		if !strings.Contains(rig.stdout.String(), "Aborted.") {
			t.Errorf("stdout = %q, want the flow to reach the confirm with --base", rig.stdout.String())
		}
	})
}
```

Add the imports the file lacks (`slices`, `issuepkg "github.com/piprim/git-zf/issue"`, `branchtest`) if missing.

- [ ] **Step 3: Rewrite the cobra-level test**

Replace `TestCloseCmd_ByID` in `cmd/issue/record_ops_e2e_test.go` (keep the `run` helper and the `t.Chdir`):

```go
	t.Run("a merge flag with an ID is accepted; an issue with no branch prints the hint and stays open", func(t *testing.T) {
		if err := run("close", rec.ShortID(), "--no-push"); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if !strings.Contains(rig.stdout.String(), "git zf issue abandon "+rec.ShortID()) {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
		got, loadErr := issuepkg.Load(ctx, rig.client, rec.ID)
		if loadErr != nil || got.State != issuepkg.StateOpen {
			t.Errorf("State = %q (%v), want %q", got.State, loadErr, issuepkg.StateOpen)
		}
	})
```

Delete the `close <id> closes the issue` subtest (it moves to Task 2 as `abandon <id>`). Update the leading comment: the argument now pre-selects the issue's branch.

In `cmd/root_test.go:111` change `{[]string{"issue", "close"}, "git zf branch close"}` to `{[]string{"issue", "close"}, "git zf issue abandon"}`.

- [ ] **Step 4: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_ByID|^TestCloseCmd_ByID" -v`
Expected: compile error, `deps.issueID undefined`.

- [ ] **Step 5: Implement**

In `cmd/issue/close.go`:
- Add `issueID string` to `closeDeps` with a comment: the `<id>` argument; the candidates are that issue's in-progress branches.
- `getCloseCmd`: `Short: "Close an issue by merging its branch"`. Long: the picker without an ID (current branch pre-selected), the ID form (`<id>` resolved as `issue start <id>`: full ID, unique prefix of at least 4 characters, or tracker number; one branch skips the picker, several are the only ones offered), the `no branch in progress` line and the hint to `git zf issue abandon <id>`, and that `--base`, `--push`, `--no-push` apply either way. The Long must contain the literal `git zf issue abandon`.
- `closeRunE`: delete the `len(args) > 0` fork and `closeByIDRunE`; after the flags, `if len(args) > 0 { deps.issueID = args[0] }`.
- `runClose`: pass `deps.issueID` to `getPickedBranch`.
- `getPickedBranch(ctx, client, invokedBranch, issueID string, prompter)`: after `branch.Fetch`, when `issueID != ""` also call `issueflow.FetchIssues(ctx, client)`, compute `slugs, err := issueSlugsOf(ctx, client, issueID)`, filter `branches` with `slices.DeleteFunc` on `!slices.Contains(slugs, row.IssueSlug)`. Then: zero → `fmt.Fprintf(client.IO().Out, noBranchHint, issueID, issueID)`, return `nil, nil` (the `branch merge` nudge is for the no-ID path only); one → return `&branches[0]`; several → `prompter.PickBranch(ctx, branches, invokedBranch)`. The no-ID path is unchanged.
- `issueSlugsOf` as in Interfaces. Exact match on the slug only: sub-task chains (`X.1`) are not candidates of `X`; a sub-task is closed by its own ID.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... ./cmd/ -run "^TestClose_|^TestCloseCmd_ByID|^TestCloseRunE|^TestGetRootCmd" -v`
Expected: PASS. `TestCloseRunE_InteractiveDispatch` still passes (the dispatch path reads `--base` tolerantly).

- [ ] **Step 7: Commit**

```bash
git add cmd/issue/close.go cmd/issue/close_e2e_test.go cmd/issue/record_ops_e2e_test.go cmd/root_test.go
git commit -m "feat(issue): close <id> merges the issue's branch; one candidate skips the picker, none prints the abandon hint"
```

Note: `go build ./...` passes but `list.go` still calls `runCloseByID`, which still exists until Task 2. The tree compiles at every commit.

---

### Task 2: `issue abandon [<id>]`

**Files:**
- Modify: `cmd/issue/record.go` (`runCloseByID` → `runAbandon`)
- Create: `cmd/issue/abandon.go`
- Modify: `cmd/issue/issue.go` (register the command)
- Modify: `cmd/issue/list.go:143` (`runCloseByID` → `runAbandon`, a rename only; Task 4 restructures it)
- Test: `cmd/issue/record_ops_e2e_test.go` (`TestRunCloseByID` → `TestRunAbandon`, new `TestAbandonCmd`)
- Test: `cmd/issue/mirror_e2e_test.go` (`TestMirror_CloseByIDClosesTheTrackerIssue` → `TestMirror_AbandonClosesTheTrackerIssue`)

**Interfaces:**
- Consumes: `resolveRecord(ctx, client, p recordPrompter, args []string) (issuepkg.Record, error)` (opens `p.PickRecord` over every repository issue when `args` is empty, as `show`/`edit`/`comment`/`label` do), `branch.Load`, `issuepkg.Append`, `pushIssue`, `issueflow.ReconcileIssues`, `openMirror(cfg, errWriter) *issuepkg.Mirror`, `cmdutil.NewClientForCmd`.
- Produces:
  - `func runAbandon(ctx context.Context, client *git.Client, p recordPrompter, args []string, m *issuepkg.Mirror) error` — the shape of `runLabel`; `args` is `nil` or `[]string{id}`.
  - `func (i Issue) getAbandonCmd() *cobra.Command` — `Use: "abandon [<id>]"`, `Args: cobra.MaximumNArgs(1)`, no flags.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "runCloseByID", direction: "upstream"})`. Expected callers: `closeByIDRunE` (gone after Task 1), `listAction`, the tests.

- [ ] **Step 2: Rename the tests and change the assertions**

In `cmd/issue/record_ops_e2e_test.go`, rename `TestRunCloseByID` → `TestRunAbandon` and every `runCloseByID(ctx, rig.client, X, nil)` → `runAbandon(ctx, rig.client, &scriptedRecordPrompter{}, []string{X}, nil)`. Change two assertions:
- first subtest: `want := "Abandoned issue " + dup.ShortID()`.
- `an issue with a branch in progress is refused`: the error must contain `name`, `"git zf issue close "+wip.ShortID()` and `"branch close"`.

Add two subtests at the end of `TestRunAbandon`:

```go
	t.Run("without an ID the picker is offered and its pick is abandoned", func(t *testing.T) {
		picked := create(t, "Picked")
		p := &scriptedRecordPrompter{PickID: picked.ID}
		if err := runAbandon(ctx, rig.client, p, nil, nil); err != nil {
			t.Fatalf("runAbandon: %v", err)
		}
		if p.Picks != 1 || !slices.ContainsFunc(p.PickedFrom, func(r issuepkg.Record) bool { return r.ID == picked.ID }) {
			t.Errorf("picks = %d, offered = %+v", p.Picks, p.PickedFrom)
		}
		if got := state(t, picked.ID); got != issuepkg.StateClosed {
			t.Errorf("State = %q, want %q", got, issuepkg.StateClosed)
		}
	})

	t.Run("a picker left with esc abandons nothing", func(t *testing.T) {
		before, _, _ := issuepkg.List(ctx, rig.client)
		err := runAbandon(ctx, rig.client, &scriptedRecordPrompter{Err: huh.ErrUserAborted}, nil, nil)
		after, _, _ := issuepkg.List(ctx, rig.client)
		same := slices.EqualFunc(before, after, func(a, b issuepkg.Record) bool { return a.ID == b.ID && a.State == b.State })
		if !errors.Is(err, huh.ErrUserAborted) || !same {
			t.Errorf("err = %v, records unchanged = %v", err, same)
		}
	})
```

Add, after `TestCloseCmd_ByID`:

```go
// `issue abandon <id>` goes through cobra. Not parallel: the command opens
// the repository of the current directory.
func TestAbandonCmd(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Wontfix", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Chdir(rig.dir)
	run := func(args ...string) error {
		root := New(rig.cfg).GetRootCmd()
		root.SetArgs(args)
		root.SetOut(rig.stdout)
		root.SetErr(rig.stderr)
		return root.ExecuteContext(ctx)
	}

	t.Run("abandon <id> closes the issue", func(t *testing.T) {
		if err := run("abandon", rec.ShortID()); err != nil {
			t.Fatalf("Execute: %v", err)
		}
		got, loadErr := issuepkg.Load(ctx, rig.client, rec.ID)
		if loadErr != nil || got.State != issuepkg.StateClosed {
			t.Errorf("State = %q (%v), want %q", got.State, loadErr, issuepkg.StateClosed)
		}
	})
	t.Run("a second ID is refused", func(t *testing.T) {
		if err := run("abandon", rec.ShortID(), rec.ShortID()); err == nil {
			t.Fatal("expected a usage error")
		}
	})
}
```

In `cmd/issue/mirror_e2e_test.go` rename the test to `TestMirror_AbandonClosesTheTrackerIssue` and the call to `runAbandon(ctx, rig.client, &scriptedRecordPrompter{}, []string{rig.onlyRecord(t).ID}, m)`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestRunAbandon|^TestAbandonCmd|^TestMirror_Abandon" -v`
Expected: compile error, `undefined: runAbandon`.

- [ ] **Step 4: Implement**

- `cmd/issue/record.go`: rename `runCloseByID` → `runAbandon` with the new signature; `resolveRecord(ctx, client, p, args)` replaces the `nil, []string{query}` call (that is the whole picker: nothing else changes for the no-ID path); the refusal becomes ``"issue %s has a branch in progress (%s): merge it with `git zf issue close %s`, or abandon the branch with `git zf branch close <branch-name>`, then abandon the issue"`` with `rec.DisplayID()` twice; the final line `"Abandoned issue %s: %s\n"`. Update the doc comment (the way out for a duplicate or a wontfix, refuses while a branch is in progress).
- `cmd/issue/abandon.go`: `getAbandonCmd` with `Short: "Close an issue without merging anything"` and a Long reworded from the old `issue close` paragraph: for a duplicate or an issue that will not be worked on; `<id>` is the full ID of a repository issue or a unique prefix of at least 4 characters, and without one a picker lists the repository issues; a tracker issue that is not in the repository cannot be abandoned; refused while a branch of the issue is in progress (merge it with `git zf issue close <id>`, or abandon the branch with `git zf branch close <branch-name>`, then abandon the issue); the close is pushed and the mirror reconciles. The Long must contain the literal `git zf branch close`. `RunE`: `cmdutil.NewClientForCmd(cmd, i.appConfig)` wrapped as `open repository: %w`, then `runAbandon(cmd.Context(), client, huhRecordPrompter{}, args, openMirror(i.appConfig, client.IO().Err))`.
- `cmd/issue/issue.go`: add `i.getAbandonCmd()` right after `i.getCloseCmd()`.
- `cmd/issue/list.go:143`: `runAbandon(ctx, infra.client, p, []string{row.IssueSlug}, infra.mirror)` instead of `runCloseByID` (behaviour unchanged here; Task 4 moves it under `IssuePickAbandon`).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... ./cmd/ -run "^TestRunAbandon|^TestAbandonCmd|^TestMirror_Abandon|^TestCloseCmd_ByID|^TestGetRootCmd" -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/abandon.go cmd/issue/record.go cmd/issue/issue.go cmd/issue/list.go cmd/issue/record_ops_e2e_test.go cmd/issue/mirror_e2e_test.go
git commit -m "feat(issue): issue abandon <id> closes an issue without a merge"
```

---

### Task 3: The table: `c` picks at once, `a` arms then abandons

**Files:**
- Modify: `tui/issue.go` (`IssuePick` consts, `issueTableModel.armClose`, `Update` disarm guard, `updateNormal`, `hint`, the `IssueTableModel` doc comment)
- Test: `tui/issue_test.go` (`TestIssueTable_ActionKeys`)

**Interfaces:**
- Produces: `tui.IssuePickAbandon IssuePick` (declared right after `IssuePickClose`, comment `// a twice: abandon the row's issue (list mode)`). `IssuePickClose`'s comment becomes `// c: close the row's issue by merging its branch (list mode)`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "updateNormal", direction: "upstream"})` and `impact({target: "PickedIssue", direction: "upstream"})`.

- [ ] **Step 2: Rewrite the key tests**

In `TestIssueTable_ActionKeys` (`tui/issue_test.go`):
- Add `'c': {IssuePickClose, "B"}` to the `n`/`l`/`r` table (`c` now quits at once with the row).
- Replace the two `c` arming subtests with:

```go
	t.Run("a arms with a notice, a second a abandons the row", func(t *testing.T) {
		m := newTable(t, IssueActions{Start: true})
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m, cmd := m.Update(runeKey('a'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "a again abandons B (no undo)") {
			t.Fatalf("first a: quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
		m, cmd = m.Update(runeKey('a'))
		if pick, slug := PickedIssue(m); !quits(cmd) || pick != IssuePickAbandon || slug != "B" {
			t.Errorf("second a: quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("any other key between two a disarms the abandon", func(t *testing.T) {
		m := newTable(t, IssueActions{Start: true})
		m, _ = m.Update(runeKey('a'))
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m, cmd := m.Update(runeKey('a'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "a again abandons B") {
			t.Errorf("quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
	})
```

- Add `'a'` to the `in pick mode does nothing` loop.
- The hint assertion becomes `"n: new · c: close · a: abandon · l: label · r: sync"`.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./tui/... -run "^TestIssueTable_ActionKeys" -v`
Expected: compile error, `undefined: IssuePickAbandon`.

- [ ] **Step 4: Implement**

In `tui/issue.go`:
- Add `IssuePickAbandon` after `IssuePickClose`.
- Rename the field `armClose` → `armAbandon` (comment: the slug a first `a` armed; a second `a` abandons it; any other key disarms; an abandon has no undo).
- `Update`: the guard reads `if key.String() != "a" { m.armAbandon = "" }`.
- `updateNormal`: `case "c"` returns `m, nil, true` in pick mode, else `m.pickRow(IssuePickClose)`. `case "a"`: the body the old `c` had, with `armAbandon`, the notice `"a again abandons "+slug+" (no undo)"` and `m.pickRow(IssuePickAbandon)`.
- `hint`: `"enter: show · e: edit · s: start · n: new · c: close · a: abandon · l: label · r: sync"`.
- The `IssueTableModel` doc comment lists `s, c, a, l, n and r` (close by merge, abandon, label the row, …).

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./tui/... -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tui/issue.go tui/issue_test.go
git commit -m "feat(tui): c closes the row by merging at once; a twice abandons it"
```

---

### Task 4: `listAction` runs the merge flow for `c` and `runAbandon` for `a`

**Files:**
- Modify: `cmd/issue/list.go` (`issueListInfra`, `getIssueListCmd` Long, `issueListRunE`, `listAction`)
- Test: `cmd/issue/record_ops_e2e_test.go` (`TestListAction`)
- Modify: every other caller of `listAction` found by grep (the `runList`/start tests pass prompters positionally).

**Interfaces:**
- Consumes: `runClose(ctx, closeDeps, ClosePrompter)`, `closeDeps.issueID` (Task 1), `runAbandon` (Task 2), `tui.IssuePickAbandon` (Task 3), `newHuhPrompter(client, cfg) *huhPrompter`, `pushflow.NewHuhConfirm()`, `invokedBranchFor(ctx, client, "")`.
- Produces:
  - `issueListInfra.pushConfirm pushflow.ConfirmFunc` — nil in tests (disables the push proposal, as `closeDeps.pushConfirm` does).
  - `func (ir Issue) listAction(ctx, infra issueListInfra, p recordPrompter, sp issueflow.StartPrompter, cp ClosePrompter, pick tui.IssuePick, row *issuepkg.Row) (reopen bool, err error)` — one more parameter, before `pick`.

- [ ] **Step 1: Run impact analysis**

Run `impact({target: "listAction", direction: "upstream"})`. Then `grep -rn "listAction(" cmd/issue/` to list every call to update.

- [ ] **Step 2: Rewrite `TestListAction`**

Every call gains `&scriptedPrompter{}` after the start prompter. Replace the `c closes the record` subtest with:

```go
	t.Run("c on a row with no branch prints the abandon hint, keeps it open and reopens", func(t *testing.T) {
		reopen, err := New(rig.cfg).listAction(ctx, infra, &scriptedRecordPrompter{}, &scriptedStartPrompter{}, &scriptedPrompter{}, tui.IssuePickClose, row)
		got, loadErr := issuepkg.Load(ctx, rig.client, rec.ID)
		if !reopen || err != nil || loadErr != nil || got.State != issuepkg.StateOpen {
			t.Errorf("reopen = %v, err = %v, state = %q (%v)", reopen, err, got.State, loadErr)
		}
		if !strings.Contains(rig.stdout.String(), "git zf issue abandon "+rec.ShortID()) {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})

	t.Run("c on a row with a branch runs the merge flow", func(t *testing.T) {
		wip, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Wip", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		name := wip.ShortID() + "@feat@wip"
		runGitIn(t, rig.dir, "checkout", "-q", "-b", name)
		runGitIn(t, rig.dir, "commit", "-q", "--allow-empty", "-m", "wip")
		runGitIn(t, rig.dir, "checkout", "-q", "main")
		branchtest.Seed(t, rig.client, branch.Op{Branch: name, Title: "Wip", IssueID: wip.ID}, branch.StatusInProgress)
		cp := &scriptedPrompter{Strategy: commitpkg.MergeStrategyClassic, Confirm: false}
		reopen, err := New(rig.cfg).listAction(ctx, infra, &scriptedRecordPrompter{}, &scriptedStartPrompter{}, cp, tui.IssuePickClose, &issuepkg.Row{IssueSlug: wip.ShortID()})
		if !reopen || err != nil || !strings.Contains(rig.stdout.String(), "Aborted.") {
			t.Errorf("reopen = %v, err = %v, stdout = %q", reopen, err, rig.stdout.String())
		}
	})

	t.Run("a abandons the record under the cursor and reopens", func(t *testing.T) {
		reopen, err := New(rig.cfg).listAction(ctx, infra, &scriptedRecordPrompter{}, &scriptedStartPrompter{}, &scriptedPrompter{}, tui.IssuePickAbandon, row)
		got, loadErr := issuepkg.Load(ctx, rig.client, rec.ID)
		if !reopen || err != nil || loadErr != nil || got.State != issuepkg.StateClosed {
			t.Errorf("reopen = %v, err = %v, state = %q (%v)", reopen, err, got.State, loadErr)
		}
	})
```

`newRecordRig` leaves `cfg.Branch.Base` empty: set `rig.cfg.Branch.Base = "main"` at the top of the test. `runGitIn(t, dir, args...)` is the helper `record_e2e_test.go` already defines.

- [ ] **Step 3: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestListAction" -v`
Expected: compile error (too many arguments to `listAction`).

- [ ] **Step 4: Implement**

In `cmd/issue/list.go`:
- Add `pushConfirm pushflow.ConfirmFunc` to `issueListInfra`; `issueListRunE` sets it to `pushflow.NewHuhConfirm()` and passes `newHuhPrompter(client, ir.appConfig)` as the new argument.
- `listAction`: add `cp ClosePrompter`. `case tui.IssuePickClose`: build `closeDeps{client: infra.client, cfg: ir.appConfig, tracker: infra.tracker, issueID: row.IssueSlug, invokedBranch: invokedBranchFor(ctx, infra.client, ""), pushConfirm: infra.pushConfirm}` and `err = runClose(ctx, deps, cp)`. `case tui.IssuePickAbandon`: `err = runAbandon(ctx, infra.client, p, []string{row.IssueSlug}, infra.mirror)`.
- `getIssueListCmd` Long, Keys paragraph: `c closes the one under the cursor by merging its branch (as git zf issue close <id>; a row without a branch is told so), a twice abandons it (as git zf issue abandon <id>: the first press asks for the second, there is no undo)`.
- Update other `listAction` callers found in Step 1 with a `&scriptedPrompter{}`.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... -v`
Expected: PASS, the whole package.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/list.go cmd/issue/record_ops_e2e_test.go
git commit -m "feat(issue): the table's c runs the merge flow, a abandons"
```

---

### Task 5: Reference pages, site docs, CLAUDE.md

**Files:**
- Regenerate: `website/docs/reference/commands/*.md` via `make docs-gen` (adds `git_zf_issue_abandon.md`, rewrites `git_zf_issue.md`, `git_zf_issue_close.md`, `git_zf_issue_list.md`)
- Modify: `website/docs/workflows/close-an-issue.md`, `website/docs/workflows/start-an-issue.md`, `website/docs/under-the-hood/issue-refs.md:103,142`, `website/docs/index.md:40`
- Modify: `CLAUDE.md:109,114,281`
- Test: `internal/docgen/main_test.go` (existing drift test)

- [ ] **Step 1: Run the drift test to verify it fails**

Run: `mise exec -- go test ./internal/docgen/...`
Expected: FAIL, `git_zf_issue_abandon.md is missing: run make docs-gen` and `git_zf_issue_close.md differs`.

- [ ] **Step 2: Regenerate and edit the docs**

Run `mise exec -- make docs-gen`. Then:
- `close-an-issue.md`: intro sentence says "merge an issue branch into its base, pick a merge strategy, and abandon an issue that will not be worked on". The first section adds, after the picker sentence, `git zf issue close <id>` (the issue's branch is picked for you; several branches of that issue are the only ones offered; an issue with no branch in progress is told so with a hint to `issue abandon`). Replace the "Close without merging" section with "## Abandon an issue": `$ git zf issue abandon 1a2b3c4`, the paragraph on duplicates/wontfix, "without an ID a picker lists the issues", pushed, refused while a branch is in progress (merge with `issue close <id>`, or `branch close <branch-name>`, see pruning), "The `a` key of the issue table, pressed twice, does the same for the row under the cursor; `c` merges." Add `git zf issue abandon` to "Commands used".
- `start-an-issue.md`: the `c` `c` row becomes `| `c` | merges the branch of the issue under the cursor, as `issue close <id>` does ([close an issue](close-an-issue.md)) |`; add `| `a` `a` | abandons the issue under the cursor, as `issue abandon <id>` does |`. The warning: "`a` closes an issue without merging anything. The first press asks for the second; there is no undo."
- `issue-refs.md:103`: `issue close <id>` → `issue abandon <id>`. Line 142: `issue close 57` → `issue abandon 57`.
- `index.md:40`: "`git zf issue close` merges the branch into its …" — leave as is unless it says "closes the issue" without "merges"; then write "merges its branch and closes the issue".
- `CLAUDE.md`: line 109 `close <id>` → `abandon <id>`; line 114 `CloseByID` → `Abandon`; line 281: "`s`, `n`, `c`, `a` (pressed twice: an abandon has no undo), `l` and `r`". Add `TestAbandonCmd` and `TestClose_ByID` to the run lines where `TestCloseCmd` appears.

- [ ] **Step 3: Run the drift test and the docs grep**

Run: `mise exec -- go test ./internal/docgen/... && grep -rn "issue close <id>\|c\` \`c\`" website/docs CLAUDE.md`
Expected: PASS; the grep finds `issue close <id>` only where it describes the merge (close-an-issue.md, start-an-issue.md `c` row, issue_close reference page), never as the close-without-merge.

- [ ] **Step 4: Commit**

```bash
git add website/docs CLAUDE.md
git commit -m "docs: close means merge; abandon closes without one"
```

---

### Task 6: Whole-tree check

- [ ] **Step 1: Run everything**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./...`
Expected: PASS.

- [ ] **Step 2: Detect changes**

Run `detect_changes({scope: "compare", base_ref: "main"})`. Expected affected symbols: `closeRunE`, `getPickedBranch`, `runClose`, `issueSlugsOf`, `runAbandon`, `getAbandonCmd`, `listAction`, `issueListRunE`, `updateNormal`, `hint`, `Update` (tui). Anything in `mergeflow`, `review`, `issue/mirror.go` or `branch/` is a regression: stop and report.

- [ ] **Step 3: Grep for leftovers**

Run: `grep -rn "runCloseByID\|closeByIDRunE\|armClose\|CloseByID" --include=*.go --include=*.md . | grep -v superpowers`
Expected: no output.
