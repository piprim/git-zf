package review

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"

	reviewpkg "github.com/piprim/git-zf/review"
)

// stateOf returns the review of issue 77 as the reviewer's clone has it.
func (r *startRig) stateOf(t *testing.T) *reviewpkg.State {
	t.Helper()

	st, err := reviewpkg.Load(t.Context(), r.reviewer.client, "77")
	if err != nil || st == nil {
		t.Fatalf("Load = %v, %v", st, err)
	}

	return st
}

// startedRig is a startRig whose reviewer ran review start.
func startedRig(t *testing.T) *startRig {
	t.Helper()

	rig := newStartRig(t, nil)
	if err := rig.start(t); err != nil {
		t.Fatalf("review start: %v", err)
	}
	rig.stdout.Reset()

	return rig
}

func TestReviewApprove_RecordsFeatureTip(t *testing.T) {
	t.Parallel()

	t.Run("with a reviewer commit the approval covers the branch tip", func(t *testing.T) {
		t.Parallel()

		rig := startedRig(t)
		commitFile(t, rig.dir, "nit.txt")
		tip := tipOf(t, rig.dir, startBranch)
		if err := runReviewApprove(t.Context(), rig.reviewer, "77"); err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
		st := rig.stateOf(t)
		if len(st.Approvals) != 1 || st.Approvals[0].ApprovedSHA != tip || !st.Approvals[0].HasCommits {
			t.Errorf("approvals = %+v, tip = %s", st.Approvals, tip)
		}
	})

	t.Run("without one it covers the submitted commit", func(t *testing.T) {
		t.Parallel()

		rig := startedRig(t)
		if err := runReviewApprove(t.Context(), rig.reviewer, "77"); err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
		st := rig.stateOf(t)
		if len(st.Approvals) != 1 || st.Approvals[0].ApprovedSHA != st.FeatureSHA || st.Approvals[0].HasCommits {
			t.Errorf("approvals = %+v, feature_sha = %s", st.Approvals, st.FeatureSHA)
		}
	})
}

// assertApproveRefused runs approve and checks that it refuses with a message
// holding want and writes no op.
func assertApproveRefused(t *testing.T, rig *startRig, want string) {
	t.Helper()

	err := runReviewApprove(t.Context(), rig.reviewer, "77")

	t.Run("approve refuses", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want one holding %q", err, want)
		}
	})
	t.Run("no approve op is written", func(t *testing.T) {
		if st := rig.stateOf(t); st.Status != reviewpkg.StatusInReview || len(st.Approvals) != 0 {
			t.Errorf("status = %s, approvals = %+v", st.Status, st.Approvals)
		}
	})
}

func TestReviewApprove_MissingLocalBranchRefuses(t *testing.T) {
	t.Parallel()

	assertApproveRefused(t, newStartRig(t, nil), "git zf review start")
}

func TestReviewApprove_StaleLocalBranchRefuses(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	rig.staleCopy(t)
	assertApproveRefused(t, rig, "git zf review start")
}

func TestReviewApprove_RemoteAheadRefuses(t *testing.T) {
	t.Parallel()

	// The developer pushes after the reviewer's start; the reviewer does not
	// fetch: approve does.
	rig := startedRig(t)
	commitFile(t, rig.dev.dir, "more.txt")
	gitIn(t, rig.dev.dir, "push", "-q", "origin", startBranch)
	assertApproveRefused(t, rig, "origin/"+startBranch)
}

func TestReviewApprove_NoRemote(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)
	err := runReviewApprove(t.Context(), rig.deps(), "77")

	t.Run("approve succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
	})
	t.Run("the approval covers the submitted commit", func(t *testing.T) {
		st, lErr := reviewpkg.Load(t.Context(), rig.client, "77")
		if lErr != nil || st == nil || len(st.Approvals) != 1 || st.Approvals[0].ApprovedSHA != st.FeatureSHA {
			t.Errorf("Load = %+v, %v", st, lErr)
		}
	})
}

func TestReviewApprove_BranchInAnotherWorktree(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, rig.dir, "worktree", "add", "-q", wt, "-b", startBranch, "origin/"+startBranch)
	commitFile(t, wt, "nit.txt")
	tip := tipOf(t, wt, "HEAD")
	err := runReviewApprove(t.Context(), rig.reviewer, "77")

	t.Run("approve succeeds from the main tree", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewApprove: %v", err)
		}
	})
	t.Run("the approval covers the tip of the branch in the worktree", func(t *testing.T) {
		if st := rig.stateOf(t); len(st.Approvals) != 1 || st.Approvals[0].ApprovedSHA != tip {
			t.Errorf("approvals = %+v, tip = %s", st.Approvals, tip)
		}
	})
}

func TestReviewReject_WithReviewerCommits(t *testing.T) {
	t.Parallel()

	rig := startedRig(t)
	commitFile(t, rig.dir, "nit.txt")
	_, err := runReviewReject(t.Context(), rig.reviewer, "77", "")

	t.Run("reject succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewReject: %v", err)
		}
	})
	t.Run("has_commits is recorded", func(t *testing.T) {
		if st := rig.stateOf(t); st.Status != reviewpkg.StatusChangesRequested || !st.HasCommits {
			t.Errorf("status = %s, has_commits = %v", st.Status, st.HasCommits)
		}
	})
	t.Run("the message shows how to see the reviewer's commits", func(t *testing.T) {
		want := "git log " + rig.stateOf(t).FeatureSHA[:7] + ".." + startBranch
		if !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, rig.stdout)
		}
	})
}

func TestReviewReject_WithoutCommits(t *testing.T) {
	t.Parallel()

	rig := startedRig(t)
	if _, err := runReviewReject(t.Context(), rig.reviewer, "77", ""); err != nil {
		t.Fatalf("runReviewReject: %v", err)
	}

	t.Run("has_commits is false", func(t *testing.T) {
		if st := rig.stateOf(t); st.Status != reviewpkg.StatusChangesRequested || st.HasCommits {
			t.Errorf("status = %s, has_commits = %v", st.Status, st.HasCommits)
		}
	})
}

func TestReviewReject_StaleLocalBranch(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t, nil)
	rig.staleCopy(t)
	asked := 0
	deps := rig.reviewer
	deps.cfg.Push.Propose = true
	deps.pushConfirm = func(context.Context, string) (bool, error) {
		asked++

		return true, nil
	}
	_, err := runReviewReject(t.Context(), deps, "77", "")

	t.Run("reject succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runReviewReject: %v", err)
		}
	})
	t.Run("has_commits is false", func(t *testing.T) {
		if st := rig.stateOf(t); st.Status != reviewpkg.StatusChangesRequested || st.HasCommits {
			t.Errorf("status = %s, has_commits = %v", st.Status, st.HasCommits)
		}
	})
	t.Run("no push is proposed", func(t *testing.T) {
		if asked != 0 {
			t.Errorf("push confirm asked %d time(s)", asked)
		}
	})
}

func TestReviewBranches_RowCarriesReviewedBranch(t *testing.T) {
	t.Parallel()

	t.Run("a request that names its branch", func(t *testing.T) {
		t.Parallel()

		rig := newReviewE2ERig(t)
		if err := runReviewRequest(t.Context(), rig.deps(), "77", startBranch); err != nil {
			t.Fatalf("review request: %v", err)
		}
		rows, err := inReviewBranches(t.Context(), rig.deps())
		if err != nil || len(rows) != 1 || rows[0].BranchName != startBranch {
			t.Errorf("rows = %+v, %v", rows, err)
		}
	})

	t.Run("a request without a branch: the issue's tracked branch", func(t *testing.T) {
		t.Parallel()

		rig := newReviewE2ERig(t)
		op := &reviewpkg.Op{Type: reviewpkg.OpRequest, FeatureSHA: tipOf(t, rig.dir, startBranch), Round: 1}
		if err := reviewpkg.Append(t.Context(), rig.client, "77", op, false); err != nil {
			t.Fatalf("append request: %v", err)
		}
		rows, err := inReviewBranches(t.Context(), rig.deps())
		if err != nil || len(rows) != 1 || rows[0].BranchName != startBranch {
			t.Errorf("rows = %+v, %v", rows, err)
		}
	})
}

// The status picker shows the branch the review is on, not the issue's most
// recent variant.
func TestReviewStatus_PickerShowsReviewedBranch(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	const variant = startBranch + "@v2"
	gitIn(t, rig.dir, "branch", variant, startBranch)
	branchtest.Seed(t, rig.client, branch.Op{Branch: variant}, branch.StatusInProgress)

	for _, name := range []string{startBranch, variant} {
		// A new round on the other branch each time: one of the two is not
		// the first row of the issue's branch chain.
		if err := runReviewRequest(t.Context(), rig.deps(), "77", name); err != nil {
			t.Fatalf("review request on %s: %v", name, err)
		}
		p := &scriptedReviewPrompter{}
		if err := runReviewStatusInteractive(t.Context(), rig.deps(), p); err != nil {
			t.Fatalf("runReviewStatusInteractive: %v", err)
		}

		t.Run("the row carries the reviewed branch "+name, func(t *testing.T) {
			if len(p.Offered) != 1 || p.Offered[0].BranchName != name {
				t.Errorf("offered = %+v", p.Offered)
			}
		})
		if _, err := runReviewReject(t.Context(), rig.deps(), "77", ""); err != nil {
			t.Fatalf("reject: %v", err)
		}
	}
}
