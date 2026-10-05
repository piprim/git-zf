package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	reviewpkg "github.com/piprim/git-zf/review"
)

func TestReviewRequest_RefusesWithUnincorporatedReviewerCommits(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, reviewpkg.StatusChangesRequested)

	err := runReviewRequest(t.Context(), rig.deps(), "77")

	t.Run("refused with sync hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want sync-hint refusal, got %v", err)
		}
	})
	t.Run("review branch NOT deleted", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("77@review")
		if !exists {
			t.Fatal("77@review must survive a refused request")
		}
	})
}

func TestReviewRequest_InteractiveOfferMergesThenProceeds(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, reviewpkg.StatusChangesRequested)
	prompter := &scriptedReviewPrompter{
		Branch:        &branch.Row{IssueSlug: "77", BranchName: "77@feat@my-feature"},
		ConfirmAnswer: true,
	}

	err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter)

	t.Run("request succeeds after inline merge", func(t *testing.T) {
		if err != nil {
			t.Fatalf("interactive request: %v\n%s", err, rig.stderr.String())
		}
	})
	t.Run("reviewer commits incorporated", func(t *testing.T) {
		// The stale 77@review was deleted by the round-2 request, so check the
		// reviewer file landed on the feature branch instead.
		mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
		if _, statErr := os.Stat(filepath.Join(rig.dir, "reviewer.txt")); statErr != nil {
			t.Fatalf("reviewer.txt not on feature branch: %v", statErr)
		}
	})
	t.Run("round 2 ref written", func(t *testing.T) {
		ref, _ := reviewpkg.Load(t.Context(), rig.client, "77")
		if ref == nil || ref.Round != 2 || ref.Status != reviewpkg.StatusInReview {
			t.Fatalf("want round-2 in_review ref, got %+v", ref)
		}
	})
}

func TestReviewRequest_InteractiveDeclineAborts(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, reviewpkg.StatusChangesRequested)
	prompter := &scriptedReviewPrompter{
		Branch:        &branch.Row{IssueSlug: "77", BranchName: "77@feat@my-feature"},
		ConfirmAnswer: false,
	}

	err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter)

	t.Run("aborted with sync hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git zf review sync") {
			t.Fatalf("want abort with sync hint, got %v", err)
		}
	})
	t.Run("review branch untouched", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("77@review")
		if !exists {
			t.Fatal("77@review must survive a declined offer")
		}
	})
}
