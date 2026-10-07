package issue

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tracker/fake"
)

var mirrorCreated = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)

func newTestMirror(ft *fake.Tracker) *Mirror {
	return &Mirror{Tracker: ft, Type: "fake", Project: "zf"}
}

func trackerIssue(id, title string) tracker.Issue {
	return tracker.Issue{TrackerType: "fake", ID: id, Subject: title, Status: "open", CreatedAt: mirrorCreated}
}

func mustReconcile(t *testing.T, m *Mirror, c *git.Client) MirrorResult {
	t.Helper()

	res, err := m.Reconcile(t.Context(), c)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	return res
}

// onlyRecord returns the single issue of the repository.
func onlyRecord(t *testing.T, c *git.Client) Record {
	t.Helper()

	records, _, err := List(t.Context(), c)
	if err != nil || len(records) != 1 {
		t.Fatalf("want 1 issue, got %d (%v)", len(records), err)
	}

	return records[0]
}

func opCount(t *testing.T, c *git.Client, id string) int {
	t.Helper()

	commits, err := c.ReadChainCommits(t.Context(), git.IssueRefs, id)
	if err != nil {
		t.Fatalf("ReadChainCommits: %v", err)
	}

	return len(commits)
}

func TestReconcile_NilMirror(t *testing.T) {
	t.Parallel()

	var m *Mirror
	res, err := m.Reconcile(t.Context(), newRepo(t, "alice", ""))

	t.Run("a nil mirror does nothing", func(t *testing.T) {
		if err != nil || res.Imported+res.Exported+res.Pulled+res.Pushed != 0 {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})
}

func TestReconcile_Import(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "From tracker")
	iss.Description, iss.Status = "Body", "In Progress"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)

	res := mustReconcile(t, m, c)
	rec := onlyRecord(t, c)

	t.Run("one issue is imported", func(t *testing.T) {
		if res.Imported != 1 || res.Exported != 0 {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("the record carries the tracker's title, description and date", func(t *testing.T) {
		if rec.Title != "From tracker" || rec.Description != "Body" || !rec.CreatedAt.Equal(mirrorCreated) {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the record is open, linked and born in the tracker", func(t *testing.T) {
		want := TrackerLink{Type: "fake", Project: "zf", ID: "42", Born: true}
		if rec.State != StateOpen || rec.Tracker == nil || *rec.Tracker != want || rec.DisplayID() != "42" {
			t.Errorf("state = %q, tracker = %+v", rec.State, rec.Tracker)
		}
	})
	t.Run("the tracker state and status name are recorded", func(t *testing.T) {
		if rec.TrackerState != StateOpen || rec.TrackerStatus != "In Progress" {
			t.Errorf("TrackerState = %q, TrackerStatus = %q", rec.TrackerState, rec.TrackerStatus)
		}
	})

	before := opCount(t, c, rec.ID)
	again := mustReconcile(t, m, c)

	t.Run("a second run changes nothing", func(t *testing.T) {
		if again.Imported != 0 || opCount(t, c, rec.ID) != before || len(ft.RecordedCreates)+len(ft.RecordedOpens) != 0 {
			t.Errorf("again = %+v, ops %d → %d, tracker writes %d", again, before, opCount(t, c, rec.ID),
				len(ft.RecordedCreates)+len(ft.RecordedOpens))
		}
	})
}

// An import that stopped after the root was published leaves a bare record:
// the next run writes the ops it lacks.
func TestReconcile_ImportHealsABareRoot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "From tracker")
	iss.Description = "Body"
	at := mirrorCreated.Truncate(time.Second)
	payload := fmt.Sprintf(importRootTemplate, at.Format(time.RFC3339), "fake", "zf", "42")
	id, err := c.WriteFixedChainRoot(t.Context(), []byte(payload), OpCreate, at)
	if err != nil {
		t.Fatalf("WriteFixedChainRoot: %v", err)
	}
	if err := c.PublishChainRoot(t.Context(), git.IssueRefs, id, id); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	m := newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{iss}})
	res := mustReconcile(t, m, c)
	rec := onlyRecord(t, c)

	t.Run("the heal counts as an import", func(t *testing.T) {
		if res.Imported != 1 {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("the record has its title and description", func(t *testing.T) {
		if rec.Title != "From tracker" || rec.Description != "Body" || rec.TrackerState != StateOpen {
			t.Errorf("record = %+v", rec)
		}
	})

	before := opCount(t, c, id)
	again := mustReconcile(t, m, c)

	t.Run("a second run changes nothing", func(t *testing.T) {
		if again.Imported != 0 || opCount(t, c, id) != before {
			t.Errorf("again = %+v, ops %d → %d", again, before, opCount(t, c, id))
		}
	})
}

func TestReconcile_ImportRootIsDeterministic(t *testing.T) {
	t.Parallel()

	// The same issue, its date written with sub-seconds in another zone.
	shifted := trackerIssue("42", "From tracker")
	shifted.CreatedAt = mirrorCreated.Add(300 * time.Millisecond).In(time.FixedZone("CEST", 2*3600))

	a, b := newRepo(t, "alice", ""), newRepo(t, "bob", "")
	mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}), a)
	mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{shifted}}), b)

	t.Run("two clones give the issue the same ID", func(t *testing.T) {
		if ida, idb := onlyRecord(t, a).ID, onlyRecord(t, b).ID; ida != idb {
			t.Errorf("IDs differ: %s vs %s", ida, idb)
		}
	})
}

func TestReconcile_Export(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{}
	m := newTestMirror(ft)

	created, err := Create(ctx, c, NewIssue{Title: "Local bug", Description: "Steps", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	closed, err := Create(ctx, c, NewIssue{Title: "Old", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Append(ctx, c, closed.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, m, c)
	rec, _ := Load(ctx, c, created.ID)

	t.Run("the open record is created in the tracker", func(t *testing.T) {
		want := []fake.Create{{Title: "Local bug", Description: "Steps"}}
		if res.Exported != 1 || !slices.Equal(ft.RecordedCreates, want) {
			t.Errorf("res = %+v, creates = %+v", res, ft.RecordedCreates)
		}
	})
	t.Run("the record is linked and keeps its short hash", func(t *testing.T) {
		want := TrackerLink{Type: "fake", Project: "zf", ID: "1"}
		if rec.Tracker == nil || *rec.Tracker != want || rec.DisplayID() != rec.ShortID() {
			t.Errorf("tracker = %+v, display = %q", rec.Tracker, rec.DisplayID())
		}
	})
	t.Run("a record closed before it was exported stays local", func(t *testing.T) {
		if got, _ := Load(ctx, c, closed.ID); got.Tracker != nil {
			t.Errorf("tracker = %+v, want nil", got.Tracker)
		}
	})

	before := opCount(t, c, created.ID)
	again := mustReconcile(t, m, c)

	t.Run("a second run changes nothing", func(t *testing.T) {
		if again.Exported != 0 || again.Imported != 0 || len(ft.RecordedCreates) != 1 || opCount(t, c, created.ID) != before {
			t.Errorf("again = %+v, creates = %d, ops %d → %d", again, len(ft.RecordedCreates), before, opCount(t, c, created.ID))
		}
	})
}

func TestReconcile_StateTable(t *testing.T) {
	t.Parallel()

	const o, cl = StateOpen, StateClosed

	rows := []struct {
		name          string
		l, r, tr      string // last recorded, repo, tracker
		want          string // the state both sides end in
		pulled        int
		pushed        int
		wantTrackerOp []fake.Open
	}{
		{"open open open: nobody moved", o, o, o, o, 0, 0, nil},
		{"open open closed: the tracker closed", o, o, cl, cl, 1, 0, nil},
		{"open closed open: the repo closed", o, cl, o, cl, 0, 1, []fake.Open{{IssueID: "42", Open: false}}},
		{"open closed closed: both closed", o, cl, cl, cl, 0, 0, nil},
		{"closed closed closed: nobody moved", cl, cl, cl, cl, 0, 0, nil},
		{"closed closed open: the tracker reopened", cl, cl, o, o, 1, 0, nil},
		{"closed open closed: the repo reopened", cl, o, cl, o, 0, 1, []fake.Open{{IssueID: "42", Open: true}}},
		{"closed open open: both reopened", cl, o, o, o, 0, 0, nil},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			c := newRepo(t, "alice", "")
			ctx := t.Context()
			ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}
			m := newTestMirror(ft)

			// Reach the row: import (open/open/open), settle on closed if
			// the row starts there, then move each side away from l.
			mustReconcile(t, m, c)
			id := onlyRecord(t, c).ID
			if row.l == cl {
				ft.CloseIssue("42")
				mustReconcile(t, m, c)
			}
			if row.r != row.l {
				if err := Append(ctx, c, id, &Op{Type: OpSetState, Value: row.r}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
			if row.tr != row.l {
				if row.tr == cl {
					ft.CloseIssue("42")
				} else {
					ft.ReopenIssue("42")
				}
			}

			res := mustReconcile(t, m, c)
			rec, _ := Load(ctx, c, id)
			trackerClosed, _ := ft.IsIssueClosed(ctx, "42")

			if rec.State != row.want || rec.TrackerState != row.want {
				t.Errorf("repo state = %q, recorded tracker state = %q, want both %q", rec.State, rec.TrackerState, row.want)
			}
			if trackerClosed != (row.want == cl) {
				t.Errorf("tracker closed = %v, want %v", trackerClosed, row.want == cl)
			}
			if res.Pulled != row.pulled || res.Pushed != row.pushed {
				t.Errorf("pulled/pushed = %d/%d, want %d/%d", res.Pulled, res.Pushed, row.pulled, row.pushed)
			}
			if !slices.Equal(ft.RecordedOpens, row.wantTrackerOp) {
				t.Errorf("tracker writes = %+v, want %+v", ft.RecordedOpens, row.wantTrackerOp)
			}
		})
	}
}

func TestReconcile_LinkWithoutTrackerState(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("7", "Linked")}}

	created, err := Create(ctx, c, NewIssue{Title: "Linked", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	link := &Op{Type: OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "7"}
	if err := Append(ctx, c, created.ID, link); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, newTestMirror(ft), c)
	rec, _ := Load(ctx, c, created.ID)

	t.Run("the missing state counts as open and is recorded", func(t *testing.T) {
		if rec.TrackerState != StateOpen || rec.State != StateOpen {
			t.Errorf("TrackerState = %q, State = %q", rec.TrackerState, rec.State)
		}
	})
	t.Run("nothing is imported, exported or written to the tracker", func(t *testing.T) {
		if res.Imported+res.Exported != 0 || len(ft.RecordedOpens)+len(ft.RecordedCreates) != 0 {
			t.Errorf("res = %+v, opens = %+v", res, ft.RecordedOpens)
		}
	})
}

func TestReconcile_TwoClonesImportOffline(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	a, b := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}

	// Neither clone has the other's refs when it imports.
	mustReconcile(t, newTestMirror(ft), a)
	mustReconcile(t, newTestMirror(ft), b)
	if _, err := Fetch(ctx, a); err != nil {
		t.Fatalf("Fetch: %v", err)
	}

	ra, rb := onlyRecord(t, a), onlyRecord(t, b)

	t.Run("both clones hold one and the same issue", func(t *testing.T) {
		if ra.ID != rb.ID {
			t.Errorf("IDs differ: %s vs %s", ra.ID, rb.ID)
		}
	})
	t.Run("the merged issue folds to the tracker's title and an open state", func(t *testing.T) {
		if ra.Title != "From tracker" || ra.State != StateOpen || ra.TrackerState != StateOpen {
			t.Errorf("record = %+v", ra)
		}
	})
}

func TestReconcile_DuplicateLink(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("1", "Exported"), trackerIssue("2", "Exported")}}

	created, err := Create(ctx, c, NewIssue{Title: "Exported", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, number := range []string{"1", "2"} {
		link := &Op{Type: OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: number}
		if err := Append(ctx, c, created.ID, link); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	res := mustReconcile(t, newTestMirror(ft), c)

	t.Run("the duplicate tracker issue is not imported", func(t *testing.T) {
		if res.Imported != 0 || onlyRecord(t, c).ID != created.ID {
			t.Errorf("res = %+v", res)
		}
	})
	t.Run("a warning names the duplicate", func(t *testing.T) {
		if !slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "tracker issue 2 duplicates") }) {
			t.Errorf("warnings = %v", res.Warnings)
		}
	})
}

func TestReconcile_TrackerDown(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	boom := errors.New("connection refused")
	ft := &fake.Tracker{ListErr: boom}

	created, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	before := opCount(t, c, created.ID)

	_, err = newTestMirror(ft).Reconcile(ctx, c)

	t.Run("the listing error is returned", func(t *testing.T) {
		if !errors.Is(err, boom) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("nothing was written on either side", func(t *testing.T) {
		if opCount(t, c, created.ID) != before || len(ft.RecordedCreates) != 0 {
			t.Errorf("ops %d → %d, creates = %d", before, opCount(t, c, created.ID), len(ft.RecordedCreates))
		}
	})
}

func TestReconcile_ExportFailureIsAWarning(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{CreateErr: errors.New("403 forbidden")}

	created, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	res, err := newTestMirror(ft).Reconcile(ctx, c)
	rec, _ := Load(ctx, c, created.ID)

	t.Run("the run succeeds with a warning", func(t *testing.T) {
		if err != nil || res.Exported != 0 || len(res.Warnings) != 1 {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("the record stays unlinked for the next run", func(t *testing.T) {
		if rec.Tracker != nil {
			t.Errorf("tracker = %+v", rec.Tracker)
		}
	})
}

// Review Focus 1 to 4.
func TestReconcile_AwkwardTrackerInput(t *testing.T) {
	t.Parallel()

	t.Run("a title with quotes, a newline and non-ASCII text is imported unchanged", func(t *testing.T) {
		t.Parallel()

		const title = "Le \"login\" échoue\nsur 日本語 {\"v\":2}"
		c := newRepo(t, "alice", "")
		mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", title)}}), c)
		if got := onlyRecord(t, c).Title; got != title {
			t.Errorf("Title = %q, want %q", got, title)
		}
	})

	t.Run("an issue listed twice is imported once", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		twice := []tracker.Issue{trackerIssue("42", "Twice"), trackerIssue("42", "Twice")}
		res := mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: twice}), c)
		if res.Imported != 1 || onlyRecord(t, c).Title != "Twice" {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("an issue ID with a space is skipped with a warning and the others are imported", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		listed := []tracker.Issue{trackerIssue("PROJ 12", "Bad"), trackerIssue("42", "Good")}
		res := mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: listed}), c)
		if res.Imported != 1 || len(res.Warnings) != 1 || onlyRecord(t, c).Title != "Good" {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("an issue without a creation date is skipped with a warning", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		undated := trackerIssue("42", "Undated")
		undated.CreatedAt = time.Time{}
		res := mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{undated}}), c)
		if res.Imported != 0 || len(res.Warnings) != 1 {
			t.Errorf("res = %+v", res)
		}
	})

	t.Run("a linked issue deleted from the tracker warns and writes nothing", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "Doomed")}}
		m := newTestMirror(ft)
		mustReconcile(t, m, c)
		rec := onlyRecord(t, c)
		before := opCount(t, c, rec.ID)

		ft.ProjectIssues = nil
		ft.Unknown = map[string]bool{"42": true}
		res := mustReconcile(t, m, c)

		if len(res.Warnings) != 1 || opCount(t, c, rec.ID) != before || onlyRecord(t, c).State != StateOpen {
			t.Errorf("res = %+v, ops %d → %d", res, before, opCount(t, c, rec.ID))
		}
	})
}

func TestResolve_TrackerNumber(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}
	m := newTestMirror(ft)

	local, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	mustReconcile(t, m, c) // imports 42, exports Local as 1

	t.Run("an imported issue resolves by its tracker number", func(t *testing.T) {
		rec, err := Resolve(ctx, c, "42")
		if err != nil || rec.Title != "From tracker" {
			t.Errorf("Resolve(42) = %q, %v", rec.Title, err)
		}
	})
	t.Run("an exported issue resolves by its tracker number", func(t *testing.T) {
		rec, err := Resolve(ctx, c, "1")
		if err != nil || rec.ID != local.ID {
			t.Errorf("Resolve(1) = %q, %v", rec.ID, err)
		}
	})
	t.Run("a number no issue is linked to is not found", func(t *testing.T) {
		if _, err := Resolve(ctx, c, "999"); !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})
}
