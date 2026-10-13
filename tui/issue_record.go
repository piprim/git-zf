package tui

import (
	"errors"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/issue"
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

// IssueNewForm is the form of `issue new`. labels are the fields of
// IssueLabelFields, shown last.
func IssueNewForm(title, branchType, description *string, allowedBranchTypes []string, labels ...huh.Field) *huh.Group {
	fields := []huh.Field{
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
	}

	return huh.NewGroup(append(fields, labels...)...)
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

// IssueLabelFields are the label fields shared by `issue new` and `issue
// label`, and labels reads what they hold once the form ran. choices are the
// labels the tracker knows (nil: unknown); current are the issue's. single
// means the one label is the status: a single pick among choices. Otherwise a
// multi-select over choices and current (none: no list) plus a line for new
// names.
func IssueLabelFields(choices, current []string, single bool) (fields []huh.Field, labels func() []string) {
	if single {
		picked := ""
		if len(current) > 0 {
			picked = current[0]
		}
		opts := make([]huh.Option[string], len(choices))
		for i, name := range choices {
			opts[i] = huh.NewOption(name, name).Selected(name == picked)
		}

		return []huh.Field{
			huh.NewSelect[string]().
				Title("Status:").
				Options(opts...).
				Value(&picked),
		}, func() []string {
			return []string{picked}
		}
	}

	known := issue.CleanLabels(append(slices.Clone(choices), current...))
	selected, extra := slices.Clone(current), ""
	if len(known) > 0 {
		opts := make([]huh.Option[string], len(known))
		for i, name := range known {
			opts[i] = huh.NewOption(name, name).Selected(slices.Contains(current, name))
		}
		fields = append(fields, huh.NewMultiSelect[string]().
			Title("Labels:").
			Description("space/x toggles, enter confirms").
			Options(opts...).
			Value(&selected))
	}
	fields = append(fields, huh.NewInput().
		Title("New labels (space separated, optional):").
		Value(&extra))

	return fields, func() []string {
		return issue.CleanLabels(append(selected, strings.Fields(extra)...))
	}
}
