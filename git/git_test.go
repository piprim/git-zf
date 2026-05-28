package git

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v6"
	gogitcfg "github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/storage/memory"

	// go-git v6 depends on go-billy/v6.
	"github.com/go-git/go-billy/v6/memfs"
	"github.com/piprim/git-zf/internal/pkg"
)

// newTestRepo creates an in-memory git repository with one initial commit.
// Used by tests that don't need on-disk functionality (DefaultBaseBranch, CreateBranch, etc).
func newTestRepo(t *testing.T) *gogit.Repository {
	t.Helper()

	repo, err := gogit.Init(memory.NewStorage(), gogit.WithWorkTree(memfs.New()))
	if err != nil {
		t.Fatalf("init in-memory repo: %v", err)
	}

	cfg, err := repo.Config()
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"
	if err := repo.SetConfig(cfg); err != nil {
		t.Fatalf("set config: %v", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	f, err := wt.Filesystem.Create("README.md")
	if err != nil {
		t.Fatalf("create README.md: %v", err)
	}
	_, _ = f.Write([]byte("# test"))
	_ = f.Close()

	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("stage README.md: %v", err)
	}

	_, err = wt.Commit("chore: init", &gogit.CommitOptions{})
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	return repo
}

func TestCommit(t *testing.T) {
	t.Parallel()

	t.Run("creates a commit with the given message", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		run := func(args ...string) {
			t.Helper()

			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		run("add", "file.txt")

		if err := client.Commit(t.Context(), []byte("feat: basic commit"), CommitOptions{}); err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		var buf bytes.Buffer
		logCmd := exec.Command("git", "log", "--format=%s", "-1")
		logCmd.Dir = dir
		logCmd.Stdout = &buf
		_ = logCmd.Run()

		if strings.TrimSpace(buf.String()) != "feat: basic commit" {
			t.Errorf("got subject %q, want %q", strings.TrimSpace(buf.String()), "feat: basic commit")
		}
	})

	t.Run("with All flag stages only tracked files", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		// Modify the tracked file (base.go was in the initial commit).
		if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package modified\n"), 0o644); err != nil {
			t.Fatalf("write base.go: %v", err)
		}

		// Create an untracked file — must NOT end up in the commit.
		if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("should not be staged"), 0o644); err != nil {
			t.Fatalf("write untracked.txt: %v", err)
		}

		if err := client.Commit(t.Context(), []byte("chore: all flag"), CommitOptions{All: true}); err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		// Verify untracked.txt is NOT in the commit tree.
		var buf bytes.Buffer
		showCmd := exec.Command("git", "show", "--stat", "HEAD")
		showCmd.Dir = dir
		showCmd.Stdout = &buf
		_ = showCmd.Run()

		if strings.Contains(buf.String(), "untracked.txt") {
			t.Error("untracked.txt must not be in commit")
		}
		if !strings.Contains(buf.String(), "base.go") {
			t.Error("base.go must be in commit")
		}
	})

	t.Run("appends Signed-off-by trailer with signoff option", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		cmd := exec.Command("git", "add", "file.txt")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}

		err := client.Commit(t.Context(), []byte("docs: readme"), CommitOptions{
			Signoff: true,
			Author:  "Alice Dev <alice@example.com>",
		})
		if err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		var buf bytes.Buffer
		logCmd := exec.Command("git", "log", "--format=%B", "-1")
		logCmd.Dir = dir
		logCmd.Stdout = &buf
		_ = logCmd.Run()

		// --signoff appends Signed-off-by using the committer identity (git config user.*),
		// not the overridden Author. newDiskRepo configures "Test User <test@test.com>".
		if !strings.Contains(buf.String(), "Signed-off-by: Test User <test@test.com>") {
			t.Errorf("signoff trailer not found in: %q", buf.String())
		}
	})

	t.Run("overrides the author identity", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}

		addCmd := exec.Command("git", "add", "file.txt")
		addCmd.Dir = dir
		if out, err := addCmd.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}

		err := client.Commit(t.Context(), []byte("fix: author override"), CommitOptions{
			Author: "Bob Builder <bob@example.com>",
		})
		if err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		var buf bytes.Buffer
		logCmd := exec.Command("git", "log", "--format=%an <%ae>", "-1")
		logCmd.Dir = dir
		logCmd.Stdout = &buf
		_ = logCmd.Run()

		got := strings.TrimSpace(buf.String())
		if got != "Bob Builder <bob@example.com>" {
			t.Errorf("author: got %q, want %q", got, "Bob Builder <bob@example.com>")
		}
	})

	t.Run("amend rewrites the tip commit message", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		run := func(args ...string) {
			t.Helper()

			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		run("add", "file.txt")

		if err := client.Commit(t.Context(), []byte("feat: to be amended"), CommitOptions{}); err != nil {
			t.Fatalf("initial Commit: %v", err)
		}

		var countBuf bytes.Buffer
		countCmd := exec.Command("git", "rev-list", "--count", "HEAD")
		countCmd.Dir = dir
		countCmd.Stdout = &countBuf
		_ = countCmd.Run()
		countBefore := strings.TrimSpace(countBuf.String())

		if err := client.Commit(t.Context(), []byte("feat: amended message"), CommitOptions{Amend: true}); err != nil {
			t.Fatalf("amend error: %v", err)
		}

		var countBuf2 bytes.Buffer
		countCmd2 := exec.Command("git", "rev-list", "--count", "HEAD")
		countCmd2.Dir = dir
		countCmd2.Stdout = &countBuf2
		_ = countCmd2.Run()
		countAfter := strings.TrimSpace(countBuf2.String())

		if countBefore != countAfter {
			t.Errorf("commit count changed %s → %s (expected no change)", countBefore, countAfter)
		}

		var msgBuf bytes.Buffer
		msgCmd := exec.Command("git", "log", "--format=%s", "-1")
		msgCmd.Dir = dir
		msgCmd.Stdout = &msgBuf
		_ = msgCmd.Run()

		if strings.TrimSpace(msgBuf.String()) != "feat: amended message" {
			t.Errorf("tip message after amend: got %q", strings.TrimSpace(msgBuf.String()))
		}
	})
}

func TestLocalBranchNames(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	client := &Client{repo: repo}

	// newTestRepo creates one commit on master.
	names, err := client.LocalBranchNames()
	if err != nil {
		t.Fatalf("LocalBranchNames: %v", err)
	}
	if len(names) != 1 || names[0] != "master" {
		t.Errorf("LocalBranchNames = %v, want [master]", names)
	}

	// Create a second branch and verify it appears.
	if err := client.CreateBranch("feature/x", "master"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	names2, err := client.LocalBranchNames()
	if err != nil {
		t.Fatalf("LocalBranchNames after create: %v", err)
	}
	if len(names2) != 2 {
		t.Errorf("got %d branch names, want 2: %v", len(names2), names2)
	}
}

func TestDefaultBaseBranch(t *testing.T) {
	t.Parallel()

	t.Run("falls back to master when no remote HEAD exists", func(t *testing.T) {
		t.Parallel()

		// newTestRepo creates a repo with no remotes and commits on the default branch.
		// go-git initializes with "master" by default.
		repo := newTestRepo(t)
		client := &Client{repo: repo}

		base, err := client.DefaultBaseBranch()
		if err != nil {
			t.Fatalf("DefaultBaseBranch: %v", err)
		}
		if base != "master" {
			t.Errorf("DefaultBaseBranch = %q, want %q", base, "master")
		}
	})

	t.Run("returns branch from refs/remotes/origin/HEAD", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)

		// Simulate refs/remotes/origin/HEAD pointing to "main".
		// In go-git, set a symbolic reference directly in the storer.
		symRef := plumbing.NewSymbolicReference(
			plumbing.ReferenceName("refs/remotes/origin/HEAD"),
			plumbing.ReferenceName("refs/remotes/origin/main"),
		)
		if err := repo.Storer.SetReference(symRef); err != nil {
			t.Fatalf("set origin/HEAD: %v", err)
		}

		client := &Client{repo: repo, remote: "origin", remoteResolved: true}
		base, err := client.DefaultBaseBranch()
		if err != nil {
			t.Fatalf("DefaultBaseBranch: %v", err)
		}
		if base != "main" {
			t.Errorf("DefaultBaseBranch = %q, want %q", base, "main")
		}
	})

	t.Run("falls back to main when master ref is absent", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)

		wt, err := repo.Worktree()
		if err != nil {
			t.Fatalf("worktree: %v", err)
		}
		if err := wt.Checkout(&gogit.CheckoutOptions{
			Branch: "refs/heads/main",
			Create: true,
		}); err != nil {
			t.Fatalf("checkout main: %v", err)
		}

		// Remove master so only main exists — isolates the fallback priority.
		if err := repo.Storer.RemoveReference(plumbing.ReferenceName("refs/heads/master")); err != nil {
			t.Fatalf("remove master: %v", err)
		}

		client := &Client{repo: repo}
		base, err := client.DefaultBaseBranch()
		if err != nil {
			t.Fatalf("DefaultBaseBranch: %v", err)
		}
		if base != "main" {
			t.Errorf("DefaultBaseBranch = %q, want %q", base, "main")
		}
	})

	t.Run("uses configured remote instead of origin for HEAD lookup", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		// Simulate refs/remotes/pi/HEAD pointing to "develop".
		symRef := plumbing.NewSymbolicReference(
			plumbing.ReferenceName("refs/remotes/pi/HEAD"),
			plumbing.ReferenceName("refs/remotes/pi/develop"),
		)
		if err := repo.Storer.SetReference(symRef); err != nil {
			t.Fatalf("set pi/HEAD: %v", err)
		}

		client := &Client{repo: repo, remote: "pi", remoteResolved: true}
		base, err := client.DefaultBaseBranch()
		if err != nil {
			t.Fatalf("DefaultBaseBranch: %v", err)
		}
		if base != "develop" {
			t.Errorf("DefaultBaseBranch = %q, want %q", base, "develop")
		}
	})

	t.Run("skips remote HEAD lookup when no remote and falls back to local", func(t *testing.T) {
		t.Parallel()

		// newTestRepo creates a repo with no remotes; go-git default branch is "master".
		repo := newTestRepo(t)
		client := &Client{repo: repo}

		base, err := client.DefaultBaseBranch()
		if err != nil {
			t.Fatalf("DefaultBaseBranch: %v", err)
		}
		if base != "master" {
			t.Errorf("DefaultBaseBranch = %q, want %q", base, "master")
		}
	})
}

func TestIsMergedInto(t *testing.T) {
	t.Parallel()

	t.Run("returns true after fast-forward merge", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)

		run := func(args ...string) {
			t.Helper()

			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		run("checkout", "-b", "feature/y")

		if err := os.WriteFile(filepath.Join(dir, "feat.txt"), []byte("content"), 0o644); err != nil {
			t.Fatalf("write feat.txt: %v", err)
		}
		run("add", "feat.txt")
		run("commit", "-m", "feat: add feature")
		run("checkout", "main")
		run("merge", "--ff-only", "feature/y")

		merged, err := client.IsMergedInto("feature/y", "main")
		if err != nil {
			t.Fatalf("IsMergedInto: %v", err)
		}
		if !merged {
			t.Error("IsMergedInto = false, want true after fast-forward merge")
		}
	})

	t.Run("returns false for an unmerged branch", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)

		run := func(args ...string) {
			t.Helper()

			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		run("checkout", "-b", "feature/z")

		if err := os.WriteFile(filepath.Join(dir, "unmerged.txt"), []byte("content"), 0o644); err != nil {
			t.Fatalf("write unmerged.txt: %v", err)
		}
		run("add", "unmerged.txt")
		run("commit", "-m", "feat: unmerged")
		run("checkout", "main")

		merged, err := client.IsMergedInto("feature/z", "main")
		if err != nil {
			t.Fatalf("IsMergedInto: %v", err)
		}
		if merged {
			t.Error("IsMergedInto = true, want false for unmerged branch")
		}
	})

	t.Run("uses configured remote for tracking ref fallback", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)

		// Create feature branch from the initial commit.
		wt, err := repo.Worktree()
		if err != nil {
			t.Fatalf("worktree: %v", err)
		}
		if err := wt.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/feature", Create: true}); err != nil {
			t.Fatalf("checkout feature: %v", err)
		}

		// Read HEAD hash (same commit on both branches at this point).
		head, err := repo.Head()
		if err != nil {
			t.Fatalf("head: %v", err)
		}

		// Simulate refs/remotes/pi/main pointing at HEAD.
		if err := repo.Storer.SetReference(plumbing.NewHashReference(
			plumbing.ReferenceName("refs/remotes/pi/main"),
			head.Hash(),
		)); err != nil {
			t.Fatalf("set pi/main: %v", err)
		}

		client := &Client{repo: repo, remote: "pi", remoteResolved: true}
		// feature == pi/main so it should be considered merged.
		merged, err := client.IsMergedInto("feature", "main")
		if err != nil {
			t.Fatalf("IsMergedInto: %v", err)
		}
		if !merged {
			t.Error("IsMergedInto = false, want true")
		}
	})
}

func TestCreateBranch(t *testing.T) {
	t.Parallel()

	t.Run("creates branch and switches HEAD", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		client := &Client{repo: repo}

		if err := client.CreateBranch("ABC-42@feat@add-oauth-login@550e8400", "master"); err != nil {
			t.Fatalf("CreateBranch: %v", err)
		}

		// Verify HEAD points to the new branch.
		head, err := repo.Head()
		if err != nil {
			t.Fatalf("Head: %v", err)
		}
		if head.Name().Short() != "ABC-42@feat@add-oauth-login@550e8400" {
			t.Errorf("HEAD = %q, want new branch", head.Name().Short())
		}
	})

	t.Run("succeeds even with unstaged working-tree changes", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		wt, _ := repo.Worktree()

		// Create an unstaged modification to an already-tracked file.
		f, err := wt.Filesystem.OpenFile("README.md", 2|0x200, 0o644) // O_WRONLY|O_TRUNC
		if err != nil {
			t.Fatalf("open README.md: %v", err)
		}
		_, _ = f.Write([]byte("# unstaged change"))
		_ = f.Close()

		client := &Client{repo: repo}

		// Must not fail even though the worktree has unstaged changes.
		if err := client.CreateBranch("42@fix@some-fix@aabbccdd", "master"); err != nil {
			t.Fatalf("CreateBranch with unstaged changes: %v", err)
		}

		head, err := repo.Head()
		if err != nil {
			t.Fatalf("Head: %v", err)
		}
		if head.Name().Short() != "42@fix@some-fix@aabbccdd" {
			t.Errorf("HEAD = %q, want new branch", head.Name().Short())
		}
	})
}

func TestCurrentBranch(t *testing.T) {
	t.Parallel()

	t.Run("returns master for a freshly initialized in-memory repo", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		client := &Client{repo: repo}

		got, err := client.CurrentBranch()
		if err != nil {
			t.Fatalf("CurrentBranch: %v", err)
		}
		if got != "master" {
			t.Errorf("CurrentBranch = %q, want %q", got, "master")
		}
	})

	t.Run("returns the issue branch name after CreateBranch", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		client := &Client{repo: repo}

		const name = "ABC-42@feat@add-oauth-login@a1b2c3d4"
		if err := client.CreateBranch(name, "master"); err != nil {
			t.Fatalf("CreateBranch: %v", err)
		}

		got, err := client.CurrentBranch()
		if err != nil {
			t.Fatalf("CurrentBranch: %v", err)
		}
		if got != name {
			t.Errorf("CurrentBranch = %q, want %q", got, name)
		}
	})

	t.Run("returns error for a repo with no commits", func(t *testing.T) {
		t.Parallel()

		repo, err := gogit.Init(memory.NewStorage(), gogit.WithWorkTree(memfs.New()))
		if err != nil {
			t.Fatalf("init: %v", err)
		}
		client := &Client{repo: repo}

		if _, err := client.CurrentBranch(); err == nil {
			t.Error("CurrentBranch on empty repo: expected error, got nil")
		}
	})
}

func TestClientIO_returnsInjectedStreams(t *testing.T) {
	t.Parallel()

	in := bytes.NewBufferString("")
	out := &bytes.Buffer{}
	errW := &bytes.Buffer{}

	c, dir := newDiskRepo(t)
	_ = dir

	c.io = &pkg.IO{In: in, Out: out, Err: errW}

	got := c.IO()
	if got == nil {
		t.Fatal("IO() returned nil")
	}

	if got.In != in || got.Out != out || got.Err != errW {
		t.Errorf("IO() returned a different struct than was injected")
	}
}

func TestIsDirty(t *testing.T) {
	t.Parallel()

	t.Run("returns false for a clean repository", func(t *testing.T) {
		t.Parallel()

		c, _ := newDiskRepo(t)

		dirty, err := c.IsDirty(t.Context())
		if err != nil {
			t.Fatalf("IsDirty: %v", err)
		}

		if dirty {
			t.Error("expected clean repo to report dirty=false")
		}
	})

	t.Run("returns true for a modified tracked file", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		if err := os.WriteFile(filepath.Join(dir, "base.go"), []byte("package other\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		dirty, err := c.IsDirty(t.Context())
		if err != nil {
			t.Fatalf("IsDirty: %v", err)
		}

		if !dirty {
			t.Error("expected modified tracked file to report dirty=true")
		}
	})

	t.Run("returns true for a staged change", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package main\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		stage := exec.CommandContext(t.Context(), "git", "add", "new.go")
		stage.Dir = dir
		if out, err := stage.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}

		dirty, err := c.IsDirty(t.Context())
		if err != nil {
			t.Fatalf("IsDirty: %v", err)
		}

		if !dirty {
			t.Error("expected staged file to report dirty=true")
		}
	})

	t.Run("returns false for untracked files only", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		if err := os.WriteFile(filepath.Join(dir, "scratch.txt"), []byte("note\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		dirty, err := c.IsDirty(t.Context())
		if err != nil {
			t.Fatalf("IsDirty: %v", err)
		}

		if dirty {
			t.Error("expected untracked-only repo to report dirty=false")
		}
	})
}

func TestRemote(t *testing.T) {
	t.Parallel()

	t.Run("returns empty string for repo with no remotes", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		c := &Client{repo: repo}

		remote, err := c.Remote()
		if err != nil {
			t.Fatalf("Remote: %v", err)
		}
		if remote != "" {
			t.Errorf("Remote = %q, want %q", remote, "")
		}
	})

	t.Run("returns the sole remote name", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
			Name: "pi",
			URLs: []string{"https://example.com/repo.git"},
		}); err != nil {
			t.Fatalf("CreateRemote: %v", err)
		}
		c := &Client{repo: repo}

		remote, err := c.Remote()
		if err != nil {
			t.Fatalf("Remote: %v", err)
		}
		if remote != "pi" {
			t.Errorf("Remote = %q, want %q", remote, "pi")
		}
	})

	t.Run("returns origin when multiple remotes include origin", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		for _, name := range []string{"origin", "upstream"} {
			if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
				Name: name,
				URLs: []string{"https://example.com/" + name + ".git"},
			}); err != nil {
				t.Fatalf("CreateRemote %s: %v", name, err)
			}
		}
		c := &Client{repo: repo}

		remote, err := c.Remote()
		if err != nil {
			t.Fatalf("Remote: %v", err)
		}
		if remote != "origin" {
			t.Errorf("Remote = %q, want %q", remote, "origin")
		}
	})

	t.Run("errors when multiple remotes exist with no origin", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		for _, name := range []string{"pi", "upstream"} {
			if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
				Name: name,
				URLs: []string{"https://example.com/" + name + ".git"},
			}); err != nil {
				t.Fatalf("CreateRemote %s: %v", name, err)
			}
		}
		c := &Client{repo: repo}

		_, err := c.Remote()
		if err == nil {
			t.Fatal("Remote: expected error, got nil")
		}
		if !strings.Contains(err.Error(), "branch.remote") {
			t.Errorf("error %q does not mention branch.remote config key", err.Error())
		}
	})

	t.Run("SetRemote pins the name, bypassing detection", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		c := &Client{repo: repo}
		c.SetRemote("pi")

		remote, err := c.Remote()
		if err != nil {
			t.Fatalf("Remote: %v", err)
		}
		if remote != "pi" {
			t.Errorf("Remote = %q, want %q", remote, "pi")
		}
	})

	t.Run("caches result on second call", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
			Name: "pi",
			URLs: []string{"https://example.com/repo.git"},
		}); err != nil {
			t.Fatalf("CreateRemote: %v", err)
		}
		c := &Client{repo: repo}

		r1, err := c.Remote()
		if err != nil {
			t.Fatalf("first Remote: %v", err)
		}
		// Simulate the remote disappearing — cache should win.
		if err := repo.DeleteRemote("pi"); err != nil {
			t.Fatalf("DeleteRemote: %v", err)
		}
		r2, err := c.Remote()
		if err != nil {
			t.Fatalf("second Remote: %v", err)
		}
		if r1 != r2 {
			t.Errorf("second call = %q, want cached %q", r2, r1)
		}
	})

	t.Run("caches no-remote result on second call", func(t *testing.T) {
		t.Parallel()

		repo := newTestRepo(t)
		c := &Client{repo: repo}

		// First call: no remotes → ("", nil)
		r1, err := c.Remote()
		if err != nil {
			t.Fatalf("first Remote: %v", err)
		}
		if r1 != "" {
			t.Fatalf("first Remote = %q, want empty", r1)
		}

		// Add a remote — second call must return cached "" (not re-detect)
		if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
			Name: "pi",
			URLs: []string{"https://example.com/repo.git"},
		}); err != nil {
			t.Fatalf("CreateRemote: %v", err)
		}

		r2, err := c.Remote()
		if err != nil {
			t.Fatalf("second Remote: %v", err)
		}
		if r2 != "" {
			t.Errorf("second Remote = %q, want cached empty string", r2)
		}
	})
}

func TestCheckout(t *testing.T) {
	t.Parallel()

	t.Run("switches HEAD to the target branch", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		newBranch := exec.CommandContext(t.Context(), "git", "checkout", "-b", "feature-x")
		newBranch.Dir = dir
		if out, err := newBranch.CombinedOutput(); err != nil {
			t.Fatalf("create feature-x: %v\n%s", err, out)
		}

		if err := c.Checkout(t.Context(), "main"); err != nil {
			t.Fatalf("Checkout main: %v", err)
		}

		got, err := c.CurrentBranch()
		if err != nil {
			t.Fatalf("CurrentBranch: %v", err)
		}

		if got != "main" {
			t.Errorf("CurrentBranch = %q, want %q", got, "main")
		}
	})

	t.Run("does not fire post-checkout hook when already on target branch", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		hookPath := filepath.Join(dir, ".git", "hooks", "post-checkout")
		markerPath := filepath.Join(dir, "post-checkout-fired")
		hook := "#!/bin/sh\ntouch " + markerPath + "\n"
		if err := os.WriteFile(hookPath, []byte(hook), 0o755); err != nil {
			t.Fatalf("write post-checkout hook: %v", err)
		}

		if err := c.Checkout(t.Context(), "main"); err != nil {
			t.Fatalf("Checkout main (already on main): %v", err)
		}

		if _, err := os.Stat(markerPath); !os.IsNotExist(err) {
			t.Errorf("post-checkout hook should NOT fire when already on target branch, stat err = %v", err)
		}
	})
}

func TestRepoName(t *testing.T) {
	t.Parallel()

	t.Run("returns last segment of HTTPS remote URL without .git suffix", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		run("remote", "add", "origin", "https://github.com/piprim/git-zf.git")

		name, err := c.RepoName()
		if err != nil {
			t.Fatalf("RepoName: %v", err)
		}
		if name != "git-zf" {
			t.Errorf("got %q, want %q", name, "git-zf")
		}
	})

	t.Run("returns last segment of SSH remote URL without .git suffix", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)
		run := func(args ...string) {
			t.Helper()
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}
		run("remote", "add", "origin", "git@github.com:piprim/git-zf.git")

		name, err := c.RepoName()
		if err != nil {
			t.Fatalf("RepoName: %v", err)
		}
		if name != "git-zf" {
			t.Errorf("got %q, want %q", name, "git-zf")
		}
	})

	t.Run("falls back to directory name when no remote", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)

		name, err := c.RepoName()
		if err != nil {
			t.Fatalf("RepoName: %v", err)
		}
		if name != filepath.Base(dir) {
			t.Errorf("got %q, want %q", name, filepath.Base(dir))
		}
	})
}

func TestCreateWorktree(t *testing.T) {
	t.Parallel()

	t.Run("creates a linked worktree at the given path", func(t *testing.T) {
		t.Parallel()

		c, dir := newDiskRepo(t)
		worktreePath := filepath.Join(t.TempDir(), "myrepo--feat-123-thing")

		if err := c.CreateWorktree(t.Context(), "feat/123-thing", "main", worktreePath); err != nil {
			t.Fatalf("CreateWorktree: %v", err)
		}

		// The worktree directory must exist.
		if _, err := os.Stat(worktreePath); err != nil {
			t.Fatalf("worktree dir not created: %v", err)
		}

		// The branch must exist in the main repo.
		var buf bytes.Buffer
		cmd := exec.Command("git", "branch", "--list", "feat/123-thing")
		cmd.Dir = dir
		cmd.Stdout = &buf
		if err := cmd.Run(); err != nil {
			t.Fatalf("git branch --list: %v", err)
		}
		if !strings.Contains(buf.String(), "feat/123-thing") {
			t.Errorf("branch feat/123-thing not found in main repo")
		}
	})

	t.Run("returns error when branch already exists", func(t *testing.T) {
		t.Parallel()

		c, _ := newDiskRepo(t)
		// "main" already exists.
		err := c.CreateWorktree(t.Context(), "main", "main", filepath.Join(t.TempDir(), "conflict"))
		if err == nil {
			t.Fatal("expected error for duplicate branch, got nil")
		}
	})
}

func TestBranchExists(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	client := &Client{repo: repo}

	if err := client.CreateBranch("feat-x", "master"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}

	t.Run("existing branch returns true", func(t *testing.T) {
		t.Parallel()

		ok, err := client.BranchExists("feat-x")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if !ok {
			t.Error("BranchExists(feat-x) = false, want true")
		}
	})

	t.Run("missing branch returns false", func(t *testing.T) {
		t.Parallel()

		ok, err := client.BranchExists("does-not-exist")
		if err != nil {
			t.Fatalf("BranchExists: %v", err)
		}
		if ok {
			t.Error("BranchExists(does-not-exist) = true, want false")
		}
	})
}

func TestSafeDeleteBranch(t *testing.T) {
	t.Parallel()

	t.Run("deletes a fully-merged branch", func(t *testing.T) {
		t.Parallel()

		c, dir := newTestClient(t)
		runGitInDir(t, dir, "branch", "feature-merged") // points at HEAD, fully merged into HEAD

		if err := c.SafeDeleteBranch("feature-merged"); err != nil {
			t.Fatalf("SafeDeleteBranch: %v", err)
		}

		names, err := c.LocalBranchNames()
		if err != nil {
			t.Fatalf("LocalBranchNames: %v", err)
		}
		for _, n := range names {
			if n == "feature-merged" {
				t.Fatalf("branch still present: %v", names)
			}
		}
	})

	t.Run("returns ErrBranchNotMerged when branch has unique commits", func(t *testing.T) {
		t.Parallel()

		c, dir := newTestClient(t)
		runGitInDir(t, dir, "checkout", "-b", "feature-divergent")
		writeFile(t, dir, "f.txt", "x")
		runGitInDir(t, dir, "add", "f.txt")
		runGitInDir(t, dir, "commit", "-m", "divergent")
		runGitInDir(t, dir, "checkout", "master")

		err := c.SafeDeleteBranch("feature-divergent")
		if !errors.Is(err, ErrBranchNotMerged) {
			t.Fatalf("got %v, want ErrBranchNotMerged", err)
		}
	})
}

func TestForceDeleteBranch(t *testing.T) {
	t.Parallel()

	t.Run("deletes even when not merged", func(t *testing.T) {
		t.Parallel()

		c, dir := newTestClient(t)
		runGitInDir(t, dir, "checkout", "-b", "feature-abandoned")
		writeFile(t, dir, "f.txt", "x")
		runGitInDir(t, dir, "add", "f.txt")
		runGitInDir(t, dir, "commit", "-m", "abandoned")
		runGitInDir(t, dir, "checkout", "master")

		if err := c.ForceDeleteBranch("feature-abandoned"); err != nil {
			t.Fatalf("ForceDeleteBranch: %v", err)
		}

		names, err := c.LocalBranchNames()
		if err != nil {
			t.Fatalf("LocalBranchNames: %v", err)
		}
		for _, n := range names {
			if n == "feature-abandoned" {
				t.Fatalf("branch still present: %v", names)
			}
		}
	})

	t.Run("returns error when branch does not exist", func(t *testing.T) {
		t.Parallel()

		c, _ := newTestClient(t)
		if err := c.ForceDeleteBranch("does-not-exist"); err == nil {
			t.Fatal("want error, got nil")
		}
	})
}

func TestGitDir(t *testing.T) {
	t.Run("regular repo returns <root>/.git", func(t *testing.T) {
		c, dir := newTestClient(t)

		got, err := c.GitDir()
		if err != nil {
			t.Fatalf("GitDir: %v", err)
		}

		want := filepath.Join(dir, ".git")
		// Real filesystems may differ in symlink resolution between TempDir and the
		// path git prints. Compare resolved paths to dodge that.
		gotResolved, _ := filepath.EvalSymlinks(got)
		wantResolved, _ := filepath.EvalSymlinks(want)
		if gotResolved != wantResolved {
			t.Fatalf("GitDir = %q, want %q (resolved: %q vs %q)", got, want, gotResolved, wantResolved)
		}
	})

	t.Run("submodule returns parent <root>/.git/modules/<name>", func(t *testing.T) {
		// Create a parent repo + a separate "remote" we can add as a submodule.
		_, parentDir := newTestClient(t)
		_, remoteDir := newTestClient(t)

		// Recent git refuses to add a local-path submodule without this.
		runGitInDir(t, parentDir, "-c", "protocol.file.allow=always",
			"submodule", "add", remoteDir, "sub")
		runGitInDir(t, parentDir, "commit", "-m", "add submodule")

		subDir := filepath.Join(parentDir, "sub")

		// Inside the submodule, <subDir>/.git is a gitlink FILE — verify the
		// premise so this test would fail loudly if git's submodule layout
		// changes in a future version.
		info, err := os.Stat(filepath.Join(subDir, ".git"))
		if err != nil {
			t.Fatalf("stat sub/.git: %v", err)
		}
		if info.IsDir() {
			t.Fatalf("sub/.git is a directory — submodule layout changed; test premise invalid")
		}

		// Construct a Client pointing at the submodule worktree and resolve GitDir.
		c, err := NewClientAt(nil, subDir)
		if err != nil {
			t.Fatalf("NewClientAt(sub): %v", err)
		}

		got, err := c.GitDir()
		if err != nil {
			t.Fatalf("GitDir: %v", err)
		}

		gotResolved, _ := filepath.EvalSymlinks(got)
		wantPrefix, _ := filepath.EvalSymlinks(filepath.Join(parentDir, ".git", "modules"))
		if !strings.HasPrefix(gotResolved, wantPrefix) {
			t.Fatalf("GitDir = %q, want path under %q", got, wantPrefix)
		}

		// And the resolved GitDir is actually a directory (the bug was that
		// downstream code joined ".git" to the worktree and got a file).
		dirInfo, err := os.Stat(got)
		if err != nil {
			t.Fatalf("stat resolved GitDir: %v", err)
		}
		if !dirInfo.IsDir() {
			t.Fatalf("resolved GitDir %q is not a directory", got)
		}
	})
}

func TestAuthors(t *testing.T) {
	t.Parallel()

	t.Run("returns the seed author after a single commit", func(t *testing.T) {
		t.Parallel()

		c, _ := newTestClient(t)

		got, err := c.Authors(t.Context())
		if err != nil {
			t.Fatalf("Authors: %v", err)
		}

		want := []string{"Test User <test@test.com>"}
		if !slices.Equal(got, want) {
			t.Errorf("Authors = %q, want %q", got, want)
		}
	})

	t.Run("deduplicates repeated authors", func(t *testing.T) {
		t.Parallel()

		c, dir := newTestClient(t)

		// Two extra commits by the same seed author.
		runGitInDir(t, dir, "commit", "--allow-empty", "-m", "chore: empty 1")
		runGitInDir(t, dir, "commit", "--allow-empty", "-m", "chore: empty 2")

		got, err := c.Authors(t.Context())
		if err != nil {
			t.Fatalf("Authors: %v", err)
		}

		want := []string{"Test User <test@test.com>"}
		if !slices.Equal(got, want) {
			t.Errorf("Authors = %q, want %q", got, want)
		}
	})

	t.Run("lists every distinct author across the history", func(t *testing.T) {
		t.Parallel()

		c, dir := newTestClient(t)

		// Two more authors, each with one commit. shortlog -n orders by
		// commit count desc, so the seed (2 commits below) sorts first.
		runGitInDir(t, dir, "commit", "--allow-empty", "-m", "chore: seed extra")
		runGitInDir(t, dir, "commit", "--allow-empty",
			"--author=Alice Example <alice@example.com>", "-m", "chore: alice")
		runGitInDir(t, dir, "commit", "--allow-empty",
			"--author=Bob Example <bob@example.com>", "-m", "chore: bob")

		got, err := c.Authors(t.Context())
		if err != nil {
			t.Fatalf("Authors: %v", err)
		}

		want := []string{
			"Test User <test@test.com>",
			"Alice Example <alice@example.com>",
			"Bob Example <bob@example.com>",
		}
		if !slices.Equal(got, want) {
			t.Errorf("Authors = %q, want %q", got, want)
		}
	})

	t.Run("surfaces authors reachable only from non-HEAD refs (--all)", func(t *testing.T) {
		t.Parallel()

		c, dir := newTestClient(t)

		// Create a sibling branch with a commit by a third party, then
		// switch back. The new commit is NOT in HEAD's ancestry, but
		// shortlog --all must still surface its author.
		runGitInDir(t, dir, "checkout", "-q", "-b", "sidequest")
		runGitInDir(t, dir, "commit", "--allow-empty",
			"--author=Carol Sidequest <carol@example.com>", "-m", "chore: carol")
		runGitInDir(t, dir, "checkout", "-q", "master")

		got, err := c.Authors(t.Context())
		if err != nil {
			t.Fatalf("Authors: %v", err)
		}

		if !slices.Contains(got, "Carol Sidequest <carol@example.com>") {
			t.Errorf("Authors = %q, missing Carol from sidequest branch", got)
		}
	})

	t.Run("succeeds on a repo with no refs and returns only the configured identity", func(t *testing.T) {
		t.Parallel()

		// Brand-new repo with no commits. `git shortlog --all` exits
		// non-zero here; Authors() must swallow that ExitError and fall
		// back to "just the configured identity". Setting local user.*
		// overrides whatever /etc/gitconfig holds on the test machine,
		// so the assertion is deterministic.
		dir := t.TempDir()
		runGitInDir(t, dir, "init", "-q", "-b", "master")
		runGitInDir(t, dir, "config", "user.name", "Empty Repo")
		runGitInDir(t, dir, "config", "user.email", "empty@example.com")

		c, err := NewClientAt(nil, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}

		got, err := c.Authors(t.Context())
		if err != nil {
			t.Fatalf("Authors: %v", err)
		}

		want := []string{"Empty Repo <empty@example.com>"}
		if !slices.Equal(got, want) {
			t.Errorf("Authors = %q, want %q", got, want)
		}
	})
}

// newTestClient initialises a real on-disk git repo in a temp dir with one
// initial commit on "master" and returns a Client + the repo directory.
// The repo uses "master" (not "main") so it matches the prune-tracker tests
// which branch off master by convention.
func newTestClient(t *testing.T) (*Client, string) {
	t.Helper()

	dir := t.TempDir()

	runGitInDir(t, dir, "init", "-q", "-b", "master")
	runGitInDir(t, dir, "config", "user.name", "Test User")
	runGitInDir(t, dir, "config", "user.email", "test@test.com")
	runGitInDir(t, dir, "config", "commit.gpgsign", "false")

	writeFile(t, dir, "base.txt", "base\n")
	runGitInDir(t, dir, "add", "base.txt")
	runGitInDir(t, dir, "commit", "-m", "chore: init")

	c, err := NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

// runGitInDir runs git with the given args inside dir, failing the test on
// non-zero exit.
func runGitInDir(t *testing.T, dir string, args ...string) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// writeFile writes content to name inside dir, failing the test on error.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("writeFile %s: %v", name, err)
	}
}
