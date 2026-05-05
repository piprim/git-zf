package tui

import (
	"errors"
	"fmt"
	"strings"

	btable "github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/piprim/git-zf/internal/pkg"
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
					"Start working on an issue (create branch)"), IssueActionNameStart),
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
			Filtering(true).
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
			Value(selected).Filtering(true),
	)
}

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

type issueTableModel struct {
	table        btable.Model
	allRows      []store.IssueRow
	filter       textinput.Model
	filtering    bool
	statusFilter string
}

func (*issueTableModel) Init() tea.Cmd { return nil }

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

func (m *issueTableModel) View() string {
	tabs := renderStatusTabs(m.statusFilter)

	if m.filtering {
		return m.table.View() + "\n\n" + tabs + "    /" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}

	return m.table.View() + "\n\n" + tabs + "    Press / to filter · tab: status · q to quit"
}

// IssueBranchPicker presents in-progress branches for the close flow.
// The branch matching currentBranch is pre-selected; falls back to the first row.
func IssueBranchPicker(rows []store.BranchRow, currentBranch string, selected *store.BranchRow) *huh.Group {
	opts := make([]huh.Option[store.BranchRow], len(rows))
	for i, r := range rows {
		label := fmt.Sprintf("[%s] %s (%s)", r.IssueSlug, r.Title, r.BranchName)
		opts[i] = huh.NewOption(label, r)
	}

	// Pre-select current branch; default to first row if not found.
	*selected = rows[0]
	for _, r := range rows {
		if r.BranchName == currentBranch {
			*selected = r

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

// IssueMergeStrategy lets the user choose between squash (default) and classic merge.
func IssueMergeStrategy(squash *bool) *huh.Group {
	*squash = true

	return huh.NewGroup(
		huh.NewSelect[bool]().
			Title("Merge strategy:").
			Options(
				huh.NewOption("Squash — combine into one commit (default)", true),
				huh.NewOption("Classic — preserve full history (--no-ff)", false),
			).
			Value(squash),
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
// author is empty for classic merges.
func IssueMergeConfirm(branchName, baseBranch, strategy, author string, confirmed *bool) *huh.Group {
	desc := fmt.Sprintf("%s → %s (%s)", branchName, baseBranch, strategy)
	if author != "" {
		desc += "\nAuthor: " + author
	}

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
