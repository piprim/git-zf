# Worktree on Issue Start — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When running `git zf issue start`, let the user choose to create a git worktree instead of a plain branch checkout, with config-driven defaults.

**Architecture:** Four independent layers in order — config, git, tui, cmd. Each task is self-contained and tested before moving on. `cmd/issue/start.go` is wired up last once all dependencies are in place.

**Tech Stack:** Go, go-git v6, charmbracelet/huh, spf13/cobra, spf13/viper, mitchellh/go-homedir. Run Go via `mise exec -- go …`.

---

### Task 1: Extend `BranchConfig` with worktree fields

**Files:**
- Modify: `config/config.go`

- [ ] **Step 1: Add fields to `BranchConfig`**

In `config/config.go`, replace:

```go
type BranchConfig struct {
	Base   string `json:"base"   toml:"base"   mapstructure:"base"`
	Remote string `json:"remote" toml:"remote" mapstructure:"remote"`
}
```

with:

```go
type BranchConfig struct {
	Base        string `json:"base"          toml:"base"          mapstructure:"base"`
	Remote      string `json:"remote"        toml:"remote"        mapstructure:"remote"`
	UseWorktree *bool  `json:"use-worktree"  toml:"use-worktree"  mapstructure:"use-worktree"`
	WorktreeDir string `json:"worktree-dir"  toml:"worktree-dir"  mapstructure:"worktree-dir"`
}
```

- [ ] **Step 2: Load the new fields from viper in `Load()`**

In `config/config.go`, after the existing `branch.remote` block, add:

```go
if viper.IsSet("branch.use-worktree") {
    v := viper.GetBool("branch.use-worktree")
    cfg.Branch.UseWorktree = &v
}

if viper.IsSet("branch.worktree-dir") {
    cfg.Branch.WorktreeDir = viper.GetString("branch.worktree-dir")
}
```

- [ ] **Step 3: Build to verify no compilation errors**

```bash
mise exec -- go build ./...
```

Expected: exits 0, no output.

- [ ] **Step 4: Commit**

```bash
git add config/config.go
git commit -m "feat(config): add use-worktree and worktree-dir to BranchConfig"
```

---

### Task 2: `git.Client.RepoName()`

**Files:**
- Modify: `git/git.go`
- Modify: `git/git_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `git/git_test.go`:

```go
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
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./git/... -run TestRepoName -v
```

Expected: FAIL — `c.RepoName undefined`.

- [ ] **Step 3: Implement `RepoName()` in `git/git.go`**

Add after `LocalBranchNames()`:

```go
// RepoName returns a short identifier for this repository.
// Resolution order:
//  1. Last path segment of the configured remote URL, with ".git" stripped.
//  2. Base name of the working tree root directory (local-only fallback).
func (c *Client) RepoName() (string, error) {
	remote, err := c.Remote()
	if err != nil {
		return "", fmt.Errorf("resolve remote: %w", err)
	}

	if remote != "" {
		remotes, err := c.repo.Remotes()
		if err != nil {
			return "", fmt.Errorf("list remotes: %w", err)
		}

		for _, r := range remotes {
			if r.Config().Name == remote && len(r.Config().URLs) > 0 {
				u := r.Config().URLs[0]
				// Strip trailing slashes then take last segment.
				u = strings.TrimRight(u, "/")
				seg := u[strings.LastIndexAny(u, "/:")+1:]
				seg = strings.TrimSuffix(seg, ".git")

				if seg != "" {
					return seg, nil
				}
			}
		}
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	return filepath.Base(root), nil
}
```

Add `"path/filepath"` to the import block in `git/git.go` if not already present.

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./git/... -run TestRepoName -v
```

Expected: all three subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add RepoName() resolving from remote URL or directory name"
```

---

### Task 3: `git.Client.CreateWorktree()`

**Files:**
- Modify: `git/git.go`
- Modify: `git/git_test.go`

- [ ] **Step 1: Write the failing test**

Add to `git/git_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./git/... -run TestCreateWorktree -v
```

Expected: FAIL — `c.CreateWorktree undefined`.

- [ ] **Step 3: Implement `CreateWorktree()` in `git/git.go`**

Add after `CreateBranch()`:

```go
// CreateWorktree creates a new branch from baseBranch and checks it out
// in a linked worktree at path. Wraps `git worktree add -b <branch> <path> <base>`.
func (c *Client) CreateWorktree(ctx context.Context, branchName, baseBranch, path string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "worktree", "add", "-b", branchName, path, baseBranch); err != nil {
		return fmt.Errorf("create worktree %q: %w", path, err)
	}

	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./git/... -run TestCreateWorktree -v
```

Expected: both subtests PASS.

- [ ] **Step 5: Run the full git test suite**

```bash
mise exec -- go test ./git/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add CreateWorktree() wrapping git worktree add"
```

---

### Task 4: `worktreePath()` helper and tests

**Files:**
- Modify: `cmd/issue/start.go` (add pure helper function)
- Create: `cmd/issue/start_test.go`

- [ ] **Step 1: Write failing tests**

Create `cmd/issue/start_test.go`:

```go
package issue

import (
	"path/filepath"
	"testing"
)

func TestWorktreePath(t *testing.T) {
	t.Parallel()

	t.Run("uses sibling of repo root when worktreeDir is empty", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "", "myapp", "feat-123-login")
		want := "/home/user/code/myapp--feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("uses configured worktreeDir as base", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "/worktrees", "myapp", "feat-123-login")
		want := "/worktrees/myapp--feat-123-login"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("expands tilde in worktreeDir", func(t *testing.T) {
		t.Parallel()

		repoRoot := "/home/user/code/myapp"
		got := worktreePath(repoRoot, "~/worktrees", "myapp", "feat-123-login")
		// ~ expansion produces an absolute path that must not start with ~
		if got[:1] == "~" {
			t.Errorf("tilde was not expanded: %q", got)
		}
		if filepath.Base(got) != "myapp--feat-123-login" {
			t.Errorf("unexpected basename %q", filepath.Base(got))
		}
	})
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./cmd/issue/... -run TestWorktreePath -v
```

Expected: FAIL — `worktreePath undefined`.

- [ ] **Step 3: Implement `worktreePath()` in `cmd/issue/start.go`**

Add at the bottom of `cmd/issue/start.go`, with the new import `"github.com/mitchellh/go-homedir"` added to the import block:

```go
// worktreePath computes the absolute path for a new worktree.
// baseDir overrides the default (sibling of repoRoot) when non-empty; ~ is expanded.
func worktreePath(repoRoot, baseDir, repoName, branchName string) string {
	base := baseDir
	if base == "" {
		base = filepath.Dir(repoRoot)
	} else if expanded, err := homedir.Expand(base); err == nil {
		base = expanded
	}

	return filepath.Join(base, repoName+"--"+branchName)
}
```

Add `"path/filepath"` and `"github.com/mitchellh/go-homedir"` to the import block.

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./cmd/issue/... -run TestWorktreePath -v
```

Expected: all three subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/issue/start.go cmd/issue/start_test.go
git commit -m "feat(issue): add worktreePath() helper with tilde expansion"
```

---

### Task 5: `WorktreeToggle` TUI form

**Files:**
- Modify: `tui/issue.go`
- Modify: `tui/issue_test.go`

- [ ] **Step 1: Write the failing test**

Add to `tui/issue_test.go`:

```go
func TestWorktreeToggle(t *testing.T) {
	t.Parallel()

	t.Run("pre-selects false (plain branch) by default", func(t *testing.T) {
		t.Parallel()

		var useWorktree bool
		WorktreeToggle(&useWorktree)
		if useWorktree {
			t.Error("default should be false (plain branch), got true")
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./tui/... -run TestWorktreeToggle -v
```

Expected: FAIL — `WorktreeToggle undefined`.

- [ ] **Step 3: Implement `WorktreeToggle` in `tui/issue.go`**

Add after `IssueTrackerToggle`:

```go
// WorktreeToggle asks whether to create a git worktree or a plain branch.
// Pre-selected default is false (plain branch).
func WorktreeToggle(useWorktree *bool) *huh.Group {
	*useWorktree = false

	return huh.NewGroup(
		huh.NewConfirm().
			Title("Create a git worktree instead of a plain branch?").
			Value(useWorktree),
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
mise exec -- go test ./tui/... -run TestWorktreeToggle -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tui/issue.go tui/issue_test.go
git commit -m "feat(tui): add WorktreeToggle form for worktree vs branch choice"
```

---

### Task 6: Wire everything together in `cmd/issue/start.go`

**Files:**
- Modify: `cmd/issue/start.go`

This task refactors `createBranch` to use a shared `prepareBranch` helper, then adds `createWorktree` and the dispatch logic in `RunIssueStart`.

- [ ] **Step 1: Extract `prepareBranch` from `createBranch`**

Replace the existing `createBranch` function and add `prepareBranch` so `createBranch` delegates to it. Full replacement of both functions:

```go
// prepareBranch assembles the branch name and resolves the base branch.
func (i Issue) prepareBranch(
	pickedIssue *issue.Issue,
	client *git.Client,
) (branchName, base string, err error) {
	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject)
	if err != nil {
		return "", "", fmt.Errorf("assemble branch name: %w", err)
	}

	base = i.appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return "", "", fmt.Errorf("detect base branch: %w", err)
		}
	}

	return b.Name(), base, nil
}

func (i Issue) createBranch(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
) error {
	branchName, base, err := i.prepareBranch(pickedIssue, client)
	if err != nil {
		return err
	}

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create branch %q based on %q?", branchName, base), &confirmed,
	)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if err := client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject)
	if err != nil {
		return fmt.Errorf("assemble branch for persist: %w", err)
	}

	var tt *string
	if pickedIssue.TrackerType != "" {
		tt = &i.appConfig.IssueTracker.Type
	}

	if err := persist(cmd.Context(), b, pickedIssue.Subject, tt); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q)\n", branchName, base)

	if pickedIssue.TrackerType != "" {
		i.updateTrackerIssueStatus(cmd, t, pickedIssue.ID)
	}

	return nil
}
```

Note: `branch.New` is called twice in `createBranch` — once for the name display and once to build the `*branch.Branch` value for `persist`. This avoids changing `persist`'s signature.

- [ ] **Step 2: Build to verify no compilation errors**

```bash
mise exec -- go build ./...
```

Expected: exits 0.

- [ ] **Step 3: Add `createWorktree`**

Add after `createBranch` in `cmd/issue/start.go`:

```go
func (i Issue) createWorktree(
	cmd *cobra.Command,
	t tracker.Tracker,
	pickedIssue *issue.Issue,
	client *git.Client,
) error {
	branchName, base, err := i.prepareBranch(pickedIssue, client)
	if err != nil {
		return err
	}

	repoRoot, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	repoName, err := client.RepoName()
	if err != nil {
		return fmt.Errorf("resolve repo name: %w", err)
	}

	path := worktreePath(repoRoot, i.appConfig.Branch.WorktreeDir, repoName, branchName)

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create worktree %q at %q based on %q?", branchName, path, base), &confirmed,
	)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if err := client.CreateWorktree(cmd.Context(), branchName, base, path); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject)
	if err != nil {
		return fmt.Errorf("assemble branch for persist: %w", err)
	}

	var tt *string
	if pickedIssue.TrackerType != "" {
		tt = &i.appConfig.IssueTracker.Type
	}

	if err := persist(cmd.Context(), b, pickedIssue.Subject, tt); err != nil {
		fmt.Fprintf(cmd.OutOrStderr(), "warning: worktree created but store record failed: %v\n", err)
	}

	fmt.Printf("Created worktree %q at %q (based on %q)\n", branchName, path, base)
	fmt.Printf("👉 Run 'cd %s' to begin working.\n", path)

	if pickedIssue.TrackerType != "" {
		i.updateTrackerIssueStatus(cmd, t, pickedIssue.ID)
	}

	return nil
}
```

- [ ] **Step 4: Add the dispatch logic in `RunIssueStart`**

In `RunIssueStart`, replace the final `return i.createBranch(cmd, t, pickedIssue, client)` line with:

```go
	useWorktree := false
	if i.appConfig.Branch.UseWorktree == nil {
		if err := huh.NewForm(tui.WorktreeToggle(&useWorktree)).Run(); err != nil {
			return fmt.Errorf("worktree toggle: %w", err)
		}
	} else {
		useWorktree = *i.appConfig.Branch.UseWorktree
	}

	if useWorktree {
		return i.createWorktree(cmd, t, pickedIssue, client)
	}

	return i.createBranch(cmd, t, pickedIssue, client)
```

- [ ] **Step 5: Build to verify no compilation errors**

```bash
mise exec -- go build ./...
```

Expected: exits 0.

- [ ] **Step 6: Run full test suite**

```bash
mise exec -- go test ./...
```

Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add cmd/issue/start.go
git commit -m "feat(issue): add worktree creation path to issue start"
```

---

### Task 7: Update `IssueActionSelect` description

**Files:**
- Modify: `tui/issue.go`

The action menu still shows `"Start working on an issue (create branch)"`. It should reflect that a worktree is also an option.

- [ ] **Step 1: Update the label in `IssueActionSelect`**

In `tui/issue.go`, replace:

```go
huh.NewOption("Start\n"+descStyle.Render(
    "Start working on an issue (create branch)"), IssueActionNameStart),
```

with:

```go
huh.NewOption("Start\n"+descStyle.Render(
    "Start working on an issue (branch or worktree)"), IssueActionNameStart),
```

- [ ] **Step 2: Build and test**

```bash
mise exec -- go build ./... && mise exec -- go test ./...
```

Expected: exits 0, all tests PASS.

- [ ] **Step 3: Commit**

```bash
git add tui/issue.go
git commit -m "style(tui): update issue start description to mention worktree"
```
