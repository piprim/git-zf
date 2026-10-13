package issue

import (
	"context"
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

	c := newRepo(t, "alice", newOrigin(t))
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
	t.Run("the healed record is pushed once, without a warning", func(t *testing.T) {
		if len(res.Warnings) != 0 {
			t.Errorf("warnings = %v", res.Warnings)
		}
		if pushed, err := c.ChainRefPushed(t.Context(), git.IssueRefs, id); err != nil || !pushed {
			t.Errorf("ChainRefPushed = %v, %v", pushed, err)
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
	t.Run("the record is linked and displays its number", func(t *testing.T) {
		want := TrackerLink{Type: "fake", Project: "zf", ID: "1"}
		if rec.Tracker == nil || *rec.Tracker != want || rec.DisplayID() != "1" {
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

// Spec step 4: nobody moved but the status name changed → one tracker_state
// op, and nothing on the run after.
func TestReconcile_StatusNameChange(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	id := onlyRecord(t, c).ID
	before := opCount(t, c, id)

	ft.ProjectIssues[0].Status = "In Progress"
	res := mustReconcile(t, m, c)
	rec, _ := Load(t.Context(), c, id)
	after := opCount(t, c, id)

	t.Run("one op records the new name and nothing moved", func(t *testing.T) {
		if after != before+1 || rec.TrackerStatus != "In Progress" || res.Pulled+res.Pushed != 0 {
			t.Errorf("ops %d → %d, status %q, res %+v", before, after, rec.TrackerStatus, res)
		}
	})

	mustReconcile(t, m, c)

	t.Run("a second run changes nothing", func(t *testing.T) {
		if got := opCount(t, c, id); got != after {
			t.Errorf("ops %d → %d", after, got)
		}
	})
}

// A reopen pushed to the tracker settles on the next run: one op records the
// status name the listing shows, then nothing.
func TestReconcile_PushedReopenSettles(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "From tracker")}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	id := onlyRecord(t, c).ID
	ft.CloseIssue("42")
	mustReconcile(t, m, c)
	if err := Append(ctx, c, id, &Op{Type: OpSetState, Value: StateOpen}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	pushed := mustReconcile(t, m, c)
	before := opCount(t, c, id)

	settled := mustReconcile(t, m, c)
	rec, _ := Load(ctx, c, id)
	after := opCount(t, c, id)

	t.Run("the reopen was pushed", func(t *testing.T) {
		if pushed.Pushed != 1 {
			t.Errorf("res = %+v", pushed)
		}
	})
	t.Run("the next run records the listed status name in one op", func(t *testing.T) {
		if after != before+1 || rec.TrackerStatus != "open" || settled.Pulled+settled.Pushed != 0 {
			t.Errorf("ops %d → %d, status %q, res %+v", before, after, rec.TrackerStatus, settled)
		}
	})

	mustReconcile(t, m, c)

	t.Run("the run after changes nothing", func(t *testing.T) {
		if got := opCount(t, c, id); got != after {
			t.Errorf("ops %d → %d", after, got)
		}
	})
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

	t.Run("a title with quotes and non-ASCII text is imported unchanged, its newline read as a space", func(t *testing.T) {
		t.Parallel()

		const title = "Le \"login\" échoue\nsur 日本語 {\"v\":2}"
		const want = "Le \"login\" échoue sur 日本語 {\"v\":2}"
		c := newRepo(t, "alice", "")
		mustReconcile(t, newTestMirror(&fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", title)}}), c)
		if got := onlyRecord(t, c).Title; got != want {
			t.Errorf("Title = %q, want %q", got, want)
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

// countingTracker counts the per-issue reads the reconcile makes.
type countingTracker struct {
	*fake.Tracker
	gets int
}

func (c *countingTracker) GetIssue(ctx context.Context, id string) (tracker.Issue, error) {
	c.gets++

	return c.Tracker.GetIssue(ctx, id)
}

// importOne imports tracker issue 42 titled "A" with description "D" and
// returns the repo, the fake, the mirror and the record.
func importOne(t *testing.T) (*git.Client, *fake.Tracker, *Mirror, Record) {
	t.Helper()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "A")
	iss.Description = "D"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)

	return c, ft, m, onlyRecord(t, c)
}

func TestReconcile_TitleAndDescription(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name           string
		r, tr          string // what each side set the title to; "A" is the baseline
		want           string
		pulled, pushed int
		edits          int // UpdateIssue calls
		conflict       bool
	}{
		{"nobody moved", "A", "A", "A", 0, 0, 0, false},
		{"the tracker moved", "A", "B", "B", 1, 0, 0, false},
		{"the repo moved", "B", "A", "B", 0, 1, 1, false},
		{"both moved the same way", "B", "B", "B", 0, 0, 0, false},
		{"both moved apart: the repo wins", "B", "C", "B", 0, 1, 1, true},
	}

	for _, row := range rows {
		t.Run("title: "+row.name, func(t *testing.T) {
			t.Parallel()

			c, ft, m, rec := importOne(t)
			ctx := t.Context()
			if row.r != "A" {
				if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: row.r}); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
			if row.tr != "A" {
				ft.Edit("42", func(iss *tracker.Issue) { iss.Subject = row.tr })
			}

			res := mustReconcile(t, m, c)
			got, _ := Load(ctx, c, rec.ID)
			onTracker, _ := ft.GetIssue(ctx, "42")

			if got.Title != row.want || onTracker.Subject != row.want {
				t.Errorf("repo title = %q, tracker title = %q, want both %q", got.Title, onTracker.Subject, row.want)
			}
			if res.Pulled != row.pulled || res.Pushed != row.pushed || len(ft.RecordedEdits) != row.edits {
				t.Errorf("pulled/pushed/edits = %d/%d/%d, want %d/%d/%d", res.Pulled, res.Pushed, len(ft.RecordedEdits), row.pulled, row.pushed, row.edits)
			}
			if warned := len(res.Warnings) == 1 && strings.Contains(res.Warnings[0], "title"); warned != row.conflict {
				t.Errorf("warnings = %q, want a conflict warning: %v", res.Warnings, row.conflict)
			}
			if got.TrackerSeen == nil || got.TrackerSeen.Title != row.want || !got.TrackerSeen.Updated.Equal(onTracker.UpdatedAt) {
				t.Errorf("snapshot = %+v, tracker updated %v", got.TrackerSeen, onTracker.UpdatedAt)
			}
		})
	}

	t.Run("description cleared on the tracker clears the record", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.Edit("42", func(iss *tracker.Issue) { iss.Description = "" })
		res := mustReconcile(t, m, c)
		got, _ := Load(t.Context(), c, rec.ID)
		if got.Description != "" || res.Pulled != 1 {
			t.Errorf("description = %q, res = %+v", got.Description, res)
		}
	})

	t.Run("description changed in the repo reaches the tracker with the unchanged title", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		if err := Append(t.Context(), c, rec.ID, &Op{Type: OpSetDescription, Value: "D2"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		if want := (fake.Edit{IssueID: "42", Title: "A", Description: "D2"}); len(ft.RecordedEdits) != 1 || ft.RecordedEdits[0] != want {
			t.Errorf("edits = %+v, want %+v", ft.RecordedEdits, want)
		}
	})
}

func TestReconcile_ContentSettles(t *testing.T) {
	t.Parallel()

	c, ft, _, rec := importOne(t)
	ct := &countingTracker{Tracker: ft}
	m := &Mirror{Tracker: ct, Type: "fake", Project: "zf"}
	ctx := t.Context()

	before := opCount(t, c, rec.ID)
	again := mustReconcile(t, m, c)

	t.Run("a run with nothing changed writes no op, reads no issue and edits nothing", func(t *testing.T) {
		if opCount(t, c, rec.ID) != before || ct.gets != 0 || len(ft.RecordedEdits) != 0 || again.Pulled+again.Pushed != 0 {
			t.Errorf("ops %d → %d, gets %d, edits %d, res %+v", before, opCount(t, c, rec.ID), ct.gets, len(ft.RecordedEdits), again)
		}
	})

	if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: "B"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	mustReconcile(t, m, c)
	gets := ct.gets
	afterPush := opCount(t, c, rec.ID)
	third := mustReconcile(t, m, c)

	t.Run("a push re-reads the issue once so that the run after it is settled", func(t *testing.T) {
		if gets != 1 || opCount(t, c, rec.ID) != afterPush || ct.gets != 1 || third.Pulled+third.Pushed != 0 {
			t.Errorf("gets after push %d, ops %d → %d, gets after third %d, third %+v", gets, afterPush, opCount(t, c, rec.ID), ct.gets, third)
		}
	})
}

func TestReconcile_ContentOfAClosedIssue(t *testing.T) {
	t.Parallel()

	c, ft, m, rec := importOne(t)
	ft.Edit("42", func(iss *tracker.Issue) { iss.Subject = "Closed with a new title" })
	ft.CloseIssue("42")
	res := mustReconcile(t, m, c)
	got, _ := Load(t.Context(), c, rec.ID)

	t.Run("an issue closed on the tracker still gets its last edit", func(t *testing.T) {
		if got.State != StateClosed || got.Title != "Closed with a new title" || res.Pulled != 1 {
			t.Errorf("record = %+v, res = %+v", got, res)
		}
	})
}

func TestReconcile_TrackerIssueGone(t *testing.T) {
	t.Parallel()

	c, ft, m, rec := importOne(t)
	ft.CloseIssue("42")
	ft.Unknown = map[string]bool{"42": true}
	before := opCount(t, c, rec.ID)
	res := mustReconcile(t, m, c)

	t.Run("a record whose tracker issue is gone is skipped with one warning", func(t *testing.T) {
		if len(res.Warnings) != 1 || opCount(t, c, rec.ID) != before {
			t.Errorf("warnings = %q, ops %d → %d", res.Warnings, before, opCount(t, c, rec.ID))
		}
	})
}

func TestReconcile_RecordWithoutSnapshot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	rec, err := Create(ctx, c, NewIssue{Title: "Local", Description: "Old", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A link written by the binary before this change: no tracker_seen.
	for _, op := range []*Op{
		{Type: OpLinkTracker, TrackerType: "fake", Project: "zf", TrackerID: "7"},
		{Type: OpTrackerState, Value: StateOpen, Status: "open"},
	} {
		if err := Append(ctx, c, rec.ID, op); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
	iss := trackerIssue("7", "Local")
	iss.Description = "Edited on the web"
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}}
	res := mustReconcile(t, newTestMirror(ft), c)
	got, _ := Load(ctx, c, rec.ID)

	t.Run("the tracker's content is pulled wherever it differs, and a snapshot is written", func(t *testing.T) {
		if got.Description != "Edited on the web" || got.TrackerSeen == nil || res.Pulled != 1 || len(ft.RecordedEdits) != 0 {
			t.Errorf("record = %+v, res = %+v, edits = %+v", got, res, ft.RecordedEdits)
		}
	})
}

func TestReconcile_ExportWritesASnapshot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	if _, err := Create(t.Context(), c, NewIssue{Title: "Local", Description: "Steps", BranchType: "feat"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ft := &fake.Tracker{}
	mustReconcile(t, newTestMirror(ft), c)
	rec := onlyRecord(t, c)

	t.Run("the snapshot is the created issue", func(t *testing.T) {
		if rec.TrackerSeen == nil || rec.TrackerSeen.Title != "Local" || rec.TrackerSeen.Description != "Steps" || rec.TrackerSeen.Updated.IsZero() {
			t.Errorf("snapshot = %+v", rec.TrackerSeen)
		}
	})
}

func TestReconcile_PullFromAnUnlistedIssueIsPushed(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", newOrigin(t))
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	ft.CloseIssue("42")
	res := mustReconcile(t, m, c)
	rec := onlyRecord(t, c)

	t.Run("the close read from the tracker is pushed", func(t *testing.T) {
		pushed, err := c.ChainRefPushed(t.Context(), git.IssueRefs, rec.ID)
		if rec.State != StateClosed || res.Pulled != 1 || err != nil || !pushed {
			t.Errorf("state %s, res %+v, ChainRefPushed = %v, %v", rec.State, res, pushed, err)
		}
	})
}

func TestReconcile_Labels(t *testing.T) {
	t.Parallel()

	// The baseline is {bug}. Each row names what each side did to it.
	rows := []struct {
		name   string
		r, tr  []string
		want   []string
		pulled int
		pushed int
		sets   int // SetLabels calls
	}{
		{"nobody moved", []string{"bug"}, []string{"bug"}, []string{"bug"}, 0, 0, 0},
		{"the tracker added one", []string{"bug"}, []string{"bug", "ui"}, []string{"bug", "ui"}, 1, 0, 0},
		{"the tracker removed one", []string{"bug"}, nil, []string{}, 1, 0, 0},
		{"the repo added one", []string{"bug", "needs triage"}, []string{"bug"}, []string{"bug", "needs triage"}, 0, 1, 1},
		{"the repo removed one", nil, []string{"bug"}, []string{}, 0, 1, 1},
		{"both added the same one", []string{"bug", "ui"}, []string{"bug", "ui"}, []string{"bug", "ui"}, 0, 0, 0},
		{"each side added its own", []string{"bug", "ui"}, []string{"bug", "doc"}, []string{"bug", "doc", "ui"}, 1, 1, 1},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			c := newRepo(t, "alice", "")
			ctx := t.Context()
			iss := trackerIssue("42", "A")
			iss.Labels = []string{"bug"}
			ft := &fake.Tracker{ProjectIssues: []tracker.Issue{iss}, Labels: []string{"bug", "ui", "doc"}}
			m := newTestMirror(ft)
			mustReconcile(t, m, c)
			rec := onlyRecord(t, c)

			for _, op := range diffLabelsForTest(rec.Labels, row.r) {
				if err := Append(ctx, c, rec.ID, op); err != nil {
					t.Fatalf("Append: %v", err)
				}
			}
			if !slices.Equal(row.tr, []string{"bug"}) {
				ft.Edit("42", func(iss *tracker.Issue) { iss.Labels = slices.Clone(row.tr) })
			}

			res := mustReconcile(t, m, c)
			got, _ := Load(ctx, c, rec.ID)
			onTracker, _ := ft.GetIssue(ctx, "42")
			sorted := slices.Clone(onTracker.Labels)
			slices.Sort(sorted)

			if !slices.Equal(got.Labels, row.want) || !slices.Equal(sorted, row.want) {
				t.Errorf("repo labels = %q, tracker labels = %q, want both %q", got.Labels, sorted, row.want)
			}
			if res.Pulled != row.pulled || res.Pushed != row.pushed || len(ft.RecordedLabelSets) != row.sets {
				t.Errorf("pulled/pushed/sets = %d/%d/%d, want %d/%d/%d", res.Pulled, res.Pushed, len(ft.RecordedLabelSets), row.pulled, row.pushed, row.sets)
			}
			if got.TrackerSeen == nil || !slices.Equal(got.TrackerSeen.Labels, row.want) {
				t.Errorf("snapshot labels = %q, want %q", got.TrackerSeen, row.want)
			}
		})
	}
}

// diffLabelsForTest is the add/remove ops that take labels from have to want.
func diffLabelsForTest(have, want []string) []*Op {
	var ops []*Op
	for _, l := range have {
		if !slices.Contains(want, l) {
			ops = append(ops, &Op{Type: OpRemoveLabel, Value: l})
		}
	}
	for _, l := range want {
		if !slices.Contains(have, l) {
			ops = append(ops, &Op{Type: OpAddLabel, Value: l})
		}
	}

	return ops
}

func TestReconcile_LabelRuleRejection(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	iss := trackerIssue("42", "A")
	iss.Status, iss.Labels = "New", []string{"New"}
	ft := &fake.Tracker{StatusIsLabel: true, Statuses: []string{"New", "Closed"}, ClosingStatuses: []string{"Closed"},
		ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	rec := onlyRecord(t, c)
	if err := Append(ctx, c, rec.ID, &Op{Type: OpAddLabel, Value: "urgent"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	res := mustReconcile(t, m, c)
	got, _ := Load(ctx, c, rec.ID)

	t.Run("the record takes the tracker's labels back, with one warning naming the dropped name", func(t *testing.T) {
		if !slices.Equal(got.Labels, []string{"New"}) || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "urgent") {
			t.Errorf("labels = %q, warnings = %q", got.Labels, res.Warnings)
		}
	})

	t.Run("the warning names the labels the tracker kept", func(t *testing.T) {
		if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], `kept ["New"]`) {
			t.Errorf("warnings = %q", res.Warnings)
		}
	})

	before := opCount(t, c, rec.ID)
	again := mustReconcile(t, m, c)

	t.Run("the run after is quiet", func(t *testing.T) {
		if len(again.Warnings) != 0 || opCount(t, c, rec.ID) != before {
			t.Errorf("warnings = %q, ops %d → %d", again.Warnings, before, opCount(t, c, rec.ID))
		}
	})
}

func TestReconcile_LabelTransportFailureKeepsTheChange(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)
	rec := onlyRecord(t, c)
	if err := Append(ctx, c, rec.ID, &Op{Type: OpAddLabel, Value: "urgent"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	ft.SetLabelsErr = errors.New("502")
	res := mustReconcile(t, m, c)
	got, _ := Load(ctx, c, rec.ID)

	t.Run("a transport failure is a warning and the local label stays", func(t *testing.T) {
		if !slices.Equal(got.Labels, []string{"urgent"}) || len(res.Warnings) != 1 {
			t.Errorf("labels = %q, warnings = %q", got.Labels, res.Warnings)
		}
	})

	ft.SetLabelsErr = nil
	mustReconcile(t, m, c)
	onTracker, _ := ft.GetIssue(ctx, "42")

	t.Run("the next run pushes it", func(t *testing.T) {
		if !slices.Equal(onTracker.Labels, []string{"urgent"}) {
			t.Errorf("tracker labels = %q", onTracker.Labels)
		}
	})
}

func TestReconcile_Comments(t *testing.T) {
	t.Parallel()

	t.Run("an import brings the tracker's comments with their author, time and ID", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
		web := ft.WebComment("42", "bob", "From the web")
		mustReconcile(t, newTestMirror(ft), c)
		rec := onlyRecord(t, c)
		if len(rec.Comments) != 1 || rec.Comments[0].Author != "bob" || rec.Comments[0].TrackerID != web.ID ||
			rec.Comments[0].Body != "From the web" || !rec.Comments[0].At.Equal(web.CreatedAt) {
			t.Errorf("comments = %+v", rec.Comments)
		}
	})

	t.Run("a tracker comment made after the import is pulled, once", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.WebComment("42", "bob", "Later")
		res := mustReconcile(t, m, c)
		got, _ := Load(t.Context(), c, rec.ID)
		if len(got.Comments) != 1 || got.Comments[0].Body != "Later" || res.Pulled != 1 {
			t.Errorf("comments = %+v, res = %+v", got.Comments, res)
		}
		before := opCount(t, c, rec.ID)
		mustReconcile(t, m, c)
		if opCount(t, c, rec.ID) != before {
			t.Errorf("ops %d → %d on the run after", before, opCount(t, c, rec.ID))
		}
	})

	t.Run("a repo comment is posted and linked, and not pulled back", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "From the repo"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		res := mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		if len(ft.RecordedComments) != 1 || ft.RecordedComments[0].Body != "From the repo" || res.Pushed != 1 {
			t.Errorf("posted = %+v, res = %+v", ft.RecordedComments, res)
		}
		if len(got.Comments) != 1 || got.Comments[0].TrackerID != ft.RecordedComments[0].ID {
			t.Errorf("comments = %+v", got.Comments)
		}
		mustReconcile(t, m, c)
		got, _ = Load(ctx, c, rec.ID)
		if len(got.Comments) != 1 || len(ft.RecordedComments) != 1 {
			t.Errorf("after a second run: comments = %+v, posted = %+v", got.Comments, ft.RecordedComments)
		}
	})

	t.Run("a local comment already on the tracker is linked, not posted again", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "Posted, link lost"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		web := ft.WebComment("42", "alice", "Posted, link lost")
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		if len(ft.RecordedComments) != 0 || len(got.Comments) != 1 || got.Comments[0].TrackerID != web.ID {
			t.Errorf("posted = %+v, comments = %+v", ft.RecordedComments, got.Comments)
		}
	})

	t.Run("an empty tracker comment is not pulled", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.WebComment("42", "bot", "")
		mustReconcile(t, m, c)
		got, _ := Load(t.Context(), c, rec.ID)
		if len(got.Comments) != 0 {
			t.Errorf("comments = %+v", got.Comments)
		}
	})

	t.Run("two clones pulling the same comment offline fold to one", func(t *testing.T) {
		t.Parallel()

		origin := newOrigin(t)
		a, b := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
		ctx := t.Context()
		ft := &fake.Tracker{ProjectIssues: []tracker.Issue{trackerIssue("42", "A")}}
		mustReconcile(t, newTestMirror(ft), a)
		if _, err := Fetch(ctx, b); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		ft.WebComment("42", "bob", "Seen by both")
		mustReconcile(t, newTestMirror(ft), a)
		mustReconcile(t, newTestMirror(ft), b)
		if _, err := Fetch(ctx, a); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if ra := onlyRecord(t, a); len(ra.Comments) != 1 {
			t.Errorf("comments = %+v", ra.Comments)
		}
	})
}

func TestSyncOne(t *testing.T) {
	t.Parallel()

	t.Run("a nil mirror does nothing", func(t *testing.T) {
		t.Parallel()

		var m *Mirror
		if res, err := m.SyncOne(t.Context(), newRepo(t, "alice", ""), "x"); err != nil || res.Pushed != 0 {
			t.Errorf("res = %+v, err = %v", res, err)
		}
	})

	t.Run("pushes the record's change to the tracker without a listing", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		listings := ft.ListCalls
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: "B"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		res, err := m.SyncOne(ctx, c, rec.ID)
		onTracker, _ := ft.GetIssue(ctx, "42")
		if err != nil || res.Pushed != 1 || onTracker.Subject != "B" || ft.ListCalls != listings {
			t.Errorf("res = %+v, err = %v, tracker title = %q, listings %d → %d", res, err, onTracker.Subject, listings, ft.ListCalls)
		}
	})

	t.Run("a record without a link is left alone", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		ctx := t.Context()
		rec, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		ft := &fake.Tracker{}
		if res, err := newTestMirror(ft).SyncOne(ctx, c, rec.ID); err != nil || res.Exported != 0 || len(ft.RecordedCreates) != 0 {
			t.Errorf("res = %+v, err = %v, creates = %+v", res, err, ft.RecordedCreates)
		}
	})

	t.Run("a comment on a record closed on both sides is posted", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "After the close"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		_, err := m.SyncOne(ctx, c, rec.ID)
		got, _ := Load(ctx, c, rec.ID)
		if err != nil || len(ft.RecordedComments) != 1 || len(got.Comments) != 1 || got.Comments[0].TrackerID == "" {
			t.Errorf("err = %v, posted = %+v, comments = %+v", err, ft.RecordedComments, got.Comments)
		}
	})

	t.Run("a comment left behind by a failed sync is posted by the next reconcile", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "After the close"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		ft.GetErr = errors.New("502")
		if _, err := m.SyncOne(ctx, c, rec.ID); err == nil {
			t.Fatal("SyncOne with the tracker down: no error")
		}
		ft.GetErr = nil
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		if len(ft.RecordedComments) != 1 || len(got.Comments) != 1 || got.Comments[0].TrackerID == "" {
			t.Errorf("posted = %+v, comments = %+v", ft.RecordedComments, got.Comments)
		}
	})

	t.Run("a tracker failure is returned, the record untouched", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := importOne(t)
		ft.GetErr = errors.New("502")
		before := opCount(t, c, rec.ID)
		if _, err := m.SyncOne(t.Context(), c, rec.ID); err == nil || opCount(t, c, rec.ID) != before {
			t.Errorf("err = %v, ops %d → %d", err, before, opCount(t, c, rec.ID))
		}
	})
}

// newStatusIsLabelMirror imports issue 42 in status "New" on a fake that
// behaves like Redmine.
func newStatusIsLabelMirror(t *testing.T) (*git.Client, *fake.Tracker, *Mirror, Record) {
	t.Helper()

	c := newRepo(t, "alice", "")
	iss := trackerIssue("42", "A")
	iss.Status = "New"
	ft := &fake.Tracker{StatusIsLabel: true, Statuses: []string{"New", "Resolved", "Closed"},
		ClosingStatuses: []string{"Closed"}, ProjectIssues: []tracker.Issue{iss}}
	m := newTestMirror(ft)
	mustReconcile(t, m, c)

	return c, ft, m, onlyRecord(t, c)
}

func TestReconcile_StatusIsLabel(t *testing.T) {
	t.Parallel()

	t.Run("the imported record carries the status as its one label", func(t *testing.T) {
		t.Parallel()

		_, _, _, rec := newStatusIsLabelMirror(t)
		if !slices.Equal(rec.Labels, []string{"New"}) {
			t.Errorf("labels = %q", rec.Labels)
		}
	})

	t.Run("a close in the repo gives the record the closing status as label in one run", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		closed, _ := ft.IsIssueClosed(ctx, "42")
		if !closed || !slices.Equal(got.Labels, []string{"Closed"}) || got.State != StateClosed {
			t.Errorf("tracker closed = %v, record = %+v", closed, got)
		}
		if len(ft.RecordedLabelSets) != 0 {
			t.Errorf("SetLabels was called: %+v", ft.RecordedLabelSets)
		}
	})

	t.Run("a closing status set as a label closes the record in the same run", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		for _, op := range []*Op{{Type: OpRemoveLabel, Value: "New"}, {Type: OpAddLabel, Value: "Closed"}} {
			if err := Append(ctx, c, rec.ID, op); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		closed, _ := ft.IsIssueClosed(ctx, "42")
		if !closed || got.State != StateClosed || !slices.Equal(got.Labels, []string{"Closed"}) || got.TrackerStatus != "Closed" {
			t.Errorf("tracker closed = %v, record = %+v", closed, got)
		}
	})

	t.Run("an exported record takes the default status as its one label, quietly", func(t *testing.T) {
		t.Parallel()

		c, ft, m, _ := newStatusIsLabelMirror(t)
		ctx := t.Context()
		local, err := Create(ctx, c, NewIssue{Title: "Local", BranchType: "feat"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		res := mustReconcile(t, m, c)
		got, _ := Load(ctx, c, local.ID)
		if res.Exported != 1 || !slices.Equal(got.Labels, []string{"New"}) || len(res.Warnings) != 0 || len(ft.RecordedLabelSets) != 0 {
			t.Errorf("res = %+v, labels = %q, sets = %+v", res, got.Labels, ft.RecordedLabelSets)
		}
	})

	t.Run("a status picked on the tracker is pulled as the label", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		if err := ft.UpdateIssueStatus(ctx, "42", "Resolved"); err != nil {
			t.Fatalf("UpdateIssueStatus: %v", err)
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		if !slices.Equal(got.Labels, []string{"Resolved"}) || got.TrackerStatus != "Resolved" {
			t.Errorf("record = %+v", got)
		}
	})
}

func TestContentSettled_ZeroUpdated(t *testing.T) {
	t.Parallel()

	t.Run("a snapshot without an updated time is not settled", func(t *testing.T) {
		rec := &Record{Title: "A", Labels: []string{}, TrackerSeen: &TrackerSnapshot{Title: "A", Labels: []string{}}}
		if contentSettled(rec, &tracker.Issue{Subject: "A"}) {
			t.Error("contentSettled = true, want false")
		}
	})
}

// trimmingTracker trims the title it is sent, as a tracker that respells it.
type trimmingTracker struct{ *fake.Tracker }

func (tt trimmingTracker) UpdateIssue(ctx context.Context, id, title, description string) error {
	return tt.Tracker.UpdateIssue(ctx, id, strings.TrimSpace(title), description)
}

func TestReconcile_RespelledPushSettles(t *testing.T) {
	t.Parallel()

	t.Run("a label the tracker respells is pushed once", func(t *testing.T) {
		t.Parallel()

		c, ft, m, rec := newStatusIsLabelMirror(t)
		ctx := t.Context()
		for _, op := range []*Op{{Type: OpRemoveLabel, Value: "New"}, {Type: OpAddLabel, Value: "resolved"}} {
			if err := Append(ctx, c, rec.ID, op); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		t.Run("the record takes the tracker's spelling", func(t *testing.T) {
			if !slices.Equal(got.Labels, []string{"Resolved"}) || len(ft.RecordedLabelSets) != 1 {
				t.Errorf("labels = %q, sets = %+v", got.Labels, ft.RecordedLabelSets)
			}
		})

		before := opCount(t, c, rec.ID)
		mustReconcile(t, m, c)
		t.Run("the second run writes nothing and sets no label", func(t *testing.T) {
			if opCount(t, c, rec.ID) != before || len(ft.RecordedLabelSets) != 1 {
				t.Errorf("ops %d → %d, sets = %+v", before, opCount(t, c, rec.ID), ft.RecordedLabelSets)
			}
		})
	})

	t.Run("a title the tracker trims is pushed once", func(t *testing.T) {
		t.Parallel()

		c, ft, _, rec := importOne(t)
		ctx := t.Context()
		m := &Mirror{Tracker: trimmingTracker{ft}, Type: "fake", Project: "zf"}
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetTitle, Value: "B "}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		mustReconcile(t, m, c)
		got, _ := Load(ctx, c, rec.ID)
		t.Run("the record takes the tracker's title", func(t *testing.T) {
			if got.Title != "B" || len(ft.RecordedEdits) != 1 {
				t.Errorf("title = %q, edits = %+v", got.Title, ft.RecordedEdits)
			}
		})

		before := opCount(t, c, rec.ID)
		mustReconcile(t, m, c)
		t.Run("the second run writes nothing and edits nothing", func(t *testing.T) {
			if opCount(t, c, rec.ID) != before || len(ft.RecordedEdits) != 1 {
				t.Errorf("ops %d → %d, edits = %+v", before, opCount(t, c, rec.ID), ft.RecordedEdits)
			}
		})
	})
}

func TestLabels_AsksTheTrackerOnce(t *testing.T) {
	t.Parallel()

	ft := &fake.Tracker{Labels: []string{"bug", "ui"}}
	m := newTestMirror(ft)
	ctx := t.Context()

	first, err := m.Labels(ctx)
	t.Run("the first call lists the tracker", func(t *testing.T) {
		if err != nil || !slices.Equal(first, []string{"bug", "ui"}) || ft.ListLabelsCalls != 1 {
			t.Errorf("labels = %q, err = %v, calls = %d", first, err, ft.ListLabelsCalls)
		}
	})

	ft.Labels = []string{"bug", "ui", "docs"}
	first[0] = "edited"
	second, _ := m.Labels(ctx)
	t.Run("the second call is answered from memory, a copy the caller cannot edit", func(t *testing.T) {
		if !slices.Equal(second, []string{"bug", "ui"}) || ft.ListLabelsCalls != 1 {
			t.Errorf("labels = %q, calls = %d", second, ft.ListLabelsCalls)
		}
	})

	mustReconcile(t, m, newRepo(t, "alice", ""))
	third, _ := m.Labels(ctx)
	t.Run("a reconcile forgets them", func(t *testing.T) {
		if !slices.Equal(third, []string{"bug", "ui", "docs"}) || ft.ListLabelsCalls != 2 {
			t.Errorf("labels = %q, calls = %d", third, ft.ListLabelsCalls)
		}
	})
}
