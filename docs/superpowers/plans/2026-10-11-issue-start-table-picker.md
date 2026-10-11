# Issue table as the start picker — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `git zf issue start` and `git zf branch new` pick their issue in the `issue list` table, `s` in that table starts the issue under the cursor, and the ID-less `issue show`/`edit`/`comment`/`label`/`close` picker is that same table.

**Architecture:** The row building moves from `cmd/issue/list.go` into `cmd/issueflow` as `ListRows`; `RunIssueStart` becomes "list rows, pick a row in the table, `StartIssue`", with `StartRow` exported for the list's `s` key. The `tui` issue table gets a pick mode (enter/`s` pick and quit, `n` asks for a new issue) read back with `PickedIssue`. The `StartPrompter` interface loses the tracker toggle, the repo picker and the error note, and gains `PickRow`.

**Tech Stack:** Go (via `mise exec -- go`), cobra, bubbletea v1.3.6 + bubbles table, huh v1.0.0 (remaining short forms only), the in-process fake tracker at `tracker/fake`.

**Spec:** `docs/superpowers/specs/2026-10-11-issue-start-table-picker-design.md`

## Global Constraints

- Every Go command runs through mise: `mise exec -- go test ./...`, `mise exec -- go build ./...`, `mise exec -- gofmt -l .` (must print nothing).
- Tests wrap every distinct assertion in `t.Run("descriptive label", ...)` (global CLAUDE.md rule).
- Per the project CLAUDE.md: run GitNexus `impact({target, direction: "upstream"})` on a symbol before editing it, and `detect_changes()` before every commit. If the index is stale, `node .gitnexus/run.cjs analyze` first.
- No new dependencies. `tea.WithContext` exists in bubbletea v1.3.6; `huh.ErrUserAborted` in huh v1.0.0.
- `importRootTemplate` in `issue/mirror.go` is frozen; nothing here touches it.
- Commit messages follow the log's style: `feat(issue): …`, `refactor(issue): …`, `docs: …`.
- The repo has a second worktree checked out at `wt/` (branch `audit-logs`). Never edit under `wt/`; when grepping, exclude it (`grep -rn --exclude-dir=wt …`).
- The spec's `PickedIssue(m) string` became `PickedIssue(m) (IssuePick, string)` and `IssueActions.Start` became `Pick` + `NewIssue` (spec section 1, amended). The spec says `runList` returns the picked slug; the plan returns the picked `*issue.Row` instead, same semantics, no second lookup. The spec puts the `StartRow` tests in `cmd/issueflow`; they live in `cmd/issue/start_row_e2e_test.go`, next to the rig and the scripted prompter every other start test uses.

## Review Focus

1. A row whose branch was started under another slug than its record's display ID (the list joins them by the chain's issue ID): starting it must resolve the record by `row.Branch.IssueID`, not by the slug. Test in Task 5.
2. The table filtered down to no visible rows: `s` and enter do nothing instead of indexing past the slice. Test in Task 1.
3. No rows at all (fresh repo, `branch new` offline): the table is skipped and the manual form opens, as today. Test kept in Task 4 (`TestRunIssueStart_ManualEmptyIDCreatesRepoIssue`, "the picker is not opened").
4. The tracker is down during `issue start`: a warning on stderr, the repository's rows are offered, the flow goes on. Test in Task 4.
5. `branch new` must never call the tracker, even with one configured and issues in it. Test in Task 4.

---

### Task 1: the table's pick mode (`tui`)

**Files:**
- Modify: `tui/issue.go` (`IssueActions` ~line 167, `issueTableModel` ~line 319, `updateNormal` ~line 528, `View` ~line 630)
- Test: `tui/issue_test.go`

**Interfaces:**
- Produces:
  - `type IssueActions struct { Show …; Load …; Edit …; Pick bool; NewIssue bool }`
  - `type IssuePick int` with `IssuePickNone`, `IssuePickRow`, `IssuePickNew`
  - `func PickedIssue(m tea.Model) (IssuePick, string)`
  - Keys: `s` in both modes; in pick mode `enter` picks and `n` (with `NewIssue`) picks new.

- [ ] **Step 1: Write the failing tests**

Append to `tui/issue_test.go`:

```go
func TestIssueTable_Pick(t *testing.T) {
	rows := []issue.Row{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B"},
	}
	key := func(k tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: k} }
	runeKey := func(r rune) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }
	newTable := func(t *testing.T, a IssueActions) tea.Model {
		t.Helper()
		m, err := IssueTableModel(rows, "open", a)
		if err != nil {
			t.Fatal(err)
		}

		return m
	}
	quits := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		_, ok := cmd().(tea.QuitMsg)

		return ok
	}
	show := func(slug string) (string, error) { return "ISSUE " + slug + " BODY", nil }

	t.Run("s in list mode quits with the row under the cursor", func(t *testing.T) {
		m := newTable(t, IssueActions{Show: show})
		m, _ = m.Update(key(tea.KeyDown))
		m, cmd := m.Update(runeKey('s'))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickRow || slug != "B" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("enter in list mode still shows the issue", func(t *testing.T) {
		m := newTable(t, IssueActions{Show: show})
		m, cmd := m.Update(key(tea.KeyEnter))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone || !strings.Contains(m.View(), "ISSUE A BODY") {
			t.Errorf("quits = %v, pick = %v, view = %q", quits(cmd), pick, m.View())
		}
	})

	t.Run("enter in pick mode quits with the row under the cursor", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(key(tea.KeyEnter))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickRow || slug != "A" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("n in pick mode with NewIssue quits asking for a new issue", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true, NewIssue: true})
		m, cmd := m.Update(runeKey('n'))
		pick, slug := PickedIssue(m)
		if !quits(cmd) || pick != IssuePickNew || slug != "" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("n in pick mode without NewIssue does nothing", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(runeKey('n'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone {
			t.Errorf("quits = %v, pick = %v", quits(cmd), pick)
		}
	})

	t.Run("q quits with nothing picked", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, cmd := m.Update(runeKey('q'))
		if pick, slug := PickedIssue(m); !quits(cmd) || pick != IssuePickNone || slug != "" {
			t.Errorf("quits = %v, pick = %v, slug = %q", quits(cmd), pick, slug)
		}
	})

	t.Run("s on a table filtered to nothing does nothing", func(t *testing.T) {
		m := newTable(t, IssueActions{Pick: true})
		m, _ = m.Update(runeKey('/'))
		for _, r := range "zzz" {
			m, _ = m.Update(runeKey(r))
		}
		m, _ = m.Update(key(tea.KeyEnter)) // confirm the filter
		m, cmd := m.Update(runeKey('s'))
		if pick, _ := PickedIssue(m); quits(cmd) || pick != IssuePickNone {
			t.Errorf("quits = %v, pick = %v", quits(cmd), pick)
		}
	})

	t.Run("the hint names the keys of the mode", func(t *testing.T) {
		list := newTable(t, IssueActions{Show: show}).View()
		pick := newTable(t, IssueActions{Pick: true, NewIssue: true}).View()
		record := newTable(t, IssueActions{Pick: true}).View()
		if !strings.Contains(list, "enter: show · e: edit · s: start") {
			t.Errorf("list hint missing in %q", list)
		}
		if !strings.Contains(pick, "enter: pick · n: new issue") {
			t.Errorf("pick hint missing in %q", pick)
		}
		if !strings.Contains(record, "enter: pick ·") || strings.Contains(record, "n: new issue") {
			t.Errorf("record hint wrong in %q", record)
		}
	})

	t.Run("PickedIssue on a foreign model is nothing picked", func(t *testing.T) {
		if pick, slug := PickedIssue(nil); pick != IssuePickNone || slug != "" {
			t.Errorf("pick = %v, slug = %q", pick, slug)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./tui/ -run TestIssueTable_Pick -v`
Expected: compile error, `undefined: IssuePick` / unknown field `Pick` in `IssueActions`.

- [ ] **Step 3: Implement the pick mode**

In `tui/issue.go`, replace the `IssueActions` type with:

```go
// IssueActions are the repository operations behind the dialogs of the issue
// table, keyed by the row's slug. A nil action disables its key.
type IssueActions struct {
	// Show renders the issue as `issue show` does; its error is shown in
	// place of the issue.
	Show func(slug string) (string, error)
	// Load returns the title and description the edit form starts from.
	Load func(slug string) (title, description string, err error)
	// Edit saves the edit form and returns the line the table shows.
	Edit func(slug, title, description string) (string, error)
	// Pick is set when the table picks an issue: enter and s pick the row
	// under the cursor and quit. With NewIssue, n picks "new issue".
	Pick     bool
	NewIssue bool
}

// IssuePick is how the issue table was quit.
type IssuePick int

const (
	IssuePickNone IssuePick = iota // q or ctrl+c: nothing picked
	IssuePickRow                   // s or enter: the slug is the row's issue
	IssuePickNew                   // n: a new issue
)

// PickedIssue reports how the table was quit and, for IssuePickRow, the
// slug of the picked row. m is the model tea.Program.Run returned.
func PickedIssue(m tea.Model) (IssuePick, string) {
	t, ok := m.(*issueTableModel)
	if !ok {
		return IssuePickNone, ""
	}

	return t.pick, t.pickedSlug
}
```

Update the doc comment of `IssueTableModel` to: `// IssueTableModel is the issue table. In list mode enter opens the row's issue in a dialog that replaces the table, e opens its edit form and s picks the row and quits; in pick mode (actions.Pick) enter and s pick, n asks for a new issue.`

Add two fields to `issueTableModel`, after `notice string`:

```go
	// pick and pickedSlug are how the table was quit: see PickedIssue.
	pick       IssuePick
	pickedSlug string
```

In `updateNormal`, replace the `case "enter":` block and add `s` and `n`:

```go
	case "enter":
		if m.actions.Pick {
			return m.pickRow()
		}
		m.openDialog()

		return m, nil, true
	case "s":
		return m.pickRow()
	case "n":
		if m.actions.Pick && m.actions.NewIssue {
			m.pick = IssuePickNew

			return m, tea.Quit, true
		}

		return m, nil, true
```

Add the method next to `openDialog`:

```go
// pickRow quits with the row under the cursor picked; nothing happens on an
// empty table.
func (m *issueTableModel) pickRow() (tea.Model, tea.Cmd, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.visible) {
		return m, nil, true
	}
	m.pick, m.pickedSlug = IssuePickRow, m.visible[c].IssueSlug

	return m, tea.Quit, true
}
```

In `View`, replace the two `hint` assignments (the `hint := "enter: show …"` line and the one inside `if len(m.projects) > 0 {`) with a single `hint := m.hint()` before `proj := ""`, keeping the `proj` assignment inside the `if`. Add:

```go
// hint is the key line under the table, for the mode.
func (m *issueTableModel) hint() string {
	keys := "enter: show · e: edit · s: start"
	if m.actions.Pick {
		keys = "enter: pick"
		if m.actions.NewIssue {
			keys += " · n: new issue"
		}
	}
	keys += " · / to filter · tab: status"
	if len(m.projects) > 0 {
		keys += " · p: project"
	}

	return keys + " · m: mine · q to quit"
}
```

- [ ] **Step 4: Run the tui tests**

Run: `mise exec -- go test ./tui/ -v -run "TestIssueTable"`
Expected: PASS, including the older `TestIssueTable_EnterOpensDialog`, `TestIssueTable_EditDialog`, `TestIssueTable_MineToggle` (they only check `enter: show`, still in the list hint).

- [ ] **Step 5: Commit**

```bash
mise exec -- gofmt -l tui && git add tui/issue.go tui/issue_test.go && git commit -m "feat(tui): pick mode and s key on the issue table"
```

---

### Task 2: `issue.RecordRow`

**Files:**
- Modify: `issue/row.go`
- Test: `issue/row_test.go`

**Interfaces:**
- Produces: `func RecordRow(rec *Record) Row` — the row of a repo issue with no branch: `IssueSlug: rec.DisplayID()`, `Title`, `Labels`, `State`, `TrackerStatus` (pointer to the tracker's status name, or the state), `TrackerID` (the tracker number of a linked record, else ""), `CreatedAt`.

- [ ] **Step 1: Write the failing test**

Append to `issue/row_test.go` (add `"slices"` and `"time"` to its imports if missing):

```go
func TestRecordRow(t *testing.T) {
	t.Parallel()

	created := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

	t.Run("an unlinked record: short ID, its state as status, no tracker number", func(t *testing.T) {
		t.Parallel()

		rec := Record{ID: "0123456789abcdef0123", Title: "Login", Labels: []string{"ui"}, State: StateOpen, CreatedAt: created}
		row := RecordRow(&rec)
		if row.IssueSlug != rec.ShortID() || row.Title != "Login" || !slices.Equal(row.Labels, []string{"ui"}) ||
			row.State != StateOpen || row.TrackerStatus == nil || *row.TrackerStatus != StateOpen ||
			row.TrackerID != "" || !row.CreatedAt.Equal(created) || row.Branch != nil {
			t.Errorf("row = %+v", row)
		}
	})

	t.Run("a linked record: the tracker number as slug and ID, the tracker's status", func(t *testing.T) {
		t.Parallel()

		rec := Record{
			ID: "0123456789abcdef0123", Title: "Login", State: StateOpen, TrackerStatus: "In Progress",
			Tracker: &TrackerLink{Type: "fake", Project: "zf", ID: "42"},
		}
		row := RecordRow(&rec)
		if row.IssueSlug != "42" || row.TrackerID != "42" || row.TrackerStatus == nil || *row.TrackerStatus != "In Progress" {
			t.Errorf("row = %+v", row)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./issue/ -run TestRecordRow -v`
Expected: compile error `undefined: RecordRow`.

- [ ] **Step 3: Implement**

Append to `issue/row.go` (add `"cmp"` to its imports):

```go
// RecordRow is the row of a repo issue that has no branch: what the list
// shows for a backlog item and what the pickers offer for a record.
func RecordRow(rec *Record) Row {
	return Row{
		IssueSlug: rec.DisplayID(), Title: rec.Title,
		Labels: rec.Labels, State: rec.State, TrackerStatus: recordStatus(rec),
		TrackerID: trackerNumber(rec), CreatedAt: rec.CreatedAt,
	}
}

// recordStatus is the status shown for a repo issue: the tracker's status
// name when the issue is mirrored, its open/closed state otherwise.
func recordStatus(rec *Record) *string {
	s := cmp.Or(rec.TrackerStatus, rec.State)

	return &s
}

// trackerNumber is the tracker number of a linked record, "" otherwise.
func trackerNumber(rec *Record) string {
	if rec.Tracker == nil {
		return ""
	}

	return rec.Tracker.ID
}
```

- [ ] **Step 4: Run the test**

Run: `mise exec -- go test ./issue/ -run TestRecordRow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add issue/row.go issue/row_test.go && git commit -m "feat(issue): RecordRow, the list row of a repo issue"
```

---

### Task 3: the rows move to `cmd/issueflow` (`ListRows`), `branch new` stays offline

**Files:**
- Create: `cmd/issueflow/rows.go`
- Modify: `cmd/issueflow/start.go` (`StartDeps`, `BuildStartDeps`, lines 25-68)
- Modify: `issue/issue.go` (`IssueStartFlags`)
- Modify: `cmd/issue/list.go` (delete lines ~225-399: `buildRows` … `trackerNumber`, `buildFromBranches`; keep `normalizeRows`), `cmd/issue/record.go` (delete `printWarnings`, `fetchIssues`, `mirrorOf`, `reconcileIssues`, lines 141-205), the call sites in `cmd/issue/{new,label,comment,show,close,edit,sync}.go`, `cmd/issue/start.go`, `cmd/branch/branch.go`
- Test: `cmd/issue/list_record_test.go` (new test), `cmd/issue/mirror_e2e_test.go`, `cmd/issue/close_mirror_e2e_test.go`, `cmd/issue/start_e2e_test.go`, `cmd/issue/start_record_e2e_test.go` (mechanical renames)

**Interfaces:**
- Consumes: `issue.RecordRow` (Task 2).
- Produces, package `issueflow`:
  - `func ListRows(ctx context.Context, deps StartDeps, status string) ([]issue.Row, error)`
  - `func MirrorOf(cfg *config.AppConfig, t tracker.Tracker) *issue.Mirror`
  - `func FetchIssues(ctx context.Context, client *git.Client)`
  - `func ReconcileIssues(ctx context.Context, client *git.Client, m *issue.Mirror) issue.MirrorResult`
  - `func PrintWarnings(w io.Writer, warnings []string)`
  - `StartDeps` gains `Mirror *issue.Mirror` and `Stderr io.Writer`; `(StartDeps).errW() io.Writer`.
  - `issue.IssueStartFlags.Tracker bool` replaces `TrackerFirst`.
- `cmd/issue` keeps `issueListInfra` and `buildRows(ctx, infra, status)` as a shim over `ListRows`, so every existing list test compiles unchanged.

- [ ] **Step 1: Write the failing test**

Append to `cmd/issue/list_record_test.go` (add `"github.com/piprim/git-zf/cmd/issueflow"` to its imports):

```go
// branch new lists offline: with the Tracker flag off, ListRows neither asks
// the tracker nor reconciles the mirror.
func TestListRows_OfflineNeverAsksTheTracker(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Local", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{{ID: "42", Subject: "Remote", Status: "open"}}}
	deps := issueflow.StartDeps{
		Client: rig.client, Tracker: ft, Mirror: &issuepkg.Mirror{Tracker: ft, Type: "fake", Project: "zf"},
		Flags: issuepkg.IssueStartFlags{Tracker: false},
	}

	rows, err := issueflow.ListRows(ctx, deps, "")

	t.Run("the repo issue is the only row", func(t *testing.T) {
		if err != nil || len(rows) != 1 || rows[0].IssueSlug != rec.ShortID() {
			t.Errorf("rows = %+v, err = %v", rows, err)
		}
	})
	t.Run("the tracker was never listed", func(t *testing.T) {
		if ft.ListCalls != 0 {
			t.Errorf("ListCalls = %d", ft.ListCalls)
		}
	})
	t.Run("nothing was exported", func(t *testing.T) {
		if len(ft.RecordedCreates) != 0 {
			t.Errorf("creates = %+v", ft.RecordedCreates)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/ -run TestListRows_OfflineNeverAsksTheTracker -v`
Expected: compile error `undefined: issueflow.ListRows` (and unknown field `Tracker` in `IssueStartFlags`).

- [ ] **Step 3: Rename the flag**

In `issue/issue.go` replace the `IssueStartFlags` struct with:

```go
type IssueStartFlags struct {
	// Tracker lists the tracker's issues in the picker: `issue start` and
	// `issue list` set it; `branch new` leaves it false and works offline.
	Tracker         bool
	Variant         string
	ParentIssueSlug string // non-empty when this is a sub-task; value is the parent's IssueSlug
}
```

Then, excluding `wt/`:

```bash
grep -rl --exclude-dir=wt "TrackerFirst" --include=*.go . | xargs sed -i 's/TrackerFirst/Tracker/g'
```

This rewrites `cmd/issue/start.go` (`Tracker: true`), `cmd/branch/branch.go` (`Tracker: false`), the two start E2E suites (`Tracker: false` / `Tracker: true`), and the doc comments in `cmd/issueflow/start.go`. Check the comment in `cmd/branch/branch.go` above `newRunE` now reads sensibly; change it to `// newRunE delegates to RunIssueStart offline: the picker lists the branches and the repo issues, never the tracker.`

- [ ] **Step 4: Create `cmd/issueflow/rows.go`**

```go
package issueflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// MirrorOf returns the issue mirror over an already built tracker, or nil
// when the mirror is off or there is no tracker.
func MirrorOf(cfg *config.AppConfig, t tracker.Tracker) *issue.Mirror {
	tc := cfg.IssueTracker
	if !tc.Mirror || t == nil || len(tc.Projects) != 1 {
		return nil
	}

	return &issue.Mirror{Tracker: t, Type: tc.Type, Project: tc.Projects[0].Name}
}

// PrintWarnings prints one warning per line.
func PrintWarnings(w io.Writer, warnings []string) {
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
}

// FetchIssues refreshes the local issue refs from the remote. A failure
// (offline, auth) is a warning: every command then works on local data. A
// remote ref that is not an issue chain is left out without a word here:
// `issue sync` names it and the way out, once, when asked.
func FetchIssues(ctx context.Context, client *git.Client) {
	if _, err := issue.Fetch(ctx, client); err != nil && !errors.Is(err, git.ErrForeignChain) {
		fmt.Fprintf(client.IO().Err, "warning: could not fetch issues, using local data: %v\n", err)
	}
}

// ReconcileIssues runs the mirror's reconcile, warnings to stderr. A nil m
// is a no-op with an empty result.
func ReconcileIssues(ctx context.Context, client *git.Client, m *issue.Mirror) issue.MirrorResult {
	res, err := m.Reconcile(ctx, client)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: tracker mirror: %v\n", err)
	}
	PrintWarnings(client.IO().Err, res.Warnings)

	return res
}

// ListRows lists the tracked branches, the tracker's open issues and the
// issues stored in the repository, merged into one row per issue; status
// ("open", "closed", "all", "" for every row) filters them as the list's
// tab does. With deps.Flags.Tracker false the tracker is not asked, nor
// the mirror reconciled: the rows are the branches and the repo issues.
// The tracker is asked once: with the mirror on, the reconcile's listing
// is the one shown.
func ListRows(ctx context.Context, deps StartDeps, status string) ([]issue.Row, error) {
	rows, err := buildFromBranches(ctx, deps.Client)
	if err != nil {
		return nil, err
	}

	FetchIssues(ctx, deps.Client)

	var issues []tracker.Issue
	if deps.Flags.Tracker {
		issues = ReconcileIssues(ctx, deps.Client, deps.Mirror).Listed
		if deps.Mirror == nil && deps.Tracker != nil {
			if issues, err = deps.Tracker.ListIssues(ctx); err != nil {
				fmt.Fprintf(deps.errW(), "warning: tracker unavailable, listing the repository alone: %v\n", err)
			}
		}
	}

	rows, err = mergeRepoIssues(ctx, deps, mergeTrackerIssues(issues, rows))
	if err != nil || status == "" {
		return rows, err
	}

	return slices.DeleteFunc(rows, func(r issue.Row) bool { return !r.MatchesStatus(status) }), nil
}

// mergeTrackerIssues adds the tracker's open issues to the branch rows: a row
// whose branch names a tracker issue gets its title, project, status and
// creation date, and every other issue is appended.
func mergeTrackerIssues(issues []tracker.Issue, rows []issue.Row) []issue.Row {
	byID := make(map[string]*tracker.Issue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}

	seen := make(map[string]bool, len(rows))
	for i := range rows {
		iss, ok := byID[rows[i].IssueSlug]
		if !ok {
			continue
		}
		seen[iss.ID] = true
		status := iss.Status
		rows[i].Title, rows[i].Project, rows[i].TrackerStatus, rows[i].CreatedAt = iss.Subject, iss.Project, &status, iss.CreatedAt
		rows[i].AssignedToMe = iss.AssignedToMe
	}

	for i := range issues {
		iss := &issues[i]
		if seen[iss.ID] {
			continue
		}
		status := iss.Status
		rows = append(rows, issue.Row{
			IssueSlug: iss.ID, Title: iss.Subject, Project: iss.Project, TrackerStatus: &status, CreatedAt: iss.CreatedAt,
			AssignedToMe: iss.AssignedToMe,
		})
	}

	return rows
}

// mergeRepoIssues enriches the branch rows with the issues stored in the
// repository: a row whose issue has a record gets its title, labels and
// state, and every record without a branch row is appended, so the backlog
// shows up before anyone starts a branch.
func mergeRepoIssues(ctx context.Context, deps StartDeps, rows []issue.Row) ([]issue.Row, error) {
	records, warnings, err := issue.List(ctx, deps.Client)
	if err != nil {
		return nil, fmt.Errorf("list repo issues: %w", err)
	}
	PrintWarnings(deps.errW(), warnings)

	// A branch names its issue by the full ID in its start op. A branch
	// started before that op carried one, or seeded by hand, falls back to
	// its slug, which is the display ID the branch was named after.
	byID := make(map[string]*issue.Record, len(records))
	byDisplayID := make(map[string]*issue.Record, len(records))
	for i := range records {
		byID[records[i].ID] = &records[i]
		byDisplayID[records[i].DisplayID()] = &records[i]
	}

	out := rows
	started := make(map[string]bool, len(out))
	for i := range out {
		var rec *issue.Record
		ok := false
		if out[i].Branch != nil {
			rec, ok = byID[out[i].Branch.IssueID]
		}
		if !ok {
			rec, ok = byDisplayID[out[i].IssueSlug]
		}
		if !ok {
			continue
		}
		started[rec.ID] = true
		// The record's title, not the one the branch chain froze at start: it
		// follows `issue edit`.
		rr := issue.RecordRow(rec)
		out[i].Title, out[i].Labels, out[i].State = rr.Title, rr.Labels, rr.State
		out[i].TrackerStatus, out[i].TrackerID, out[i].CreatedAt = rr.TrackerStatus, rr.TrackerID, rr.CreatedAt
	}

	for i := range records {
		if started[records[i].ID] {
			continue
		}
		out = append(out, issue.RecordRow(&records[i]))
	}

	return out, nil
}

func buildFromBranches(ctx context.Context, c *git.Client) ([]issue.Row, error) {
	branches, err := branch.ListRows(ctx, c, branch.StatusAll)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	rows := make([]issue.Row, len(branches))
	for i := range branches {
		b := branches[i]
		rows[i] = issue.Row{
			IssueSlug: b.IssueSlug,
			Title:     b.Title,
			Branch:    &b,
		}
	}

	return rows, nil
}
```

- [ ] **Step 5: Extend `StartDeps`**

In `cmd/issueflow/start.go` replace the `StartDeps` struct and the tracker block of `BuildStartDeps`:

```go
type StartDeps struct {
	Client  *git.Client
	Cfg     *config.AppConfig
	Tracker tracker.Tracker // nil when cfg.IssueTracker.Type == ""
	// Mirror is non-nil when the issues are mirrored with the tracker; the
	// listing then comes from the reconcile.
	Mirror *issue.Mirror
	// Stderr receives the warnings of the listing; nil means Client.IO().Err.
	Stderr             io.Writer
	Flags              issue.IssueStartFlags
	BaseBranchOverride string // set by PickBaseBranch; skips the parent lookup in prepareBranch
}

// errW is where the warnings go.
func (d StartDeps) errW() io.Writer {
	if d.Stderr != nil {
		return d.Stderr
	}

	return d.Client.IO().Err
}
```

and in `BuildStartDeps`, after `deps.Tracker = t`, add `deps.Mirror = MirrorOf(cfg, t)`. Add `"io"` to the imports.

- [ ] **Step 6: Point `cmd/issue` at the moved code**

In `cmd/issue/list.go` delete `buildRows`, `buildAllRows`, `mergeTrackerIssues`, `mergeRepoIssues`, `trackerStatusOf`, `trackerNumber` and `buildFromBranches` (keep `normalizeRows`), and add:

```go
// deps is the start flow's view of the list's infrastructure: the rows come
// from issueflow.ListRows, with the tracker listed.
func (infra issueListInfra) deps() issueflow.StartDeps {
	return issueflow.StartDeps{
		Client: infra.client, Tracker: infra.tracker, Mirror: infra.mirror, Stderr: infra.stderr,
		Flags: issuepkg.IssueStartFlags{Tracker: true},
	}
}

// buildRows lists the issue rows matching status, as the TUI's status tab
// does; "" keeps every row.
func buildRows(ctx context.Context, infra issueListInfra, status string) ([]issuepkg.Row, error) {
	return issueflow.ListRows(ctx, infra.deps(), status)
}
```

Add `"github.com/piprim/git-zf/cmd/issueflow"` to `list.go`'s imports and drop the ones the compiler now reports unused (`cmp`, `slices`, `branch`).

In `cmd/issue/record.go` delete `printWarnings`, `fetchIssues`, `mirrorOf` and `reconcileIssues` (keep `pushIssue`, `openMirror`, `syncOne`). Then rewrite every call site, production and test, excluding `wt/`:

```bash
cd cmd/issue && sed -i \
  -e 's/\bprintWarnings(/issueflow.PrintWarnings(/g' \
  -e 's/\bfetchIssues(/issueflow.FetchIssues(/g' \
  -e 's/\breconcileIssues(/issueflow.ReconcileIssues(/g' \
  -e 's/\bmirrorOf(/issueflow.MirrorOf(/g' \
  *.go && cd ../..
```

Add the `issueflow` import to every file the compiler flags (`record.go`, `new.go`, `label.go`, `comment.go`, `show.go`, `close.go`, `edit.go`, `sync.go`, `mirror_e2e_test.go`, `close_mirror_e2e_test.go`): `mise exec -- go build ./... && mise exec -- go vet ./cmd/issue/` until clean.

- [ ] **Step 7: Build and run the list and mirror suites**

Run: `mise exec -- go build ./... && mise exec -- go test ./cmd/issue/ -run "TestListRows_|TestBuildRows|TestBuildIssueRows|TestRunIssueList|TestMirror|TestOpenMirror|TestClose_Mirror" -v`
Expected: PASS. `TestMirror_ListReadsTheRepository` still sees `ft.ListCalls == 1`.

- [ ] **Step 8: Run everything**

Run: `mise exec -- go test ./... 2>&1 | tail -30`
Expected: PASS everywhere (the start suites only had the flag renamed).

- [ ] **Step 9: Commit**

```bash
mise exec -- gofmt -l . && git add -A cmd issue && git commit -m "refactor(issue): the list rows move to issueflow.ListRows; branch new lists offline"
```

---

### Task 4: the start flow picks in the table

**Files:**
- Modify: `cmd/issueflow/start_prompter.go` (interface lines 24-78; huh methods `PickIssueFromRepo`, `NotifyTrackerError`, `PickUseTracker` lines 152-185)
- Modify: `cmd/issueflow/start.go` (`RunIssueStart` … `getFromTracker`, lines 72-347)
- Modify: `tui/issue.go` (delete `IssueTrackerToggle` ~line 69-92, `IssueTrackerError` ~134-145, `issueTrackerPickerHeight` and the `.Height(...)` of `IssueTrackerPicker`)
- Modify: `cmd/issue/start_prompter_test.go`, `cmd/issue/start_e2e_test.go`, `cmd/issue/start_record_e2e_test.go`

**Interfaces:**
- Consumes: `tui.IssueTableModel`, `tui.IssueActions{Pick, NewIssue}`, `tui.PickedIssue`, `tui.IssuePick*` (Task 1); `ListRows`, `StartDeps.Mirror` (Task 3).
- Produces, package `issueflow`:
  - `StartPrompter.PickRow(ctx context.Context, rows []issue.Row) (*issue.Row, error)` — nil, nil means "new issue"; `PickUseTracker`, `PickIssueFromRepo`, `NotifyTrackerError` removed.
  - `func RunIssueStart(ctx, deps StartDeps, prompter StartPrompter) error` (same signature)
  - `func StartRow(ctx, deps StartDeps, prompter StartPrompter, row *issue.Row) error`
  - `func StartIssue(ctx, deps StartDeps, prompter StartPrompter, picked *issue.Issue) error`
- Test double `scriptedStartPrompter` fields: `PickSlug string`, `PickNew bool`, `PickRowErr error`, `OfferedRows []issuepkg.Row`.

- [ ] **Step 1: Rewrite the scripted prompter (the tests stop compiling)**

In `cmd/issue/start_prompter_test.go`, delete the fields `IssueFromRepo`, `IssueFromRepoErr`, `CapturedRepoRecords`, `UseTracker`, `TrackerErrorNotifications`, `TrackerErrorMessage`, `TrackerErrorErr`, `UseTrackerErr` and the methods `PickIssueFromRepo`, `NotifyTrackerError`, `PickUseTracker`. Add the fields (after `IssueFromTracker`):

```go
	// PickRow answers: PickSlug names the row to pick among those offered;
	// PickNew picks "new issue" instead (nil, nil); PickRowErr fails the
	// pick. A PickSlug that is not offered is an error: the test scripted
	// a row the flow did not list.
	PickSlug   string
	PickNew    bool
	PickRowErr error
	// OfferedRows records what PickRow was offered; nil ⇒ the table was
	// never opened (RunIssueStart skips it when there are no rows).
	OfferedRows []issuepkg.Row
```

and the method (add `"fmt"` to the imports):

```go
func (s *scriptedStartPrompter) PickRow(_ context.Context, rows []issuepkg.Row) (*issuepkg.Row, error) {
	s.OfferedRows = rows
	if s.PickRowErr != nil {
		return nil, s.PickRowErr
	}
	if s.PickNew {
		return nil, nil
	}
	for i := range rows {
		if rows[i].IssueSlug == s.PickSlug {
			return &rows[i], nil
		}
	}

	return nil, fmt.Errorf("scripted PickRow: no row %q among the %d offered", s.PickSlug, len(rows))
}
```

- [ ] **Step 2: Rescript `cmd/issue/start_e2e_test.go`**

- `TestRunIssueStart_BranchHappyPath_WithTracker`: in the prompter literal replace `UseTracker:       true,` with `PickSlug:         "ABC-3",`.
- `TestRunIssueStart_NoTrackerStatusUpdate`: replace `UseTracker:       true,` with `PickSlug:         "ABC-9",`.
- `TestRunIssueStart_PickerSelectsParent` and `TestRunIssueStart_WritesBranchRef_WithParent` seed a branch with `branchtest.Seed`, so the table has a row: add `PickNew: true,` to their `scriptedStartPrompter` literals (they go on to the manual form through `IssueFromUser`).
- Replace `TestRunIssueStart_TrackerListErrorFallsBackToManual` whole with:

```go
// Review Focus 4. The tracker is down: a warning, and the table is offered
// the repository's rows.
func TestRunIssueStart_TrackerDownOffersTheRepositoryRows(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	rig.tracker.ListErr = errors.New("network error")
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Local one", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	prompter := &scriptedStartPrompter{PickSlug: rec.ShortID(), ConfirmBranch: true}

	runErr := issueflow.RunIssueStart(ctx, rig.deps(issuepkg.IssueStartFlags{Tracker: true}), prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("RunIssueStart: %v", runErr)
		}
	})
	t.Run("the warning names the tracker failure", func(t *testing.T) {
		if got := rig.stderr.String(); !strings.Contains(got, "warning: tracker unavailable") {
			t.Errorf("stderr = %q", got)
		}
	})
	t.Run("the repository's row was offered", func(t *testing.T) {
		if len(prompter.OfferedRows) != 1 || prompter.OfferedRows[0].IssueSlug != rec.ShortID() {
			t.Errorf("offered = %+v", prompter.OfferedRows)
		}
	})
	t.Run("the branch was created", func(t *testing.T) {
		exists, err := rig.client.BranchExists(rec.ShortID() + "@feat@local-one")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
}
```

- Replace `TestRunIssueStart_DeclinesTrackerTogglesToManual` whole with:

```go
// Review Focus 5. branch new (Tracker false) never asks the tracker, even
// with issues in it: the table is empty and the manual form opens.
func TestRunIssueStart_BranchNewNeverAsksTheTracker(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	rig.tracker.ProjectIssues = []tracker.Issue{{ID: "ABC-11", Subject: "Remote", TrackerType: "fake"}}

	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{ID: "ABC-11", Subject: "Manual choice"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.deps(issuepkg.IssueStartFlags{Tracker: false}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("the tracker was never listed", func(t *testing.T) {
		if rig.tracker.ListCalls != 0 {
			t.Errorf("ListCalls = %d", rig.tracker.ListCalls)
		}
	})
	t.Run("the table was not opened: nothing to list offline", func(t *testing.T) {
		if prompter.OfferedRows != nil {
			t.Errorf("offered = %+v", prompter.OfferedRows)
		}
	})
	t.Run("the manual branch was created", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-11@feat@manual-choice")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the tracker received no status update", func(t *testing.T) {
		if got := len(rig.tracker.RecordedUpdates); got != 0 {
			t.Errorf("RecordedUpdates = %d, want 0", got)
		}
	})
}
```

Add `"strings"` to the file's imports.

- [ ] **Step 3: Rescript `cmd/issue/start_record_e2e_test.go`**

- `TestRunIssueStart_ManualEmptyIDCreatesRepoIssue`, subtest "the picker is not opened…": replace both `CapturedRepoRecords` with `OfferedRows` and the message with `"PickRow was offered %+v"`.
- `TestRunIssueStart_PicksRepoIssue`: `IssueFromRepo: &open` → `PickSlug: open.ShortID()`; the "only open issues are offered" check becomes `if len(prompter.OfferedRows) != 1 || prompter.OfferedRows[0].IssueSlug != open.ShortID() { t.Errorf("offered = %+v", prompter.OfferedRows) }`.
- `TestRunIssueStart_PickerNewThenTypedID`: `IssueFromRepo: nil, // "New issue…"` → `PickNew: true,`; "the picker was offered" checks `len(prompter.OfferedRows) != 1`.
- `TestRunIssueStart_TrackerErrorFallbackCreatesRepoIssue`: delete `UseTracker: true,`; change the comment on `rig.tracker.ProjectIssues = nil` to `// empty tracker, empty repo ⇒ no row ⇒ manual form`; replace the "the tracker error was notified" subtest with:

```go
	t.Run("the tracker was asked once", func(t *testing.T) {
		if rig.tracker.ListCalls != 1 {
			t.Errorf("ListCalls = %d", rig.tracker.ListCalls)
		}
	})
```

- `TestRunIssueStart_NonLatinBacklogItem` and `TestRunIssueStart_RepoIssueWithUnknownTypeIsRefused`: `IssueFromRepo: &rec` → `PickSlug: rec.ShortID()`.
- `TestRunIssueStart_ExportedRecordIsNamedByItsNumber`: `IssueFromRepo: &exported` → `PickSlug: exported.DisplayID()`.
- `TestRunIssueStart_ImportedRecordStartsAsTrackerIssue`: replace `UseTracker: true,` and `IssueFromRepo: &imported,` with `PickSlug: imported.DisplayID(),`; the comment on `rig.tracker.ProjectIssues = nil` becomes `// the tracker lists nothing: the imported record is the only row`.

- [ ] **Step 4: Run the suites to verify they fail**

Run: `mise exec -- go vet ./cmd/issue/`
Expected: `*scriptedStartPrompter does not implement issueflow.StartPrompter (missing method NotifyTrackerError)` and the like.

- [ ] **Step 5: The interface and the huh prompter**

In `cmd/issueflow/start_prompter.go`, replace the `StartPrompter` doc comment and interface by:

```go
// StartPrompter resolves every user-facing decision in the issue-start flow.
// The production implementation drives the issue table and huh forms; the
// test implementation in cmd/issue's start_prompter_test.go returns canned
// values. The order below mirrors the order calls happen in RunIssueStart.
type StartPrompter interface {
	// PickRow opens the issue table over rows. A nil row with a nil error
	// means "new issue": the flow falls through to PickIssueFromUser.
	PickRow(ctx context.Context, rows []issuepkg.Row) (*issuepkg.Row, error)

	// PickIssueFromTracker asks the branch type of a tracker issue: the
	// form lists issues (one, from now on) and the types.
	PickIssueFromTracker(ctx context.Context, issues []tracker.Issue, allowedTypes []string) (*issuepkg.Issue, error)

	// PickIssueFromUser opens the manual issue-input form (issue ID, subject, type).
	PickIssueFromUser(ctx context.Context, allowedTypes []string) (*issuepkg.Issue, error)

	// PickUseWorktree drives the "worktree vs plain branch?" toggle. Called
	// only when cfg.Branch.UseWorktree == nil.
	PickUseWorktree(ctx context.Context) (use bool, err error)

	// PickBaseBranch presents a picker over all local git branches.
	// defaultBase is pre-selected. Called only when --parent was not explicitly
	// provided and the repo has more than one local branch.
	PickBaseBranch(ctx context.Context, defaultBase string, branches []string) (baseBranch string, err error)

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
	ResolveBranchConflict(
		ctx context.Context, client *git.Client, b *branch.Branch, picked *issuepkg.Issue,
	) (*branch.Branch, error)
}
```

Delete the methods `PickIssueFromRepo`, `NotifyTrackerError` and `PickUseTracker` of `HuhStartPrompter`, and add (imports: `tea "github.com/charmbracelet/bubbletea"`):

```go
// PickRow opens the issue table over rows: enter or s picks the row under
// the cursor, n asks for a new issue, q gives up (huh.ErrUserAborted, as the
// huh picker's Esc was).
func (*HuhStartPrompter) PickRow(ctx context.Context, rows []issuepkg.Row) (*issuepkg.Row, error) {
	m, err := tui.IssueTableModel(rows, issuepkg.StateOpen, tui.IssueActions{Pick: true, NewIssue: true})
	if err != nil {
		return nil, fmt.Errorf("issue table: %w", err)
	}
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		return nil, fmt.Errorf("issue table: %w", err)
	}

	pick, slug := tui.PickedIssue(final)
	switch pick {
	case tui.IssuePickNew:
		return nil, nil
	case tui.IssuePickRow:
		for i := range rows {
			if rows[i].IssueSlug == slug {
				return &rows[i], nil
			}
		}
	case tui.IssuePickNone:
	}

	return nil, huh.ErrUserAborted
}
```

In `tui/issue.go` delete `IssueTrackerToggle`, `IssueTrackerError`, the `issueTrackerPickerHeight` constant and the `.Height(issueTrackerPickerHeight)` line of `IssueTrackerPicker`; its doc comment becomes `// IssueTrackerPicker asks the branch type of a tracker issue; it lists the issues given (one, from the start flow) and the types.`

- [ ] **Step 6: Split the flow**

In `cmd/issueflow/start.go` replace everything from the `RunIssueStart` doc comment down to the end of `getFromTracker` (keep `publishRepoIssue`, `checkRecordType`, `issueFromRecord`, `fromImportedRecord`, `resolveUseWorktree` and what follows) with:

```go
// allowedTypes are the branch types this clone's commit types allow.
func allowedTypes(deps StartDeps) ([]string, error) {
	types := make([]string, 0, len(deps.Cfg.CommitTypes))
	for _, t := range deps.Cfg.CommitTypes {
		types = append(types, t.Name)
	}
	if len(types) == 0 {
		return nil, errors.New("config: no commit types found")
	}

	return types, nil
}

// RunIssueStart is the prompter-driven issue-start flow: the open issues
// are listed (ListRows), one is picked in the table (PickRow) or a new one
// is asked for in the manual form, and StartIssue creates its branch.
// Called from cmd/issue's startRunE and cmd/branch's newRunE with a
// HuhStartPrompter; tests call it with a scripted one. An empty table is
// skipped: the manual form opens at once.
//
// Returns nil on the non-error exits: the operator aborted at the confirm
// or in the conflict loop, or a test prompter returned no issue.
func RunIssueStart(ctx context.Context, deps StartDeps, prompter StartPrompter) error {
	types, err := allowedTypes(deps)
	if err != nil {
		return err
	}

	rows, err := ListRows(ctx, deps, issue.StateOpen)
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}

	var row *issue.Row
	if len(rows) > 0 {
		if row, err = prompter.PickRow(ctx, rows); err != nil {
			return fmt.Errorf("pick issue: %w", err)
		}
	}

	var picked *issue.Issue
	if row == nil {
		picked, err = newIssueFromUser(ctx, deps.Client, prompter, types)
	} else {
		picked, err = issueFromRow(ctx, deps, prompter, row, types)
	}
	if err != nil || picked == nil {
		return err
	}

	return StartIssue(ctx, deps, prompter, picked)
}

// StartRow starts the issue of a list row: `s` in `issue list`.
func StartRow(ctx context.Context, deps StartDeps, prompter StartPrompter, row *issue.Row) error {
	types, err := allowedTypes(deps)
	if err != nil {
		return err
	}

	picked, err := issueFromRow(ctx, deps, prompter, row, types)
	if err != nil || picked == nil {
		return err
	}

	return StartIssue(ctx, deps, prompter, picked)
}

// StartIssue creates the branch of a picked issue: base branch, worktree,
// confirm, create, record, push, tracker status.
func StartIssue(ctx context.Context, deps StartDeps, prompter StartPrompter, pickedIssue *issue.Issue) error {
	// When --parent was not explicitly set, offer a base branch picker so the
	// user can create a sub-task without knowing the flag exists. Candidates are
	// the union of local branches and remote-tracking branches: on a fresh clone
	// the parent integration branch exists only as origin/<name> (never checked
	// out locally), and it must still be offerable as a base — git.CreateBranch
	// resolves a remote-only base via its remote-tracking ref.
	if deps.Flags.ParentIssueSlug == "" {
		if candidates, brErr := baseBranchCandidates(deps.Client); brErr == nil && len(candidates) > 1 {
			defaultBase, dbErr := resolveDefaultBase(deps)
			if dbErr != nil {
				return fmt.Errorf("resolve default base: %w", dbErr)
			}
			baseBranch, pbErr := prompter.PickBaseBranch(ctx, defaultBase, candidates)
			if pbErr != nil {
				return fmt.Errorf("pick base branch: %w", pbErr)
			}
			deps.BaseBranchOverride = baseBranch
			deps.Flags.ParentIssueSlug = resolveParentSlug(baseBranch)
		}
	}

	useWorktree, err := resolveUseWorktree(ctx, deps, prompter)
	if err != nil {
		return err
	}

	creator := branchCreator
	if useWorktree {
		creator = worktreeCreator
	}

	return createFlow(ctx, deps, prompter, pickedIssue, creator)
}

// issueFromRow converts a picked row. A repo issue (State set) is resolved
// and goes through the record path; any other row is a tracker issue, whose
// type is asked with the one-issue form. The row's branch, if any, is
// ignored here: the conflict loop deals with an existing branch name.
func issueFromRow(
	ctx context.Context, deps StartDeps, p StartPrompter, row *issue.Row, types []string,
) (*issue.Issue, error) {
	if row.State == "" {
		src := tracker.Issue{TrackerType: deps.Cfg.IssueTracker.Type, ID: row.IssueSlug, Subject: row.Title}
		got, err := p.PickIssueFromTracker(ctx, []tracker.Issue{src}, types)
		if err != nil {
			return nil, fmt.Errorf("tracker issue form: %w", err)
		}
		if got == nil {
			return nil, nil
		}
		got.Issue = src

		return got, nil
	}

	// A branch started under another slug is joined to its record by the
	// full ID its chain carries.
	query := row.IssueSlug
	if row.Branch != nil && row.Branch.IssueID != "" {
		query = row.Branch.IssueID
	}
	rec, err := issue.Resolve(ctx, deps.Client, query)
	if err != nil {
		return nil, fmt.Errorf("resolve issue %s: %w", row.IssueSlug, err)
	}
	if rec.Tracker != nil && rec.Tracker.Born {
		return fromImportedRecord(ctx, p, &rec, types)
	}
	if err := checkRecordType(&rec, types); err != nil {
		return nil, err
	}

	return issueFromRecord(&rec), nil
}

// newIssueFromUser is the manual form. A typed ID is used as is, with no
// record (an issue living in a tracker git-zf does not talk to); an empty
// one prepares a new repo issue, which createFlow publishes once the branch
// exists, so every abort in between (base picker, confirm, branch conflict,
// git failure) leaves no issue behind.
func newIssueFromUser(ctx context.Context, c *git.Client, p StartPrompter, types []string) (*issue.Issue, error) {
	got, err := p.PickIssueFromUser(ctx, types)
	if err != nil {
		return nil, fmt.Errorf("issue input: %w", err)
	}
	if got == nil || got.ID != "" {
		return got, nil
	}

	id, err := issue.Prepare(ctx, c, issue.NewIssue{Title: got.Subject, BranchType: got.Type})
	if err != nil {
		return nil, fmt.Errorf("prepare repo issue: %w", err)
	}

	rec := issue.Record{ID: id}
	got.ID, got.RecordID, got.RecordPending = rec.DisplayID(), id, true

	return got, nil
}
```

Then `mise exec -- go build ./... && mise exec -- go vet ./...` and drop the imports the compiler reports unused (`slices` stays: `checkRecordType` uses it).

- [ ] **Step 7: Run the start suites**

Run: `mise exec -- go test ./cmd/issue/ -run "^TestRunIssueStart_|^TestStartRunE" -v 2>&1 | grep -E "^(=== RUN|--- FAIL|FAIL|ok|PASS)" | grep -v "=== RUN"`
Expected: `ok`, no FAIL. If `TestRunIssueStart_ImportedRecordStartsAsTrackerIssue` fails on the branch name, the row's slug is the record's display ID ("42"): check `PickSlug` uses `imported.DisplayID()`.

- [ ] **Step 8: Run everything and commit**

Run: `mise exec -- go test ./... 2>&1 | tail -30` — expected all `ok`.

```bash
mise exec -- gofmt -l . && git add -A cmd tui && git commit -m "feat(issue): issue start and branch new pick in the issue table"
```

---

### Task 5: `StartRow` end to end

**Files:**
- Create: `cmd/issue/start_row_e2e_test.go`

**Interfaces:**
- Consumes: `issueflow.StartRow`, `issueflow.ListRows` (Tasks 3-4), `startTestRig`, `scriptedStartPrompter`.

- [ ] **Step 1: Write the tests**

```go
package issue

import (
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// rowOf lists the open rows of the rig and returns the one with slug.
func rowOf(t *testing.T, rig *startTestRig, flags issuepkg.IssueStartFlags, slug string) *issuepkg.Row {
	t.Helper()

	rows, err := issueflow.ListRows(t.Context(), rig.deps(flags), "")
	if err != nil {
		t.Fatalf("ListRows: %v", err)
	}
	for i := range rows {
		if rows[i].IssueSlug == slug {
			return &rows[i]
		}
	}
	t.Fatalf("no row %q among %+v", slug, rows)

	return nil
}

// s in issue list on a tracker issue: the type form, then a tracker branch.
func TestStartRow_TrackerRow(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rig.tracker.ProjectIssues = []tracker.Issue{{ID: "ABC-3", Subject: "Implement OAuth", TrackerType: "fake"}}
	row := rowOf(t, rig, flags, "ABC-3")

	prompter := &scriptedStartPrompter{
		IssueFromTracker: &issuepkg.Issue{Type: "feat"},
		ConfirmBranch:    true,
		TrackerStatus:    "In Progress",
	}

	err := issueflow.StartRow(t.Context(), rig.deps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("StartRow: %v", err)
		}
	})
	t.Run("the branch is named after the tracker issue and the picked type", func(t *testing.T) {
		exists, err := rig.client.BranchExists("ABC-3@feat@implement-oauth")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the branch chain records the tracker origin", func(t *testing.T) {
		ref, err := branch.Load(t.Context(), rig.client, "ABC-3")
		if err != nil || ref == nil || ref.TrackerType != "fake" {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("the tracker status was updated", func(t *testing.T) {
		if len(rig.tracker.RecordedUpdates) != 1 || rig.tracker.RecordedUpdates[0].IssueID != "ABC-3" {
			t.Errorf("updates = %+v", rig.tracker.RecordedUpdates)
		}
	})
}

// s on a repo issue: the record's type names the branch, no form asked.
func TestStartRow_RepoRow(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Open one", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	row := rowOf(t, rig, flags, rec.ShortID())

	prompter := &scriptedStartPrompter{ConfirmBranch: true}

	runErr := issueflow.StartRow(ctx, rig.deps(flags), prompter, row)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the branch is created with the record's type", func(t *testing.T) {
		exists, err := rig.client.BranchExists(rec.ShortID() + "@fix@open-one")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("the branch ref records the full issue ID", func(t *testing.T) {
		ref, err := branch.Load(ctx, rig.client, rec.ShortID())
		if err != nil || ref == nil || ref.IssueID != rec.ID {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
	t.Run("the table was not opened", func(t *testing.T) {
		if prompter.OfferedRows != nil {
			t.Errorf("offered = %+v", prompter.OfferedRows)
		}
	})
}

// Review Focus 1. A branch started under another slug is joined to its
// record by the chain's issue ID: starting that row resolves the record by
// that ID, and the conflict loop sees the existing name.
func TestStartRow_RowJoinedByIssueID(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()
	flags := issuepkg.IssueStartFlags{Tracker: true}
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Renamed", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: "other-slug@fix@renamed", Title: "Renamed", IssueID: rec.ID}, branch.StatusInProgress)
	row := rowOf(t, rig, flags, "other-slug")

	prompter := &scriptedStartPrompter{ConfirmBranch: true, ConflictAbort: true}

	runErr := issueflow.StartRow(ctx, rig.deps(flags), prompter, row)

	t.Run("the record is found by the chain's issue ID: no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("StartRow: %v", runErr)
		}
	})
	t.Run("the abort in the conflict loop creates nothing", func(t *testing.T) {
		if out := gitOutput(t, rig.dir, "branch", "--format=%(refname:short)"); out != "main" {
			t.Errorf("branches = %q, want only main", out)
		}
	})
}
```

- [ ] **Step 2: Run them**

Run: `mise exec -- go test ./cmd/issue/ -run "^TestStartRow_" -v`
Expected: PASS. If `TestStartRow_RowJoinedByIssueID` fails with `resolve issue other-slug`, `issueFromRow` is not using `row.Branch.IssueID` (Task 4 step 6).

- [ ] **Step 3: Commit**

```bash
git add cmd/issue/start_row_e2e_test.go && git commit -m "test(issue): StartRow from a tracker row, a repo row, a row joined by ID"
```

---

### Task 6: `s` in `issue list` starts the issue

**Files:**
- Modify: `cmd/issue/list.go` (`issueListRunE` lines 60-86, `runList` lines 88-141)
- Test: `cmd/issue/issue_test.go` (`TestRunIssueList`), `cmd/issue/list_record_test.go` (the `runList` calls)

**Interfaces:**
- Consumes: `tui.PickedIssue`, `issueflow.StartRow`, `issueListInfra.deps()` (Tasks 1, 3, 4).
- Produces: `func runList(ctx, w io.Writer, infra issueListInfra, flags issueListFlags) (*issuepkg.Row, error)` — the row picked with `s`, nil when none.

- [ ] **Step 1: Write the failing test**

In `cmd/issue/issue_test.go`, `TestRunIssueList`: change every `if err := runList(` to `if _, err := runList(`; same in `cmd/issue/list_record_test.go` (three calls). Add a subtest to `TestRunIssueList`:

```go
	t.Run("the stdout and json outputs pick nothing", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		branchtest.Seed(t, s, branch.Op{Branch: "P-1@feat@pick@uuid-p1", Title: "Pick"}, branch.StatusInProgress)
		infra := issueListInfra{client: s, tracker: nil, stderr: &bytes.Buffer{}}
		for name, flags := range map[string]issueListFlags{"json": {jsonOut: true}, "stdout": {stdout: true}} {
			t.Run(name, func(t *testing.T) {
				row, err := runList(t.Context(), &bytes.Buffer{}, infra, flags)
				if err != nil || row != nil {
					t.Errorf("row = %+v, err = %v", row, err)
				}
			})
		}
	})
```

- [ ] **Step 2: Run to verify it fails**

Run: `mise exec -- go vet ./cmd/issue/`
Expected: `assignment mismatch: 2 variables but runList returns 1 value`.

- [ ] **Step 3: Implement**

`runList` returns `(*issuepkg.Row, error)`: every `return nil` becomes `return nil, nil`, every `return fmt.Errorf(...)` becomes `return nil, fmt.Errorf(...)`, and the program run at the end becomes:

```go
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return nil, fmt.Errorf("run table: %w", err)
	}
	if pick, slug := tui.PickedIssue(final); pick == tui.IssuePickRow {
		for i := range rows {
			if rows[i].IssueSlug == slug {
				return &rows[i], nil
			}
		}
	}

	return nil, nil
```

Update its doc comment: `// runList prints or shows the issues. It returns the row picked with s in the table, nil when none: the stdout and JSON outputs never pick.`

`issueListRunE` ends with:

```go
	row, err := runList(ctx, os.Stdout, infra, flags)
	if err != nil || row == nil {
		return err
	}

	// The table's program has exited: the start forms get the terminal.
	deps := infra.deps()
	deps.Cfg = ir.appConfig

	return issueflow.StartRow(ctx, deps, issueflow.NewHuhStartPrompter(), row)
```

- [ ] **Step 4: Run the list tests**

Run: `mise exec -- go test ./cmd/issue/ -run "TestRunIssueList|TestRunList|TestBuildRows|TestMirror_List" -v 2>&1 | grep -E "^(--- FAIL|FAIL|ok)"`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
mise exec -- gofmt -l cmd && git add cmd/issue && git commit -m "feat(issue): s in issue list starts the issue under the cursor"
```

---

### Task 7: the record picker becomes the table

**Files:**
- Modify: `cmd/issue/record.go` (`PickRecord` lines 55-62)
- Modify: `tui/issue_record.go` (delete `IssueRecordNew`, `issueRecordPickerHeight`, `IssueRecordPicker`)

**Interfaces:**
- Consumes: `issue.RecordRow` (Task 2), `tui.IssueTableModel` pick mode without `NewIssue` (Task 1).
- `recordPrompter.PickRecord(ctx, records) (string, error)` and `scriptedRecordPrompter` unchanged.

- [ ] **Step 1: Confirm the tests that pin this**

The table side is `TestIssueTable_Pick` ("n in pick mode without NewIssue does nothing", Task 1); the rows side is `TestRecordRow` (Task 2). The huh implementation itself needs a terminal and has no test, as `huhRecordPrompter` never had.

- [ ] **Step 2: Implement**

Replace `PickRecord` in `cmd/issue/record.go` (imports: add `tea "github.com/charmbracelet/bubbletea"`):

```go
// PickRecord opens the issue table over records, every state, and returns
// the full ID of the one picked with enter or s. q is huh.ErrUserAborted.
func (huhRecordPrompter) PickRecord(ctx context.Context, records []issuepkg.Record) (string, error) {
	rows := make([]issuepkg.Row, len(records))
	for i := range records {
		rows[i] = issuepkg.RecordRow(&records[i])
	}
	m, err := tui.IssueTableModel(rows, "all", tui.IssueActions{Pick: true})
	if err != nil {
		return "", fmt.Errorf("issue picker: %w", err)
	}
	final, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if err != nil {
		return "", fmt.Errorf("issue picker: %w", err)
	}
	if pick, slug := tui.PickedIssue(final); pick == tui.IssuePickRow {
		for i := range records {
			if records[i].DisplayID() == slug {
				return records[i].ID, nil
			}
		}
	}

	return "", fmt.Errorf("issue picker: %w", huh.ErrUserAborted)
}
```

In `tui/issue_record.go` delete the `IssueRecordNew` constant and its comment, the `issueRecordPickerHeight` constant, and the `IssueRecordPicker` function; drop the `slices` import if the compiler reports it unused.

- [ ] **Step 3: Build, vet, run the record suites**

Run: `mise exec -- go build ./... && mise exec -- go vet ./... && mise exec -- go test ./tui/ ./cmd/issue/ -run "TestIssueTable|^TestRun(New|Show|Edit|Comment|Label|Sync|CloseByID)|^TestMirror" 2>&1 | tail -5`
Expected: `ok` for both packages. `grep -rn --exclude-dir=wt "IssueRecordPicker\|IssueRecordNew" --include=*.go .` prints nothing.

- [ ] **Step 4: Commit**

```bash
mise exec -- gofmt -l . && git add cmd/issue/record.go tui/issue_record.go && git commit -m "feat(issue): the ID-less record picker is the issue table"
```

---

### Task 8: docs and the final check

**Files:**
- Modify: `CLAUDE.md` ("Testing the start flow" section, lines 46-59)

- [ ] **Step 1: Update the section**

Replace the section's body with:

```markdown
### Testing the start flow

The issue-start flow (used by `issue start`, `branch new` and `s` in
`issue list`) is end-to-end tested in `cmd/issue/start_e2e_test.go`,
`start_record_e2e_test.go` and `start_row_e2e_test.go`. The pattern mirrors
the close-flow tests: a real on-disk repo, the fake tracker at
`tracker/fake/`, and a `scriptedStartPrompter` that returns canned answers
for every form. The picker is the `issue list` table over
`issueflow.ListRows`: a script picks a row by slug (`PickSlug`), asks for a
new issue (`PickNew`) and reads what it was offered in `OfferedRows`. An
empty table is skipped: the manual form opens at once.

To exercise just the start-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_|^TestStartRow_" -v

`branch new` lists the branches and the repo issues alone
(`IssueStartFlags.Tracker` false); `issue start` and `issue list` list the
tracker too. When adding a toggle, confirm or picker form, extend
`StartPrompter` and add a matching E2E test alongside the existing
happy-path and failure-mode tests.
```

- [ ] **Step 2: Full verification**

Run: `mise exec -- gofmt -l . ; mise exec -- go vet ./... && mise exec -- go test ./... 2>&1 | tail -40`
Expected: gofmt prints nothing, vet is silent, every package `ok`.

- [ ] **Step 3: Scope check and commit**

Run GitNexus `detect_changes({scope: "compare", base_ref: "main"})` and confirm the affected flows are the issue start, list and record-picker flows only.

```bash
git add CLAUDE.md && git commit -m "docs: the start flow picks in the issue table"
```
