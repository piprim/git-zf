package issueflow

import (
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
)

func TestWorktreePath(t *testing.T) {
	t.Parallel()

	t.Run("defaults to .worktrees/<branch> in the repo root", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "", "myapp", "feat-123-login")
		want := "/home/user/code/myapp/.worktrees/feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("a relative worktreeDir is under the repo root, no repo prefix", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "wt", "myapp", "feat-123-login")
		want := "/home/user/code/myapp/wt/feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("a worktreeDir outside the repo keeps the <repo>-- prefix", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "/worktrees", "myapp", "feat-123-login")
		want := "/worktrees/myapp--feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("expands tilde in worktreeDir", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "~/worktrees", "myapp", "feat-123-login")
		// ~ expansion produces an absolute path that must not start with ~
		if got[:1] == "~" {
			t.Errorf("tilde was not expanded: %q", got)
		}
		if filepath.Base(got) != "myapp--feat-123-login" {
			t.Errorf("unexpected basename %q", filepath.Base(got))
		}
	})
}

func TestResolveParentSlug(t *testing.T) {
	t.Parallel()

	for base, want := range map[string]string{
		"7@feat@big-thing":    "7",
		"7@feat@big-thing@v2": "7",
		"main":                "",
		"7@review":            "",
	} {
		t.Run("base "+base, func(t *testing.T) {
			t.Parallel()

			if got := resolveParentSlug(base); got != want {
				t.Errorf("resolveParentSlug(%q) = %q, want %q", base, got, want)
			}
		})
	}
}

// TestBranchChain_fromLinkedWorktree guards what replaced the shared store: a
// client opened inside a linked worktree reads the chains of the repository.
func TestBranchChain_fromLinkedWorktree(t *testing.T) {
	t.Parallel()

	main, run := newChainRepo(t, "")
	run("branch", "7@feat@big", "main")
	branchtest.Seed(t, main, branch.Op{Branch: "7@feat@big", Title: "Big"}, branch.StatusInProgress)

	wtDir := filepath.Join(t.TempDir(), "repo--wt")
	run("worktree", "add", "-q", "-b", "wt-branch", wtDir, "main")

	wtClient, err := git.NewClientAt(nil, wtDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	t.Run("the worktree sees the branch tracked from the main tree", func(t *testing.T) {
		st, e, err := branch.Find(t.Context(), wtClient, "7@feat@big")
		if err != nil || st == nil || e == nil || st.Title != "Big" {
			t.Fatalf("Find = %+v, %+v, %v", st, e, err)
		}
	})
}
