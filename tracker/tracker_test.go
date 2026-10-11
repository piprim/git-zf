package tracker_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

type stubTracker struct{}

func (s *stubTracker) ListIssues(_ context.Context) ([]tracker.Issue, error)  { return nil, nil }
func (s *stubTracker) ListStatuses(_ context.Context) ([]string, error)       { return nil, nil }
func (s *stubTracker) UpdateIssueStatus(_ context.Context, _, _ string) error { return nil }
func (s *stubTracker) AddComment(_ context.Context, _, _ string) (tracker.Comment, error) {
	return tracker.Comment{}, nil
}
func (s *stubTracker) GetIssue(_ context.Context, _ string) (tracker.Issue, error) {
	return tracker.Issue{}, nil
}
func (s *stubTracker) UpdateIssue(_ context.Context, _, _, _ string) error     { return nil }
func (s *stubTracker) SetLabels(_ context.Context, _ string, _ []string) error { return nil }
func (s *stubTracker) ListLabels(_ context.Context) ([]string, error)          { return nil, nil }
func (s *stubTracker) ListComments(_ context.Context, _ string) ([]tracker.Comment, error) {
	return nil, nil
}
func (s *stubTracker) IsIssueClosed(_ context.Context, _ string) (bool, error) { return false, nil }

func TestIssueClean(t *testing.T) {
	got := tracker.Issue{
		ID: "7", Subject: "Fix\x1b[31m\nlogin", Description: "a\r\n\tb", Status: "open\x07", Project: "o/r",
	}.Clean()

	t.Run("the subject is one line without escapes", func(t *testing.T) {
		if got.Subject != "Fix[31m login" {
			t.Errorf("Subject = %q", got.Subject)
		}
	})
	t.Run("the description keeps newlines and tabs", func(t *testing.T) {
		if got.Description != "a\n\tb" {
			t.Errorf("Description = %q", got.Description)
		}
	})
	t.Run("the status loses its control character", func(t *testing.T) {
		if got.Status != "open" {
			t.Errorf("Status = %q", got.Status)
		}
	})
	t.Run("the other fields are untouched", func(t *testing.T) {
		if got.ID != "7" || got.Project != "o/r" {
			t.Errorf("got %+v", got)
		}
	})
}

func TestNew(t *testing.T) {
	// Register is init-time only and not synchronised, so this test stays
	// sequential: the first subtest registers, the second reads.
	t.Run("registered type returns a non-nil tracker", func(t *testing.T) {

		const key = "stub-test-register"
		tracker.Register(key, func(_ config.IssueTrackerConfig) (tracker.Tracker, error) {
			return &stubTracker{}, nil
		})

		tr, err := tracker.New(config.IssueTrackerConfig{Type: key})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if tr == nil {
			t.Error("New returned nil tracker")
		}
	})

	t.Run("unknown type returns error mentioning the type name", func(t *testing.T) {
		_, err := tracker.New(config.IssueTrackerConfig{Type: "no-such-adapter-xyz"})
		if err == nil {
			t.Fatal("expected error for unknown type, got nil")
		}
		if !strings.Contains(err.Error(), "no-such-adapter-xyz") {
			t.Errorf("error should mention the unknown type, got: %v", err)
		}
	})
}

func (s *stubTracker) ListProjectIssues(_ context.Context) ([]tracker.Issue, error) { return nil, nil }
func (s *stubTracker) CreateIssue(_ context.Context, _, _ string) (tracker.Issue, error) {
	return tracker.Issue{}, nil
}
func (s *stubTracker) SetIssueOpen(_ context.Context, _ string, _ bool) error { return nil }

func TestIssueClean_Labels(t *testing.T) {
	t.Parallel()

	t.Run("labels lose their control characters", func(t *testing.T) {
		t.Parallel()

		got := tracker.Issue{Labels: []string{"a\tb", "ok"}}.Clean().Labels
		if !slices.Equal(got, []string{"a b", "ok"}) {
			t.Errorf("Labels = %q", got)
		}
	})
}

func TestCommentClean(t *testing.T) {
	t.Parallel()

	t.Run("author and ID are one line, the body keeps newlines", func(t *testing.T) {
		t.Parallel()

		got := tracker.Comment{ID: "7\x07", Author: "bob\n", Body: "a\nb\x1b[0m"}.Clean()
		if got.ID != "7" || got.Author != "bob " || got.Body != "a\nb[0m" {
			t.Errorf("Clean = %+v", got)
		}
	})
}

type singleLabelTracker struct {
	tracker.Tracker
	single bool
}

func (s singleLabelTracker) SingleLabel() bool { return s.single }

func TestSingleLabel(t *testing.T) {
	t.Parallel()

	t.Run("a tracker without the method is not single-label", func(t *testing.T) {
		t.Parallel()

		if tracker.SingleLabel(nil) || tracker.SingleLabel(struct{ tracker.Tracker }{}) {
			t.Error("want false")
		}
	})
	t.Run("a tracker with the method answers for itself", func(t *testing.T) {
		t.Parallel()

		if !tracker.SingleLabel(singleLabelTracker{single: true}) || tracker.SingleLabel(singleLabelTracker{single: false}) {
			t.Error("want the method's answer")
		}
	})
}
