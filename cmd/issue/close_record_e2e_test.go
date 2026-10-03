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

// A teammate who never fetched the issue closes its branch: the issue must be
// fetched and closed, for everyone, not left open behind a warning.
func TestClose_ClosesRepoIssueNeverFetchedLocally(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	origin := rig.addOrigin(t)
	ctx := t.Context()

	// The author creates and pushes the issue from another clone.
	author := newRecordRig(t, "author", origin)
	rec, err := issuepkg.Create(ctx, author.client, issuepkg.NewIssue{Title: "Add thing", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Push(ctx, author.client, rec.ID); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// The closer knows the issue only through the branch ref.
	ref, err := rig.client.ReadBranchRef(ctx, "ABC-1")
	if err != nil || ref == nil {
		t.Fatalf("ReadBranchRef = %+v, %v", ref, err)
	}
	ref.IssueID = rec.ID
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

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("no close-repo-issue warning", func(t *testing.T) {
		if strings.Contains(rig.stderr.String(), "close repo issue") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("the issue is closed on the closer's clone", func(t *testing.T) {
		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil || got.State != issuepkg.StateClosed {
			t.Errorf("state = %q (%v)", got.State, err)
		}
	})
	t.Run("the author sees it closed after a sync", func(t *testing.T) {
		if _, err := issuepkg.Fetch(ctx, author.client); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got, err := issuepkg.Load(ctx, author.client, rec.ID)
		if err != nil || got.State != issuepkg.StateClosed {
			t.Errorf("state = %q (%v)", got.State, err)
		}
	})
}
