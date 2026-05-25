package tui

import (
	"errors"
	"fmt"

	btable "github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/store"
)

const (
	BranchActionNameList  = "branchList"
	BranchActionNameNew   = "branchNew"
	BranchActionNameMerge = "branchMerge"
	BranchActionNamePrune = "branchPrune"

	branchTableColWidthIssueID = 10
	branchTableColWidthTitle   = 28
	branchTableColWidthBranch  = 38
	branchTableColWidthType    = 8
	branchTableColWidthStatus  = 12
	branchTableColWidthCreated = 10
	branchTableHeight          = 20
	BranchTableHeaderColor     = lipgloss.Color("63")
)

// BranchActionSelect presents the list of available branch actions.
func BranchActionSelect(action *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Branch action:").
			Options(
				huh.NewOption("List\n"+descStyle.Render("List branches by status"), BranchActionNameList),
				huh.NewOption("New\n"+descStyle.Render("Create a new branch (manual input)"), BranchActionNameNew),
				huh.NewOption("Prune\n"+
					descStyle.Render("Remove DB records for deleted or merged branches"), BranchActionNamePrune),
				huh.NewOption("Merge\n"+descStyle.Render("Merge a branch"), BranchActionNameMerge),
			).
			Value(action),
	)
}

// BranchPruneConfirm asks whether to proceed with pruning nDeleted deleted and
// nMerged merged branch records.
func BranchPruneConfirm(nDeleted, nMerged int, confirmed *bool) *huh.Group {
	title := fmt.Sprintf(
		"Prune %d deleted + %d merged branch records. Proceed?",
		nDeleted, nMerged,
	)

	return huh.NewGroup(
		huh.NewConfirm().
			Title(title).
			Value(confirmed),
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

func BranchTableModel(rows []store.BranchRow) (tea.Model, error) {
	cols := []btable.Column{
		{Title: "Issue ID", Width: branchTableColWidthIssueID},
		{Title: "Title", Width: branchTableColWidthTitle},
		{Title: "Branch", Width: branchTableColWidthBranch},
		{Title: "Type", Width: branchTableColWidthType},
		{Title: "Status", Width: branchTableColWidthStatus},
		{Title: "Created", Width: branchTableColWidthCreated},
	}

	tableRows := make([]btable.Row, len(rows))
	for i := range rows {
		tableRows[i] = btable.Row{
			rows[i].IssueSlug,
			rows[i].Title,
			rows[i].BranchName,
			rows[i].Type,
			string(rows[i].Status),
			rows[i].CreatedAt.Format("2006-01-02"),
		}
	}

	t := btable.New(
		btable.WithColumns(cols),
		btable.WithRows(tableRows),
		btable.WithFocused(true),
		btable.WithHeight(branchTableHeight),
	)

	st := btable.DefaultStyles()
	st.Header = lipgloss.NewStyle().Bold(true).Foreground(BranchTableHeaderColor).Padding(0, 1)
	t.SetStyles(st)

	return &branchTableModel{table: t}, nil
}

// branchTableModel wraps bubbles/table as a minimal Bubble Tea program.
type branchTableModel struct {
	table btable.Model
}

func (*branchTableModel) Init() tea.Cmd { return nil }

func (m *branchTableModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

func (m *branchTableModel) View() string {
	return m.table.View() + "\n\nPress q to quit."
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
