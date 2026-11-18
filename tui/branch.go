package tui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	btable "github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/piprim/git-zf/branch"
)

const (
	branchTableColWidthIssueID = 10
	branchTableColWidthTitle   = 28
	branchTableColWidthBranch  = 38
	branchTableColWidthType    = 8
	branchTableColWidthStatus  = 12
	branchTableColWidthCreated = 10
	branchTableHeight          = 20
	BranchTableHeaderColor     = lipgloss.Color("63")
)

// BranchPruneConfirm asks whether to record nClosed branches as closed and
// nMerged as merged.
func BranchPruneConfirm(nClosed, nMerged int, confirmed *bool) *huh.Group {
	title := fmt.Sprintf(
		"Record %d branch(es) as closed and %d as merged. Proceed?",
		nClosed, nMerged,
	)

	return huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Value(confirmed),
	)
}

// BranchPick is how the branch table was quit.
type BranchPick int

const (
	BranchPickNone         BranchPick = iota // q, ctrl+c
	BranchPickRow                            // enter: the row under the cursor
	BranchPickNew                            // n
	BranchPickPrune                          // p
	BranchPickPruneTracker                   // P
	BranchPickKey                            // an action key (C, c, m, u, a, t, l, D): see PickedBranchKey
)

var branchActionKeys = []struct {
	key, label string
}{
	{"C", "checkout"},
	{"c", "close"},
	{"m", "merge"},
	{"u", "pull request"},
	{"a", "abandon"},
	{"t", "track"},
	{"l", "log"},
	{"D", "delete"},
}

// BranchStatusPlain is the S option for rows with no record.
const BranchStatusPlain = "plain"

// BranchTypeNone is the T option for rows with no record.
const BranchTypeNone = "none"

var branchStatuses = []string{BranchStatusPlain, branch.StatusInProgress, branch.StatusMerged, branch.StatusClosed}

// BranchTableOptions configures BranchTableModel.
type BranchTableOptions struct {
	Types        []string // the T options, from the config's commit types; the rows' types and "none" are added
	TypesChecked []string // T options checked at start; nil = all
	Statuses     []string // S options checked at start; nil = all four
	Remote       string   // shown before a RemoteOnly row's name; "" hides the R key
	Remotes      bool     // R on at start
	Filter       string   // the / text at start
	Cursor       int      // the row under the cursor at start
	// Offers reports whether row offers the action of key; nil offers every key.
	Offers func(row branch.Row, key string) bool
}

// BranchTableState is the options that rebuild m as the user left it: its
// filters and cursor. The table loop passes it to the next BranchTableModel.
func BranchTableState(m tea.Model) BranchTableOptions {
	t, ok := m.(*branchTableModel)
	if !ok {
		return BranchTableOptions{}
	}
	o := t.opts
	o.Statuses, o.TypesChecked = checked(branchStatuses, t.statuses), checked(t.allTypes, t.types)
	o.Remotes, o.Filter, o.Cursor = t.remotes, t.filter.Value(), t.table.Cursor()

	return o
}

// PickedBranch reports how the table was quit and, for BranchPickRow or
// BranchPickKey, a copy of the picked row. m is the model tea.Program.Run
// returned.
func PickedBranch(m tea.Model) (BranchPick, *branch.Row) {
	t, ok := m.(*branchTableModel)
	if !ok {
		return BranchPickNone, nil
	}

	return t.pick, t.picked
}

// PickedBranchKey reports the action key of a BranchPickKey quit, "" otherwise.
func PickedBranchKey(m tea.Model) string {
	t, ok := m.(*branchTableModel)
	if !ok || t.pick != BranchPickKey {
		return ""
	}

	return t.pickedKey
}

// BranchTableModel is the table of every local branch: S and T open
// multi-select filters under the table, R shows the remote-only rows, / filters
// by text, enter picks the row under the cursor, the action keys (C, c, m, u,
// a, t, l, D) and n, p and P quit with their pick (see PickedBranch).
func BranchTableModel(rows []branch.Row, opts BranchTableOptions) tea.Model {
	cols := []btable.Column{
		{Title: "Branch", Width: branchTableColWidthBranch},
		{Title: "Issue", Width: branchTableColWidthIssueID},
		{Title: "Type", Width: branchTableColWidthType},
		{Title: "Status", Width: branchTableColWidthStatus},
		{Title: "Title", Width: branchTableColWidthTitle},
		{Title: "Created", Width: branchTableColWidthCreated},
		{Title: "Last commit", Width: branchTableColWidthCreated},
	}
	width := 0
	for _, c := range cols {
		width += c.Width + 2
	}

	t := btable.New(btable.WithColumns(cols), btable.WithFocused(true), btable.WithHeight(branchTableHeight))
	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	fi := textinput.New()
	fi.Placeholder = "type to filter…"
	fi.CharLimit = 64

	m := &branchTableModel{table: t, allRows: rows, filter: fi, opts: opts, width: width, now: time.Now(), remotes: opts.Remotes}
	// The T options: the config's types, then any other type a row carries
	// (commit types are per-clone config), then none.
	m.allTypes = slices.Clone(opts.Types)
	for i := range rows {
		if ty := rowType(&rows[i]); ty != BranchTypeNone && !slices.Contains(m.allTypes, ty) {
			m.allTypes = append(m.allTypes, ty)
		}
	}
	m.allTypes = append(m.allTypes, BranchTypeNone)
	m.types = toSet(m.allTypes)
	if opts.TypesChecked != nil {
		m.types = toSet(opts.TypesChecked)
	}
	m.statuses = toSet(branchStatuses)
	if opts.Statuses != nil {
		m.statuses = toSet(opts.Statuses)
	}
	m.filter.SetValue(opts.Filter)
	m.refreshRows()
	if opts.Cursor > 0 && opts.Cursor < len(m.visible) {
		m.table.SetCursor(opts.Cursor)
	}

	return m
}

func toSet(vals []string) map[string]bool {
	set := make(map[string]bool, len(vals))
	for _, v := range vals {
		set[v] = true
	}

	return set
}

type branchTableModel struct {
	table     btable.Model
	allRows   []branch.Row
	visible   []branch.Row // mirrors the table's rows
	filter    textinput.Model
	filtering bool
	statuses  map[string]bool
	types     map[string]bool
	remotes   bool
	// form is the S or T multi-select while it is open under the table;
	// pending is its value, applied on completion.
	form      *huh.Form
	formKind  string
	pending   []string
	notice    string
	armKey    string
	armBranch string
	pick      BranchPick
	picked    *branch.Row
	pickedKey string
	opts      BranchTableOptions
	allTypes []string // the T options
	width    int
	now      time.Time
}

func (*branchTableModel) Init() tea.Cmd { return nil }

func (m *branchTableModel) setStatuses(s []string) { m.statuses = toSet(s); m.refreshRows() }
func (m *branchTableModel) setTypes(s []string)    { m.types = toSet(s); m.refreshRows() }

func rowStatus(r *branch.Row) string {
	if r.Plain {
		return BranchStatusPlain
	}

	return r.Status
}

func rowType(r *branch.Row) string {
	if r.Plain || r.Type == "" {
		return BranchTypeNone
	}

	return r.Type
}

func (m *branchTableModel) cells(r *branch.Row) btable.Row {
	name := "  "
	if r.Current {
		name = "* "
	}
	if r.RemoteOnly {
		name += m.opts.Remote + "/"
	}
	created := ""
	if !r.CreatedAt.IsZero() {
		created = r.CreatedAt.Format("2006-01-02")
	}

	return btable.Row{name + r.BranchName, r.IssueSlug, r.Type, r.Status, r.Title, created, relativeTime(r.LastCommit, m.now)}
}

func (m *branchTableModel) refreshRows() {
	q := strings.ToLower(m.filter.Value())
	m.visible = m.visible[:0]
	var tableRows []btable.Row
	for i := range m.allRows {
		r := &m.allRows[i]
		if (r.RemoteOnly && !m.remotes) || !m.statuses[rowStatus(r)] || !m.types[rowType(r)] {
			continue
		}
		cells := m.cells(r)
		if q != "" && !slices.ContainsFunc(cells, func(c string) bool { return strings.Contains(strings.ToLower(c), q) }) {
			continue
		}
		m.visible = append(m.visible, *r)
		tableRows = append(tableRows, cells)
	}
	m.table.SetRows(tableRows)
	// SetRows clamps the cursor to len-1, which is -1 on an empty table;
	// bring it back on the first row when rows return.
	if c := m.table.Cursor(); len(tableRows) > 0 && (c < 0 || c >= len(tableRows)) {
		m.table.SetCursor(0)
	}
}

func (m *branchTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.form != nil {
		return m.updateForm(msg)
	}
	if key, ok := msg.(tea.KeyMsg); ok {
		if m.filtering {
			return m.updateFiltering(msg, key)
		}
		m.notice = ""
		if key.String() == "esc" {
			return m.updateEsc()
		}
		if key.String() != m.armKey {
			m.armKey, m.armBranch = "", ""
		}
		if model, cmd, handled := m.updateNormal(key); handled {
			return model, cmd
		}
	}
	var cmd tea.Cmd
	m.table, cmd = m.table.Update(msg)

	return m, cmd
}

// updateEsc handles esc at the table's top level: it dismisses what there is
// to dismiss, an armed key then an applied filter, and closes the table as q
// does when there is nothing.
func (m *branchTableModel) updateEsc() (tea.Model, tea.Cmd) {
	switch {
	case m.armKey != "":
		m.armKey, m.armBranch = "", ""
	case m.filter.Value() != "":
		m.filter.Reset()
		m.refreshRows()
	default:
		model, cmd, _ := m.quitWith(BranchPickNone)

		return model, cmd
	}

	return m, nil
}

func (m *branchTableModel) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		if m.formKind == "S" {
			m.setStatuses(m.pending)
		} else {
			m.setTypes(m.pending)
		}
	case huh.StateAborted:
		m.form = nil
	case huh.StateNormal:
	}

	return m, cmd
}

// openForm opens the S (statuses) or T (types) multi-select, checked as the
// current filter is.
func (m *branchTableModel) openForm(kind string) tea.Cmd {
	all, set, title := branchStatuses, m.statuses, "Statuses"
	if kind == "T" {
		all, set, title = m.allTypes, m.types, "Types"
	}
	m.pending = m.pending[:0]
	opts := make([]huh.Option[string], len(all))
	for i, v := range all {
		opts[i] = huh.NewOption(v, v)
		if set[v] {
			m.pending = append(m.pending, v)
		}
	}
	m.formKind = kind
	m.form = Form(huh.NewGroup(huh.NewMultiSelect[string]().Title(title).Value(&m.pending).Options(opts...))).WithWidth(m.width)

	return m.form.Init()
}

func (m *branchTableModel) updateFiltering(msg tea.Msg, key tea.KeyMsg) (tea.Model, tea.Cmd) {
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

func (m *branchTableModel) quitWith(pick BranchPick) (tea.Model, tea.Cmd, bool) {
	m.pick = pick

	return m, tea.Quit, true
}

func (m *branchTableModel) updateNormal(key tea.KeyMsg) (tea.Model, tea.Cmd, bool) {
	switch key.String() {
	case "q", "ctrl+c":
		return m.quitWith(BranchPickNone)
	case "enter":
		c := m.table.Cursor()
		if c < 0 || c >= len(m.visible) {
			return m, nil, true
		}
		row := m.visible[c]
		m.picked = &row

		return m.quitWith(BranchPickRow)
	case "n":
		return m.quitWith(BranchPickNew)
	case "p":
		return m.quitWith(BranchPickPrune)
	case "P":
		return m.quitWith(BranchPickPruneTracker)
	case "S", "T":
		return m, m.openForm(key.String()), true
	case "R":
		if m.opts.Remote != "" {
			m.remotes = !m.remotes
			m.refreshRows()
		}

		return m, nil, true
	case "/":
		m.filtering = true

		return m, m.filter.Focus(), true
	}

	for _, ak := range branchActionKeys {
		if key.String() == ak.key {
			return m.pickActionKey(ak.key, ak.label)
		}
	}

	return m, nil, false
}

func (m *branchTableModel) pickActionKey(key, label string) (tea.Model, tea.Cmd, bool) {
	c := m.table.Cursor()
	if c < 0 || c >= len(m.visible) {
		return m, nil, true
	}
	row := m.visible[c]
	if m.opts.Offers != nil && !m.opts.Offers(row, key) {
		m.notice = "no " + label + " for this branch"

		return m, nil, true
	}
	switch key {
	case "a":
		if m.armKey != "a" || m.armBranch != row.BranchName {
			m.armKey, m.armBranch = "a", row.BranchName
			m.notice = "a again abandons " + row.BranchName + " (no undo)"

			return m, nil, true
		}
	case "D":
		if m.armKey != "D" || m.armBranch != row.BranchName {
			m.armKey, m.armBranch = "D", row.BranchName
			m.notice = "D again deletes " + row.BranchName

			return m, nil, true
		}
	}
	m.picked = &row
	m.pickedKey = key

	return m.quitWith(BranchPickKey)
}

// checked lists the values of all that set holds, in all's order.
func checked(all []string, set map[string]bool) []string {
	var out []string
	for _, v := range all {
		if set[v] {
			out = append(out, v)
		}
	}

	return out
}

func (m *branchTableModel) View() string {
	body := m.table.View()
	if len(m.visible) == 0 {
		body = "No branch matches."
	}
	if m.form != nil {
		return body + "\n\n" + m.form.View()
	}

	var active []string
	if s := checked(branchStatuses, m.statuses); len(s) != len(branchStatuses) {
		active = append(active, "status: "+strings.Join(s, ","))
	}
	if ts := checked(m.allTypes, m.types); len(ts) != len(m.allTypes) {
		active = append(active, "type: "+strings.Join(ts, ","))
	}
	if m.remotes {
		active = append(active, "remotes: on")
	}
	view := body + "\n\n"
	if len(active) > 0 {
		view += activeTabStyle.Render(strings.Join(active, "  ")) + "\n"
	}
	if m.filtering {
		return view + "/" + m.filter.View() + "  (esc: clear  enter: confirm)"
	}
	filters := []string{"S", "status", "T", "type"}
	if m.opts.Remote != "" {
		filters = append(filters, "R", "remotes")
	}
	filters = append(filters, "/", "filter")

	actions := []string{"enter", "actions"}
	for _, ak := range branchActionKeys {
		actions = append(actions, ak.key, ak.label)
	}
	actions = append(actions, "n", "new", "p", "prune", "P", "prune-tracker")

	out := view + keyHints(filters...) + "\n" + keyHints(actions...)
	if m.notice != "" {
		return out + "\n" + m.notice
	}

	return out
}

// relativeTime is how long before now t is: "2h ago", "1d ago", "3w ago",
// "5mo ago", "2y ago"; "" for a zero t.
func relativeTime(t, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	days := int(d.Hours() / 24)
	switch {
	case days < 1:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case days < 7:
		return fmt.Sprintf("%dd ago", days)
	case days < 30:
		return fmt.Sprintf("%dw ago", days/7)
	case days < 365:
		return fmt.Sprintf("%dmo ago", days/30)
	}

	return fmt.Sprintf("%dy ago", days/365)
}

// BranchConflictPicker shows a 3-option picker when a deterministic branch
// name already exists locally. The selected value is stored in *action and
// is one of: "checkout", "variant", "abort".
func BranchConflictPicker(branchName string, action *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title(fmt.Sprintf("Branch %q already exists.", branchName)).
			Options(
				huh.NewOption("Checkout the existing branch", "checkout"),
				huh.NewOption("Create a variant (you'll be asked for a label)", "variant"),
				huh.NewOption("Abort", "abort"),
			).
			Value(action),
	)
}

// VariantLabelInput prompts for a variant label and validates inline that
// the input slugs to a non-empty value.
func VariantLabelInput(label *string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Variant label (e.g. spike, approach-b):").
			Validate(func(s string) error {
				if branch.Slug(s) == "" {
					return errors.New("label is empty after slugging — use letters, digits, or hyphens")
				}

				return nil
			}).
			Value(label),
	)
}

// BaseBranchPicker lets the user pick the base branch for a new issue branch.
// defaultBase is pre-selected. branches is the full list of local git branch names.
func BaseBranchPicker(defaultBase string, branches []string, selected *string) *huh.Group {
	opts := make([]huh.Option[string], len(branches))
	for i, name := range branches {
		opts[i] = huh.NewOption(name, name)
	}
	*selected = defaultBase

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Base branch:").
			Options(opts...).
			Value(selected),
	)
}
