# Worktree-Aware Close Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `git zf issue close` (and `branch merge`) work when the source branch is checked out in a linked git worktree, remove the worktree after the merge, delete the feature branch locally and on the remote, and make every command inside a worktree share the repository's real store.

**Architecture:** The shared merge engine (`cmd/mergeflow`) gets an optional second git client opened at the linked worktree. Squash and Classic keep running on the main tree; Rebase runs its source-side steps in the worktree and fast-forwards the target from the main tree. The close flow discovers the worktree live via `git worktree list --porcelain`, passes the source client to the engine, and after the commit lands offers to remove the worktree, then deletes the branch locally and remotely. The SQLite store moves to the common git dir so linked worktrees share it.

**Tech Stack:** Go 1.25 via mise, go-git v6 (read-only use), git CLI for every mutating operation, cobra, huh forms, modernc sqlite.

**Spec:** `docs/superpowers/specs/2026-09-22-worktree-aware-close-design.md`

## Global Constraints

- Run Go only through mise: `mise exec -- go test ./...`, `mise exec -- go build -o ./bin/git-zf .`
- Every test assertion or scenario sits in its own `t.Run("descriptive label", ...)`, including single-assertion tests (global user rule).
- Tests use a real on-disk repository and scripted prompters, never huh forms. Follow the existing rigs (`newCloseRig`, `newEngineRig`, `newMergeRig`, `newDiskRepo`).
- git-zf never passes `--force` to `git worktree remove`.
- Mutating git operations go through the git CLI with an explicit `-C <root>` (via `runInteractive` or `exec.CommandContext`); go-git is used for reads only. This is what makes the process independent of its current directory.
- Project rule: run GitNexus `detect_changes` before each commit. At the time of writing the index is broken ("No indexed repositories"). If it still fails, proceed with the commit and say so in the task report. Do not spend time repairing the index.
- Commit messages follow the repo's conventional style: `feat(scope): …`, `fix(scope): …`, `test(scope): …`, `docs: …`.
- Do not commit the spec file `docs/superpowers/specs/2026-09-22-worktree-aware-close-design.md` (user decision). Leave it uncommitted in the working tree; `git add` only the files each task names.

## Deviation from the spec (decided while planning)

`branch merge` keeps opening its git client at the current directory instead of the main tree. Its target is "the current branch" by definition, so the tree the user stands in is exactly the tree that has the target checked out, which is what the engine needs. It only gains worktree detection for the *source* and the remove-worktree step. The close flow does re-anchor on the main tree as the spec says.

---

## File structure

| File | Responsibility |
|---|---|
| `internal/gitdir/gitdir.go` (modify) | `Common()` resolver next to `Get()` |
| `store/store.go` (modify) | `OpenRepo` opens the store in the common dir |
| `git/worktree.go` (create) | worktree listing/parsing, `WorktreeFor`, `RemoveWorktree`, `CommonDir`, `MainTree`, `SamePath`; `CreateWorktree` moves here |
| `git/worktree_test.go` (create) | tests for the above |
| `cmd/issueflow/start.go` (modify) | the two direct store opens use `CommonDir` |
| `cmd/mergeflow/mergeflow.go`, `strategies.go` (modify) | `Params.SourceClient`, two-client run, preflight reorder |
| `cmd/mergeflow/worktree.go` (create) | `SourceTree` and `RemoveWorktreeStep` shared by close and branch merge |
| `cmd/mergeflow/mergeflow_worktree_test.go` (create) | worktree rig + per-strategy tests |
| `tui/issue.go` (modify) | `IssueRemoveWorktree` form, delete-branch title |
| `cmd/issue/close_prompter.go`, `close_prompter_test.go` (modify) | `ConfirmRemoveWorktree` |
| `cmd/issue/close.go` (modify) | main-tree client, source tree, post-merge steps, remote delete |
| `cmd/issue/close_e2e_test.go` (modify) | origin helper, worktree rig, new cases |
| `cmd/cmdutil/git.go` (modify) | `NewMainClientForCmd` |
| `cmd/branch/merge.go`, `merge_prompter.go`, `merge_prompter_test.go`, `merge_e2e_test.go` (modify) | worktree source support |
| `README.md`, `ROADMAP.md`, `CLAUDE.md` (modify) | documentation |

---

### Task 1: Common git dir resolver and shared store

**Files:**
- Modify: `internal/gitdir/gitdir.go`
- Modify: `internal/gitdir/gitdir_test.go`
- Modify: `store/store.go:386-400` (`OpenRepo`)
- Modify: `store/store_test.go`

**Interfaces:**
- Produces: `gitdir.Common() (string, error)` — absolute path of the common git dir (`git rev-parse --git-common-dir`).
- Produces: `store.OpenRepo` now opens `<common dir>/git-zf.db`.

- [ ] **Step 1: Write the failing gitdir tests**

Append to `internal/gitdir/gitdir_test.go` (the file already has `initGitRepo` and `TestGet_linkedWorktree`):

```go
func TestCommon(t *testing.T) {
	t.Run("main tree: equals Get", func(t *testing.T) {
		mainDir := t.TempDir()
		initGitRepo(t, mainDir)
		t.Chdir(mainDir)

		got, err := gitdir.Common()
		if err != nil {
			t.Fatalf("Common() error = %v", err)
		}
		want, err := gitdir.Get()
		if err != nil {
			t.Fatalf("Get() error = %v", err)
		}
		if got != want {
			t.Errorf("Common() = %q, want Get() = %q", got, want)
		}
	})

	t.Run("linked worktree: returns the main .git", func(t *testing.T) {
		mainDir := t.TempDir()
		initGitRepo(t, mainDir)

		worktreeDir := t.TempDir()
		cmd := exec.Command("git", "worktree", "add", "-b", "wt-branch", worktreeDir, "main")
		cmd.Dir = mainDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git worktree add: %v\n%s", err, out)
		}

		t.Chdir(worktreeDir)

		got, err := gitdir.Common()
		if err != nil {
			t.Fatalf("Common() error = %v", err)
		}
		gotReal, _ := filepath.EvalSymlinks(got)
		wantReal, _ := filepath.EvalSymlinks(filepath.Join(mainDir, ".git"))
		if gotReal != wantReal {
			t.Errorf("Common() = %q, want %q", gotReal, wantReal)
		}
		if strings.Contains(got, "worktrees") {
			t.Errorf("Common() = %q, must not be the per-worktree dir", got)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./internal/gitdir/... -run '^TestCommon$' -v`
Expected: compile error `undefined: gitdir.Common`.

- [ ] **Step 3: Implement `Common` by extracting a shared `revParse` helper**

Replace the body of `internal/gitdir/gitdir.go` with:

```go
package gitdir

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Get returns the path to the .git directory for the current working tree.
// It resolves gitfiles, submodules, and linked worktrees via git rev-parse,
// without importing go-git. Inside a linked worktree this is the per-worktree
// dir (.git/worktrees/<name>), which is where MERGE_HEAD and friends live.
func Get() (string, error) {
	return revParse("--git-dir")
}

// Common returns the path to the common .git directory shared by every
// worktree of the repository. In the main working tree it equals Get; inside
// a linked worktree Get returns .git/worktrees/<name> while Common returns the
// main .git. Files that must be shared across worktrees (the git-zf store)
// belong here.
func Common() (string, error) {
	return revParse("--git-common-dir")
}

func revParse(flag string) (string, error) {
	cmd := exec.Command("git", "rev-parse", flag)

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}

	d := strings.TrimSpace(string(out))
	if !filepath.IsAbs(d) {
		wd, werr := os.Getwd()
		if werr != nil {
			return "", fmt.Errorf("getwd: %w", werr)
		}

		d = filepath.Join(wd, d)
	}

	return d, nil
}
```

- [ ] **Step 4: Run the gitdir tests**

Run: `mise exec -- go test ./internal/gitdir/... -v`
Expected: PASS, including the pre-existing `TestGet_linkedWorktree`.

- [ ] **Step 5: Write the failing store test**

Append to `store/store_test.go`:

```go
// TestOpenRepo_linkedWorktreeSharesMainStore guards the store location: a row
// written from the main tree must be visible when OpenRepo runs inside a
// linked worktree (the store lives in the common git dir, not the per-worktree
// dir).
func TestOpenRepo_linkedWorktreeSharesMainStore(t *testing.T) {
	mainDir := t.TempDir()
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(mainDir, "init", "-q", "-b", "main")
	runGit(mainDir, "config", "user.name", "Test")
	runGit(mainDir, "config", "user.email", "test@example.com")
	runGit(mainDir, "commit", "-q", "--allow-empty", "-m", "init")

	mainStore, err := Open(t.Context(), filepath.Join(mainDir, ".git"))
	if err != nil {
		t.Fatalf("Open main store: %v", err)
	}
	if err := mainStore.InsertIssueWithBranch(t.Context(),
		&Issue{IDSlug: "ABC-7", Title: "Shared", StatusID: StatusIDInProgress},
		&Branch{Name: "ABC-7@feat@shared", Type: "feat", StatusID: StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = mainStore.Close()

	wtDir := filepath.Join(t.TempDir(), "repo--wt")
	runGit(mainDir, "worktree", "add", "-q", "-b", "wt-branch", wtDir, "main")
	t.Chdir(wtDir)

	s, err := OpenRepo(t.Context())
	if err != nil {
		t.Fatalf("OpenRepo inside worktree: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	t.Run("row seeded from the main tree is visible", func(t *testing.T) {
		rows, err := s.ListBranches(t.Context(), BranchStatusInProgress)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 1 || rows[0].IssueSlug != "ABC-7" {
			t.Fatalf("ListBranches = %+v, want the ABC-7 row", rows)
		}
	})

	t.Run("no database was created in the per-worktree git dir", func(t *testing.T) {
		perWorktree := filepath.Join(mainDir, ".git", "worktrees", "repo--wt", "git-zf.db")
		if _, err := os.Stat(perWorktree); err == nil {
			t.Fatalf("unexpected store at %s", perWorktree)
		}
	})
}
```

Add `"os"`, `"os/exec"`, and `"path/filepath"` to the test file's imports if missing.

- [ ] **Step 6: Run the store test to verify it fails**

Run: `mise exec -- go test ./store/... -run '^TestOpenRepo_linkedWorktreeSharesMainStore$' -v`
Expected: FAIL in "row seeded from the main tree is visible" (empty list) and in the per-worktree assertion.

- [ ] **Step 7: Point `OpenRepo` at the common dir**

In `store/store.go` replace the `OpenRepo` doc comment and body:

```go
// OpenRepo opens the local store inside the current git repository's common
// .git directory. Resolves it via gitdir.Common() so regular repos, submodules
// (where <worktree>/.git is a gitlink file), and linked worktrees all share one
// store: inside a linked worktree the per-worktree dir (.git/worktrees/<name>)
// would otherwise hold a separate, empty database.
func OpenRepo(ctx context.Context) (*Store, error) {
	d, err := gitdir.Common()
	if err != nil {
		return nil, fmt.Errorf("not a git repository: %w", err)
	}

	s, err := Open(ctx, d)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}

	return s, nil
}
```

- [ ] **Step 8: Run the store tests**

Run: `mise exec -- go test ./store/... ./internal/gitdir/... -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/gitdir/gitdir.go internal/gitdir/gitdir_test.go store/store.go store/store_test.go
git commit -m "fix(store): open the store in the common git dir so linked worktrees share it"
```

---

### Task 2: Worktree helpers on `git.Client`

**Files:**
- Create: `git/worktree.go`
- Create: `git/worktree_test.go`
- Modify: `git/git.go:833-846` (move `CreateWorktree` into `git/worktree.go`, unchanged)

**Interfaces:**
- Produces:
  ```go
  type Worktree struct { Path, Branch string; Main, Prunable bool }
  func parseWorktreeList(out string) []Worktree
  func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error)
  func (c *Client) WorktreeFor(ctx context.Context, branch string) (*Worktree, error) // linked entries only; may be Prunable
  func (c *Client) RemoveWorktree(ctx context.Context, path string) error           // never --force
  func (c *Client) CommonDir() (string, error)
  func (c *Client) MainTree() (*Client, error)                                        // c itself when already on the main tree
  func SamePath(a, b string) bool
  ```

- [ ] **Step 1: Write the failing parser test**

Create `git/worktree_test.go`:

```go
package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseWorktreeList(t *testing.T) {
	t.Parallel()

	const porcelain = "worktree /home/u/repo\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"branch refs/heads/main\n" +
		"\n" +
		"worktree /home/u/repo--feat\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"branch refs/heads/ABC-1@feat@thing\n" +
		"\n" +
		"worktree /home/u/repo--detached\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"detached\n" +
		"\n" +
		"worktree /home/u/repo--gone\n" +
		"HEAD 0bb8041ce5d8a70917f266ff86f894dd9dbcb139\n" +
		"branch refs/heads/old\n" +
		"prunable gitdir file points to non-existent location\n" +
		"\n"

	got := parseWorktreeList(porcelain)

	t.Run("four entries", func(t *testing.T) {
		if len(got) != 4 {
			t.Fatalf("len = %d, want 4: %+v", len(got), got)
		}
	})
	t.Run("first entry is the main tree on main", func(t *testing.T) {
		if !got[0].Main || got[0].Path != "/home/u/repo" || got[0].Branch != "main" {
			t.Fatalf("entry 0 = %+v", got[0])
		}
	})
	t.Run("linked entry carries the short branch name", func(t *testing.T) {
		if got[1].Main || got[1].Branch != "ABC-1@feat@thing" || got[1].Path != "/home/u/repo--feat" {
			t.Fatalf("entry 1 = %+v", got[1])
		}
	})
	t.Run("detached entry has an empty branch", func(t *testing.T) {
		if got[2].Branch != "" {
			t.Fatalf("entry 2 = %+v", got[2])
		}
	})
	t.Run("prunable flag is set", func(t *testing.T) {
		if !got[3].Prunable || got[3].Branch != "old" {
			t.Fatalf("entry 3 = %+v", got[3])
		}
	})
	t.Run("empty input yields no entries", func(t *testing.T) {
		if n := len(parseWorktreeList("")); n != 0 {
			t.Fatalf("len = %d, want 0", n)
		}
	})
}
```

- [ ] **Step 2: Run the parser test to verify it fails**

Run: `mise exec -- go test ./git/... -run '^TestParseWorktreeList$' -v`
Expected: compile error `undefined: parseWorktreeList`.

- [ ] **Step 3: Create `git/worktree.go` with the parser, listing, and helpers**

Move `CreateWorktree` (and its doc comment) out of `git/git.go` into this new file verbatim, then add:

```go
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
	Path     string // absolute path of the working tree
	Branch   string // short branch name; "" when HEAD is detached
	Main     bool   // the main working tree (always the first entry)
	Prunable bool   // git flagged the entry prunable (its directory is gone)
}

// parseWorktreeList parses `git worktree list --porcelain` output. Entries are
// blank-line separated; the first entry is the main working tree.
func parseWorktreeList(out string) []Worktree {
	var (
		list []Worktree
		cur  *Worktree
	)

	flush := func() {
		if cur != nil {
			list = append(list, *cur)
			cur = nil
		}
	}

	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == "":
			flush()
		case strings.HasPrefix(line, "worktree "):
			flush()
			cur = &Worktree{Path: strings.TrimPrefix(line, "worktree "), Main: len(list) == 0}
		case cur == nil:
			// attribute line before any "worktree" header: ignore
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		case strings.HasPrefix(line, "prunable"):
			cur.Prunable = true
		}
	}
	flush()

	return list
}

// Worktrees lists every working tree of the repository (main first).
func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "list", "--porcelain")
	cmd.Env = append(os.Environ(), "LC_ALL=C")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}

	return parseWorktreeList(string(out)), nil
}

// WorktreeFor returns the linked worktree that has branch checked out, or nil
// when the branch is not checked out in any linked worktree. The main working
// tree is never returned. A returned entry may be Prunable (its directory is
// gone but git still records it); callers decide how to treat that.
func (c *Client) WorktreeFor(ctx context.Context, branch string) (*Worktree, error) {
	list, err := c.Worktrees(ctx)
	if err != nil {
		return nil, err
	}

	for i := range list {
		if !list[i].Main && list[i].Branch == branch {
			return &list[i], nil
		}
	}

	return nil, nil //nolint:nilnil // nil,nil is the documented "not in a worktree" answer
}

// RemoveWorktree runs `git worktree remove <path>` without --force, so git's
// own safety checks (modified or untracked files) apply and the error carries
// git's reason.
func (c *Client) RemoveWorktree(ctx context.Context, path string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "worktree", "remove", path)
	cmd.Env = append(os.Environ(), "LC_ALL=C")

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("worktree remove %s: %w: %s", path, err, strings.TrimSpace(string(out)))
	}

	return nil
}

// CommonDir returns the absolute path of the common .git directory shared by
// every worktree. In the main tree it equals GitDir; in a linked worktree
// GitDir is .git/worktrees/<name> while CommonDir is the main .git.
func (c *Client) CommonDir() (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), "git", "-C", root, "rev-parse", "--git-common-dir")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-common-dir: %w", err)
	}

	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}

	return dir, nil
}

// MainTree returns a client anchored on the main working tree. When c already
// is the main tree it returns c. Otherwise it opens a new client at the main
// tree path and carries over the pinned remote. Close and other target-side
// flows use it so checkouts of the base branch and branch deletions run in the
// tree that holds the base, even when the command was typed inside a linked
// worktree.
func (c *Client) MainTree() (*Client, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	list, err := c.Worktrees(context.Background())
	if err != nil {
		return nil, err
	}

	for _, w := range list {
		if !w.Main {
			continue
		}
		if SamePath(w.Path, root) {
			return c, nil
		}

		m, err := NewClientAt(c.io, w.Path)
		if err != nil {
			return nil, err
		}
		m.remote, m.remoteResolved = c.remote, c.remoteResolved

		return m, nil
	}

	return c, nil
}

// SamePath reports whether a and b name the same directory once symlinks are
// resolved (t.TempDir on macOS and go-git roots may differ only by symlinks).
func SamePath(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = filepath.Clean(a)
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = filepath.Clean(b)
	}

	return ra == rb
}
```

- [ ] **Step 4: Run the parser test**

Run: `mise exec -- go test ./git/... -run '^TestParseWorktreeList$|^TestCreateWorktree$' -v`
Expected: PASS (both; `CreateWorktree` moved without behaviour change).

- [ ] **Step 5: Write the failing real-repo tests**

Append to `git/worktree_test.go` (`newDiskRepo` from `git/merge_test.go` gives a client on a `main` branch with one commit; `writeFile` and `runGitInDir` exist in `git/git_test.go`):

```go
// newRepoWithWorktree returns the main-tree client, the repo dir and the path
// of a linked worktree holding branch "feat/x" cut from main.
func newRepoWithWorktree(t *testing.T) (*Client, string, string) {
	t.Helper()

	c, dir := newDiskRepo(t)
	wt := filepath.Join(t.TempDir(), "repo--feat-x")
	if err := c.CreateWorktree(t.Context(), "feat/x", "main", wt); err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	return c, dir, wt
}

func TestWorktreeFor(t *testing.T) {
	t.Parallel()

	c, _, wt := newRepoWithWorktree(t)

	t.Run("finds the linked worktree holding the branch", func(t *testing.T) {
		got, err := c.WorktreeFor(t.Context(), "feat/x")
		if err != nil {
			t.Fatalf("WorktreeFor: %v", err)
		}
		if got == nil || !SamePath(got.Path, wt) || got.Main || got.Prunable {
			t.Fatalf("WorktreeFor = %+v, want linked entry at %s", got, wt)
		}
	})
	t.Run("returns nil for the branch checked out in the main tree", func(t *testing.T) {
		got, err := c.WorktreeFor(t.Context(), "main")
		if err != nil {
			t.Fatalf("WorktreeFor: %v", err)
		}
		if got != nil {
			t.Fatalf("WorktreeFor(main) = %+v, want nil", got)
		}
	})
	t.Run("returns nil for an unknown branch", func(t *testing.T) {
		got, err := c.WorktreeFor(t.Context(), "nope")
		if err != nil {
			t.Fatalf("WorktreeFor: %v", err)
		}
		if got != nil {
			t.Fatalf("WorktreeFor(nope) = %+v, want nil", got)
		}
	})
	t.Run("reports a prunable entry once the directory is gone", func(t *testing.T) {
		if err := os.RemoveAll(wt); err != nil {
			t.Fatalf("remove worktree dir: %v", err)
		}
		got, err := c.WorktreeFor(t.Context(), "feat/x")
		if err != nil {
			t.Fatalf("WorktreeFor: %v", err)
		}
		if got == nil || !got.Prunable {
			t.Fatalf("WorktreeFor after rm = %+v, want Prunable", got)
		}
	})
}

func TestCommonDir(t *testing.T) {
	t.Parallel()

	c, dir, wt := newRepoWithWorktree(t)
	want := filepath.Join(dir, ".git")

	t.Run("main tree", func(t *testing.T) {
		got, err := c.CommonDir()
		if err != nil {
			t.Fatalf("CommonDir: %v", err)
		}
		if !SamePath(got, want) {
			t.Fatalf("CommonDir = %q, want %q", got, want)
		}
	})
	t.Run("linked worktree resolves to the main .git", func(t *testing.T) {
		src, err := NewClientAt(nil, wt)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		got, err := src.CommonDir()
		if err != nil {
			t.Fatalf("CommonDir: %v", err)
		}
		if !SamePath(got, want) {
			t.Fatalf("CommonDir = %q, want %q", got, want)
		}
	})
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()

	t.Run("removes a clean worktree", func(t *testing.T) {
		t.Parallel()

		c, _, wt := newRepoWithWorktree(t)
		if err := c.RemoveWorktree(t.Context(), wt); err != nil {
			t.Fatalf("RemoveWorktree: %v", err)
		}
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Fatalf("worktree dir still present: %v", err)
		}
		got, err := c.WorktreeFor(t.Context(), "feat/x")
		if err != nil {
			t.Fatalf("WorktreeFor: %v", err)
		}
		if got != nil {
			t.Fatalf("entry still listed: %+v", got)
		}
	})
	t.Run("refuses a worktree with an untracked file", func(t *testing.T) {
		t.Parallel()

		c, _, wt := newRepoWithWorktree(t)
		writeFile(t, wt, "scratch.txt", "keep me\n")
		err := c.RemoveWorktree(t.Context(), wt)
		if err == nil {
			t.Fatal("expected git to refuse, got nil")
		}
		if _, statErr := os.Stat(filepath.Join(wt, "scratch.txt")); statErr != nil {
			t.Fatalf("untracked file was lost: %v", statErr)
		}
	})
}

func TestMainTree(t *testing.T) {
	t.Parallel()

	c, dir, wt := newRepoWithWorktree(t)

	t.Run("main-tree client returns itself", func(t *testing.T) {
		m, err := c.MainTree()
		if err != nil {
			t.Fatalf("MainTree: %v", err)
		}
		if m != c {
			t.Fatal("expected the same client instance")
		}
	})
	t.Run("worktree client re-anchors on the main tree", func(t *testing.T) {
		src, err := NewClientAt(nil, wt)
		if err != nil {
			t.Fatalf("NewClientAt: %v", err)
		}
		m, err := src.MainTree()
		if err != nil {
			t.Fatalf("MainTree: %v", err)
		}
		root, err := m.WorkingTreeRoot()
		if err != nil {
			t.Fatalf("WorkingTreeRoot: %v", err)
		}
		if !SamePath(root, dir) {
			t.Fatalf("root = %q, want %q", root, dir)
		}
		cur, err := m.CurrentBranch()
		if err != nil {
			t.Fatalf("CurrentBranch: %v", err)
		}
		if cur != "main" {
			t.Fatalf("current branch = %q, want main", cur)
		}
	})
}
```

- [ ] **Step 6: Run the git package tests**

Run: `mise exec -- go test ./git/... -run 'Worktree|CommonDir|MainTree' -v`
Expected: PASS. If `TestWorktreeFor` "prunable" fails because git does not flag the entry immediately, keep the assertion: git marks an entry prunable as soon as its directory is missing (verified on git 2.39+).

- [ ] **Step 7: Run the whole git package and build**

Run: `mise exec -- go test ./git/... && mise exec -- go build ./...`
Expected: PASS, build OK.

- [ ] **Step 8: Commit**

```bash
git add git/worktree.go git/worktree_test.go git/git.go
git commit -m "feat(git): worktree discovery, removal, common dir and main-tree client"
```

---

### Task 3: Start flow opens the store in the common dir

**Files:**
- Modify: `cmd/issueflow/start.go:250-258` and `:397-400`
- Modify: `cmd/issueflow/start_test.go` (or the file holding `TestWorktreePath`)

**Interfaces:**
- Consumes: `git.Client.CommonDir()` from Task 2.

- [ ] **Step 1: Write the failing test**

Append to the issueflow test file that already holds `TestWorktreePath` (find it with `grep -ln TestWorktreePath cmd/issueflow/*_test.go`):

```go
// TestResolveParentSlug_fromLinkedWorktree guards the store location used by
// the parent lookup: a client opened inside a linked worktree must read the
// repo's shared store, not an empty per-worktree one. The base branch name is
// deliberately not a git-zf name so the branch.Parse fallback cannot mask a
// wrong store path.
func TestResolveParentSlug_fromLinkedWorktree(t *testing.T) {
	mainDir := t.TempDir()
	runGit := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit(mainDir, "init", "-q", "-b", "main")
	runGit(mainDir, "config", "user.name", "Test")
	runGit(mainDir, "config", "user.email", "test@example.com")
	runGit(mainDir, "commit", "-q", "--allow-empty", "-m", "init")
	runGit(mainDir, "branch", "integration", "main")

	mainStore, err := store.Open(t.Context(), filepath.Join(mainDir, ".git"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := mainStore.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "PARENT-1", Title: "Parent", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "integration", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_ = mainStore.Close()

	wtDir := filepath.Join(t.TempDir(), "repo--wt")
	runGit(mainDir, "worktree", "add", "-q", "-b", "wt-branch", wtDir, "main")

	wtClient, err := git.NewClientAt(nil, wtDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	t.Run("parent slug resolved through the shared store", func(t *testing.T) {
		if got := resolveParentSlug(t.Context(), wtClient, "integration"); got != "PARENT-1" {
			t.Fatalf("resolveParentSlug = %q, want PARENT-1", got)
		}
	})
}
```

The test needs `"os/exec"`, `"path/filepath"`, `"github.com/piprim/git-zf/git"`, and `"github.com/piprim/git-zf/store"` in that file's imports; add whichever are missing.

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issueflow/... -run '^TestResolveParentSlug_fromLinkedWorktree$' -v`
Expected: FAIL with `resolveParentSlug = "", want PARENT-1`.

- [ ] **Step 3: Switch both store opens to `CommonDir`**

In `cmd/issueflow/start.go`, in `createFlow`:

```go
	var parentStore *store.Store
	if deps.Flags.ParentIssueSlug != "" {
		commonDir, gdErr := deps.Client.CommonDir()
		if gdErr != nil {
			return fmt.Errorf("resolve common git dir for parent store: %w", gdErr)
		}
		var openErr error
		parentStore, openErr = store.Open(ctx, commonDir)
		if openErr != nil {
			return fmt.Errorf("open store for parent lookup: %w", openErr)
		}
		defer func() { _ = parentStore.Close() }()
	}
```

and in `resolveParentSlug`:

```go
	if commonDir, gdErr := c.CommonDir(); gdErr == nil {
		if s, openErr := store.Open(ctx, commonDir); openErr == nil {
```

Update the comment above the first block: "Use the client's common git dir so this works in tests (temp dirs), production (CWD is the repo) and inside linked worktrees (which share the store)."

- [ ] **Step 4: Run the issueflow and issue suites**

Run: `mise exec -- go test ./cmd/issueflow/... ./cmd/issue/... -v -run 'TestResolveParentSlug|^TestRunIssueStart_'`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/issueflow/start.go cmd/issueflow/*_test.go
git commit -m "fix(issue): start flow reads the shared store from inside a linked worktree"
```

---

### Task 4: Merge engine runs with a source client

**Files:**
- Modify: `cmd/mergeflow/mergeflow.go:21-30` (`Params`), `:52-60` (`run`), `:65-70` (`Run` setup)
- Modify: `cmd/mergeflow/strategies.go` (all four functions)
- Create: `cmd/mergeflow/mergeflow_worktree_test.go`

**Interfaces:**
- Produces: `mergeflow.Params.SourceClient *git.Client` (nil = single-tree mode).
- Internal: `rebasePreflight(ctx, tree *git.Client)`, `composeAndCommit(ctx, tree *git.Client, prefill, strategy)`.

- [ ] **Step 1: Write the failing worktree tests**

Create `cmd/mergeflow/mergeflow_worktree_test.go` (`initRepo`, `gitRun`, `writeFile`, `scriptedMergePrompter`, `plainPrefill` already exist in `mergeflow_test.go`):

```go
package mergeflow

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
)

// worktreeRig: master in the main tree, "feature" one commit ahead checked out
// in a linked worktree, and a second client opened on that worktree.
type worktreeRig struct {
	*engineRig
	wtDir string
	src   *git.Client
}

func newWorktreeRig(t *testing.T) *worktreeRig {
	t.Helper()

	rig := initRepo(t)
	wtDir := filepath.Join(t.TempDir(), "repo--feature")
	gitRun(t, rig.dir, "worktree", "add", "-q", "-b", "feature", wtDir, "master")
	writeFile(t, wtDir, "base.txt", "base\nfeature\n")
	gitRun(t, wtDir, "add", "base.txt")
	gitRun(t, wtDir, "commit", "-m", "feat: feature work")

	src, err := git.NewClientAt(rig.client.IO(), wtDir)
	if err != nil {
		t.Fatalf("NewClientAt(worktree): %v", err)
	}

	return &worktreeRig{engineRig: rig, wtDir: wtDir, src: src}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func hasMergeHead(t *testing.T, dir string) bool {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "rev-parse", "-q", "--verify", "MERGE_HEAD")
	cmd.Dir = dir

	return cmd.Run() == nil
}

func (r *worktreeRig) assertWorktreeIntact(t *testing.T) {
	t.Helper()

	t.Run("worktree directory still exists", func(t *testing.T) {
		if _, err := os.Stat(r.wtDir); err != nil {
			t.Fatalf("worktree gone: %v", err)
		}
	})
	t.Run("worktree still on feature", func(t *testing.T) {
		if got := gitOut(t, r.wtDir, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
			t.Fatalf("worktree HEAD = %q", got)
		}
	})
	t.Run("main tree still on master", func(t *testing.T) {
		if got := gitOut(t, r.dir, "rev-parse", "--abbrev-ref", "HEAD"); got != "master" {
			t.Fatalf("main HEAD = %q", got)
		}
	})
	t.Run("no merge in progress in either tree", func(t *testing.T) {
		if hasMergeHead(t, r.dir) || hasMergeHead(t, r.wtDir) {
			t.Fatal("MERGE_HEAD left behind")
		}
	})
}

func TestRun_Worktree_Squash(t *testing.T) {
	rig := newWorktreeRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategySquash, Confirm: true,
		Message: []byte("chore: squash feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("strategy reported", func(t *testing.T) {
		if res.Strategy != commit.MergeStrategySquash {
			t.Fatalf("Strategy = %q", res.Strategy)
		}
	})
	t.Run("master carries the squash commit", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: squash feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_Classic(t *testing.T) {
	rig := newWorktreeRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyClassic, Confirm: true,
		Message: []byte("chore: merge feature into master\n"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("master carries a merge commit with two parents", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: merge feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
		if parents := gitOut(t, rig.dir, "log", "-1", "--format=%P", "master"); len(strings.Fields(parents)) != 2 {
			t.Fatalf("parents = %q, want two", parents)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_Rebase(t *testing.T) {
	rig := newWorktreeRig(t)
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		Message: []byte("chore: rebase feature into master\n"),
	}

	res, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	})
	t.Run("not fast-forward-deferred", func(t *testing.T) {
		if res.FastForwardDeferred {
			t.Fatal("unexpected FastForwardDeferred")
		}
	})
	t.Run("master fast-forwarded to the commit made in the worktree", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: rebase feature into master" {
			t.Fatalf("master HEAD subject = %q", got)
		}
		if a, b := gitOut(t, rig.dir, "rev-parse", "master"), gitOut(t, rig.wtDir, "rev-parse", "feature"); a != b {
			t.Fatalf("master %s != feature %s", a, b)
		}
	})
	rig.assertWorktreeIntact(t)
}

func TestRun_Worktree_RebaseAbortRestoresWorktree(t *testing.T) {
	rig := newWorktreeRig(t)
	origTip := gitOut(t, rig.wtDir, "rev-parse", "feature")
	prompter := &scriptedMergePrompter{
		Strategy: commit.MergeStrategyRebase, Confirm: true,
		MessageErr: errors.New("compose cancelled"),
	}

	_, err := Run(t.Context(), rig.client,
		Params{Source: "feature", Target: "master", SourceClient: rig.src}, prompter, plainPrefill)

	t.Run("compose error propagates", func(t *testing.T) {
		if err == nil {
			t.Fatal("expected the compose error")
		}
	})
	t.Run("feature restored to its original tip", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "rev-parse", "feature"); got != origTip {
			t.Fatalf("feature = %s, want %s", got, origTip)
		}
	})
	t.Run("worktree is clean", func(t *testing.T) {
		if got := gitOut(t, rig.wtDir, "status", "--porcelain"); got != "" {
			t.Fatalf("worktree dirty:\n%s", got)
		}
	})
	t.Run("master untouched", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: init" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	rig.assertWorktreeIntact(t)
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/mergeflow/... -run '^TestRun_Worktree' -v`
Expected: compile error `unknown field SourceClient in struct literal`.

- [ ] **Step 3: Add `SourceClient` to `Params` and `src` to `run`**

In `cmd/mergeflow/mergeflow.go`:

```go
// Params identify WHAT to merge. The engine advances Target; Source is merged in.
type Params struct {
	Source string
	Target string

	// SourceMaterialized widens abort-rollback: when true, staged residue is
	// discarded on abort (the source branch is disposable / reproducible from
	// origin). close sets this for ref-derived picks; branch merge sets it when
	// it materialized a remote-only source.
	SourceMaterialized bool

	// SourceClient is a client opened at the linked worktree that has Source
	// checked out (see git.Client.WorktreeFor). When nil the engine runs
	// single-tree on the client passed to Run. When set, Rebase performs its
	// source-side steps (merge, soft reset, commit) on that tree, because git
	// refuses to check out a branch that another worktree holds.
	SourceClient *git.Client
}
```

```go
type run struct {
	client       *git.Client // main tree: every target-side operation
	src          *git.Client // source-side operations; == client in single-tree mode
	source       string
	target       string
	materialized bool
	prompter     Prompter
	prefill      PrefillFunc
}
```

In `Run`:

```go
	src := p.SourceClient
	if src == nil {
		src = client
	}
	r := &run{
		client: client, src: src, source: p.Source, target: p.Target,
		materialized: p.SourceMaterialized, prompter: prompter, prefill: prefill,
	}
```

- [ ] **Step 4: Reorder the preflight and route each strategy to the right client**

In `cmd/mergeflow/strategies.go`:

`squash`: change the final line to `return r.composeAndCommit(ctx, r.client, prefill, commit.MergeStrategySquash)`.

`rebase`: replace the function body's client usage so it reads:

```go
func (r *run) rebase(ctx context.Context) (err error) {
	plan, err := r.rebasePreflight(ctx, r.src)
	if err != nil {
		return err
	}

	// MergeRebase checks out the source itself; on a worktree client that is
	// a no-op because the worktree already has it checked out.
	if err := r.src.MergeRebase(ctx, r.source, r.target); err != nil {
		return fmt.Errorf("merge rebase: %w", err)
	}

	defer func() {
		if err == nil || errors.Is(err, errFastForwardDeferred) {
			return
		}

		if rbErr := r.src.ResetHard(ctx, plan.featureOrigSHA.String()); rbErr != nil {
			err = fmt.Errorf("rollback after %w failed: %v", err, rbErr)

			return
		}

		fmt.Fprintf(r.client.IO().Err,
			"Rolled back: feature branch %q restored to %s\n",
			r.source, plan.featureOrigSHA.String()[:7])
	}()

	baseRef := "refs/remotes/" + plan.remoteBase
	if plan.remoteName == "" {
		baseRef = "refs/heads/" + r.target
	}
	baseOriginSHA, err := r.client.ResolveRef(baseRef)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", baseRef, err)
	}

	prefill := r.prefill(commit.MergeStrategyRebase, plan.featureOrigSHA, baseOriginSHA)
	if err := r.composeAndCommit(ctx, r.src, prefill, commit.MergeStrategyRebase); err != nil {
		return err
	}

	if ffErr := r.client.FastForwardOnly(ctx, r.source, r.target); ffErr != nil {
		fmt.Fprintf(r.client.IO().Err,
			"Commit created on %q but local %s has diverged from %s.\n"+
				"Run `git pull --ff-only` on %s, then `git merge --ff-only %s` to land it.\n",
			r.source, r.target, plan.remoteBase,
			r.target, r.source)

		return errFastForwardDeferred
	}

	return nil
}
```

`classic`: first line becomes `plan, err := r.rebasePreflight(ctx, r.client)`; last line becomes `return r.composeAndCommit(ctx, r.client, prefill, commit.MergeStrategyClassic)`. Everything else stays on `r.client`.

`rebasePreflight`: new signature and body:

```go
// rebasePreflight runs the read-only checks that precede a Rebase or Classic
// merge: dirty check on tree (the working tree the strategy is about to
// modify), resolve the source tip by ref, remote, fetch, ancestor check, and
// merge dry-run. It never checks out anything: the source may live in a
// linked worktree that the main tree cannot check out.
func (r *run) rebasePreflight(ctx context.Context, tree *git.Client) (rebasePlan, error) {
	dirty, err := tree.IsDirty(ctx)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("dirty check: %w", err)
	}

	if dirty {
		return rebasePlan{},
			errors.New("working tree has uncommitted modifications — commit or stash before merging")
	}

	featureOrigSHA, err := r.client.ResolveRef("refs/heads/" + r.source)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("resolve %s: %w", r.source, err)
	}

	remoteName, err := r.client.Remote()
	if err != nil {
		return rebasePlan{}, fmt.Errorf("resolve remote: %w", err)
	}

	if err := r.client.Fetch(ctx); err != nil {
		return rebasePlan{}, fmt.Errorf("fetch: %w", err)
	}

	remoteBase := r.target
	if remoteName != "" {
		remoteBase = remoteName + "/" + r.target
	}

	integrated, err := r.client.IsAncestor(ctx, r.source, remoteBase)
	if err != nil {
		return rebasePlan{}, fmt.Errorf("ancestor check: %w", err)
	}

	if integrated {
		return rebasePlan{}, fmt.Errorf("%q has no commits ahead of %s",
			r.source, remoteBase)
	}

	if err := r.mergeDryRun(ctx, remoteBase); err != nil {
		return rebasePlan{}, err
	}

	return rebasePlan{
		featureOrigSHA: featureOrigSHA,
		remoteName:     remoteName,
		remoteBase:     remoteBase,
	}, nil
}
```

`composeAndCommit`:

```go
// composeAndCommit drives the commit-message form with the caller-supplied
// prefill and commits the staged merge on tree (the client whose working tree
// holds the staged result: src for Rebase, main for Squash/Classic).
func (r *run) composeAndCommit(ctx context.Context, tree *git.Client, prefill map[string]any, strategy commit.MergeStrategy) error {
	msg, opts, err := r.prompter.ComposeMessage(ctx, prefill)
	if err != nil {
		return err //nolint:wrapcheck // prompter already wraps
	}

	if err := tree.Commit(ctx, msg, convert.CommitOptionsFromTUI(opts)); err != nil {
		return fmt.Errorf("commit %s: %w", strategy, err)
	}

	return nil
}
```

Add `"github.com/piprim/git-zf/git"` to the imports of `strategies.go`.

- [ ] **Step 5: Run the whole mergeflow package**

Run: `mise exec -- go test ./cmd/mergeflow/... -v`
Expected: PASS for the four new worktree tests and every pre-existing single-tree test (the guard for the preflight reorder).

- [ ] **Step 6: Run the two suites that drive the engine**

Run: `mise exec -- go test ./cmd/issue/... -run '^TestClose_' && mise exec -- go test ./cmd/branch/... -run '^TestRunMerge_'`
Expected: PASS, no changes needed there yet.

- [ ] **Step 7: Commit**

```bash
git add cmd/mergeflow/mergeflow.go cmd/mergeflow/strategies.go cmd/mergeflow/mergeflow_worktree_test.go
git commit -m "feat(mergeflow): run source-side steps on a worktree client"
```

---

### Task 5: Shared source-tree and remove-worktree steps

**Files:**
- Create: `cmd/mergeflow/worktree.go`
- Create: `cmd/mergeflow/worktree_test.go`

**Interfaces:**
- Produces:
  ```go
  func SourceTree(ctx context.Context, main *git.Client, branch string) (src *git.Client, wt *git.Worktree, err error)
  type ConfirmRemoveFunc func(ctx context.Context, path string) (bool, error)
  func RemoveWorktreeStep(ctx context.Context, main *git.Client, wt *git.Worktree, invokedFrom string, confirm ConfirmRemoveFunc) (removed bool, err error)
  ```
- Consumes: `git.Client.WorktreeFor`, `RemoveWorktree`, `SamePath` (Task 2).

- [ ] **Step 1: Write the failing tests**

Create `cmd/mergeflow/worktree_test.go`:

```go
package mergeflow

import (
	"context"
	"os"
	"strings"
	"testing"
)

func accept(context.Context, string) (bool, error)  { return true, nil }
func decline(context.Context, string) (bool, error) { return false, nil }

func TestSourceTree(t *testing.T) {
	t.Run("branch in a linked worktree yields a client on it", func(t *testing.T) {
		rig := newWorktreeRig(t)
		src, wt, err := SourceTree(t.Context(), rig.client, "feature")
		if err != nil {
			t.Fatalf("SourceTree: %v", err)
		}
		if src == nil || wt == nil {
			t.Fatalf("src=%v wt=%v, want both set", src, wt)
		}
		root, _ := src.WorkingTreeRoot()
		if got := gitOut(t, root, "rev-parse", "--abbrev-ref", "HEAD"); got != "feature" {
			t.Fatalf("source client HEAD = %q", got)
		}
	})
	t.Run("branch in the main tree yields nil", func(t *testing.T) {
		rig := newWorktreeRig(t)
		src, wt, err := SourceTree(t.Context(), rig.client, "master")
		if err != nil || src != nil || wt != nil {
			t.Fatalf("SourceTree = %v %v %v, want nil nil nil", src, wt, err)
		}
	})
	t.Run("prunable entry yields nil and a prune hint", func(t *testing.T) {
		rig := newWorktreeRig(t)
		if err := os.RemoveAll(rig.wtDir); err != nil {
			t.Fatalf("rm: %v", err)
		}
		stderr := rig.client.IO().Err.(interface{ String() string })
		src, wt, err := SourceTree(t.Context(), rig.client, "feature")
		if err != nil || src != nil || wt != nil {
			t.Fatalf("SourceTree = %v %v %v, want nil nil nil", src, wt, err)
		}
		if !strings.Contains(stderr.String(), "git worktree prune") {
			t.Fatalf("stderr = %q, want prune hint", stderr.String())
		}
	})
}

func TestRemoveWorktreeStep(t *testing.T) {
	t.Run("accepted: worktree removed", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, "", accept)
		if err != nil || !removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if _, statErr := os.Stat(rig.wtDir); !os.IsNotExist(statErr) {
			t.Fatalf("worktree dir still present: %v", statErr)
		}
	})
	t.Run("declined: worktree kept and reported", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, "", decline)
		if err != nil || removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if _, statErr := os.Stat(rig.wtDir); statErr != nil {
			t.Fatalf("worktree dir gone: %v", statErr)
		}
		if !strings.Contains(rig.stdout.String(), "kept") {
			t.Fatalf("stdout = %q, want 'kept'", rig.stdout.String())
		}
	})
	t.Run("untracked file: git refuses, force hint printed, no error", func(t *testing.T) {
		rig := newWorktreeRig(t)
		writeFile(t, rig.wtDir, "scratch.txt", "keep\n")
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		stderr := rig.client.IO().Err.(interface{ String() string })
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, "", accept)
		if err != nil || removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if !strings.Contains(stderr.String(), "git worktree remove --force") {
			t.Fatalf("stderr = %q, want --force hint", stderr.String())
		}
	})
	t.Run("invoked from inside the removed worktree: cd hint printed", func(t *testing.T) {
		rig := newWorktreeRig(t)
		_, wt, _ := SourceTree(t.Context(), rig.client, "feature")
		removed, err := RemoveWorktreeStep(t.Context(), rig.client, wt, rig.wtDir, accept)
		if err != nil || !removed {
			t.Fatalf("removed=%v err=%v", removed, err)
		}
		if !strings.Contains(rig.stdout.String(), "cd "+rig.dir) {
			t.Fatalf("stdout = %q, want cd hint to %s", rig.stdout.String(), rig.dir)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/mergeflow/... -run '^TestSourceTree$|^TestRemoveWorktreeStep$' -v`
Expected: compile error `undefined: SourceTree`.

- [ ] **Step 3: Implement `cmd/mergeflow/worktree.go`**

```go
package mergeflow

import (
	"context"
	"fmt"

	"github.com/charmbracelet/lipgloss"
	"github.com/piprim/git-zf/git"
)

// hintStyle matches the cd hint printed by issue start.
var hintStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))

// SourceTree resolves the linked worktree that has branch checked out and opens
// a client on it, for use as Params.SourceClient. Returns (nil, nil, nil) when
// the branch is not held by a linked worktree. A prunable entry (its directory
// is gone) is treated the same way, with a `git worktree prune` hint on stderr:
// git still refuses to check out or delete the branch until the entry is
// pruned, and nothing here can act on a missing directory.
func SourceTree(ctx context.Context, main *git.Client, branch string) (*git.Client, *git.Worktree, error) {
	wt, err := main.WorktreeFor(ctx, branch)
	if err != nil {
		return nil, nil, fmt.Errorf("locate worktree for %q: %w", branch, err)
	}
	if wt == nil {
		return nil, nil, nil
	}
	if wt.Prunable {
		fmt.Fprintf(main.IO().Err,
			"warning: branch %q is held by a stale worktree entry (%s); run `git worktree prune` before deleting it\n",
			branch, wt.Path)

		return nil, nil, nil
	}

	src, err := git.NewClientAt(main.IO(), wt.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("open worktree %s: %w", wt.Path, err)
	}
	if remote, rerr := main.Remote(); rerr == nil && remote != "" {
		src.SetRemote(remote)
	}

	return src, wt, nil
}

// ConfirmRemoveFunc asks whether the worktree at path should be removed.
type ConfirmRemoveFunc func(ctx context.Context, path string) (bool, error)

// RemoveWorktreeStep is the post-merge step shared by issue close and branch
// merge: confirm, then `git worktree remove` (never --force). A refusal by git
// (modified or untracked files) is printed as a warning with a manual --force
// hint and is not an error: the merge already landed. invokedFrom is the
// working-tree root the command was typed in; when it is the removed worktree
// a cd hint back to the main tree is printed, since the shell now sits in a
// deleted directory. Returns whether the worktree was removed.
func RemoveWorktreeStep(
	ctx context.Context, main *git.Client, wt *git.Worktree, invokedFrom string, confirm ConfirmRemoveFunc,
) (bool, error) {
	ok, err := confirm(ctx, wt.Path)
	if err != nil {
		return false, err //nolint:wrapcheck // prompter already wraps
	}
	if !ok {
		fmt.Fprintf(main.IO().Out, "Worktree %q kept.\n", wt.Path)

		return false, nil
	}

	if err := main.RemoveWorktree(ctx, wt.Path); err != nil {
		fmt.Fprintf(main.IO().Err,
			"warning: %v\nRemove it manually with: git worktree remove --force %s\n", err, wt.Path)

		return false, nil
	}

	fmt.Fprintf(main.IO().Out, "Removed worktree %q.\n", wt.Path)

	if invokedFrom != "" && git.SamePath(invokedFrom, wt.Path) {
		if root, rerr := main.WorkingTreeRoot(); rerr == nil {
			fmt.Fprintln(main.IO().Out,
				hintStyle.Render("Run 'cd "+root+"' — the worktree you were in has been removed."))
		}
	}

	return true, nil
}
```

- [ ] **Step 4: Run the tests**

Run: `mise exec -- go test ./cmd/mergeflow/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/mergeflow/worktree.go cmd/mergeflow/worktree_test.go
git commit -m "feat(mergeflow): shared source-tree lookup and remove-worktree step"
```

---

### Task 6: Close prompter, forms, and remote branch deletion

**Files:**
- Modify: `tui/issue.go:649-655`
- Modify: `cmd/issue/close_prompter.go:24-58, 152-159`
- Modify: `cmd/issue/close_prompter_test.go:20-48, 94-100`
- Modify: `cmd/issue/close.go:642-665` (`doDeleteBranch`)
- Modify: `cmd/issue/close_e2e_test.go` (origin helper + one test)

**Interfaces:**
- Produces: `ClosePrompter.ConfirmRemoveWorktree(ctx, path string) (bool, error)`; `tui.IssueRemoveWorktree(path string, confirmed *bool) *huh.Group`; `scriptedPrompter.RemoveWorktree bool`, `RemoveWorktreeErr error`, `RemoveWorktreeCalls int`.
- Produces: `doDeleteBranch(ctx, c, picked, strategy, prompter, heldByWorktree bool) error`.

- [ ] **Step 1: Write the failing E2E test for remote deletion**

Add to `cmd/issue/close_e2e_test.go`, next to `addBranch`:

```go
// addOrigin wires a bare origin, pushes main and the seeded feature branch,
// and re-opens the client so remote auto-detection sees it.
func (r *closeTestRig) addOrigin(t *testing.T) string {
	t.Helper()

	originDir := filepath.Join(t.TempDir(), "origin.git")
	if out, err := exec.CommandContext(t.Context(), "git", "init", "-q", "--bare", "--initial-branch=main", originDir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v\n%s", err, out)
	}
	mustRunGitAt(t, r.dir, "remote", "add", "origin", originDir)
	mustRunGitAt(t, r.dir, "push", "-q", "origin", "main", "ABC-1@feat@add-thing")
	r.rebuildClient(t)

	return originDir
}

// rebuildClient re-opens the git client on the rig's repo (needed after the
// remote set changes, since Remote() caches its detection).
func (r *closeTestRig) rebuildClient(t *testing.T) {
	t.Helper()

	c, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: r.stdout, Err: r.stderr}, r.dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}
	r.client = c
}

func lsRemoteHeads(t *testing.T, dir, branch string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", "ls-remote", "--heads", "origin", branch)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-remote: %v", err)
	}

	return strings.TrimSpace(string(out))
}

func TestClose_DeletesRemoteFeatureBranch(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	rig.addOrigin(t)

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("local feature branch deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})
	t.Run("remote feature branch deleted", func(t *testing.T) {
		if got := lsRemoteHeads(t, rig.dir, "ABC-1@feat@add-thing"); got != "" {
			t.Fatalf("origin still has the branch: %q", got)
		}
	})
}
```

Add `"strings"` to the imports if missing (`bytes`, `exec`, `filepath`, `pkg`, `git` are already imported).

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run '^TestClose_DeletesRemoteFeatureBranch$' -v`
Expected: FAIL in "remote feature branch deleted" (the ref is still on origin).

- [ ] **Step 3: Add the remote deletion and the held-by-worktree guard to `doDeleteBranch`**

Replace `doDeleteBranch` in `cmd/issue/close.go`:

```go
// doDeleteBranch offers to delete the merged feature branch locally and on the
// remote (mirrors branch merge). heldByWorktree is true when the branch is
// still checked out in a linked worktree that was kept: git would refuse the
// local delete, so it is skipped with a warning while the remote delete still
// runs.
func doDeleteBranch(
	ctx context.Context,
	c *git.Client,
	picked *store.BranchRow,
	strategy commit.MergeStrategy, prompter ClosePrompter, heldByWorktree bool) error {
	shouldDelete, err := prompter.ConfirmDeleteBranch(ctx, picked.BranchName)
	if err != nil {
		//nolint:wrapcheck // prompter error already wrapped by huhPrompter
		return err
	}

	if !shouldDelete {
		return nil
	}

	if heldByWorktree {
		fmt.Fprintf(c.IO().Err,
			"warning: branch %q is still checked out in its worktree; delete it after `git worktree remove`\n",
			picked.BranchName)
	} else {
		force := strategy == commit.MergeStrategySquash || strategy == commit.MergeStrategyRebase
		if err := c.DeleteLocalBranch(ctx, picked.BranchName, force); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: delete branch: %v\n", err)
		}
	}

	if c.RemoteBranchExists(ctx, picked.BranchName) {
		if err := c.DeleteRemoteBranch(ctx, picked.BranchName); err != nil {
			fmt.Fprintf(c.IO().Err, "warning: delete remote branch: %v\n", err)
		}
	}

	return nil
}
```

Update the single call site in `runClose` to pass `false` for now:

```go
	if err := doDeleteBranch(ctx, deps.client, picked, res.Strategy, prompter, false); err != nil {
```

- [ ] **Step 4: Add the forms and the prompter method**

In `tui/issue.go` replace `IssueDeleteBranch` and add the new form:

```go
// IssueDeleteBranch confirms deleting the merged feature branch locally and on
// the remote.
func IssueDeleteBranch(branchName string, confirmed *bool) *huh.Group {
	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Delete branch %q locally and on the remote?", branchName)).
			Value(confirmed),
	)
}

// IssueRemoveWorktree confirms removing the linked worktree that held the
// merged branch. Defaults to yes: the branch is merged, the directory is
// disposable.
func IssueRemoveWorktree(path string, confirmed *bool) *huh.Group {
	*confirmed = true

	return huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Remove worktree %q?", path)).
			Description("The branch is merged; git worktree remove deletes the directory (refused if it has changes).").
			Value(confirmed),
	)
}
```

In `cmd/issue/close_prompter.go` add to the `ClosePrompter` interface, right after `ConfirmDeleteBranch`:

```go
	// ConfirmRemoveWorktree runs after a successful merge, only when the
	// branch was checked out in a linked worktree, and before
	// ConfirmDeleteBranch.
	ConfirmRemoveWorktree(ctx context.Context, path string) (remove bool, err error)
```

and the implementation after `ConfirmDeleteBranch`:

```go
func (p *huhPrompter) ConfirmRemoveWorktree(ctx context.Context, path string) (bool, error) {
	var remove bool
	if err := huh.NewForm(tui.IssueRemoveWorktree(path, &remove)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("remove worktree form: %w", err)
	}

	return remove, nil
}
```

In `cmd/issue/close_prompter_test.go` add fields to `scriptedPrompter`:

```go
	RemoveWorktree      bool
	RemoveWorktreeErr   error
	RemoveWorktreeCalls int
```

and the method:

```go
func (s *scriptedPrompter) ConfirmRemoveWorktree(_ context.Context, _ string) (bool, error) {
	s.RemoveWorktreeCalls++
	if s.RemoveWorktreeErr != nil {
		return false, s.RemoveWorktreeErr
	}

	return s.RemoveWorktree, nil
}
```

- [ ] **Step 5: Run the close suite**

Run: `mise exec -- go test ./cmd/issue/... -run '^TestClose_' -v`
Expected: PASS, including `TestClose_DeletesRemoteFeatureBranch`.

- [ ] **Step 6: Build and run the tui tests**

Run: `mise exec -- go build ./... && mise exec -- go test ./tui/...`
Expected: OK.

- [ ] **Step 7: Commit**

```bash
git add tui/issue.go cmd/issue/close_prompter.go cmd/issue/close_prompter_test.go cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "feat(close): delete the feature branch on the remote too; add remove-worktree prompt"
```

---

### Task 7: Close flow becomes worktree-aware

**Files:**
- Modify: `cmd/cmdutil/git.go`
- Modify: `cmd/issue/close.go` (`closeDeps`, `buildCloseDeps`, `runClose`, `reviewPreflight`)
- Modify: `cmd/issue/close_e2e_test.go` (worktree rig + six tests)

**Interfaces:**
- Produces: `cmdutil.NewMainClientForCmd(cmd, cfg) (main *git.Client, invokedFrom string, err error)`.
- Produces: `closeDeps.invokedFrom string`.
- Consumes: `mergeflow.SourceTree`, `mergeflow.RemoveWorktreeStep`, `Params.SourceClient`, `doDeleteBranch(..., heldByWorktree)`.

- [ ] **Step 1: Write the failing E2E tests**

Append to `cmd/issue/close_e2e_test.go`:

```go
// newWorktreeCloseRig mirrors newCloseRig but creates the feature branch in a
// linked worktree (as `issue start` does when the user picks "worktree") and
// makes its commit there. rig.client stays on the main tree.
func newWorktreeCloseRig(t *testing.T) (*closeTestRig, string) {
	t.Helper()

	dir := t.TempDir()
	runGit := func(cwd string, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = cwd
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	runGit(dir, "init", "-q", "-b", "main")
	runGit(dir, "config", "user.name", "Test User")
	runGit(dir, "config", "user.email", "test@test.com")
	runGit(dir, "config", "commit.gpgsign", "false")
	writeFileAt(t, dir, "base.txt", "base\n")
	runGit(dir, "add", "base.txt")
	runGit(dir, "commit", "-m", "chore: init")

	wtDir := filepath.Join(t.TempDir(), "repo--ABC-1")
	runGit(dir, "worktree", "add", "-q", "-b", "ABC-1@feat@add-thing", wtDir, "main")
	writeFileAt(t, wtDir, "feature.txt", "feature\n")
	runGit(wtDir, "add", "feature.txt")
	runGit(wtDir, "commit", "-m", "feat: add thing")

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	s, err := store.Open(t.Context(), dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	trackerType := "fake"
	if err := s.InsertIssueWithBranch(t.Context(),
		&store.Issue{IDSlug: "ABC-1", Title: "Add thing", StatusID: store.StatusIDInProgress, TrackerType: &trackerType},
		&store.Branch{Name: "ABC-1@feat@add-thing", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("seed branch: %v", err)
	}
	if _, err := client.WriteBranchRef(t.Context(), "ABC-1", git.BranchRef{
		IssueSlug: "ABC-1", BranchName: "ABC-1@feat@add-thing",
		CreatedAt: time.Now().UTC().Format(time.RFC3339), TrackerType: "fake",
	}); err != nil {
		t.Fatalf("seed branch ref: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.Branch.Base = "main"
	cfg.IssueTracker.Type = "fake"
	rawT, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		t.Fatalf("tracker.New: %v", err)
	}
	fakeT, ok := rawT.(*fake.Tracker)
	if !ok {
		t.Fatalf("tracker.New returned %T", rawT)
	}

	rig := &closeTestRig{dir: dir, client: client, store: s, tracker: fakeT, cfg: cfg, stdout: stdout, stderr: stderr}

	return rig, wtDir
}

func worktreePrompter(rig *closeTestRig, strategy commitpkg.MergeStrategy, remove bool) *scriptedPrompter {
	return &scriptedPrompter{
		Branch:         rig.pickedBranchRow(),
		Strategy:       strategy,
		Confirm:        true,
		Message:        []byte("feat(thing): close ABC-1\n"),
		TrackerStatus:  "Closed",
		DeleteBranch:   true,
		RemoveWorktree: remove,
	}
}

func assertDirGone(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("%s still exists (%v)", dir, err)
	}
}

func assertDirPresent(t *testing.T, dir string) {
	t.Helper()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("%s missing: %v", dir, err)
	}
}

func TestClose_Worktree_RebaseRemovesWorktreeAndBranch(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	rig.addOrigin(t)
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("main carries the close commit", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("remove prompt asked once", func(t *testing.T) {
		if prompter.RemoveWorktreeCalls != 1 {
			t.Fatalf("RemoveWorktreeCalls = %d", prompter.RemoveWorktreeCalls)
		}
	})
	t.Run("worktree directory removed", func(t *testing.T) {
		assertDirGone(t, wtDir)
	})
	t.Run("local branch deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})
	t.Run("remote branch deleted", func(t *testing.T) {
		if got := lsRemoteHeads(t, rig.dir, "ABC-1@feat@add-thing"); got != "" {
			t.Fatalf("origin still has the branch: %q", got)
		}
	})
	t.Run("store records branch as merged", func(t *testing.T) {
		rows, err := rig.store.ListBranches(t.Context(), store.BranchStatusMerged)
		if err != nil {
			t.Fatalf("ListBranches: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("merged rows = %d, want 1", len(rows))
		}
	})
}

func TestClose_Worktree_ClassicAndSquash(t *testing.T) {
	t.Parallel()

	for _, strategy := range []commitpkg.MergeStrategy{commitpkg.MergeStrategyClassic, commitpkg.MergeStrategySquash} {
		t.Run(string(strategy), func(t *testing.T) {
			t.Parallel()

			rig, wtDir := newWorktreeCloseRig(t)
			prompter := worktreePrompter(rig, strategy, true)

			err := runClose(t.Context(), rig.deps(), prompter)

			t.Run("no error", func(t *testing.T) {
				if err != nil {
					t.Fatalf("runClose: %v", err)
				}
			})
			t.Run("main carries the close commit", func(t *testing.T) {
				assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
			})
			t.Run("worktree directory removed", func(t *testing.T) {
				assertDirGone(t, wtDir)
			})
			t.Run("local branch deleted", func(t *testing.T) {
				assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
			})
		})
	}
}

func TestClose_Worktree_RemovalDeclinedKeepsBranch(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, false)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("worktree kept", func(t *testing.T) {
		assertDirPresent(t, wtDir)
		if !strings.Contains(rig.stdout.String(), "kept") {
			t.Fatalf("stdout = %q, want 'kept'", rig.stdout.String())
		}
	})
	t.Run("local branch kept with a warning", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("ABC-1@feat@add-thing")
		if !exists {
			t.Fatal("branch should still exist")
		}
		if !strings.Contains(rig.stderr.String(), "still checked out in its worktree") {
			t.Fatalf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestClose_Worktree_UntrackedFileBlocksRemoval(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	writeFileAt(t, wtDir, "scratch.txt", "keep me\n")
	prompter := worktreePrompter(rig, commitpkg.MergeStrategySquash, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("merge landed", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("worktree and untracked file survive", func(t *testing.T) {
		assertDirPresent(t, filepath.Join(wtDir, "scratch.txt"))
	})
	t.Run("force hint printed", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "git worktree remove --force") {
			t.Fatalf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("local branch kept", func(t *testing.T) {
		exists, _ := rig.client.BranchExists("ABC-1@feat@add-thing")
		if !exists {
			t.Fatal("branch should still exist")
		}
	})
}

func TestClose_Worktree_InvokedFromInsidePrintsCdHint(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	deps := rig.deps()
	deps.invokedFrom = wtDir
	prompter := worktreePrompter(rig, commitpkg.MergeStrategyRebase, true)

	err := runClose(t.Context(), deps, prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("worktree removed", func(t *testing.T) {
		assertDirGone(t, wtDir)
	})
	t.Run("cd hint back to the main tree", func(t *testing.T) {
		if !strings.Contains(rig.stdout.String(), "cd "+rig.dir) {
			t.Fatalf("stdout = %q, want cd hint to %s", rig.stdout.String(), rig.dir)
		}
	})
}

// A prunable entry (directory deleted by hand) makes git refuse to check out
// the branch anywhere until `git worktree prune`; Squash never checks out the
// source, so the close still lands and the hint tells the user what to do.
func TestClose_Worktree_PrunableEntryPrintsPruneHint(t *testing.T) {
	t.Parallel()

	rig, wtDir := newWorktreeCloseRig(t)
	if err := os.RemoveAll(wtDir); err != nil {
		t.Fatalf("rm worktree dir: %v", err)
	}
	prompter := worktreePrompter(rig, commitpkg.MergeStrategySquash, true)

	err := runClose(t.Context(), rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runClose: %v", err)
		}
	})
	t.Run("merge landed", func(t *testing.T) {
		assertHeadSubject(t, rig.dir, "main", "feat(thing): close ABC-1")
	})
	t.Run("prune hint printed", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "git worktree prune") {
			t.Fatalf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("remove prompt not asked", func(t *testing.T) {
		if prompter.RemoveWorktreeCalls != 0 {
			t.Fatalf("RemoveWorktreeCalls = %d, want 0", prompter.RemoveWorktreeCalls)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... -run '^TestClose_Worktree' -v`
Expected: compile error `deps.invokedFrom undefined`.

- [ ] **Step 3: Add `NewMainClientForCmd`**

In `cmd/cmdutil/git.go`:

```go
// NewMainClientForCmd opens the repository like NewClientForCmd and re-anchors
// the client on the main working tree when the command was typed inside a
// linked worktree. invokedFrom is the working-tree root of the directory the
// command was typed in, so callers can tell whether a worktree they remove is
// the one the user's shell is standing in.
func NewMainClientForCmd(cmd *cobra.Command, cfg *config.AppConfig) (mainClient *git.Client, invokedFrom string, err error) {
	c, err := NewClientForCmd(cmd, cfg)
	if err != nil {
		return nil, "", err
	}
	invokedFrom, err = c.WorkingTreeRoot()
	if err != nil {
		return nil, "", fmt.Errorf("working tree root: %w", err)
	}
	mainClient, err = c.MainTree()
	if err != nil {
		return nil, "", fmt.Errorf("resolve main working tree: %w", err)
	}

	return mainClient, invokedFrom, nil
}
```

The "Produces" line above reads `(main *git.Client, ...)`; the real name is `mainClient`.

- [ ] **Step 4: Wire the close flow**

In `cmd/issue/close.go`:

`closeDeps` gains, after `baseOverride`:

```go
	// invokedFrom is the working-tree root the command was typed in. client is
	// always anchored on the main tree; when invokedFrom is a linked worktree
	// that gets removed, a cd hint back to the main tree is printed.
	invokedFrom string
```

`buildCloseDeps`: replace the client construction:

```go
	client, invokedFrom, err := cmdutil.NewMainClientForCmd(cmd, cfg)
	if err != nil {
		_ = s.Close()

		return closeDeps{}, err
	}

	deps := closeDeps{client: client, store: s, cfg: cfg, invokedFrom: invokedFrom}
```

`runClose`: after the materialize block and its rollback defer, before `reviewPreflight`:

```go
	// A branch started as a worktree is checked out there, and git refuses to
	// check it out (or delete it) from the main tree. Hand the engine a client
	// on that worktree; the worktree itself is only touched after the commit.
	srcClient, wt, err := mergeflow.SourceTree(ctx, deps.client, picked.BranchName)
	if err != nil {
		return err
	}

	reviewCleanup, err := reviewPreflight(ctx, deps, picked, srcClient)
```

The `mergeflow.Run` call gains `SourceClient: srcClient`:

```go
	res, err := mergeflow.Run(ctx, deps.client, mergeflow.Params{
		Source:             picked.BranchName,
		Target:             base,
		SourceMaterialized: createdBranch,
		SourceClient:       srcClient,
	}, prompter, prefill)
```

The post-merge tail becomes:

```go
	mergeCommitted = true
	picked = trackPickedCandidate(ctx, deps, picked)

	updateClosedStatus(ctx, deps, picked, prompter)

	worktreeRemoved := false
	if wt != nil {
		worktreeRemoved, err = mergeflow.RemoveWorktreeStep(ctx, deps.client, wt, deps.invokedFrom, prompter.ConfirmRemoveWorktree)
		if err != nil {
			return err
		}
	}

	if err := doDeleteBranch(ctx, deps.client, picked, res.Strategy, prompter, wt != nil && !worktreeRemoved); err != nil {
		return err
	}
```

`reviewPreflight` gains a `src *git.Client` parameter; at the top of the function add:

```go
	// The feature branch is checked out in src when it lives in a linked
	// worktree; the fast-forward / merge below must run there.
	tree := deps.client
	if src != nil {
		tree = src
	}
```

and switch these calls in the function body from `deps.client` to `tree`: `FastForwardOnly(ctx, pending.EffectiveRef, picked.BranchName)`, `IsDirty(ctx)`, `MergeForward(ctx, pending.EffectiveRef, picked.BranchName)`, and the `AbortMerge(ctx)` right after it. Everything else in the function (`ReadReviewRef`, `MergeDryRun`, the review-branch lookups, the returned cleanup closure) stays on `deps.client`.

Two E2E tests call `reviewPreflight` directly (`cmd/issue/close_e2e_test.go:1786` and `:1842` at the time of writing; confirm with `grep -n "reviewPreflight(" cmd/issue/*_test.go`). Add `nil` as their fourth argument.

- [ ] **Step 5: Run the close suite**

Run: `mise exec -- go test ./cmd/issue/... -run '^TestClose_' -v`
Expected: PASS for all six worktree tests and the whole pre-existing suite.

- [ ] **Step 6: Run the review suite (shares `reviewPreflight` behaviour through close)**

Run: `mise exec -- go test ./cmd/review/... ./cmd/issue/... ./cmd/cmdutil/...`
Expected: PASS.

- [ ] **Step 7: Manual smoke test (interactive, optional when no TTY is available)**

```bash
mise exec -- go build -o ./bin/git-zf .
ZF=/home/pi/code/pi/git-zf/bin/git-zf
cd "$(mktemp -d)" && git init -q -b main demo && cd demo && git commit -q --allow-empty -m init
$ZF issue start        # enter ID ABC-9, title "smoke", type feat; answer "worktree" when asked
cd ../demo--ABC-9@feat@smoke
echo hi > f.txt && git add f.txt && git commit -q -m "feat: smoke"
$ZF issue close        # pick ABC-9, Rebase, confirm, accept the commit form, accept "Remove worktree"
```

Expected: the close lands on `main` in `demo`, the remove-worktree prompt appears after the tracker step, the worktree directory disappears, and the yellow `cd` hint back to `demo` is printed because the command was typed inside the removed worktree.

- [ ] **Step 8: Commit**

```bash
git add cmd/cmdutil/git.go cmd/issue/close.go cmd/issue/close_e2e_test.go
git commit -m "feat(close): close issues whose branch lives in a linked worktree"
```

---

### Task 8: `branch merge` accepts a worktree-held source

**Files:**
- Modify: `cmd/branch/merge.go:60-70, 108-170`
- Modify: `cmd/branch/merge_prompter.go:28-32` + implementation
- Modify: `cmd/branch/merge_prompter_test.go`
- Modify: `cmd/branch/merge_e2e_test.go`

**Interfaces:**
- Produces: `MergePrompter.ConfirmRemoveWorktree(ctx, path string) (bool, error)`; `scriptedMergePrompter.RemoveWorktree bool`, `RemoveWorktreeCalls int`.
- Consumes: `mergeflow.SourceTree`, `mergeflow.RemoveWorktreeStep`, `Params.SourceClient`, `tui.IssueRemoveWorktree`.

- [ ] **Step 1: Write the failing E2E test**

Append to `cmd/branch/merge_e2e_test.go`:

```go
// addWorktreeBranch creates name one commit ahead of base, checked out in a
// linked worktree, and returns the worktree path.
func (r *mergeRig) addWorktreeBranch(t *testing.T, name, base string) string {
	t.Helper()

	wt := filepath.Join(t.TempDir(), "repo--"+name)
	r.git(t, "worktree", "add", "-q", "-b", name, wt, base)
	mergeWrite(t, wt, name+".txt", name+"\n")
	if out, err := exec.CommandContext(t.Context(), "git", "-C", wt, "add", name+".txt").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	if out, err := exec.CommandContext(t.Context(), "git", "-C", wt, "commit", "-m", "feat: "+name).CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	r.rebuildClient(t)

	return wt
}

func TestRunMerge_SourceInWorktree(t *testing.T) {
	rig := newMergeRig(t)
	rig.addOrigin(t)
	wt := rig.addWorktreeBranch(t, "feature", "master")
	rig.git(t, "push", "-q", "origin", "feature")
	rig.rebuildClient(t)

	p := &scriptedMergePrompter{
		Source:         SourceBranch{Name: "feature"},
		Strategy:       commit.MergeStrategyRebase,
		Confirm:        true,
		Message:        []byte("chore: merge feature\n"),
		RemoveWorktree: true,
		DeleteSource:   true,
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("master carries the merge commit", func(t *testing.T) {
		if got := rig.headSubject(t, "master"); got != "chore: merge feature" {
			t.Fatalf("master HEAD subject = %q", got)
		}
	})
	t.Run("remove prompt asked once", func(t *testing.T) {
		if p.RemoveWorktreeCalls != 1 {
			t.Fatalf("RemoveWorktreeCalls = %d", p.RemoveWorktreeCalls)
		}
	})
	t.Run("worktree removed", func(t *testing.T) {
		if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
			t.Fatalf("worktree still present: %v", statErr)
		}
	})
	t.Run("local source deleted", func(t *testing.T) {
		if rig.branchExists(t, "feature") {
			t.Fatal("local feature should have been deleted")
		}
	})
	t.Run("remote source deleted", func(t *testing.T) {
		if out := rig.gitOut(t, "ls-remote", "origin", "refs/heads/feature"); out != "" {
			t.Fatalf("origin still has feature: %q", out)
		}
	})
}

func TestRunMerge_SourceInWorktree_DeclinedRemovalKeepsLocalBranch(t *testing.T) {
	rig := newMergeRig(t)
	wt := rig.addWorktreeBranch(t, "feature", "master")

	p := &scriptedMergePrompter{
		Source:         SourceBranch{Name: "feature"},
		Strategy:       commit.MergeStrategySquash,
		Confirm:        true,
		Message:        []byte("chore: merge feature\n"),
		RemoveWorktree: false,
		DeleteSource:   true,
	}

	err := runMerge(t.Context(), rig.deps(declinePush), p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runMerge: %v", err)
		}
	})
	t.Run("worktree kept", func(t *testing.T) {
		if _, statErr := os.Stat(wt); statErr != nil {
			t.Fatalf("worktree missing: %v", statErr)
		}
	})
	t.Run("local source kept with a warning", func(t *testing.T) {
		if !rig.branchExists(t, "feature") {
			t.Fatal("feature should still exist (held by its worktree)")
		}
		if !strings.Contains(rig.stderr.String(), "still checked out in its worktree") {
			t.Fatalf("stderr = %q", rig.stderr.String())
		}
	})
}
```

Add `"os"`, `"path/filepath"`, and `"strings"` to the test imports if missing.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/branch/... -run '^TestRunMerge_SourceInWorktree' -v`
Expected: compile error `unknown field RemoveWorktree`.

- [ ] **Step 3: Extend the prompter**

`cmd/branch/merge_prompter.go`, interface:

```go
type MergePrompter interface {
	mergeflow.Prompter // PickStrategy, ConfirmMerge, ComposeMessage
	PickSource(ctx context.Context, sources []SourceBranch) (SourceBranch, error)
	ConfirmRemoveWorktree(ctx context.Context, path string) (remove bool, err error)
	ConfirmDeleteSource(ctx context.Context, source string) (delete bool, err error)
}
```

implementation (next to `ConfirmDeleteSource`):

```go
func (p *huhMergePrompter) ConfirmRemoveWorktree(ctx context.Context, path string) (bool, error) {
	var remove bool
	if err := huh.NewForm(tui.IssueRemoveWorktree(path, &remove)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("remove worktree form: %w", err)
	}

	return remove, nil
}
```

`cmd/branch/merge_prompter_test.go`, struct fields:

```go
	RemoveWorktree      bool
	RemoveWorktreeCalls int
```

method:

```go
func (s *scriptedMergePrompter) ConfirmRemoveWorktree(context.Context, string) (bool, error) {
	s.RemoveWorktreeCalls++

	return s.RemoveWorktree, nil
}
```

- [ ] **Step 4: Wire `runMerge`**

In `cmd/branch/merge.go`, after the materialize block and its rollback defer:

```go
	// A source checked out in a linked worktree cannot be checked out here;
	// give the engine a client on that worktree instead.
	srcClient, wt, err := mergeflow.SourceTree(ctx, d.client, source.Name)
	if err != nil {
		return err
	}
```

pass it to the engine:

```go
	res, err := mergeflow.Run(ctx, d.client, mergeflow.Params{
		Source: source.Name, Target: target, SourceMaterialized: created, SourceClient: srcClient,
	}, prompter, prefill)
```

and replace the post-merge delete block:

```go
	mergeCommitted = true

	// Post-merge: offer to remove the source's worktree (when it has one), then
	// to delete the source (local + remote), then propose push.
	worktreeRemoved := false
	if wt != nil {
		worktreeRemoved, err = mergeflow.RemoveWorktreeStep(ctx, d.client, wt, "", prompter.ConfirmRemoveWorktree)
		if err != nil {
			return err
		}
	}

	if del, derr := prompter.ConfirmDeleteSource(ctx, source.Name); derr != nil {
		return derr //nolint:wrapcheck // prompter already wraps
	} else if del {
		if wt != nil && !worktreeRemoved {
			fmt.Fprintf(d.client.IO().Err,
				"warning: branch %q is still checked out in its worktree; delete it after `git worktree remove`\n",
				source.Name)
		} else {
			force := res.Strategy == commit.MergeStrategySquash || res.Strategy == commit.MergeStrategyRebase
			if delErr := d.client.DeleteLocalBranch(ctx, source.Name, force); delErr != nil {
				fmt.Fprintf(d.client.IO().Err, "warning: delete branch: %v\n", delErr)
			}
		}
		if d.client.RemoteBranchExists(ctx, source.Name) {
			if rErr := d.client.DeleteRemoteBranch(ctx, source.Name); rErr != nil {
				fmt.Fprintf(d.client.IO().Err, "warning: delete remote branch: %v\n", rErr)
			}
		}
	}
```

`invokedFrom` is passed as `""` on purpose: the target is the current branch, so the user cannot be standing in the source's worktree.

- [ ] **Step 5: Run the branch suite**

Run: `mise exec -- go test ./cmd/branch/... -v -run '^TestRunMerge_'`
Expected: PASS, including the two new tests and every pre-existing merge test.

- [ ] **Step 6: Commit**

```bash
git add cmd/branch/merge.go cmd/branch/merge_prompter.go cmd/branch/merge_prompter_test.go cmd/branch/merge_e2e_test.go
git commit -m "feat(branch): merge a source branch held by a linked worktree"
```

---

### Task 9: Documentation and full verification

**Files:**
- Modify: `README.md` (`issue close` section, `branch merge` section)
- Modify: `ROADMAP.md`
- Modify: `CLAUDE.md` (testing notes)

- [ ] **Step 1: README — `issue close`**

In the "The close flow" numbered list, replace step 5 with:

```markdown
5. If the branch was started as a **worktree**, a prompt offers to remove it (`git worktree remove`, never forced: a worktree with modified or untracked files is left in place with a hint). When you ran the command from inside that worktree, a `cd` hint back to the main checkout is printed, since your shell is now in a deleted directory.
6. Optionally delete the branch **locally and on the remote**. Safe delete (`-d`) is used for classic merges; force delete (`-D`) for Squash and Rebase (neither preserves ancestry, so git requires `-D`). A branch still held by a kept worktree is not deleted locally.
```

Add right after the list:

```markdown
`issue close` and every other command work from inside a linked worktree: the local store lives in the repository's common `.git` directory, so worktrees share it. Closing a worktree-held issue runs the Rebase strategy's steps in that worktree and fast-forwards the base from the main checkout; Squash and Classic run in the main checkout. Closing is refused by git when the *base* branch is checked out in another linked worktree.
```

- [ ] **Step 2: README — `branch merge`**

Find the `branch merge` section (`grep -n "branch merge" README.md`) and append one paragraph:

```markdown
A source branch checked out in a linked worktree can be merged too: the merge runs against that worktree, and after the commit lands you are offered to remove the worktree before the usual delete-source step.
```

- [ ] **Step 3: ROADMAP cleanup**

In `ROADMAP.md` remove these entries, all shipped:

- "`git zf issue close` could suggest deleting the folder created by the git worktree if it make sens." (this plan)
- "Delete the remote branches deleting the local branches on `issue close` for example." (this plan)
- "`git zf review status` hangs when there isn't any review." (prints "No review history found." already)
- The whole "*Merge-vs-parent preview* on commit" bullet (shipped in commit `d23f69b`)
- The "One caveat to flag" bullet about `ParentIssueSlug` on a fresh clone (shipped: `resolveParentSlug` falls back to `branch.Parse`)
- The "### Open: `git zf branch merge`" section (shipped in commit `abf50a1`)

Add under "## Enhancement":

```markdown
- `git zf init` installs hooks under the per-worktree git dir when run inside a linked worktree; git reads hooks from the common dir. Run `init` from the main checkout for now.
```

- [ ] **Step 4: CLAUDE.md testing notes**

Under "#### Testing the close flow", append:

```markdown
Worktree-held branches are covered by `newWorktreeCloseRig` in the same file
(feature branch checked out in a linked worktree). The engine-level worktree
cases live in `cmd/mergeflow/mergeflow_worktree_test.go` (`newWorktreeRig`).

    mise exec -- go test ./cmd/issue/... -run "^TestClose_Worktree" -v
    mise exec -- go test ./cmd/mergeflow/... -run "Worktree" -v
```

- [ ] **Step 5: Full verification**

Run: `mise exec -- go build -o ./bin/git-zf . && mise exec -- go test ./...`
Expected: build OK, every package PASS.

Run: `mise exec -- go vet ./...`
Expected: no findings.

- [ ] **Step 6: Commit**

```bash
git add README.md ROADMAP.md CLAUDE.md
git commit -m "docs: worktree-aware close and branch merge; prune shipped roadmap items"
```
