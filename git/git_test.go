package git

import (
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"

	// go-git v6 depends on go-billy/v6.
	"github.com/go-git/go-billy/v6/memfs"
)

// newTestRepo creates an in-memory git repository with one initial commit.
// User is configured as "Test User <test@example.com>" in the repo config so
// that commits without an explicit Author succeed.
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

	_, err = wt.Commit("chore: init", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	return repo
}

// stageNewFile creates filename in wt, writes content, and stages it.
func stageNewFile(t *testing.T, wt *gogit.Worktree, filename, content string) {
	t.Helper()

	f, err := wt.Filesystem.Create(filename)
	if err != nil {
		t.Fatalf("create %s: %v", filename, err)
	}
	_, _ = f.Write([]byte(content))
	_ = f.Close()

	if _, err := wt.Add(filename); err != nil {
		t.Fatalf("stage %s: %v", filename, err)
	}
}

func TestCommit_basic(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "hello")
	client := &Client{repo: repo}

	if _, err := client.Commit([]byte("feat: basic commit"), CommitOptions{}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head: %v", err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if c.Message != "feat: basic commit" {
		t.Errorf("got message %q, want %q", c.Message, "feat: basic commit")
	}
}

func TestCommit_all_stagesTrackedOnly(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()

	// Modify the tracked file (README.md was in the initial commit).
	f, _ := wt.Filesystem.Create("README.md")
	_, _ = f.Write([]byte("# modified"))
	_ = f.Close()

	// Create an untracked file — must NOT end up in the commit.
	u, _ := wt.Filesystem.Create("untracked.txt")
	_, _ = u.Write([]byte("should not be staged"))
	_ = u.Close()

	client := &Client{repo: repo}

	if _, err := client.Commit([]byte("chore: all flag"), CommitOptions{All: true}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head: %v", err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	tree, err := repo.TreeObject(c.TreeHash)
	if err != nil {
		t.Fatalf("TreeObject: %v", err)
	}

	if _, err := tree.File("README.md"); err != nil {
		t.Error("README.md not found in commit tree")
	}
	if _, err := tree.File("untracked.txt"); err == nil {
		t.Error("untracked.txt must not be in commit tree")
	}
}

func TestCommit_signoff(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "x")
	client := &Client{repo: repo}

	_, err := client.Commit([]byte("docs: readme"), CommitOptions{
		Signoff: true,
		Author:  "Alice Dev <alice@example.com>",
	})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head: %v", err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if !strings.Contains(c.Message, "Signed-off-by: Alice Dev <alice@example.com>") {
		t.Errorf("signoff trailer not found in: %q", c.Message)
	}
}

func TestCommit_author(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "x")
	client := &Client{repo: repo}

	_, err := client.Commit([]byte("fix: author override"), CommitOptions{
		Author: "Bob Builder <bob@example.com>",
	})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head: %v", err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if c.Author.Name != "Bob Builder" || c.Author.Email != "bob@example.com" {
		t.Errorf("author: got %q <%s>, want Bob Builder <bob@example.com>",
			c.Author.Name, c.Author.Email)
	}
}

func TestCommit_amend(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "x")
	client := &Client{repo: repo}

	// Second commit — the one we will amend.
	if _, err := client.Commit([]byte("feat: to be amended"), CommitOptions{}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	iter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		t.Fatalf("repo.Log: %v", err)
	}
	countBefore := 0
	_ = iter.ForEach(func(_ *object.Commit) error { countBefore++; return nil })

	// Amend: replace the tip commit message.
	if _, err := client.Commit([]byte("feat: amended message"), CommitOptions{Amend: true}); err != nil {
		t.Fatalf("amend error: %v", err)
	}

	iter2, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		t.Fatalf("repo.Log: %v", err)
	}
	countAfter := 0
	_ = iter2.ForEach(func(_ *object.Commit) error { countAfter++; return nil })
	if countAfter != countBefore {
		t.Errorf("commit count changed: %d → %d (expected no change)", countBefore, countAfter)
	}

	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("repo.Head: %v", err)
	}
	c, err := repo.CommitObject(ref.Hash())
	if err != nil {
		t.Fatalf("CommitObject: %v", err)
	}
	if c.Message != "feat: amended message" {
		t.Errorf("tip message after amend: got %q", c.Message)
	}
}

func TestDefaultBaseBranch_fallback(t *testing.T) {
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
}

func TestDefaultBaseBranch_remote(t *testing.T) {
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
}

func TestDefaultBaseBranch_main(t *testing.T) {
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
}

func TestCommit_summary(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "feature.go", "package main\n\nfunc New() {}\n")
	client := &Client{repo: repo}

	summary, err := client.Commit([]byte("feat: add feature\n\nsome body"), CommitOptions{})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	if len(summary.ShortHash) != 7 {
		t.Errorf("ShortHash len = %d, want 7", len(summary.ShortHash))
	}
	if summary.Branch != "master" {
		t.Errorf("Branch = %q, want %q", summary.Branch, "master")
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
	if summary.Deletions != 0 {
		t.Errorf("Deletions = %d, want 0", summary.Deletions)
	}
}

func TestCommit_summary_rootCommit(t *testing.T) {
	t.Parallel()

	repo, err := gogit.Init(memory.NewStorage(), gogit.WithWorkTree(memfs.New()))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	cfg, _ := repo.Config()
	cfg.User.Name = "Test User"
	cfg.User.Email = "test@example.com"
	_ = repo.SetConfig(cfg)

	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "init.txt", "hello")

	client := &Client{repo: repo}
	summary, err := client.Commit([]byte("chore: initial commit"), CommitOptions{})
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

func TestIsMergedInto_merged(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	client := &Client{repo: repo}

	// Create a feature branch with one extra commit.
	if err := client.CreateBranch("feature/y", "master"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	stageNewFile(t, wt, "feat.txt", "content")
	if _, err := client.Commit([]byte("feat: add feature"), CommitOptions{}); err != nil {
		t.Fatalf("Commit on feature: %v", err)
	}

	// Switch back to master and merge feature/y by fast-forward.
	featureRef, _ := repo.Reference(plumbing.ReferenceName("refs/heads/feature/y"), true)
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/master", Keep: true}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}
	// Fast-forward master to feature/y's tip.
	masterRef := plumbing.NewHashReference(plumbing.ReferenceName("refs/heads/master"), featureRef.Hash())
	if err := repo.Storer.SetReference(masterRef); err != nil {
		t.Fatalf("fast-forward master: %v", err)
	}

	merged, err := client.IsMergedInto("feature/y", "master")
	if err != nil {
		t.Fatalf("IsMergedInto: %v", err)
	}
	if !merged {
		t.Error("IsMergedInto = false, want true after fast-forward merge")
	}
}

func TestIsMergedInto_notMerged(t *testing.T) {
	t.Parallel()

	repo := newTestRepo(t)
	client := &Client{repo: repo}

	// Create a feature branch but do NOT merge it.
	if err := client.CreateBranch("feature/z", "master"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "unmerged.txt", "content")
	if _, err := client.Commit([]byte("feat: unmerged"), CommitOptions{}); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Switch back to master (which hasn't moved).
	if err := wt.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/master", Keep: true}); err != nil {
		t.Fatalf("checkout master: %v", err)
	}

	merged, err := client.IsMergedInto("feature/z", "master")
	if err != nil {
		t.Fatalf("IsMergedInto: %v", err)
	}
	if merged {
		t.Error("IsMergedInto = true, want false for unmerged branch")
	}
}

func TestCreateBranch(t *testing.T) {
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
}

func TestCreateBranch_withUnstagedChanges(t *testing.T) {
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
}

func TestCurrentBranch_master(t *testing.T) {
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
}

func TestCurrentBranch_issueBranch(t *testing.T) {
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
}

func TestCurrentBranch_emptyRepo(t *testing.T) {
	t.Parallel()

	repo, err := gogit.Init(memory.NewStorage(), gogit.WithWorkTree(memfs.New()))
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	client := &Client{repo: repo}

	if _, err := client.CurrentBranch(); err == nil {
		t.Error("CurrentBranch on empty repo: expected error, got nil")
	}
}
