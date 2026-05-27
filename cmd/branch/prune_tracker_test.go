package branch

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
)

func TestExtractIssueID(t *testing.T) {
	cases := []struct {
		name      string
		branch    string
		wantID    string
		wantFound bool
	}{
		{"github-style prefixed", "ABC-42@feat@add-oauth-login@550e8400", "ABC-42", true},
		{"redmine-style numeric @", "42@feat@something", "42", true},
		{"redmine-style numeric -", "42-feat-something", "42", true},
		{"github-style with pipe", "ABC-7|spike|try", "ABC-7", true},
		{"unparseable: no delimiter", "main", "", false},
		{"unparseable: empty", "", "", false},
		{"unparseable: just delimiters", "@@@", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, ok := extractIssueID(tc.branch)
			if ok != tc.wantFound {
				t.Fatalf("ok = %v, want %v (id=%q)", ok, tc.wantFound, id)
			}
			if id != tc.wantID {
				t.Fatalf("id = %q, want %q", id, tc.wantID)
			}
		})
	}
}

// fakeTrackerPruner satisfies trackerPruner without a real git repo.
type fakeTrackerPruner struct {
	base    string
	locals  []string
	baseErr error
}

func (f *fakeTrackerPruner) DefaultBaseBranch() (string, error)  { return f.base, f.baseErr }
func (f *fakeTrackerPruner) LocalBranchNames() ([]string, error) { return f.locals, nil }
func (f *fakeTrackerPruner) SafeDeleteBranch(string) error       { return nil }
func (f *fakeTrackerPruner) ForceDeleteBranch(string) error      { return nil }

// fakeIssueResolver lets discovery tests stub IsIssueClosed without spinning
// up tracker/fake.
type fakeIssueResolver struct {
	closed  map[string]bool
	errs    map[string]error
	unknown map[string]bool
}

func (f *fakeIssueResolver) IsIssueClosed(_ context.Context, id string) (bool, error) {
	if err, ok := f.errs[id]; ok && err != nil {
		return false, err
	}
	if f.unknown[id] {
		return false, tracker.ErrIssueNotFound
	}

	return f.closed[id], nil
}

func TestRunDiscoverTracker(t *testing.T) {
	storeByName := map[string]*store.BranchRow{
		"ABC-42@feat@x": {IssueSlug: "ABC-42", BranchName: "ABC-42@feat@x"},
		"ABC-51@feat@y": {IssueSlug: "ABC-51", BranchName: "ABC-51@feat@y"},
	}

	t.Run("returns only branches whose extracted ID is closed", func(t *testing.T) {
		pr := &fakeTrackerPruner{base: "master", locals: []string{
			"master",
			"ABC-42@feat@x",    // closed
			"ABC-51@feat@y",    // open
			"ABC-77@spike@z",   // closed, no store row
			"no-issue-id-here", // regex miss
		}}
		tr := &fakeIssueResolver{closed: map[string]bool{"ABC-42": true, "ABC-77": true}}

		result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, storeByName, "master")
		if err != nil {
			t.Fatalf("err: %v", err)
		}

		if len(result.Candidates) != 2 {
			t.Fatalf("got %d candidates, want 2: %#v", len(result.Candidates), result.Candidates)
		}
		// Sorted by branch name → ABC-42 first, ABC-77 second.
		if result.Candidates[0].BranchName != "ABC-42@feat@x" || result.Candidates[0].StoreRow == nil {
			t.Fatalf("c[0] = %+v", result.Candidates[0])
		}
		if result.Candidates[1].BranchName != "ABC-77@spike@z" || result.Candidates[1].StoreRow != nil {
			t.Fatalf("c[1] = %+v", result.Candidates[1])
		}
		if len(result.Warnings) != 0 {
			t.Fatalf("warnings = %v, want none", result.Warnings)
		}
	})

	t.Run("skip + warn on ErrIssueNotFound", func(t *testing.T) {
		pr := &fakeTrackerPruner{base: "master", locals: []string{"ABC-99@feat@x"}}
		tr := &fakeIssueResolver{unknown: map[string]bool{"ABC-99": true}}

		result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, nil, "master")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(result.Candidates) != 0 {
			t.Fatalf("candidates = %v, want none", result.Candidates)
		}
		if len(result.Warnings) != 1 {
			t.Fatalf("warnings = %v, want 1", result.Warnings)
		}
	})

	t.Run("skip + warn on transport error", func(t *testing.T) {
		pr := &fakeTrackerPruner{base: "master", locals: []string{"ABC-1@feat@x"}}
		tr := &fakeIssueResolver{errs: map[string]error{"ABC-1": errors.New("boom")}}

		result, err := runDiscoverTracker(context.Background(), io.Discard, pr, tr, nil, "master")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(result.Candidates) != 0 || len(result.Warnings) != 1 {
			t.Fatalf("candidates=%d warnings=%d", len(result.Candidates), len(result.Warnings))
		}
	})

	t.Run("skip base branch name even if regex extracts an ID", func(t *testing.T) {
		// Edge case: a base branch literally named e.g. "release-1.2.3" could match the regex.
		pr := &fakeTrackerPruner{base: "release-1.2.3", locals: []string{"release-1.2.3"}}
		tr := &fakeIssueResolver{closed: map[string]bool{"release": true}}

		result, _ := runDiscoverTracker(context.Background(), io.Discard, pr, tr, nil, "release-1.2.3")
		if len(result.Candidates) != 0 {
			t.Fatalf("should skip base branch, got %v", result.Candidates)
		}
	})
}

// trackingFakePruner records every delete call. Lets tests pre-program
// SafeDeleteBranch to return git.ErrBranchNotMerged for specific branches.
type trackingFakePruner struct {
	fakeTrackerPruner
	safeCalls  []string
	forceCalls []string
	safeRefuse map[string]bool // names that should return git.ErrBranchNotMerged
}

func (t *trackingFakePruner) SafeDeleteBranch(n string) error {
	t.safeCalls = append(t.safeCalls, n)
	if t.safeRefuse[n] {
		return git.ErrBranchNotMerged
	}

	return nil
}

func (t *trackingFakePruner) ForceDeleteBranch(n string) error {
	t.forceCalls = append(t.forceCalls, n)

	return nil
}

// statusFlipRecorder counts UpdateBranchStatus invocations per branch.
type statusFlipRecorder struct {
	flipped map[string]int64 // branchName → statusID
}

func (s *statusFlipRecorder) updateBranchStatus(_ context.Context, name string, statusID int64) error {
	if s.flipped == nil {
		s.flipped = map[string]int64{}
	}
	s.flipped[name] = statusID

	return nil
}

func TestRunExecuteTracker(t *testing.T) {
	t.Run("safe delete + store flip for each candidate", func(t *testing.T) {
		pr := &trackingFakePruner{}
		flip := &statusFlipRecorder{}
		cands := []trackerCandidate{
			{BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
			{BranchName: "ABC-43@feat@y", IssueID: "ABC-43", StoreRow: &store.BranchRow{BranchName: "ABC-43@feat@y"}},
		}
		decisions := map[string]string{"ABC-42@feat@x": "safe", "ABC-43@feat@y": "safe"}

		warnings, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(pr.safeCalls) != 2 {
			t.Fatalf("safeCalls = %v, want 2", pr.safeCalls)
		}
		if flip.flipped["ABC-42@feat@x"] != store.StatusIDClosed {
			t.Fatalf("flipped[ABC-42] = %d, want %d", flip.flipped["ABC-42@feat@x"], store.StatusIDClosed)
		}
		if len(warnings) != 0 {
			t.Fatalf("warnings = %v, want none", warnings)
		}
	})

	t.Run("safe-delete refusal: warn + skip store flip", func(t *testing.T) {
		pr := &trackingFakePruner{safeRefuse: map[string]bool{"ABC-42@feat@x": true}}
		flip := &statusFlipRecorder{}
		cands := []trackerCandidate{
			{BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
		}
		decisions := map[string]string{"ABC-42@feat@x": "safe"}

		warnings, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if _, ok := flip.flipped["ABC-42@feat@x"]; ok {
			t.Fatal("should NOT have flipped store row for refused branch")
		}
		if len(warnings) != 1 {
			t.Fatalf("warnings = %v, want 1", warnings)
		}
	})

	t.Run("force-delete executes regardless and flips store", func(t *testing.T) {
		pr := &trackingFakePruner{}
		flip := &statusFlipRecorder{}
		cands := []trackerCandidate{
			{BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
		}
		decisions := map[string]string{"ABC-42@feat@x": "force"}

		if _, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions); err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(pr.forceCalls) != 1 || pr.forceCalls[0] != "ABC-42@feat@x" {
			t.Fatalf("forceCalls = %v", pr.forceCalls)
		}
		if flip.flipped["ABC-42@feat@x"] != store.StatusIDClosed {
			t.Fatalf("flipped[ABC-42] = %d, want %d", flip.flipped["ABC-42@feat@x"], store.StatusIDClosed)
		}
	})

	t.Run("skip: no ref action, store still flipped", func(t *testing.T) {
		pr := &trackingFakePruner{}
		flip := &statusFlipRecorder{}
		cands := []trackerCandidate{
			{BranchName: "ABC-42@feat@x", IssueID: "ABC-42", StoreRow: &store.BranchRow{BranchName: "ABC-42@feat@x"}},
		}
		decisions := map[string]string{"ABC-42@feat@x": "skip"}

		if _, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions); err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(pr.safeCalls)+len(pr.forceCalls) != 0 {
			t.Fatalf("expected no delete calls; safe=%v force=%v", pr.safeCalls, pr.forceCalls)
		}
		if flip.flipped["ABC-42@feat@x"] != store.StatusIDClosed {
			t.Fatalf("flipped[ABC-42] = %d, want %d", flip.flipped["ABC-42@feat@x"], store.StatusIDClosed)
		}
	})

	t.Run("candidate with nil StoreRow: ref action only, no store call", func(t *testing.T) {
		pr := &trackingFakePruner{}
		flip := &statusFlipRecorder{}
		cands := []trackerCandidate{
			{BranchName: "rogue@feat@x", IssueID: "rogue", StoreRow: nil},
		}
		decisions := map[string]string{"rogue@feat@x": "force"}

		if _, err := runExecuteTracker(context.Background(), io.Discard, pr, flip.updateBranchStatus, cands, decisions); err != nil {
			t.Fatalf("err: %v", err)
		}
		if len(flip.flipped) != 0 {
			t.Fatalf("flipped = %v, want empty", flip.flipped)
		}
	})
}
