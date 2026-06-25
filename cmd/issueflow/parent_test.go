package issueflow

import (
	"context"
	"errors"
	"testing"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
)

// fakeParentStore / fakeParentClient implement the ParentStore / ParentClient
// role interfaces with canned data — no real repo or DB.
type fakeParentStore struct {
	parentOf map[string]string // childSlug → parentSlug ("" = none)
	branches []store.BranchRow // returned by ListBranches
	listErr  error
}

func (f *fakeParentStore) GetParentIssue(_ context.Context, childSlug string) (string, error) {
	return f.parentOf[childSlug], nil
}
func (f *fakeParentStore) ListBranches(_ context.Context, _ store.BranchStatus) ([]store.BranchRow, error) {
	return f.branches, f.listErr
}

type fakeParentClient struct {
	defaultBase string
	refs        map[string]*git.BranchRef // issueSlug → ref (nil = absent)
}

func (f *fakeParentClient) DefaultBaseBranch() (string, error)      { return f.defaultBase, nil }
func (f *fakeParentClient) FetchBranchRefs(_ context.Context) error { return nil }
func (f *fakeParentClient) ReadBranchRef(_ context.Context, issueSlug string) (*git.BranchRef, error) {
	return f.refs[issueSlug], nil
}

func TestResolveParentBranch(t *testing.T) {
	t.Parallel()

	t.Run("no parent → cfg base", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{parentOf: map[string]string{}}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X", "main")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "main" {
			t.Fatalf("got %q, want %q", got, "main")
		}
	})

	t.Run("empty cfg base → DefaultBaseBranch", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{parentOf: map[string]string{}}
		c := &fakeParentClient{defaultBase: "master", refs: map[string]*git.BranchRef{}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X", "")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "master" {
			t.Fatalf("got %q, want %q", got, "master")
		}
	})

	t.Run("parent from store → parent branch name", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{
			parentOf: map[string]string{"X.2": "X"},
			branches: []store.BranchRow{{IssueSlug: "X", BranchName: "X@feat@big"}},
		}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X.2", "main")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "X@feat@big" {
			t.Fatalf("got %q, want %q", got, "X@feat@big")
		}
	})

	t.Run("cross-clone: parent slug from branch ref, name from parent ref", func(t *testing.T) {
		t.Parallel()
		// Store has no parent relation and no parent branch row (fresh clone);
		// the child's ref carries ParentSlug, and the parent's ref carries the name.
		s := &fakeParentStore{parentOf: map[string]string{}}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{
			"X.2": {ParentSlug: "X"},
			"X":   {BranchName: "X@feat@big"},
		}}
		got, err := ResolveParentBranch(t.Context(), s, c, "X.2", "main")
		if err != nil {
			t.Fatalf("ResolveParentBranch: %v", err)
		}
		if got != "X@feat@big" {
			t.Fatalf("got %q, want %q", got, "X@feat@big")
		}
	})

	t.Run("ListBranches error is wrapped", func(t *testing.T) {
		t.Parallel()
		s := &fakeParentStore{
			parentOf: map[string]string{"X.2": "X"},
			listErr:  errors.New("db down"),
		}
		c := &fakeParentClient{refs: map[string]*git.BranchRef{}}
		if _, err := ResolveParentBranch(t.Context(), s, c, "X.2", "main"); err == nil {
			t.Fatal("want error when ListBranches fails")
		}
	})
}
