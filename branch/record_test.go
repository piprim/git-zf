package branch

import (
	"slices"
	"testing"
)

const (
	t0 = "2026-10-01T10:00:00Z"
	t1 = "2026-10-01T11:00:00Z"
	t2 = "2026-10-01T12:00:00Z"
	t3 = "2026-10-01T13:00:00Z"
)

const (
	feat   = "42@feat@add-login"
	featV2 = "42@feat@add-login@v2"
)

func startOp(id, at, name string, parents ...string) Op {
	return Op{
		V: OpVersion, ID: id, Type: OpStart, At: at, Author: "dev <dev@test.com>",
		Branch: name, BranchType: "feat", Parents: parents,
	}
}

func statusOp(id, at, name, status string, parents ...string) Op {
	return Op{V: OpVersion, ID: id, Type: OpSetStatus, At: at, Branch: name, Status: status, Parents: parents}
}

func entryNames(st State) []string {
	names := []string{}
	for _, e := range st.Entries {
		names = append(names, e.Name)
	}

	return names
}

func TestFold(t *testing.T) {
	t.Parallel()

	root := startOp("s1", t0, feat)
	root.Title, root.Parent, root.TrackerType, root.IssueID = "Add login", "7", "redmine", "abc123"

	t.Run("an empty chain has no entry", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", nil)
		if st.Slug != "42" || len(st.Entries) != 0 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("start adds an in-progress entry and sets the issue fields", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{root})
		if st.Title != "Add login" || st.Parent != "7" || st.TrackerType != "redmine" || st.IssueID != "abc123" {
			t.Errorf("issue fields = %+v", st)
		}
		if len(st.Entries) != 1 {
			t.Fatalf("entries = %+v", st.Entries)
		}
		e := st.Entries[0]
		if e.Name != feat || e.Type != "feat" || e.Status != StatusInProgress || e.Author != "dev <dev@test.com>" {
			t.Errorf("entry = %+v", e)
		}
		if e.CreatedAt.Format("15:04") != "10:00" || !e.UpdatedAt.Equal(e.CreatedAt) {
			t.Errorf("times = %v, %v", e.CreatedAt, e.UpdatedAt)
		}
	})

	t.Run("a start without a branch name is ignored", func(t *testing.T) {
		t.Parallel()

		bad := startOp("s1", t0, "")
		bad.Title = "ghost"
		if st := Fold("42", []Op{bad}); len(st.Entries) != 0 || st.Title != "" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("a second start for a known branch adds no entry and reopens nothing", func(t *testing.T) {
		t.Parallel()

		ops := []Op{
			root,
			statusOp("m1", t1, feat, StatusMerged, "s1"),
			startOp("s2", t2, feat, "m1"),
		}
		st := Fold("42", ops)
		if len(st.Entries) != 1 || st.Entries[0].Status != StatusMerged {
			t.Errorf("entries = %+v", st.Entries)
		}
	})

	t.Run("two concurrent starts of two variants give two entries in start order", func(t *testing.T) {
		t.Parallel()

		// Two roots: each clone created the chain. A merge commit joins them.
		ops := []Op{
			startOp("b", t1, featV2),
			root,
			{V: OpVersion, ID: "m", Type: OpMerge, At: t2, Parents: []string{"s1", "b"}},
		}
		if got := entryNames(Fold("42", ops)); !slices.Equal(got, []string{feat, featV2}) {
			t.Errorf("entries = %v", got)
		}
	})

	for _, tc := range []struct {
		field string
		set   func(*Op, string)
		get   func(State) string
	}{
		{"title", func(o *Op, v string) { o.Title = v }, func(s State) string { return s.Title }},
		{"parent", func(o *Op, v string) { o.Parent = v }, func(s State) string { return s.Parent }},
		{"tracker type", func(o *Op, v string) { o.TrackerType = v }, func(s State) string { return s.TrackerType }},
		{"issue ID", func(o *Op, v string) { o.IssueID = v }, func(s State) string { return s.IssueID }},
	} {
		t.Run("the "+tc.field+" keeps its first non-empty value", func(t *testing.T) {
			t.Parallel()

			empty := startOp("a", t0, feat)
			first := startOp("b", t1, featV2, "a")
			tc.set(&first, "first")
			second := startOp("c", t2, "42@fix@other", "b")
			tc.set(&second, "second")

			if got := tc.get(Fold("42", []Op{empty, first, second})); got != "first" {
				t.Errorf("%s = %q, want first", tc.field, got)
			}
		})
	}

	t.Run("set_status changes the entry and its update time", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{root, statusOp("m1", t2, feat, StatusMerged, "s1")})
		e := st.Entries[0]
		if e.Status != StatusMerged || e.UpdatedAt.Format("15:04") != "12:00" || e.CreatedAt.Format("15:04") != "10:00" {
			t.Errorf("entry = %+v", e)
		}
	})

	t.Run("set_status in_progress reopens a merged branch", func(t *testing.T) {
		t.Parallel()

		ops := []Op{
			root,
			statusOp("m1", t1, feat, StatusMerged, "s1"),
			statusOp("r1", t2, feat, StatusInProgress, "m1"),
		}
		if got := Fold("42", ops).Entries[0].Status; got != StatusInProgress {
			t.Errorf("status = %q", got)
		}
	})

	for name, order := range map[string][2]string{
		"merged sorts last": {StatusClosed, StatusMerged},
		"closed sorts last": {StatusMerged, StatusClosed},
	} {
		t.Run("of two concurrent set_status the later wins: "+name, func(t *testing.T) {
			t.Parallel()

			// Both written on top of the root, on two clones.
			ops := []Op{
				statusOp("y", t2, feat, order[1], "s1"),
				root,
				statusOp("x", t1, feat, order[0], "s1"),
			}
			if got := Fold("42", ops).Entries[0].Status; got != order[1] {
				t.Errorf("status = %q, want %q", got, order[1])
			}
		})
	}

	t.Run("set_status for an unknown branch is ignored", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{root, statusOp("m1", t1, featV2, StatusMerged, "s1")})
		if len(st.Entries) != 1 || st.Entries[0].Status != StatusInProgress {
			t.Errorf("entries = %+v", st.Entries)
		}
	})

	t.Run("set_status with an unknown status is ignored", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{root, statusOp("m1", t1, feat, "archived", "s1")})
		if st.Entries[0].Status != StatusInProgress {
			t.Errorf("status = %q", st.Entries[0].Status)
		}
	})

	t.Run("unknown types and merge ops change nothing", func(t *testing.T) {
		t.Parallel()

		ops := []Op{
			root,
			{V: OpVersion, ID: "u1", Type: "rename", At: t1, Branch: feat, Parents: []string{"s1"}},
			{V: OpVersion, ID: "m1", Type: OpMerge, At: t2, Parents: []string{"u1"}},
		}
		st := Fold("42", ops)
		if len(st.Entries) != 1 || st.Entries[0].Status != StatusInProgress || !st.Entries[0].UpdatedAt.Equal(st.Entries[0].CreatedAt) {
			t.Errorf("entries = %+v", st.Entries)
		}
	})

	t.Run("slice order does not matter", func(t *testing.T) {
		t.Parallel()

		ops := []Op{
			statusOp("m1", t3, featV2, StatusMerged, "s2"),
			startOp("s2", t1, featV2, "s1"),
			root,
		}
		st := Fold("42", ops)
		if !slices.Equal(entryNames(st), []string{feat, featV2}) || st.Entries[1].Status != StatusMerged {
			t.Errorf("entries = %+v", st.Entries)
		}
	})
}

func TestDecodeOp(t *testing.T) {
	t.Parallel()

	t.Run("a valid payload decodes and carries the commit identity", func(t *testing.T) {
		t.Parallel()

		payload := []byte(`{"v":1,"type":"start","at":"2026-10-01T10:00:00Z","branch":"42@feat@x","parent":"7"}`)
		got, ok := DecodeOp("c1", []string{"p1"}, payload)
		if !ok || got.Type != OpStart || got.Branch != "42@feat@x" || got.Parent != "7" {
			t.Errorf("op = %+v, ok = %v", got, ok)
		}
		if got.ID != "c1" || !slices.Equal(got.Parents, []string{"p1"}) {
			t.Errorf("identity = %q %v", got.ID, got.Parents)
		}
	})

	for name, payload := range map[string][]byte{
		"invalid JSON":       []byte("not json"),
		"an unknown version": []byte(`{"v":99,"type":"start"}`),
		"a missing payload":  nil,
	} {
		t.Run(name+" is rejected but keeps ID and parents", func(t *testing.T) {
			t.Parallel()

			got, ok := DecodeOp("c1", []string{"p1"}, payload)
			if ok || got.Type != "" || got.ID != "c1" || !slices.Equal(got.Parents, []string{"p1"}) {
				t.Errorf("op = %+v, ok = %v", got, ok)
			}
		})
	}
}

func TestRows(t *testing.T) {
	t.Parallel()

	states := []State{
		Fold("42", []Op{
			func() Op { o := startOp("s1", t0, feat); o.Title = "Add login"; return o }(),
			startOp("s2", t2, featV2, "s1"),
			statusOp("m1", t3, feat, StatusMerged, "s2"),
		}),
		Fold("7", []Op{startOp("p1", t1, "7@feat@big-thing")}),
	}

	t.Run("StatusAll returns every branch, newest first", func(t *testing.T) {
		t.Parallel()

		names := []string{}
		for _, r := range Rows(states, StatusAll) {
			names = append(names, r.BranchName)
		}
		if !slices.Equal(names, []string{featV2, "7@feat@big-thing", feat}) {
			t.Errorf("rows = %v", names)
		}
	})

	t.Run("a status keeps only the branches in it", func(t *testing.T) {
		t.Parallel()

		rows := Rows(states, StatusMerged)
		if len(rows) != 1 || rows[0].BranchName != feat || rows[0].IssueSlug != "42" || rows[0].Title != "Add login" {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("a chain without a title takes it from the branch name", func(t *testing.T) {
		t.Parallel()

		rows := Rows(states[1:], StatusAll)
		if len(rows) != 1 || rows[0].Title != "big thing" {
			t.Errorf("rows = %+v", rows)
		}
	})

	t.Run("no state gives an empty, non-nil slice", func(t *testing.T) {
		t.Parallel()

		if rows := Rows(nil, StatusAll); rows == nil || len(rows) != 0 {
			t.Errorf("rows = %#v", rows)
		}
	})

	t.Run("Children returns the states whose parent is the slug", func(t *testing.T) {
		t.Parallel()

		child := startOp("c1", t0, "7.1@feat@part")
		child.Parent = "7"
		all := append(slices.Clone(states), Fold("7.1", []Op{child}))

		got := Children(all, "7")
		if len(got) != 1 || got[0].Slug != "7.1" {
			t.Errorf("children = %+v", got)
		}
	})
}
