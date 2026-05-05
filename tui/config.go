package tui

import "github.com/charmbracelet/huh"

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
