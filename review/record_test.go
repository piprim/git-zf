package review

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

func op(id, typ, at string, parents ...string) Op {
	return Op{V: OpVersion, ID: id, Type: typ, At: at, Author: "dev <dev@test.com>", Parents: parents}
}

func request(id, at, sha string, parents ...string) Op {
	o := op(id, OpRequest, at, parents...)
	o.FeatureSHA = sha

	return o
}

func approve(id, at, sha, author string, hasCommits bool, parents ...string) Op {
	o := op(id, OpApprove, at, parents...)
	o.ApprovedSHA, o.Author, o.HasCommits = sha, author, hasCommits

	return o
}

func reject(id, at, comment string, hasCommits bool, parents ...string) Op {
	o := op(id, OpReject, at, parents...)
	o.Comment, o.HasCommits = comment, hasCommits

	return o
}

func withRound(o Op, round int) Op {
	o.Round = round

	return o
}

func approvedSHAs(st State) []string {
	shas := []string{}
	for _, a := range st.Approvals {
		shas = append(shas, a.ApprovedSHA)
	}
	slices.Sort(shas)

	return shas
}

func TestFold(t *testing.T) {
	t.Parallel()

	req := request("r1", t0, "f1")

	t.Run("an empty chain has no status and round 0", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", nil)
		if st.Slug != "42" || st.Status != "" || st.Round != 0 || st.Closed {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("request opens round 1 in review", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{req})
		if st.Status != StatusInReview || st.Round != 1 || st.FeatureSHA != "f1" {
			t.Errorf("state = %+v", st)
		}
		if st.UpdatedAt.IsZero() {
			t.Error("UpdatedAt is zero")
		}
	})

	t.Run("start records the first op author as reviewer", func(t *testing.T) {
		t.Parallel()

		s1 := op("s1", OpStart, t1, "r1")
		s1.Author = "alice"
		s2 := op("s2", OpStart, t2, "s1")
		s2.Author = "bob"

		if got := Fold("42", []Op{req, s1, s2}).Reviewer; got != "alice" {
			t.Errorf("Reviewer = %q, want alice", got)
		}
	})

	t.Run("start, approve and reject before any request are ignored", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			op("s0", OpStart, t0),
			approve("a0", t1, "f1", "alice", true, "s0"),
			reject("j0", t2, "no", true, "a0"),
		})
		if st.Status != "" || st.Reviewer != "" || len(st.Approvals) != 0 || st.Comment != "" || st.HasCommits {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("approve moves to approved and records the approval", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{req, approve("a1", t1, "f1", "alice", false, "r1")})
		if st.Status != StatusApproved || len(st.Approvals) != 1 {
			t.Fatalf("state = %+v", st)
		}
		want := Approval{Commit: "a1", Author: "alice", ApprovedSHA: "f1"}
		if st.Approvals[0] != want {
			t.Errorf("approval = %+v, want %+v", st.Approvals[0], want)
		}
	})

	t.Run("reject moves to changes_requested, keeps the comment and voids approvals", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			approve("a1", t1, "f1", "alice", false, "r1"),
			reject("j1", t2, "fix the tests", true, "a1"),
		})
		if st.Status != StatusChangesRequested || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
		if st.Comment != "fix the tests" || !st.HasCommits {
			t.Errorf("Comment = %q, HasCommits = %v", st.Comment, st.HasCommits)
		}
	})

	t.Run("a new request starts the next round and clears the previous one", func(t *testing.T) {
		t.Parallel()

		s1 := op("s1", OpStart, t1, "r1")
		s1.Author = "alice"
		st := Fold("42", []Op{
			req, s1,
			reject("j1", t2, "fix the tests", true, "s1"),
			request("r2", t3, "f2", "j1"),
		})
		if st.Status != StatusInReview || st.Round != 2 || st.FeatureSHA != "f2" {
			t.Errorf("state = %+v", st)
		}
		if st.Reviewer != "" || st.Comment != "" || st.HasCommits || len(st.Approvals) != 0 {
			t.Errorf("round 1 leaked into round 2: %+v", st)
		}
	})

	t.Run("two concurrent requests count as one round", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			request("ra", t0, "f1"),
			request("rb", t0, "f1"),
			op("m", OpMerge, t1, "ra", "rb"),
		})
		if st.Round != 1 || st.Status != StatusInReview {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("two concurrent approvals are both kept and has_commits is ORed", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			approve("a1", t1, "f1", "alice", false, "r1"),
			approve("b1", t1, "f9", "bob", true, "r1"),
			op("m", OpMerge, t2, "a1", "b1"),
		})
		if st.Status != StatusApproved || !st.HasCommits {
			t.Errorf("state = %+v", st)
		}
		if got := approvedSHAs(st); !slices.Equal(got, []string{"f1", "f9"}) {
			t.Errorf("approved SHAs = %v", got)
		}
	})

	t.Run("a reject wins over a concurrent approve sorted before it", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			approve("a1", t1, "f1", "alice", false, "r1"),
			reject("j1", t2, "no", false, "r1"),
		})
		if st.Status != StatusChangesRequested || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("a reject wins over a concurrent approve sorted after it", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			reject("j1", t1, "no", false, "r1"),
			approve("a1", t2, "f1", "alice", false, "r1"),
		})
		if st.Status != StatusChangesRequested || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("two concurrent rejects keep both comments", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{
			req,
			reject("j1", t1, "first", false, "r1"),
			reject("j2", t2, "second", true, "r1"),
		})
		if st.Comment != "first\n\nsecond" || !st.HasCommits {
			t.Errorf("Comment = %q, HasCommits = %v", st.Comment, st.HasCommits)
		}
	})

	closed := []Op{req, approve("a1", t1, "f1", "alice", false, "r1"), op("c1", OpClose, t2, "a1")}

	t.Run("close marks the review closed", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", closed)
		if !st.Closed || st.Status != StatusApproved {
			t.Errorf("after close: %+v", st)
		}
	})

	t.Run("a request after close reopens the review", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", append(slices.Clone(closed), request("r2", t3, "f2", "c1")))
		if st.Closed || st.Round != 2 || st.Status != StatusInReview {
			t.Errorf("after reopen: %+v", st)
		}
	})

	t.Run("a start after an approve does not change the reviewer", func(t *testing.T) {
		t.Parallel()

		s1 := op("s1", OpStart, t2, "a1")
		s1.Author = "alice"
		st := Fold("42", []Op{req, approve("a1", t1, "f1", "bob", false, "r1"), s1})
		if st.Reviewer != "" {
			t.Errorf("Reviewer = %q, want empty", st.Reviewer)
		}
	})

	t.Run("a start after a reject does not change the reviewer", func(t *testing.T) {
		t.Parallel()

		s1 := op("s1", OpStart, t2, "j1")
		s1.Author = "alice"
		st := Fold("42", []Op{req, reject("j1", t1, "no", false, "r1"), s1})
		if st.Reviewer != "" {
			t.Errorf("Reviewer = %q, want empty", st.Reviewer)
		}
	})

	// Round 1 was rejected and round 2 requested; the stale op below was
	// written on top of request 1 (offline reviewer) and dated after request 2.
	round2 := []Op{
		req,
		withRound(reject("j1", t1, "no", false, "r1"), 1),
		request("r2", t2, "f2", "j1"),
	}

	t.Run("a stale approve of round 1 is ignored in round 2", func(t *testing.T) {
		t.Parallel()

		stale := withRound(approve("a1", t3, "f1", "alice", false, "r1"), 1)
		st := Fold("42", append(slices.Clone(round2), stale))
		if st.Status != StatusInReview || st.Round != 2 || len(st.Approvals) != 0 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("a stale reject of round 1 is ignored in round 2", func(t *testing.T) {
		t.Parallel()

		stale := withRound(reject("j9", t3, "late", false, "r1"), 1)
		st := Fold("42", append(slices.Clone(round2), stale))
		if st.Status != StatusInReview || st.Round != 2 || st.Comment != "" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("a stale start of round 1 does not set the reviewer of round 2", func(t *testing.T) {
		t.Parallel()

		stale := withRound(op("s1", OpStart, t3, "r1"), 1)
		stale.Author = "alice"
		st := Fold("42", append(slices.Clone(round2), stale))
		if st.Reviewer != "" {
			t.Errorf("Reviewer = %q, want empty", st.Reviewer)
		}
	})

	t.Run("an op with round 0 applies to the current round", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", append(slices.Clone(round2), approve("a2", t3, "f2", "alice", false, "r2")))
		if st.Status != StatusApproved || st.Round != 2 || len(st.Approvals) != 1 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("an op whose round matches applies", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", append(slices.Clone(round2), withRound(approve("a2", t3, "f2", "alice", false, "r2"), 2)))
		if st.Status != StatusApproved || st.Round != 2 || len(st.Approvals) != 1 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("unknown types and merge ops change nothing", func(t *testing.T) {
		t.Parallel()

		st := Fold("42", []Op{req, op("u1", "assign", t1, "r1"), op("m1", OpMerge, t2, "u1")})
		base := Fold("42", []Op{req})
		if st.Status != base.Status || st.Round != base.Round || !st.UpdatedAt.Equal(base.UpdatedAt) {
			t.Errorf("state = %+v, want %+v", st, base)
		}
	})

	t.Run("every round is kept with its decision and times", func(t *testing.T) {
		t.Parallel()

		start := op("s1", OpStart, t1, "r1")
		start.Author = "alice"
		ops := []Op{
			req, start,
			reject("x1", t2, "no", true, "s1"),
			request("r2", t3, "f2", "x1"),
		}

		rounds := Fold("42", ops).Rounds
		if len(rounds) != 2 {
			t.Fatalf("rounds = %+v", rounds)
		}
		first, second := rounds[0], rounds[1]
		if first.Round != 1 || first.Status != StatusChangesRequested || first.Reviewer != "alice" || !first.HasCommits {
			t.Errorf("round 1 = %+v", first)
		}
		if first.OpenedAt.Format("15:04") != "10:00" || first.ResolvedAt.Format("15:04") != "12:00" {
			t.Errorf("round 1 times = %v, %v", first.OpenedAt, first.ResolvedAt)
		}
		if second.Round != 2 || second.Status != StatusInReview || second.Reviewer != "" || !second.ResolvedAt.IsZero() {
			t.Errorf("round 2 = %+v", second)
		}
	})

	t.Run("a close before any request records no round", func(t *testing.T) {
		t.Parallel()

		if rounds := Fold("42", []Op{op("c1", OpClose, t0)}).Rounds; len(rounds) != 0 {
			t.Errorf("rounds = %+v", rounds)
		}
	})

	t.Run("slice order does not matter", func(t *testing.T) {
		t.Parallel()

		a := approve("a1", t1, "f1", "alice", false, "r1")
		if got := Fold("42", []Op{a, req}).Status; got != StatusApproved {
			t.Errorf("Status = %q", got)
		}
	})
}

func TestDecodeOp(t *testing.T) {
	t.Parallel()

	t.Run("a valid payload decodes and carries the commit identity", func(t *testing.T) {
		t.Parallel()

		payload := []byte(`{"v":1,"type":"approve","at":"2026-10-01T10:00:00Z","author":"a","approved_sha":"f1","has_commits":true}`)
		got, ok := DecodeOp("c1", []string{"p1"}, payload)
		if !ok || got.Type != OpApprove || got.ApprovedSHA != "f1" || !got.HasCommits {
			t.Errorf("op = %+v, ok = %v", got, ok)
		}
		if got.ID != "c1" || !slices.Equal(got.Parents, []string{"p1"}) {
			t.Errorf("identity = %q %v", got.ID, got.Parents)
		}
	})

	for name, payload := range map[string][]byte{
		"invalid JSON":       []byte("not json"),
		"an unknown version": []byte(`{"v":99,"type":"approve"}`),
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
