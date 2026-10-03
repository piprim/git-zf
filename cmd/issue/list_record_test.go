package issue

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
)

func TestBuildRows_RepoIssues(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	s := openTestIssueStore(t)

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

	// One repo issue has a branch; one store row is a legacy issue with no record.
	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: started.ShortID(), Title: "Started", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: started.ShortID() + "@feat@started", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: "JIRA-7", Title: "Legacy", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "JIRA-7@feat@legacy", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{store: s, stderr: &bytes.Buffer{}, client: rig.client}

	bySlug := func(t *testing.T, status string) map[string]store.IssueRow {
		t.Helper()

		rows, err := buildRows(ctx, infra, status)
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		out := make(map[string]store.IssueRow, len(rows))
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

	t.Run("a legacy store row without a record is kept unchanged", func(t *testing.T) {
		row := bySlug(t, "")["JIRA-7"]
		if row.Branch == nil || row.State != "" || row.TrackerStatus != nil {
			t.Errorf("row = %+v", row)
		}
	})

	t.Run("status open hides the closed repo issue", func(t *testing.T) {
		rows := bySlug(t, "open")
		if _, ok := rows[done.ShortID()]; ok {
			t.Errorf("closed issue listed under open: %+v", rows)
		}
		if _, ok := rows[backlog.ShortID()]; !ok {
			t.Errorf("backlog issue missing under open: %+v", rows)
		}
	})

	t.Run("status closed lists the closed repo issue and not the backlog", func(t *testing.T) {
		rows := bySlug(t, "closed")
		if _, ok := rows[done.ShortID()]; !ok {
			t.Errorf("closed issue missing: %+v", rows)
		}
		if _, ok := rows[backlog.ShortID()]; ok {
			t.Errorf("backlog issue listed under closed: %+v", rows)
		}
	})

	t.Run("json output carries labels and state", func(t *testing.T) {
		var buf bytes.Buffer
		if err := runList(ctx, &buf, infra, issueListFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		var rows []store.IssueRow
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

	t.Run("stdout table shows labels next to the title and the issue status header", func(t *testing.T) {
		var buf bytes.Buffer
		if err := runList(ctx, &buf, infra, issueListFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		out := buf.String()
		for _, want := range []string{"Started [ui]", "ISSUE STATUS", "Backlog"} {
			if !strings.Contains(out, want) {
				t.Errorf("table misses %q:\n%s", want, out)
			}
		}
	})
}
