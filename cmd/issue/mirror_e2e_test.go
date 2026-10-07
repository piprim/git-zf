package issue

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

// newMirrorRig is a recordRig with the mirror on, backed by a fake tracker.
func newMirrorRig(t *testing.T) (*recordRig, *fake.Tracker, *issuepkg.Mirror) {
	t.Helper()

	rig := newRecordRig(t, "alice", "")
	rig.cfg.IssueTracker = config.IssueTrackerConfig{
		Type: "fake", Mirror: true,
		Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "piprim/git-zf"}},
	}
	ft := &fake.Tracker{}

	return rig, ft, mirrorOf(rig.cfg, ft)
}

func fromTracker(id, title, status string) tracker.Issue {
	return tracker.Issue{
		TrackerType: "fake", ID: id, Subject: title, Status: status,
		CreatedAt: time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC),
	}
}

func TestMirrorOf(t *testing.T) {
	t.Parallel()

	on := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "fake", Mirror: true, Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a/b"}},
	}}
	off := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "fake", Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a/b"}},
	}}

	t.Run("mirror on gives a mirror named by the tracker type and the near slug", func(t *testing.T) {
		t.Parallel()

		m := mirrorOf(on, &fake.Tracker{})
		if m == nil || m.Type != "fake" || m.Project != "zf" {
			t.Errorf("mirror = %+v", m)
		}
	})
	t.Run("mirror off gives nil", func(t *testing.T) {
		t.Parallel()

		if m := mirrorOf(off, &fake.Tracker{}); m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
	t.Run("no tracker gives nil", func(t *testing.T) {
		t.Parallel()

		if m := mirrorOf(on, nil); m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
}

// Review Focus 5.
func TestOpenMirror_TrackerCannotBeBuilt(t *testing.T) {
	t.Parallel()

	cfg := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "no-such-tracker", Mirror: true, Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "a/b"}},
	}}
	var errW bytes.Buffer

	m := openMirror(cfg, &errW)

	t.Run("the mirror is off", func(t *testing.T) {
		if m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
	t.Run("one warning says why", func(t *testing.T) {
		if got := errW.String(); strings.Count(got, "\n") != 1 || !strings.Contains(got, "warning: issue mirror off") {
			t.Errorf("stderr = %q", got)
		}
	})
}

func TestMirror_NewCreatesTheTrackerIssue(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()

	err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug", Description: "Steps"}, nil, m)
	rec := rig.onlyRecord(t)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the tracker issue is created at once", func(t *testing.T) {
		want := []fake.Create{{Title: "Local bug", Description: "Steps"}}
		if !slices.Equal(ft.RecordedCreates, want) {
			t.Errorf("creates = %+v", ft.RecordedCreates)
		}
	})
	t.Run("the record is linked to it", func(t *testing.T) {
		if rec.Tracker == nil || rec.Tracker.ID != "1" || rec.Tracker.Born {
			t.Errorf("tracker = %+v", rec.Tracker)
		}
	})
}

func TestMirror_NewSurvivesATrackerFailure(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ft.ListErr = errors.New("connection refused")

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, m)

	t.Run("the issue is created locally", func(t *testing.T) {
		if err != nil || rig.onlyRecord(t).Title != "Local bug" {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("the failure is a warning", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "warning: tracker mirror: ") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestMirror_ListReadsTheRepository(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "In Progress")}
	ft.Issues = []tracker.Issue{{TrackerType: "fake", ID: "99", Subject: "Assigned to me"}}
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	local := rig.onlyRecord(t)

	infra := issueListInfra{tracker: ft, mirror: m, stderr: rig.stderr, client: rig.client}
	rows, err := buildRows(ctx, infra, "")
	bySlug := make(map[string]issuepkg.Row, len(rows))
	for _, r := range rows {
		bySlug[r.IssueSlug] = r
	}

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
	})
	t.Run("the tracker's project issue is listed under its number with its status name", func(t *testing.T) {
		row, ok := bySlug["42"]
		if !ok || row.Title != "From tracker" || row.TrackerStatus == nil || *row.TrackerStatus != "In Progress" {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
	})
	t.Run("the repo-born issue shows its short hash and its new tracker number", func(t *testing.T) {
		row, ok := bySlug[local.ShortID()]
		if !ok || row.TrackerID != "1" {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
		if cell := issuepkg.RowCells(&row, false)[0]; cell != local.ShortID()+" (#1)" {
			t.Errorf("ID cell = %q", cell)
		}
	})
	t.Run("the assigned-to-me listing is not used", func(t *testing.T) {
		if _, ok := bySlug["99"]; ok || len(rows) != 2 {
			t.Errorf("rows = %+v", rows)
		}
	})
}

func TestMirror_ListWithTheMirrorOffIsUnchanged(t *testing.T) {
	t.Parallel()

	rig, ft, _ := newMirrorRig(t)
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	ft.Issues = []tracker.Issue{{TrackerType: "fake", ID: "99", Subject: "Assigned to me", Status: "open"}}

	rows, err := buildRows(t.Context(), issueListInfra{tracker: ft, stderr: rig.stderr, client: rig.client}, "")

	t.Run("only the assigned-to-me listing is shown and nothing is imported", func(t *testing.T) {
		if err != nil || len(rows) != 1 || rows[0].IssueSlug != "99" || ft.ListProjectCalls != 0 {
			t.Errorf("rows = %+v, err = %v, project listings = %d", rows, err, ft.ListProjectCalls)
		}
	})
}

func TestMirror_CloseByIDClosesTheTrackerIssue(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Duplicate"}, nil, m); err != nil {
		t.Fatalf("runNew: %v", err)
	}

	err := runCloseByID(ctx, rig.client, rig.onlyRecord(t).ID, m)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runCloseByID: %v", err)
		}
	})
	t.Run("the tracker issue is closed at once", func(t *testing.T) {
		if !slices.Equal(ft.RecordedOpens, []fake.Open{{IssueID: "1", Open: false}}) {
			t.Errorf("tracker writes = %+v", ft.RecordedOpens)
		}
	})
}

func TestMirror_SyncPrintsTheCounts(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rig.stdout.Reset()

	err := runSync(ctx, rig.client, m)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runSync: %v", err)
		}
	})
	t.Run("the mirror line counts imports and exports", func(t *testing.T) {
		want := "Tracker mirror: 1 imported, 1 exported, 0 pulled, 0 pushed.\n"
		if !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})
	t.Run("without a mirror the line is absent", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runSync(ctx, rig.client, nil); err != nil || strings.Contains(rig.stdout.String(), "Tracker mirror") {
			t.Errorf("stdout = %q, err = %v", rig.stdout.String(), err)
		}
	})
}

func TestMirror_ShowByTrackerNumber(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	reconcileIssues(ctx, rig.client, m)
	calls := ft.ListProjectCalls
	rig.stdout.Reset()

	err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{"42"}, false)
	out := rig.stdout.String()

	t.Run("the issue is found by its tracker number", func(t *testing.T) {
		if err != nil || !strings.HasPrefix(out, "42  From tracker\n") {
			t.Errorf("stdout = %q, err = %v", out, err)
		}
	})
	t.Run("the link is printed", func(t *testing.T) {
		if !strings.Contains(out, "Tracker: fake zf #42\n") {
			t.Errorf("stdout = %q", out)
		}
	})
	t.Run("show does not call the tracker", func(t *testing.T) {
		if ft.ListProjectCalls != calls {
			t.Errorf("project listings = %d, want %d", ft.ListProjectCalls, calls)
		}
	})
}
