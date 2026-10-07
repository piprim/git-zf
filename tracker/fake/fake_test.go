package fake

import (
	"errors"
	"testing"

	"github.com/piprim/git-zf/tracker"
)

var _ tracker.Tracker = (*Tracker)(nil)

func TestFake_Mirror(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("a created issue takes the next free number", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}, {ID: "2"}, {ID: "x"}}}
		ft.CloseIssue("2")
		if iss, err := ft.CreateIssue(ctx, "T", ""); err != nil || iss.ID != "3" {
			t.Errorf("CreateIssue = %+v, %v, want ID 3", iss, err)
		}
	})

	t.Run("a created issue is numbered, recorded and listed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{}
		iss, err := ft.CreateIssue(ctx, "Bug", "Steps")
		if err != nil || iss.ID != "1" || iss.Status != "open" {
			t.Fatalf("CreateIssue = %+v, %v", iss, err)
		}
		if len(ft.RecordedCreates) != 1 || ft.RecordedCreates[0] != (Create{Title: "Bug", Description: "Steps"}) {
			t.Errorf("RecordedCreates = %+v", ft.RecordedCreates)
		}
		listed, _ := ft.ListProjectIssues(ctx)
		if len(listed) != 1 || listed[0].ID != "1" || ft.ListProjectCalls != 1 {
			t.Errorf("listed = %+v, calls = %d", listed, ft.ListProjectCalls)
		}
	})

	t.Run("closing unlists the issue and reopening lists it again", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "42", Subject: "A"}}}
		if err := ft.SetIssueOpen(ctx, "42", false); err != nil {
			t.Fatalf("SetIssueOpen: %v", err)
		}
		listed, _ := ft.ListProjectIssues(ctx)
		closed, _ := ft.IsIssueClosed(ctx, "42")
		if len(listed) != 0 || !closed {
			t.Errorf("after close: listed = %+v, closed = %v", listed, closed)
		}

		ft.ReopenIssue("42")
		listed, _ = ft.ListProjectIssues(ctx)
		closed, _ = ft.IsIssueClosed(ctx, "42")
		if len(listed) != 1 || closed {
			t.Errorf("after reopen: listed = %+v, closed = %v", listed, closed)
		}
	})

	t.Run("SetIssueOpen is recorded, CloseIssue is not", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "42"}, {ID: "43"}}}
		_ = ft.SetIssueOpen(ctx, "42", false)
		ft.CloseIssue("43")
		if len(ft.RecordedOpens) != 1 || ft.RecordedOpens[0] != (Open{IssueID: "42"}) {
			t.Errorf("RecordedOpens = %+v", ft.RecordedOpens)
		}
	})

	t.Run("a closing status closes the issue, another one renames its status", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{
			ProjectIssues:   []tracker.Issue{{ID: "42", Status: "New"}, {ID: "43", Status: "New"}},
			ClosingStatuses: []string{"Closed"},
		}
		_ = ft.UpdateIssueStatus(ctx, "42", "Closed")
		_ = ft.UpdateIssueStatus(ctx, "43", "In Progress")

		listed, _ := ft.ListProjectIssues(ctx)
		if len(listed) != 1 || listed[0].ID != "43" || listed[0].Status != "In Progress" {
			t.Errorf("listed = %+v", listed)
		}
	})

	t.Run("ListErr and CreateErr are returned", func(t *testing.T) {
		t.Parallel()

		boom := errors.New("boom")
		ft := &Tracker{ListErr: boom, CreateErr: boom}
		if _, err := ft.ListProjectIssues(ctx); !errors.Is(err, boom) {
			t.Errorf("ListProjectIssues err = %v", err)
		}
		if _, err := ft.CreateIssue(ctx, "x", ""); !errors.Is(err, boom) {
			t.Errorf("CreateIssue err = %v", err)
		}
	})
}
