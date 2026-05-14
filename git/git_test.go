package git

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v6"
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

		if _, err := client.Commit(t.Context(), []byte("feat: basic commit"), CommitOptions{}); err != nil {
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

		if _, err := client.Commit(t.Context(), []byte("chore: all flag"), CommitOptions{All: true}); err != nil {
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

		_, err := client.Commit(t.Context(), []byte("docs: readme"), CommitOptions{
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

		_, err := client.Commit(t.Context(), []byte("fix: author override"), CommitOptions{
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

		if _, err := client.Commit(t.Context(), []byte("feat: to be amended"), CommitOptions{}); err != nil {
			t.Fatalf("initial Commit: %v", err)
		}

		var countBuf bytes.Buffer
		countCmd := exec.Command("git", "rev-list", "--count", "HEAD")
		countCmd.Dir = dir
		countCmd.Stdout = &countBuf
		_ = countCmd.Run()
		countBefore := strings.TrimSpace(countBuf.String())

		if _, err := client.Commit(t.Context(), []byte("feat: amended message"), CommitOptions{Amend: true}); err != nil {
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

	t.Run("returns a correct commit summary", func(t *testing.T) {
		t.Parallel()

		client, dir := newDiskRepo(t)
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		if err := os.WriteFile(filepath.Join(dir, "feature.go"), []byte("package main\n\nfunc New() {}\n"), 0o644); err != nil {
			t.Fatalf("write feature.go: %v", err)
		}

		addCmd := exec.Command("git", "add", "feature.go")
		addCmd.Dir = dir
		if out, err := addCmd.CombinedOutput(); err != nil {
			t.Fatalf("git add: %v\n%s", err, out)
		}

		summary, err := client.Commit(t.Context(), []byte("feat: add feature\n\nsome body"), CommitOptions{})
		if err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		if len(summary.ShortHash) != 7 {
			t.Errorf("ShortHash len = %d, want 7", len(summary.ShortHash))
		}
		if summary.Branch != "main" {
			t.Errorf("Branch = %q, want %q", summary.Branch, "main")
		}
		if summary.IsRoot {
			t.Error("IsRoot = true, want false")
		}
		if summary.Subject != "feat: add feature" {
			t.Errorf("Subject = %q, want %q", summary.Subject, "feat: add feature")
		}
		if summary.Files != 1 {
			t.Errorf("Files = %d, want 1", summary.Files)
		}
		if summary.Additions == 0 {
			t.Error("Additions = 0, want > 0")
		}
	})

	t.Run("marks root commit in the summary", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()

		run := func(args ...string) {
			t.Helper()

			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, out)
			}
		}

		run("init", "--initial-branch=main")
		run("config", "user.name", "Test User")
		run("config", "user.email", "test@test.com")
		run("config", "commit.gpgsign", "false")

		if err := os.WriteFile(filepath.Join(dir, "init.txt"), []byte("hello"), 0o644); err != nil {
			t.Fatalf("write init.txt: %v", err)
		}
		run("add", "init.txt")

		client, err := NewClientAt(nil, dir)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		client.io = &pkg.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}

		summary, err := client.Commit(t.Context(), []byte("chore: initial commit"), CommitOptions{})
		if err != nil {
			t.Fatalf("Commit error: %v", err)
		}

		if !summary.IsRoot {
			t.Error("IsRoot = false, want true")
		}
		if summary.Files != 1 {
			t.Errorf("Files = %d, want 1", summary.Files)
		}
		if summary.Additions == 0 {
			t.Error("Additions = 0, want > 0 for root commit")
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

		client := &Client{repo: repo}
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
