package tui

import (
	"fmt"

	"github.com/charmbracelet/huh"
)

const (
	BranchActionNameList  = "branchList"
	BranchActionNameNew   = "branchNew"
	BranchActionNameMerge = "branchMerge"
	BranchActionNamePrune = "branchPrune"
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
