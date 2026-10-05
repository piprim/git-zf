package issue

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// fakeIssueTracker is a stub tracker.Tracker for issue list tests.
type fakeIssueTracker struct {
	issues []tracker.Issue
	err    error
}

func (f *fakeIssueTracker) ListIssues(_ context.Context) ([]tracker.Issue, error) {
	return f.issues, f.err
}
func (f *fakeIssueTracker) ListStatuses(_ context.Context) ([]string, error) { return nil, nil }
func (f *fakeIssueTracker) UpdateIssueStatus(_ context.Context, _, _ string) error {
	return nil
}

func (f *fakeIssueTracker) AddComment(_ context.Context, _, _ string) error { return nil }
func (f *fakeIssueTracker) IsIssueClosed(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// newListClient returns the client of a fresh repository with no tracked
// branch and no issue.
func newListClient(t *testing.T) *git.Client {
	t.Helper()

	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "Test User"},
		{"config", "user.email", "test@test.com"},
		{"config", "commit.gpgsign", "false"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: io.Discard, Err: io.Discard}, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c
}

func TestBuildIssueRows(t *testing.T) {
	t.Parallel()

	t.Run("tracker path merges remote issues with local branches (partial match)", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)

		// Seed 2 of the 3 tracker issues as tracked branches.
		branchtest.Seed(t, s, branch.Op{Branch: "T-1@feat@first@uuid-t1", Title: "First"}, branch.StatusInProgress)
		branchtest.Seed(t, s, branch.Op{Branch: "T-2@fix@second@uuid-t2", Title: "Second"}, branch.StatusInProgress)

		tk := &fakeIssueTracker{issues: []tracker.Issue{
			{ID: "T-1", Subject: "First", Status: "In Progress"},
			{ID: "T-2", Subject: "Second", Status: "In Progress"},
			{ID: "T-3", Subject: "Third", Status: "New"},
		}}

		infra := issueListInfra{client: s, tracker: tk, stderr: &bytes.Buffer{}}

		rows, err := buildRows(t.Context(), infra, "open")
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		if len(rows) != 3 {
			t.Fatalf("got %d rows, want 3", len(rows))
		}

		bySlug := make(map[string]issuepkg.Row)
		for _, r := range rows {
			bySlug[r.IssueSlug] = r
		}

		if bySlug["T-1"].Branch == nil {
			t.Error("T-1: Branch should be non-nil")
		}
		if bySlug["T-2"].Branch == nil {
			t.Error("T-2: Branch should be non-nil")
		}
		if bySlug["T-3"].Branch != nil {
			t.Error("T-3: Branch should be nil")
		}
		for slug, r := range bySlug {
			if r.TrackerStatus == nil {
				t.Errorf("%s: TrackerStatus should be non-nil", slug)
			}
		}
	})

	t.Run("local fallback when tracker is nil returns the tracked branches without TrackerStatus", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		branchtest.Seed(t, s, branch.Op{Branch: "L-1@feat@local-only@uuid-l1", Title: "Local only"}, branch.StatusInProgress)

		infra := issueListInfra{client: s, tracker: nil, stderr: &bytes.Buffer{}}

		rows, err := buildRows(t.Context(), infra, "open")
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0].TrackerStatus != nil {
			t.Error("TrackerStatus should be nil in local mode")
		}
		if rows[0].Branch == nil {
			t.Error("Branch should be non-nil in local mode")
		}
	})

	t.Run("tracker error falls back to the repository and logs a warning", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		branchtest.Seed(t, s, branch.Op{Branch: "F-1@feat@fallback@uuid-f1", Title: "Fallback"}, branch.StatusInProgress)

		tk := &fakeIssueTracker{err: errors.New("network error")}
		var stderr bytes.Buffer
		infra := issueListInfra{client: s, tracker: tk, stderr: &stderr}

		rows, err := buildRows(t.Context(), infra, "open")
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want 1", len(rows))
		}
		if rows[0].TrackerStatus != nil {
			t.Error("TrackerStatus should be nil after fallback")
		}
		if !bytes.Contains(stderr.Bytes(), []byte("warning")) {
			t.Errorf("expected warning on stderr, got %q", stderr.String())
		}
	})

	t.Run("tracker path populates the Project field from each issue", func(t *testing.T) {
		t.Parallel()

		tk := &fakeIssueTracker{issues: []tracker.Issue{
			{ID: "1", Subject: "a", Status: "open", Project: "octo/cat"},
			{ID: "2", Subject: "b", Status: "open", Project: "octo/dog"},
		}}

		db := newListClient(t)

		infra := issueListInfra{client: db, tracker: tk, stderr: io.Discard}

		rows, err := buildFromTracker(t.Context(), infra)
		if err != nil {
			t.Fatalf("buildFromTracker: %v", err)
		}
		if len(rows) != 2 {
			t.Fatalf("got %d rows, want 2", len(rows))
		}
		if rows[0].Project != "octo/cat" || rows[1].Project != "octo/dog" {
			t.Errorf("projects = %q,%q want octo/cat,octo/dog", rows[0].Project, rows[1].Project)
		}
	})
}

func TestRunIssueList(t *testing.T) {
	t.Parallel()

	t.Run("json output includes issue slug and N.A. for nil TrackerStatus", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		branchtest.Seed(t, s, branch.Op{Branch: "J-1@feat@json-issue@uuid-j1", Title: "JSON issue"}, branch.StatusInProgress)

		infra := issueListInfra{client: s, tracker: nil, stderr: &bytes.Buffer{}}
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, infra, issueListFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		if !strings.Contains(buf.String(), "J-1") {
			t.Errorf("JSON output missing J-1, got: %s", buf.String())
		}
		if !strings.Contains(buf.String(), "N.A.") {
			t.Errorf("JSON output should contain N.A. for nil TrackerStatus; got: %s", buf.String())
		}
	})

	t.Run("stdout output includes issue slug and N.A. for nil TrackerStatus", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		branchtest.Seed(t, s, branch.Op{Branch: "S-1@feat@stdout@uuid-s1b", Title: "Stdout issue"}, branch.StatusInProgress)

		infra := issueListInfra{client: s, tracker: nil, stderr: &bytes.Buffer{}}
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}

		if !strings.Contains(buf.String(), "S-1") {
			t.Errorf("stdout output missing S-1, got: %s", buf.String())
		}
		if !strings.Contains(buf.String(), "N.A.") {
			t.Errorf("stdout output should contain N.A. for nil TrackerStatus; got: %s", buf.String())
		}
	})

	t.Run("stdout output says 'No issues found' for a repository with nothing tracked", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		infra := issueListInfra{client: s, tracker: nil, stderr: &bytes.Buffer{}}
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(buf.String(), "No issues found") {
			t.Errorf("expected 'No issues found', got: %s", buf.String())
		}
	})

	t.Run("stdout output uses ∅ for tracker issue with no local branch", func(t *testing.T) {
		t.Parallel()

		s := newListClient(t)
		tk := &fakeIssueTracker{issues: []tracker.Issue{
			{ID: "NB-1", Subject: "No branch yet", Status: "New"},
		}}
		infra := issueListInfra{client: s, tracker: tk, stderr: &bytes.Buffer{}}
		var buf bytes.Buffer
		if err := runList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if !strings.Contains(buf.String(), "∅") {
			t.Errorf("expected ∅ for missing branch, got: %s", buf.String())
		}
	})
}
