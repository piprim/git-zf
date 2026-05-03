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

func IssueTableModel(rows []store.IssueRow) (tea.Model, error) {
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
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
			pkg.TrackerStatusOrNA(r.TrackerStatus),
			pkg.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.CreatedAt.Format("2006-01-02") }),
		}
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(tableRows),
		btable.WithFocused(true),
		btable.WithHeight(issueTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 64

	m := &issueTableModel{table: t, allRows: tableRows, filter: fi}

	return m, nil
}

type issueTableModel struct {
	table     btable.Model
	allRows   []btable.Row
	filter    textinput.Model
	filtering bool
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
				m.table.SetRows(m.allRows)

				return m, nil
			case "enter":
				m.filtering = false
				m.filter.Blur()

				return m, nil
			}

			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(msg)
			m.table.SetRows(filterIssueRows(m.allRows, m.filter.Value()))

			return m, cmd
		}

		switch key.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "/":
			m.filtering = true
			return m, m.filter.Focus()
		}
	}

	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

func (m *issueTableModel) View() string {
	if m.filtering {
		return m.table.View() + "\n\n/" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}

	return m.table.View() + "\n\nPress / to filter · q to quit."
}

func filterIssueRows(rows []btable.Row, query string) []btable.Row {
	if query == "" {
		return rows
	}

	q := strings.ToLower(query)
	filtered := make([]btable.Row, 0, len(rows))

	for _, row := range rows {
		for _, cell := range row {
			if strings.Contains(strings.ToLower(cell), q) {
				filtered = append(filtered, row)

				break
			}
		}
	}

	return filtered
}
