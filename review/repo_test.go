package review

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/gittest"
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

func mustAppend(t *testing.T, c *git.Client, slug string, op Op) {
	t.Helper()

	if err := Append(t.Context(), c, slug, &op, false); err != nil {
		t.Fatalf("Append %s: %v", op.Type, err)
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

func writeBlobRef(t *testing.T, c *git.Client, slug string) {
	t.Helper()

	dir := c.WorkingTreeRoot()
	cmd := exec.CommandContext(t.Context(), "git", "-C", dir, "hash-object", "-w", "--stdin")
	cmd.Stdin = strings.NewReader(`{"status":"in_review","round":1}`)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("hash-object: %v", err)
	}
	runGit(t, dir, "update-ref", "refs/zf/reviews/"+slug, strings.TrimSpace(string(out)))
}

func TestAppendLoad(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	dir := c.WorkingTreeRoot()
	ctx := t.Context()

	t.Run("Load of an unknown review is nil without error", func(t *testing.T) {
		st, err := Load(ctx, c, "42")
		if st != nil || err != nil {
			t.Errorf("Load = %v, %v", st, err)
		}
	})

	mustAppend(t, c, "42", Op{Type: OpRequest, FeatureSHA: "f1"})

	t.Run("the first op creates the chain", func(t *testing.T) {
		st := mustLoad(t, c, "42")
		if st.Slug != "42" || st.Status != StatusInReview || st.Round != 1 || st.FeatureSHA != "f1" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("Append fills v, at and author", func(t *testing.T) {
		raw := runGit(t, dir, "cat-file", "blob", "refs/zf/reviews/42:op.json")
		for _, want := range []string{`"v":1`, `"at":"`, `"author":"alice <alice@test.com>"`} {
			if !strings.Contains(raw, want) {
				t.Errorf("op.json = %s, want it to contain %s", raw, want)
			}
		}
	})

	mustAppend(t, c, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})

	t.Run("a later op lands on the same chain", func(t *testing.T) {
		if n := runGit(t, dir, "rev-list", "--count", "refs/zf/reviews/42"); n != "2" {
			t.Errorf("chain length = %s, want 2", n)
		}
		if st := mustLoad(t, c, "42"); st.Status != StatusApproved || len(st.Approvals) != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}

func TestListAndLegacy(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	mustAppend(t, c, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	mustAppend(t, c, "10", Op{Type: OpRequest, FeatureSHA: "f2"})
	writeBlobRef(t, c, "old")

	t.Run("List returns the chains in slug order and warns about the blob", func(t *testing.T) {
		states, warnings, err := List(ctx, c)
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
		if len(warnings) != 1 || !strings.Contains(warnings[0], "old") {
			t.Errorf("warnings = %v", warnings)
		}
	})

	t.Run("the warning about the blob names the way out", func(t *testing.T) {
		_, warnings, err := List(ctx, c)
		if err != nil || len(warnings) != 1 {
			t.Fatalf("List = %v, %v", warnings, err)
		}
		for _, want := range []string{"git zf review request", "git update-ref -d refs/zf/reviews/old"} {
			if !strings.Contains(warnings[0], want) {
				t.Errorf("warning lacks %q: %s", want, warnings[0])
			}
		}
	})

	t.Run("Load of a blob ref reports ErrLegacyReview", func(t *testing.T) {
		st, err := Load(ctx, c, "old")
		if st != nil || !errors.Is(err, ErrLegacyReview) {
			t.Errorf("Load = %v, %v", st, err)
		}
	})

	t.Run("Append refuses to write on a blob ref", func(t *testing.T) {
		err := Append(ctx, c, "old", &Op{Type: OpRequest}, false)
		if !errors.Is(err, ErrLegacyReview) {
			t.Errorf("Append = %v", err)
		}
	})

	t.Run("ReplaceLegacyWith replaces the blob by a chain holding the op", func(t *testing.T) {
		if err := ReplaceLegacyWith(ctx, c, "old", &Op{Type: OpRequest, FeatureSHA: "f3"}, false); err != nil {
			t.Fatalf("ReplaceLegacyWith: %v", err)
		}
		if st := mustLoad(t, c, "old"); st.Round != 1 || st.Status != StatusInReview || st.FeatureSHA != "f3" {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("ReplaceLegacyWith keeps the blob when the root cannot be written", func(t *testing.T) {
		solo := newRepo(t, "solo", "")
		dir := solo.WorkingTreeRoot()
		writeBlobRef(t, solo, "old")
		blob := runGit(t, dir, "rev-parse", "refs/zf/reviews/old")
		runGit(t, dir, "config", "gpg.format", "ssh")
		runGit(t, dir, "config", "user.signingkey", "/nonexistent/key.pub")

		if err := ReplaceLegacyWith(ctx, solo, "old", &Op{Type: OpRequest, FeatureSHA: "f3"}, true); err == nil {
			t.Fatal("ReplaceLegacyWith succeeded without a usable signing key")
		}
		if got := runGit(t, dir, "rev-parse", "refs/zf/reviews/old"); got != blob {
			t.Errorf("refs/zf/reviews/old = %s, want the blob %s", got, blob)
		}
	})
}

func TestTwoClonesApproveOffline(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	dev := newRepo(t, "dev", origin)
	alice := newRepo(t, "alice", origin)
	bob := newRepo(t, "bob", origin)
	ctx := t.Context()

	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push: %v", err)
	}
	for _, c := range []*git.Client{alice, bob} {
		if err := Sync(ctx, c); err != nil {
			t.Fatalf("reviewer sync: %v", err)
		}
	}

	// Both reviewers approve without seeing each other's op.
	mustAppend(t, alice, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})
	mustAppend(t, bob, "42", Op{Type: OpApprove, ApprovedSHA: "f2", HasCommits: true})

	t.Run("the first reviewer's push succeeds", func(t *testing.T) {
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("alice push: %v", err)
		}
	})

	t.Run("the second reviewer's push succeeds after an automatic merge", func(t *testing.T) {
		if err := Push(ctx, bob, "42"); err != nil {
			t.Fatalf("bob push: %v", err)
		}
	})

	for _, c := range []*git.Client{dev, alice} {
		if err := Sync(ctx, c); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	t.Run("the developer sees both approvals", func(t *testing.T) {
		st := mustLoad(t, dev, "42")
		if st.Status != StatusApproved || len(st.Approvals) != 2 || !st.HasCommits {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("the chain holds exactly one merge commit", func(t *testing.T) {
		n := runGit(t, dev.WorkingTreeRoot(), "rev-list", "--count", "--min-parents=2", "refs/zf/reviews/42")
		if n != "1" {
			t.Errorf("merge commits = %s, want 1", n)
		}
	})

	t.Run("every clone converges on the same tip", func(t *testing.T) {
		tips := []string{}
		for _, c := range []*git.Client{dev, alice, bob} {
			tips = append(tips, runGit(t, c.WorkingTreeRoot(), "rev-parse", "refs/zf/reviews/42"))
		}
		if tips[0] != tips[1] || tips[1] != tips[2] {
			t.Errorf("tips = %v", tips)
		}
	})
}

func TestTwoClonesStaleApproval(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	dev := newRepo(t, "dev", origin)
	alice := newRepo(t, "alice", origin)
	ctx := t.Context()

	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push round 1: %v", err)
	}
	if err := Sync(ctx, alice); err != nil {
		t.Fatalf("alice sync: %v", err)
	}

	mustAppend(t, dev, "42", Op{Type: OpReject, Round: 1})
	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f2"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push round 2: %v", err)
	}

	// at has a one-second resolution: date the stale approval after request 2,
	// as in the scenario where it would otherwise approve round 2.
	time.Sleep(1100 * time.Millisecond)

	// alice never synced round 2: her approval is written on top of request 1.
	mustAppend(t, alice, "42", Op{Type: OpApprove, ApprovedSHA: "f1", Round: 1})

	t.Run("the stale approval's push succeeds after an automatic merge", func(t *testing.T) {
		if err := Push(ctx, alice, "42"); err != nil {
			t.Fatalf("alice push: %v", err)
		}
	})

	for _, c := range []*git.Client{dev, alice} {
		if err := Sync(ctx, c); err != nil {
			t.Fatalf("sync: %v", err)
		}
	}

	for name, c := range map[string]*git.Client{"dev": dev, "alice": alice} {
		t.Run(name+" sees round 2 in review with no approval", func(t *testing.T) {
			st := mustLoad(t, c, "42")
			if st.Status != StatusInReview || st.Round != 2 || len(st.Approvals) != 0 {
				t.Errorf("state = %+v", st)
			}
		})
	}
}

func TestSync_EdgeCases(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	dev := newRepo(t, "dev", origin)
	devDir := dev.WorkingTreeRoot()
	ctx := t.Context()

	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push: %v", err)
	}

	t.Run("an approval on a clone that never loaded the review joins the existing chain", func(t *testing.T) {
		devTip := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42")
		carol := newRepo(t, "carol", origin)
		if err := Sync(ctx, carol); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		mustAppend(t, carol, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})

		carolDir := carol.WorkingTreeRoot()
		roots := runGit(t, carolDir, "rev-list", "--count", "--max-parents=0", "refs/zf/reviews/42")
		if roots != "1" {
			t.Errorf("root commits = %s, want 1", roots)
		}
		if n := runGit(t, carolDir, "rev-list", "--count", "refs/zf/reviews/42"); n != "2" {
			t.Errorf("chain length = %s, want 2", n)
		}
		if root := runGit(t, carolDir, "rev-list", "--max-parents=0", "refs/zf/reviews/42"); root != devTip {
			t.Errorf("chain root = %s, want dev's request commit %s", root, devTip)
		}
	})

	t.Run("an op whose push failed stays local and goes out with the next Sync", func(t *testing.T) {
		mustAppend(t, dev, "42", Op{Type: OpStart})

		if err := os.Rename(origin, origin+".off"); err != nil {
			t.Fatalf("rename origin: %v", err)
		}
		pushErr := Push(ctx, dev, "42")
		if err := os.Rename(origin+".off", origin); err != nil {
			t.Fatalf("restore origin: %v", err)
		}
		if pushErr == nil {
			t.Fatal("Push succeeded with the remote gone")
		}

		local := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42")
		if err := Sync(ctx, dev); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if remote := runGit(t, origin, "rev-parse", "refs/zf/reviews/42"); remote != local {
			t.Errorf("origin tip = %s, want %s", remote, local)
		}
	})

	t.Run("tracking refs removed by a plain git fetch --prune are restored without a merge", func(t *testing.T) {
		before := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42")
		runGit(t, devDir, "update-ref", "-d", "refs/remotes/origin/zf/reviews/42")

		if err := Sync(ctx, dev); err != nil {
			t.Fatalf("Sync: %v", err)
		}
		if after := runGit(t, devDir, "rev-parse", "refs/zf/reviews/42"); after != before {
			t.Errorf("tip moved from %s to %s", before, after)
		}
		if n := runGit(t, devDir, "rev-list", "--count", "--min-parents=2", "refs/zf/reviews/42"); n != "0" {
			t.Errorf("merge commits = %s, want 0", n)
		}
		if tracked := runGit(t, devDir, "rev-parse", "refs/remotes/origin/zf/reviews/42"); tracked != before {
			t.Errorf("tracking ref = %s, want %s", tracked, before)
		}
	})

	t.Run("without a remote every network call is a no-op", func(t *testing.T) {
		solo := newRepo(t, "solo", "")
		mustAppend(t, solo, "7", Op{Type: OpRequest, FeatureSHA: "f1"})
		if err := Sync(ctx, solo); err != nil {
			t.Errorf("Sync: %v", err)
		}
		if err := Push(ctx, solo, "7"); err != nil {
			t.Errorf("Push: %v", err)
		}
		if st := mustLoad(t, solo, "7"); st.Status != StatusInReview {
			t.Errorf("state = %+v", st)
		}
	})

	t.Run("a commit whose op.json is not JSON is skipped with a warning", func(t *testing.T) {
		solo := newRepo(t, "solo", "")
		mustAppend(t, solo, "7", Op{Type: OpRequest, FeatureSHA: "f1"})
		if _, err := solo.AppendChainCommit(ctx, git.ReviewRefs, "7", []byte("not json"), "junk", false); err != nil {
			t.Fatalf("AppendChainCommit: %v", err)
		}

		st := mustLoad(t, solo, "7")
		if st.Status != StatusInReview || st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
		if len(st.Warnings) != 1 {
			t.Errorf("warnings = %v", st.Warnings)
		}
	})
}

func TestSignatureState(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	dir := c.WorkingTreeRoot()
	ctx := t.Context()
	gittest.SSHSigner(t, dir)

	mustAppend(t, c, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	mustAppend(t, c, "42", Op{Type: OpApprove, ApprovedSHA: "f1"})
	if err := Append(ctx, c, "42", &Op{Type: OpApprove, ApprovedSHA: "f1"}, true); err != nil {
		t.Fatalf("signed Append: %v", err)
	}
	st := mustLoad(t, c, "42")
	if len(st.Approvals) != 2 {
		t.Fatalf("approvals = %+v", st.Approvals)
	}

	t.Run("an unsigned approval is unsigned", func(t *testing.T) {
		if got := SignatureState(ctx, c, st.Approvals[0].Commit); got != SigNone {
			t.Errorf("SignatureState = %q", got)
		}
	})

	t.Run("an approval signed by an allowed signer is verified", func(t *testing.T) {
		if got := SignatureState(ctx, c, st.Approvals[1].Commit); got != SigVerified {
			t.Errorf("SignatureState = %q", got)
		}
	})

	runGit(t, dir, "config", "--unset", "gpg.ssh.allowedSignersFile")

	t.Run("a signature git cannot check is signed, not verified", func(t *testing.T) {
		if got := SignatureState(ctx, c, st.Approvals[1].Commit); got != SigUnverified {
			t.Errorf("SignatureState = %q", got)
		}
	})
}

func TestFetch_ReconcilesWhenTheRemoteIsUnreachable(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	dev := newRepo(t, "dev", origin)
	other := newRepo(t, "other", origin)
	ctx := t.Context()

	mustAppend(t, dev, "42", Op{Type: OpRequest, FeatureSHA: "f1"})
	if err := Push(ctx, dev, "42"); err != nil {
		t.Fatalf("dev push: %v", err)
	}

	// The other clone learned the chain through a plain `git fetch` (the
	// refspec `git zf init` configures), never through git-zf: it has the
	// tracking ref and no local ref. Then the remote becomes unreachable.
	runGit(t, other.WorkingTreeRoot(), "fetch", "-q", "origin",
		"+refs/zf/reviews/*:refs/remotes/origin/zf/reviews/*")
	if err := os.Rename(origin, origin+".off"); err != nil {
		t.Fatalf("rename origin: %v", err)
	}

	err := Fetch(ctx, other, true)

	t.Run("the fetch failure is reported", func(t *testing.T) {
		if err == nil {
			t.Fatal("Fetch succeeded with the remote gone")
		}
	})

	t.Run("the chain known through the tracking ref is still reconciled", func(t *testing.T) {
		st, lErr := Load(ctx, other, "42")
		if lErr != nil || st == nil {
			t.Fatalf("Load = %v, %v; the lock is invisible while offline", st, lErr)
		}
		if st.Status != StatusInReview || st.Round != 1 {
			t.Errorf("state = %+v", st)
		}
	})
}
