package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestReviewRequest_RefusesWithUnincorporatedReviewerCommits(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)

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
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	// seedPendingReview only writes the review ref (round 1); mirror a real
	// rejected round-1 by also seeding the store's review row, so the
	// upcoming request's InsertReview bumps the round counter to 2.
	if _, err := rig.store.InsertReview(t.Context(), "77", ""); err != nil {
		t.Fatalf("seed round-1 review row: %v", err)
	}
	prompter := &scriptedReviewPrompter{
		Branch:        &store.BranchRow{IssueSlug: "77", BranchName: "77@feat@my-feature"},
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
		ref, _, _ := rig.client.ReadReviewRef(t.Context(), "77")
		if ref == nil || ref.Round != 2 || ref.Status != string(store.ReviewStatusInReview) {
			t.Fatalf("want round-2 in_review ref, got %+v", ref)
		}
	})
}

func TestReviewRequest_InteractiveDeclineAborts(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	prompter := &scriptedReviewPrompter{
		Branch:        &store.BranchRow{IssueSlug: "77", BranchName: "77@feat@my-feature"},
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
