// Package text cleans the strings that reach the terminal from outside the
// process: the issue, branch and review chains another clone pushed, and what
// a tracker reports.
package text

import (
	"strings"
	"unicode"
)

// Clean drops every control character but the newline and the tab, so that a
// description or a comment cannot carry an escape sequence (recolor the
// terminal, move the cursor, set the window title) or a carriage return that
// rewrites the line.
func Clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			return r
		}

		return -1
	}, s)
}

// Line is Clean for a one-line field (a title, a label, an author, a status):
// a newline or a tab becomes a space, so the field cannot forge a second line.
func Line(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}

		return r
	}, s)
}
