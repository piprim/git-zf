package issue

import (
	"strings"
	"testing"

	commitpkg "github.com/piprim/git-zf/commit"
	issuepkg "github.com/piprim/git-zf/issue"
)

// Closing a branch whose BranchRef names a repo issue closes that issue.
func TestClose_ClosesRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()

	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Add thing", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ref, err := rig.client.ReadBranchRef(ctx, "ABC-1")
	if err != nil || ref == nil {
		t.Fatalf("ReadBranchRef = %+v, %v", ref, err)
	}
	ref.IssueID = rec.ID
	if _, err := rig.client.WriteBranchRef(ctx, "ABC-1", *ref); err != nil {
		t.Fatalf("WriteBranchRef: %v", err)
	}

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	runErr := runClose(ctx, rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the repo issue is closed", func(t *testing.T) {
		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil || got.State != issuepkg.StateClosed {
			t.Errorf("state = %q (%v)", got.State, err)
		}
	})
	t.Run("no warning is printed", func(t *testing.T) {
		if strings.Contains(rig.stderr.String(), "close repo issue") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

// A BranchRef naming an issue that does not exist must not fail the close:
// the merge already landed.
func TestClose_MissingRepoIssueIsAWarning(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()

	ref, err := rig.client.ReadBranchRef(ctx, "ABC-1")
	if err != nil || ref == nil {
		t.Fatalf("ReadBranchRef = %+v, %v", ref, err)
	}
	ref.IssueID = strings.Repeat("0", 39) + "1"
	if _, err := rig.client.WriteBranchRef(ctx, "ABC-1", *ref); err != nil {
		t.Fatalf("WriteBranchRef: %v", err)
	}

	prompter := &scriptedPrompter{
		Branch:       rig.pickedBranchRow(),
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): close ABC-1\n"),
		DeleteBranch: true,
	}

	runErr := runClose(ctx, rig.deps(), prompter)

	t.Run("the close still succeeds", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("a warning names the failure", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "warning: close repo issue") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("the feature branch is still deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})
}
