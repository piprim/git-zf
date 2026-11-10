package tui

import "testing"

func TestKeyHint(t *testing.T) {
	for _, tc := range []struct {
		key   string
		label string
		want  string
	}{
		{"S", "status", "[S]tatus"},
		{"n", "new", "[n]ew"},
		{"C", "checkout", "[C]heckout"},
		{"c", "close", "[c]lose"},
		{"P", "prune-tracker", "[P]rune-tracker"},
		{"r", "sync", "[r]-Sync"},
		{"/", "filter", "[/]-Filter"},
		{"enter", "actions", "[enter]-Actions"},
		{"tab", "status", "[tab]-Status"},
		{"u", "pull request", "[u]-Pull request"},
	} {
		t.Run(tc.key+" "+tc.label, func(t *testing.T) {
			if got := keyHint(tc.key, tc.label); got != tc.want {
				t.Errorf("keyHint(%q, %q) = %q, want %q", tc.key, tc.label, got, tc.want)
			}
		})
	}

	t.Run("keyHints joins pairs by two spaces", func(t *testing.T) {
		if got := keyHints("S", "status", "/", "filter"); got != "[S]tatus  [/]-Filter" {
			t.Errorf("keyHints = %q, want %q", got, "[S]tatus  [/]-Filter")
		}
	})
}
