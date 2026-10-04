# Submodule-Safe Close via Real Merge + Soft-Reset

**Date:** 2026-05-13
**Status:** Design — pending implementation plan

## Context

`git zf issue close` currently offers two merge strategies: **Squash** (`git merge --squash` + commit on base) and **Classic** (`git merge --no-ff`). The squash path is known to mishandle submodule pointers (gitlinks): when `--squash` collapses tree changes into the index without an intermediate commit, Git can stage the wrong submodule SHA — or skip the update entirely — producing broken or noisy commits on the base branch.

The fix is to add a third strategy, **Rebase**, that produces the same end state as Squash (one clean commit on base, submodule references correct) but uses a submodule-safe mechanism underneath: a real `git merge`, followed by a soft reset to collapse the merged tree into a single staged diff that flows through the existing `tui.commit` form.

Squash and Classic are kept as options.

## Goals

- Add a `Rebase` strategy to the close picker, producing a single clean commit on the local base branch that includes correct submodule pointers.
- Reuse the existing `tui.commit` form so Rebase commits follow the commitizen convention, identical to the squash-via-form path.
- The rebase strategy must **fail fast** when conflicts exist — never leave the repo in a partial mid-rebase state.
- On any failure between the merge and the commit, restore the feature branch to its original tip atomically.
- Match today's offline-then-network-as-needed posture: fetch is explicit and load-bearing for this strategy only.

## Non-goals

- Replacing or modifying the Squash and Classic strategies.
- Pushing the feature branch to the remote (the close flow is local-integration only).
- Updating local base from `origin/<base>` automatically. If local base has diverged from `origin/<base>` after the rebase commit lands on feature, the user is told to reconcile and FF manually.
- End-to-end huh-form tests (same scope boundary as the squash-via-form spec).

## End-to-end flow

```
1. Pre-flight
   - git status --porcelain --untracked-files=no → abort if dirty
     (modified or staged tracked files; untracked files are ignored — they
     survive `git reset --hard` rollback so they don't put user work at risk)
2. Setup
   - git checkout <featureBranch>
   - featureOrigSHA = ResolveRef("HEAD")
   - git fetch origin → abort on failure
   - if IsAncestor(<featureBranch>, "origin/"+<baseBranch>) → abort with
     "feature has no commits ahead of origin/<base> — already integrated?"
   - merge-tree dry-run feature vs origin/<base> → abort with file list on conflict
3. Execute
   - git merge --no-edit origin/<baseBranch>   (real merge — submodule-safe; --no-edit suppresses $EDITOR for the transient merge-commit message that reset --soft will discard)
   - git reset --soft origin/<baseBranch>      (collapse to single staged diff)
4. TUI form
   - FillOutForm pre-filled (type, scope, subject = "Squashed close of <orig> into <base>.")
   - On huh.ErrUserAborted OR commit failure: rollback (see Error Handling)
5. Commit
   - client.Commit(...) — feature now points at single clean commit
6. Deploy
   - git checkout <baseBranch>
   - git merge --ff-only <featureBranch>
   - On FF failure: print remediation; return errFastForwardDeferred
     (skips updateStatus and the delete-branch prompt; does NOT roll back)
7. Bookkeeping (only when FF succeeded)
   - updateStatus (store + tracker), delete-branch prompt — unchanged from today
```

### Why "real merge" instead of "interactive rebase"

The user's request was framed as `git rebase -i origin/main` with submodule-correct mechanics. Two reasons we use `git merge` under the hood instead:

1. **Dry-run accuracy.** `git merge-tree` predicts the *endpoint* of a merge. A rebase replays commits one at a time, so a transient conflict in commit #2 that's resolved by commit #4 will halt the rebase even though the endpoint is clean. With a real merge, the dry-run's "clean" verdict is a guarantee.
2. **Submodule handling.** The original quirk is specific to `git merge --squash`. Regular `git merge` handles submodule gitlinks correctly — same mechanism as `Classic`, just followed by a soft-reset to collapse history.

The user-facing strategy is still called **Rebase** because that matches the operator's mental model ("one clean commit on top of the base"). The docstring on `Client.MergeRebase` is explicit about the actual mechanic.

### Why `git merge` (not `--no-commit`) before the soft reset

`git merge --no-commit` leaves `.git/MERGE_HEAD` and `.git/MERGE_MSG` in place. `git reset --soft` does *not* clear them. The next `git commit` would silently produce a two-parent merge commit instead of the single-parent squash commit we want.

Letting `git merge origin/<base>` complete normally lets Git clean up `MERGE_HEAD`/`MERGE_MSG` as part of finalizing that commit. The subsequent `git reset --soft` then moves HEAD in a clean state, and the next commit is single-parent as intended. The transient merge commit becomes unreachable after the reset and is eventually garbage-collected.

Trade-off accepted: one transient commit appears in the reflog briefly before GC. Cost is negligible; the alternative (`--no-commit` + manual `git update-ref -d MERGE_HEAD` + `rm .git/MERGE_MSG`) requires janitor code that, if forgotten or regressed, reintroduces the two-parent bug silently.

## Architecture

### `git/merge.go` and `git/git.go` — eight new methods

```go
// IO returns the injected IO streams so callers can write to the same
// stdout/stderr the client uses for interactive subprocess invocations.
// This preserves Cobra-aware stream redirection (tests, piping, future TUI
// capture) instead of leaking to the process-global os.Stdout/Stderr.
func (c *Client) IO() *pkg.IO

// IsDirty reports whether the working tree has tracked-file modifications
// or staged-but-uncommitted changes. Wraps `git status --porcelain --untracked-files=no`.
// Untracked files (no `git add`) are intentionally NOT counted as dirty: the
// rollback step `git reset --hard` does not touch untracked content, so their
// presence does not put user work at risk. Ignored files (`.gitignore`) are
// already excluded by `--porcelain`. Any non-empty output → dirty.
func (c *Client) IsDirty(ctx context.Context) (bool, error)

// Checkout switches the working tree to branchName. Wraps `git checkout <name>`.
func (c *Client) Checkout(ctx context.Context, branchName string) error

// FetchOrigin runs `git fetch origin`. Returns a wrapped error when the remote
// is unreachable or auth fails.
func (c *Client) FetchOrigin(ctx context.Context) error

// IsAncestor reports whether child is an ancestor of ancestor (or equal).
// Wraps `git merge-base --is-ancestor`; exit code 0 → true, 1 → false, other → error.
func (c *Client) IsAncestor(ctx context.Context, child, ancestor string) (bool, error)

// MergeRebase prepares featureBranch for a single-commit close. The mechanic
// is a real `git merge origin/<baseBranch>` (submodule-safe) followed by
// `git reset --soft origin/<baseBranch>`, leaving HEAD at origin/<baseBranch>,
// the working tree at the merged state, and the index staged with the diff.
// Caller is responsible for the final commit and for rollback on failure.
func (c *Client) MergeRebase(ctx context.Context, featureBranch, baseBranch string) error

// ResetHard runs `git reset --hard <target>`. Used by the close orchestrator
// to atomically roll the current branch back to its original tip on TUI abort
// or commit failure.
func (c *Client) ResetHard(ctx context.Context, target string) error

// FastForwardOnly checks out targetBranch and runs `git merge --ff-only sourceBranch`.
// Returns a wrapped error containing the git output when FF is refused
// (diverged history) so the caller can render a useful message.
func (c *Client) FastForwardOnly(ctx context.Context, sourceBranch, targetBranch string) error
```

`IO`, `IsDirty`, and `Checkout` live in `git/git.go` (general-purpose); the rest live in `git/merge.go`. `IO()` returns the existing private `io *pkg.IO` field — pure accessor, no allocation. `MergeDryRun` keeps its signature — callers pass `"origin/"+baseBranch` instead of `baseBranch` when using the rebase strategy. `MergeSquash` and `MergeNoFF` are unchanged.

### `cmd/issue/close.go` — strategy enum + new orchestrator

The strategy enum is workflow logic, not UI; it lives next to its single consumer:

```go
type MergeStrategy string

const (
    StrategySquash  MergeStrategy = "squash"
    StrategyRebase  MergeStrategy = "rebase"
    StrategyClassic MergeStrategy = "classic"
)
```

`doMerge` return changes from `(squash bool, aborted bool, err error)` to `(strategy MergeStrategy, aborted bool, err error)`. Dispatch:

```go
switch strategy {
case StrategyClassic:
    err = mc.client.MergeNoFF(ctx, picked.BranchName, base)
case StrategySquash:
    err = doSquashCommit(ctx, mc)             // existing
case StrategyRebase:
    err = doRebaseClose(ctx, mc)              // new
}
```

`doDeleteBranch` accepts `strategy MergeStrategy` instead of `squashed bool` and uses `-D` for `StrategySquash` and `StrategyRebase`, `-d` for `StrategyClassic`.

`closeRunE` recognizes a sentinel `errFastForwardDeferred` from `doRebaseClose` to skip `updateStatus` and the delete-branch prompt while still exiting cleanly. The user is left on the base branch, with feature holding the new single clean commit.

### `doRebaseClose` skeleton

```go
var errFastForwardDeferred = errors.New("commit created, FF deferred")

func doRebaseClose(ctx context.Context, mc *mergeContext) (err error) {
    // Step 1: dirty check.
    if dirty, derr := mc.client.IsDirty(ctx); derr != nil {
        return fmt.Errorf("dirty check: %w", derr)
    } else if dirty {
        return errors.New("working tree is dirty — commit or stash first")
    }

    // Step 2: setup.
    if err := mc.client.Checkout(ctx, mc.pickedBranch.BranchName); err != nil {
        return fmt.Errorf("checkout %q: %w", mc.pickedBranch.BranchName, err)
    }

    featureOrigSHA, err := mc.client.ResolveRef("HEAD")
    if err != nil {
        return fmt.Errorf("resolve HEAD: %w", err)
    }

    if err := mc.client.FetchOrigin(ctx); err != nil {
        return fmt.Errorf("fetch origin: %w", err)
    }

    remoteBase := "origin/" + mc.baseBranch
    integrated, err := mc.client.IsAncestor(ctx, mc.pickedBranch.BranchName, remoteBase)
    if err != nil {
        return fmt.Errorf("ancestor check: %w", err)
    }
    if integrated {
        return fmt.Errorf("%q has no commits ahead of %s — already integrated?",
            mc.pickedBranch.BranchName, remoteBase)
    }

    conflicts, err := mc.client.MergeDryRun(ctx, mc.pickedBranch.BranchName, remoteBase)
    if err != nil {
        return fmt.Errorf("merge dry-run: %w", err)
    }
    if len(conflicts) > 0 {
        // Print conflicts (same as today's squash path).
        return fmt.Errorf("merge conflicts in %q", mc.pickedBranch.BranchName)
    }

    // Step 3: execute the submodule-safe merge + soft reset.
    if err := mc.client.MergeRebase(ctx, mc.pickedBranch.BranchName, mc.baseBranch); err != nil {
        return fmt.Errorf("merge rebase: %w", err)
    }

    // Defer rollback for any failure between here and a successful commit.
    // - Skip rollback on FF-deferred sentinel: commit already landed on feature.
    // - On rollback failure itself, compound both errors so the user sees both.
    defer func() {
        if err == nil || errors.Is(err, errFastForwardDeferred) {
            return
        }
        if rbErr := mc.client.ResetHard(ctx, featureOrigSHA.String()); rbErr != nil {
            err = fmt.Errorf("rollback after %w failed: %v", err, rbErr)
            return
        }
        fmt.Fprintf(mc.client.IO().Err,
            "Rolled back: feature branch %q restored to %s\n",
            mc.pickedBranch.BranchName, featureOrigSHA.String()[:shortSHALen])
    }()

    // Steps 4 + 5: TUI form, then commit.
    baseOriginSHA, err := mc.client.ResolveRef(remoteBase)
    if err != nil {
        return fmt.Errorf("resolve %s: %w", remoteBase, err)
    }

    hint := commitpkg.IssueHint{
        IssueID:    mc.pickedBranch.IssueSlug,
        BranchType: mc.pickedBranch.Type,
    }
    prefill := hint.Prefill(mc.cfg.CommitMessage.Items)
    prefill["subject"] = fmt.Sprintf("Squashed close of %s into %s.",
        featureOrigSHA.String()[:shortSHALen], baseOriginSHA.String()[:shortSHALen])

    authors, _ := mc.client.Authors()
    defaults := tui.CommitOption{Authors: authors}
    if len(authors) > 0 {
        defaults.Author = authors[0]
    }

    msg, opts, err := commitpkg.FillOutForm(ctx, mc.cfg, defaults, mc.store, prefill)
    if err != nil {
        return fmt.Errorf("fill commit form: %w", err)
    }

    if _, err := mc.client.Commit(ctx, msg, git.CommitOptions{
        All:        opts.All,
        Amend:      opts.Amend,
        NoVerify:   opts.NoVerify,
        Signoff:    opts.Signoff,
        AllowEmpty: opts.AllowEmpty,
        Author:     opts.Author,
    }); err != nil {
        return fmt.Errorf("commit: %w", err)
    }

    // Step 6: deploy via FF.
    if ffErr := mc.client.FastForwardOnly(ctx, mc.pickedBranch.BranchName, mc.baseBranch); ffErr != nil {
        fmt.Fprintf(mc.client.IO().Err,
            "Commit created on %q but local %s has diverged from %s.\n"+
                "Run `git pull --ff-only` on %s, then `git merge --ff-only %s` to land it.\n",
            mc.pickedBranch.BranchName, mc.baseBranch, remoteBase,
            mc.baseBranch, mc.pickedBranch.BranchName)
        return errFastForwardDeferred
    }

    return nil
}
```

Notes:
- `mc.client.IsDirty`, `mc.client.Checkout`, and `mc.client.IO()` are added in `git/git.go` alongside `CurrentBranch` etc.
- Stderr writes go through `mc.client.IO().Err` to respect the IO injection set up in `closeRunE` (the Cobra-aware streams via `cmd.OutOrStdout()` / `cmd.ErrOrStderr()`). The existing `doSquashCommit`'s `fmt.Println` calls bypass this injection — they're a pre-existing leak we don't propagate here.
- `shortSHALen` already exists in `cmd/issue/close.go` (= 7).
- Imports added to `cmd/issue/close.go`: `errors`. `git` and `commit` packages are already imported. No `os` import needed.

### `tui/issue.go` — strategy picker becomes ternary, agnostic

```go
type StrategyOption struct {
    Value string  // returned in *selected when chosen
    Label string  // shown in the picker
    Hint  string  // optional one-liner displayed under the label
}

// IssueMergeStrategy renders a single-select picker from the given options.
// The package does not know what strategies mean — callers own the option list.
func IssueMergeStrategy(selected *string, options []StrategyOption) *huh.Group
```

`close.go` owns the option list (labels, hints, default selection) and converts the returned string to `MergeStrategy` after the form runs. `tui` stays a pure rendering layer.

`IssueMergeConfirm` already takes a `strategy string` — no signature change.

## Error handling

| Failure point | Behavior |
|---|---|
| Working tree dirty (step 1) | Abort before any mutation. No rollback needed. |
| Checkout fails (step 2) | Abort before SHA capture / fetch. No rollback needed. |
| `git fetch origin` fails | Abort with wrapped error. No mutation happened. |
| `IsAncestor` reports already-integrated | Abort cleanly. No mutation happened. |
| `MergeDryRun` reports conflicts | Print files, abort. No mutation happened. |
| `MergeRebase` fails partway | Wrapped error propagates; `defer` rolls feature back to `featureOrigSHA`. |
| `FillOutForm` returns `huh.ErrUserAborted` | `defer` rolls back; clear "feature restored" message on stderr. |
| `client.Commit` fails (hook rejection, signing failure, etc.) | `defer` rolls back; hook stderr is preserved through the wrapped error. |
| `FastForwardOnly` fails (local base diverged from `origin/<base>`) | Return `errFastForwardDeferred`. **No rollback** — feature already holds the clean commit. `closeRunE` skips `updateStatus` and the delete-branch prompt. User is told to `git pull` base and FF manually. |
| `ResetHard` itself fails during rollback | Wrap both the original error and the rollback error so the user knows the repo is in a half-state and what triggered it. |

The `defer` uses a named return value and a closure so the rollback observes the actual `err` at function exit, not at the moment the `defer` was declared.

## Edge cases

- **Feature is `HEAD` of nothing ahead of origin/base.** Caught by the `IsAncestor` check in step 2. Aborts cleanly with a "nothing to integrate" message instead of producing an empty commit.
- **Local base is *behind* `origin/<base>` but not diverged.** The new clean commit sits at `origin/<base> + 1`. `git merge --ff-only <feature>` from local base fast-forwards through any commits local base was missing, then to the new commit. Clean.
- **Local base has diverged from `origin/<base>`.** `FastForwardOnly` returns the FF-deferred sentinel. The commit is preserved on feature; user reconciles manually.
- **Feature contains a working-tree-only change to a submodule that isn't committed yet.** Caught by the dirty-tree pre-flight in step 1 (submodule pointer changes show up as modified tracked content).
- **Untracked file would be overwritten by checkout/merge.** `IsDirty` ignores untracked content, but git itself refuses the checkout/merge step before any mutation (`untracked working tree files would be overwritten by …`). Native git error surfaces with file paths — clearer than a pre-emptive refusal would have been.
- **Repo is inside a `git worktree`.** All commands use `git -C <root>` already; `git reset --hard` and `git update-ref` are worktree-safe. No special handling required.
- **Pre-commit hook rejects the commit.** Falls through to the `defer` rollback. Feature is restored; hook's stderr surfaces via the wrapped error.

## Testing

### Unit tests (`git/merge_test.go`)

- `TestFetchOrigin` — set up a bare "origin" repo + a clone; commit on origin; assert `FetchOrigin` updates `refs/remotes/origin/main`.
- `TestIsAncestor` — table-driven against the disk repo helper: descendant returns true, equal returns true, sibling/unrelated returns false, missing ref returns error.
- `TestMergeRebase_clean` — feature with 2 commits, base advances independently on origin/main, run `MergeRebase`, assert:
  - HEAD ref name = featureBranch
  - HEAD SHA = `origin/main` tip
  - `git diff --cached --name-only` lists the expected files
  - `.git/MERGE_HEAD` does not exist
  - `git status --porcelain` shows staged-only changes
- `TestMergeRebase_submodule` — **the regression test that justifies the feature.** Repo with a submodule. Feature advances the submodule pointer + modifies a regular file. Base advances independently (no submodule change). Run `MergeRebase`. Commit. Assert the resulting commit on base contains the correct submodule SHA from feature, not the base's stale pointer. This is the test that would have caught the original `--squash` quirk.
- `TestResetHard` — set up dirty index + working tree, call `ResetHard` to a known SHA, assert clean status and HEAD at target.
- `TestFastForwardOnly_clean` — set up source ahead of target, FF, assert linear history.
- `TestFastForwardOnly_diverged` — source and target diverged, FF returns error containing the git output (substring match on "Not possible to fast-forward" or "non-fast-forward").

### TUI tests (`tui/issue_test.go`)

- Strategy picker: assert it renders the given options and that `*selected` reflects the chosen value. No git-strategy knowledge in tui.

### `cmd/issue/close.go`

- No automated end-to-end (huh forms remain un-mocked, same scope boundary as the squash-via-form spec).
- The `defer`-rollback logic should be exercised by integration tests against a real on-disk repo if the project chooses to invest. For this spec, it's covered by manual end-to-end.

### Manual end-to-end

```bash
mise exec -- go test ./...
mise exec -- go build -o ./bin/git-zf .
make install

# In a repo with submodules, on an in-progress feature branch:
git zf issue close --debug

# Scenarios to verify:
# 1. Clean rebase close
#    - Strategy: Rebase. Confirm. TUI form opens pre-filled.
#    - Submit. Single clean commit lands on base. Submodule SHA correct.
# 2. TUI abort (Esc in the commit form)
#    - Strategy: Rebase. Confirm. In the commit form, press Esc.
#    - Expect: "feature branch <name> restored to <sha>" on stderr.
#    - git log <feature> shows original tip.
# 3. Pre-commit hook rejection
#    - Same as scenario 1 but with a failing pre-commit hook installed.
#    - Expect: rollback runs, feature restored, hook error visible.
# 4. FF-deferred path
#    - Local base diverged from origin/base before close.
#    - Strategy: Rebase, run close.
#    - Expect: commit lands on feature, base unchanged, instructive message
#      printed, store/tracker NOT updated, delete-branch prompt skipped.
# 5. Already-integrated abort
#    - Feature already merged to origin/main, run close.
#    - Expect: "feature has no commits ahead of origin/main" before any mutation.
# 6. Dirty tree pre-flight
#    - Uncommitted edit in working tree, run close with Rebase.
#    - Expect: refusal before any mutation.
```

## Rejected alternatives

### Real-rebase mechanic (`git rebase origin/base` + `reset --soft`)

Original sketch was: `git rebase origin/base` on feature (non-interactive replay of each commit) followed by `git reset --soft origin/base` to collapse. Rejected because rebase replays each commit individually:

> Imagine a feature with 5 commits. Commit #2 introduces a change that conflicts with origin/base, but commit #4 removes or fixes it. The `merge-tree` dry-run returns no conflicts (the endpoint resolves cleanly). The CLI proceeds to the rebase. The rebase crashes on commit #2, dumping the user into a detached HEAD with conflict markers, halting the CLI.

The dry-run's verdict doesn't match the rebase's behavior. Replaced with a real merge whose endpoint mathematics exactly match `merge-tree`'s prediction.

### `git merge --no-commit` + manual `MERGE_HEAD` cleanup

Cleaner reflog (no transient merge commit) but requires explicit `git update-ref -d MERGE_HEAD` + `rm .git/MERGE_MSG` before the TUI commit. Forgetting that janitor step silently produces a two-parent commit instead of a single-parent squash — a regression that's invisible until someone inspects the history. The transient commit from the plain `git merge` path is briefly in the reflog and GC'd; the cost is negligible compared to the silent-bug risk.

### Interactive rebase driven by `GIT_SEQUENCE_EDITOR` + `GIT_EDITOR` shims

Literal interpretation of the user's request. Requires shipping (or generating at runtime) shim scripts, harder to test cross-platform (Windows shell semantics), and inherits the intermediate-conflict problem above. The real-merge mechanic achieves the same end state with stock git primitives.

### Strategy enum in `tui` package

Initially proposed for convenience. Rejected because it routes business logic through a UI package. Moved to `cmd/issue/close.go`; `tui` now takes a generic option list and is agnostic to git workflows.

## Out of scope (flagged for follow-up)

- An automated end-to-end test of the close flow (needs a huh form mock harness — separate work, same boundary as the squash-via-form spec).
- Auto-syncing local base with `origin/<base>` before the close. Today's flow is "user keeps local base in sync"; if FF fails after a Rebase close, the user reconciles manually. A `--sync-base` flag could be added later without disturbing this design.
- Symmetric Rebase variant for the Classic strategy (a `--no-ff` merge against `origin/<base>`). Out of scope until requested.
- Pushing the resulting feature branch with `--force-with-lease`. The close flow remains local-only; pushing is the operator's responsibility (or a future `git zf issue push` command).
