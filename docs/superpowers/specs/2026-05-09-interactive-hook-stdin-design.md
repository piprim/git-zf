# Design: Interactive stdin for git hooks during issue close and commit

**Date:** 2026-05-09
**Status:** approved

## Problem

Two related hook problems exist:

1. **`git zf issue close`** — merge operations spawn git subprocesses via
   `exec.CommandContext` without setting `Cmd.Stdin`. Go's `os/exec` defaults
   the subprocess stdin to `/dev/null`. If a hook reads from stdin (e.g.
   prompts the user for confirmation), it receives EOF immediately or blocks
   indefinitely, hanging the merge.

2. **`git zf commit`** — the `Commit` method uses go-git (`wt.Commit()`), a
   pure Go git implementation that **skips hook execution entirely**. No
   `pre-commit`, `commit-msg`, or `post-commit` hooks run.

## Scope

### `git/merge.go` — hook-triggering exec calls

| Call site | Hooks triggered |
|---|---|
| `git checkout baseBranch` in `MergeSquash` | `post-checkout` |
| `git merge --squash branchName` in `MergeSquash` | `post-merge` |
| `git commit -m …` in `MergeSquash` | `pre-commit`, `commit-msg` |
| `git checkout baseBranch` in `MergeNoFF` | `post-checkout` |
| `git merge --no-ff …` in `MergeNoFF` | `pre-commit`, `commit-msg`, `post-merge` |

`git merge --squash` stages changes without committing but does trigger
`post-merge`. Worktree add/remove and branch delete do not trigger user hooks
and remain as `CombinedOutput()` calls.

### `git/git.go` — `Commit` replaced with subprocess

`Commit` is rewritten to call `git commit` via `runInteractive` instead of
`wt.Commit()`. go-git is retained only for reading repo state after the commit
(HEAD hash, stats, branch name) via `buildSummary`.

## Dependency graph

The solution requires restructuring three packages to avoid import cycles.
Current state — `store` and `git` import stdlib only; `internal/pkg` imports
both:

```
internal/pkg → git, store, config
git          → stdlib
store        → stdlib
```

Target state — a clean one-way chain with no cycles:

```
config   → stdlib
pkg      → config                (IO struct, RunInteractive, GetAllowedBranchType)
git      → pkg                   (Client.runInteractive calls pkg.RunInteractive)
store    → git                   (store.OpenRepo auto-detects repo root)
tty/tui  → store                 (BranchFieldOrEmpty/TrackerStatusOrNA move here)
cmd/*    → store, git, pkg
```

Three moves make this possible:

1. **`GetStore` → `store.OpenRepo`** — it is a persistence adapter; it belongs
   in the `store` package.
2. **`BranchFieldOrEmpty`, `TrackerStatusOrNA` → `store`** — nil-safe accessors
   over `store.BranchRow`; they belong with the type they guard. Moving them
   breaks the `pkg → store` link, eliminating the cycle.
3. **`IO` struct + `RunInteractive` → `internal/pkg`** — `pkg` now imports only
   `config` and stdlib, so `git` can safely import it.

## Solution

### `IO` struct and `RunInteractive` in `internal/pkg`

`IO` groups the standard streams used for interactive subprocess operations:

```go
type IO struct {
    In  io.Reader
    Out io.Writer
    Err io.Writer
}
```

`RunInteractive` is a generic helper usable by any package:

```go
func RunInteractive(ctx context.Context, io IO, cmd, dir string, args ...string) error {
    var buf bytes.Buffer
    c := exec.CommandContext(ctx, cmd, args...)
    c.Dir    = dir
    c.Stdin  = io.In
    c.Stdout = io.MultiWriter(io.Out, &buf)
    c.Stderr = io.MultiWriter(io.Err, &buf)

    if err := c.Run(); err != nil {
        return fmt.Errorf("%w: %s", err, buf.String())
    }

    return nil
}
```

- Stdin is wired through so hooks can prompt interactively.
- Stdout and stderr are teed: output appears live and is captured in `buf`
  for error context.
- `dir` sets `cmd.Dir`; no `-C` flag needed.

### `Client` — `IO` field and constructors

`IO` is stored on `Client` as an unexported field. Both constructors take
`*IO` as their first parameter; `nil` falls back to process standard streams:

```go
func NewClient(io *IO) (*Client, error)
func NewClientAt(io *IO, dir string) (*Client, error)
```

Default when `nil`:
```go
io = &IO{In: os.Stdin, Out: os.Stdout, Err: os.Stderr}
```

Cobra command call sites populate `IO` from the cobra command:
```go
client, err := git.NewClient(&git.IO{
    In:  cmd.InOrStdin(),
    Out: cmd.OutOrStdout(),
    Err: cmd.ErrOrStderr(),
})
```

Callers that don't need IO control pass `nil`. Tests inject
`&git.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}`.

### `Client.runInteractive` — thin git wrapper

```go
func (c *Client) runInteractive(ctx context.Context, dir string, args ...string) error {
    return pkg.RunInteractive(ctx, c.io, "git", dir, args...)
}
```

### `MergeSquash` call sites

```go
if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
    return fmt.Errorf("checkout %s: %w", baseBranch, err)
}
if err := c.runInteractive(ctx, root, "merge", "--squash", branchName); err != nil {
    return fmt.Errorf("merge --squash %s: %w", branchName, err)
}
commitArgs := []string{"commit", "-m", msg}
if author != "" {
    commitArgs = append(commitArgs, "--author="+author)
}
if err := c.runInteractive(ctx, root, commitArgs...); err != nil {
    return fmt.Errorf("commit squash: %w", err)
}
```

### `MergeNoFF` call sites

```go
if err := c.runInteractive(ctx, root, "checkout", baseBranch); err != nil {
    return fmt.Errorf("checkout %s: %w", baseBranch, err)
}
if err := c.runInteractive(ctx, root, "merge", "--no-ff", branchName); err != nil {
    return fmt.Errorf("merge --no-ff %s: %w", branchName, err)
}
```

### `Commit` rewrite (`git/git.go`)

`Commit` gains a `ctx context.Context` first parameter and is rewritten to
call `git commit` via `runInteractive`. The commit message is written to a
temp file and passed via `-F` to handle multi-line messages safely:

```go
func (c *Client) Commit(ctx context.Context, msg []byte, opts CommitOptions) (CommitSummary, error) {
    root, err := c.WorkingTreeRoot()
    if err != nil {
        return CommitSummary{}, fmt.Errorf("working tree root: %w", err)
    }

    f, err := os.CreateTemp("", "git-zf-msg-*")
    if err != nil {
        return CommitSummary{}, fmt.Errorf("create temp msg file: %w", err)
    }
    defer os.Remove(f.Name())

    if _, err := f.Write(msg); err != nil {
        _ = f.Close()
        return CommitSummary{}, fmt.Errorf("write commit msg: %w", err)
    }
    if err := f.Close(); err != nil {
        return CommitSummary{}, fmt.Errorf("close temp msg file: %w", err)
    }

    args := []string{"commit", "-F", f.Name()}
    if opts.All        { args = append(args, "--all") }
    if opts.Amend      { args = append(args, "--amend") }
    if opts.NoVerify   { args = append(args, "--no-verify") }
    if opts.Signoff    { args = append(args, "--signoff") }
    if opts.AllowEmpty { args = append(args, "--allow-empty") }
    if opts.Author != "" { args = append(args, "--author="+opts.Author) }

    if err := c.runInteractive(ctx, root, args...); err != nil {
        return CommitSummary{}, fmt.Errorf("commit: %w", err)
    }

    head, err := c.repo.Head()
    if err != nil {
        return CommitSummary{}, fmt.Errorf("read HEAD after commit: %w", err)
    }

    return c.buildSummary(head.Hash(), string(msg))
}
```

After the subprocess commits, go-git reads HEAD from disk and `buildSummary`
builds the `CommitSummary` as before.

**Behavioral change — signoff:** the current code manually builds
`Signed-off-by: <author or config user>`. With `--signoff`, git uses the
committer identity (git config `user.name`/`user.email`). This is more correct
per git conventions. The manual signoff assembly is removed.

`Commit`'s caller in `cmd/commit/commit.go` threads `cmd.Context()` through.
The unused `time` import in `git/git.go` is removed after the rewrite.

### `store.OpenRepo` — replaces `pkg.GetStore`

```go
func OpenRepo(ctx context.Context) (*Store, error) {
    client, err := git.NewClient(nil)
    if err != nil {
        return nil, fmt.Errorf("not a git repository: %w", err)
    }

    root, err := client.WorkingTreeRoot()
    if err != nil {
        return nil, fmt.Errorf("working tree root: %w", err)
    }

    s, err := Open(ctx, filepath.Join(root, ".git"))
    if err != nil {
        return nil, fmt.Errorf("open store: %w", err)
    }

    return s, nil
}
```

All `pkg.GetStore(ctx)` call sites become `store.OpenRepo(ctx)`.

### `BranchFieldOrEmpty` and `TrackerStatusOrNA` → `store`

These nil-safe accessors over `store.BranchRow` move from `internal/pkg` into
the `store` package. Call sites in `tty` and `tui` update their import from
`pkg` to `store`.

## Files changed

| File | Change |
|---|---|
| `internal/pkg/helper.go` | Add `IO` struct + `RunInteractive`; remove `GetStore`, `BranchFieldOrEmpty`, `TrackerStatusOrNA` |
| `git/git.go` | Add `io git.IO` field to `Client`; update constructors; rewrite `Commit`; remove `time` import |
| `git/merge.go` | Add `Client.runInteractive`; update `MergeSquash` and `MergeNoFF` |
| `store/store.go` | Add `OpenRepo`; add `BranchFieldOrEmpty`, `TrackerStatusOrNA` |
| `cmd/commit/commit.go` | Thread `cmd.Context()` into `client.Commit(ctx, …)`; pass cobra IO to `NewClient` |
| All callers of `NewClient` / `NewClientAt` | Pass cobra IO or `nil` |
| All callers of `pkg.GetStore` | Become `store.OpenRepo` |
| `tty/`, `tui/` callers of `BranchFieldOrEmpty` / `TrackerStatusOrNA` | Update import to `store` |

## Testing

Existing tests in `git/merge_test.go` use `NewClientAt(nil, dir)` and pass
as-is — the test repos have no hooks so interactive stdin is never read.

Tests that need silent or controlled I/O inject:
```go
&git.IO{In: strings.NewReader(""), Out: io.Discard, Err: io.Discard}
```

No new tests are required: the existing `TestMergeSquash`, `TestMergeNoFF`
tests continue to validate correct merge behaviour after the refactor.
