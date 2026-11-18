package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	branchpkg "github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// homeRig is a repository on disk with one commit on main.
type homeRig struct {
	t   *testing.T
	dir string
	err *bytes.Buffer
	c   *git.Client
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}

	return strings.TrimSpace(string(out))
}

// clientAt opens dir with an Err buffer of its own.
func clientAt(t *testing.T, dir string) (*git.Client, *bytes.Buffer) {
	t.Helper()

	var errBuf bytes.Buffer
	c, err := git.NewClientAt(&pkg.IO{Out: &bytes.Buffer{}, Err: &errBuf}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, &errBuf
}

// newHomeRig makes a repository; origin, when not empty, is its remote.
func newHomeRig(t *testing.T, origin string) *homeRig {
	t.Helper()

	dir := t.TempDir()
	gitIn(t, dir, "init", "-q", "-b", "main")
	gitIn(t, dir, "config", "user.name", "alice")
	gitIn(t, dir, "config", "user.email", "alice@test.com")
	gitIn(t, dir, "config", "commit.gpgsign", "false")
	r := &homeRig{t: t, dir: dir}
	r.write("base.txt", "base\n")
	gitIn(t, dir, "add", "base.txt")
	gitIn(t, dir, "commit", "-q", "-m", "chore: init")
	if origin != "" {
		gitIn(t, dir, "remote", "add", "origin", origin)
	}
	r.c, r.err = clientAt(t, dir)

	return r
}

func (r *homeRig) write(name, content string) {
	r.t.Helper()

	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatalf("write %s: %v", name, err)
	}
}

func (r *homeRig) gather() (tui.HomeData, map[string]string) {
	return gatherHome(r.t.Context(), r.c, "git-zf")
}

func newHomeOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	gitIn(t, t.TempDir(), "init", "-q", "--bare", dir)

	return dir
}

func TestGatherHome(t *testing.T) {
	t.Parallel()

	t.Run("clean tree on a plain branch", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		d, hints := r.gather()
		if d.Prog != "git-zf" || d.Branch != "main" || d.Issue != "" || len(d.Entries) != 0 || d.Upstream != "" {
			t.Errorf("data = %+v", d)
		}
		if len(hints) != 0 || r.err.Len() != 0 {
			t.Errorf("hints = %v, stderr = %q, want none", hints, r.err.String())
		}
	})

	t.Run("staged and modified again", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		r.write("base.txt", "staged\n")
		gitIn(t, r.dir, "add", "base.txt")
		r.write("base.txt", "again\n")
		d, hints := r.gather()
		if got := tui.CountStatus(d.Entries); got != (tui.StatusCounts{Staged: 1, Modified: 1}) {
			t.Errorf("counts = %+v", got)
		}
		if hints["commit"] != "1 staged file" {
			t.Errorf("commit hint = %q", hints["commit"])
		}
	})

	t.Run("modified files only", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		r.write("base.txt", "changed\n")
		r.write("new.txt", "new\n")
		if _, hints := r.gather(); hints["commit"] != "1 modified file" {
			t.Errorf("commit hint = %q", hints["commit"])
		}
	})

	t.Run("an unmerged file", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		gitIn(t, r.dir, "checkout", "-q", "-b", "other")
		r.write("base.txt", "other\n")
		gitIn(t, r.dir, "commit", "-q", "-am", "other")
		gitIn(t, r.dir, "checkout", "-q", "main")
		r.write("base.txt", "main\n")
		gitIn(t, r.dir, "commit", "-q", "-am", "main")
		_ = exec.CommandContext(t.Context(), "git", "-C", r.dir, "merge", "other").Run() // conflicts
		d, _ := r.gather()
		if got := tui.CountStatus(d.Entries).Unmerged; got != 1 {
			t.Errorf("unmerged = %d, want 1 (%+v)", got, d.Entries)
		}
	})

	t.Run("tracked branch with an issue record", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		ctx := t.Context()
		rec, err := issuepkg.Create(ctx, r.c, issuepkg.NewIssue{Title: "Login form", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		name := rec.DisplayID() + "@feat@login"
		branchtest.Seed(t, r.c, branchpkg.Op{Branch: name, Title: "Frozen title", IssueID: rec.ID}, branchpkg.StatusInProgress)
		gitIn(t, r.dir, "checkout", "-q", "-b", name)

		t.Run("shows the record's ID and title", func(t *testing.T) {
			d, hints := r.gather()
			if d.Branch != name || d.Issue != rec.DisplayID()+" Login form" {
				t.Errorf("branch = %q issue = %q", d.Branch, d.Issue)
			}
			if hints["issue"] != "1 open" || hints["branch"] != "1 in progress" {
				t.Errorf("hints = %v", hints)
			}
		})

		t.Run("the record's title follows an edit", func(t *testing.T) {
			if err := issuepkg.Append(ctx, r.c, rec.ID, &issuepkg.Op{Type: issuepkg.OpSetTitle, Value: "Edited"}); err != nil {
				t.Fatalf("Append: %v", err)
			}
			if d, _ := r.gather(); d.Issue != rec.DisplayID()+" Edited" {
				t.Errorf("issue = %q", d.Issue)
			}
		})
	})

	t.Run("tracked branch without a record", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		branchtest.Seed(t, r.c, branchpkg.Op{Branch: "77@feat@x", Title: "From the tracker"}, branchpkg.StatusInProgress)
		gitIn(t, r.dir, "checkout", "-q", "-b", "77@feat@x")
		d, hints := r.gather()
		if d.Issue != "77 From the tracker" {
			t.Errorf("issue = %q", d.Issue)
		}
		if _, ok := hints["issue"]; ok {
			t.Errorf("issue hint = %q, want none without a record", hints["issue"])
		}
	})

	t.Run("a plain branch named like an issue shows no issue", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		branchtest.Seed(t, r.c, branchpkg.Op{Branch: "77@feat@x", Title: "Not this branch"}, branchpkg.StatusInProgress)
		reviewtest.Seed(t, r.c, "77", reviewpkg.StatusInReview, 1, gitIn(t, r.dir, "rev-parse", "HEAD"))
		gitIn(t, r.dir, "checkout", "-q", "-b", "77")
		if d, _ := r.gather(); d.Issue != "" || d.Review != "" {
			t.Errorf("issue = %q review = %q, want none on an untracked branch", d.Issue, d.Review)
		}
	})

	t.Run("a review branch", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		branchtest.Seed(t, r.c, branchpkg.Op{Branch: "77@feat@x", Title: "Reviewed"}, branchpkg.StatusInProgress)
		sha := gitIn(t, r.dir, "rev-parse", "HEAD")
		reviewtest.Seed(t, r.c, "77", reviewpkg.StatusInReview, 1, sha)
		gitIn(t, r.dir, "checkout", "-q", "-b", "77@review")

		t.Run("shows the issue and the review of its slug", func(t *testing.T) {
			d, hints := r.gather()
			if d.Issue != "77 Reviewed" || d.Review != "in review" || hints["review"] != "1 in review" {
				t.Errorf("issue = %q review = %q hints = %v", d.Issue, d.Review, hints)
			}
		})

		t.Run("a closed review is ignored", func(t *testing.T) {
			if err := reviewpkg.Append(t.Context(), r.c, "77", &reviewpkg.Op{Type: reviewpkg.OpClose}, false); err != nil {
				t.Fatalf("Append: %v", err)
			}
			d, hints := r.gather()
			if _, ok := hints["review"]; d.Review != "" || ok {
				t.Errorf("review = %q hints = %v, want none", d.Review, hints)
			}
		})
	})

	t.Run("changes requested", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		branchtest.Seed(t, r.c, branchpkg.Op{Branch: "77@feat@x"}, branchpkg.StatusInProgress)
		reviewtest.Seed(t, r.c, "77", reviewpkg.StatusChangesRequested, 1, gitIn(t, r.dir, "rev-parse", "HEAD"))
		gitIn(t, r.dir, "checkout", "-q", "-b", "77@feat@x")
		d, hints := r.gather()
		if _, ok := hints["review"]; d.Review != "changes requested" || ok {
			t.Errorf("review = %q hints = %v", d.Review, hints)
		}
	})

	t.Run("upstream ahead and behind", func(t *testing.T) {
		t.Parallel()

		origin := newHomeOrigin(t)
		r := newHomeRig(t, origin)
		gitIn(t, r.dir, "push", "-q", "-u", "origin", "main")
		other := t.TempDir()
		gitIn(t, other, "clone", "-q", "-b", "main", origin, ".")
		gitIn(t, other, "-c", "user.name=bob", "-c", "user.email=bob@test.com", "commit", "-q", "--allow-empty", "-m", "bob")
		gitIn(t, other, "push", "-q", "origin", "HEAD:main")
		gitIn(t, r.dir, "commit", "-q", "--allow-empty", "-m", "alice")
		gitIn(t, r.dir, "fetch", "-q", "origin")
		d, _ := r.gather()
		if d.Upstream != "origin/main" || !d.HasAB || d.Ahead != 1 || d.Behind != 1 {
			t.Errorf("upstream = %q hasAB = %v ahead = %d behind = %d", d.Upstream, d.HasAB, d.Ahead, d.Behind)
		}
	})

	t.Run("detached HEAD", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		gitIn(t, r.dir, "checkout", "-q", "--detach")
		if d, _ := r.gather(); !strings.HasPrefix(d.Branch, "detached at ") || len(d.Branch) != 19 {
			t.Errorf("branch = %q", d.Branch)
		}
	})

	t.Run("no commit yet", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		gitIn(t, dir, "init", "-q", "-b", "trunk")
		c, errBuf := clientAt(t, dir)
		d, hints := gatherHome(t.Context(), c, "git-zf")
		if d.Branch != "trunk" || len(hints) != 0 || errBuf.Len() != 0 {
			t.Errorf("branch = %q hints = %v stderr = %q", d.Branch, hints, errBuf.String())
		}
	})

	t.Run("a linked worktree", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		wt := filepath.Join(t.TempDir(), "wt")
		gitIn(t, r.dir, "worktree", "add", "-q", "-b", "side", wt)
		if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		c, _ := clientAt(t, wt)
		d, _ := gatherHome(t.Context(), c, "git-zf")
		if d.Branch != "side" || tui.CountStatus(d.Entries).Untracked != 1 {
			t.Errorf("worktree: branch = %q entries = %+v", d.Branch, d.Entries)
		}
		if main, _ := r.gather(); main.Branch != "main" || len(main.Entries) != 0 {
			t.Errorf("main tree: branch = %q entries = %+v", main.Branch, main.Entries)
		}
	})
}

func TestHomeEntries(t *testing.T) {
	t.Parallel()

	t.Run("keys, labels and hints", func(t *testing.T) {
		t.Parallel()

		subs := []*cobra.Command{{Use: "commit"}, {Use: "log"}}
		got := homeEntries(subs, map[string]string{"commit": "2 staged files"})
		want := []tui.HomeEntry{
			{Key: "c", Name: "commit", Label: "Commit", Hint: "2 staged files"},
			{Key: "l", Name: "log", Label: "Log"},
		}
		if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
			t.Errorf("entries = %+v, want %+v", got, want)
		}
	})
}

func TestRefreshHome(t *testing.T) {
	t.Parallel()

	t.Run("fetches what the remote has", func(t *testing.T) {
		t.Parallel()

		origin := newHomeOrigin(t)
		alice, bob := newHomeRig(t, origin), newHomeRig(t, origin)
		ctx := t.Context()
		rec, err := issuepkg.Create(ctx, alice.c, issuepkg.NewIssue{Title: "Shared", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := issuepkg.Push(ctx, alice.c, rec.ID); err != nil {
			t.Fatalf("Push: %v", err)
		}

		refreshHome(ctx, bob.c)
		if _, hints := bob.gather(); hints["issue"] != "1 open" || bob.err.Len() != 0 {
			t.Errorf("hints = %v stderr = %q", hints, bob.err.String())
		}
	})

	t.Run("a remote that is down, then back", func(t *testing.T) {
		t.Parallel()

		origin := newHomeOrigin(t)
		alice, bob := newHomeRig(t, origin), newHomeRig(t, origin)
		ctx := t.Context()
		rec, err := issuepkg.Create(ctx, alice.c, issuepkg.NewIssue{Title: "Shared", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := issuepkg.Push(ctx, alice.c, rec.ID); err != nil {
			t.Fatalf("Push: %v", err)
		}
		gitIn(t, bob.dir, "remote", "set-url", "origin", "/nonexistent/origin.git")

		t.Run("down gives one warning", func(t *testing.T) {
			c, errBuf := clientAt(t, bob.dir)
			refreshHome(ctx, c)
			got := errBuf.String()
			if strings.Contains(got, "not retried") {
				t.Errorf("stderr = %q, want the first failure, not the latch's", got)
			}
			if strings.Count(got, "\n") != 1 || !strings.HasPrefix(got, "warning: could not fetch, showing local data:") {
				t.Errorf("stderr = %q, want one warning line", got)
			}
		})

		t.Run("the next refresh reaches a remote that is back", func(t *testing.T) {
			gitIn(t, bob.dir, "remote", "set-url", "origin", origin)
			c, errBuf := clientAt(t, bob.dir)
			refreshHome(ctx, c)
			if _, hints := gatherHome(ctx, c, "git-zf"); hints["issue"] != "1 open" || errBuf.Len() != 0 {
				t.Errorf("hints = %v stderr = %q", hints, errBuf.String())
			}
		})
	})

	t.Run("a remote that cannot be chosen is a warning", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		gitIn(t, r.dir, "remote", "add", "one", newHomeOrigin(t))
		gitIn(t, r.dir, "remote", "add", "two", newHomeOrigin(t))
		c, errBuf := clientAt(t, r.dir)
		refreshHome(t.Context(), c)
		got := errBuf.String()
		if strings.Count(got, "\n") != 1 || !strings.HasPrefix(got, "warning: could not fetch, showing local data:") {
			t.Errorf("stderr = %q, want one warning line", got)
		}
	})

	t.Run("no remote is a no-op", func(t *testing.T) {
		t.Parallel()

		r := newHomeRig(t, "")
		refreshHome(t.Context(), r.c)
		if r.err.Len() != 0 {
			t.Errorf("stderr = %q, want nothing", r.err.String())
		}
	})
}
