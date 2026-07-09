package issueflow

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
)

type guardRig struct {
	dir    string
	client *git.Client
	store  *store.Store
	run    func(args ...string)
}

func newGuardRig(t *testing.T) *guardRig {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "T")
	run("config", "user.email", "t@t")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-m", "chore: init")
	run("checkout", "-b", "42@feat@title")
	if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("feat\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "feat.txt")
	run("commit", "-m", "feat: work")

	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: os.Stdout, Err: os.Stderr}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}
	s, err := store.Open(t.Context(), filepath.Join(dir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "42", Title: "title", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "42@feat@title", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return &guardRig{dir: dir, client: client, store: s, run: run}
}

// addReviewBranchWithCommit creates 42@review off the feature branch with one
// extra commit, then returns to the feature branch.
func (r *guardRig) addReviewBranchWithCommit(t *testing.T) {
	t.Helper()
	r.run("checkout", "-b", "42@review")
	if err := os.WriteFile(filepath.Join(r.dir, "review-fix.txt"), []byte("fix\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.run("add", "review-fix.txt")
	r.run("commit", "-m", "fix: reviewer nit")
	r.run("checkout", "42@feat@title")
}

func (r *guardRig) writeReviewRef(t *testing.T, status string) {
	t.Helper()
	if _, err := r.client.WriteReviewRef(t.Context(), "42", git.ReviewRef{
		Status: status, Round: 1, FeatureSHA: "unused", CreatedAt: "2026-07-08T00:00:00Z",
	}, ""); err != nil {
		t.Fatalf("write review ref: %v", err)
	}
}

func TestPendingReviewCommits(t *testing.T) {
	t.Run("trips on changes_requested with unincorporated commits", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if p == nil || p.Commits != 1 || p.EffectiveRef != "42@review" {
			t.Fatalf("want pending 1 commit on 42@review, got %+v", p)
		}
	})

	t.Run("trips on approved", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusApproved))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p == nil {
			t.Fatalf("want pending, got %+v err %v", p, err)
		}
	})

	t.Run("silent during in_review", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusInReview))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending, got %+v err %v", p, err)
		}
	})

	t.Run("silent without a review ref (stale branch after close)", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending, got %+v err %v", p, err)
		}
	})

	t.Run("silent when commits are contained", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		rig.run("merge", "--no-edit", "42@review")
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending after merge, got %+v err %v", p, err)
		}
	})

	t.Run("silent when no review branch exists anywhere", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil || p != nil {
			t.Fatalf("want nil pending, got %+v err %v", p, err)
		}
	})

	t.Run("prefers remote-tracking ref when local review branch is stale", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		// Simulate: reviewer pushed a second commit that only origin has.
		// The remote is never contacted — the remote-tracking ref is set by
		// hand with update-ref, exactly the state a past `git fetch` leaves.
		rig.run("remote", "add", "origin", rig.dir)
		rig.run("checkout", "42@review")
		if err := os.WriteFile(filepath.Join(rig.dir, "review-fix-2.txt"), []byte("fix2\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rig.run("add", "review-fix-2.txt")
		rig.run("commit", "-m", "fix: second reviewer nit")
		rig.run("update-ref", "refs/remotes/origin/42@review", "HEAD")
		rig.run("reset", "--hard", "HEAD~1") // local 42@review is now stale
		rig.run("checkout", "42@feat@title")

		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if p == nil || p.EffectiveRef != "origin/42@review" || p.Commits != 2 {
			t.Fatalf("want 2 commits via origin/42@review, got %+v", p)
		}
	})

	t.Run("local wins when it is ahead of the remote-tracking ref", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		// Remote-tracking ref exists but points one commit behind the local
		// branch — the reviewer's own machine before pushing.
		rig.run("remote", "add", "origin", rig.dir)
		rig.run("update-ref", "refs/remotes/origin/42@review", "42@review~1")

		p, err := PendingReviewCommits(t.Context(), rig.client, "42", "42@feat@title")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if p == nil || p.EffectiveRef != "42@review" || p.Commits != 1 {
			t.Fatalf("want 1 commit via local 42@review, got %+v", p)
		}
	})
}

func TestPendingReviewForHEAD(t *testing.T) {
	t.Run("pending on the checked-out tracked branch", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		p, branch, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p == nil || branch != "42@feat@title" {
			t.Fatalf("want pending on 42@feat@title, got %+v %q err %v", p, branch, err)
		}
	})

	t.Run("exempt on @review branch", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		rig.run("checkout", "42@review")
		p, _, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p != nil {
			t.Fatalf("want exempt on @review branch, got %+v err %v", p, err)
		}
	})

	t.Run("exempt while a merge is in progress", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.addReviewBranchWithCommit(t)
		rig.writeReviewRef(t, string(store.ReviewStatusChangesRequested))
		// Force a conflicted merge so MERGE_HEAD exists.
		if err := os.WriteFile(filepath.Join(rig.dir, "review-fix.txt"), []byte("mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		rig.run("add", "review-fix.txt")
		rig.run("commit", "-m", "feat: conflicting")
		_ = rig.client.MergeLeaveConflicts(t.Context(), "42@review", "42@feat@title")
		p, _, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p != nil {
			t.Fatalf("want exempt mid-merge, got %+v err %v", p, err)
		}
	})

	t.Run("exempt on an untracked branch", func(t *testing.T) {
		rig := newGuardRig(t)
		rig.run("checkout", "-b", "random-branch")
		p, _, err := PendingReviewForHEAD(t.Context(), rig.client, rig.store)
		if err != nil || p != nil {
			t.Fatalf("want exempt on untracked branch, got %+v err %v", p, err)
		}
	})
}
