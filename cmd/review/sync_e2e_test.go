package review

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
)

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
	branchtest.Seed(t, rig.client, branch.Op{Branch: "7@feat@parent", Title: "parent"}, branch.StatusInProgress)
	// 77 becomes a sub-task of 7 (the rig tracked it without a parent).
	branchtest.Amend(t, rig.client, branch.Op{Branch: "77@feat@my-feature", Parent: "7"})
}

func TestReviewSync_ParentOnly(t *testing.T) {
	t.Parallel()

	t.Run("a sub-task behind its parent is merged", func(t *testing.T) {
		t.Parallel()

		rig := newReviewE2ERig(t)
		seedParent(t, rig)
		p := &scriptedReviewPrompter{Branch: &branch.Row{IssueSlug: "77", BranchName: startBranch}}
		if err := runReviewSyncInteractive(t.Context(), rig.deps(), p); err != nil {
			t.Fatalf("sync: %v\n%s", err, rig.stderr)
		}
		if n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", startBranch); n != 0 {
			t.Errorf("want the parent merged, %d commit(s) behind", n)
		}
	})

	t.Run("a branch without a parent is not a candidate", func(t *testing.T) {
		t.Parallel()

		rig := newReviewE2ERig(t)
		mustRunGit(t, rig.dir, "checkout", startBranch)
		p := &scriptedReviewPrompter{}
		if err := runReviewSyncInteractive(t.Context(), rig.deps(), p); err != nil {
			t.Fatalf("sync: %v", err)
		}
		if len(p.Offered) != 0 || !strings.Contains(rig.stdout.String(), "Nothing to sync.") {
			t.Errorf("offered = %+v, stdout:\n%s", p.Offered, rig.stdout)
		}
	})
}

func TestReviewSync_UsesPickedVariant(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	seedParent(t, rig)
	const variant = startBranch + "@v2"
	mustRunGit(t, rig.dir, "branch", variant, startBranch)
	branchtest.Seed(t, rig.client, branch.Op{Branch: variant}, branch.StatusInProgress)

	for _, picked := range []string{variant, startBranch} {
		// Whichever of the two the chain lists first, each is merged into
		// only when it is the one picked.
		other := startBranch
		if picked == startBranch {
			other = variant
		}
		behindOther, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", other)
		p := &scriptedReviewPrompter{Branch: &branch.Row{IssueSlug: "77", BranchName: picked}}
		err := runReviewSyncInteractive(t.Context(), rig.deps(), p)

		t.Run(picked+" picked: it is merged into", func(t *testing.T) {
			if err != nil {
				t.Fatalf("sync: %v\n%s", err, rig.stderr)
			}
			if n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", picked); n != 0 {
				t.Errorf("%s is %d commit(s) behind the parent", picked, n)
			}
		})
		t.Run(picked+" picked: the other branch is left alone", func(t *testing.T) {
			if n, _ := rig.client.CommitsAhead(t.Context(), "7@feat@parent", other); n != behindOther {
				t.Errorf("%s: %d commit(s) behind, was %d", other, n, behindOther)
			}
		})
	}
}
