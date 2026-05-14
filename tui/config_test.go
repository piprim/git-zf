package tui

import "testing"

func TestConfigSectionPicker(t *testing.T) {
	t.Parallel()

	t.Run("returns non-nil group for standard keys", func(t *testing.T) {
		t.Parallel()

		keys := []string{"commit-types", "commit-message", "branch", "issue-tracker"}
		var selected []string
		if ConfigSectionPicker(keys, &selected) == nil {
			t.Fatal("ConfigSectionPicker returned nil group")
		}
	})

	t.Run("returns non-nil group for empty key list", func(t *testing.T) {
		t.Parallel()

		var selected []string
		if ConfigSectionPicker([]string{}, &selected) == nil {
			t.Fatal("ConfigSectionPicker with empty keys returned nil group")
		}
	})
}
