package tui

import (
	"errors"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/issue"
)

// IssueRecordNew is the value IssueRecordPicker stores when the user picks
// the "New issue…" entry instead of a record.
const IssueRecordNew = ""

const issueRecordPickerHeight = 10

func requiredText(s string) error {
	if s == "" {
		return errors.New("required")
	}

	return nil
}

// branchTypeOptions builds the select options for the branch type.
func branchTypeOptions(allowed []string) []huh.Option[string] {
	if len(allowed) == 0 {
		return []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	opts := make([]huh.Option[string], 0, len(allowed))
	for _, a := range allowed {
		opts = append(opts, huh.NewOption(a, a))
	}

	return opts
}

// IssueNewForm is the form of `issue new`. labels is one comma-separated line.
func IssueNewForm(title, branchType, description, labels *string, allowedBranchTypes []string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(requiredText).
			Value(title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(branchTypeOptions(allowedBranchTypes)...).
			Value(branchType),
		huh.NewText().
			Title("Description:").
			Value(description),
		huh.NewInput().
			Title("Labels (comma-separated, optional):").
			Value(labels),
	)
}

// IssueEditForm is the form of `issue edit`: title and description come
// prefilled with the issue's current values.
func IssueEditForm(title, description *string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Title:").
			Validate(requiredText).
			Value(title),
		huh.NewText().
			Title("Description:").
			Value(description),
	)
}

// IssueRecordPicker lists repo issues as "[short-id] title". picked receives
// the full ID of the chosen record. With offerNew, a first "New issue…" entry
// stores IssueRecordNew instead.
func IssueRecordPicker(records []issue.Record, picked *string, offerNew bool) *huh.Group {
	opts := make([]huh.Option[string], 0, len(records)+1)
	if offerNew {
		opts = append(opts, huh.NewOption("New issue…", IssueRecordNew))
	}

	for i := range records {
		opts = append(opts, huh.NewOption("["+records[i].DisplayID()+"] "+records[i].Title, records[i].ID))
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Pick an issue:").
			Options(opts...).
			Value(picked).
			Height(issueRecordPickerHeight),
	)
}

// IssueCommentInput is the multiline form of `issue comment`.
func IssueCommentInput(body *string) *huh.Group {
	return huh.NewGroup(
		huh.NewText().
			Title("Comment:").
			Validate(requiredText).
			Value(body),
	)
}

// IssueLabelInput asks for label changes as "+add -remove" tokens.
func IssueLabelInput(changes *string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Label changes:").
			Placeholder("+bug -wontfix").
			Validate(requiredText).
			Value(changes),
	)
}
