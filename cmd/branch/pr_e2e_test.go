package branch

import (
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// prRig is a merge rig with an origin, a pushed branch "feat-x" and a fake
// forge.
func newPrRig(t *testing.T) (*mergeRig, *fake.Tracker) {
	t.Helper()

	r := newMergeRig(t)
	r.addOrigin(t)
	r.addLocalBranch(t, "feat-x")
	r.git(t, "push", "-q", "origin", "feat-x")

	return r, &fake.Tracker{}
}

func (r *mergeRig) prDeps(ft *fake.Tracker, form *scriptedPullRequestForm) prDeps {
	var forge tracker.PullRequests
	if ft != nil {
		forge = ft
	}

	return prDeps{
		client: r.client, cfg: r.cfg, forge: forge, trackerType: "fake",
		pushConfirm: acceptPush, form: form.form(),
	}
}

func TestRunPr_OpensWithThePrefill(t *testing.T) {
	r, ft := newPrRig(t)
	form := &scriptedPullRequestForm{Open: true}

	err := runPr(t.Context(), r.prDeps(ft, form), "feat-x")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runPr: %v", err)
		}
	})
	t.Run("the pull request is opened from the branch name against the base", func(t *testing.T) {
		want := fake.PullRequestCreate{Head: "feat-x", Base: "master", Title: "feat-x"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
	})
	t.Run("the URL is printed", func(t *testing.T) {
		if !strings.Contains(r.stdout.String(), "fake://pr/1") {
			t.Errorf("stdout %q", r.stdout.String())
		}
	})
}

func TestRunPr_CurrentBranchByDefault(t *testing.T) {
	r, ft := newPrRig(t)
	r.git(t, "switch", "feat-x")
	r.rebuildClient(t)

	err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "")

	t.Run("the checked-out branch is used", func(t *testing.T) {
		if err != nil || len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0].Head != "feat-x" {
			t.Errorf("err %v, opened %+v", err, ft.RecordedPullRequests)
		}
	})
}

func TestRunPr_ExistingPullRequestPrintsItsURL(t *testing.T) {
	r, ft := newPrRig(t)
	ft.PullRequests = []tracker.PullRequest{{Number: "7", URL: "fake://pr/7", Head: "feat-x", Base: "master"}}
	form := &scriptedPullRequestForm{Open: true}

	err := runPr(t.Context(), r.prDeps(ft, form), "feat-x")

	t.Run("no form, nothing opened, the URL printed", func(t *testing.T) {
		if err != nil || form.Calls != 0 || len(ft.RecordedPullRequests) != 0 || !strings.Contains(r.stdout.String(), "fake://pr/7") {
			t.Errorf("err %v, calls %d, opened %+v, stdout %q", err, form.Calls, ft.RecordedPullRequests, r.stdout.String())
		}
	})
}

func TestRunPr_PushesFirst(t *testing.T) {
	r, ft := newPrRig(t)
	r.addLocalBranch(t, "feat-y") // not pushed

	err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "feat-y")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runPr: %v", err)
		}
	})
	t.Run("the branch is on origin", func(t *testing.T) {
		if !r.gitOK(t, "rev-parse", "--verify", "--quiet", "refs/remotes/origin/feat-y") {
			t.Error("feat-y was not pushed")
		}
	})
	t.Run("the pull request is opened", func(t *testing.T) {
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0].Head != "feat-y" {
			t.Errorf("opened %+v", ft.RecordedPullRequests)
		}
	})
}

func TestRunPr_DeclinedPushAborts(t *testing.T) {
	r, ft := newPrRig(t)
	r.addLocalBranch(t, "feat-y")
	d := r.prDeps(ft, &scriptedPullRequestForm{Open: true})
	d.pushConfirm = declinePush

	err := runPr(t.Context(), d, "feat-y")

	t.Run("aborted, nothing opened", func(t *testing.T) {
		if err != nil || len(ft.RecordedPullRequests) != 0 || !strings.Contains(r.stdout.String(), "Aborted.") {
			t.Errorf("err %v, opened %+v, stdout %q", err, ft.RecordedPullRequests, r.stdout.String())
		}
	})
}

func TestRunPr_Refusals(t *testing.T) {
	t.Run("a tracker without pull requests", func(t *testing.T) {
		r, _ := newPrRig(t)
		err := runPr(t.Context(), r.prDeps(nil, &scriptedPullRequestForm{Open: true}), "feat-x")
		if err == nil || !strings.Contains(err.Error(), "no pull requests") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("no tracker at all", func(t *testing.T) {
		r, _ := newPrRig(t)
		d := r.prDeps(nil, &scriptedPullRequestForm{Open: true})
		d.trackerType = ""
		err := runPr(t.Context(), d, "feat-x")
		if err == nil || !strings.Contains(err.Error(), "no issue tracker") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("--yes: a pull request needs a title", func(t *testing.T) {
		r, ft := newPrRig(t)
		d := r.prDeps(ft, &scriptedPullRequestForm{Open: true})
		d.yes = true
		err := runPr(t.Context(), d, "feat-x")
		if err == nil || !strings.Contains(err.Error(), "--yes") || len(ft.RecordedPullRequests) != 0 {
			t.Errorf("err = %v, opened %+v", err, ft.RecordedPullRequests)
		}
	})
	t.Run("the base branch itself", func(t *testing.T) {
		r, ft := newPrRig(t)
		err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "master")
		if err == nil || !strings.Contains(err.Error(), "base branch") {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("a branch that does not exist", func(t *testing.T) {
		r, ft := newPrRig(t)
		if err := runPr(t.Context(), r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "nope"); err == nil {
			t.Error("want an error")
		}
	})
	t.Run("no remote", func(t *testing.T) {
		r := newMergeRig(t)
		r.addLocalBranch(t, "feat-x")
		err := runPr(t.Context(), r.prDeps(&fake.Tracker{}, &scriptedPullRequestForm{Open: true}), "feat-x")
		if err == nil || !strings.Contains(err.Error(), "remote") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestRunPr_MirroredIssueClosesItsNumber(t *testing.T) {
	r, ft := newPrRig(t)
	ctx := t.Context()
	r.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{Name: "zf", ID: "piprim/git-zf"}},
	}
	ft.ProjectIssues = []tracker.Issue{{
		TrackerType: "fake", ID: "42", Subject: "Add thing", Status: "open",
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}}
	if _, err := issueflow.MirrorOf(r.cfg, ft).Reconcile(ctx, r.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	records, _, err := issuepkg.List(ctx, r.client)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 issue, got %d (%v)", len(records), err)
	}
	r.addLocalBranch(t, "42@feat@add-thing")
	r.git(t, "push", "-q", "origin", "42@feat@add-thing")
	branchtest.Seed(t, r.client,
		branch.Op{Branch: "42@feat@add-thing", Title: "Add thing", TrackerType: "fake", IssueID: records[0].ID},
		branch.StatusInProgress)

	runErr := runPr(ctx, r.prDeps(ft, &scriptedPullRequestForm{Open: true}), "42@feat@add-thing")

	t.Run("the title is the issue's and the body closes the tracker number", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runPr: %v", runErr)
		}
		want := fake.PullRequestCreate{Head: "42@feat@add-thing", Base: "master", Title: "Add thing", Body: "Closes #42"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
	})
}
