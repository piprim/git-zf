package tracker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

type stubTracker struct{}

func (s *stubTracker) ListIssues(_ context.Context) ([]tracker.Issue, error)   { return nil, nil }
func (s *stubTracker) ListStatuses(_ context.Context) ([]string, error)        { return nil, nil }
func (s *stubTracker) UpdateIssueStatus(_ context.Context, _, _ string) error  { return nil }
func (s *stubTracker) AddComment(_ context.Context, _, _ string) error         { return nil }
func (s *stubTracker) IsIssueClosed(_ context.Context, _ string) (bool, error) { return false, nil }

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
