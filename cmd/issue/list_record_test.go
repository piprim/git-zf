package issue

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

func TestBuildRows_RepoIssues(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()

	started, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Started", BranchType: "feat", Labels: []string{"ui"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backlog, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Backlog", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	done, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Done", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Append(ctx, rig.client, done.ID, &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// One repo issue has a branch; one tracked branch belongs to an issue with no record.
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: started.ShortID() + "@feat@started", Title: "Started"}, branch.StatusInProgress)
	branchtest.Seed(t, rig.client, branch.Op{Branch: "JIRA-7@feat@legacy", Title: "Legacy"}, branch.StatusInProgress)
	// A branch whose slug is not the issue's display ID, tied to it by the full ID.
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: "other-slug@fix@renamed", Title: "Renamed", IssueID: done.ID}, branch.StatusMerged)

	infra := issueListInfra{stderr: &bytes.Buffer{}, client: rig.client}

	bySlug := func(t *testing.T, status string) map[string]issuepkg.Row {
		t.Helper()

		rows, err := buildRows(ctx, infra, status)
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		out := make(map[string]issuepkg.Row, len(rows))
		for _, r := range rows {
			out[r.IssueSlug] = r
		}

		return out
	}

	t.Run("no filter lists branch rows and every repo issue once", func(t *testing.T) {
		rows := bySlug(t, "")
		if len(rows) != 4 {
			t.Fatalf("want 4 rows, got %d: %+v", len(rows), rows)
		}
	})

	t.Run("a repo issue without a branch has its creation date", func(t *testing.T) {
		rows := bySlug(t, "")
		row := rows[backlog.ShortID()]
		if row.Branch != nil || !row.CreatedAt.Equal(backlog.CreatedAt) || issuepkg.CreatedCell(&row) == "∅" {
			t.Errorf("row = %+v", row)
		}
	})

	t.Run("a branch joins its issue on the full ID, whatever its slug", func(t *testing.T) {
		rows := bySlug(t, "")
		row, ok := rows["other-slug"]
		if !ok || row.Title != "Done" || row.State != issuepkg.StateClosed {
			t.Errorf("row = %+v", row)
		}
		if _, ok := rows[done.ShortID()]; ok {
			t.Errorf("the joined issue is listed a second time: %+v", rows)
		}
	})

	t.Run("a started repo issue carries its branch, labels and state", func(t *testing.T) {
		row := bySlug(t, "")[started.ShortID()]
		if row.Branch == nil || row.State != issuepkg.StateOpen || !slices.Equal(row.Labels, []string{"ui"}) {
			t.Errorf("row = %+v", row)
		}
		if row.TrackerStatus == nil || *row.TrackerStatus != issuepkg.StateOpen {
			t.Errorf("TrackerStatus = %v", row.TrackerStatus)
		}
	})

	t.Run("a backlog repo issue appears with no branch", func(t *testing.T) {
		row, ok := bySlug(t, "")[backlog.ShortID()]
		if !ok || row.Branch != nil || row.Title != "Backlog" || row.State != issuepkg.StateOpen {
			t.Errorf("row = %+v (present %v)", row, ok)
		}
	})

	t.Run("a tracked branch whose issue has no record is kept unchanged", func(t *testing.T) {
		row := bySlug(t, "")["JIRA-7"]
		if row.Branch == nil || row.State != "" || row.TrackerStatus != nil {
			t.Errorf("row = %+v", row)
		}
	})

	t.Run("status open hides the closed repo issue", func(t *testing.T) {
		rows := bySlug(t, "open")
		if _, ok := rows["other-slug"]; ok {
			t.Errorf("closed issue listed under open: %+v", rows)
		}
		if _, ok := rows[backlog.ShortID()]; !ok {
			t.Errorf("backlog issue missing under open: %+v", rows)
		}
	})

	t.Run("status closed lists the closed repo issue and not the backlog", func(t *testing.T) {
		rows := bySlug(t, "closed")
		if _, ok := rows["other-slug"]; !ok {
			t.Errorf("closed issue missing: %+v", rows)
		}
		if _, ok := rows[backlog.ShortID()]; ok {
			t.Errorf("backlog issue listed under closed: %+v", rows)
		}
	})

	t.Run("json output carries labels and state", func(t *testing.T) {
		var buf bytes.Buffer
		if _, _, err := runList(ctx, &buf, infra, issueListFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		var rows []issuepkg.Row
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		found := false
		for _, r := range rows {
			if r.IssueSlug == started.ShortID() {
				found = r.State == "open" && slices.Equal(r.Labels, []string{"ui"})
			}
		}
		if !found {
			t.Errorf("json = %s", buf.String())
		}
	})

	t.Run("json output has an empty label list, never null, for a row without a repo issue", func(t *testing.T) {
		var buf bytes.Buffer
		if _, _, err := runList(ctx, &buf, infra, issueListFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		if strings.Contains(buf.String(), `"labels":null`) {
			t.Errorf("json = %s", buf.String())
		}
	})

	t.Run("stdout table shows labels next to the title and the issue status header", func(t *testing.T) {
		var buf bytes.Buffer
		if _, _, err := runList(ctx, &buf, infra, issueListFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		out := buf.String()
		for _, want := range []string{"Started [ui]", "ISSUE STATUS", "Backlog"} {
			if !strings.Contains(out, want) {
				t.Errorf("table misses %q:\n%s", want, out)
			}
		}
	})

	t.Run("the dialog renders a row's issue as show does", func(t *testing.T) {
		row := bySlug(t, "")[backlog.ShortID()]
		out, err := renderIssue(ctx, infra, row.IssueSlug)
		if err != nil {
			t.Fatalf("renderIssue: %v", err)
		}
		for _, want := range []string{"Backlog", "ID: " + backlog.ID} {
			if !strings.Contains(out, want) {
				t.Errorf("show misses %q:\n%s", want, out)
			}
		}
	})

	t.Run("a row without a repo issue nor a tracker is an error", func(t *testing.T) {
		row := bySlug(t, "")["JIRA-7"]
		if _, err := renderIssue(ctx, infra, row.IssueSlug); err == nil {
			t.Error("expected an error")
		}
		if _, err := editIssue(ctx, infra, row.IssueSlug, "Legacy", ""); err == nil {
			t.Error("expected an error")
		}
	})

	t.Run("the edit dialog with the same values changes nothing", func(t *testing.T) {
		line, err := editIssue(ctx, infra, backlog.ShortID(), "Backlog", "")
		if err != nil || !strings.Contains(line, "unchanged") {
			t.Errorf("line = %q, err = %v", line, err)
		}
	})

	t.Run("the edit dialog saves the title and description and the row follows", func(t *testing.T) {
		line, err := editIssue(ctx, infra, backlog.ShortID(), " Backlog renamed ", "Now described")
		if err != nil || !strings.Contains(line, "Edited issue "+backlog.ShortID()) {
			t.Fatalf("line = %q, err = %v", line, err)
		}
		rec, err := issuepkg.Load(ctx, rig.client, backlog.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if rec.Title != "Backlog renamed" || rec.Description != "Now described" {
			t.Errorf("record = %+v", rec)
		}
		if row := bySlug(t, "")[backlog.ShortID()]; row.Title != "Backlog renamed" {
			t.Errorf("row = %+v", row)
		}
	})
}

// A started issue is listed with the title of its record, so an `issue edit`
// shows up, and not with the title the branch chain recorded at start.
func TestBuildRows_StartedIssueShowsEditedTitle(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()

	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Old title", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: rec.ShortID() + "@feat@old-title", Title: "Old title", IssueID: rec.ID}, branch.StatusInProgress)
	if err := issuepkg.Append(ctx, rig.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpSetTitle, Value: "New title"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	rows, err := buildRows(ctx, issueListInfra{stderr: &bytes.Buffer{}, client: rig.client}, "")
	if err != nil {
		t.Fatalf("buildRows: %v", err)
	}

	t.Run("the row carries the edited title and still its branch", func(t *testing.T) {
		if len(rows) != 1 || rows[0].Title != "New title" || rows[0].Branch == nil {
			t.Errorf("rows = %+v", rows)
		}
	})
}

// --status filters --stdout and --json on the issue state, as the TUI tab
// does, not on the branch status.
func TestBuildRows_StatusFollowsIssueState(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()

	reopened, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Reopened", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: reopened.ShortID() + "@feat@reopened", Title: "Reopened", IssueID: reopened.ID}, branch.StatusMerged)

	closed, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Closed", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	branchtest.Seed(t, rig.client,
		branch.Op{Branch: closed.ShortID() + "@fix@closed", Title: "Closed", IssueID: closed.ID}, branch.StatusInProgress)
	if err := issuepkg.Append(ctx, rig.client, closed.ID, &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	slugs := func(t *testing.T, status string) []string {
		t.Helper()

		rows, err := buildRows(ctx, issueListInfra{stderr: &bytes.Buffer{}, client: rig.client}, status)
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		out := make([]string, len(rows))
		for i, r := range rows {
			out[i] = r.IssueSlug
		}

		return out
	}

	t.Run("open lists the open issue with a merged branch only", func(t *testing.T) {
		if got := slugs(t, "open"); !slices.Equal(got, []string{reopened.ShortID()}) {
			t.Errorf("open = %v, want [%s]", got, reopened.ShortID())
		}
	})

	t.Run("closed lists the closed issue with an in-progress branch only", func(t *testing.T) {
		if got := slugs(t, "closed"); !slices.Equal(got, []string{closed.ShortID()}) {
			t.Errorf("closed = %v, want [%s]", got, closed.ShortID())
		}
	})

	t.Run("all lists both", func(t *testing.T) {
		if got := slugs(t, "all"); len(got) != 2 {
			t.Errorf("all = %v, want 2 rows", got)
		}
	})
}

func TestRenderIssue_TrackerOnlyRow(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{
		ProjectIssues: []tracker.Issue{{
			TrackerType: "fake", ID: "77", Subject: "From the tracker", Description: "Steps to reproduce",
			Status: "New", Project: "cpro", Labels: []string{"bug"},
		}},
		Comments: map[string][]tracker.Comment{"77": {{ID: "1", Author: "bob", Body: "On it"}}},
	}
	infra := issueListInfra{stderr: &bytes.Buffer{}, client: rig.client, tracker: ft}

	out, err := renderIssue(ctx, infra, "77")

	t.Run("the tracker's issue is shown with its comments", func(t *testing.T) {
		if err != nil {
			t.Fatalf("renderIssue: %v", err)
		}
		for _, want := range []string{"77  From the tracker", "Status: New", "Project: cpro", "Labels: bug", "Steps to reproduce", "bob", "On it"} {
			if !strings.Contains(out, want) {
				t.Errorf("output misses %q:\n%s", want, out)
			}
		}
	})

	t.Run("an issue the tracker does not know is an error", func(t *testing.T) {
		if _, err := renderIssue(ctx, infra, "78"); err == nil {
			t.Error("expected an error")
		}
	})
}

// branch new lists offline: with the Tracker flag off, ListRows neither asks
// the tracker nor reconciles the mirror.
func TestListRows_OfflineNeverAsksTheTracker(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Local", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{{ID: "42", Subject: "Remote", Status: "open"}}}
	deps := issueflow.StartDeps{
		Client: rig.client, Tracker: ft, Mirror: &issuepkg.Mirror{Tracker: ft, Type: "fake", Project: "zf"},
		Flags: issuepkg.IssueStartFlags{Tracker: false},
	}

	rows, err := issueflow.ListRows(ctx, deps, "")

	t.Run("the repo issue is the only row", func(t *testing.T) {
		if err != nil || len(rows) != 1 || rows[0].IssueSlug != rec.ShortID() {
			t.Errorf("rows = %+v, err = %v", rows, err)
		}
	})
	t.Run("the tracker was never listed", func(t *testing.T) {
		if ft.ListCalls != 0 {
			t.Errorf("ListCalls = %d", ft.ListCalls)
		}
	})
	t.Run("nothing was exported", func(t *testing.T) {
		if len(ft.RecordedCreates) != 0 {
			t.Errorf("creates = %+v", ft.RecordedCreates)
		}
	})
}
