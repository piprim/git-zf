package issue

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

func TestCreateLoadAppend(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	rec, err := Create(ctx, c, NewIssue{Title: "Login fails", Description: "Steps to reproduce", BranchType: "fix", Labels: []string{"ui", "bug"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("Create returns the folded record", func(t *testing.T) {
		if rec.Title != "Login fails" || rec.Description != "Steps to reproduce" || rec.BranchType != "fix" {
			t.Errorf("record = %+v", rec)
		}
		if rec.State != StateOpen || !slices.Equal(rec.Labels, []string{"bug", "ui"}) {
			t.Errorf("state/labels = %q %v", rec.State, rec.Labels)
		}
		if len(rec.ID) != 40 || rec.CreatedAt.IsZero() {
			t.Errorf("ID = %q, CreatedAt = %v", rec.ID, rec.CreatedAt)
		}
	})

	t.Run("Append stamps the author from the git identity", func(t *testing.T) {
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "me too"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := Load(ctx, c, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(got.Comments) != 1 || got.Comments[0].Body != "me too" {
			t.Fatalf("Comments = %+v", got.Comments)
		}
		if got.Comments[0].Author != "alice <alice@test.com>" || got.Comments[0].At.IsZero() {
			t.Errorf("comment = %+v", got.Comments[0])
		}
	})

	t.Run("Append on an unknown issue fails", func(t *testing.T) {
		err := Append(ctx, c, strings.Repeat("0", 40), &Op{Type: OpAddComment, Body: "x"})
		if !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})

	t.Run("a malformed op is skipped and reported in Warnings", func(t *testing.T) {
		if _, err := c.AppendChainCommit(ctx, git.IssueRefs, rec.ID, []byte(`{not json`), "junk", false); err != nil {
			t.Fatalf("AppendChainCommit: %v", err)
		}
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
			t.Fatalf("Append: %v", err)
		}

		got, err := Load(ctx, c, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.State != StateClosed || got.Title != "Login fails" {
			t.Errorf("record = %+v", got)
		}
		if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "WARN:") {
			t.Errorf("Warnings = %v", got.Warnings)
		}
	})
}

func TestListAndResolve(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	first, err := Create(ctx, c, NewIssue{Title: "First", Description: "", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := Create(ctx, c, NewIssue{Title: "Second", Description: "", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("List returns every issue", func(t *testing.T) {
		recs, warnings, err := List(ctx, c)
		if err != nil || len(recs) != 2 || len(warnings) != 0 {
			t.Fatalf("List = %d records, %v, %v", len(recs), warnings, err)
		}
	})

	t.Run("List skips a corrupt ref with a warning", func(t *testing.T) {
		wrong := strings.Repeat("1", 40)
		root := c.WorkingTreeRoot()
		runGit(t, root, "update-ref", "refs/zf/issues/"+wrong, first.ID)

		recs, warnings, err := List(ctx, c)
		runGit(t, root, "update-ref", "-d", "refs/zf/issues/"+wrong)
		if err != nil || len(recs) != 2 {
			t.Fatalf("List = %d records, %v", len(recs), err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], wrong) {
			t.Errorf("warnings = %v", warnings)
		}
	})

	t.Run("Resolve by full ID", func(t *testing.T) {
		got, err := Resolve(ctx, c, second.ID)
		if err != nil || got.Title != "Second" {
			t.Errorf("Resolve = %+v, %v", got, err)
		}
	})

	t.Run("Resolve by short ID", func(t *testing.T) {
		got, err := Resolve(ctx, c, first.ShortID())
		if err != nil || got.ID != first.ID {
			t.Errorf("Resolve = %+v, %v", got, err)
		}
	})

	t.Run("Resolve of an unknown ID fails with ErrIssueNotFound", func(t *testing.T) {
		_, err := Resolve(ctx, c, "ffffffff")
		if !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("Resolve rejects a prefix shorter than 4 characters", func(t *testing.T) {
		_, err := Resolve(ctx, c, first.ID[:3])
		if !errors.Is(err, git.ErrIssueNotFound) || !strings.Contains(err.Error(), "at least 4") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestResolve_Ambiguous(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	// Create issues until two share their first 4 hex characters is too slow;
	// instead alias one chain under a second ref name sharing a 39-char prefix.
	rec, err := Create(ctx, c, NewIssue{Title: "Twin", Description: "", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	last := "0"
	if strings.HasSuffix(rec.ID, "0") {
		last = "1"
	}
	twin := rec.ID[:39] + last
	root := c.WorkingTreeRoot()
	runGit(t, root, "update-ref", "refs/zf/issues/"+twin, rec.ID)

	t.Run("an ambiguous prefix lists the candidates", func(t *testing.T) {
		_, err := Resolve(ctx, c, rec.ID[:10])
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(err.Error(), rec.ID) || !strings.Contains(err.Error(), twin) || !strings.Contains(err.Error(), "Twin") {
			t.Errorf("err should list both IDs and the title: %v", err)
		}
	})

	t.Run("the full ID still resolves", func(t *testing.T) {
		got, err := Resolve(ctx, c, rec.ID)
		if err != nil || got.Title != "Twin" {
			t.Errorf("Resolve = %+v, %v", got, err)
		}
	})
}

func TestPushFetchSync(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	alice := newRepo(t, "alice", origin)
	bob := newRepo(t, "bob", origin)
	ctx := t.Context()

	rec, err := Create(ctx, alice, NewIssue{Title: "Shared", Description: "", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Push(ctx, alice, rec.ID); err != nil {
		t.Fatalf("Push: %v", err)
	}

	t.Run("Fetch brings the issue to another clone", func(t *testing.T) {
		if _, err := Fetch(ctx, bob); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got, err := Load(ctx, bob, rec.ID)
		if err != nil || got.Title != "Shared" {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})

	t.Run("comments written offline on two clones are both kept", func(t *testing.T) {
		if err := Append(ctx, alice, rec.ID, &Op{Type: OpAddComment, Body: "from alice"}); err != nil {
			t.Fatalf("alice Append: %v", err)
		}
		if err := Push(ctx, alice, rec.ID); err != nil {
			t.Fatalf("alice Push: %v", err)
		}
		if err := Append(ctx, bob, rec.ID, &Op{Type: OpAddComment, Body: "from bob"}); err != nil {
			t.Fatalf("bob Append: %v", err)
		}
		// Bob's push is rejected first, then merged and retried inside Push.
		if err := Push(ctx, bob, rec.ID); err != nil {
			t.Fatalf("bob Push: %v", err)
		}
		if _, err := Fetch(ctx, alice); err != nil {
			t.Fatalf("alice Fetch: %v", err)
		}

		for name, c := range map[string]*git.Client{"alice": alice, "bob": bob} {
			got, err := Load(ctx, c, rec.ID)
			if err != nil {
				t.Fatalf("%s Load: %v", name, err)
			}
			bodies := []string{}
			for _, k := range got.Comments {
				bodies = append(bodies, k.Body)
			}
			slices.Sort(bodies)
			if !slices.Equal(bodies, []string{"from alice", "from bob"}) {
				t.Errorf("%s sees comments %v", name, bodies)
			}
		}
	})

	t.Run("Sync pushes unpushed issues and reports the count", func(t *testing.T) {
		offline, err := Create(ctx, bob, NewIssue{Title: "Offline", Description: "", BranchType: "fix"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		res, err := Sync(ctx, bob)
		if err != nil || res.Pushed != 1 || len(res.Failed) != 0 {
			t.Fatalf("Sync = %+v, %v", res, err)
		}

		if _, err := Fetch(ctx, alice); err != nil {
			t.Fatalf("alice Fetch: %v", err)
		}
		if got, err := Load(ctx, alice, offline.ID); err != nil || got.Title != "Offline" {
			t.Errorf("alice Load = %+v, %v", got, err)
		}
	})

	t.Run("a second Sync has nothing to do", func(t *testing.T) {
		res, err := Sync(ctx, bob)
		if err != nil || res.Pushed != 0 || res.Merged != 0 {
			t.Errorf("Sync = %+v, %v", res, err)
		}
	})
}

func TestSync_NoRemote(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	if _, err := Create(ctx, c, NewIssue{Title: "Local", Description: "", BranchType: "feat"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("Sync without a remote succeeds and pushes nothing", func(t *testing.T) {
		res, err := Sync(ctx, c)
		if err != nil || res.Pushed != 0 || len(res.Failed) != 0 {
			t.Errorf("Sync = %+v, %v", res, err)
		}
	})
}

// Issue refs live in the common git dir, so an issue created from inside a
// linked worktree is the same issue seen from the main checkout.
func TestLinkedWorktreeSharesIssues(t *testing.T) {
	t.Parallel()

	main := newRepo(t, "alice", "")
	ctx := t.Context()
	root := main.WorkingTreeRoot()
	wtDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, root, "worktree", "add", "-q", "-b", "side", wtDir)

	wt, err := git.NewClientAt(nil, wtDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	rec, err := Create(ctx, wt, NewIssue{Title: "From worktree", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create in worktree: %v", err)
	}

	t.Run("the main checkout sees the issue", func(t *testing.T) {
		got, err := Load(ctx, main, rec.ID)
		if err != nil || got.Title != "From worktree" {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})

	t.Run("a comment from the main checkout is seen in the worktree", func(t *testing.T) {
		if err := Append(ctx, main, rec.ID, &Op{Type: OpAddComment, Body: "from main"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := Load(ctx, wt, rec.ID)
		if err != nil || len(got.Comments) != 1 {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})
}

// The tracking refs say what the remote had at the last fetch. When the remote
// no longer has an issue (ref deleted there, or the remote URL now points at
// another host), Sync must notice and push it again.
func TestSync_RepushesWhenTheRemoteLacksTheIssue(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	alice := newRepo(t, "alice", origin)
	ctx := t.Context()

	rec, err := Create(ctx, alice, NewIssue{Title: "Shared", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Push(ctx, alice, rec.ID); err != nil {
		t.Fatalf("Push: %v", err)
	}
	ref := "refs/zf/issues/" + rec.ID

	t.Run("after the ref is deleted on the remote", func(t *testing.T) {
		runGit(t, origin, "update-ref", "-d", ref)

		res, err := Sync(ctx, alice)
		if err != nil || res.Pushed != 1 {
			t.Fatalf("Sync = %+v, %v; want 1 pushed", res, err)
		}
		if got := runGit(t, origin, "for-each-ref", "--format=%(refname)", "refs/zf/issues"); got != ref {
			t.Errorf("origin issue refs = %q", got)
		}
	})

	t.Run("after the remote URL moves to an empty repository", func(t *testing.T) {
		moved := newOrigin(t)
		root := alice.WorkingTreeRoot()
		runGit(t, root, "remote", "set-url", "origin", moved)

		res, err := Sync(ctx, alice)
		if err != nil || res.Pushed != 1 {
			t.Fatalf("Sync = %+v, %v; want 1 pushed", res, err)
		}
		if got := runGit(t, moved, "for-each-ref", "--format=%(refname)", "refs/zf/issues"); got != ref {
			t.Errorf("new origin issue refs = %q", got)
		}
	})

	t.Run("the local issue is still there", func(t *testing.T) {
		if got, err := Load(ctx, alice, rec.ID); err != nil || got.Title != "Shared" {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})
}

func TestPushAll(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	a, b := newRepo(t, "alice", origin), newRepo(t, "bob", origin)
	ctx := t.Context()

	first, err := Create(ctx, a, NewIssue{Title: "First", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := Create(ctx, a, NewIssue{Title: "Second", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("both issues reach the remote in one push", func(t *testing.T) {
		if err := PushAll(ctx, a, []string{first.ID, second.ID}); err != nil {
			t.Fatalf("PushAll: %v", err)
		}
		if _, err := Fetch(ctx, b); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if records, _, err := List(ctx, b); err != nil || len(records) != 2 {
			t.Errorf("bob has %d issues, want 2 (err %v)", len(records), err)
		}
	})

	t.Run("a rejected push is merged and retried", func(t *testing.T) {
		if err := Append(ctx, b, first.ID, &Op{Type: OpAddComment, Body: "from bob"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		if err := Push(ctx, b, first.ID); err != nil {
			t.Fatalf("Push: %v", err)
		}
		for _, id := range []string{first.ID, second.ID} {
			if err := Append(ctx, a, id, &Op{Type: OpAddComment, Body: "from alice"}); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}

		if err := PushAll(ctx, a, []string{first.ID, second.ID}); err != nil {
			t.Fatalf("PushAll after a divergence: %v", err)
		}
		rec, err := Load(ctx, a, first.ID)
		if err != nil || len(rec.Comments) != 2 {
			t.Errorf("alice's first issue has %d comments (%v), want both", len(rec.Comments), err)
		}
	})

	t.Run("no ids and no remote are no-ops", func(t *testing.T) {
		alone := newRepo(t, "carol", "")
		rec, err := Create(ctx, alone, NewIssue{Title: "Alone", BranchType: "fix"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := PushAll(ctx, alone, nil); err != nil {
			t.Errorf("PushAll(nil) = %v", err)
		}
		if err := PushAll(ctx, alone, []string{rec.ID}); err != nil {
			t.Errorf("PushAll without a remote = %v", err)
		}
	})
}

// Review Focus 6.
func TestList_SkipsARefThatIsNotARoot(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	good, err := Create(ctx, c, NewIssue{Title: "Good", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Append(ctx, c, good.ID, &Op{Type: OpAddComment, Body: "second commit"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	tip, err := c.ChainTip(ctx, git.IssueRefs, good.ID)
	if err != nil {
		t.Fatalf("ChainTip: %v", err)
	}
	// A ref named after a commit that is not its chain's root.
	if err := c.PublishChainRoot(ctx, git.IssueRefs, tip, tip); err != nil {
		t.Fatalf("PublishChainRoot: %v", err)
	}

	records, warnings, err := List(ctx, c)

	t.Run("the good issue is listed", func(t *testing.T) {
		if err != nil || len(records) != 1 || records[0].ID != good.ID {
			t.Errorf("records = %+v, err = %v", records, err)
		}
	})
	t.Run("the bad ref is a warning naming it", func(t *testing.T) {
		if len(warnings) != 1 || !strings.Contains(warnings[0], tip) {
			t.Errorf("warnings = %v", warnings)
		}
	})
	t.Run("Load refuses the bad ref", func(t *testing.T) {
		if _, err := Load(ctx, c, tip); !errors.Is(err, git.ErrIssueRefCorrupt) {
			t.Errorf("Load = %v, want ErrIssueRefCorrupt", err)
		}
	})
}

// A foreign chain force-pushed under an issue's ID is not merged in: Sync
// pushes the local chain back over it, and syncs the other issues.
func TestSync_ForeignChainOnTheRemote(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	alice := newRepo(t, "alice", origin)
	bob := newRepo(t, "bob", origin)
	ctx := t.Context()

	victim, err := Create(ctx, alice, NewIssue{Title: "Victim", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	intruder, err := Create(ctx, alice, NewIssue{Title: "Intruder", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := PushAll(ctx, alice, []string{victim.ID, intruder.ID}); err != nil {
		t.Fatalf("PushAll: %v", err)
	}
	ref := "refs/zf/issues/" + victim.ID
	runGit(t, origin, "update-ref", ref, intruder.ID)

	if err := Append(ctx, alice, victim.ID, &Op{Type: OpAddComment, Body: "offline"}); err != nil {
		t.Fatalf("Append: %v", err)
	}
	other, err := Create(ctx, alice, NewIssue{Title: "Other", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("a clone without the issue cannot repair it and syncs the rest", func(t *testing.T) {
		res, err := Sync(ctx, bob)
		if err != nil || res.Repaired != 0 || len(res.Failed) != 0 {
			t.Errorf("Sync = %+v, %v; want nothing repaired, nothing failed", res, err)
		}
		if len(res.LeftOut) != 1 || !strings.Contains(res.LeftOut[0], victim.ID) {
			t.Errorf("LeftOut = %v, want the victim's ref", res.LeftOut)
		}
		if tip, _ := bob.ChainTip(ctx, git.IssueRefs, victim.ID); tip != "" {
			t.Errorf("bob took the foreign chain: tip = %q", tip)
		}
	})

	res, err := Sync(ctx, alice)

	t.Run("sync repairs the issue and pushes the other one", func(t *testing.T) {
		if err != nil || res.Repaired != 1 || res.Pushed != 1 || res.Merged != 0 || len(res.Failed) != 0 {
			t.Errorf("Sync = %+v, %v; want 1 repaired, 1 pushed", res, err)
		}
		if pushed, _ := alice.ChainRefPushed(ctx, git.IssueRefs, other.ID); !pushed {
			t.Error("other issue not pushed")
		}
	})

	t.Run("the remote holds the local chain again", func(t *testing.T) {
		tip, _ := alice.ChainTip(ctx, git.IssueRefs, victim.ID)
		if got := runGit(t, origin, "rev-parse", ref); got != tip {
			t.Errorf("origin %s = %q, want %q", ref, got, tip)
		}
	})

	t.Run("the local issue keeps its own history only", func(t *testing.T) {
		got, err := Load(ctx, alice, victim.ID)
		if err != nil || got.Title != "Victim" || len(got.Comments) != 1 {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})

	t.Run("the other clone then fetches the repaired issue", func(t *testing.T) {
		if _, err := Fetch(ctx, bob); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got, err := Load(ctx, bob, victim.ID)
		if err != nil || got.Title != "Victim" || len(got.Comments) != 1 {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})
}

func TestCreate_TwiceWithinASecond(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()
	in := NewIssue{Title: "Login fails", BranchType: "fix"}

	first, err := Create(ctx, c, in)
	if err != nil {
		t.Fatalf("first Create: %v", err)
	}
	second, err := Create(ctx, c, in)

	t.Run("the second identical issue is created", func(t *testing.T) {
		if err != nil {
			t.Fatalf("second Create: %v", err)
		}
		if second.ID == first.ID {
			t.Errorf("both issues got ID %s", first.ID)
		}
	})
	t.Run("both are listed", func(t *testing.T) {
		records, _, err := List(ctx, c)
		if err != nil || len(records) != 2 {
			t.Errorf("List: %d records, err = %v", len(records), err)
		}
	})
}
