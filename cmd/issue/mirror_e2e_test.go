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

// Two clones, one tracker: alice's reconcile exports a shared record; bob,
// who has not fetched since, runs `issue new`. His reconcile must see the
// link, or it imports the new tracker issue as a second record and exports
// the shared record again.
func TestMirror_NewFetchesBeforeReconciling(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	origin := newBareOrigin(t)
	ft := &fake.Tracker{}
	clone := func(user string) (*recordRig, *issuepkg.Mirror) {
		rig := newRecordRig(t, user, origin)
		rig.cfg.IssueTracker = config.IssueTrackerConfig{
			Type: "fake", Mirror: true,
			Projects: []config.TrackerProject{{NearSlug: "zf", FarSlug: "piprim/git-zf"}},
		}

		return rig, mirrorOf(rig.cfg, ft)
	}
	alice, aliceMirror := clone("alice")
	bob, bobMirror := clone("bob")

	shared, err := issuepkg.Create(ctx, alice.client, issuepkg.NewIssue{Title: "Shared", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Push(ctx, alice.client, shared.ID); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := issuepkg.Fetch(ctx, bob.client); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if _, err := aliceMirror.Reconcile(ctx, alice.client); err != nil { // exports Shared as 1, pushes
		t.Fatalf("Reconcile: %v", err)
	}

	runErr := runNew(ctx, bob.client, bob.cfg, issuepkg.NewIssue{Title: "Bob's bug"}, nil, bobMirror)
	records, _, listErr := issuepkg.List(ctx, bob.client)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil || listErr != nil {
			t.Fatalf("runNew: %v, List: %v", runErr, listErr)
		}
	})
	t.Run("each record got one tracker issue", func(t *testing.T) {
		want := []fake.Create{{Title: "Shared"}, {Title: "Bob's bug"}}
		if !slices.Equal(ft.RecordedCreates, want) {
			t.Errorf("creates = %+v", ft.RecordedCreates)
		}
	})
	t.Run("bob has two records, the shared one linked once", func(t *testing.T) {
		if len(records) != 2 {
			t.Fatalf("bob has %d records: %+v", len(records), records)
		}
		for _, rec := range records {
			if rec.Tracker == nil || rec.Tracker.Born || len(rec.DuplicateTrackerIDs) != 0 {
				t.Errorf("record %s: tracker = %+v, duplicates = %v", rec.DisplayID(), rec.Tracker, rec.DuplicateTrackerIDs)
			}
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
