package branch

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

// newRepo creates a repository with one commit and the given user identity.
// origin, when non-empty, is added as the "origin" remote.
func newRepo(t *testing.T, user, origin string) *git.Client {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", user)
	runGit(t, dir, "config", "user.email", user+"@test.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit(t, dir, "add", "base.txt")
	runGit(t, dir, "commit", "-q", "-m", "chore: init")
	if origin != "" {
		runGit(t, dir, "remote", "add", "origin", origin)
	}

	c, err := git.NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c
}

func newOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, t.TempDir(), "init", "-q", "--bare", dir)

	return dir
}

func mustStart(t *testing.T, c *git.Client, slug string, op Op) {
	t.Helper()

	if err := Start(t.Context(), c, slug, &op); err != nil {
		t.Fatalf("Start %s: %v", op.Branch, err)
	}
}

func mustLoad(t *testing.T, c *git.Client, slug string) *State {
	t.Helper()

	st, err := Load(t.Context(), c, slug)
	if err != nil || st == nil {
		t.Fatalf("Load(%s) = %v, %v", slug, st, err)
	}

	return st
}

func commitCount(t *testing.T, c *git.Client, slug string) int {
	t.Helper()

	commits, err := c.ReadChainCommits(t.Context(), git.BranchRefs, slug)
	if err != nil {
		t.Fatalf("ReadChainCommits(%s): %v", slug, err)
	}

	return len(commits)
}

// writeBlobRef leaves at refs/zf/branches/<slug> the JSON blob an older git-zf
// wrote, and returns the blob's SHA.
func writeBlobRef(t *testing.T, c *git.Client, slug, content string) string {
	t.Helper()

	dir := c.WorkingTreeRoot()
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(content)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}

	sha := strings.TrimSpace(string(out))
	runGit(t, dir, "update-ref", "refs/zf/branches/"+slug, sha)

	return sha
}

const legacy42 = `{"issue_slug":"42","branch_name":"42@feat@add-login","parent_slug":"7",` +
	`"created_at":"2026-06-21T10:00:00Z","tracker_type":"redmine","issue_id":"abc123"}`

func TestStartLoadFind(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	t.Run("Load of an unknown issue is nil without error", func(t *testing.T) {
		st, err := Load(ctx, c, "42")
		if st != nil || err != nil {
			t.Errorf("Load = %v, %v", st, err)
		}
	})

	mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat", Title: "Add login", Parent: "7"})

	t.Run("Start creates the chain with the branch in progress", func(t *testing.T) {
		st := mustLoad(t, c, "42")
		if st.Title != "Add login" || st.Parent != "7" || len(st.Entries) != 1 {
			t.Fatalf("state = %+v", st)
		}
		if e := st.Entries[0]; e.Name != feat || e.Status != StatusInProgress || !strings.Contains(e.Author, "alice") {
			t.Errorf("entry = %+v", e)
		}
	})

	t.Run("Start of a branch already in progress writes nothing", func(t *testing.T) {
		before := commitCount(t, c, "42")
		mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat"})
		if after := commitCount(t, c, "42"); after != before {
			t.Errorf("chain grew from %d to %d commits", before, after)
		}
	})

	t.Run("Start of a variant appends a second entry", func(t *testing.T) {
		mustStart(t, c, "42", Op{Branch: featV2, BranchType: "feat"})
		if got := entryNames(*mustLoad(t, c, "42")); !slices.Equal(got, []string{feat, featV2}) {
			t.Errorf("entries = %v", got)
		}
	})

	t.Run("SetStatus records the new status", func(t *testing.T) {
		if err := SetStatus(ctx, c, "42", feat, StatusMerged); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
		if got := mustLoad(t, c, "42").Entry(feat).Status; got != StatusMerged {
			t.Errorf("status = %q", got)
		}
	})

	t.Run("Find returns the state and entry of a tracked branch", func(t *testing.T) {
		st, e, err := Find(ctx, c, feat)
		if err != nil || st == nil || e == nil || st.Slug != "42" || e.Status != StatusMerged {
			t.Errorf("Find = %+v, %+v, %v", st, e, err)
		}
	})

	for name, branchName := range map[string]string{
		"a branch the chain does not know": "42@fix@other",
		"a branch of an untracked issue":   "99@feat@nothing",
		"a review branch":                  "42@review",
		"a name that is not git-zf's":      "main",
	} {
		t.Run("Find returns nil for "+name, func(t *testing.T) {
			st, e, err := Find(ctx, c, branchName)
			if st != nil || e != nil || err != nil {
				t.Errorf("Find = %v, %v, %v", st, e, err)
			}
		})
	}

	t.Run("Start of a merged branch reopens it", func(t *testing.T) {
		mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat"})
		st := mustLoad(t, c, "42")
		if len(st.Entries) != 2 || st.Entry(feat).Status != StatusInProgress {
			t.Errorf("entries = %+v", st.Entries)
		}
	})

	t.Run("SetStatus on an issue with no chain fails", func(t *testing.T) {
		if err := SetStatus(ctx, c, "99", "99@feat@nothing", StatusMerged); err == nil {
			t.Error("SetStatus succeeded on a missing chain")
		}
	})
}

func TestListAndLegacy(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat", Title: "Add login"})
	mustStart(t, c, "10", Op{Branch: "10@fix@typo", BranchType: "fix"})
	writeBlobRef(t, c, "old1", legacy42)
	writeBlobRef(t, c, "old2", `{}`)

	t.Run("List returns the chains in slug order", func(t *testing.T) {
		states, _, err := List(ctx, c)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		slugs := []string{}
		for _, st := range states {
			slugs = append(slugs, st.Slug)
		}
		if !slices.Equal(slugs, []string{"10", "42"}) {
			t.Errorf("slugs = %v", slugs)
		}
	})

	t.Run("List names every blob ref in one warning that gives the way out", func(t *testing.T) {
		_, warnings, err := List(ctx, c)
		if err != nil || len(warnings) != 1 {
			t.Fatalf("List = %v, %v", warnings, err)
		}
		for _, want := range []string{"2 branch ref(s)", "old1", "old2", "git zf issue track"} {
			if !strings.Contains(warnings[0], want) {
				t.Errorf("warning lacks %q: %s", want, warnings[0])
			}
		}
	})

	t.Run("Load of a blob ref reports ErrLegacyBranch", func(t *testing.T) {
		st, err := Load(ctx, c, "old1")
		if st != nil || !errors.Is(err, ErrLegacyBranch) {
			t.Errorf("Load = %v, %v", st, err)
		}
	})

	t.Run("a malformed op is skipped and named in the warnings", func(t *testing.T) {
		if _, err := c.AppendChainCommit(ctx, git.BranchRefs, "10", []byte("not json"), "junk", false); err != nil {
			t.Fatalf("AppendChainCommit: %v", err)
		}
		states, warnings, err := List(ctx, c)
		if err != nil || len(states) != 2 || len(states[0].Entries) != 1 {
			t.Fatalf("List = %+v, %v", states, err)
		}
		if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, "malformed op") }) {
			t.Errorf("warnings = %v", warnings)
		}
	})
}

func TestStart_ReplacesALegacyBlob(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	t.Run("the new chain keeps the blob's parent, tracker type and issue ID", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		writeBlobRef(t, c, "42", legacy42)
		mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat", Title: "add login"})

		st := mustLoad(t, c, "42")
		if st.Parent != "7" || st.TrackerType != "redmine" || st.IssueID != "abc123" || st.Title != "add login" {
			t.Errorf("state = %+v", st)
		}
		if len(st.Entries) != 1 || st.Entries[0].Status != StatusInProgress {
			t.Errorf("entries = %+v", st.Entries)
		}
	})

	t.Run("a value of the command wins over the blob's", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		writeBlobRef(t, c, "42", legacy42)
		mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat", Parent: "8"})

		if got := mustLoad(t, c, "42").Parent; got != "8" {
			t.Errorf("Parent = %q, want 8", got)
		}
	})

	t.Run("a blob that is not JSON is replaced with nothing carried over", func(t *testing.T) {
		t.Parallel()

		c := newRepo(t, "alice", "")
		writeBlobRef(t, c, "42", "garbage")
		mustStart(t, c, "42", Op{Branch: feat, BranchType: "feat"})

		if st := mustLoad(t, c, "42"); st.Parent != "" || len(st.Entries) != 1 {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("the blob seen on the remote is deleted there and the chain pushed", func(t *testing.T) {
		t.Parallel()

		origin := newOrigin(t)
		alice := newRepo(t, "alice", origin)
		writeBlobRef(t, alice, "42", legacy42)
		runGit(t, alice.WorkingTreeRoot(), "push", "-q", "origin", "refs/zf/branches/42")

		mustStart(t, alice, "42", Op{Branch: feat, BranchType: "feat"})
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("Push: %v", err)
		}

		if typ := runGit(t, origin, "cat-file", "-t", "refs/zf/branches/42"); typ != "commit" {
			t.Errorf("origin ref is a %s, want a commit", typ)
		}
	})
}

func TestTwoClones_SameLegacyBlob(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// setup gives alice and bob the same legacy blob, also on the remote.
	setup := func(t *testing.T) (alice, bob *git.Client) {
		t.Helper()

		origin := newOrigin(t)
		alice, bob = newRepo(t, "alice", origin), newRepo(t, "bob", origin)
		writeBlobRef(t, alice, "42", legacy42)
		writeBlobRef(t, bob, "42", legacy42)
		runGit(t, alice.WorkingTreeRoot(), "push", "-q", "origin", "refs/zf/branches/42")

		return alice, bob
	}

	t.Run("the second clone to convert appends to the first one's chain", func(t *testing.T) {
		t.Parallel()

		alice, bob := setup(t)
		mustStart(t, alice, "42", Op{Branch: feat, BranchType: "feat"})
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("alice Push: %v", err)
		}

		mustStart(t, bob, "42", Op{Branch: featV2, BranchType: "feat"})
		if err := Push(ctx, bob, "42"); err != nil {
			t.Fatalf("bob Push: %v", err)
		}

		commits, err := bob.ReadChainCommits(ctx, git.BranchRefs, "42")
		if err != nil {
			t.Fatalf("ReadChainCommits: %v", err)
		}
		roots := 0
		for _, commit := range commits {
			if len(commit.Parents) == 0 {
				roots++
			}
		}
		if roots != 1 || len(commits) != 2 {
			t.Errorf("chain has %d commits and %d roots, want 2 and 1", len(commits), roots)
		}
		if got := entryNames(*mustLoad(t, bob, "42")); !slices.Equal(got, []string{feat, featV2}) {
			t.Errorf("entries = %v", got)
		}
	})

	t.Run("two clones converting offline end with one merge and one entry", func(t *testing.T) {
		t.Parallel()

		alice, bob := setup(t)
		mustStart(t, alice, "42", Op{Branch: feat, BranchType: "feat"})
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("alice Push: %v", err)
		}

		// bob is offline: his fetch fails, he converts his own blob.
		bobDir := bob.WorkingTreeRoot()
		url := runGit(t, bobDir, "remote", "get-url", "origin")
		runGit(t, bobDir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unreachable.git"))
		mustStart(t, bob, "42", Op{Branch: feat, BranchType: "feat"})
		runGit(t, bobDir, "remote", "set-url", "origin", url)

		if err := Sync(ctx, bob); err != nil {
			t.Fatalf("bob Sync: %v", err)
		}
		if err := Sync(ctx, alice); err != nil {
			t.Fatalf("alice Sync: %v", err)
		}

		for name, c := range map[string]*git.Client{"alice": alice, "bob": bob} {
			if n := commitCount(t, c, "42"); n != 3 {
				t.Errorf("%s has %d commits, want two roots and a merge", name, n)
			}
			st := mustLoad(t, c, "42")
			if len(st.Entries) != 1 || st.Parent != "7" {
				t.Errorf("%s state = %+v", name, st)
			}
		}
	})
}

func TestTwoClones_VariantsOffline(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	origin := newOrigin(t)
	alice, bob := newRepo(t, "alice", origin), newRepo(t, "bob", origin)

	mustStart(t, alice, "42", Op{Branch: feat, BranchType: "feat", Title: "Add login"})
	mustStart(t, bob, "42", Op{Branch: featV2, BranchType: "feat", Title: "Add login, again"})

	if err := Sync(ctx, alice); err != nil {
		t.Fatalf("alice Sync: %v", err)
	}
	if err := Sync(ctx, bob); err != nil {
		t.Fatalf("bob Sync: %v", err)
	}
	if err := Sync(ctx, alice); err != nil {
		t.Fatalf("alice second Sync: %v", err)
	}

	a, b := mustLoad(t, alice, "42"), mustLoad(t, bob, "42")

	t.Run("both clones hold both branches", func(t *testing.T) {
		for name, st := range map[string]*State{"alice": a, "bob": b} {
			got := entryNames(*st)
			slices.Sort(got)
			if !slices.Equal(got, []string{feat, featV2}) {
				t.Errorf("%s entries = %v", name, got)
			}
		}
	})

	t.Run("both clones fold to the same title and order", func(t *testing.T) {
		if a.Title != b.Title || !slices.Equal(entryNames(*a), entryNames(*b)) {
			t.Errorf("alice = %q %v, bob = %q %v", a.Title, entryNames(*a), b.Title, entryNames(*b))
		}
	})

	t.Run("a status set on one clone reaches the other", func(t *testing.T) {
		if err := SetStatus(ctx, alice, "42", feat, StatusMerged); err != nil {
			t.Fatalf("SetStatus: %v", err)
		}
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("Push: %v", err)
		}
		if err := Fetch(ctx, bob); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if got := mustLoad(t, bob, "42").Entry(feat).Status; got != StatusMerged {
			t.Errorf("bob sees %q", got)
		}
	})
}

func TestPush_NamesTheBlobAnOlderBinaryPushed(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	origin := newOrigin(t)
	alice, old := newRepo(t, "alice", origin), newRepo(t, "old", origin)

	mustStart(t, alice, "42", Op{Branch: feat, BranchType: "feat"})
	if err := Push(ctx, alice, "42"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// An older git-zf force-pushes its blob over the chain.
	writeBlobRef(t, old, "42", legacy42)
	runGit(t, old.WorkingTreeRoot(), "push", "-q", "--force", "origin", "refs/zf/branches/42")

	if err := SetStatus(ctx, alice, "42", feat, StatusMerged); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	err := Push(ctx, alice, "42")

	t.Run("the push fails and says how to delete the remote blob", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "git push origin --delete refs/zf/branches/42") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("the local chain is intact", func(t *testing.T) {
		if got := mustLoad(t, alice, "42").Entry(feat).Status; got != StatusMerged {
			t.Errorf("status = %q", got)
		}
	})
}
