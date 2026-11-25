package review

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
)

func tipOf(t *testing.T, dir, ref string) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "-C", dir, "rev-parse", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}

	return strings.TrimSpace(string(out))
}

func TestReviewRequest_RecordsPickedVariant(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	const variant = "77@feat@my-feature@v2"

	mustRunGit(t, rig.dir, "checkout", "-b", variant, "77@feat@my-feature")
	if err := os.WriteFile(filepath.Join(rig.dir, "v2.txt"), []byte("v2\n"), 0o644); err != nil {
		t.Fatalf("write v2.txt: %v", err)
	}
	mustRunGit(t, rig.dir, "add", "v2.txt")
	mustRunGit(t, rig.dir, "commit", "-m", "feat: variant")
	mustRunGit(t, rig.dir, "checkout", "main")
	branchtest.Seed(t, rig.client, branch.Op{Branch: variant, Title: "my feature"}, branch.StatusInProgress)

	prompter := &scriptedReviewPrompter{Branch: &branch.Row{IssueSlug: "77", BranchName: variant}}
	if err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter); err != nil {
		t.Fatalf("request: %v\n%s", err, rig.stderr.String())
	}

	st, err := reviewpkg.Load(t.Context(), rig.client, "77")
	if err != nil || st == nil {
		t.Fatalf("Load: %+v, %v", st, err)
	}

	t.Run("the op names the picked variant", func(t *testing.T) {
		if st.Branch != variant {
			t.Errorf("Branch = %q, want %q", st.Branch, variant)
		}
	})
	t.Run("the submitted commit is the variant's tip", func(t *testing.T) {
		if want := tipOf(t, rig.dir, variant); st.FeatureSHA != want {
			t.Errorf("FeatureSHA = %q, want %q", st.FeatureSHA, want)
		}
	})
	t.Run("nothing says the branch is locked", func(t *testing.T) {
		if out := rig.stdout.String(); strings.Contains(strings.ToLower(out), "lock") {
			t.Errorf("stdout:\n%s", out)
		}
	})
}

func TestReviewRequest_ApprovedIsOfferedForNewRound(t *testing.T) {
	t.Parallel()

	rig := newReviewE2ERig(t)
	reviewtest.Seed(t, rig.client, "77", reviewpkg.StatusApproved, 1, tipOf(t, rig.dir, "77@feat@my-feature"))

	prompter := &scriptedReviewPrompter{Branch: &branch.Row{IssueSlug: "77", BranchName: "77@feat@my-feature"}}
	err := runReviewRequestInteractive(t.Context(), rig.deps(), prompter)

	t.Run("the approved branch is offered", func(t *testing.T) {
		if len(prompter.Offered) != 1 || prompter.Offered[0].IssueSlug != "77" {
			t.Fatalf("offered = %+v", prompter.Offered)
		}
	})
	t.Run("the request opens round 2", func(t *testing.T) {
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		st, _ := reviewpkg.Load(t.Context(), rig.client, "77")
		if st == nil || st.Round != 2 || st.Status != reviewpkg.StatusInReview {
			t.Fatalf("state = %+v", st)
		}
	})
}
