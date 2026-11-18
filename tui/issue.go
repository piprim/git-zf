package tui

import (
	"fmt"
	"slices"
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
	issueTableColWidthAssigned      = 8
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

// IssueTrackerPicker asks the branch type of a tracker issue; it lists the
// issues given (one, from the start flow) and the types.
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
			Value(selected),
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(branchType),
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

// IssueActions are the repository operations behind the dialogs and menu of
// the issue table, keyed by the row's slug. A nil action disables its key and
// menu entry.
type IssueActions struct {
	// Show renders the issue as `issue show` does; its error is shown in
	// place of the issue.
	Show func(slug string) (string, error)
	// Pick is set when the table picks an issue: enter and s pick the row
	// under the cursor and quit; n, c, a, l, e and r do nothing.
	Pick bool
	// Start is set when a pick starts a branch (s in the list, the start
	// flow's table): a closed issue is then refused with a notice. The
	// record picker leaves it unset and picks closed records too.
	Start bool
}

// IssuePick is how the issue table was quit.
type IssuePick int

const (
	IssuePickNone    IssuePick = iota // q or ctrl+c: nothing picked
	IssuePickRow                      // s or start from the menu (enter in pick mode): the slug is the row's issue
	IssuePickNew                      // n: a new issue
	IssuePickClose                    // c or close from the menu: close the row's issue by merging its branch
	IssuePickAbandon                  // a twice or abandon from the menu: abandon the row's issue
	IssuePickLabel                    // l or label from the menu: label the row's issue
	IssuePickSync                     // r: sync the issues (list mode)
	IssuePickComment                  // comment from the menu: comment on the row's issue
	IssuePickEdit                     // e or edit from the menu: edit the row's issue
)

type issueMenuEntry string

const (
	issueEntryShow    issueMenuEntry = "show"
	issueEntryStart   issueMenuEntry = "start"
	issueEntryClose   issueMenuEntry = "close (merge)"
	issueEntryEdit    issueMenuEntry = "edit"
	issueEntryLabel   issueMenuEntry = "label"
	issueEntryComment issueMenuEntry = "comment"
	issueEntryAbandon issueMenuEntry = "abandon (no undo)"
)

// issueMenuEntries returns the actions of row, in menu order.
func issueMenuEntries(row *issue.Row) []issueMenuEntry {
	open := row.MatchesStatus(statusOpen)
	repo := row.State != ""
	inProgress := row.Branch != nil && row.Branch.Status == branch.StatusInProgress

	entries := []issueMenuEntry{issueEntryShow}
	if open {
		entries = append(entries, issueEntryStart)
	}
	if open && inProgress {
		entries = append(entries, issueEntryClose)
	}
	if repo {
		entries = append(entries, issueEntryEdit, issueEntryLabel, issueEntryComment)
	}
	if repo && open && !inProgress {
		entries = append(entries, issueEntryAbandon)
	}

	return entries
}

// PickedIssue reports how the table was quit and, for IssuePickRow, the
// slug of the picked row. m is the model tea.Program.Run returned.
func PickedIssue(m tea.Model) (IssuePick, string) {
	t, ok := m.(*issueTableModel)
	if !ok {
		return IssuePickNone, ""
	}

	return t.pick, t.pickedSlug
}

// IssueTableOptions configures IssueTableModelWith.
type IssueTableOptions struct {
	Status  string // statusOpen, statusClosed, statusAll; "" = statusOpen
	Project string // projectAll or a project in the rows; "" = projectAll
	Filter  string // the / text at start
	Mine    bool   // m on at start
	Cursor  int    // the row under the cursor at start
	Show    bool   // the show dialog is open on that row at start
}

// IssueTableState is the options that rebuild m as the user left it: its
// filters and cursor, and the show dialog when e quit from it (the edit then
// returns to the dialog). The list loop passes it to the next IssueTableModelWith.
func IssueTableState(m tea.Model) IssueTableOptions {
	t, ok := m.(*issueTableModel)
	if !ok {
		return IssueTableOptions{}
	}

	return IssueTableOptions{
		Status:  t.statusFilter,
		Project: t.projectFilter,
		Filter:  t.filter.Value(),
		Mine:    t.mine,
		Cursor:  t.table.Cursor(),
		Show:    t.viewing,
	}
}

// IssueTableModel is the issue table. In list mode enter opens the row's
// action menu under the table (esc closes it), and s, c, a, l, e, n and r
// quit with the action picked (start, close by merging, abandon, label or
// edit the row, a new issue, a sync: see PickedIssue); in pick
// mode (actions.Pick) enter and s pick, n asks for a new issue.
func IssueTableModel(rows []issue.Row, initialStatus string, actions IssueActions) (tea.Model, error) {
	return IssueTableModelWith(rows, IssueTableOptions{Status: initialStatus}, actions)
}

// IssueTableModelWith is IssueTableModel configured by opts.
func IssueTableModelWith(rows []issue.Row, opts IssueTableOptions, actions IssueActions) (tea.Model, error) {
	projects := issue.UniqueProjects(rows)
	cols := buildIssueTableColumns(rows)
	width := 0
	for _, c := range cols {
		width += c.Width + 2
	}

	status := opts.Status
	if status == "" {
		status = statusOpen
	}

	project := projectAll
	if slices.Contains(projects, opts.Project) {
		project = opts.Project
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithFocused(true),
		btable.WithHeight(issueTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 64
	fi.SetValue(opts.Filter)

	dialog := viewport.New(width, issueTableHeight)
	dialog.SetHorizontalStep(horizontalStep)

	m := &issueTableModel{
		table:         t,
		allRows:       rows,
		filter:        fi,
		statusFilter:  status,
		projects:      projects,
		projectFilter: project,
		mine:          opts.Mine,
		actions:       actions,
		dialog:        dialog,
	}
	m.refreshRows()
	if opts.Cursor > 0 && len(m.visible) > 0 {
		c := opts.Cursor
		if c >= len(m.visible) {
			c = len(m.visible) - 1
		}
		m.table.SetCursor(c)
	}
	if opts.Show {
		m.openDialog()
	}

	return m, nil
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
		btable.Column{Title: "Assigned", Width: issueTableColWidthAssigned},
		btable.Column{Title: "Created", Width: issueTableColWidthCreated},
	)
}

// applyFilters builds the bubbletea table rows, keeping only those matching
// status, project, and free-text search. includeProject controls whether the
// Project cell is emitted (must match the table's column count).
func applyFilters(rows []issue.Row, status, text, project string, includeProject, mine bool) []btable.Row {
	kept := filterRows(rows, status, text, project, includeProject, mine)
	out := make([]btable.Row, len(kept))
	for i := range kept {
		out[i] = issue.RowCells(&kept[i], includeProject)
	}

	return out
}

// filterRows is applyFilters on the issue rows: the rows the table shows, in
// its order. mine keeps only the issues the tracker assigns to the user.
func filterRows(rows []issue.Row, status, text, project string, includeProject, mine bool) []issue.Row {
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

		if mine && !r.AssignedToMe {
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
	// mine keeps only the issues assigned to the user.
	mine       bool
	picking    bool
	pickCursor int
	// visible mirrors the table's rows: the one under the cursor is shown.
	visible []issue.Row
	actions IssueActions
	// viewing is set while the show dialog replaces the table.
	viewing bool
	dialog  viewport.Model
	// form is the active nested form ("menu" or "abandon"), under the table;
	// the fields hold its values.
	form                 *huh.Form
	formKind             string
	menuSlug, menuPicked string
	abandonOK            bool
	// notice is the line under the table until the next key.
	notice string
	// armAbandon is the slug a first a armed: a second a abandons it, any
	// other key disarms. An abandon has no undo and no reopen command.
	armAbandon string
	// pick and pickedSlug are how the table was quit: see PickedIssue.
	pick       IssuePick
	pickedSlug string
}

func (*issueTableModel) Init() tea.Cmd { return nil }

func (m *issueTableModel) refreshRows() {
	includeProj := len(m.projects) > 1
	m.visible = filterRows(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj, m.mine)
	m.table.SetRows(applyFilters(m.allRows, m.statusFilter, m.filter.Value(), m.projectFilter, includeProj, m.mine))
	if c := m.table.Cursor(); len(m.visible) > 0 && (c < 0 || c >= len(m.visible)) {
		m.table.SetCursor(0)
	}
}

func (m *issueTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		m.dialog.Width = size.Width - 4 // the border and its padding
	}
	if m.form != nil {
		return m.updateForm(msg)
	}
	if m.viewing {
		return m.updateViewing(msg)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		m.notice = ""
		if key.String() != "a" {
			m.armAbandon = ""
		}
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
			model, cmd, _ := m.pickRow(IssuePickEdit)

			return model, cmd
		case "ctrl+c":
			return m, tea.Quit
		}
	}

	var cmd tea.Cmd
	m.dialog, cmd = m.dialog.Update(msg)

	return m, cmd
}

func (m *issueTableModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		kind := m.formKind
		m.form = nil
		switch kind {
		case "menu":
			return m.runMenuEntry(issueMenuEntry(m.menuPicked))
		case "abandon":
			if m.abandonOK {
				model, qcmd, _ := m.pickRow(IssuePickAbandon)

				return model, qcmd
			}
		}
	case huh.StateAborted:
		m.form = nil
	case huh.StateNormal:
	}

	return m, cmd
}

// openMenu opens the contextual menu of the row under the cursor, dropping
// entries whose action callback is nil.
func (m *issueTableModel) openMenu() tea.Cmd {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.visible) {
		return nil
	}
	row := &m.visible[c]
	var opts []MenuOption
	for _, e := range issueMenuEntries(row) {
		if e == issueEntryShow && m.actions.Show == nil {
			continue
		}
		opts = append(opts, MenuOption{Label: string(e), Value: string(e)})
	}
	if len(opts) == 0 {
		return nil
	}
	m.menuSlug = row.IssueSlug
	m.menuPicked = opts[0].Value
	m.formKind = "menu"
	title := "Issue " + row.IssueSlug + ": " + row.Title
	m.form = Form(MenuSelect(title, opts, &m.menuPicked)).WithWidth(m.dialog.Width)

	return m.form.Init()
}

func (m *issueTableModel) runMenuEntry(e issueMenuEntry) (tea.Model, tea.Cmd) {
	switch e {
	case issueEntryShow:
		m.openDialog()

		return m, nil
	case issueEntryEdit:
		model, cmd, _ := m.pickRow(IssuePickEdit)

		return model, cmd
	case issueEntryStart:
		model, cmd, _ := m.pickRow(IssuePickRow)

		return model, cmd
	case issueEntryClose:
		model, cmd, _ := m.pickRow(IssuePickClose)

		return model, cmd
	case issueEntryLabel:
		model, cmd, _ := m.pickRow(IssuePickLabel)

		return model, cmd
	case issueEntryComment:
		model, cmd, _ := m.pickRow(IssuePickComment)

		return model, cmd
	case issueEntryAbandon:
		m.abandonOK = false
		m.formKind = "abandon"
		m.form = Form(IssueConfirm("Abandon "+m.menuSlug+"? There is no undo.", &m.abandonOK)).WithWidth(m.dialog.Width)

		return m, m.form.Init()
	}

	return m, nil
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

// pickRow quits with the row under the cursor picked for pick; nothing
// happens on an empty table, and a start (IssuePickRow with actions.Start)
// refuses a closed issue (closed record, merged branch) with a notice.
func (m *issueTableModel) pickRow(pick IssuePick) (tea.Model, tea.Cmd, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.visible) {
		return m, nil, true
	}
	row := &m.visible[c]
	if pick == IssuePickRow && m.actions.Start && !row.MatchesStatus(statusOpen) {
		m.notice = row.IssueSlug + " is closed: not started"

		return m, nil, true
	}
	m.pick, m.pickedSlug = pick, row.IssueSlug

	return m, tea.Quit, true
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
		if m.actions.Pick {
			return m.pickRow(IssuePickRow)
		}

		return m, m.openMenu(), true
	case "s":
		return m.pickRow(IssuePickRow)
	case "n":
		if !m.actions.Pick {
			m.pick = IssuePickNew

			return m, tea.Quit, true
		}

		return m, nil, true
	case "l":
		if m.actions.Pick {
			return m, nil, true
		}

		return m.pickRow(IssuePickLabel)
	case "c":
		if m.actions.Pick {
			return m, nil, true
		}

		return m.pickRow(IssuePickClose)
	case "a":
		c := m.table.Cursor()
		if m.actions.Pick || c < 0 || c >= len(m.visible) {
			return m, nil, true
		}
		if slug := m.visible[c].IssueSlug; m.armAbandon != slug {
			m.armAbandon, m.notice = slug, "a again abandons "+slug+" (no undo)"

			return m, nil, true
		}

		return m.pickRow(IssuePickAbandon)
	case "r":
		if m.actions.Pick {
			return m, nil, true
		}
		m.pick = IssuePickSync

		return m, tea.Quit, true
	case "e":
		if m.actions.Pick {
			return m, nil, true
		}

		return m.pickRow(IssuePickEdit)
	case "m":
		m.mine = !m.mine
		m.refreshRows()

		return m, nil, true
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

// renderPicker is an inline list of options under title, the one at cursor
// highlighted.
func renderPicker(title string, cursor int, opts []string) string {
	lines := make([]string, len(opts))

	for i, o := range opts {
		if i == cursor {
			lines[i] = activeTabStyle.Render("> " + o)
		} else {
			lines[i] = inactiveTabStyle.Render("  " + o)
		}
	}

	return title + " (↑↓ / j·k, enter: confirm, esc: cancel):\n" + strings.Join(lines, "\n")
}

var dialogStyle = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)

func (m *issueTableModel) View() string {
	if m.form != nil {
		return m.table.View() + "\n\n" + m.form.View()
	}
	if m.viewing {
		return dialogStyle.Render(m.dialog.View()) + "\n\n↑↓←→ scroll · e: edit · esc: back"
	}

	tabs := renderStatusTabs(m.statusFilter)

	if m.picking {
		return m.table.View() + "\n\n" + tabs + "\n\n" + renderPicker("Pick project", m.pickCursor, projectPickerOptions(m.projects))
	}

	hint := m.hint()
	proj := ""

	if len(m.projects) > 0 {
		proj = "    Project: " + activeTabStyle.Render(m.projectFilter)
	}

	mine := "  " + inactiveTabStyle.Render("Mine")
	if m.mine {
		mine = "  " + activeTabStyle.Render("[ Mine ]")
	}

	filters := m.table.View() + "\n\n" + tabs + mine + proj + "    "
	if m.filtering {
		return filters + "/" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}
	view := filters + m.filterHint() + "\n" + hint
	if m.notice != "" {
		return view + "\n" + m.notice
	}

	return view
}

// filterHint is the keys of the filters, on their line.
func (m *issueTableModel) filterHint() string {
	pairs := []string{"tab", "status"}
	if len(m.projects) > 0 {
		pairs = append(pairs, "p", "project")
	}

	return keyHints(append(pairs, "m", "mine", "/", "filter")...)
}

// hint is the key line under the filters, for the mode. The list line names
// every action and fits 90 columns without q, which every table quits with.
func (m *issueTableModel) hint() string {
	if m.actions.Pick {
		return keyHints("enter", "pick", "q", "quit")
	}

	return keyHints("enter", "actions", "e", "edit", "s", "start", "n", "new", "c", "close", "a", "abandon", "l", "label", "r", "sync")
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
