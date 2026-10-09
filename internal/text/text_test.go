package text

import "testing"

func TestClean(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain text is unchanged", "Login fails", "Login fails"},
		{"newline and tab are kept", "a\n\tb", "a\n\tb"},
		{"ANSI escape is dropped", "ok\x1b[31m FAIL\x1b[0m", "ok[31m FAIL[0m"},
		{"carriage return and bell are dropped", "a\rb\a", "ab"},
		{"C1 control is dropped", "a\u009bb", "ab"},
		{"non-ASCII text is kept", "é→✓", "é→✓"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Clean(tc.in); got != tc.want {
				t.Errorf("Clean(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLine(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"plain text is unchanged", "Login fails", "Login fails"},
		{"newline becomes a space", "Created issue\nClosed issue", "Created issue Closed issue"},
		{"tab becomes a space", "a\tb", "a b"},
		{"escape is dropped", "\x1b]0;owned\x07title", "]0;ownedtitle"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Line(tc.in); got != tc.want {
				t.Errorf("Line(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
