package tui

import (
	"slices"

	"github.com/charmbracelet/huh"
)

// MenuOption is one entry of a MenuSelect form: a bold label, an optional
// dimmed description on the line below, and the value written when picked.
type MenuOption struct {
	Label string
	Desc  string
	Value string
}

// MenuSelect presents a list of actions, in the same style as
// BranchActionSelect / IssueActionSelect, and writes the picked Value into
// value. When *value does not match any option it is reset to the first
// option's Value so the cursor always starts on a real entry.
func MenuSelect(title string, opts []MenuOption, value *string) *huh.Group {
	if len(opts) > 0 && !slices.ContainsFunc(opts, func(o MenuOption) bool { return o.Value == *value }) {
		*value = opts[0].Value
	}

	huhOpts := make([]huh.Option[string], len(opts))
	for i, o := range opts {
		label := o.Label
		if o.Desc != "" {
			label = o.Label + "\n" + descStyle.Render(o.Desc)
		}

		huhOpts[i] = huh.NewOption(label, o.Value)
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title(title).
			Options(huhOpts...).
			Value(value),
	)
}
