package tui

import (
	"errors"

	"github.com/charmbracelet/huh"
)

// ConfigDestPicker presents a select form for choosing where to write the config file.
// opts is built by the caller based on which paths already exist.
func ConfigDestPicker(opts []huh.Option[string], dest *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Write config to:").
			Options(opts...).
			Value(dest),
	)
}

// ConfigSectionPicker returns a huh.Group with a MultiSelect that lets the user
// choose which config sections to write to the repo-local TOML file.
// keys is the ordered list of section names from the config map.
// selected is the pointer where chosen keys are written after the form runs.
func ConfigSectionPicker(keys []string, selected *[]string) *huh.Group {
	opts := make([]huh.Option[string], len(keys))
	for i, k := range keys {
		opts[i] = huh.NewOption(k, k)
	}

	multiselect := huh.NewMultiSelect[string]().
		Title("Which sections do you want to include in the repo config?").
		Description("Use space/x to toggle, enter to confirm.").
		Options(opts...).
		Validate(func(v []string) error {
			if len(v) == 0 {
				return errors.New("select at least one section (space/x to toggle)")
			}

			return nil
		}).
		Value(selected)

	return huh.NewGroup(multiselect)
}
