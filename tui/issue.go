package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	btable "github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
)

const (
	IssueActionNameStart = "issueStart"
	IssueActionNameList  = "issueList"
	IssueActionNameClose = "issueClose"

	issueTableColWidthIssueID       = 10
	issueTableColWidthTitle         = 28
	issueTableColWidthBranch        = 38
	issueTableColWidthLocalStatus   = 12
	issueTableColWidthTrackerStatus = 14
	issueTableColWidthCreated       = 10
	issueTableHeight                = 20

	statusOpen   = "open"
	statusClosed = "closed"
	statusAll    = "all"

	projectAll                = "all"
	issueTableColWidthProject = 18
	issueTableMaxCols         = 7
)

var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true)
	inactiveTabStyle = lipgloss.NewStyle().Faint(true)
)

// IssueActionSelect presents the list of available issue actions.
func IssueActionSelect(action *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Issue action:").
			Options(
				huh.NewOption("Start\n"+descStyle.Render(
					"Start working on an issue (branch or worktree)"), IssueActionNameStart),
				huh.NewOption("List\n"+descStyle.Render("List open issues"), IssueActionNameList),
				huh.NewOption("Close\n"+descStyle.Render("Close an issue"), IssueActionNameClose),
			).
			Value(action),
	)
}

func IssueInput(issueID, title, branchType *string, allowedBranchTypes []string) *huh.Group {
	typeOpts := make([]huh.Option[string], 0, len(allowedBranchTypes))
	for _, allowed := range allowedBranchTypes {
		typeOpts = append(typeOpts, huh.NewOption(allowed, allowed))
	}
	if len(typeOpts) == 0 {
		typeOpts = []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	group := huh.NewGroup(
		huh.NewInput().
			Title("Issue ID:").
			Placeholder("ABC-42").
			Validate(func(s string) error {
				if s == "" {
					return errors.New("required")
				}

				return nil
			}).
			Value(issueID),
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(func(s string) error {
				if s == "" {
					return errors.New("required")
				}

				return nil
			}).
			Value(title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(branchType),
	)

	return group
}

func IssueConfirm(confirmTitle string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(confirmTitle).
			Value(confirmed),
	)
}

// IssueTrackerToggle asks whether to fetch issues from the tracker.
// trackerFirst=true pre-selects YES (used for `issue start`);
// trackerFirst=false pre-selects NO (used for `branch new`).
func IssueTrackerToggle(useTracker *bool, trackerFirst bool, trackerType string) *huh.Group {
	*useTracker = trackerFirst

	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Fetch issues from %s?", trackerType)).
			Value(useTracker),
	)
}

// WorktreeToggle asks whether to create a git worktree or a plain branch.
// Pre-selected default is false (plain branch).
func WorktreeToggle(useWorktree *bool) *huh.Group {
	*useWorktree = false

	return huh.NewGroup(
		huh.NewConfirm().
			Title("Create a git worktree instead of a plain branch?").
			Value(useWorktree),
	)
}

const issueTrackerPickerHeight = 10

// IssueTrackerPicker shows the live issue list and branch type selector.
func IssueTrackerPicker(
	issues []tracker.Issue,
	selected *tracker.Issue,
	branchTypes []string,
	branchType *string) *huh.Group {
	opts := make([]huh.Option[tracker.Issue], len(issues))
	for i, iss := range issues {
		label := fmt.Sprintf("[%s] %s", iss.ID, iss.Subject)
		opts[i] = huh.NewOption(label, iss)
	}

	if len(opts) == 0 {
		opts = []huh.Option[tracker.Issue]{huh.NewOption("(no issues found)", tracker.Issue{})}
	}

	typeOpts := make([]huh.Option[string], len(branchTypes))
	for i, tp := range branchTypes {
		typeOpts[i] = huh.NewOption(tp, tp)
	}

	if len(typeOpts) == 0 {
		typeOpts = []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	return huh.NewGroup(
		huh.NewSelect[tracker.Issue]().
			Title("Pick an issue:").
			Options(opts...).
			Value(selected).
			Height(issueTrackerPickerHeight),
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(branchType),
	)
}

// IssueTrackerError shows an error note with a "Continue with manual input" button.
func IssueTrackerError(msg string) *huh.Group {
	return huh.NewGroup(
		huh.NewNote().
			Title("Tracker error").
			Description(msg).
			Next(true).
			NextLabel("Continue with manual input"),
	)
}

const issueStatusSkip = ""

// IssueStatusPicker lets the user pick a new status from the live list, or
// skip the update entirely. selected is set to the chosen status name, or to
// issueStatusSkip ("") when the user chooses to skip.
func IssueStatusPicker(issueID, trackerType string, statuses []string, selected *string) *huh.Group {
	opts := make([]huh.Option[string], 0, len(statuses)+1)
	opts = append(opts, huh.NewOption("Skip (don't update)", issueStatusSkip))

	for _, s := range statuses {
		opts = append(opts, huh.NewOption(s, s))
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Update issue %s status in %s:", issueID, trackerType)).
			Options(opts...).
			Value(selected),
	)
}

func IssueTableModel(rows []store.IssueRow, initialStatus string) (tea.Model, error) {
	projects := uniqueProjects(rows)
	includeProj := len(projects) > 1
	cols := buildIssueTableColumns(rows)

	status := initialStatus
	if status == "" {
		status = statusOpen
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(applyFilters(rows, status, "", projectAll, includeProj)),
		btable.WithFocused(true),
		btable.WithHeight(issueTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 64

	return &issueTableModel{
		table:         t,
		allRows:       rows,
		filter:        fi,
		statusFilter:  status,
		projects:      projects,
		projectFilter: projectAll,
	}, nil
}

func issueRowToTableRow(r store.IssueRow, includeProject bool) btable.Row {
	row := make(btable.Row, 0, issueTableMaxCols)
	row = append(row, r.IssueSlug)

	if includeProject {
		row = append(row, r.Project)
	}

	return append(row,
		r.Title,
		store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
		store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
		store.TrackerStatusOrNA(r.TrackerStatus),
		store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
	)
}

// buildIssueTableColumns returns the bubbletea columns. The Project column
// appears only when rows span more than one project.
func buildIssueTableColumns(rows []store.IssueRow) []btable.Column {
	cols := []btable.Column{
		{Title: "Issue ID", Width: issueTableColWidthIssueID},
	}

	if len(uniqueProjects(rows)) > 1 {
		cols = append(cols, btable.Column{Title: "Project", Width: issueTableColWidthProject})
	}

	return append(cols,
		btable.Column{Title: "Title", Width: issueTableColWidthTitle},
		btable.Column{Title: "Branch", Width: issueTableColWidthBranch},
		btable.Column{Title: "Local Status", Width: issueTableColWidthLocalStatus},
		btable.Column{Title: "Tracker Status", Width: issueTableColWidthTrackerStatus},
		btable.Column{Title: "Created", Width: issueTableColWidthCreated},
	)
}

func matchesStatus(r store.IssueRow, status string) bool {
	switch status {
	case statusClosed:
		return r.Branch != nil && r.Branch.Status == store.BranchStatusMerged
	case statusAll:
		return true
	default: // "open" and anything else
		return r.Branch == nil || r.Branch.Status == store.BranchStatusInProgress
	}
}

// applyFilters builds the bubbletea table rows, keeping only those matching
// status, project, and free-text search. includeProject controls whether the
// Project cell is emitted (must match the table's column count).
func applyFilters(rows []store.IssueRow, status, text, project string, includeProject bool) []btable.Row {
	q := strings.ToLower(text)
	out := make([]btable.Row, 0, len(rows))

	for _, r := range rows {
		if !matchesStatus(r, status) {
			continue
		}

		if project != projectAll && r.Project != project {
			continue
		}

		row := issueRowToTableRow(r, includeProject)

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
	case statusOpen:
		return statusClosed
	case statusClosed:
		return statusAll
	default:
		return statusOpen
	}
}

// uniqueProjects returns the deduplicated, sorted list of non-empty
// IssueRow.Project values.
func uniqueProjects(rows []store.IssueRow) []string {
	seen := make(map[string]struct{})
	for _, r := range rows {
		if r.Project == "" {
			continue
		}

		seen[r.Project] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}

	slices.Sort(out)

	return out
}

// projectPickerOptions returns the ordered list ["all", p1, p2, …] used both
// for rendering the picker and for resolving the cursor position.
func projectPickerOptions(projects []string) []string {
	opts := make([]string, 0, len(projects)+1)
	opts = append(opts, projectAll)

	return append(opts, projects...)
}

type issueTableModel struct {
	table         btable.Model
	allRows       []store.IssueRow
	filter        textinput.Model
	filtering     bool
	statusFilter  string
	projects      []string
	projectFilter string
	picking       bool
	pickCursor    int
}

func (*issueTableModel) Init() tea.Cmd { return nil }

func (m *issueTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.picking {
			opts := projectPickerOptions(m.projects)
			switch key.String() {
			case "esc":
				m.picking = false

				return m, nil
			case "enter":
				m.picking = false
				m.projectFilter = opts[m.pickCursor]
				includeProj := len(m.projects) > 1
				m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))

				return m, nil
			case "up", "k":
				if m.pickCursor > 0 {
					m.pickCursor--
				}

				return m, nil
			case "down", "j":
				if m.pickCursor < len(opts)-1 {
					m.pickCursor++
				}

				return m, nil
			}

			return m, nil
		}

		if m.filtering {
			switch key.String() {
			case "esc":
				m.filtering = false
				m.filter.Blur()
				m.filter.Reset()
				includeProj := len(m.projects) > 1
				m.table.SetRows(applyFilters(m.allRows, m.statusFilter, "", m.projectFilter, includeProj))

				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()

				return m, nil
			}

			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			includeProj := len(m.projects) > 1
			m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))

			return m, cmd
		}

		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			m.statusFilter = nextStatus(m.statusFilter)
			includeProj := len(m.projects) > 1
			m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))

			return m, nil
		case "p":
			if len(m.projects) > 0 {
				m.picking = true
				opts := projectPickerOptions(m.projects)
				m.pickCursor = 0
				for i, o := range opts {
					if o == m.projectFilter {
						m.pickCursor = i

						break
					}
				}
			}

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

func renderStatusTabs(current string) string {
	type tab struct {
		label string
		value string
	}

	tabs := []tab{
		{"Open", statusOpen},
		{"Closed", statusClosed},
		{"All", statusAll},
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

func renderProjectPicker(cursor int, projects []string) string {
	opts := projectPickerOptions(projects)
	lines := make([]string, len(opts))

	for i, o := range opts {
		if i == cursor {
			lines[i] = activeTabStyle.Render("> " + o)
		} else {
			lines[i] = inactiveTabStyle.Render("  " + o)
		}
	}

	return "Pick project (↑↓ / j·k, enter: confirm, esc: cancel):\n" + strings.Join(lines, "\n")
}

func (m *issueTableModel) View() string {
	tabs := renderStatusTabs(m.statusFilter)

	if m.picking {
		return m.table.View() + "\n\n" + tabs + "\n\n" + renderProjectPicker(m.pickCursor, m.projects)
	}

	hint := "Press / to filter · tab: status · q to quit"
	proj := ""

	if len(m.projects) > 0 {
		hint = "Press / to filter · tab: status · p: project · q to quit"
		proj = "    Project: " + activeTabStyle.Render(m.projectFilter)
	}

	view := m.table.View() + "\n\n" + tabs + proj

	if m.filtering {
		return view + "    /" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}

	return view + "    " + hint
}

// IssueBranchPicker presents in-progress branches for the close flow.
// The branch matching currentBranch is pre-selected; falls back to the first row.
func IssueBranchPicker(rows []store.BranchRow, currentBranch string, selected *store.BranchRow) *huh.Group {
	opts := make([]huh.Option[store.BranchRow], len(rows))
	for i := range rows {
		label := fmt.Sprintf("[%s] %s (%s)", rows[i].IssueSlug, rows[i].Title, rows[i].BranchName)
		opts[i] = huh.NewOption(label, rows[i])
	}

	// Pre-select current branch; default to first row if not found.
	*selected = rows[0]
	for i := range rows {
		if rows[i].BranchName == currentBranch {
			*selected = rows[i]

			break
		}
	}

	return huh.NewGroup(
		huh.NewSelect[store.BranchRow]().
			Title("Select branch to close:").
			Options(opts...).
			Value(selected),
	)
}

// StrategyOption is one entry rendered by IssueMergeStrategy. The picker does
// not know what the strategies mean — callers own the option list.
type StrategyOption struct {
	Value string
	Label string
	Hint  string
}

// IssueMergeStrategy renders a single-select picker from the given options and
// writes the chosen Value to *selected. *selected is pre-populated with the
// first option's Value as the default.
func IssueMergeStrategy(selected *string, options []StrategyOption) *huh.Group {
	if len(options) > 0 {
		*selected = options[0].Value
	}

	huhOpts := make([]huh.Option[string], len(options))
	for i, o := range options {
		label := o.Label
		if o.Hint != "" {
			label = o.Label + "\n" + descStyle.Render(o.Hint)
		}

		huhOpts[i] = huh.NewOption(label, o.Value)
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Merge strategy:").
			Options(huhOpts...).
			Value(selected),
	)
}

// IssueMergeAuthor lets the user pick the squash commit author.
// authors[0] is expected to be the git config identity (pre-filled by the caller).
func IssueMergeAuthor(authors []string, author *string) *huh.Group {
	opts := make([]huh.Option[string], 0, len(authors))
	for _, a := range authors {
		opts = append(opts, huh.NewOption(a, a))
	}

	if len(opts) == 0 {
		opts = []huh.Option[string]{huh.NewOption("(no authors found)", "")}
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Squash commit author:").
			Options(opts...).
			Value(author),
	)
}

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

// IssueDeleteBranch asks whether to delete the local branch after closing.
func IssueDeleteBranch(branchName string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Delete local branch %q?", branchName)).
			Value(confirmed),
	)
}
