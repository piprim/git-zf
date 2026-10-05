package review

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
)

// writeLegacyBranchBlob leaves at refs/zf/branches/<slug> the JSON blob an
// older git-zf wrote.
func writeLegacyBranchBlob(t *testing.T, dir, slug, content string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}

	mustRunGit(t, dir, "update-ref", "refs/zf/branches/"+slug, string(bytes.TrimSpace(out)))
}

// `issue track` on a branch whose ref is still a blob replaces the blob by a
// chain, and keeps what the blob knew about the issue.
func TestTrack_Developer_ReplacesALegacyBlob(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	writeLegacyBranchBlob(t, rig.dir, "X.2",
		`{"issue_slug":"X.2","branch_name":"X.2@feat@part-two","parent_slug":"X","tracker_type":"redmine"}`)
	mustRunGit(t, rig.dir, "checkout", "-q", "-b", "X.2@feat@part-two")

	err := runTrack(ctx, rig.deps())

	t.Run("track succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack: %v", err)
		}
	})

	st, loadErr := branch.Load(ctx, rig.client, "X.2")

	t.Run("the ref is now a chain that tracks the branch", func(t *testing.T) {
		if loadErr != nil || st == nil || st.Entry("X.2@feat@part-two") == nil {
			t.Fatalf("Load = %+v, %v", st, loadErr)
		}
	})

	t.Run("the parent and tracker type of the blob are kept", func(t *testing.T) {
		if st == nil || st.Parent != "X" || st.TrackerType != "redmine" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("the title comes from the branch name", func(t *testing.T) {
		if st == nil || st.Title != "part two" {
			t.Errorf("state = %+v", st)
		}
	})
}

// A sub-task whose parent is still tracked in the old blob format cannot be
// synced: the drift check against the parent would silently be skipped.
func TestRunReviewSync_RefusesWhenTheParentRefIsALegacyBlob(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	branchtest.Amend(t, rig.client, branch.Op{Branch: "77@feat@my-feature", Parent: "7"})
	writeLegacyBranchBlob(t, rig.dir, "7", `{"issue_slug":"7","branch_name":"7@feat@parent"}`)

	err := runReviewSync(t.Context(), rig.deps(), "77")

	t.Run("sync is refused with ErrLegacyBranch", func(t *testing.T) {
		if !errors.Is(err, branch.ErrLegacyBranch) {
			t.Fatalf("err = %v, want ErrLegacyBranch", err)
		}
	})

	t.Run("the error names the way out", func(t *testing.T) {
		if !strings.Contains(err.Error(), "git zf issue track") {
			t.Errorf("err = %v", err)
		}
	})
}

// A branch that exists only in another clone is not offered by a picker that
// acts on a checked-out branch.
func TestReviewRequest_OffersOnlyBranchesPresentLocally(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	// Tracked (a teammate started it), but never checked out here.
	branchtest.Seed(t, rig.client, branch.Op{Branch: "99@feat@theirs"}, branch.StatusInProgress)

	prompter := &captureReviewPrompter{}
	if err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}

	offered := branchSlugsOffered(prompter.seen)

	t.Run("the local branch is offered", func(t *testing.T) {
		if !offered["77"] {
			t.Errorf("offered = %+v", prompter.seen)
		}
	})

	t.Run("the branch that is not checked out here is not offered", func(t *testing.T) {
		if offered["99"] {
			t.Errorf("offered = %+v", prompter.seen)
		}
	})
}

// A branch that a prune closed is reopened by its owner with `issue track`.
func TestTrack_Developer_ReopensAClosedBranch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	if err := branch.SetStatus(ctx, rig.client, "77", "77@feat@my-feature", branch.StatusClosed); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	mustRunGit(t, rig.dir, "checkout", "-q", "77@feat@my-feature")

	err := runTrack(ctx, rig.deps())

	t.Run("track succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runTrack: %v", err)
		}
	})

	t.Run("the branch is in progress again", func(t *testing.T) {
		_, e, findErr := branch.Find(ctx, rig.client, "77@feat@my-feature")
		if findErr != nil || e == nil || e.Status != branch.StatusInProgress {
			t.Errorf("entry = %+v, %v", e, findErr)
		}
	})

	t.Run("the chain still has one entry for it", func(t *testing.T) {
		st, _ := branch.Load(ctx, rig.client, "77")
		if st == nil || len(st.Entries) != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

// A merged branch is not reopened by track: only a closed one is.
func TestTrack_Developer_LeavesAMergedBranch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	if err := branch.SetStatus(ctx, rig.client, "77", "77@feat@my-feature", branch.StatusMerged); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	mustRunGit(t, rig.dir, "checkout", "-q", "77@feat@my-feature")

	if err := runTrack(ctx, rig.deps()); err != nil {
		t.Fatalf("runTrack: %v", err)
	}

	t.Run("the branch stays merged and track says so", func(t *testing.T) {
		_, e, _ := branch.Find(ctx, rig.client, "77@feat@my-feature")
		if e == nil || e.Status != branch.StatusMerged {
			t.Errorf("entry = %+v", e)
		}
		if !strings.Contains(rig.stdout.String(), "already tracked (status: merged)") {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})
}
