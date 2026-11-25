package issueflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/branch/branchtest"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/review"
)

// absentSHA is a well-formed SHA no object has.
const absentSHA = "1234567890123456789012345678901234567890"

// tipOf returns the commit ref points at.
func tipOf(t *testing.T, c *git.Client, ref string) string {
	t.Helper()

	h, err := c.ResolveRef(ref)
	if err != nil {
		t.Fatalf("resolve %s: %v", ref, err)
	}

	return h.String()
}

func TestRemoteOf(t *testing.T) {
	t.Parallel()

	const name = "7@feat@x"
	commit := func(run func(...string), msg string) { run("commit", "-q", "--allow-empty", "-m", msg) }
	// pushed leaves name with two commits of its own, checked out and pushed.
	pushed := func(run func(...string)) {
		run("checkout", "-q", "-b", name)
		commit(run, "feat: one")
		commit(run, "feat: two")
		run("push", "-q", "origin", name)
	}

	cases := []struct {
		label   string
		origin  bool
		setup   func(run func(...string))
		want    RemoteRelation
		wantRef string
	}{
		{"no remote", false, func(run func(...string)) { run("branch", name) }, RemoteNoRemote, ""},
		{"not pushed", true, func(run func(...string)) { run("branch", name) }, RemoteNotPushed, "origin/" + name},
		{"neither local nor on the remote", true, func(func(...string)) {}, RemoteNotPushed, "origin/" + name},
		{"on the remote, no local branch", true, func(run func(...string)) {
			pushed(run)
			run("checkout", "-q", "main")
			run("branch", "-q", "-D", name)
		}, RemoteLocalMissing, "origin/" + name},
		{"same", true, pushed, RemoteSame, "origin/" + name},
		{"local ahead", true, func(run func(...string)) {
			pushed(run)
			commit(run, "feat: three")
		}, RemoteLocalAhead, "origin/" + name},
		{"behind", true, func(run func(...string)) {
			pushed(run)
			run("reset", "-q", "--hard", "HEAD~1")
		}, RemoteBehind, "origin/" + name},
		{"diverged", true, func(run func(...string)) {
			pushed(run)
			run("reset", "-q", "--hard", "HEAD~1")
			commit(run, "feat: other")
		}, RemoteDiverged, "origin/" + name},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			origin := ""
			if tc.origin {
				origin = newBareOrigin(t)
			}
			c, run := newChainRepo(t, origin)
			tc.setup(run)

			rel, ref, err := RemoteOf(t.Context(), c, name)
			if err != nil {
				t.Fatalf("RemoteOf: %v", err)
			}
			if rel != tc.want {
				t.Errorf("relation = %d, want %d", rel, tc.want)
			}
			if ref != tc.wantRef {
				t.Errorf("remoteRef = %q, want %q", ref, tc.wantRef)
			}
		})
	}
}

func TestReviewedBranch(t *testing.T) {
	t.Parallel()

	c, _ := newChainRepo(t, "")
	branchtest.Seed(t, c, branch.Op{Branch: "7@feat@x"}, branch.StatusInProgress)

	t.Run("the recorded branch wins", func(t *testing.T) {
		t.Parallel()

		got, err := ReviewedBranch(t.Context(), c, &review.State{Slug: "7", Branch: "7@feat@x@v2"})
		if err != nil || got != "7@feat@x@v2" {
			t.Errorf("ReviewedBranch = %q, %v; want 7@feat@x@v2", got, err)
		}
	})

	t.Run("no recorded branch: the issue's tracked branch", func(t *testing.T) {
		t.Parallel()

		got, err := ReviewedBranch(t.Context(), c, &review.State{Slug: "7"})
		if err != nil || got != "7@feat@x" {
			t.Errorf("ReviewedBranch = %q, %v; want 7@feat@x", got, err)
		}
	})

	t.Run("BranchNameForIssue: an issue without a branch", func(t *testing.T) {
		t.Parallel()

		got, err := BranchNameForIssue(t.Context(), c, "404")
		if err != nil || got != "" {
			t.Errorf("BranchNameForIssue = %q, %v; want empty", got, err)
		}
	})
}

// approvalRepo builds main: c0 - c1 - c2, and side: c0 - cX, with a merge of
// c2 and cX on "both".
func approvalRepo(t *testing.T) (c *git.Client, c1, c2, cX, merged string) {
	t.Helper()

	c, run := newChainRepo(t, "")
	run("branch", "side")
	run("commit", "-q", "--allow-empty", "-m", "feat: one")
	c1 = tipOf(t, c, "HEAD")
	run("commit", "-q", "--allow-empty", "-m", "feat: two")
	c2 = tipOf(t, c, "HEAD")
	run("checkout", "-q", "side")
	run("commit", "-q", "--allow-empty", "-m", "feat: other")
	cX = tipOf(t, c, "HEAD")
	run("checkout", "-q", "-b", "both", "main")
	run("merge", "-q", "--no-ff", "-m", "merge", "side")
	merged = tipOf(t, c, "HEAD")
	run("checkout", "-q", "main")

	return c, c1, c2, cX, merged
}

func TestApprovedCommit(t *testing.T) {
	t.Parallel()

	c, c1, c2, cX, merged := approvalRepo(t)
	of := func(shas ...string) []review.Approval {
		out := make([]review.Approval, len(shas))
		for i, s := range shas {
			out[i] = review.Approval{ApprovedSHA: s}
		}

		return out
	}

	cases := []struct {
		label     string
		approvals []review.Approval
		want      string
		wantErr   error
	}{
		{"one approval", of(c1), c1, nil},
		{"two on the same commit", of(c2, c2), c2, nil},
		{"the second contains the first", of(c1, c2), c2, nil},
		{"the first contains the second", of(c2, c1), c2, nil},
		{"a merge contains two diverged ones", of(c2, cX, merged), merged, nil},
		{"two diverged", of(c2, cX), "", ErrApprovalsDiverged},
		{"an empty SHA", of(c1, ""), "", ErrApprovalMissing},
		{"a SHA absent from the clone", of(absentSHA), "", ErrApprovalMissing},
		{"no approval", nil, "", ErrApprovalMissing},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			got, err := ApprovedCommit(t.Context(), c, tc.approvals)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("approved = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCheckApprovedTip(t *testing.T) {
	t.Parallel()

	// main is at c2: c1 is behind it, cX is elsewhere.
	c, c1, c2, cX, merged := approvalRepo(t)

	cases := []struct {
		label    string
		approved string
		wantErr  error
	}{
		{"A is not in the clone", absentSHA, ErrApprovalMissing},
		{"T is A", c2, nil},
		{"T is a strict ancestor of A", merged, ErrApprovedAhead},
		{"T is a strict descendant of A", c1, ErrCommitsAfterApproval},
		{"anything else", cX, ErrNotApprovedBranch},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			t.Parallel()

			err := CheckApprovedTip(t.Context(), c, "main", tc.approved)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if err != nil && !strings.Contains(err.Error(), tc.approved[:10]) {
				t.Errorf("message %q does not name %s", err, tc.approved[:10])
			}
		})
	}

	t.Run("no local branch is an error", func(t *testing.T) {
		t.Parallel()

		if err := CheckApprovedTip(t.Context(), c, "nope", c2); err == nil {
			t.Error("err = nil, want an error")
		}
	})
}
