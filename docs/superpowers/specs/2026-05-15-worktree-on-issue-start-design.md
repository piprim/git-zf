# Design: Worktree support on `issue start`

**Date:** 2026-05-15
**Status:** approved

## Overview

When starting an issue, `git zf issue start` currently always creates a branch and checks it out in the current working tree. This design adds the ability to create a git worktree instead — a linked working directory on disk checked out to the new branch — leaving the main working tree untouched.

## Configuration

Two new fields added to `BranchConfig` in `config/config.go`:

```go
type BranchConfig struct {
    Base        string  `json:"base"          toml:"base"          mapstructure:"base"`
    Remote      string  `json:"remote"        toml:"remote"        mapstructure:"remote"`
    UseWorktree *bool   `json:"use-worktree"  toml:"use-worktree"  mapstructure:"use-worktree"`
    WorktreeDir string  `json:"worktree-dir"  toml:"worktree-dir"  mapstructure:"worktree-dir"`
}
```

`UseWorktree` is a three-state pointer:
- `nil` (key absent from config) — ask the user at runtime via TUI toggle
- `true` — always create a worktree, skip the toggle
- `false` — always create a plain branch, skip the toggle

`WorktreeDir` is optional. When set, it is the base directory for all worktrees (e.g. `~/worktrees`). `~` is expanded. When absent, the worktree is created as a sibling of the repo root.

Example `.git-zf.toml`:

```toml
[branch]
use-worktree = true
worktree-dir = "~/worktrees"
```

## Git layer

New method on `git.Client`:

```go
// CreateWorktree creates a new branch from baseBranch and checks it out
// in a linked worktree at path. Wraps `git worktree add -b <branch> <path> <base>`.
func (c *Client) CreateWorktree(ctx context.Context, branchName, baseBranch, path string) error
```

The method receives a fully-resolved absolute path. All path and config logic lives in the `cmd` layer.

## Worktree path computation

The full worktree path is `<base>/<repoName>--<branchName>`.

**Base directory** (in priority order):
1. `WorktreeDir` from config (expand `~`)
2. `filepath.Dir(repoRoot)` — parent of the repo root (sibling placement)

**Repo name** (in priority order):
1. Last path segment of the configured remote URL, with `.git` suffix stripped (e.g. `git@github.com:piprim/git-zf.git` → `git-zf`). This works correctly inside Docker containers or any environment where the working directory name does not reflect the repo name.
2. `filepath.Base(repoRoot)` — fallback for local-only repos with no remote.

The `--` separator between repo name and branch name avoids ambiguity when either component contains hyphens.

## TUI layer

New form function in `tui/`:

```go
// WorktreeToggle asks the user whether to create a worktree or a plain branch.
// Pre-selected default is plain branch (useWorktree = false).
func WorktreeToggle(useWorktree *bool) *huh.Group
```

Same pattern as `IssueTrackerToggle`. The form is shown only when `cfg.UseWorktree == nil`.

Decision logic in `RunIssueStart`:

```
cfg.UseWorktree == nil   → show WorktreeToggle → useWorktree = user's choice
cfg.UseWorktree == true  → useWorktree = true  (skip form)
cfg.UseWorktree == false → useWorktree = false (skip form)

useWorktree == true  → createWorktree(cmd, t, pickedIssue, client)
useWorktree == false → createBranch(cmd, t, pickedIssue, client)
```

## `cmd/issue/start.go` changes

### Shared helper: `prepareBranch`

Extracted from the common setup shared by both paths:

```go
func (i Issue) prepareBranch(
    pickedIssue *issue.Issue,
    client *git.Client,
) (branchName, base string, err error)
```

Assembles `branch.New(...)` and resolves the base branch (from `appConfig.Branch.Base` or `client.DefaultBaseBranch()`).

### `createBranch` (existing, unchanged behaviour)

Calls `prepareBranch`, shows confirm form, calls `client.CreateBranch`, calls `persist`, prints success, optionally calls `updateTrackerIssueStatus`.

### `createWorktree` (new, parallel to `createBranch`)

1. Calls `prepareBranch`
2. Resolves repo name (remote URL → fallback dir name)
3. Resolves worktree base dir (`WorktreeDir` config → fallback `filepath.Dir(repoRoot)`)
4. Computes full path: `<base>/<repoName>--<branchName>`
5. Shows confirm form: `"Create worktree %q at %q based on %q?"`
6. Calls `client.CreateWorktree(ctx, branchName, base, path)`
7. Calls `persist(...)` — same store record as `createBranch`
8. Prints:
   ```
   Created worktree %q at %q (based on %q)
   👉 Run 'cd %s' to begin working.
   ```
   The `cd` hint is essential: a Go CLI process cannot change the working directory of the parent shell, so the terminal remains in the main repo after the command exits. Without the hint, muscle memory may lead the user to open their editor and commit work to the wrong tree.
9. Optionally calls `updateTrackerIssueStatus`

### `persist` and `updateTrackerIssueStatus`

Unchanged and shared by both paths. The store does not need to know whether a worktree was used.

## Data flow

```
RunIssueStart
  └─ [tracker or user form] → pickedIssue
  └─ [worktree toggle, if UseWorktree==nil] → useWorktree bool
  └─ useWorktree?
       ├─ false → createBranch → prepareBranch → client.CreateBranch
       │                       → persist → updateTrackerIssueStatus
       └─ true  → createWorktree → prepareBranch → resolveRepoName
                                 → resolvePath
                                 → client.CreateWorktree
                                 → persist → updateTrackerIssueStatus
```

## Error handling

- `CreateWorktree` returns a wrapped error from the git CLI if the path already exists or the branch name conflicts.
- Path computation errors (e.g. `WorkingTreeRoot` failure) surface as errors returned from `createWorktree`.
- `persist` failure is non-fatal (warning printed to stderr), same as `createBranch`.

## Testing

- `git.Client.CreateWorktree`: unit test verifying `git worktree add` is called with the correct arguments.
- `prepareBranch`: unit test for base branch resolution logic.
- Repo name resolution: unit test covering remote URL parsing (HTTPS and SSH forms) and directory fallback.
- Path computation: unit test covering `WorktreeDir` config path and sibling fallback.
