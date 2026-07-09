package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func TestRunReviewSync_MergesReviewBranch(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, rig.stderr.String())
		}
	})
	t.Run("reviewer commit incorporated", func(t *testing.T) {
		n, cErr := rig.client.CommitsAhead(t.Context(), "77@review", "77@feat@my-feature")
		if cErr != nil {
			t.Fatalf("CommitsAhead: %v", cErr)
		}
		if n != 0 {
			t.Fatalf("want 0 pending commits after sync, got %d", n)
		}
	})
	t.Run("review branch kept (cleanup is close/request's job)", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("77@review")
		if !exists {
			t.Fatal("77@review should survive sync")
		}
	})
}

func TestRunReviewSync_ConflictLeavesMergeInProgress(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	// Conflicting change on the feature branch (same file as the reviewer's).
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat: conflicting work")

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("returns nil (expected outcome, instructions printed)", func(t *testing.T) {
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
	})
	t.Run("merge left in progress", func(t *testing.T) {
		inProgress, _ := rig.client.MergeInProgress()
		if !inProgress {
			t.Fatal("want MERGE_HEAD present")
		}
	})
	t.Run("resolve hint printed", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "git zf commit") {
			t.Fatalf("want resolve hint, got:\n%s", rig.stdout.String())
		}
	})
}

func TestRunReviewSync_DirtyTreeRefusedBeforeMerge(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("wip\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("refused with stash hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git stash") {
			t.Fatalf("want stash hint error, got %v", err)
		}
	})
	t.Run("no merge attempted", func(t *testing.T) {
		inProgress, _ := rig.client.MergeInProgress()
		if inProgress {
			t.Fatal("no MERGE_HEAD expected")
		}
	})
}

func TestRunReviewSync_InReviewStatusDoesNotMerge(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusInReview)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("nothing to sync", func(t *testing.T) {
		if err != nil {
			t.Fatalf("sync: %v", err)
		}
		if !strings.Contains(rig.stdout.String(), "Nothing to sync") {
			t.Fatalf("want nothing-to-sync, got:\n%s", rig.stdout.String())
		}
	})
	t.Run("review commits untouched", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "77@review", "77@feat@my-feature")
		if n != 1 {
			t.Fatalf("want review commit still pending, got %d", n)
		}
	})
}

// seedParent gives the rig's issue 77 a parent issue 7 whose branch carries
// one commit the sub-task doesn't have. No remote in this rig, so sync's
// parent merge uses the local parent branch.
func seedParent(t *testing.T, rig *reviewE2ERig) {
	t.Helper()
	mustRunGit(t, rig.dir, "checkout", "main")
	mustRunGit(t, rig.dir, "checkout", "-b", "7@feat@parent")
	if err := os.WriteFile(filepath.Join(rig.dir, "parent.txt"), []byte("p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "parent.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat(7): parent commit")
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := rig.store.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "7", Title: "parent", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "7@feat@parent", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	if err := rig.store.InsertIssueRelation(t.Context(), "7", "77"); err != nil {
		t.Fatalf("InsertIssueRelation: %v", err)
	}
}

func TestRunReviewSync_SubtaskMergesReviewThenParent(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	seedParent(t, rig)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("sync: %v\n%s", err, rig.stderr.String())
		}
	})
	t.Run("reviewer commits incorporated (step 1)", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "77@review", "77@feat@my-feature")
		if n != 0 {
			t.Fatalf("want 0 pending review commits, got %d", n)
		}
	})
	t.Run("parent drift merged (step 2)", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", "77@feat@my-feature")
		if n != 0 {
			t.Fatalf("want parent merged, %d commits behind", n)
		}
	})
}

func TestRunReviewSync_ConflictedReviewMergeSkipsParent(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	// Conflicting change on the feature branch (same file as the reviewer's).
	mustRunGit(t, rig.dir, "checkout", "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "reviewer.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, rig.dir, "add", "reviewer.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat: conflicting work")
	seedParent(t, rig)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("returns nil with merge left in progress", func(t *testing.T) {
		if err != nil {
			t.Fatalf("want nil, got %v", err)
		}
		inProgress, _ := rig.client.MergeInProgress()
		if !inProgress {
			t.Fatal("want MERGE_HEAD present")
		}
	})
	t.Run("parent merge NOT attempted (step 2 skipped)", func(t *testing.T) {
		n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", "77@feat@my-feature")
		if n == 0 {
			t.Fatal("parent must not be merged while step 1 conflicts are unresolved")
		}
	})
}

func TestRunReviewSyncInteractive_OffersBranchWithPendingReview(t *testing.T) {
	rig := newReviewE2ERig(t)
	seedPendingReview(t, rig, store.ReviewStatusChangesRequested)
	capture := &captureReviewPrompter{} // records offered branches, picks none

	err := runReviewSyncInteractive(t.Context(), rig.deps(), capture)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("interactive sync: %v", err)
		}
	})
	t.Run("77 offered despite having no parent issue", func(t *testing.T) {
		if !branchSlugsOffered(capture.seen)["77"] {
			t.Fatalf("want slug 77 offered, got %v", capture.seen)
		}
	})
}
