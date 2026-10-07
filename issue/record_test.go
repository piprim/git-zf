package issue

import (
	"slices"
	"testing"
)

func op(id, typ, at string, parents ...string) Op {
	return Op{V: OpVersion, ID: id, Type: typ, At: at, Parents: parents}
}

func TestFold(t *testing.T) {
	t.Parallel()

	create := op("c0", OpCreate, "2026-10-01T10:00:00Z")
	create.Title, create.Description, create.BranchType = "Login fails", "Steps…", "fix"

	t.Run("create sets the identity fields and an open state", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{create})
		if rec.ID != "c0" || rec.Title != "Login fails" || rec.Description != "Steps…" || rec.BranchType != "fix" {
			t.Errorf("unexpected record: %+v", rec)
		}
		if rec.State != StateOpen {
			t.Errorf("State = %q, want %q", rec.State, StateOpen)
		}
		if rec.CreatedAt.IsZero() {
			t.Error("CreatedAt is zero")
		}
	})

	t.Run("set_state closed then open ends open", func(t *testing.T) {
		t.Parallel()

		closed := op("c1", OpSetState, "2026-10-01T11:00:00Z", "c0")
		closed.Value = StateClosed
		reopened := op("c2", OpSetState, "2026-10-01T12:00:00Z", "c1")
		reopened.Value = StateOpen

		if got := Fold("c0", []Op{create, closed}).State; got != StateClosed {
			t.Errorf("after close: State = %q", got)
		}
		if got := Fold("c0", []Op{create, closed, reopened}).State; got != StateOpen {
			t.Errorf("after reopen: State = %q", got)
		}
	})

	t.Run("an unknown state value is ignored", func(t *testing.T) {
		t.Parallel()

		bad := op("c1", OpSetState, "2026-10-01T11:00:00Z", "c0")
		bad.Value = "wontfix"
		if got := Fold("c0", []Op{create, bad}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}
	})

	t.Run("labels are a sorted set and removing an absent label is a no-op", func(t *testing.T) {
		t.Parallel()

		a1 := op("c1", OpAddLabel, "2026-10-01T11:00:00Z", "c0")
		a1.Value = "ui"
		a2 := op("c2", OpAddLabel, "2026-10-01T11:01:00Z", "c1")
		a2.Value = "bug"
		a3 := op("c3", OpAddLabel, "2026-10-01T11:02:00Z", "c2")
		a3.Value = "ui"
		r1 := op("c4", OpRemoveLabel, "2026-10-01T11:03:00Z", "c3")
		r1.Value = "nope"

		got := Fold("c0", []Op{create, a1, a2, a3, r1}).Labels
		if !slices.Equal(got, []string{"bug", "ui"}) {
			t.Errorf("Labels = %v, want [bug ui]", got)
		}
	})

	t.Run("comments accumulate in order with their op ID", func(t *testing.T) {
		t.Parallel()

		k1 := op("c1", OpAddComment, "2026-10-01T11:00:00Z", "c0")
		k1.Body, k1.Author = "first", "A <a@x>"
		k2 := op("c2", OpAddComment, "2026-10-01T12:00:00Z", "c1")
		k2.Body = "second"

		got := Fold("c0", []Op{create, k1, k2}).Comments
		if len(got) != 2 || got[0].Body != "first" || got[1].Body != "second" {
			t.Fatalf("Comments = %+v", got)
		}
		if got[0].ID != "c1" || got[0].Author != "A <a@x>" {
			t.Errorf("first comment = %+v", got[0])
		}
	})

	t.Run("slice order does not matter", func(t *testing.T) {
		t.Parallel()

		closed := op("c1", OpSetState, "2026-10-01T11:00:00Z", "c0")
		closed.Value = StateClosed
		if got := Fold("c0", []Op{closed, create}).State; got != StateClosed {
			t.Errorf("State = %q, want %q", got, StateClosed)
		}
	})

	t.Run("concurrent ops are ordered by At then ID", func(t *testing.T) {
		t.Parallel()

		// Two clones diverge from c0: one closes at 11:00, the other reopens
		// at 12:00. A merge joins them. The later At wins.
		closed := op("bb", OpSetState, "2026-10-01T11:00:00Z", "c0")
		closed.Value = StateClosed
		opened := op("aa", OpSetState, "2026-10-01T12:00:00Z", "c0")
		opened.Value = StateOpen
		merge := op("m", OpMerge, "2026-10-01T13:00:00Z", "bb", "aa")

		if got := Fold("c0", []Op{create, closed, opened, merge}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}

		// Same At on both sides: the greater ID is applied last.
		opened.At = closed.At
		if got := Fold("c0", []Op{create, closed, opened, merge}).State; got != StateClosed {
			t.Errorf("equal At: State = %q, want %q (ID bb applied after aa)", got, StateClosed)
		}
	})

	t.Run("a causally later op beats a future-dated one", func(t *testing.T) {
		t.Parallel()

		// A clock set to 2099 closes the issue; a later op, written after
		// seeing it, reopens it with a correct clock.
		skewed := op("c1", OpSetState, "2099-01-01T00:00:00Z", "c0")
		skewed.Value = StateClosed
		later := op("c2", OpSetState, "2026-10-01T12:00:00Z", "c1")
		later.Value = StateOpen

		if got := Fold("c0", []Op{create, skewed, later}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}
	})

	t.Run("set_title and set_description replace the create values", func(t *testing.T) {
		t.Parallel()

		title := op("c1", OpSetTitle, "2026-10-01T11:00:00Z", "c0")
		title.Value = "Login fails on Safari"
		desc := op("c2", OpSetDescription, "2026-10-01T12:00:00Z", "c1")
		desc.Value = "New steps"

		rec := Fold("c0", []Op{create, title, desc})
		if rec.Title != "Login fails on Safari" || rec.Description != "New steps" {
			t.Errorf("unexpected record: %+v", rec)
		}
		if rec.BranchType != "fix" {
			t.Errorf("BranchType = %q, want it untouched", rec.BranchType)
		}
	})

	t.Run("set_description with no value clears the description", func(t *testing.T) {
		t.Parallel()

		desc := op("c1", OpSetDescription, "2026-10-01T11:00:00Z", "c0")
		if got := Fold("c0", []Op{create, desc}).Description; got != "" {
			t.Errorf("Description = %q, want empty", got)
		}
	})

	t.Run("concurrent title edits: the later At wins", func(t *testing.T) {
		t.Parallel()

		early := op("bb", OpSetTitle, "2026-10-01T11:00:00Z", "c0")
		early.Value = "early"
		late := op("aa", OpSetTitle, "2026-10-01T12:00:00Z", "c0")
		late.Value = "late"
		merge := op("m", OpMerge, "2026-10-01T13:00:00Z", "bb", "aa")

		if got := Fold("c0", []Op{create, early, late, merge}).Title; got != "late" {
			t.Errorf("Title = %q, want %q", got, "late")
		}
	})

	t.Run("unknown op types are skipped", func(t *testing.T) {
		t.Parallel()

		future := op("c1", "set_milestone", "2026-10-01T11:00:00Z", "c0")
		future.Value = "v2"
		closed := op("c2", OpSetState, "2026-10-01T12:00:00Z", "c1")
		closed.Value = StateClosed

		rec := Fold("c0", []Op{create, future, closed})
		if rec.State != StateClosed || rec.Title != "Login fails" {
			t.Errorf("unexpected record: %+v", rec)
		}
	})

	t.Run("an empty chain folds to an open record with empty slices", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", nil)
		if rec.State != StateOpen || rec.Labels == nil || rec.Comments == nil {
			t.Errorf("unexpected record: %+v", rec)
		}
	})
}

func TestDecodeOp(t *testing.T) {
	t.Parallel()

	t.Run("valid payload decodes and carries the commit identity", func(t *testing.T) {
		t.Parallel()

		got, ok := DecodeOp("abc", []string{"p"}, []byte(`{"v":1,"type":"add_label","at":"2026-10-01T10:00:00Z","value":"ui"}`))
		if !ok || got.Type != OpAddLabel || got.Value != "ui" || got.ID != "abc" || len(got.Parents) != 1 {
			t.Errorf("got %+v ok=%v", got, ok)
		}
	})

	for name, payload := range map[string]string{
		"invalid JSON":    `{not json`,
		"unknown version": `{"v":99,"type":"create"}`,
		"missing payload": ``,
	} {
		t.Run(name+" is rejected but keeps ID and parents", func(t *testing.T) {
			t.Parallel()

			got, ok := DecodeOp("abc", []string{"p"}, []byte(payload))
			if ok || got.Type != "" || got.ID != "abc" || len(got.Parents) != 1 {
				t.Errorf("got %+v ok=%v", got, ok)
			}
		})
	}
}

func TestRecordIDs(t *testing.T) {
	t.Parallel()

	rec := Record{ID: "0123456789abcdef0123456789abcdef01234567"}

	t.Run("ShortID is the first 7 characters", func(t *testing.T) {
		t.Parallel()

		if got := rec.ShortID(); got != "0123456" {
			t.Errorf("ShortID = %q", got)
		}
	})

	t.Run("DisplayID is the short ID for an unlinked record", func(t *testing.T) {
		t.Parallel()

		if got := rec.DisplayID(); got != "0123456" {
			t.Errorf("DisplayID = %q", got)
		}
	})

	t.Run("ShortID of a short ID is the ID itself", func(t *testing.T) {
		t.Parallel()

		short := Record{ID: "abc"}
		if got := short.ShortID(); got != "abc" {
			t.Errorf("ShortID = %q", got)
		}
	})
}

func TestFold_Tracker(t *testing.T) {
	t.Parallel()

	root := op("c0", OpCreate, "2026-10-01T10:00:00Z")
	root.TrackerType, root.Project, root.TrackerID = "forgejo", "zf", "42"

	plain := op("p0", OpCreate, "2026-10-01T10:00:00Z")
	plain.Title = "Local"

	link := func(id, number string, parents ...string) Op {
		o := op(id, OpLinkTracker, "2026-10-01T11:00:00Z", parents...)
		o.TrackerType, o.Project, o.TrackerID = "forgejo", "zf", number

		return o
	}
	seen := func(id, value, status string, parents ...string) Op {
		o := op(id, OpTrackerState, "2026-10-01T12:00:00Z", parents...)
		o.Value, o.Status = value, status

		return o
	}

	t.Run("an import root links the record and marks it born in the tracker", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root})
		want := TrackerLink{Type: "forgejo", Project: "zf", ID: "42", Born: true}
		if rec.Tracker == nil || *rec.Tracker != want {
			t.Errorf("Tracker = %+v, want %+v", rec.Tracker, want)
		}
	})

	t.Run("a tracker-born record is displayed by its tracker number", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root})
		if got := rec.DisplayID(); got != "42" {
			t.Errorf("DisplayID = %q, want 42", got)
		}
	})

	t.Run("set_title after an import root gives the title", func(t *testing.T) {
		t.Parallel()

		title := op("c1", OpSetTitle, "2026-10-01T10:01:00Z", "c0")
		title.Value = "From tracker"
		if got := Fold("c0", []Op{root, title}).Title; got != "From tracker" {
			t.Errorf("Title = %q", got)
		}
	})

	t.Run("a record without a link has none", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain})
		if rec.Tracker != nil || rec.TrackerState != "" {
			t.Errorf("Tracker = %+v, TrackerState = %q", rec.Tracker, rec.TrackerState)
		}
	})

	t.Run("link_tracker links a repo-born record and keeps its short hash", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain, link("p1", "57", "p0")})
		want := TrackerLink{Type: "forgejo", Project: "zf", ID: "57"}
		if rec.Tracker == nil || *rec.Tracker != want {
			t.Errorf("Tracker = %+v, want %+v", rec.Tracker, want)
		}
		if got := rec.DisplayID(); got != "p0" {
			t.Errorf("DisplayID = %q, want p0", got)
		}
	})

	t.Run("the first link_tracker wins and the loser is recorded", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain, link("p1", "57", "p0"), link("p2", "58", "p1")})
		if rec.Tracker == nil || rec.Tracker.ID != "57" {
			t.Errorf("Tracker = %+v, want number 57", rec.Tracker)
		}
		if !slices.Equal(rec.DuplicateTrackerIDs, []string{"58"}) {
			t.Errorf("DuplicateTrackerIDs = %v, want [58]", rec.DuplicateTrackerIDs)
		}
	})

	t.Run("a repeated link_tracker for the same number is not a duplicate", func(t *testing.T) {
		t.Parallel()

		rec := Fold("p0", []Op{plain, link("p1", "57", "p0"), link("p2", "57", "p1")})
		if len(rec.DuplicateTrackerIDs) != 0 {
			t.Errorf("DuplicateTrackerIDs = %v, want none", rec.DuplicateTrackerIDs)
		}
	})

	t.Run("a link_tracker on a tracker-born record is a duplicate", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root, link("c1", "58", "c0")})
		if rec.Tracker == nil || rec.Tracker.ID != "42" || !slices.Equal(rec.DuplicateTrackerIDs, []string{"58"}) {
			t.Errorf("Tracker = %+v, duplicates = %v", rec.Tracker, rec.DuplicateTrackerIDs)
		}
	})

	t.Run("a link_tracker without a number is ignored", func(t *testing.T) {
		t.Parallel()

		if rec := Fold("p0", []Op{plain, link("p1", "", "p0")}); rec.Tracker != nil {
			t.Errorf("Tracker = %+v, want nil", rec.Tracker)
		}
	})

	t.Run("tracker_state records the state seen and the status name", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root, seen("c1", StateClosed, "Rejected", "c0")})
		if rec.TrackerState != StateClosed || rec.TrackerStatus != "Rejected" {
			t.Errorf("TrackerState = %q, TrackerStatus = %q", rec.TrackerState, rec.TrackerStatus)
		}
	})

	t.Run("tracker_state does not change the issue state", func(t *testing.T) {
		t.Parallel()

		if got := Fold("c0", []Op{root, seen("c1", StateClosed, "", "c0")}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}
	})

	t.Run("a tracker_state with an unknown value is ignored", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{root, seen("c1", "resolved", "Resolved", "c0")})
		if rec.TrackerState != "" || rec.TrackerStatus != "" {
			t.Errorf("TrackerState = %q, TrackerStatus = %q", rec.TrackerState, rec.TrackerStatus)
		}
	})
}
