package tui

import (
	"fmt"
	"strings"

	btable "github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

const (
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
)

var (
	activeTabStyle   = lipgloss.NewStyle().Bold(true)
	inactiveTabStyle = lipgloss.NewStyle().Faint(true)
)

// IssueInput is the manual issue form of `issue start` / `branch new`. An
// empty issue ID means "create a new issue in the repository": the caller
// then assigns the new record's ID.
func IssueInput(issueID, title, branchType *string, allowedBranchTypes []string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Issue ID (leave empty to create a new repo issue):").
			Placeholder("ABC-42").
			Value(issueID),
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(requiredText).
			Value(title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(branchTypeOptions(allowedBranchTypes)...).
			Value(branchType),
	)
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
	selected *int,
	branchTypes []string,
	branchType *string) *huh.Group {
	opts := make([]huh.Option[int], len(issues))
	for i, iss := range issues {
		label := fmt.Sprintf("[%s] %s", iss.ID, iss.Subject)
		opts[i] = huh.NewOption(label, i)
	}

	if len(opts) == 0 {
		opts = []huh.Option[int]{huh.NewOption("(no issues found)", -1)}
	}

	typeOpts := make([]huh.Option[string], len(branchTypes))
	for i, tp := range branchTypes {
		typeOpts[i] = huh.NewOption(tp, tp)
	}

	if len(typeOpts) == 0 {
		typeOpts = []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	return huh.NewGroup(
		huh.NewSelect[int]().
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
}

// IssueTableModel is the `issue list` table. Enter opens the row's issue in a
// dialog that replaces the table, e opens its edit form; esc closes either.
func IssueTableModel(rows []issue.Row, initialStatus string, actions IssueActions) (tea.Model, error) {
	projects := issue.UniqueProjects(rows)
	includeProj := len(projects) > 1
	cols := buildIssueTableColumns(rows)
	width := 0
	for _, c := range cols {
		width += c.Width + 2
	}

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
		visible:       filterRows(rows, status, "", projectAll, includeProj),
		actions:       actions,
		dialog:        viewport.New(width, issueTableHeight),
	}, nil
}

// buildIssueTableColumns returns the bubbletea columns. The Project column
// appears only when rows span more than one project.
func buildIssueTableColumns(rows []issue.Row) []btable.Column {
	cols := []btable.Column{
		{Title: "Issue ID", Width: issueTableColWidthIssueID},
	}

	if len(issue.UniqueProjects(rows)) > 1 {
		cols = append(cols, btable.Column{Title: "Project", Width: issueTableColWidthProject})
	}

	return append(cols,
		btable.Column{Title: "Title", Width: issueTableColWidthTitle},
		btable.Column{Title: "Branch", Width: issueTableColWidthBranch},
		btable.Column{Title: "Local Status", Width: issueTableColWidthLocalStatus},
		btable.Column{Title: "Issue Status", Width: issueTableColWidthTrackerStatus},
		btable.Column{Title: "Created", Width: issueTableColWidthCreated},
	)
}

// applyFilters builds the bubbletea table rows, keeping only those matching
// status, project, and free-text search. includeProject controls whether the
// Project cell is emitted (must match the table's column count).
func applyFilters(rows []issue.Row, status, text, project string, includeProject bool) []btable.Row {
	kept := filterRows(rows, status, text, project, includeProject)
	out := make([]btable.Row, len(kept))
	for i := range kept {
		out[i] = issue.RowCells(&kept[i], includeProject)
	}

	return out
}

// filterRows is applyFilters on the issue rows: the rows the table shows, in
// its order.
func filterRows(rows []issue.Row, status, text, project string, includeProject bool) []issue.Row {
	q := strings.ToLower(text)
	out := make([]issue.Row, 0, len(rows))

	for i := range rows {
		r := &rows[i]
		if !r.MatchesStatus(status) {
			continue
		}

		if project != projectAll && r.Project != project {
			continue
		}

		row := issue.RowCells(r, includeProject)

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

		out = append(out, *r)
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

// projectPickerOptions returns the ordered list ["all", p1, p2, …] used both
// for rendering the picker and for resolving the cursor position.
func projectPickerOptions(projects []string) []string {
	opts := make([]string, 0, len(projects)+1)
	opts = append(opts, projectAll)

	return append(opts, projects...)
}

type issueTableModel struct {
	table         btable.Model
	allRows       []issue.Row
	filter        textinput.Model
	filtering     bool
	statusFilter  string
	projects      []string
	projectFilter string
	picking       bool
	pickCursor    int
	// visible mirrors the table's rows: the one under the cursor is shown.
	visible []issue.Row
	actions IssueActions
	// viewing is set while the show dialog replaces the table.
	viewing bool
	dialog  viewport.Model
	// form is the edit dialog while it replaces the table; the fields are
	// its values.
	form                                 *huh.Form
	editSlug, editTitle, editDescription string
	// notice is the line under the table until the next key.
	notice string
}

func (*issueTableModel) Init() tea.Cmd { return nil }

func (m *issueTableModel) refreshRows() {
	includeProj := len(m.projects) > 1
	m.visible = filterRows(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj)
	m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj))
}

func (m *issueTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.dialog.Width = size.Width - 4 // the border and its padding
	}
	if m.form != nil {
		return m.updateEditing(msg)
	}
	if m.viewing {
		return m.updateViewing(msg)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		m.notice = ""
		if m.picking {
			return m.updatePicking(key)
		}
		if m.filtering {
			return m.updateFiltering(msg, key)
		}
		if model, cmd, handled := m.updateNormal(key); handled {
			return model, cmd
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)
	return m, cmd
}

func (m *issueTableModel) updateViewing(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc", "q":
			m.viewing = false

			return m, nil
		case "e":
			return m, m.openEdit()
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.dialog, cmd = m.dialog.Update(msg)

	return m, cmd
}

func (m *issueTableModel) updateEditing(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "esc":
			m.form = nil

			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	f, cmd := m.form.Update(msg)
	m.form = f.(*huh.Form)
	switch m.form.State {
	case huh.StateCompleted:
		m.form = nil
		m.saveEdit()
	case huh.StateAborted:
		m.form = nil
	case huh.StateNormal:
	}

	return m, cmd
}

// openEdit replaces the table with the edit form of the issue under the
// cursor; a load error is the notice instead.
func (m *issueTableModel) openEdit() tea.Cmd {
	c := m.table.Cursor()
	if m.actions.Load == nil || m.actions.Edit == nil || c < 0 || c >= len(m.visible) {
		return nil
	}
	m.viewing = false
	slug := m.visible[c].IssueSlug
	title, description, err := m.actions.Load(slug)
	if err != nil {
		m.notice = "Error: " + err.Error()

		return nil
	}
	m.editSlug, m.editTitle, m.editDescription = slug, title, description
	m.form = huh.NewForm(IssueEditForm(&m.editTitle, &m.editDescription)).WithWidth(m.dialog.Width)

	return m.form.Init()
}

// saveEdit gives the form's values to Edit and shows the row's new title.
func (m *issueTableModel) saveEdit() {
	line, err := m.actions.Edit(m.editSlug, m.editTitle, m.editDescription)
	if err != nil {
		m.notice = "Error: " + err.Error()

		return
	}
	m.notice = line
	for i := range m.allRows {
		if m.allRows[i].IssueSlug == m.editSlug {
			m.allRows[i].Title = strings.TrimSpace(m.editTitle)
		}
	}
	m.refreshRows()
}

// openDialog shows the issue under the cursor, or the error of loading it.
func (m *issueTableModel) openDialog() {
	c := m.table.Cursor()
	if m.actions.Show == nil || c < 0 || c >= len(m.visible) {
		return
	}
	text, err := m.actions.Show(m.visible[c].IssueSlug)
	if err != nil {
		text = "Error: " + err.Error()
	}
	m.dialog.SetContent(text)
	m.dialog.GotoTop()
	m.viewing = true
}

func (m *issueTableModel) updatePicking(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	opts := projectPickerOptions(m.projects)
	switch key.String() {
	case "esc":
		m.picking = false

		return m, nil
	case "enter":
		m.picking = false
		m.projectFilter = opts[m.pickCursor]
		m.refreshRows()

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

func (m *issueTableModel) updateFiltering(msg tea.Msg, key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		m.filtering = false
		m.filter.Blur()
		m.filter.Reset()
		m.refreshRows()

		return m, nil
	case "enter":
		m.filtering = false
		m.filter.Blur()

		return m, nil
	}

	var cmd tea.Cmd
	m.filter, cmd = m.filter.Update(msg)
	m.refreshRows()

	return m, cmd
}

func (m *issueTableModel) updateNormal(key tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "q", "ctrl+c":
		return m, tea.Quit, true
	case "enter":
		m.openDialog()

		return m, nil, true
	case "e":
		return m, m.openEdit(), true
	case "tab":
		m.statusFilter = nextStatus(m.statusFilter)
		m.refreshRows()

		return m, nil, true
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

		return m, nil, true
	case "/":
		m.filtering = true

		return m, m.filter.Focus(), true
	}

	return m, nil, false
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

var dialogStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

func (m *issueTableModel) View() string {
	if m.form != nil {
		return dialogStyle.Render(m.form.View()) + "\n\nenter: save · esc: cancel"
	}
	if m.viewing {
		return dialogStyle.Render(m.dialog.View()) + "\n\n↑↓ scroll · e: edit · esc: back"
	}

	tabs := renderStatusTabs(m.statusFilter)

	if m.picking {
		return m.table.View() + "\n\n" + tabs + "\n\n" + renderProjectPicker(m.pickCursor, m.projects)
	}

	hint := "enter: show · e: edit · / to filter · tab: status · q to quit"
	proj := ""

	if len(m.projects) > 0 {
		hint = "enter: show · e: edit · / to filter · tab: status · p: project · q to quit"
		proj = "    Project: " + activeTabStyle.Render(m.projectFilter)
	}

	view := m.table.View() + "\n\n" + tabs + proj

	if m.filtering {
		return view + "    /" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}
	if m.notice != "" {
		return view + "    " + hint + "\n" + m.notice
	}

	return view + "    " + hint
}

// BranchPicker presents a list of branches under title (close and the review
// commands). The first row for which preselect reports true is pre-selected;
// it falls back to the first row.
func BranchPicker(
	title string, rows []branch.Row, preselect func(*branch.Row) bool, selected *branch.Row,
) *huh.Group {
	opts := make([]huh.Option[branch.Row], len(rows))
	for i := range rows {
		label := fmt.Sprintf("[%s] %s (%s)", rows[i].IssueSlug, rows[i].Title, rows[i].BranchName)
		opts[i] = huh.NewOption(label, rows[i])
	}

	*selected = rows[0]
	for i := range rows {
		if preselect(&rows[i]) {
			*selected = rows[i]

			break
		}
	}

	return huh.NewGroup(
		huh.NewSelect[branch.Row]().
			Title(title).
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

// IssueDeleteBranch confirms deleting the merged feature branch locally and on
// the remote.
func IssueDeleteBranch(branchName string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Delete branch %q locally and on the remote?", branchName)).
			Value(confirmed),
	)
}

// IssueRemoveWorktree confirms removing the linked worktree that held the
// merged branch. Defaults to yes: the branch is merged, the directory is
// disposable.
func IssueRemoveWorktree(path string, confirmed *bool) *huh.Group {
	*confirmed = true

	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Remove worktree %q?", path)).
			Description("The branch is merged; git worktree remove deletes the directory (refused if it has changes).").
			Value(confirmed),
	)
}
