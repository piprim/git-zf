package tui

import (
	"errors"
	"slices"

	"github.com/charmbracelet/huh"
)

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

// IssueLabelPicker is the `issue label` form when the tracker's labels are
// known: a multi-select over choices, current preselected, plus a line for
// names not in the list.
func IssueLabelPicker(choices, current []string, selected *[]string, extra *string) *huh.Group {
	opts := make([]huh.Option[string], len(choices))
	for i, name := range choices {
		opts[i] = huh.NewOption(name, name).Selected(slices.Contains(current, name))
	}

	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Labels:").
			Description("space/x toggles, enter confirms").
			Options(opts...).
			Value(selected),
		huh.NewInput().
			Title("New labels (space separated, optional):").
			Value(extra),
	)
}

// IssueLabelStatusPicker is the `issue label` form on a tracker whose one label
// is the status: a single pick.
func IssueLabelStatusPicker(choices []string, current string, picked *string) *huh.Group {
	opts := make([]huh.Option[string], len(choices))
	for i, name := range choices {
		opts[i] = huh.NewOption(name, name).Selected(name == current)
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Status:").
			Options(opts...).
			Value(picked),
	)
}
