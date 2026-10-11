package fake

import (
	"errors"
	"slices"
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

func TestFake_Content(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("every mutation moves UpdatedAt forward", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1", Subject: "T"}}}
		before, _ := ft.GetIssue(ctx, "1")
		_ = ft.UpdateIssue(ctx, "1", "T2", "D")
		mid, _ := ft.GetIssue(ctx, "1")
		ft.Edit("1", func(iss *tracker.Issue) { iss.Subject = "T3" })
		after, _ := ft.GetIssue(ctx, "1")
		if !before.UpdatedAt.Before(mid.UpdatedAt) || !mid.UpdatedAt.Before(after.UpdatedAt) || after.Subject != "T3" {
			t.Errorf("UpdatedAt %v, %v, %v; subject %q", before.UpdatedAt, mid.UpdatedAt, after.UpdatedAt, after.Subject)
		}
	})

	t.Run("GetIssue of a closed issue reports Closed and the status closed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1", Status: "open"}}}
		ft.CloseIssue("1")
		if iss, err := ft.GetIssue(ctx, "1"); err != nil || !iss.Closed || iss.Status != "closed" {
			t.Errorf("GetIssue = %+v, %v", iss, err)
		}
	})

	t.Run("GetIssue honours Unknown and Errors like IsIssueClosed", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{Unknown: map[string]bool{"9": true}, Errors: map[string]error{"8": errors.New("boom")}}
		if _, err := ft.GetIssue(ctx, "9"); !errors.Is(err, tracker.ErrIssueNotFound) {
			t.Errorf("unknown: err = %v", err)
		}
		if _, err := ft.GetIssue(ctx, "8"); err == nil || err.Error() != "boom" {
			t.Errorf("errors: err = %v", err)
		}
	})

	t.Run("SetLabels records the set, creates the missing names and lists them", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}}, Labels: []string{"bug"}}
		if err := ft.SetLabels(ctx, "1", []string{"bug", "urgent"}); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		iss, _ := ft.GetIssue(ctx, "1")
		names, _ := ft.ListLabels(ctx)
		if !slices.Equal(iss.Labels, []string{"bug", "urgent"}) || !slices.Equal(names, []string{"bug", "urgent"}) ||
			len(ft.RecordedLabelSets) != 1 || !slices.Equal(ft.RecordedLabelSets[0].Labels, []string{"bug", "urgent"}) {
			t.Errorf("labels = %q, catalogue = %q, recorded = %+v", iss.Labels, names, ft.RecordedLabelSets)
		}
	})

	t.Run("AddComment returns the comment with an ID, lists it and records it", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}}}
		c, err := ft.AddComment(ctx, "1", "Hello")
		listed, _ := ft.ListComments(ctx, "1")
		if err != nil || c.ID != "c1" || c.Body != "Hello" || len(listed) != 1 || listed[0].ID != "c1" ||
			len(ft.RecordedComments) != 1 || ft.RecordedComments[0].ID != "c1" {
			t.Errorf("comment = %+v, err = %v, listed = %+v, recorded = %+v", c, err, listed, ft.RecordedComments)
		}
	})

	t.Run("a web comment is listed but not recorded", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{ProjectIssues: []tracker.Issue{{ID: "1"}}}
		c := ft.WebComment("1", "bob", "From the web")
		listed, _ := ft.ListComments(ctx, "1")
		if c.Author != "bob" || len(listed) != 1 || len(ft.RecordedComments) != 0 {
			t.Errorf("comment = %+v, listed = %+v, recorded = %+v", c, listed, ft.RecordedComments)
		}
	})

	t.Run("with StatusIsLabel the status is the one label and SetLabels sets it", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{StatusIsLabel: true, Statuses: []string{"New", "Closed"}, ClosingStatuses: []string{"Closed"},
			ProjectIssues: []tracker.Issue{{ID: "1", Status: "New"}}}
		listed, _ := ft.ListProjectIssues(ctx)
		names, _ := ft.ListLabels(ctx)
		if !tracker.SingleLabel(ft) || !slices.Equal(listed[0].Labels, []string{"New"}) || !slices.Equal(names, []string{"New", "Closed"}) {
			t.Errorf("single = %v, labels = %q, names = %q", tracker.SingleLabel(ft), listed[0].Labels, names)
		}
		if err := ft.SetLabels(ctx, "1", []string{"Closed"}); err != nil {
			t.Fatalf("SetLabels: %v", err)
		}
		if iss, _ := ft.GetIssue(ctx, "1"); !iss.Closed || !slices.Equal(iss.Labels, []string{"Closed"}) {
			t.Errorf("after: %+v", iss)
		}
	})

	t.Run("with StatusIsLabel a set that is not one status is ErrLabelRule", func(t *testing.T) {
		t.Parallel()

		ft := &Tracker{StatusIsLabel: true, Statuses: []string{"New"}, ProjectIssues: []tracker.Issue{{ID: "1", Status: "New"}}}
		if err := ft.SetLabels(ctx, "1", []string{"New", "urgent"}); !errors.Is(err, tracker.ErrLabelRule) {
			t.Errorf("two names: err = %v", err)
		}
		if err := ft.SetLabels(ctx, "1", []string{"urgent"}); !errors.Is(err, tracker.ErrLabelRule) {
			t.Errorf("not a status: err = %v", err)
		}
	})
}
