package branch

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// scriptedPullRequestForm answers the form with Open, after an optional edit
// of the draft, and keeps what it was shown.
type scriptedPullRequestForm struct {
	Open  bool
	Edit  func(*PullRequestDraft)
	Err   error
	Calls int
	Seen  PullRequestDraft
	Name  string
}

func (s *scriptedPullRequestForm) form() PullRequestForm {
	return func(_ context.Context, branchName string, draft *PullRequestDraft) (bool, error) {
		s.Calls++
		s.Name, s.Seen = branchName, *draft
		if s.Err != nil {
			return false, s.Err
		}
		if s.Edit != nil {
			s.Edit(draft)
		}

		return s.Open, nil
	}
}

func TestOpenPullRequest(t *testing.T) {
	t.Parallel()

	draft := PullRequestDraft{Title: "Add thing", Body: "Closes #1", Base: "master"}

	t.Run("yes opens the pull request with the draft and prints its URL", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{}
		out := &bytes.Buffer{}
		form := &scriptedPullRequestForm{Open: true}
		if err := OpenPullRequest(t.Context(), out, ft, "feat-x", draft, form.form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		want := fake.PullRequestCreate{Head: "feat-x", Base: "master", Title: "Add thing", Body: "Closes #1"}
		if len(ft.RecordedPullRequests) != 1 || ft.RecordedPullRequests[0] != want {
			t.Errorf("RecordedPullRequests = %+v", ft.RecordedPullRequests)
		}
		if form.Name != "feat-x" || form.Seen != draft {
			t.Errorf("form saw %q %+v", form.Name, form.Seen)
		}
		if !strings.Contains(out.String(), "fake://pr/1") {
			t.Errorf("output %q lacks the URL", out.String())
		}
	})

	t.Run("the form's edits are what is opened", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{}
		form := &scriptedPullRequestForm{Open: true, Edit: func(d *PullRequestDraft) { d.Title, d.Base = "Better", "develop" }}
		if err := OpenPullRequest(t.Context(), &bytes.Buffer{}, ft, "feat-x", draft, form.form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		if got := ft.RecordedPullRequests[0]; got.Title != "Better" || got.Base != "develop" {
			t.Errorf("opened %+v", got)
		}
	})

	t.Run("no opens nothing", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{}
		if err := OpenPullRequest(t.Context(), &bytes.Buffer{}, ft, "feat-x", draft, (&scriptedPullRequestForm{}).form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		if len(ft.RecordedPullRequests) != 0 {
			t.Errorf("opened %+v", ft.RecordedPullRequests)
		}
	})

	t.Run("an open pull request skips the form and prints its URL", func(t *testing.T) {
		t.Parallel()
		ft := &fake.Tracker{PullRequests: []tracker.PullRequest{{Number: "7", URL: "fake://pr/7", Head: "feat-x", Base: "master"}}}
		out := &bytes.Buffer{}
		form := &scriptedPullRequestForm{Open: true}
		if err := OpenPullRequest(t.Context(), out, ft, "feat-x", draft, form.form()); err != nil {
			t.Fatalf("OpenPullRequest: %v", err)
		}
		if form.Calls != 0 || len(ft.RecordedPullRequests) != 0 {
			t.Errorf("form calls %d, opened %+v", form.Calls, ft.RecordedPullRequests)
		}
		if !strings.Contains(out.String(), "#7") || !strings.Contains(out.String(), "fake://pr/7") {
			t.Errorf("output %q", out.String())
		}
	})

	t.Run("a form error is returned", func(t *testing.T) {
		t.Parallel()
		form := &scriptedPullRequestForm{Err: errors.New("boom")}
		if err := OpenPullRequest(t.Context(), &bytes.Buffer{}, &fake.Tracker{}, "feat-x", draft, form.form()); err == nil {
			t.Error("want an error")
		}
	})
}

func TestPullRequestDraftFor(t *testing.T) {
	t.Parallel()

	t.Run("an untracked branch: its name, the configured base, no body", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t)
		r.addLocalBranch(t, "feat-x")
		got := PullRequestDraftFor(t.Context(), r.client, "master", "feat-x")
		if got != (PullRequestDraft{Title: "feat-x", Base: "master"}) {
			t.Errorf("draft = %+v", got)
		}
	})

	t.Run("a tracked branch: the issue title", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t)
		r.addLocalBranch(t, "ABC-1@feat@add-thing")
		branchtest.Seed(t, r.client, branch.Op{Branch: "ABC-1@feat@add-thing", Title: "Add thing"}, branch.StatusInProgress)
		got := PullRequestDraftFor(t.Context(), r.client, "master", "ABC-1@feat@add-thing")
		if got.Title != "Add thing" || got.Base != "master" || got.Body != "" {
			t.Errorf("draft = %+v", got)
		}
	})

	t.Run("without a configured base, the default base branch", func(t *testing.T) {
		t.Parallel()
		r := newMergeRig(t)
		r.addLocalBranch(t, "feat-x")
		if got := PullRequestDraftFor(t.Context(), r.client, "", "feat-x"); got.Base != "master" {
			t.Errorf("draft = %+v", got)
		}
	})
}
