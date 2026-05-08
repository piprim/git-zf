package tui

import "testing"

func TestConfigSectionPicker_returnsGroup(t *testing.T) {
	t.Parallel()

	keys := []string{"commit-types", "commit-message", "branch", "issue-tracker"}
	var selected []string
	group := ConfigSectionPicker(keys, &selected)
	if group == nil {
		t.Fatal("ConfigSectionPicker returned nil group")
	}
}

func TestConfigSectionPicker_emptyKeys(t *testing.T) {
	t.Parallel()

	var selected []string
	group := ConfigSectionPicker([]string{}, &selected)
	if group == nil {
		t.Fatal("ConfigSectionPicker with empty keys returned nil group")
	}
}
