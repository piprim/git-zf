package issue

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/cmd/issueflow"
	commitpkg "github.com/piprim/git-zf/commit"
	reviewpkg "github.com/piprim/git-zf/review"
)

// absentSHA is a well-formed SHA no object has.
const absentSHA = "1234567890123456789012345678901234567890"

// assertRuleARefused checks the close was refused with want, before main or
// the feature branch moved, and left the review open.
func assertRuleARefused(t *testing.T, rig *closeTestRig, err, want error, mainBefore, featureBefore string) {
	t.Helper()

	t.Run("the close is refused with the rule's error", func(t *testing.T) {
		if !errors.Is(err, want) {
			t.Fatalf("runClose = %v, want %v", err, want)
		}
	})
	t.Run("main did not move", func(t *testing.T) {
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got != mainBefore {
			t.Errorf("main moved from %s to %s", mainBefore, got)
		}
	})
	t.Run("the feature branch did not move", func(t *testing.T) {
		if got := featureTip(t, rig); got != featureBefore {
			t.Errorf("feature moved from %s to %s", featureBefore, got)
		}
	})
	t.Run("the review stays open", func(t *testing.T) {
		if st, _ := reviewpkg.Load(t.Context(), rig.client, "ABC-1"); st == nil || st.Closed {
			t.Errorf("review = %+v", st)
		}
	})
}

// assertClosedByReview checks the close merged into main and marked the review
// closed.
func assertClosedByReview(t *testing.T, rig *closeTestRig, err error, mainBefore string) {
	t.Helper()

	t.Run("the close succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("main advanced", func(t *testing.T) {
		if got := revParseInDir(t, rig.dir, "refs/heads/main"); got == mainBefore {
			t.Error("main did not move")
		}
	})
	t.Run("the review is marked closed", func(t *testing.T) {
		if st, _ := reviewpkg.Load(t.Context(), rig.client, "ABC-1"); st == nil || !st.Closed {
			t.Errorf("review = %+v", st)
		}
	})
}

func TestClose_RuleA(t *testing.T) {
	t.Parallel()

	// Each setup returns the approved commit.
	refusals := []struct {
		label   string
		setup   func(t *testing.T, rig *closeTestRig) string
		wantErr error
	}{
		{"A is not in the clone", func(*testing.T, *closeTestRig) string { return absentSHA }, issueflow.ErrApprovalMissing},
		{"T is a strict ancestor of A", func(t *testing.T, rig *closeTestRig) string {
			return commitOnNewBranch(t, rig.dir, "ahead", remoteFeature, "ahead.txt")
		}, issueflow.ErrApprovedAhead},
		{"T is a strict descendant of A", func(t *testing.T, rig *closeTestRig) string {
			approved := featureTip(t, rig)
			commitLocally(t, rig, "late.txt", "late\n")

			return approved
		}, issueflow.ErrCommitsAfterApproval},
		{"anything else", func(t *testing.T, rig *closeTestRig) string {
			return commitOnNewBranch(t, rig.dir, "elsewhere", "main", "elsewhere.txt")
		}, issueflow.ErrNotApprovedBranch},
	}

	for _, tc := range refusals {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			rig := newCloseRig(t)
			approved := tc.setup(t, rig)
			seedApproval(t, rig, "ABC-1", approved, false)
			mainBefore, featureBefore := revParseInDir(t, rig.dir, "refs/heads/main"), featureTip(t, rig)

			err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

			assertRuleARefused(t, rig, err, tc.wantErr, mainBefore, featureBefore)
		})
	}

	t.Run("T is A", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		seedApproval(t, rig, "ABC-1", featureTip(t, rig), false)
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertClosedByReview(t, rig, err, mainBefore)
	})
}

// The incident: the developer rebased and pushed, a clone still holds the old
// copy of the branch, and the rebased branch is the approved one.
func TestClose_RuleA_Incident(t *testing.T) {
	t.Parallel()

	incident := func(t *testing.T) (rig *closeTestRig, approved string) {
		t.Helper()

		rig = newRemoteCloseRig(t)
		commitLocally(t, rig, "old-migration.txt", "old\n")
		approved = pushFromElsewhere(t, rig.dir, remoteFeature, "renamed-migration.txt", "renamed\n")
		seedApproval(t, rig, "ABC-1", approved, false)

		return rig, approved
	}

	t.Run("the remote check stops the close of the old copy", func(t *testing.T) {
		t.Parallel()

		rig, _ := incident(t)
		mainBefore, featureBefore := revParseInDir(t, rig.dir, "refs/heads/main"), featureTip(t, rig)

		err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertRuleARefused(t, rig, err, ErrRemoteMoved, mainBefore, featureBefore)
	})

	t.Run("reset to the approved commit, the close merges the rebased work only", func(t *testing.T) {
		t.Parallel()

		rig, approved := incident(t)
		mustRunGitAt(t, rig.dir, "fetch", "-q", "origin")
		mustRunGitAt(t, rig.dir, "branch", "-f", remoteFeature, approved)
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertClosedByReview(t, rig, err, mainBefore)
		t.Run("main holds the renamed migration", func(t *testing.T) {
			if got := showFileAt(t, rig.dir, "main", "renamed-migration.txt"); got != "renamed\n" {
				t.Errorf("main:renamed-migration.txt = %q", got)
			}
		})
		t.Run("main does not hold the old migration", func(t *testing.T) {
			if err := exec.CommandContext(t.Context(), "git", "-C", rig.dir,
				"cat-file", "-e", "main:old-migration.txt").Run(); err == nil {
				t.Error("main holds old-migration.txt")
			}
		})
	})
}

func TestClose_RuleA_TwoApprovals(t *testing.T) {
	t.Parallel()

	t.Run("one contains the other", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		first := featureTip(t, rig)
		second := commitOnNewBranch(t, rig.dir, "second", remoteFeature, "second.txt")
		seedApproval(t, rig, "ABC-1", first, false)
		seedApproval(t, rig, "ABC-1", second, false)
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		t.Run("the tip on the first one", func(t *testing.T) {
			assertRuleARefused(t, rig, err, issueflow.ErrApprovedAhead, mainBefore, first)
		})

		mustRunGitAt(t, rig.dir, "branch", "-f", remoteFeature, second)
		err = runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		t.Run("the tip on the second one", func(t *testing.T) {
			assertClosedByReview(t, rig, err, mainBefore)
		})
	})

	t.Run("diverged", func(t *testing.T) {
		t.Parallel()

		rig := newCloseRig(t)
		tip := featureTip(t, rig)
		seedApproval(t, rig, "ABC-1", tip, false)
		seedApproval(t, rig, "ABC-1", commitOnNewBranch(t, rig.dir, "elsewhere", "main", "elsewhere.txt"), false)
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertRuleARefused(t, rig, err, issueflow.ErrApprovalsDiverged, mainBefore, tip)
	})
}

func TestClose_RuleA_EmptyApprovedSHA(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	seedApproval(t, rig, "ABC-1", "", false)
	mainBefore, featureBefore := revParseInDir(t, rig.dir, "refs/heads/main"), featureTip(t, rig)

	err := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

	assertRuleARefused(t, rig, err, issueflow.ErrApprovalMissing, mainBefore, featureBefore)
}

// The two-run cases: the first close takes the remote's commits and stops, the
// second one applies rule A to what came in.
func TestClose_RuleA_TwoStep(t *testing.T) {
	t.Parallel()

	takeRemote := func(rig *closeTestRig) *scriptedPrompter {
		p := classicClosePrompter(rig)
		p.TakeRemote = true

		return p
	}

	t.Run("the remote moved past the approval", func(t *testing.T) {
		t.Parallel()

		rig := newRemoteCloseRig(t)
		seedApproval(t, rig, "ABC-1", featureTip(t, rig), false)
		remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		first := runClose(t.Context(), rig.deps(), takeRemote(rig))

		t.Run("the first close fast-forwards and stops", func(t *testing.T) {
			if !errors.Is(first, ErrRemoteMoved) || featureTip(t, rig) != remoteTip {
				t.Fatalf("runClose = %v, tip %s, want %s", first, featureTip(t, rig), remoteTip)
			}
		})

		second := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertRuleARefused(t, rig, second, issueflow.ErrCommitsAfterApproval, mainBefore, remoteTip)
	})

	t.Run("diverged without conflict", func(t *testing.T) {
		t.Parallel()

		rig := newRemoteCloseRig(t)
		commitLocally(t, rig, "mine.txt", "mine\n")
		seedApproval(t, rig, "ABC-1", featureTip(t, rig), false)
		pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		first := runClose(t.Context(), rig.deps(), takeRemote(rig))

		t.Run("the first close merges and stops", func(t *testing.T) {
			if !errors.Is(first, ErrRemoteMoved) {
				t.Fatalf("runClose = %v", first)
			}
		})
		merged := featureTip(t, rig)

		second := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertRuleARefused(t, rig, second, issueflow.ErrCommitsAfterApproval, mainBefore, merged)
		t.Run("the merge commit is still the tip of the branch", func(t *testing.T) {
			if revParseInDir(t, rig.dir, remoteFeature+"^2") == "" {
				t.Error("the tip is not a merge commit")
			}
		})
	})

	t.Run("the developer is behind the reviewer's approved commits", func(t *testing.T) {
		t.Parallel()

		rig := newRemoteCloseRig(t)
		remoteTip := pushFromElsewhere(t, rig.dir, remoteFeature, "more.txt", "more\n")
		seedApproval(t, rig, "ABC-1", remoteTip, false)
		mainBefore := revParseInDir(t, rig.dir, "refs/heads/main")

		first := runClose(t.Context(), rig.deps(), takeRemote(rig))

		t.Run("the first close fast-forwards and stops", func(t *testing.T) {
			if !errors.Is(first, ErrRemoteMoved) || featureTip(t, rig) != remoteTip {
				t.Fatalf("runClose = %v, tip %s, want %s", first, featureTip(t, rig), remoteTip)
			}
		})

		second := runClose(t.Context(), rig.deps(), classicClosePrompter(rig))

		assertClosedByReview(t, rig, second, mainBefore)
	})
}

func TestClose_SignedGate_UnsignedContainsSigned(t *testing.T) {
	rig, slug, feature := newSignedCloseRig(t)
	seedApproval(t, rig, slug, featureTip(t, rig), true)
	descendant := commitOnNewBranch(t, rig.dir, "descendant", feature, "descendant.txt")
	seedApproval(t, rig, slug, descendant, false)
	// The branch is on the approved commit: only the signature is missing.
	mustRunGitAt(t, rig.dir, "branch", "-f", feature, descendant)

	cleanup, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("the approved commit is the descendant, whose approval is unsigned", func(t *testing.T) {
		if !errors.Is(err, ErrApprovalNotSigned) || cleanup != nil {
			t.Fatalf("reviewPreflight = %v", err)
		}
	})
}

func TestClose_SignedGate_TwoApprovalsSameCommit(t *testing.T) {
	rig, slug, _ := newSignedCloseRig(t)
	seedApproval(t, rig, slug, featureTip(t, rig), false)
	seedApproval(t, rig, slug, featureTip(t, rig), true)

	cleanup, err := reviewPreflight(t.Context(), rig.deps(), rig.pickedBranchRow(), nil)

	t.Run("one verified approval of the approved commit is enough", func(t *testing.T) {
		if err != nil || cleanup == nil {
			t.Fatalf("reviewPreflight = %v, cleanup nil: %v", err, cleanup == nil)
		}
	})
}

// The review of an issue gates the branch it reviews, not the issue's other
// variants.
func TestClose_Variant_NotGatedByReview(t *testing.T) {
	t.Parallel()

	const variant = "ABC-1@feat@other-way"

	for _, status := range []string{
		reviewpkg.StatusInReview, reviewpkg.StatusChangesRequested, reviewpkg.StatusApproved,
	} {
		t.Run(status, func(t *testing.T) {
			t.Parallel()

			rig := newCloseRig(t)
			commitOnNewBranch(t, rig.dir, variant, "main", "other-way.txt")
			branchtest.Seed(t, rig.client, branch.Op{Branch: variant, Title: "Other way"}, branch.StatusInProgress)

			ops := []reviewpkg.Op{{Type: reviewpkg.OpRequest, FeatureSHA: featureTip(t, rig), Branch: remoteFeature}}
			switch status {
			case reviewpkg.StatusChangesRequested:
				ops = append(ops, reviewpkg.Op{Type: reviewpkg.OpReject, Round: 1})
			case reviewpkg.StatusApproved:
				ops = append(ops, reviewpkg.Op{Type: reviewpkg.OpApprove, ApprovedSHA: featureTip(t, rig), Round: 1})
			}
			for _, op := range ops {
				if err := reviewpkg.Append(t.Context(), rig.client, "ABC-1", &op, false); err != nil {
					t.Fatalf("append %s: %v", op.Type, err)
				}
			}

			p := classicClosePrompter(rig)
			p.Branch = &branch.Row{
				IssueSlug: "ABC-1", Title: "Other way", BranchName: variant,
				Type: "feat", Status: branch.StatusInProgress,
			}
			p.Strategy = commitpkg.MergeStrategySquash
			p.Message = []byte("feat: the other way\n")

			err := runClose(t.Context(), rig.deps(), p)

			t.Run("the variant closes", func(t *testing.T) {
				if err != nil {
					t.Fatalf("runClose: %v", err)
				}
				if got := strings.TrimSpace(showFileAt(t, rig.dir, "main", "other-way.txt")); got != "x" {
					t.Errorf("main:other-way.txt = %q", got)
				}
			})
			t.Run("the review of the other branch stays open, with its status", func(t *testing.T) {
				st, lErr := reviewpkg.Load(t.Context(), rig.client, "ABC-1")
				if lErr != nil || st == nil || st.Closed || st.Status != status {
					t.Errorf("review = %+v, %v", st, lErr)
				}
			})
		})
	}
}
