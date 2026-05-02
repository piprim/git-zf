package tui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tracker"
)

const (
	IssueActionNameStart = "issueStart"
	IssueActionNameList  = "issueList"
	IssueActionNameClose = "issueClose"
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
