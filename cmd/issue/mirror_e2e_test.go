package issue

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/cmd/issueflow"
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
		Projects: []config.TrackerProject{{Name: "zf", ID: "piprim/git-zf"}},
	}
	ft := &fake.Tracker{}

	return rig, ft, issueflow.MirrorOf(rig.cfg, ft)
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
		Type: "fake", Mirror: true, Projects: []config.TrackerProject{{Name: "zf", ID: "a/b"}},
	}}
	off := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "fake", Projects: []config.TrackerProject{{Name: "zf", ID: "a/b"}},
	}}

	t.Run("mirror on gives a mirror named by the tracker type and the name", func(t *testing.T) {
		t.Parallel()

		m := issueflow.MirrorOf(on, &fake.Tracker{})
		if m == nil || m.Type != "fake" || m.Project != "zf" {
			t.Errorf("mirror = %+v", m)
		}
	})
	t.Run("mirror off gives nil", func(t *testing.T) {
		t.Parallel()

		if m := issueflow.MirrorOf(off, &fake.Tracker{}); m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
	t.Run("no tracker gives nil", func(t *testing.T) {
		t.Parallel()

		if m := issueflow.MirrorOf(on, nil); m != nil {
			t.Errorf("mirror = %+v, want nil", m)
		}
	})
}

// Review Focus 5.
func TestOpenMirror_TrackerCannotBeBuilt(t *testing.T) {
	t.Parallel()

	cfg := &config.AppConfig{IssueTracker: config.IssueTrackerConfig{
		Type: "no-such-tracker", Mirror: true, Projects: []config.TrackerProject{{Name: "zf", ID: "a/b"}},
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
	t.Run("the issue is announced by its tracker number", func(t *testing.T) {
		if want := "Created issue 1: Local bug"; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q, want %q in it", rig.stdout.String(), want)
		}
	})
}

func TestMirror_NewOffersTheTrackerLabels(t *testing.T) {
	t.Parallel()

	t.Run("the form is offered the tracker's labels", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ft.Labels = []string{"bug", "ui"}
		p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "From form", Labels: []string{"ui"}}}

		if err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p, m); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if !slices.Equal(p.LabelOffered.Choices, []string{"bug", "ui"}) || p.LabelOffered.Single {
			t.Errorf("offered = %+v", p.LabelOffered)
		}
		if rec := rig.onlyRecord(t); !slices.Equal(rec.Labels, []string{"ui"}) {
			t.Errorf("labels = %v", rec.Labels)
		}
	})

	t.Run("on a single-label tracker the form is a single pick among the statuses", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ft.StatusIsLabel, ft.Statuses = true, []string{"New", "Resolved"}
		p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "From form", Labels: []string{"New"}}}

		if err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p, m); err != nil {
			t.Fatalf("runNew: %v", err)
		}
		if !p.LabelOffered.Single || !slices.Equal(p.LabelOffered.Choices, []string{"New", "Resolved"}) {
			t.Errorf("offered = %+v", p.LabelOffered)
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
			Projects: []config.TrackerProject{{Name: "zf", ID: "piprim/git-zf"}},
		}

		return rig, issueflow.MirrorOf(rig.cfg, ft)
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

func TestMirror_ListReadsTheRepository(t *testing.T) { // no reconcile: a sync (`r`) is the one tracker call
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "In Progress")}
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
	t.Run("the list never asks the tracker: the repository's issues alone are shown", func(t *testing.T) {
		if _, ok := bySlug["42"]; ok || len(rows) != 1 || ft.ListCalls != 0 {
			t.Errorf("rows = %+v, listings = %d", rows, ft.ListCalls)
		}
	})
	t.Run("the local issue is listed under its own ID until a sync exports it", func(t *testing.T) {
		row, ok := bySlug[local.DisplayID()]
		if !ok || row.TrackerID != "" || row.Title != local.Title {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
	})

	if err := runSync(ctx, rig.client, m); err != nil {
		t.Fatalf("runSync: %v", err)
	}
	rows, err = buildRows(ctx, infra, "")
	if err != nil {
		t.Fatalf("buildRows after sync: %v", err)
	}
	bySlug = make(map[string]issuepkg.Row, len(rows))
	for _, r := range rows {
		bySlug[r.IssueSlug] = r
	}

	t.Run("after a sync the tracker's project issue is listed under its number with its status name", func(t *testing.T) {
		row, ok := bySlug["42"]
		if !ok || row.Title != "From tracker" || row.TrackerStatus == nil || *row.TrackerStatus != "In Progress" {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
	})
	t.Run("after a sync the exported issue is keyed by its new tracker number", func(t *testing.T) {
		row, ok := bySlug["1"]
		if !ok || row.TrackerID != "1" || row.Title != local.Title {
			t.Errorf("row = %+v (found %v)", row, ok)
		}
		if cell := issuepkg.RowCells(&row, false)[0]; cell != "1" {
			t.Errorf("ID cell = %q", cell)
		}
	})
	t.Run("the tracker was listed once, by the sync", func(t *testing.T) {
		if len(rows) != 2 || ft.ListCalls != 1 {
			t.Errorf("rows = %+v, listings = %d", rows, ft.ListCalls)
		}
	})
}

func TestMirror_ListWithTheMirrorOffListsTheTrackerWithoutImporting(t *testing.T) {
	t.Parallel()

	rig, ft, _ := newMirrorRig(t)
	ctx := t.Context()
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "From tracker", "open")}
	if err := runNew(ctx, rig.client, rig.cfg, issuepkg.NewIssue{Title: "Local bug"}, nil, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	local := rig.onlyRecord(t)

	rows, err := buildRows(ctx, issueListInfra{tracker: ft, stderr: rig.stderr, client: rig.client}, "")
	bySlug := make(map[string]issuepkg.Row, len(rows))
	for _, r := range rows {
		bySlug[r.IssueSlug] = r
	}

	t.Run("the tracker's issue and the local issue are both listed", func(t *testing.T) {
		if err != nil || len(rows) != 2 {
			t.Fatalf("rows = %+v, err = %v", rows, err)
		}
		if row, ok := bySlug["42"]; !ok || row.Title != "From tracker" || row.TrackerStatus == nil || *row.TrackerStatus != "open" {
			t.Errorf("tracker row = %+v (found %v)", row, ok)
		}
		if row, ok := bySlug[local.ShortID()]; !ok || row.Title != "Local bug" {
			t.Errorf("local row = %+v (found %v)", row, ok)
		}
	})
	t.Run("nothing is imported or exported", func(t *testing.T) {
		records, _, err := issuepkg.List(ctx, rig.client)
		if err != nil || len(records) != 1 || records[0].Tracker != nil || len(ft.RecordedCreates) != 0 {
			t.Errorf("records = %+v, err = %v, creates = %+v", records, err, ft.RecordedCreates)
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
	issueflow.ReconcileIssues(ctx, rig.client, m)
	calls := ft.ListCalls
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
		if ft.ListCalls != calls {
			t.Errorf("project listings = %d, want %d", ft.ListCalls, calls)
		}
	})
}

// importedRecord imports tracker issue 42 into the rig and returns its record.
func importedRecord(t *testing.T, rig *recordRig, ft *fake.Tracker, m *issuepkg.Mirror) issuepkg.Record {
	t.Helper()

	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "open")}
	if _, err := m.Reconcile(t.Context(), rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	return rig.onlyRecord(t)
}

func TestMirror_EditReachesTheTrackerAtOnce(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	rec := importedRecord(t, rig, ft, m)
	title := "B"

	err := runEdit(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, editFlags{title: &title}, m)
	onTracker, _ := ft.GetIssue(ctx, "42")

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runEdit: %v", err)
		}
	})
	t.Run("the tracker has the new title, with no project listing", func(t *testing.T) {
		if onTracker.Subject != "B" || ft.ListCalls != 1 {
			t.Errorf("tracker title = %q, listings = %d", onTracker.Subject, ft.ListCalls)
		}
	})
	t.Run("the record is settled: a reconcile writes nothing", func(t *testing.T) {
		res, _ := m.Reconcile(ctx, rig.client)
		if res.Pulled+res.Pushed != 0 {
			t.Errorf("res = %+v", res)
		}
	})
}

func TestMirror_CommentReachesTheTrackerAtOnce(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	rec := importedRecord(t, rig, ft, m)

	err := runComment(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, "Posted", m)
	got, _ := issuepkg.Load(ctx, rig.client, rec.ID)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runComment: %v", err)
		}
	})
	t.Run("the comment is posted and linked", func(t *testing.T) {
		if len(ft.RecordedComments) != 1 || ft.RecordedComments[0].Body != "Posted" ||
			len(got.Comments) != 1 || got.Comments[0].TrackerID != ft.RecordedComments[0].ID {
			t.Errorf("posted = %+v, comments = %+v", ft.RecordedComments, got.Comments)
		}
	})
}

func TestMirror_ShowPrintsATrackerComment(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "open")}
	ft.WebComment("42", "bob", "From the web")
	if _, err := m.Reconcile(t.Context(), rig.client); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	rec := rig.onlyRecord(t)

	var out bytes.Buffer
	renderRecord(&out, &rec)

	t.Run("the comment shows its tracker author", func(t *testing.T) {
		if !strings.Contains(out.String(), "bob") || !strings.Contains(out.String(), "From the web") {
			t.Errorf("output = %q", out.String())
		}
	})
}

func TestMirror_EditWithTheTrackerDown(t *testing.T) {
	t.Parallel()

	rig, ft, m := newMirrorRig(t)
	ctx := t.Context()
	rec := importedRecord(t, rig, ft, m)
	ft.GetErr = errors.New("502")
	title := "B"

	err := runEdit(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, editFlags{title: &title}, m)
	got, _ := issuepkg.Load(ctx, rig.client, rec.ID)

	t.Run("the edit is saved and the tracker failure is one warning", func(t *testing.T) {
		if err != nil || got.Title != "B" || !strings.Contains(rig.stderr.String(), "warning") {
			t.Errorf("err = %v, title = %q, stderr = %q", err, got.Title, rig.stderr.String())
		}
	})
}

func TestMirror_LabelPicker(t *testing.T) {
	t.Parallel()

	t.Run("the form is offered the tracker's labels united with the issue's, the issue's as current", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.Labels = []string{"bug", "ui"}
		rec := importedRecord(t, rig, ft, m)
		if err := issuepkg.Append(ctx, rig.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddLabel, Value: "local"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		p := &scriptedRecordPrompter{LabelSet: []string{"local", "ui"}}

		if err := runLabel(ctx, rig.client, p, []string{rec.ID}, m); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if !slices.Equal(p.LabelOffered.Choices, []string{"bug", "local", "ui"}) || !slices.Equal(p.LabelOffered.Current, []string{"local"}) || p.LabelOffered.Single {
			t.Errorf("offered = %+v", p.LabelOffered)
		}
		onTracker, _ := ft.GetIssue(ctx, "42")
		sorted := slices.Clone(onTracker.Labels)
		slices.Sort(sorted)
		if !slices.Equal(sorted, []string{"local", "ui"}) {
			t.Errorf("tracker labels = %q, want local and ui at once", onTracker.Labels)
		}
	})

	t.Run("on a single-label tracker the form is a single pick among the statuses", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.StatusIsLabel, ft.Statuses, ft.ClosingStatuses = true, []string{"New", "Resolved"}, []string{"Closed"}
		ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "New")}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec := rig.onlyRecord(t)
		p := &scriptedRecordPrompter{LabelSet: []string{"Resolved"}}

		if err := runLabel(ctx, rig.client, p, []string{rec.ID}, m); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if !p.LabelOffered.Single || !slices.Equal(p.LabelOffered.Choices, []string{"New", "Resolved"}) {
			t.Errorf("offered = %+v", p.LabelOffered)
		}
		if onTracker, _ := ft.GetIssue(ctx, "42"); onTracker.Status != "Resolved" {
			t.Errorf("tracker status = %q", onTracker.Status)
		}
	})

	t.Run("on a single-label tracker tokens that leave two labels are refused before any op", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.StatusIsLabel, ft.Statuses = true, []string{"New", "Resolved"}
		ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "New")}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec := rig.onlyRecord(t)

		err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID, "+urgent"}, m)
		got, _ := issuepkg.Load(ctx, rig.client, rec.ID)
		if err == nil || !slices.Equal(got.Labels, []string{"New"}) {
			t.Errorf("err = %v, labels = %q", err, got.Labels)
		}
	})

	t.Run("with the tracker down the text form is used and tokens are written unchecked", func(t *testing.T) {
		t.Parallel()

		rig, ft, m := newMirrorRig(t)
		ctx := t.Context()
		ft.StatusIsLabel, ft.Statuses = true, []string{"New"}
		ft.ProjectIssues = []tracker.Issue{fromTracker("42", "A", "New")}
		if _, err := m.Reconcile(ctx, rig.client); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		rec := rig.onlyRecord(t)
		ft.ListLabelsErr, ft.GetErr = errors.New("502"), errors.New("502")

		err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID, "+urgent"}, m)
		got, _ := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil || !slices.Equal(got.Labels, []string{"New", "urgent"}) {
			t.Errorf("err = %v, labels = %q", err, got.Labels)
		}
	})
}
