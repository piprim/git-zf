package issueflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
)

func TestResolveParentBranch(t *testing.T) {
	t.Parallel()

	t.Run("no parent → cfg base", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")
		branchtest.Seed(t, c, branch.Op{Branch: "X@feat@thing"}, branch.StatusInProgress)

		got, err := ResolveParentBranch(t.Context(), c, "X", "develop")
		if err != nil || got != "develop" {
			t.Fatalf("got %q, %v, want develop", got, err)
		}
	})

	t.Run("an untracked issue → cfg base", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")

		got, err := ResolveParentBranch(t.Context(), c, "X", "develop")
		if err != nil || got != "develop" {
			t.Fatalf("got %q, %v, want develop", got, err)
		}
	})

	t.Run("empty cfg base → DefaultBaseBranch", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")

		got, err := ResolveParentBranch(t.Context(), c, "X", "")
		if err != nil || got != "main" {
			t.Fatalf("got %q, %v, want main", got, err)
		}
	})

	t.Run("parent recorded on the chain → parent branch name", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")
		branchtest.Seed(t, c, branch.Op{Branch: "X@feat@big"}, branch.StatusInProgress)
		branchtest.Seed(t, c, branch.Op{Branch: "X.2@feat@part", Parent: "X"}, branch.StatusInProgress)

		got, err := ResolveParentBranch(t.Context(), c, "X.2", "main")
		if err != nil || got != "X@feat@big" {
			t.Fatalf("got %q, %v, want X@feat@big", got, err)
		}
	})

	t.Run("cross-clone: the chains fetched from the remote are enough", func(t *testing.T) {
		t.Parallel()

		origin := newBareOrigin(t)
		alice, _ := newChainRepo(t, origin)
		branchtest.Seed(t, alice, branch.Op{Branch: "X@feat@big"}, branch.StatusInProgress)
		branchtest.Seed(t, alice, branch.Op{Branch: "X.2@feat@part", Parent: "X"}, branch.StatusInProgress)
		if err := branch.Sync(t.Context(), alice); err != nil {
			t.Fatalf("alice Sync: %v", err)
		}

		bob, _ := newChainRepo(t, origin)
		if err := branch.Fetch(t.Context(), bob); err != nil {
			t.Fatalf("bob Fetch: %v", err)
		}

		got, err := ResolveParentBranch(t.Context(), bob, "X.2", "main")
		if err != nil || got != "X@feat@big" {
			t.Fatalf("got %q, %v, want X@feat@big", got, err)
		}
	})

	t.Run("a parent with no chain here → cfg base", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")
		branchtest.Seed(t, c, branch.Op{Branch: "X.2@feat@part", Parent: "X"}, branch.StatusInProgress)

		got, err := ResolveParentBranch(t.Context(), c, "X.2", "main")
		if err != nil || got != "main" {
			t.Fatalf("got %q, %v, want main", got, err)
		}
	})

	t.Run("a parent still in the old blob format is refused, not ignored", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")
		writeLegacyBlob(t, c, "X", `{"issue_slug":"X","branch_name":"X@feat@big"}`)
		branchtest.Seed(t, c, branch.Op{Branch: "X.2@feat@part", Parent: "X"}, branch.StatusInProgress)

		_, err := ResolveParentBranch(t.Context(), c, "X.2", "main")
		if !errors.Is(err, branch.ErrLegacyBranch) || !strings.Contains(err.Error(), "git zf issue track") {
			t.Fatalf("err = %v, want ErrLegacyBranch naming issue track", err)
		}
	})

	t.Run("an issue still in the old blob format has no parent → cfg base", func(t *testing.T) {
		t.Parallel()

		c, _ := newChainRepo(t, "")
		writeLegacyBlob(t, c, "X.2", `{"issue_slug":"X.2","parent_slug":"X"}`)

		got, err := ResolveParentBranch(t.Context(), c, "X.2", "main")
		if err != nil || got != "main" {
			t.Fatalf("got %q, %v, want main", got, err)
		}
	})
}
