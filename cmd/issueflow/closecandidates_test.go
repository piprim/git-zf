package issueflow

import (
	"context"
	"errors"
	"testing"

	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// fakeCandStore implements CandidateStore with canned data.
type fakeCandStore struct {
	inProgress []store.BranchRow
	all        []store.BranchRow
	listErr    error
	inserted   []insertedIssue
}

type insertedIssue struct {
	issue  store.Issue
	branch store.Branch
}

func (f *fakeCandStore) ListBranches(_ context.Context, status store.BranchStatus) ([]store.BranchRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	if status == store.BranchStatusAll {
		return f.all, nil
	}
	return f.inProgress, nil
}

func (f *fakeCandStore) InsertIssueWithBranch(_ context.Context, issue *store.Issue, b *store.Branch) error {
	f.inserted = append(f.inserted, insertedIssue{issue: *issue, branch: *b})
	// Mirror a real insert: the branch becomes visible with a fresh IssueID.
	f.all = append(f.all, store.BranchRow{
		IssueID: int64(len(f.all) + 1), IssueSlug: issue.IDSlug, Title: issue.Title,
		BranchName: b.Name, Type: b.Type, Status: store.BranchStatusInProgress,
	})
	return nil
}

// fakeCandClient implements CandidateClient with canned data.
type fakeCandClient struct {
	refs         []git.BranchRef
	refsErr      error
	unresolvable map[string]bool           // branch names that fail ResolveBranchRef
	localExists  map[string]bool           // branch names present as local refs
	created      []string                  // names passed to CreateLocalBranch
	bySlug       map[string]*git.BranchRef // for ReadBranchRef
}

func (f *fakeCandClient) ListBranchRefs(_ context.Context) ([]git.BranchRef, error) {
	return f.refs, f.refsErr
}
func (f *fakeCandClient) ReadBranchRef(_ context.Context, issueSlug string) (*git.BranchRef, error) {
	return f.bySlug[issueSlug], nil
}
func (f *fakeCandClient) ResolveBranchRef(name string) (plumbing.Hash, error) {
	if f.unresolvable[name] {
		return plumbing.ZeroHash, errors.New("not found")
	}
	return plumbing.ZeroHash, nil
}
func (f *fakeCandClient) BranchExists(name string) (bool, error) { return f.localExists[name], nil }
func (f *fakeCandClient) CreateLocalBranch(_ context.Context, name, _ string) error {
	f.created = append(f.created, name)
	return nil
}

func TestCloseCandidates(t *testing.T) {
	t.Parallel()

	t.Run("store-only when no refs", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{inProgress: []store.BranchRow{{IssueID: 1, IssueSlug: "A", BranchName: "A@feat@x"}}}
		c := &fakeCandClient{refs: nil}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 1 || got[0].IssueSlug != "A" {
			t.Fatalf("got %+v, want single store row A", got)
		}
	})

	t.Run("ref-only synthesizes untracked rows", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{inProgress: nil}
		c := &fakeCandClient{refs: []git.BranchRef{
			{IssueSlug: "B", BranchName: "B@feat@thing", CreatedAt: "2026-07-21T10:00:00Z"},
		}}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("got %d rows, want 1: %+v", len(got), got)
		}
		if got[0].IssueID != 0 {
			t.Errorf("IssueID = %d, want 0 (untracked marker)", got[0].IssueID)
		}
		if got[0].Type != "feat" || got[0].Title != "thing" {
			t.Errorf("Type/Title = %q/%q, want feat/thing", got[0].Type, got[0].Title)
		}
	})

	t.Run("union dedupes by slug (store wins)", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{inProgress: []store.BranchRow{{IssueID: 7, IssueSlug: "C", BranchName: "C@feat@x"}}}
		c := &fakeCandClient{refs: []git.BranchRef{{IssueSlug: "C", BranchName: "C@feat@x"}}}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 1 || got[0].IssueID != 7 {
			t.Fatalf("got %+v, want single tracked row with IssueID 7", got)
		}
	})

	t.Run("merged ref excluded", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{refs: []git.BranchRef{{IssueSlug: "D", BranchName: "D@feat@x", Merged: true}}}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no candidates (merged excluded)", got)
		}
	})

	t.Run("unresolvable feature branch excluded", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			refs:         []git.BranchRef{{IssueSlug: "E", BranchName: "E@feat@x"}},
			unresolvable: map[string]bool{"E@feat@x": true},
		}
		got, err := CloseCandidates(t.Context(), s, c)
		if err != nil {
			t.Fatalf("CloseCandidates: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("got %+v, want no candidates (unresolvable excluded)", got)
		}
	})
}

func TestMaterializeAndTrack(t *testing.T) {
	t.Parallel()

	t.Run("store-derived pick returned unchanged", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{}
		picked := store.BranchRow{IssueID: 5, IssueSlug: "A", BranchName: "A@feat@x", Type: "feat"}
		got, err := MaterializeAndTrack(t.Context(), s, c, picked)
		if err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if got.IssueID != 5 {
			t.Errorf("IssueID = %d, want 5 (unchanged)", got.IssueID)
		}
		if len(s.inserted) != 0 || len(c.created) != 0 {
			t.Errorf("expected no insert/create for tracked pick; inserted=%v created=%v", s.inserted, c.created)
		}
	})

	t.Run("ref-derived pick materializes and tracks", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			localExists: map[string]bool{}, // feature branch absent locally
			bySlug: map[string]*git.BranchRef{
				"B": {IssueSlug: "B", BranchName: "B@feat@thing", TrackerType: "github"},
			},
		}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		got, err := MaterializeAndTrack(t.Context(), s, c, picked)
		if err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if got.IssueID == 0 {
			t.Errorf("IssueID = 0, want non-zero after track")
		}
		if len(c.created) != 1 || c.created[0] != "B@feat@thing" {
			t.Errorf("created = %v, want [B@feat@thing]", c.created)
		}
		if len(s.inserted) != 1 {
			t.Fatalf("inserted len = %d, want 1", len(s.inserted))
		}
		if got.IssueID != s.all[len(s.all)-1].IssueID {
			t.Errorf("returned IssueID %d does not match tracked row", got.IssueID)
		}
	})

	t.Run("tracker type carried from ref", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			localExists: map[string]bool{"B@feat@thing": true}, // already local; skip create
			bySlug:      map[string]*git.BranchRef{"B": {IssueSlug: "B", BranchName: "B@feat@thing", TrackerType: "github"}},
		}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		if _, err := MaterializeAndTrack(t.Context(), s, c, picked); err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if len(s.inserted) != 1 {
			t.Fatalf("inserted len = %d, want 1", len(s.inserted))
		}
		tt := s.inserted[0].issue.TrackerType
		if tt == nil || *tt != "github" {
			t.Errorf("issue TrackerType = %v, want *github", tt)
		}
	})

	t.Run("empty tracker type inserts nil (manual)", func(t *testing.T) {
		t.Parallel()
		s := &fakeCandStore{}
		c := &fakeCandClient{
			localExists: map[string]bool{"B@feat@thing": true},
			bySlug:      map[string]*git.BranchRef{"B": {IssueSlug: "B", BranchName: "B@feat@thing"}},
		}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		if _, err := MaterializeAndTrack(t.Context(), s, c, picked); err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if s.inserted[0].issue.TrackerType != nil {
			t.Errorf("issue TrackerType = %v, want nil (manual)", *s.inserted[0].issue.TrackerType)
		}
	})

	t.Run("idempotent: already-tracked branch not double-inserted", func(t *testing.T) {
		t.Parallel()
		existing := store.BranchRow{IssueID: 3, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat"}
		s := &fakeCandStore{all: []store.BranchRow{existing}}
		c := &fakeCandClient{localExists: map[string]bool{"B@feat@thing": true}}
		picked := store.BranchRow{IssueID: 0, IssueSlug: "B", BranchName: "B@feat@thing", Type: "feat", Title: "thing"}
		got, err := MaterializeAndTrack(t.Context(), s, c, picked)
		if err != nil {
			t.Fatalf("MaterializeAndTrack: %v", err)
		}
		if len(s.inserted) != 0 {
			t.Errorf("expected no insert; inserted=%v", s.inserted)
		}
		if got.IssueID != 3 {
			t.Errorf("IssueID = %d, want 3 (existing row)", got.IssueID)
		}
	})
}
