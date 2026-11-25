package review

import (
	"os"
	"path/filepath"
	"testing"

	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
)

// The hooks of an older `git zf init` still call the guard commands: they
// must let everything through, whatever the review says.
func TestReviewGuard_IsANoOp(t *testing.T) {
	rig := newReviewE2ERig(t)
	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusInReview, 1, "unused")
	t.Chdir(rig.dir)

	cmd := New(rig.cfg).getGuardCmd()
	cmd.SetArgs([]string{"77@feat@my-feature"})

	t.Run("a branch in review is let through", func(t *testing.T) {
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Errorf("guard = %v, want nil", err)
		}
	})
}

func TestReviewGuardCommit_IsANoOp(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, reviewpkg.StatusChangesRequested)
	t.Chdir(rig.dir)

	cmd := New(rig.cfg).getGuardCommitCmd()
	cmd.SetArgs(nil)

	t.Run("pending reviewer commits are let through", func(t *testing.T) {
		if err := cmd.ExecuteContext(t.Context()); err != nil {
			t.Errorf("guard-commit = %v, want nil", err)
		}
	})
}

// seedPendingReview creates 77@review with one commit ahead of the feature
// branch and seeds a local review chain with the given status.
func seedPendingReview(t *testing.T, rig *reviewE2ERig, status string) {
	t.Helper()
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	mustRunGit(t, rig.dir, "checkout", "-b", "77@review")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "fix: reviewer nit")
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	reviewtest.Seed(t, rig.client, "77", status, 1, "unused")
}
