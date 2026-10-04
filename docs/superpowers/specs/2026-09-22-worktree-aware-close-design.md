# Design: worktree-aware `issue close` and `branch merge`

**Date:** 2026-09-22
**Status:** draft

## Overview

`git zf issue start` can create the feature branch in a linked git worktree,
but nothing downstream knows about it. Closing such an issue is broken today:
the merge engine checks out the source branch and then the target, and git
refuses both when the branch lives in another worktree.

Reproduced with plain git (feature branch `feat` checked out in a linked
worktree, `main` in the main tree):

```
$ git checkout feat          # from the main tree
fatal: 'feat' is already used by worktree at '.../repo--feat'
$ git branch -D feat
error: cannot delete branch 'feat' used by worktree at '.../repo--feat'
$ git checkout main          # from inside the worktree
fatal: 'main' is already used by worktree at '.../repo'
```

A second, related defect: inside a linked worktree `git rev-parse --git-dir`
resolves to `.git/worktrees/<name>`, and every command opens its SQLite store
there. Commands run from inside a worktree therefore see a separate, empty
store instead of the repository's real one.

This spec makes the close flow and the shared merge engine worktree-aware, moves
the store to the common git dir, and folds in one adjacent roadmap item: on
`issue close`, the feature branch is also deleted on the remote, as
`branch merge` already does.

**Decisions taken during brainstorming:**

- Closing a worktree-held issue must work both from the main tree and from
  inside the worktree.
- The merge engine keeps the worktree alive during the merge and removes it
  only after the merge commit has landed (approach B). Nothing destructive
  happens before the commit exists, matching the rollback discipline the close
  flow already follows.
- No schema change: the worktree location is discovered live from
  `git worktree list --porcelain`. Git is the source of truth for where a
  branch is checked out, so a worktree the user moved or removed by hand never
  leaves a stale row behind.

### Impact check

GitNexus could not be used: `gitnexus analyze` fails to finalize (empty
registry, `AnalysisNotFinalizedError`) on this machine at the time of writing.
Callers were enumerated by grep instead:

| Symbol | Callers | Notes |
|---|---|---|
| `mergeflow.rebasePreflight` | `rebase`, `classic` (same package) | private to the engine |
| `mergeflow.composeAndCommit` | the three strategy executors | private to the engine |
| `issue.doDeleteBranch`, `issue.reviewPreflight` | `runClose` only | private to the close flow |
| `store.OpenRepo` | 9 command call sites | path changes only inside a linked worktree |
| `gitdir.Get` | `store.OpenRepo`, `config.RepoDir` | `config.RepoDir` keeps using `Get` |
| `git.Client.GitDir` | `MergeInProgress`, `init` hooks, two store opens in `issueflow/start.go` | only the store opens switch to the common dir |

Blast radius is the merge engine, the close flow, `branch merge`, and the store
path. The `branch merge` and close E2E suites are the regression net.

## What this does NOT change

- The three merge strategies' git mechanics (`git/merge.go` helpers keep their
  semantics; they are called on a different client, not rewritten).
- `issue start`: worktree creation, naming (`worktreePath`), and the `cd` hint.
- The `Tracker` interface, the review refs, the branch refs.
- The close picker, the sub-task guard, the review incorporation logic (only
  which client runs it changes).
- `git zf commit`, `review`, `issue list`, `prune`: they keep opening the git
  client at the current directory. They benefit from the store fix only.
- Closing when the *target* branch is checked out in another linked worktree.
  Git's "already used by worktree" error surfaces from the target-side
  checkout, exactly as today. Known limitation.

## Part 1 — Foundation

### 1.1 Worktree discovery (`git` package)

```go
// Worktree is one entry of `git worktree list --porcelain`.
type Worktree struct {
    Path     string // absolute
    Branch   string // short name; "" when detached
    Main     bool   // first entry of the listing
    Prunable bool   // git marked the entry prunable (path gone from disk)
}

func (c *Client) Worktrees(ctx context.Context) ([]Worktree, error)
func (c *Client) WorktreeFor(ctx context.Context, branch string) (*Worktree, error) // nil when not checked out in a linked worktree
func (c *Client) RemoveWorktree(ctx context.Context, path string) error          // `git worktree remove <path>`, never --force
func (c *Client) CommonDir() (string, error)                                       // `git rev-parse --git-common-dir`, absolute
```

`WorktreeFor` ignores the main entry and prunable entries: a branch held by a
prunable entry is reported as "no worktree", and the caller prints a
`git worktree prune` hint (see Part 4).

The porcelain parser is a pure function over the listing text so it can be
unit-tested on canned input, including a `prunable` line.

### 1.2 Main-tree client

```go
// MainClient opens the repository at the current directory and, when that
// directory is a linked worktree, re-opens at the main working tree.
func MainClient(io *pkg.IO) (*Client, error)
```

Implementation: `NewClient`, then `Worktrees`; when the client's
`WorkingTreeRoot` is not the `Main` entry, return `NewClientAt(io, main.Path)`.
Verified during brainstorming that go-git v6 opens a linked worktree and sees
the shared branches and refs, so both clients resolve the same refs.

### 1.3 Store location

`internal/gitdir` gains `Common()` wrapping `git rev-parse --git-common-dir`
(absolute, same normalisation as `Get`). Store opens switch to it:

- `store.OpenRepo` → `gitdir.Common()`.
- The two `store.Open(ctx, deps.Client.GitDir())` calls in
  `cmd/issueflow/start.go` → `deps.Client.CommonDir()`.

For the main tree, common dir == git dir, so the on-disk path is unchanged for
every existing user. `GitDir` / `gitdir.Get` keep their meaning for
`MergeInProgress` (reads the per-worktree `MERGE_HEAD`), `init` (hooks) and
`config.RepoDir`.

Any database a user accumulated under a per-worktree git dir is left in place
and ignored. It only ever held rows written from inside that worktree, which the
main store never saw.

## Part 2 — Merge engine (`cmd/mergeflow`)

### 2.1 Two clients, one engine

```go
type Params struct {
    Source, Target     string
    SourceMaterialized bool
    // SourceClient is a client opened at the linked worktree that has Source
    // checked out. nil → single-tree mode (today's behaviour).
    SourceClient *git.Client
}
```

`run` holds `main` (the client passed to `Run`) and `src`
(`Params.SourceClient`, or `main` when nil). The `Run` signature is unchanged.

### 2.2 Preflight stops checking out the source

`rebasePreflight` today: dirty check → `Checkout(source)` → `ResolveRef("HEAD")`
→ remote → fetch → ancestor check → dry run. New:

1. Dirty check on the tree the strategy will modify: `src` for Rebase, `main`
   for Classic. `rebasePreflight` takes the client to check as a parameter.
2. `featureOrigSHA = ResolveRef("refs/heads/" + source)` — no checkout.
3. Remote, fetch, ancestor check, dry run: unchanged (read-only, on `main`).

The source checkout moves into `rebase()` (step 1 below). In single-tree mode
the only observable difference is that the checkout happens after the read-only
preflight instead of before it.

### 2.3 Per strategy

| Strategy | Runs on | Change |
|---|---|---|
| Squash | `main` | none |
| Classic | `main` | preflight no longer checks out the source; everything else unchanged |
| Rebase | `src` then `main` | see below |

Rebase:

1. `src.Checkout(source)` — no-op when `src` is the worktree (already on it).
2. `src.MergeRebase(source, target)`, rollback closure uses `src.ResetHard`.
3. `composeAndCommit(ctx, src, …)`.
4. `main.FastForwardOnly(source, target)`; on refusal, `errFastForwardDeferred`
   as today.

`composeAndCommit` takes the client explicitly and each strategy passes the one
whose tree holds the staged result.

## Part 3 — Close flow and `branch merge`

### 3.1 Client setup (`cmd/issue/close.go`)

`buildCloseDeps` opens the client with `git.MainClient`, so target-side work
and store cleanup always act on the main tree. `closeDeps` records
`invokedFrom` (the working-tree root of the directory the command was typed
in) so the `cd` hint can be decided later.

After `getPickedBranch`:

```go
wt, _ := deps.client.WorktreeFor(ctx, picked.BranchName)
var srcClient *git.Client
if wt != nil {
    srcClient, err = git.NewClientAt(deps.client.IO(), wt.Path)
}
```

`srcClient` is passed as `Params.SourceClient`.

### 3.2 Review incorporation moves with the source

`reviewPreflight` takes the client to operate on. The `FastForwardOnly` /
`MergeForward` / `IsDirty` / `AbortMerge` calls that act on the feature branch
run on `srcClient` when non-nil. The deferred review cleanup (review branch,
review ref) stays on the main client.

### 3.3 Post-merge steps, in order

Only once `mergeCommitted` is true:

1. `trackPickedCandidate`, `updateClosedStatus` — unchanged.
2. **Remove worktree** (only when `wt != nil`):
   `prompter.ConfirmRemoveWorktree(ctx, wt.Path)` (default yes) →
   `deps.client.RemoveWorktree(ctx, wt.Path)`. On refusal by git: print the
   reason as a warning plus the hint `git worktree remove --force <path>`, and
   continue. When `invokedFrom == wt.Path` and the removal succeeded, print the
   hint `Run 'cd <mainRoot>' — the worktree you were in has been removed.` in
   the same lipgloss style `issue start` uses.
3. **Delete branch**: `ConfirmDeleteBranch` prompt text becomes
   `Delete branch %q locally and on the remote?`. Local delete keeps the
   safe/force rule. Then `if RemoteBranchExists(name) { DeleteRemoteBranch }`,
   warning on failure, exactly as `cmd/branch/merge.go` does. When the worktree
   is still present (declined or refused), the local delete is skipped with a
   warning that names the worktree, since git would refuse it; the remote
   delete still runs.
4. Success line and `proposeClosePush` — unchanged.

On the `FastForwardDeferred` path steps 2 and 3 are skipped: the source is
still needed for the manual fast-forward.

Abort paths are untouched: nothing about the worktree changes before the merge
commit exists, so the materialized-branch rollback and the deferred review
cleanup work as they do now.

### 3.4 Prompters

`ClosePrompter` gains:

```go
ConfirmRemoveWorktree(ctx context.Context, path string) (bool, error)
```

`huhPrompter` implements it with a confirm form; `scriptedPrompter` gets a
canned answer field. Same addition to the `branch merge` prompter and its
scripted counterpart.

### 3.5 `branch merge` (`cmd/branch/merge.go`)

- Client opened with `git.MainClient`.
- After the source is picked: `WorktreeFor` → `SourceClient`.
- Before `ConfirmDeleteSource`: the remove-worktree confirm + removal + `cd`
  hint, same rules as 3.3 step 2. When the worktree is still present, the
  local delete is skipped with a warning; the remote delete still runs.

## Part 4 — Error handling and edge cases

| Situation | Behaviour |
|---|---|
| Worktree has tracked modifications | Rebase aborts in preflight ("commit or stash before merging"). Classic/Squash proceed; the later `worktree remove` is refused by git, the reason is printed, branch kept. |
| Worktree has untracked files | Merge proceeds. `worktree remove` refuses; warning + `--force` hint; local branch delete skipped; close finishes normally. git-zf never force-removes. |
| Removal declined | Worktree and branch kept; close reports the merge and says the worktree was kept. |
| Command typed inside the worktree that gets removed | Process unaffected (all git calls use explicit `-C <path>`); the shell is left in a deleted directory, hence the `cd` hint. |
| Target checked out in another linked worktree | Out of scope; git's "already used by worktree" error surfaces from the target-side checkout as today. |
| Branch held by a prunable worktree entry | Treated as no worktree; single-tree flow. The local delete fails with git's message; close prints `git worktree prune` as the hint when the listing marked the entry prunable. |
| Rebase fast-forward deferred | Commit landed in the worktree on the source. Worktree removal and branch delete skipped; existing recovery instructions printed. |
| Store location | Per-worktree databases are ignored; main-tree path unchanged. |

## Testing

All tests use a real on-disk repo and a scripted prompter. Every assertion is
wrapped in its own `t.Run`.

**`git` package**

- Porcelain parser on canned output: main + linked + detached + prunable.
- Real repo: `WorktreeFor` finds the linked entry and returns nil for the main
  branch; `CommonDir` from both trees returns the same path; `RemoveWorktree`
  succeeds on a clean tree and refuses on an untracked file; `MainClient`
  opened inside a linked worktree reports the main root.

**`internal/gitdir` + `store`**

- `Common()` inside a linked worktree returns the shared `.git`
  (extend `TestGet_linkedWorktree` with a sibling test).
- A row written through `OpenRepo` from inside the worktree is visible from
  the main tree.

**`cmd/mergeflow`** — new worktree rig (feature branch checked out in a linked
worktree, engine called with `SourceClient`):

- One test per strategy: merge commit on the target; worktree still present and
  on its branch; no `MERGE_HEAD` in either tree.
- Rebase abort at the compose step: worktree reset to the original tip.
- The existing single-tree tests stay green (guards the preflight reorder).

**Close E2E** (`cmd/issue/close_e2e_test.go`) — rig variant with the seeded
branch in a linked worktree and a bare remote (as the review tests do):

- Rebase happy path, removal accepted: worktree gone, branch deleted locally
  and on the remote, store row merged.
- Classic and Squash happy paths, removal accepted.
- Removal declined: worktree and branch kept, message printed.
- Untracked file present: removal refused, warning + `--force` hint, branch
  kept, exit nil.
- Deps built from inside the worktree: close succeeds, `cd` hint printed.
- Prunable entry: single-tree flow, `git worktree prune` hint printed.
- Plain-branch close (existing suite) unchanged, plus one assertion that the
  remote branch is deleted when it exists.

**Branch merge E2E** (`cmd/branch/merge_e2e_test.go`)

- Source in a linked worktree: merge succeeds, removal accepted, source deleted
  locally and remotely.

**Regression net**: close, start, prune, merge, review suites all green.

## Documentation updates

- README `issue close`: worktree removal step, local + remote branch deletion,
  the `cd` hint, the untracked-files behaviour.
- README `branch merge`: worktree note.
- ROADMAP: remove the two items covered here and the already-shipped entries
  (`branch merge`, parent-slug caveat, merge-vs-parent preview, `review status`
  empty case).
- CLAUDE.md testing notes: mention the worktree rigs.

## Risks and open questions

- **go-git on linked worktrees.** Verified for open, branch listing, ref
  resolution, and dirty check. The engine also runs `Commit` through go-git on
  the worktree client; the mergeflow worktree tests cover it. If a go-git gap
  appears, the fallback is to route the affected call through the CLI with
  `-C <path>`, which the client already does for most operations.
- **Preflight reorder.** Moving the source checkout after the read-only steps
  changes nothing observable but is the one edit shared by all callers; the
  existing single-tree mergeflow tests are the guard.
- **Hooks in linked worktrees.** `init` installs hooks under `GitDir`, which is
  the per-worktree dir inside a worktree while git reads hooks from the common
  dir. Pre-existing, out of scope, noted for a follow-up.
