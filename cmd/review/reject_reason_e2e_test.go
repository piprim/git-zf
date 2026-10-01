package review

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
)

func bringRigToInReview(t *testing.T, rig *reviewE2ERig) store.BranchRow {
	t.Helper()
	ctx := t.Context()
	if err := rig.client.RunGitAt(ctx, rig.dir, "checkout", "77@feat@my-feature"); err != nil {
		t.Fatalf("checkout feature branch: %v", err)
	}
	branches, _ := rig.store.ListBranches(ctx, store.BranchStatusInProgress)
	var picked store.BranchRow
	for _, b := range branches {
		if b.IssueSlug == "77" {
			picked = b
		}
	}
	if err := runReviewRequestInteractive(ctx, rig.deps(), &scriptedReviewPrompter{Branch: &picked}); err != nil {
		t.Fatalf("runReviewRequestInteractive: %v", err)
	}
	return picked
}

func TestReviewReject_Reason(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	picked := bringRigToInReview(t, rig)

	const reason = "Missing tests for the empty case.\n\nSee the spec, section 3."
	p := &scriptedReviewPrompter{Branch: &picked, TextAnswer: reason}
	if err := runReviewRejectInteractive(ctx, rig.deps(), p, "", true); err != nil {
		t.Fatalf("runReviewRejectInteractive: %v", err)
	}

	t.Run("prompted reason is stored in the review ref", func(t *testing.T) {
		ref, _, err := rig.client.ReadReviewRef(ctx, "77")
		if err != nil || ref == nil {
			t.Fatalf("ReadReviewRef: ref=%v err=%v", ref, err)
		}
		if ref.Comment != reason {
			t.Errorf("Comment: got %q, want %q", ref.Comment, reason)
		}
	})

	t.Run("reject output echoes the reason", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "Missing tests for the empty case.") {
			t.Errorf("stdout missing reason:\n%s", rig.stdout.String())
		}
	})

	t.Run("review status prints the reason under the latest round", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runReviewStatus(ctx, rig.deps(), "77"); err != nil {
			t.Fatalf("runReviewStatus: %v", err)
		}
		out := rig.stdout.String()
		if !strings.Contains(out, "changes_requested") || !strings.Contains(out, "See the spec, section 3.") {
			t.Errorf("status output missing reason:\n%s", out)
		}
	})
}

func TestReviewReject_ReasonFromFlagSkipsPrompt(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	picked := bringRigToInReview(t, rig)

	p := &scriptedReviewPrompter{Branch: &picked, TextAnswer: "from prompt"}
	if err := runReviewRejectInteractive(ctx, rig.deps(), p, "from flag", false); err != nil {
		t.Fatalf("runReviewRejectInteractive: %v", err)
	}

	t.Run("flag reason wins and prompt is not consulted", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(ctx, "77")
		if ref == nil || ref.Comment != "from flag" {
			t.Errorf("Comment: got %+v, want %q", ref, "from flag")
		}
		if p.TextCalls != 0 {
			t.Errorf("Text prompt called %d times, want 0", p.TextCalls)
		}
	})
}

func TestReviewReject_NoReasonLeavesRefClean(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	picked := bringRigToInReview(t, rig)

	if err := runReviewRejectInteractive(ctx, rig.deps(), &scriptedReviewPrompter{Branch: &picked}, "", true); err != nil {
		t.Fatalf("runReviewRejectInteractive: %v", err)
	}

	t.Run("empty prompt answer stores no comment", func(t *testing.T) {
		ref, _, _ := rig.client.ReadReviewRef(ctx, "77")
		if ref == nil || ref.Comment != "" {
			t.Errorf("Comment: got %+v, want empty", ref)
		}
	})
}

func TestRejectReasonFlags(t *testing.T) {
	t.Parallel()

	parse := func(t *testing.T, args ...string) (string, bool, error) {
		t.Helper()
		cmd := Review{}.getRejectCmd()
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatalf("ParseFlags: %v", err)
		}
		return rejectReasonFromFlags(cmd)
	}

	t.Run("-m sets the reason and marks it given", func(t *testing.T) {
		got, given, err := parse(t, "-m", "needs work")
		if err != nil || got != "needs work" || !given {
			t.Errorf("got %q given=%v, %v", got, given, err)
		}
	})

	t.Run("-m trims surrounding whitespace", func(t *testing.T) {
		got, given, err := parse(t, "-m", "  needs work\n")
		if err != nil || got != "needs work" || !given {
			t.Errorf("got %q given=%v, %v", got, given, err)
		}
	})

	t.Run("-F on an empty file still counts as given", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "empty.md")
		if err := os.WriteFile(path, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		got, given, err := parse(t, "-F", path)
		if err != nil || got != "" || !given {
			t.Errorf("got %q given=%v, %v", got, given, err)
		}
	})

	t.Run("-F reads the reason from a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "review.md")
		if err := os.WriteFile(path, []byte("# Review\n\nnope\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		got, given, err := parse(t, "-F", path)
		if err != nil || got != "# Review\n\nnope" || !given {
			t.Errorf("got %q given=%v, %v", got, given, err)
		}
	})

	t.Run("-F on a missing file errors", func(t *testing.T) {
		if _, _, err := parse(t, "-F", filepath.Join(t.TempDir(), "nope.md")); err == nil {
			t.Error("expected error for missing file")
		}
	})

	t.Run("-m and -F together error", func(t *testing.T) {
		_, _, err := parse(t, "-m", "x", "-F", "y.md")
		if !errors.Is(err, errReasonFlagsExclusive) {
			t.Errorf("got %v, want errReasonFlagsExclusive", err)
		}
	})

	t.Run("no flags yields empty reason and not given", func(t *testing.T) {
		got, given, err := parse(t)
		if err != nil || got != "" || given {
			t.Errorf("got %q given=%v, %v", got, given, err)
		}
	})
}

func TestReviewReject_ReasonPrintsOnlyAfterStatusRecorded(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	rig := newReviewE2ERig(t)
	bringRigToInReview(t, rig)

	// Not in review any more (the ref is authoritative): reject must fail
	// before touching the ref and must not echo the reason.
	ref, sha, err := rig.client.ReadReviewRef(ctx, "77")
	if err != nil || ref == nil {
		t.Fatalf("ReadReviewRef: ref=%v err=%v", ref, err)
	}
	ref.Status = string(store.ReviewStatusApproved)
	if _, err := rig.client.WriteReviewRef(ctx, "77", *ref, sha); err != nil {
		t.Fatalf("WriteReviewRef: %v", err)
	}
	rig.stdout.Reset()
	err = runReviewReject(ctx, rig.deps(), "77", "should not appear")

	t.Run("returns the not-in-review error", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("reason is not printed on failure", func(t *testing.T) {
		if strings.Contains(rig.stdout.String(), "should not appear") {
			t.Errorf("reason leaked to stdout:\n%s", rig.stdout.String())
		}
	})
}
