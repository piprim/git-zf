package commit

import (
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestIssueTitleFromStore(t *testing.T) {
	// Not parallel: store.Open on disk; each subtest builds its own dir.

	newStore := func(t *testing.T) *store.Store {
		t.Helper()

		s, err := store.Open(t.Context(), t.TempDir())
		if err != nil {
			t.Fatalf("store.Open: %v", err)
		}
		t.Cleanup(func() { _ = s.Close() })

		return s
	}

	t.Run("returns the stored title for a known slug", func(t *testing.T) {
		s := newStore(t)
		if err := s.InsertIssueWithBranch(t.Context(),
			&store.Issue{IDSlug: "ABC-1", Title: "Add OAuth login", StatusID: store.StatusIDInProgress},
			&store.Branch{Name: "ABC-1@feat@add-oauth-login", Type: "feat", StatusID: store.StatusIDInProgress},
		); err != nil {
			t.Fatalf("seed: %v", err)
		}

		if got := issueTitleFromStore(t.Context(), s, "ABC-1"); got != "Add OAuth login" {
			t.Errorf("issueTitleFromStore(ABC-1) = %q, want %q", got, "Add OAuth login")
		}
	})

	t.Run("returns empty for a slug with no row", func(t *testing.T) {
		s := newStore(t)
		if got := issueTitleFromStore(t.Context(), s, "NOPE-9"); got != "" {
			t.Errorf("issueTitleFromStore(NOPE-9) = %q, want empty", got)
		}
	})

	t.Run("returns empty for an empty slug", func(t *testing.T) {
		s := newStore(t)
		if got := issueTitleFromStore(t.Context(), s, ""); got != "" {
			t.Errorf(`issueTitleFromStore("") = %q, want empty`, got)
		}
	})

	t.Run("returns empty when the lookup fails", func(t *testing.T) {
		s := newStore(t)
		_ = s.Close() // force ListBranchesByIssueSlugs to return an error

		if got := issueTitleFromStore(t.Context(), s, "ABC-1"); got != "" {
			t.Errorf("issueTitleFromStore on closed store = %q, want empty", got)
		}
	})
}
