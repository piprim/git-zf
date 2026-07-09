package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// mustRunGit runs a git command in the given directory, failing the test on error.
func mustRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// seedPendingReview creates 77@review with one commit ahead of the feature
// branch and writes a local review ref with the given status.
func seedPendingReview(t *testing.T, rig *reviewE2ERig, status store.ReviewStatus) {
	t.Helper()
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	mustRunGit(t, rig.dir, "checkout", "-b", "77@review")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if _, err := rig.client.WriteReviewRef(t.Context(), "77", git.ReviewRef{
		Status: string(status), Round: 1, FeatureSHA: "unused", CreatedAt: "2026-07-08T00:00:00Z",
	}, ""); err != nil {
		t.Fatalf("write review ref: %v", err)
	}
}

func TestGuardCommit(t *testing.T) {
	t.Run("blocks with sync hint on changes_requested", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
		err := runReviewGuardCommit(t.Context(), rig.deps())
		if err == nil {
			t.Fatal("want guard error, got nil")
		}
		if !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync hint in %q", err.Error())
		}
		if !strings.Contains(err.Error(), "--no-verify") {
			t.Fatalf("want bypass hint in %q", err.Error())
		}
	})

	t.Run("passes during in_review", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		seedPendingReview(t, rig, store.ReviewStatusInReview)
		if err := runReviewGuardCommit(t.Context(), rig.deps()); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})

	t.Run("passes on the @review branch itself", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
		mustRunGit(t, rig.dir, "checkout", "77@review")
		if err := runReviewGuardCommit(t.Context(), rig.deps()); err != nil {
			t.Fatalf("want pass on review branch, got %v", err)
		}
	})

	t.Run("passes on an untracked branch", func(t *testing.T) {
		rig := newReviewE2ERig(t)
		mustRunGit(t, rig.dir, "checkout", "-b", "scratch")
		if err := runReviewGuardCommit(t.Context(), rig.deps()); err != nil {
			t.Fatalf("want pass, got %v", err)
		}
	})
}
