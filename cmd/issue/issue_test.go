package issue

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/piprim/git-zf/store"
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

func (f *fakeIssueTracker) UpdateIssueStatus(_ context.Context, _, _ string) error { return nil }

func openTestIssueStore(t *testing.T) *store.Store {
	t.Helper()

	s, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	return s
}

func TestBuildIssueRows_trackerPath_partialMatch(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)

	// Seed 2 of the 3 tracker issues in the local store.
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "T-1", Title: "First", StatusID: 1},
		&store.Branch{UUID: "uuid-t1", Name: "T-1@feat@first@uuid-t1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "T-2", Title: "Second", StatusID: 1},
		&store.Branch{UUID: "uuid-t2", Name: "T-2@fix@second@uuid-t2", Type: "fix", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tk := &fakeIssueTracker{issues: []tracker.Issue{
		{ID: "T-1", Subject: "First", Status: "In Progress"},
		{ID: "T-2", Subject: "Second", Status: "In Progress"},
		{ID: "T-3", Subject: "Third", Status: "New"},
	}}

	infra := issueListInfra{tracker: tk, store: s, stderr: &bytes.Buffer{}}

	rows, err := buildRows(t.Context(), infra, "open")
	if err != nil {
		t.Fatalf("buildIssueRows: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	// T-1 and T-2 have local branches; T-3 does not.
	bySlug := make(map[string]store.IssueRow)
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
}

func TestBuildIssueRows_localFallback_nilTracker(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "L-1", Title: "Local only", StatusID: 1},
		&store.Branch{UUID: "uuid-l1", Name: "L-1@feat@local-only@uuid-l1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{tracker: nil, store: s, stderr: &bytes.Buffer{}}

	rows, err := buildRows(t.Context(), infra, "open")
	if err != nil {
		t.Fatalf("buildIssueRows: %v", err)
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
}

func TestRunIssueList_json(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "J-1", Title: "JSON issue", StatusID: 1},
		&store.Branch{UUID: "uuid-j1", Name: "J-1@feat@json-issue@uuid-j1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{tracker: nil, store: s, stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, infra, issueListFlags{jsonOut: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "J-1") {
		t.Errorf("JSON output missing J-1, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "N.A.") {
		t.Errorf("JSON output should contain N.A. for nil TrackerStatus; got: %s", buf.String())
	}
}

func TestRunIssueList_stdout(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "S-1", Title: "Stdout issue", StatusID: 1},
		&store.Branch{UUID: "uuid-s1b", Name: "S-1@feat@stdout@uuid-s1b", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{tracker: nil, store: s, stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "S-1") {
		t.Errorf("stdout output missing S-1, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "N.A.") {
		t.Errorf("stdout output should contain N.A. for nil TrackerStatus; got: %s", buf.String())
	}
}

func TestRunIssueList_stdout_emptyStore(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	infra := issueListInfra{tracker: nil, store: s, stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "No issues found") {
		t.Errorf("expected 'No issues found', got: %s", buf.String())
	}
}

func TestRunIssueList_stdout_trackerIssueNoLocalBranch(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)
	tk := &fakeIssueTracker{issues: []tracker.Issue{
		{ID: "NB-1", Subject: "No branch yet", Status: "New"},
	}}
	infra := issueListInfra{tracker: tk, store: s, stderr: &bytes.Buffer{}}
	var buf bytes.Buffer
	if err := runList(t.Context(), &buf, infra, issueListFlags{stdout: true}); err != nil {
		t.Fatalf("runIssueList: %v", err)
	}

	if !strings.Contains(buf.String(), "∅") {
		t.Errorf("expected ∅ for missing branch, got: %s", buf.String())
	}
}

func TestBuildIssueRows_trackerError_fallback(t *testing.T) {
	t.Parallel()

	s := openTestIssueStore(t)

	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "F-1", Title: "Fallback", StatusID: 1},
		&store.Branch{UUID: "uuid-f1", Name: "F-1@feat@fallback@uuid-f1", Type: "feat", StatusID: 1},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	tk := &fakeIssueTracker{err: errors.New("network error")}
	var stderr bytes.Buffer
	infra := issueListInfra{tracker: tk, store: s, stderr: &stderr}

	rows, err := buildRows(t.Context(), infra, "open")
	if err != nil {
		t.Fatalf("buildIssueRows: %v", err)
	}
	// Fell back to local store.
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].TrackerStatus != nil {
		t.Error("TrackerStatus should be nil after fallback")
	}
	if !bytes.Contains(stderr.Bytes(), []byte("warning")) {
		t.Errorf("expected warning on stderr, got %q", stderr.String())
	}
}

func TestBuildFromTracker_populatesProject(t *testing.T) {
	t.Parallel()

	tk := &fakeIssueTracker{issues: []tracker.Issue{
		{ID: "1", Subject: "a", Status: "open", Project: "octo/cat"},
		{ID: "2", Subject: "b", Status: "open", Project: "octo/dog"},
	}}

	db := openTestIssueStore(t)
	defer db.Close()

	infra := issueListInfra{tracker: tk, store: db, stderr: io.Discard}

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
}
