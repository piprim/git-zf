package tui

import "testing"

func TestMenuSelect(t *testing.T) {
	t.Parallel()

	t.Run("returns a group and pre-selects the first option", func(t *testing.T) {
		t.Parallel()

		selected := ""
		opts := []MenuOption{
			{Label: "Commit", Desc: "Record changes", Value: "commit"},
			{Label: "Issue", Desc: "Manage issues", Value: "issue"},
		}

		group := MenuSelect("git zf:", opts, &selected)
		if group == nil {
			t.Fatal("MenuSelect returned nil group")
		}
		if selected != "commit" {
			t.Errorf("selected = %q, want %q (first option's value)", selected, "commit")
		}
	})

	t.Run("keeps a pre-set value when it matches an option", func(t *testing.T) {
		t.Parallel()

		selected := "issue"
		opts := []MenuOption{
			{Label: "Commit", Value: "commit"},
			{Label: "Issue", Value: "issue"},
		}

		if MenuSelect("git zf:", opts, &selected) == nil {
			t.Fatal("MenuSelect returned nil group")
		}
		if selected != "issue" {
			t.Errorf("selected = %q, want %q", selected, "issue")
		}
	})
}
