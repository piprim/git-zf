# Issue List Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement `git zf issue list` — shows open issues (from the tracker when configured, local store otherwise) enriched with local branch data in a six-column table.

**Architecture:** Tracker-first / local-fallback. `buildIssueRows` merges `tracker.ListIssues` output with a single batched store lookup by issue slug. All display helpers (`renderIssueTable`, `runIssueTableTUI`) mirror the existing `branch list` pattern exactly.

**Tech Stack:** Go, `charmbracelet/huh`, `charmbracelet/bubbles/table`, `charmbracelet/lipgloss/table`, `modernc.org/sqlite`.

---

## File map

| File | Change |
|------|--------|
| `store/store.go` | Add `IssueRow` struct; add `ListBranchesByIssueSlugs` method; add `strings` import |
| `store/store_test.go` | Add `TestListBranchesByIssueSlugs_*` tests |
| `tui/issue.go` | Add `IssueStatusFilter` function |
| `tui/issue_test.go` | Create; add `TestIssueStatusFilter_*` tests |
| `cmd/issue.go` | Add `issueListFlags`, `issueListInfra`, helpers, `buildIssueRows`, `buildFromTracker`, `buildFromStore`, `toIssueStoreStatus`, `renderIssueTable`, `branchFieldOrEmpty`, `trackerStatusOrNA`, `runIssueList`, `runIssueTableTUI`, `issueTableModel`, `getIssueListCmd`, `issueListRunE`; wire into `getIssueCmd` and `issueRunE` |
| `cmd/issue_test.go` | Add `fakeIssueTracker`, `openTestIssueStore`, `TestBuildIssueRows_*`, `TestRunIssueList_*` |

---

## Task 1: `store.IssueRow` and `ListBranchesByIssueSlugs`

**Files:**
- Modify: `store/store.go`
- Modify: `store/store_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `store/store_test.go`:

```go
func TestListBranchesByIssueSlugs_match(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "ABC-1", Title: "First", StatusID: 1},
		&Branch{UUID: "uuid-s1", Name: "ABC-1@feat@first@uuid-s1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "ABC-2", Title: "Second", StatusID: 1},
		&Branch{UUID: "uuid-s2", Name: "ABC-2@fix@second@uuid-s2", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := s.ListBranchesByIssueSlugs(t.Context(), []string{"ABC-1", "ABC-2", "ABC-99"})
	if err != nil {
		t.Fatalf("ListBranchesByIssueSlugs: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("got %d entries, want 2", len(result))
	}
	if b, ok := result["ABC-1"]; !ok {
		t.Error("missing ABC-1")
	} else if b.BranchName != "ABC-1@feat@first@uuid-s1" {
		t.Errorf("BranchName = %q", b.BranchName)
	}
	if _, ok := result["ABC-2"]; !ok {
		t.Error("missing ABC-2")
	}
	if _, ok := result["ABC-99"]; ok {
		t.Error("unexpected ABC-99 in result")
	}
}

func TestListBranchesByIssueSlugs_emptyInput(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	result, err := s.ListBranchesByIssueSlugs(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListBranchesByIslugs: %v", err)
	}
	if len(result) != 0 {
		t.Errorf("got %d entries, want 0", len(result))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./store/... -run TestListBranchesByIssueSlugs -v
```

Expected: FAIL with `s.ListBranchesByIssueSlugs undefined`

- [ ] **Step 3: Add `IssueRow` struct and `strings` import to `store/store.go`**

Add `"strings"` to the import block.

Add after the `BranchRow` struct definition:

```go
// IssueRow is the unified display row for git zf issue list.
// It composes an issue identity with an optional local branch.
type IssueRow struct {
	IssueSlug     string     `json:"issue_slug"`
	Title         string     `json:"title"`
	TrackerStatus *string    `json:"tracker_status"` // nil → display "N.A."
	Branch        *BranchRow `json:"branch"`          // nil → not started locally
}
```

- [ ] **Step 4: Add `ListBranchesByIssueSlugs` to `store/store.go`**

Add after `ListBranches`:

```go
// ListBranchesByIssueSlugs returns a map[id_slug]BranchRow for the given slugs.
// Uses a single SELECT … WHERE i.id_slug IN (…). Returns an empty map for empty input.
func (s *Store) ListBranchesByIssueSlugs(ctx context.Context, slugs []string) (map[string]BranchRow, error) {
	if len(slugs) == 0 {
		return map[string]BranchRow{}, nil
	}

	placeholders := make([]string, len(slugs))
	for i := range slugs {
		placeholders[i] = "?"
	}

	q := fmt.Sprintf(`
		SELECT b.uuid, i.id_slug, i.title, b.name, b.type, st.name, b.created_at
		FROM branches b
		JOIN issues i ON b.issue_id = i.id
		JOIN statuses st ON b.status_id = st.id
		WHERE i.id_slug IN (%s)
		ORDER BY b.created_at DESC`, strings.Join(placeholders, ","))

	args := make([]any, len(slugs))
	for i, slug := range slugs {
		args[i] = slug
	}

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list branches by slugs query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]BranchRow)

	for rows.Next() {
		var r BranchRow
		var createdAtStr string

		if err := rows.Scan(
			&r.UUID, &r.IssueSlug, &r.Title, &r.BranchName, &r.Type, &r.Status, &createdAtStr,
		); err != nil {
			return nil, fmt.Errorf("scan branch row: %w", err)
		}

		t, parseErr := parseSQLiteTime(createdAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse branch created_at %q: %w", createdAtStr, parseErr)
		}

		r.CreatedAt = t
		result[r.IssueSlug] = r
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	return result, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
mise exec -- go test ./store/... -run TestListBranchesByIssueSlugs -v
```

Expected: PASS

- [ ] **Step 6: Run all store tests to catch regressions**

```bash
mise exec -- go test ./store/... -v
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add store/store.go store/store_test.go
git commit -m "feat(store): add IssueRow and ListBranchesByIssueSlugs"
```

---

## Task 2: `IssueStatusFilter` TUI function

**Files:**
- Modify: `tui/issue.go`
- Create: `tui/issue_test.go`

- [ ] **Step 1: Write the failing tests**

Create `tui/issue_test.go`:

```go
package tui

import "testing"

func TestIssueStatusFilter_defaultsToOpen(t *testing.T) {
	var status string
	IssueStatusFilter(&status, "")
	if status != "open" {
		t.Errorf("status = %q, want %q", status, "open")
	}
}

func TestIssueStatusFilter_preservesSelected(t *testing.T) {
	var status string
	IssueStatusFilter(&status, "closed")
	if status != "closed" {
		t.Errorf("status = %q, want %q", status, "closed")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./tui/... -run TestIssueStatusFilter -v
```

Expected: FAIL with `IssueStatusFilter undefined`

- [ ] **Step 3: Add `IssueStatusFilter` to `tui/issue.go`**

Add at the end of `tui/issue.go`, after `IssueStatusPicker`:

```go
// IssueStatusFilter presents a status filter for the issue list.
// selected is the pre-selected value ("open", "closed", "all"); defaults to "open" when empty.
func IssueStatusFilter(status *string, selected string) *huh.Group {
	*status = selected
	if *status == "" {
		*status = "open"
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Filter by status:").
			Options(
				huh.NewOption("Open", "open"),
				huh.NewOption("Closed / Merged", "closed"),
				huh.NewOption("All", "all"),
			).
			Value(status),
	)
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./tui/... -run TestIssueStatusFilter -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add tui/issue.go tui/issue_test.go
git commit -m "feat(tui): add IssueStatusFilter"
```

---

## Task 3: `buildIssueRows` — core merge logic

**Files:**
- Modify: `cmd/issue.go`
- Modify: `cmd/issue_test.go` (create if absent)

- [ ] **Step 1: Write the failing tests**

Create (or add to) `cmd/issue_test.go`:

```go
package cmd

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
)

// fakeIssueTracker is a stub tracker.Tracker for issue list tests.
type fakeIssueTracker struct {
	issues []tracker.Issue
	err    error
}

func (f *fakeIssueTracker) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	return f.issues, f.err
}
func (f *fakeIssueTracker) ListStatuses(_ context.Context) ([]string, error) { return nil, nil }
func (f *fakeIssueTracker) UpdateIssueStatus(_ context.Context, _, _ string) error { return nil }

func openTestIssueStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestBuildIssueRows_trackerPath_partialMatch(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)

	// Seed 2 of the 3 tracker issues in the local store.
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "T-1", Title: "First", StatusID: 1},
		&store.Branch{UUID: "uuid-t1", Name: "T-1@feat@first@uuid-t1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "T-2", Title: "Second", StatusID: 1},
		&store.Branch{UUID: "uuid-t2", Name: "T-2@fix@second@uuid-t2", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tk := &fakeIssueTracker{issues: []tracker.Issue{
		{ID: "T-1", Subject: "First", Status: "In Progress"},
		{ID: "T-2", Subject: "Second", Status: "In Progress"},
		{ID: "T-3", Subject: "Third", Status: "New"},
	}}

	infra := issueListInfra{Tracker: tk, Store: s, Stderr: &bytes.Buffer{}}

	rows, err := buildIssueRows(t.Context(), infra, "open")
	if err != nil {
		t.Fatalf("buildIssueRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// T-1 and T-2 have local branches; T-3 does not.
	bySlug := make(map[string]store.IssueRow)
	for _, r := range rows {
		bySlug[r.IssueSlug] = r
	}

	if bySlug["T-1"].Branch == nil {
		t.Error("T-1: Branch should be non-nil")
	}
	if bySlug["T-2"].Branch == nil {
		t.Error("T-2: Branch should be non-nil")
	}
	if bySlug["T-3"].Branch != nil {
		t.Error("T-3: Branch should be nil")
	}
	for slug, r := range bySlug {
		if r.TrackerStatus == nil {
			t.Errorf("%s: TrackerStatus should be non-nil", slug)
		}
	}
}

func TestBuildIssueRows_localFallback_nilTracker(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "L-1", Title: "Local only", StatusID: 1},
		&store.Branch{UUID: "uuid-l1", Name: "L-1@feat@local-only@uuid-l1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{Tracker: nil, Store: s, Stderr: &bytes.Buffer{}}

	rows, err := buildIssueRows(t.Context(), infra, "open")
	if err != nil {
		t.Fatalf("buildIssueRows: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].TrackerStatus != nil {
		t.Error("TrackerStatus should be nil in local mode")
	}
	if rows[0].Branch == nil {
		t.Error("Branch should be non-nil in local mode")
	}
}

func TestBuildIssueRows_trackerError_fallback(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "F-1", Title: "Fallback", StatusID: 1},
		&store.Branch{UUID: "uuid-f1", Name: "F-1@feat@fallback@uuid-f1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tk := &fakeIssueTracker{err: errors.New("network error")}
	var stderr bytes.Buffer
	infra := issueListInfra{Tracker: tk, Store: s, Stderr: &stderr}

	rows, err := buildIssueRows(t.Context(), infra, "open")
	if err != nil {
		t.Fatalf("buildIssueRows: %v", err)
	}
	// Fell back to local store.
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].TrackerStatus != nil {
		t.Error("TrackerStatus should be nil after fallback")
	}
	if !strings.Contains(stderr.String(), "warning") {
		t.Errorf("expected warning on stderr, got %q", stderr.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./cmd/... -run TestBuildIssueRows -v
```

Expected: FAIL with `issueListInfra undefined` and `buildIssueRows undefined`

- [ ] **Step 3: Add `issueListInfra`, `buildIssueRows`, and helpers to `cmd/issue.go`**

Add these imports to `cmd/issue.go` (merge with existing):

```go
import (
    "context"
    "errors"
    "fmt"
    "io"
    "path/filepath"

    "github.com/charmbracelet/huh"
    "github.com/piprim/git-zf/branch"
    "github.com/piprim/git-zf/config"
    "github.com/piprim/git-zf/git"
    "github.com/piprim/git-zf/issue"
    "github.com/piprim/git-zf/store"
    "github.com/piprim/git-zf/tracker"
    _ "github.com/piprim/git-zf/tracker/redmine"
    "github.com/piprim/git-zf/tui"
    "github.com/spf13/cobra"
)
```

Add these declarations to `cmd/issue.go`:

```go
// issueListFlags holds the parsed flags for git zf issue list.
type issueListFlags struct {
	status  string
	stdout  bool
	jsonOut bool
}

// issueListInfra groups the dependencies of buildIssueRows.
// Tracker is nil when no tracker is configured.
// Stderr receives the fallback warning when the tracker call fails.
type issueListInfra struct {
	Tracker tracker.Tracker
	Store   *store.Store
	Stderr  io.Writer
}

// buildIssueRows fetches and merges tracker + store data into display rows.
// When infra.Tracker is nil the local path is used directly.
// Falls back to local path with a warning on infra.Stderr if the tracker call fails.
func buildIssueRows(ctx context.Context, infra issueListInfra, status string) ([]store.IssueRow, error) {
	if infra.Tracker != nil {
		rows, err := buildFromTracker(ctx, infra)
		if err != nil {
			fmt.Fprintf(infra.Stderr, "warning: tracker unavailable, falling back to local store: %v\n", err)
		} else {
			return rows, nil
		}
	}

	return buildFromStore(ctx, infra.Store, status)
}

func buildFromTracker(ctx context.Context, infra issueListInfra) ([]store.IssueRow, error) {
	issues, err := infra.Tracker.ListIssues(ctx)
	if err != nil {
		return nil, fmt.Errorf("list tracker issues: %w", err)
	}

	slugs := make([]string, len(issues))
	for i, iss := range issues {
		slugs[i] = iss.ID
	}

	branchMap, err := infra.Store.ListBranchesByIssueSlugs(ctx, slugs)
	if err != nil {
		return nil, fmt.Errorf("list branches by slugs: %w", err)
	}

	rows := make([]store.IssueRow, len(issues))
	for i, iss := range issues {
		status := iss.Status
		row := store.IssueRow{
			IssueSlug:     iss.ID,
			Title:         iss.Subject,
			TrackerStatus: &status,
		}
		if b, ok := branchMap[iss.ID]; ok {
			b := b
			row.Branch = &b
		}
		rows[i] = row
	}

	return rows, nil
}

func buildFromStore(ctx context.Context, s *store.Store, status string) ([]store.IssueRow, error) {
	branches, err := s.ListBranches(ctx, toIssueStoreStatus(status))
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	rows := make([]store.IssueRow, len(branches))
	for i := range branches {
		b := branches[i]
		rows[i] = store.IssueRow{
			IssueSlug: b.IssueSlug,
			Title:     b.Title,
			Branch:    &b,
		}
	}

	return rows, nil
}

func toIssueStoreStatus(s string) store.BranchStatus {
	switch s {
	case "open":
		return store.BranchStatusInProgress
	case "closed":
		return store.BranchStatusMerged
	default:
		return store.BranchStatusAll
	}
}
```

Note: remove the `"errors"` import if it is not used elsewhere in `cmd/issue.go`.

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./cmd/... -run TestBuildIssueRows -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/issue.go cmd/issue_test.go
git commit -m "feat(cmd): add buildIssueRows with tracker-first / local-fallback"
```

---

## Task 4: `runIssueList` — stdout and JSON paths

**Files:**
- Modify: `cmd/issue.go`
- Modify: `cmd/issue_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `cmd/issue_test.go`:

```go
func TestRunIssueList_json(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "J-1", Title: "JSON issue", StatusID: 1},
		&store.Branch{UUID: "uuid-j1", Name: "J-1@feat@json-issue@uuid-j1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{Tracker: nil, Store: s, Stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runIssueList(t.Context(), &buf, infra, issueListFlags{jsonOut: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "J-1") {
		t.Errorf("JSON output missing J-1, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "N.A.") {
		t.Errorf("JSON output should contain N.A. for nil TrackerStatus; got: %s", buf.String())
	}
}

func TestRunIssueList_stdout(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "S-1", Title: "Stdout issue", StatusID: 1},
		&store.Branch{UUID: "uuid-s1b", Name: "S-1@feat@stdout@uuid-s1b", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{Tracker: nil, Store: s, Stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runIssueList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "S-1") {
		t.Errorf("stdout output missing S-1, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "N.A.") {
		t.Errorf("stdout output should contain N.A. for nil TrackerStatus; got: %s", buf.String())
	}
}

func TestRunIssueList_stdout_emptyStore(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	infra := issueListInfra{Tracker: nil, Store: s, Stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runIssueList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "No issues found") {
		t.Errorf("expected 'No issues found', got: %s", buf.String())
	}
}

func TestRunIssueList_stdout_trackerIssueNoLocalBranch(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	tk := &fakeIssueTracker{issues: []tracker.Issue{
		{ID: "NB-1", Subject: "No branch yet", Status: "New"},
	}}
	infra := issueListInfra{Tracker: tk, Store: s, Stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runIssueList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "∅") {
		t.Errorf("expected ∅ for missing branch, got: %s", buf.String())
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./cmd/... -run TestRunIssueList -v
```

Expected: FAIL with `runIssueList undefined`

- [ ] **Step 3: Add `renderIssueTable`, display helpers, and `runIssueList` to `cmd/issue.go`**

Add these imports to `cmd/issue.go` (merge with existing):

```go
import (
    "encoding/json"
    "os"

    btable "github.com/charmbracelet/bubbles/table"
    tea    "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    lgtable "github.com/charmbracelet/lipgloss/table"
)
```

Add these constants and functions to `cmd/issue.go`:

```go
const (
	issueTableColWidthIssueID       = 10
	issueTableColWidthTitle         = 28
	issueTableColWidthBranch        = 38
	issueTableColWidthLocalStatus   = 12
	issueTableColWidthTrackerStatus = 14
	issueTableColWidthCreated       = 10
	issueTableHeight                = 20
)

func branchFieldOrEmpty(branch *store.BranchRow, fn func(*store.BranchRow) string) string {
	if branch == nil {
		return "∅"
	}

	return fn(branch)
}

func trackerStatusOrNA(s *string) string {
	if s == nil {
		return "N.A."
	}

	return *s
}

func renderIssueTable(w io.Writer, rows []store.IssueRow) {
	t := lgtable.New().
		Headers("ISSUE ID", "TITLE", "BRANCH", "LOCAL STATUS", "TRACKER STATUS", "CREATED").
		StyleFunc(func(row, _ int) lipgloss.Style {
			if row == lgtable.HeaderRow {
				return lipgloss.NewStyle().Bold(true)
			}

			return lipgloss.NewStyle()
		})

	for _, r := range rows {
		t.Row(
			r.IssueSlug,
			r.Title,
			branchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
			branchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
			trackerStatusOrNA(r.TrackerStatus),
			branchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
		)
	}

	fmt.Fprintln(w, t.Render())
}

func runIssueList(ctx context.Context, w io.Writer, infra issueListInfra, flags issueListFlags) error {
	if flags.jsonOut {
		rows, err := buildIssueRows(ctx, infra, flags.status)
		if err != nil {
			return fmt.Errorf("build issue rows: %w", err)
		}
		if err := json.NewEncoder(w).Encode(rows); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	if flags.stdout {
		rows, err := buildIssueRows(ctx, infra, flags.status)
		if err != nil {
			return fmt.Errorf("build issue rows: %w", err)
		}
		if len(rows) == 0 {
			fmt.Fprintln(w, "No issues found.")

			return nil
		}
		renderIssueTable(w, rows)

		return nil
	}

	// TUI path: status filter form then interactive table.
	statusStr := flags.status
	if err := huh.NewForm(tui.IssueStatusFilter(&statusStr, statusStr)).Run(); err != nil {
		return fmt.Errorf("status filter: %w", err)
	}

	rows, err := buildIssueRows(ctx, infra, statusStr)
	if err != nil {
		return fmt.Errorf("build issue rows: %w", err)
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No issues found.")

		return nil
	}

	return runIssueTableTUI(rows)
}
```

Note: `runIssueTableTUI` is added in Task 5. The build will fail until then — that's expected.

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./cmd/... -run TestRunIssueList -v
```

Expected: PASS (once Task 5 stub is present — if `runIssueTableTUI` is missing, add a temporary stub: `func runIssueTableTUI(_ []store.IssueRow) error { return nil }` and remove it in Task 5)

- [ ] **Step 5: Commit**

```bash
git add cmd/issue.go cmd/issue_test.go
git commit -m "feat(cmd): add runIssueList with stdout and JSON paths"
```

---

## Task 5: TUI table, command wiring, and `issueRunE` integration

**Files:**
- Modify: `cmd/issue.go`

- [ ] **Step 1: Add `runIssueTableTUI` and `issueTableModel` to `cmd/issue.go`**

Replace the temporary stub (if any) or add after `runIssueList`:

```go
func runIssueTableTUI(rows []store.IssueRow) error {
	cols := []btable.Column{
		{Title: "Issue ID", Width: issueTableColWidthIssueID},
		{Title: "Title", Width: issueTableColWidthTitle},
		{Title: "Branch", Width: issueTableColWidthBranch},
		{Title: "Local Status", Width: issueTableColWidthLocalStatus},
		{Title: "Tracker Status", Width: issueTableColWidthTrackerStatus},
		{Title: "Created", Width: issueTableColWidthCreated},
	}

	tableRows := make([]btable.Row, len(rows))
	for i, r := range rows {
		tableRows[i] = btable.Row{
			r.IssueSlug,
			r.Title,
			branchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
			branchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
			trackerStatusOrNA(r.TrackerStatus),
			branchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
		}
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(tableRows),
		btable.WithFocused(true),
		btable.WithHeight(issueTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(branchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	if _, err := tea.NewProgram(&issueTableModel{table: t}).Run(); err != nil {
		return fmt.Errorf("run table: %w", err)
	}

	return nil
}

type issueTableModel struct {
	table btable.Model
}

func (*issueTableModel) Init() tea.Cmd { return nil }

func (m *issueTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

func (m *issueTableModel) View() string {
	return m.table.View() + "\n\nPress q to quit."
}
```

- [ ] **Step 2: Add `issueListRunE` and `getIssueListCmd` to `cmd/issue.go`**

```go
func getIssueListCmd() *cobra.Command {
	var flags issueListFlags

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List issues",
	}

	f := cmd.Flags()
	f.StringVar(&flags.status, "status", "", "filter by status: open, closed, all")
	f.BoolVar(&flags.stdout, "stdout", false, "print table to stdout without TUI")
	f.BoolVar(&flags.jsonOut, "json", false, "print JSON array to stdout")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return issueListRunE(cmd, flags)
	}

	return cmd
}

func issueListRunE(cmd *cobra.Command, flags issueListFlags) error {
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
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	var t tracker.Tracker
	if appConfig.IssueTracker.Type != "" {
		t, err = tracker.New(appConfig.IssueTracker)
		if err != nil {
			fmt.Fprintf(cmd.OutOrStderr(), "warning: could not initialize tracker: %v\n", err)
		}
	}

	infra := issueListInfra{
		Tracker: t,
		Store:   s,
		Stderr:  cmd.OutOrStderr(),
	}

	return runIssueList(cmd.Context(), os.Stdout, infra, flags)
}
```

- [ ] **Step 3: Wire `getIssueListCmd` into `getIssueCmd`**

In `getIssueCmd`, change:

```go
cmd.AddCommand(getIssueStartCmd())
```

to:

```go
cmd.AddCommand(getIssueStartCmd(), getIssueListCmd())
```

- [ ] **Step 4: Wire `issueListRunE` into `issueRunE`**

In `issueRunE`, change:

```go
switch action {
case tui.IssueActionNameStart:
    return issueStartRunE(cmd, args)
default:
    fmt.Println("Not yet implemented.")

    return nil
}
```

to:

```go
switch action {
case tui.IssueActionNameStart:
    return issueStartRunE(cmd, args)
case tui.IssueActionNameList:
    return issueListRunE(cmd, issueListFlags{})
default:
    fmt.Println("Not yet implemented.")

    return nil
}
```

- [ ] **Step 5: Build to verify no compilation errors**

```bash
mise exec -- go build ./...
```

Expected: no errors

- [ ] **Step 6: Run all tests**

```bash
mise exec -- go test ./...
```

Expected: all PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/issue.go
git commit -m "feat(cmd): wire git zf issue list command"
```
