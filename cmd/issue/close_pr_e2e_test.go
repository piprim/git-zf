package issue

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	commitpkg "github.com/piprim/git-zf/commit"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/review/reviewtest"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// prCloseRig is a close rig with an origin and an open pull request of the
// feature branch against main on the fake forge.
func newPrCloseRig(t *testing.T) *closeTestRig {
	t.Helper()

	rig := newCloseRig(t)
	rig.addOrigin(t)
	rig.tracker.PullRequests = []tracker.PullRequest{{
		Number: "7", URL: "fake://pr/7", Head: "ABC-1@feat@add-thing", Base: "main",
	}}

	return rig
}

// forgeMerged plays the forge: origin/main takes the feature's tip.
func forgeMerged(t *testing.T, rig *closeTestRig) {
	t.Helper()

	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@feat@add-thing:main")
}

func prPrompter(rig *closeTestRig, strategy commitpkg.MergeStrategy) *scriptedPrompter {
	return &scriptedPrompter{
		Branch: rig.pickedBranchRow(), Strategy: strategy, Confirm: true,
		Message: []byte("feat(thing): close ABC-1\n"), DeleteBranch: true,
	}
}

func TestClose_PullRequest_HappyPath(t *testing.T) {
	rig := newPrCloseRig(t)
	forgeMerged(t, rig)
	p := prPrompter(rig, commitpkg.MergeStrategySquash)

	err := runClose(t.Context(), rig.deps(), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("the forge merged with the picked strategy", func(t *testing.T) {
		if len(rig.tracker.RecordedMerges) != 1 || rig.tracker.RecordedMerges[0] != (fake.Merge{Number: "7", Method: tracker.MergeMethodSquash}) {
			t.Errorf("RecordedMerges = %+v", rig.tracker.RecordedMerges)
		}
	})
	t.Run("no commit message was composed, no base was picked", func(t *testing.T) {
		if p.CapturedPrefill != nil || p.BaseCalled {
			t.Errorf("prefill %v, base picked %v", p.CapturedPrefill, p.BaseCalled)
		}
	})
	t.Run("local main is the forge's main", func(t *testing.T) {
		if got := rig.headSubject(t, "main"); got != "feat: add thing" {
			t.Errorf("main head = %q", got)
		}
	})
	t.Run("the branch is recorded merged", func(t *testing.T) {
		if got := rig.statusOf(t, "ABC-1@feat@add-thing"); got != branch.StatusMerged {
			t.Errorf("status = %q", got)
		}
	})
	t.Run("the closing line names the pull request", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "pull request #7") {
			t.Errorf("stdout %q", rig.stdout.String())
		}
	})
}

func TestClose_PullRequest_BaseIsThePullRequests(t *testing.T) {
	rig := newPrCloseRig(t)
	forgeMerged(t, rig)
	rig.addBranch(t, "develop") // a second candidate: the picker would run

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategyClassic))

	t.Run("the pull request's base is used without the picker", func(t *testing.T) {
		if err != nil || !strings.Contains(rig.stdout.String(), `against "main"`) {
			t.Errorf("err %v, stdout %q", err, rig.stdout.String())
		}
	})
}

func TestClose_PullRequest_BaseFlagConflict(t *testing.T) {
	rig := newPrCloseRig(t)
	d := rig.deps()
	d.baseOverride = "develop"

	err := runClose(t.Context(), d, prPrompter(rig, commitpkg.MergeStrategyClassic))

	t.Run("refused before any merge", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "targets") || len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("err %v, merges %+v", err, rig.tracker.RecordedMerges)
		}
	})
}

func TestClose_PullRequest_UnpushedRefused(t *testing.T) {
	rig := newPrCloseRig(t)
	mustRunGitAt(t, rig.dir, "switch", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "commit", "--allow-empty", "-m", "feat: more")
	mustRunGitAt(t, rig.dir, "switch", "main")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("refused, nothing merged, branch still in progress", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "unpushed") {
			t.Errorf("err = %v", err)
		}
		if len(rig.tracker.RecordedMerges) != 0 || rig.statusOf(t, "ABC-1@feat@add-thing") != branch.StatusInProgress {
			t.Errorf("merges %+v, status %q", rig.tracker.RecordedMerges, rig.statusOf(t, "ABC-1@feat@add-thing"))
		}
	})
}

func TestClose_PullRequest_BehindRefused(t *testing.T) {
	rig := newPrCloseRig(t)
	mustRunGitAt(t, rig.dir, "switch", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "commit", "--allow-empty", "-m", "feat: more")
	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "reset", "-q", "--hard", "HEAD~1")
	mustRunGitAt(t, rig.dir, "switch", "main")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("refused with a fetch hint", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "behind") || len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("err %v, merges %+v", err, rig.tracker.RecordedMerges)
		}
	})
}

func TestClose_PullRequest_ForgeRefusal(t *testing.T) {
	rig := newPrCloseRig(t)
	rig.tracker.MergeError = errors.New("Pull Request is not mergeable")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("the forge's message is the error", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "not mergeable") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("nothing changed: main untouched, branch in progress", func(t *testing.T) {
		if got := rig.headSubject(t, "main"); got != "chore: init" {
			t.Errorf("main head = %q", got)
		}
		if got := rig.statusOf(t, "ABC-1@feat@add-thing"); got != branch.StatusInProgress {
			t.Errorf("status = %q", got)
		}
	})
}

func TestClose_PullRequest_NoCapabilityMergesLocally(t *testing.T) {
	rig := newPrCloseRig(t)
	d := rig.deps()
	d.tracker = struct{ tracker.Tracker }{rig.tracker}

	err := runClose(t.Context(), d, prPrompter(rig, commitpkg.MergeStrategySquash))

	t.Run("the local squash ran, the forge was not asked", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
		if got := rig.headSubject(t, "main"); got != "feat(thing): close ABC-1" {
			t.Errorf("main head = %q", got)
		}
		if len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("merges %+v", rig.tracker.RecordedMerges)
		}
	})
}

// A branch reviewed with git-zf gets the reviewer's commits merged in by the
// review preflight, which puts it ahead of the remote. With a pull request,
// close pushes them itself before asking the forge: the forge must merge
// the reviewed branch, and a "push it first" here would cost a second run.
func TestClose_PullRequest_ReviewedBranchIsPushedFirst(t *testing.T) {
	rig := newPrCloseRig(t)
	// The reviewer fixed the branch on ABC-1@review and pushed it.
	mustRunGitAt(t, rig.dir, "checkout", "-q", "-b", "ABC-1@review", "ABC-1@feat@add-thing")
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("feature\nreviewer fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRunGitAt(t, rig.dir, "add", "feature.txt")
	mustRunGitAt(t, rig.dir, "commit", "-q", "-m", "fix: reviewer fix")
	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@review")
	mustRunGitAt(t, rig.dir, "checkout", "-q", "main")
	featureSHA, _ := rig.client.ResolveRef("refs/heads/ABC-1@feat@add-thing")
	reviewtest.Seed(t, rig.client, "ABC-1", reviewpkg.StatusApproved, 1, featureSHA.String())
	p := prPrompter(rig, commitpkg.MergeStrategyClassic)
	p.DeleteBranch = false // keep the remote branch to inspect it

	err := runClose(t.Context(), rig.deps(), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("the reviewer's commit reached the remote branch before the forge merged", func(t *testing.T) {
		mustRunGitAt(t, rig.dir, "fetch", "-q", "origin")
		if got := rig.headSubject(t, "origin/ABC-1@feat@add-thing"); got != "fix: reviewer fix" {
			t.Errorf("origin feature head = %q", got)
		}
		if len(rig.tracker.RecordedMerges) != 1 {
			t.Errorf("merges %+v", rig.tracker.RecordedMerges)
		}
	})
}

func TestClose_PullRequest_ReviewedBranchWithUnpushedCommitRefused(t *testing.T) {
	rig := newPrCloseRig(t)
	mustRunGitAt(t, rig.dir, "checkout", "-q", "-b", "ABC-1@review", "ABC-1@feat@add-thing")
	if err := os.WriteFile(filepath.Join(rig.dir, "feature.txt"), []byte("feature\nreviewer fix\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustRunGitAt(t, rig.dir, "add", "feature.txt")
	mustRunGitAt(t, rig.dir, "commit", "-q", "-m", "fix: reviewer fix")
	mustRunGitAt(t, rig.dir, "push", "-q", "origin", "ABC-1@review")
	mustRunGitAt(t, rig.dir, "checkout", "-q", "main")
	featureSHA, _ := rig.client.ResolveRef("refs/heads/ABC-1@feat@add-thing")
	reviewtest.Seed(t, rig.client, "ABC-1", reviewpkg.StatusApproved, 1, featureSHA.String())
	mustRunGitAt(t, rig.dir, "checkout", "-q", "ABC-1@feat@add-thing")
	mustRunGitAt(t, rig.dir, "commit", "--allow-empty", "-m", "feat: more")
	mustRunGitAt(t, rig.dir, "checkout", "-q", "main")

	err := runClose(t.Context(), rig.deps(), prPrompter(rig, commitpkg.MergeStrategyClassic))

	t.Run("refused as unpushed", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "unpushed") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("nothing merged", func(t *testing.T) {
		if len(rig.tracker.RecordedMerges) != 0 {
			t.Errorf("merges %+v", rig.tracker.RecordedMerges)
		}
	})
	t.Run("nothing was pushed", func(t *testing.T) {
		mustRunGitAt(t, rig.dir, "fetch", "-q", "origin")
		if got := rig.headSubject(t, "origin/ABC-1@feat@add-thing"); got != "feat: add thing" {
			t.Errorf("origin feature head = %q", got)
		}
	})
}
