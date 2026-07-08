package convert

import (
	"testing"

	"github.com/piprim/git-zf/tui"
)

// TestCommitOptionsFromTUI asserts every git.CommitOptions field is populated
// from its tui.CommitOption counterpart. A mixed boolean pattern makes a
// dropped or mis-wired field observable — the silent failure this mapper exists
// to prevent.
func TestCommitOptionsFromTUI(t *testing.T) {
	in := tui.CommitOption{
		// TUI-only fields that must not influence the result.
		Skip:    true,
		Authors: []string{"Ada <ada@example.com>"},
		// Mapped fields, mixed so a field swap is observable.
		All:              true,
		Amend:            false,
		NoVerify:         true,
		Signoff:          false,
		AllowEmpty:       true,
		IncludeUntracked: true,
		Author:           "Jane Doe <jane@example.com>",
	}

	got := CommitOptionsFromTUI(in)

	t.Run("maps All", func(t *testing.T) {
		if got.All != in.All {
			t.Errorf("All = %v, want %v", got.All, in.All)
		}
	})

	t.Run("maps Amend", func(t *testing.T) {
		if got.Amend != in.Amend {
			t.Errorf("Amend = %v, want %v", got.Amend, in.Amend)
		}
	})

	t.Run("maps NoVerify", func(t *testing.T) {
		if got.NoVerify != in.NoVerify {
			t.Errorf("NoVerify = %v, want %v", got.NoVerify, in.NoVerify)
		}
	})

	t.Run("maps Signoff", func(t *testing.T) {
		if got.Signoff != in.Signoff {
			t.Errorf("Signoff = %v, want %v", got.Signoff, in.Signoff)
		}
	})

	t.Run("maps AllowEmpty", func(t *testing.T) {
		if got.AllowEmpty != in.AllowEmpty {
			t.Errorf("AllowEmpty = %v, want %v", got.AllowEmpty, in.AllowEmpty)
		}
	})

	t.Run("maps Author", func(t *testing.T) {
		if got.Author != in.Author {
			t.Errorf("Author = %q, want %q", got.Author, in.Author)
		}
	})

	t.Run("maps IncludeUntracked", func(t *testing.T) {
		if got.IncludeUntracked != in.IncludeUntracked {
			t.Errorf("IncludeUntracked = %v, want %v", got.IncludeUntracked, in.IncludeUntracked)
		}
	})
}
