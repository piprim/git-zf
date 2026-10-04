# Issue Table Inline Status Filter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the pre-table `huh` status-filter form with inline status tabs (`Open / Closed / All`) toggled by `tab` inside the issue table TUI.

**Architecture:** Fetch all `store.IssueRow` values once (no status pre-filter); the `issueTableModel` holds the full slice and derives the displayed `[]btable.Row` by composing a status predicate and a case-insensitive text filter via a shared `applyFilters` helper. A `renderStatusTabs` function renders the active tab bold and inactive tabs dimmed using lipgloss in the model's footer.

**Tech Stack:** Go, `github.com/charmbracelet/bubbles/table`, `github.com/charmbracelet/bubbles/textinput`, `github.com/charmbracelet/bubbletea`, `github.com/charmbracelet/lipgloss`

---

## File Map

| File | Change |
|---|---|
| `tui/issue.go` | Add `issueRowToTableRow`, `matchesStatus`, `applyFilters`, `nextStatus`, `renderStatusTabs`; update `issueTableModel` struct, `IssueTableModel` constructor, `Update`, `View`; delete `IssueStatusFilter`, `filterIssueRows` |
| `tui/issue_test.go` | Delete old `IssueStatusFilter` tests; add `TestApplyFilters_*` and `TestNextStatus` |
| `cmd/issue/list.go` | Remove huh pre-form from TUI path; pass `""` to `buildRows`; pass `flags.status` to `IssueTableModel`; remove unused `huh` import |

---

## Task 1: Pure filter functions (TDD)

**Files:**
- Modify: `tui/issue_test.go`
- Modify: `tui/issue.go`

- [ ] **Step 1: Replace the two `IssueStatusFilter` tests with filter tests**

Replace the entire contents of `tui/issue_test.go` with:

```go
package tui

import (
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestApplyFilters_Open(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "open", "")

	if len(got) != 2 {
		t.Fatalf("want 2 rows, got %d", len(got))
	}

	if got[0][0] != "A" || got[1][0] != "B" {
		t.Errorf("unexpected slugs: %q %q", got[0][0], got[1][0])
	}
}

func TestApplyFilters_Closed(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "closed", "")

	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}

	if got[0][0] != "C" {
		t.Errorf("want C, got %q", got[0][0])
	}
}

func TestApplyFilters_All(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "A", Title: "A"},
		{IssueSlug: "B", Title: "B", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "C", Title: "C", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "all", "")

	if len(got) != 3 {
		t.Fatalf("want 3 rows, got %d", len(got))
	}
}

func TestApplyFilters_TextFilter(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "ABC-1", Title: "Fix login", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "ABC-2", Title: "Update signup", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
	}

	got := applyFilters(rows, "open", "login")

	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}

	if got[0][0] != "ABC-1" {
		t.Errorf("want ABC-1, got %q", got[0][0])
	}
}

func TestApplyFilters_StatusAndText(t *testing.T) {
	rows := []store.IssueRow{
		{IssueSlug: "X-1", Title: "Fix auth", Branch: &store.BranchRow{Status: store.BranchStatusInProgress}},
		{IssueSlug: "X-2", Title: "Fix auth", Branch: &store.BranchRow{Status: store.BranchStatusMerged}},
	}

	got := applyFilters(rows, "open", "auth")

	if len(got) != 1 {
		t.Fatalf("want 1 row, got %d", len(got))
	}

	if got[0][0] != "X-1" {
		t.Errorf("want X-1, got %q", got[0][0])
	}
}

func TestNextStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"open", "closed"},
		{"closed", "all"},
		{"all", "open"},
		{"", "open"},
	}

	for _, tc := range cases {
		if got := nextStatus(tc.in); got != tc.want {
			t.Errorf("nextStatus(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail (functions not yet defined)**

```bash
mise exec -- go test ./tui/... -run "TestApplyFilters|TestNextStatus" -v
```

Expected: FAIL — `undefined: applyFilters` and `undefined: nextStatus`

- [ ] **Step 3: Add `issueRowToTableRow`, `matchesStatus`, `applyFilters`, and `nextStatus` to `tui/issue.go`**

Add these functions after the existing `IssueTableModel` function and before `issueTableModel`. They replace `filterIssueRows` (do NOT delete `filterIssueRows` yet — that happens in Task 2 to avoid breaking the current `Update` method):

```go
func issueRowToTableRow(r store.IssueRow) btable.Row {
	return btable.Row{
		r.IssueSlug,
		r.Title,
		pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
		pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
		pkg.TrackerStatusOrNA(r.TrackerStatus),
		pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
	}
}

func matchesStatus(r store.IssueRow, status string) bool {
	switch status {
	case "closed":
		return r.Branch != nil && r.Branch.Status == store.BranchStatusMerged
	case "all":
		return true
	default: // "open" and anything else
		return r.Branch == nil || r.Branch.Status == store.BranchStatusInProgress
	}
}

func applyFilters(rows []store.IssueRow, status, text string) []btable.Row {
	q := strings.ToLower(text)
	out := make([]btable.Row, 0, len(rows))

	for _, r := range rows {
		if !matchesStatus(r, status) {
			continue
		}

		row := issueRowToTableRow(r)

		if q != "" {
			matched := false
			for _, cell := range row {
				if strings.Contains(strings.ToLower(cell), q) {
					matched = true

					break
				}
			}

			if !matched {
				continue
			}
		}

		out = append(out, row)
	}

	return out
}

func nextStatus(current string) string {
	switch current {
	case "open":
		return "closed"
	case "closed":
		return "all"
	default:
		return "open"
	}
}
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
mise exec -- go test ./tui/... -run "TestApplyFilters|TestNextStatus" -v
```

Expected: PASS — all 6 test cases green.

- [ ] **Step 5: Commit**

```bash
git add tui/issue.go tui/issue_test.go
git commit -m "feat(tui): add applyFilters and nextStatus for inline issue status filter"
```

---

## Task 2: Update `issueTableModel` struct, constructor, and `Update`

**Files:**
- Modify: `tui/issue.go`

- [ ] **Step 1: Update `issueTableModel` struct**

Replace the struct definition:

```go
// before
type issueTableModel struct {
	table     btable.Model
	allRows   []btable.Row
	filter    textinput.Model
	filtering bool
}

// after
type issueTableModel struct {
	table        btable.Model
	allRows      []store.IssueRow
	filter       textinput.Model
	filtering    bool
	statusFilter string
}
```

- [ ] **Step 2: Rewrite `IssueTableModel` constructor**

Replace the entire `IssueTableModel` function:

```go
func IssueTableModel(rows []store.IssueRow, initialStatus string) (tea.Model, error) {
	cols := []btable.Column{
		{Title: "Issue ID", Width: issueTableColWidthIssueID},
		{Title: "Title", Width: issueTableColWidthTitle},
		{Title: "Branch", Width: issueTableColWidthBranch},
		{Title: "Local Status", Width: issueTableColWidthLocalStatus},
		{Title: "Tracker Status", Width: issueTableColWidthTrackerStatus},
		{Title: "Created", Width: issueTableColWidthCreated},
	}

	status := initialStatus
	if status == "" {
		status = "open"
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(applyFilters(rows, status, "")),
		btable.WithFocused(true),
		btable.WithHeight(issueTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 64

	return &issueTableModel{table: t, allRows: rows, filter: fi, statusFilter: status}, nil
}
```

- [ ] **Step 3: Rewrite the `Update` method and delete `filterIssueRows`**

Replace the entire `Update` method:

```go
func (m *issueTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.filtering {
			switch key.String() {
			case "esc":
				m.filtering = false
				m.filter.Blur()
				m.filter.Reset()
				m.table.SetRows(applyFilters(m.allRows, m.statusFilter, ""))

				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()

				return m, nil
			}

			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value()))

			return m, cmd
		}

		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.statusFilter = nextStatus(m.statusFilter)
			m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value()))

			return m, nil
		case "/":
			m.filtering = true
			return m, m.filter.Focus()
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	return m, cmd
}
```

Then delete the now-dead `filterIssueRows` function entirely.

- [ ] **Step 4: Build to verify no compile errors**

```bash
mise exec -- go build ./...
```

Expected: success (no output).

- [ ] **Step 5: Run all tests**

```bash
mise exec -- go test ./tui/... -v
```

Expected: all tests pass.

- [ ] **Step 6: Commit**

```bash
git add tui/issue.go
git commit -m "refactor(tui): update issueTableModel to use []store.IssueRow and inline status filter"
```

---

## Task 3: Update `View` with lipgloss status tabs

**Files:**
- Modify: `tui/issue.go`

- [ ] **Step 1: Add style vars and `renderStatusTabs` to `tui/issue.go`**

Add these after the `const` block (alongside the existing `issueTableColWidth*` constants):

```go
var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true)
	inactiveTabStyle = lipgloss.NewStyle().Faint(true)
)
```

Then add the `renderStatusTabs` function anywhere above `View`:

```go
func renderStatusTabs(current string) string {
	type tab struct {
		label string
		value string
	}

	tabs := []tab{
		{"Open", "open"},
		{"Closed", "closed"},
		{"All", "all"},
	}

	parts := make([]string, len(tabs))
	for i, t := range tabs {
		if t.value == current {
			parts[i] = activeTabStyle.Render("[ " + t.label + " ]")
		} else {
			parts[i] = inactiveTabStyle.Render(t.label)
		}
	}

	return strings.Join(parts, "  ")
}
```

- [ ] **Step 2: Replace the `View` method**

```go
func (m *issueTableModel) View() string {
	tabs := renderStatusTabs(m.statusFilter)

	if m.filtering {
		return m.table.View() + "\n\n" + tabs + "    /" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}

	return m.table.View() + "\n\n" + tabs + "    Press / to filter · tab: status · q to quit"
}
```

- [ ] **Step 3: Build**

```bash
mise exec -- go build ./...
```

Expected: success.

- [ ] **Step 4: Commit**

```bash
git add tui/issue.go
git commit -m "feat(tui): render inline status tabs in issue table footer"
```

---

## Task 4: Update `cmd/issue/list.go` and delete `IssueStatusFilter`

**Files:**
- Modify: `cmd/issue/list.go`
- Modify: `tui/issue.go`

- [ ] **Step 1: Update the TUI path in `runList` (`cmd/issue/list.go`)**

Find the TUI path block (currently the last `else` branch in `runList`) and replace it:

```go
// before
// TUI path: status filter form then interactive table.
statusStr := flags.status
if err := huh.NewForm(tui.IssueStatusFilter(&statusStr, statusStr)).Run(); err != nil {
    return fmt.Errorf("status filter: %w", err)
}

rows, err := buildRows(ctx, infra, statusStr)
if err != nil {
    return fmt.Errorf("build issue rows: %w", err)
}

if len(rows) == 0 {
    fmt.Fprintln(w, "No issues found.")

    return nil
}

m, err := tui.IssueTableModel(rows)
if err != nil {
    return fmt.Errorf("failed to construct issue table: %w", err)
}
if _, err := tea.NewProgram(m).Run(); err != nil {
    return fmt.Errorf("run table: %w", err)
}

return nil

// after
// TUI path: fetch all rows; status filter lives inside the table model.
rows, err := buildRows(ctx, infra, "")
if err != nil {
    return fmt.Errorf("build issue rows: %w", err)
}

if len(rows) == 0 {
    fmt.Fprintln(w, "No issues found.")

    return nil
}

m, err := tui.IssueTableModel(rows, flags.status)
if err != nil {
    return fmt.Errorf("failed to construct issue table: %w", err)
}
if _, err := tea.NewProgram(m).Run(); err != nil {
    return fmt.Errorf("run table: %w", err)
}

return nil
```

- [ ] **Step 2: Remove the `huh` import from `cmd/issue/list.go`**

Delete the line:
```go
"github.com/charmbracelet/huh"
```

from the import block. (`huh` is no longer used in this file after removing the pre-form.)

- [ ] **Step 3: Delete `IssueStatusFilter` from `tui/issue.go`**

Remove the entire `IssueStatusFilter` function:

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

- [ ] **Step 4: Build to verify no compile errors**

```bash
mise exec -- go build ./...
```

Expected: success.

- [ ] **Step 5: Run the full test suite**

```bash
mise exec -- go test ./...
```

Expected: all tests pass. In particular `./tui/...` and `./cmd/issue/...`.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/list.go tui/issue.go
git commit -m "feat(issue): replace huh pre-filter with inline status tabs in issue table TUI"
```
