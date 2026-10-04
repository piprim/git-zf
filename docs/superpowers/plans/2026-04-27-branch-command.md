# `git cz branch` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `git cz branch` with `list` (interactive bubbles/table + `--json`/`--stdout` non-TUI paths) and `new` (manual-input form, delegates to `issueStartRunE`) subcommands; `merge` is stubbed.

**Architecture:** The store gains a typed `BranchStatus` enum and `ListBranches` read method (three-table JOIN). `tui/branch.go` adds action-selector and status-filter `huh` groups. `cmd/branch.go` wires the full command tree; the testable core is `runBranchList(ctx, w, store, flags)`. `branchNewRunE` delegates directly to `issueStartRunE` — no new form logic.

**Tech Stack:** Go, `charmbracelet/huh`, `charmbracelet/bubbles/table` (BubbleTea interactive table), `charmbracelet/lipgloss/table` (static stdout table), `modernc.org/sqlite`, Cobra.

**Spec:** `docs/superpowers/specs/2026-04-27-branch-list-design.md`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `store/store.go` | Modify | Add `BranchStatus`, `BranchRow`, `ListBranches` |
| `store/store_test.go` | Modify | Add 5 `ListBranches` tests |
| `tui/branch.go` | Create | `BranchActionSelect`, `BranchStatusFilter` |
| `cmd/branch.go` | Create | Full `branch` command tree + `runBranchList` helper |
| `cmd/branch_test.go` | Create | Tests for `--json` and `--stdout` paths |
| `cmd/root.go` | Modify | Register `getBranchCmd()` |

---

## Task 1: Store data layer — `BranchStatus`, `BranchRow`, `ListBranches`

**Files:**
- Modify: `store/store.go`
- Test: `store/store_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `store/store_test.go` (after the existing tests):

```go
func TestListBranches_all(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "A-1", Title: "First", StatusID: 1},
		&Branch{UUID: "uuid-1", Name: "A-1@feat@first@uuid-1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "A-2", Title: "Second", StatusID: 1},
		&Branch{UUID: "uuid-2", Name: "A-2@fix@second@uuid-2", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("got %d rows, want 2", len(rows))
	}
}

func TestListBranches_filterInProgress(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "B-1", Title: "In progress issue", StatusID: 1},
		&Branch{UUID: "uuid-ip", Name: "B-1@feat@in-progress@uuid-ip", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert in_progress: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusInProgress)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != BranchStatusInProgress {
		t.Errorf("Status = %q, want %q", rows[0].Status, BranchStatusInProgress)
	}
}

func TestListBranches_filterMerged(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "C-1", Title: "Merged issue", StatusID: 1},
		&Branch{UUID: "uuid-mg", Name: "C-1@feat@merged@uuid-mg", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	now := time.Now()
	if err := s.UpdateBranchStatus(t.Context(), "uuid-mg", 2, &now); err != nil {
		t.Fatalf("UpdateBranchStatus: %v", err)
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusMerged)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].Status != BranchStatusMerged {
		t.Errorf("Status = %q, want %q", rows[0].Status, BranchStatusMerged)
	}
}

func TestListBranches_empty(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestListBranches_order(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	// Insert two branches with explicit created_at values to test DESC ordering.
	for _, row := range []struct {
		uuid, slug, name, tp, dt string
	}{
		{"uuid-old", "D-1", "D-1@feat@old@uuid-old", "feat", "2025-01-01 00:00:00"},
		{"uuid-new", "D-2", "D-2@feat@new@uuid-new", "feat", "2025-06-01 00:00:00"},
	} {
		if _, err := s.db.ExecContext(t.Context(),
			`INSERT INTO issues (id_slug, title, status_id) VALUES (?, 'T', 1)`, row.slug,
		); err != nil {
			t.Fatalf("insert issue: %v", err)
		}

		var issueID int64
		if err := s.db.QueryRowContext(t.Context(),
			"SELECT last_insert_rowid()",
		).Scan(&issueID); err != nil {
			t.Fatalf("last insert id: %v", err)
		}

		if _, err := s.db.ExecContext(t.Context(),
			`INSERT INTO branches (uuid, name, issue_id, type, status_id, created_at)
			 VALUES (?, ?, ?, ?, 1, ?)`,
			row.uuid, row.name, issueID, row.tp, row.dt,
		); err != nil {
			t.Fatalf("insert branch: %v", err)
		}
	}

	rows, err := s.ListBranches(t.Context(), BranchStatusAll)
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].IssueSlug != "D-2" {
		t.Errorf("first row (newest) = %q, want %q", rows[0].IssueSlug, "D-2")
	}
	if rows[1].IssueSlug != "D-1" {
		t.Errorf("second row (oldest) = %q, want %q", rows[1].IssueSlug, "D-1")
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
cd /workspace && go test ./store/... -run "TestListBranches" -v
```

Expected: `undefined: BranchStatus`, `undefined: BranchStatusAll`, etc.

- [ ] **Step 3: Add `BranchStatus`, `BranchRow`, and `ListBranches` to `store/store.go`**

Add after the `Branch` struct (before `Open`):

```go
// BranchStatus is the typed string representation of the statuses table.
type BranchStatus string

const (
	BranchStatusInProgress BranchStatus = "in_progress"
	BranchStatusMerged     BranchStatus = "merged"
	BranchStatusAll        BranchStatus = "" // sentinel: no WHERE filter; not a DB value
)

// BranchRow is the joined result of one branch with its parent issue and status.
type BranchRow struct {
	IssueSlug  string       `json:"issue_slug"`
	Title      string       `json:"title"`
	BranchName string       `json:"branch_name"`
	Type       string       `json:"type"`
	Status     BranchStatus `json:"status"`
	CreatedAt  time.Time    `json:"created_at"`
}
```

Then add the `ListBranches` method after `UpdateIssueStatus` (before `migrate`):

```go
// ListBranches returns all branches joined with their issue and status,
// ordered by created_at DESC. BranchStatusAll returns every row.
func (s *Store) ListBranches(ctx context.Context, status BranchStatus) ([]BranchRow, error) {
	q := `
		SELECT i.id_slug, i.title, b.name, b.type, st.name, b.created_at
		FROM branches b
		JOIN issues i ON b.issue_id = i.id
		JOIN statuses st ON b.status_id = st.id`

	var args []any
	if status != BranchStatusAll {
		q += " WHERE st.name = ?"
		args = append(args, string(status))
	}
	q += " ORDER BY b.created_at DESC"

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list branches query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []BranchRow

	for rows.Next() {
		var r BranchRow
		var createdAtStr string

		if err := rows.Scan(
			&r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}

		r.CreatedAt, err = time.Parse("2006-01-02 15:04:05", createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse branch created_at %q: %w", createdAtStr, err)
		}

		result = append(result, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	if result == nil {
		result = []BranchRow{}
	}

	return result, nil
}
```

Note: `createdAtStr` is used because SQLite stores `CURRENT_TIMESTAMP` as text `"2006-01-02 15:04:05"`. The `modernc.org/sqlite` driver does not auto-parse this into `time.Time`.

- [ ] **Step 4: Run the tests to confirm they pass**

```bash
cd /workspace && go test ./store/... -run "TestListBranches" -v
```

Expected: all 5 tests PASS.

- [ ] **Step 5: Run the full test suite to check for regressions**

```bash
cd /workspace && go test ./store/...
```

Expected: all existing tests still PASS.

- [ ] **Step 6: Commit**

```bash
git add store/store.go store/store_test.go
git commit -m "feat(store): add BranchStatus enum and ListBranches query"
```

---

## Task 2: TUI branch forms

**Files:**
- Create: `tui/branch.go`

- [ ] **Step 1: Create `tui/branch.go`**

```go
package tui

import "github.com/charmbracelet/huh"

const (
	BranchActionNameList  = "branchList"
	BranchActionNameNew   = "branchNew"
	BranchActionNameMerge = "branchMerge"
)

// BranchActionSelect presents the list of available branch actions.
func BranchActionSelect(action *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Branch action:").
			Options(
				huh.NewOption("List\n"+descStyle.Render("List branches by status"), BranchActionNameList),
				huh.NewOption("New\n"+descStyle.Render("Create a new branch (manual input)"), BranchActionNameNew),
				huh.NewOption("Merge\n"+descStyle.Render("Merge a branch"), BranchActionNameMerge),
			).
			Value(action),
	)
}

// BranchStatusFilter presents a status filter for the branch list.
// selected is the pre-selected value ("in_progress", "merged", or "all"); defaults to "in_progress" when empty.
func BranchStatusFilter(status *string, selected string) *huh.Group {
	*status = selected
	if *status == "" {
		*status = "in_progress"
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Filter by status:").
			Options(
				huh.NewOption("In progress", "in_progress"),
				huh.NewOption("Merged", "merged"),
				huh.NewOption("All", "all"),
			).
			Value(status),
	)
}
```

`descStyle` is the package-level `lipgloss.Style` defined in `tui/commit.go` — accessible within the same package.

- [ ] **Step 2: Verify the build**

```bash
cd /workspace && go build ./tui/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add tui/branch.go
git commit -m "feat(tui): add BranchActionSelect and BranchStatusFilter"
```

---

## Task 3: `cmd/branch.go` — skeleton, non-TUI paths, tests, root registration

**Files:**
- Create: `cmd/branch.go`
- Create: `cmd/branch_test.go`
- Modify: `cmd/root.go`

This task wires up the complete command structure and the two non-TUI output paths (`--json`, `--stdout`). The TUI path in `branchListRunE` is completed in Task 4 — for now it prints "TUI not yet implemented." so the build and tests pass.

- [ ] **Step 1: Write the failing tests**

Create `cmd/branch_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func openTestBranchStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestBranchList_json(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	if err := s.InsertIssueWithBranch(context.Background(),
		&store.Issue{IDSlug: "ABC-42", Title: "Add OAuth login", StatusID: 1},
		&store.Branch{UUID: "550e8400", Name: "ABC-42@feat@add-oauth-login@550e8400", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var buf bytes.Buffer
	if err := runBranchList(context.Background(), &buf, s, branchListFlags{jsonOut: true}); err != nil {
		t.Fatalf("runBranchList: %v", err)
	}

	var rows []store.BranchRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].IssueSlug != "ABC-42" {
		t.Errorf("IssueSlug = %q, want ABC-42", rows[0].IssueSlug)
	}
	if rows[0].BranchName != "ABC-42@feat@add-oauth-login@550e8400" {
		t.Errorf("BranchName = %q", rows[0].BranchName)
	}
	if string(rows[0].Status) != "in_progress" {
		t.Errorf("Status = %q, want in_progress", rows[0].Status)
	}
}

func TestBranchList_json_emptyStore(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)

	var buf bytes.Buffer
	if err := runBranchList(context.Background(), &buf, s, branchListFlags{jsonOut: true}); err != nil {
		t.Fatalf("runBranchList: %v", err)
	}

	var rows []store.BranchRow
	if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}

func TestBranchList_stdout(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)
	if err := s.InsertIssueWithBranch(context.Background(),
		&store.Issue{IDSlug: "XY-1", Title: "Some feature", StatusID: 1},
		&store.Branch{UUID: "aabbccdd", Name: "XY-1@feat@some-feature@aabbccdd", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var buf bytes.Buffer
	if err := runBranchList(context.Background(), &buf, s, branchListFlags{stdout: true}); err != nil {
		t.Fatalf("runBranchList: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "ISSUE ID") {
		t.Errorf("output missing ISSUE ID header: %q", out)
	}
	if !strings.Contains(out, "XY-1") {
		t.Errorf("output missing issue slug XY-1: %q", out)
	}
}

func TestBranchList_stdout_emptyStore(t *testing.T) {
	t.Parallel()

	s := openTestBranchStore(t)

	var buf bytes.Buffer
	if err := runBranchList(context.Background(), &buf, s, branchListFlags{stdout: true}); err != nil {
		t.Fatalf("runBranchList: %v", err)
	}

	if !strings.Contains(buf.String(), "No branches found.") {
		t.Errorf("expected 'No branches found.', got: %q", buf.String())
	}
}
```

- [ ] **Step 2: Run the tests to confirm they fail**

```bash
cd /workspace && go test ./cmd/... -run "TestBranchList" -v
```

Expected: `undefined: runBranchList`, `undefined: branchListFlags`

- [ ] **Step 3: Create `cmd/branch.go`**

```go
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	btable "github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	lgtable "github.com/charmbracelet/lipgloss/table"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

type branchListFlags struct {
	status  string
	stdout  bool
	jsonOut bool
}

func getBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Manage local branches",
		RunE:  branchRunE,
	}
	cmd.AddCommand(getBranchListCmd(), getBranchNewCmd(), getBranchMergeCmd())

	return cmd
}

func branchRunE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.BranchActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.BranchActionNameList:
		return branchListRunE(cmd, args)
	case tui.BranchActionNameNew:
		return branchNewRunE(cmd, args)
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}

func getBranchListCmd() *cobra.Command {
	var flags branchListFlags

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List branches",
		RunE: func(cmd *cobra.Command, args []string) error {
			return branchListRunE(cmd, args)
		},
	}

	f := cmd.Flags()
	f.StringVar(&flags.status, "status", "", "filter by status: in_progress, merged, all")
	f.BoolVar(&flags.stdout, "stdout", false, "print table to stdout without TUI")
	f.BoolVar(&flags.jsonOut, "json", false, "print JSON array to stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		client, err := git.NewClient()
		if err != nil {
			return fmt.Errorf("not a git repository: %w", err)
		}

		root, err := client.WorkingTreeRoot()
		if err != nil {
			return fmt.Errorf("working tree root: %w", err)
		}

		s, err := store.Open(cmd.Context(), filepath.Join(root, ".git"))
		if err != nil {
			log.Printf("open store: %v", err)

			return fmt.Errorf("open store: %w", err)
		}
		defer func() { _ = s.Close() }()

		return runBranchList(cmd.Context(), os.Stdout, s, flags)
	}

	return cmd
}

// runBranchList executes the branch list logic. w receives stdout/non-TUI output.
// When neither --json nor --stdout is set, runBranchList runs the interactive TUI.
func runBranchList(ctx context.Context, w io.Writer, s *store.Store, flags branchListFlags) error {
	queryStatus := toBranchStatus(flags.status)

	if flags.jsonOut {
		rows, err := s.ListBranches(ctx, queryStatus)
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if flags.stdout {
		rows, err := s.ListBranches(ctx, queryStatus)
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		if len(rows) == 0 {
			fmt.Fprintln(w, "No branches found.")

			return nil
		}
		renderLipglossTable(w, rows)

		return nil
	}

	// TUI path — completed in Task 4.
	fmt.Fprintln(w, "TUI not yet implemented.")

	return nil
}

func renderLipglossTable(w io.Writer, rows []store.BranchRow) {
	t := lgtable.New().
		Headers("ISSUE ID", "TITLE", "BRANCH", "TYPE", "STATUS", "CREATED").
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for _, r := range rows {
		t.Row(r.IssueSlug, r.Title, r.BranchName, r.Type, string(r.Status), r.CreatedAt.Format("2006-01-02"))
	}

	fmt.Fprintln(w, t.Render())
}

func toBranchStatus(s string) store.BranchStatus {
	switch s {
	case "in_progress":
		return store.BranchStatusInProgress
	case "merged":
		return store.BranchStatusMerged
	default:
		return store.BranchStatusAll
	}
}

// branchTableModel wraps bubbles/table as a minimal Bubble Tea program.
// Placeholder until Task 4 wires the TUI path.
type branchTableModel struct {
	table btable.Model
}

func (m branchTableModel) Init() tea.Cmd { return nil }

func (m branchTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

func (m branchTableModel) View() string {
	return m.table.View() + "\n\nPress q to quit."
}

func getBranchNewCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "new",
		Short: "Create a new branch (manual input)",
		Long:  "Enter issue details manually, then a named branch is created and checked out.",
		RunE:  branchNewRunE,
	}
}

// branchNewRunE delegates to issueStartRunE (manual input form).
// Step 2b: add toggle to tracker-picker.
func branchNewRunE(cmd *cobra.Command, args []string) error {
	return issueStartRunE(cmd, args)
}

func getBranchMergeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "merge",
		Short: "Merge a branch",
		RunE:  branchMergeRunE,
	}
}

func branchMergeRunE(_ *cobra.Command, _ []string) error {
	fmt.Println("Not yet implemented.")

	return nil
}
```

- [ ] **Step 4: Register `getBranchCmd()` in `cmd/root.go`**

In `cmd/root.go`, change the line:

```go
rootCmd.AddCommand(getCommitCmd(), getIssueCmd(), getVersionCmd(), getInstallCmd())
```

to:

```go
rootCmd.AddCommand(getCommitCmd(), getIssueCmd(), getBranchCmd(), getVersionCmd(), getInstallCmd())
```

- [ ] **Step 5: Run the tests**

```bash
cd /workspace && go test ./cmd/... -run "TestBranchList" -v
```

Expected: all 4 tests PASS.

- [ ] **Step 6: Build to verify no compile errors**

```bash
cd /workspace && go build ./...
```

Expected: success.

- [ ] **Step 7: Commit**

```bash
git add cmd/branch.go cmd/branch_test.go cmd/root.go
git commit -m "feat(cmd): add git cz branch command with list --json/--stdout and new/merge stubs"
```

---

## Task 4: `cmd/branch.go` — complete the TUI path (bubbles/table)

**Files:**
- Modify: `cmd/branch.go`

This task replaces the `"TUI not yet implemented."` placeholder in `runBranchList` with the full interactive flow: `huh` status filter → `bubbles/table` interactive viewer.

- [ ] **Step 1: Replace the TUI placeholder in `runBranchList`**

In `cmd/branch.go`, replace the TUI placeholder section:

```go
	// TUI path — completed in Task 4.
	fmt.Fprintln(w, "TUI not yet implemented.")

	return nil
```

with:

```go
	// TUI path: status filter then interactive table.
	statusStr := flags.status
	if err := huh.NewForm(tui.BranchStatusFilter(&statusStr, statusStr)).Run(); err != nil {
		return fmt.Errorf("status filter: %w", err)
	}

	queryStatus = toBranchStatus(statusStr)

	rows, err := s.ListBranches(ctx, queryStatus)
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No branches found.")

		return nil
	}

	return runBranchTable(rows)
```

- [ ] **Step 2: Add `runBranchTable` function**

Add after `renderLipglossTable` in `cmd/branch.go`:

```go
func runBranchTable(rows []store.BranchRow) error {
	cols := []btable.Column{
		{Title: "Issue ID", Width: 10},
		{Title: "Title", Width: 28},
		{Title: "Branch", Width: 38},
		{Title: "Type", Width: 8},
		{Title: "Status", Width: 12},
		{Title: "Created", Width: 10},
	}

	tableRows := make([]btable.Row, len(rows))
	for i, r := range rows {
		tableRows[i] = btable.Row{
			r.IssueSlug,
			r.Title,
			r.BranchName,
			r.Type,
			string(r.Status),
			r.CreatedAt.Format("2006-01-02"),
		}
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(tableRows),
		btable.WithFocused(true),
		btable.WithHeight(20),
	)

	s := btable.DefaultStyles()
	s.Header = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("63")).Padding(0, 1)
	t.SetStyles(s)

	if _, err := tea.NewProgram(branchTableModel{table: t}).Run(); err != nil {
		return fmt.Errorf("run table: %w", err)
	}

	return nil
}
```

- [ ] **Step 3: Remove the now-unused `_ = w` or check that `w` is still used**

The `w` parameter in `runBranchList` is used in the `flags.jsonOut`, `flags.stdout`, and empty-result paths. Verify `w` is not unused:

After the TUI path change, `runBranchTable` does not use `w`. But `fmt.Fprintln(w, "No branches found.")` still uses it. `w` is fine.

- [ ] **Step 4: Build**

```bash
cd /workspace && go build ./...
```

Expected: success.

- [ ] **Step 5: Run all tests to check for regressions**

```bash
cd /workspace && go test ./...
```

Expected: all tests PASS (TUI path is not tested automatically).

- [ ] **Step 6: Commit**

```bash
git add cmd/branch.go
git commit -m "feat(cmd): wire TUI path for git cz branch list (bubbles/table)"
```

---

## Task 5: Final wiring — verify `git cz branch new` end-to-end

**Files:**
- No code changes needed — `branchNewRunE` already delegates to `issueStartRunE`.

This task is a manual smoke-test and documentation checkpoint.

- [ ] **Step 1: Run the full test suite**

```bash
cd /workspace && go test ./... -v 2>&1 | tail -30
```

Expected: all tests PASS. Note the count.

- [ ] **Step 2: Build the binary**

```bash
cd /workspace && go build -o commitizen-go .
```

Expected: success.

- [ ] **Step 3: Smoke-test `--help` output**

```bash
./commitizen-go branch --help
./commitizen-go branch list --help
./commitizen-go branch new --help
./commitizen-go branch merge --help
```

Expected output for `branch --help`: shows `list`, `new`, `merge` subcommands.
Expected output for `branch list --help`: shows `--status`, `--stdout`, `--json` flags.

- [ ] **Step 4: Smoke-test `branch merge` stub**

```bash
./commitizen-go branch merge
```

Expected: prints `Not yet implemented.` and exits 0.

- [ ] **Step 5: Commit**

```bash
git add commitizen-go   # only if you want to track the binary; otherwise skip
git commit -m "feat: git cz branch list+new complete, merge stubbed" --allow-empty
```

(Use `--allow-empty` only if there are no file changes. If the binary is gitignored, skip the `git add` and omit `--allow-empty`.)

---

## Self-Review Checklist

**Spec coverage:**
- [x] Section 1: command structure — all commands and flags present
- [x] Section 2: `BranchStatus`, `BranchRow`, `ListBranches` — Task 1
- [x] Section 3: `BranchActionSelect`, `BranchStatusFilter` — Task 2
- [x] Section 4: `branchListRunE`, `branchNewRunE`, `branchMergeRunE` — Tasks 3–5
- [x] Section 5: TUI table, lipgloss table, JSON, empty state — Tasks 3–4
- [x] Section 6: file structure matches
- [x] Section 7: all 5 store tests + 4 cmd tests included
- [x] `git cz branch new` toggle note — `// Step 2b:` comment in `branchNewRunE`
- [x] `cmd/root.go` registration — Task 3 Step 4

**Type consistency:**
- `branchListFlags.jsonOut` is used consistently (avoids shadowing the `encoding/json` import)
- `store.BranchStatus` / `store.BranchStatusAll` / `store.BranchStatusInProgress` / `store.BranchStatusMerged` used consistently across Task 1 tests and `cmd/branch.go`
- `tui.BranchActionNameList` / `tui.BranchActionNameNew` / `tui.BranchActionNameMerge` used in `branchRunE` switch

**nlreturn compliance:** blank lines before all `return` statements that are not the sole statement in their block.

**wrapcheck compliance:** all errors from external packages (`store`, `git`, `huh`, `tea`) wrapped with `fmt.Errorf("context: %w", err)`.
