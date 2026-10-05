# Design: reviews stored as signed commit chains

**Date:** 2026-10-04
**Status:** draft

## Overview

`git-bug.md` lists four ideas borrowed from git-bug. Issues already follow the
first one: `refs/zf/issues/<id>` is a chain of commits, one per change, folded
into the current state. Reviews do not. `refs/zf/reviews/<slug>` is a JSON blob
rewritten with compare-and-swap and pushed with `--force-with-lease`, so two
people acting on the same review get a rejected push, the history of rounds is
lost, and nothing can be signed because a blob carries no signature.

This spec moves reviews to the model issues use. Each review action becomes one
commit in a chain; the state is the fold of that chain; concurrent actions merge
instead of colliding. Because the actions are commits, an approval can be signed
and `issue close` can refuse to merge without a valid signature.

It is the first of three steps. The later ones (branch refs as chains, removing
SQLite) get their own specs.

**Decisions taken during brainstorming:**

- Scope: reviews only. Branch refs and the SQLite store stay as they are.
- Storage: one chain per review at `refs/zf/reviews/<slug>`, reusing the issue
  plumbing. Not ops inside the issue chain (a tracker-born issue may have no
  repo record), and not a single signed commit with CAS (keeps the rejections,
  keeps no history).
- Signature: an opt-in gate at close. `git verify-commit` decides; git-zf keeps
  no key list.
- After close: the chain is kept and marked closed. No review ref is ever
  deleted.
- Existing blob refs: no migration. A review in progress at upgrade time is
  requested again.
- Concurrent approvals are all kept; a reject wins over a concurrent approve.
- Tracking refs live under `refs/remotes/<remote>/zf/`, for reviews and for
  issues.

## Section 1: data model

### Ref layout

| Ref | Content |
|---|---|
| `refs/zf/reviews/<slug>` | The local review chain of issue `<slug>`. |
| `refs/remotes/<remote>/zf/reviews/<slug>` | What `<remote>` had at the last fetch or push. |
| `refs/remotes/<remote>/zf/issues/<id>` | Same, for issues. Replaces `refs/zf/remote/issues/<id>`. |

`<remote>` is the configured remote name (`Client.Remote()`), not a literal
`origin`.

Fetch writes only the tracking namespace. A local chain is never touched by a
fetch, so an op written offline cannot be lost.

The tracking refs sit in git's remote-tracking namespace. Two consequences:

- Git prunes by refspec, and the default `+refs/heads/*:refs/remotes/<remote>/*`
  owns everything under `refs/remotes/<remote>/`. A plain `git fetch --prune`
  therefore deletes tracking refs whose refspec is not in the remote's
  configuration. `git zf init` adds the refspecs (see Section 2). Once they are
  configured, a plain `git fetch` also brings review and issue data.
- The refs are listed as remote branches: `git branch -r` shows
  `<remote>/zf/reviews/<slug>` and `<remote>/zf/issues/<id>`. This is accepted.
  No real branch may be named `zf/...`; git-zf never creates one.

### Ops

One commit per action. Its tree holds one file, `op.json`; its parent is the
previous op. The envelope is the one issues use: `v` (1), `type`, `at` (RFC
3339, UTC), `author` (git identity).

| `type` | Fields | Written by |
|---|---|---|
| `request` | `feature_sha` | `review request`. Root commit of the chain on the first round. |
| `start` | none | `review start` |
| `approve` | `approved_sha`, `has_commits` | `review approve` |
| `reject` | `comment`, `has_commits` | `review reject` |
| `close` | none | `issue close` |
| `merge` | none | reconcile; two parents |

`approved_sha` is the commit the reviewer approved: the tip of `<slug>@review`
when that branch exists locally, else the review's `feature_sha`. It is part of
the signed commit, so the signature covers what was approved.

### Fold

Ops are linearized as issue ops are: an op comes after its parents; ops with no
order between them are sorted by `at`, then by commit ID. Every clone computes
the same sequence.

```go
type Approval struct {
    Commit      string // the approve op's commit ID
    Author      string
    ApprovedSHA string
    HasCommits  bool
}

type State struct {
    Slug       string
    Status     string // "", in_review, approved, changes_requested
    Round      int
    FeatureSHA string
    Reviewer   string
    Comment    string     // reject comments of the round, joined by a blank line
    HasCommits bool       // OR over the round's approve and reject ops
    Approvals  []Approval // approvals of the current round
    Closed     bool
    UpdatedAt  time.Time  // at of the last applied op
    Warnings   []string   // commits skipped as malformed
}
```

Status strings are the ones used today.

| Op | Applies when | Effect |
|---|---|---|
| `request` | status is not `in_review` | `Round++`, status `in_review`, `FeatureSHA` set; `Reviewer`, `Comment`, `HasCommits`, `Approvals` cleared; `Closed` false. |
| `start` | status is `in_review` and `Reviewer` is empty | `Reviewer` = op author. |
| `approve` | status is `in_review` or `approved` | status `approved`; appended to `Approvals`; `HasCommits` ORed. |
| `reject` | status is `in_review`, `approved` or `changes_requested` | status `changes_requested`; `Approvals` cleared; comment appended to `Comment`; `HasCommits` ORed. |
| `close` | always | `Closed` true. |
| `merge`, unknown type, unknown version | never | skipped. |

An op that does not apply is ignored. The rules give these outcomes for
concurrent actions:

- Two `request` ops: one round, not two.
- Two `approve` ops: both are kept. Dropping the second would lose its
  `approved_sha`, which may be the only one matching the feature tip once that
  reviewer's commits are incorporated.
- `approve` and `reject`: changes requested, whichever sorts first. An `approve`
  sorted after a `reject` in the same round is ignored because the status is no
  longer `in_review` or `approved`.
- Two `reject` ops: both comments are kept.

Two clones that each create the root of the same slug's chain produce unrelated
histories. Reconcile joins them with a `merge` commit like any other divergence,
and the fold above treats the second `request` as a duplicate.

### Old blob refs

A `refs/zf/reviews/<slug>` that points at a blob was written by an older
git-zf. It is not migrated and not treated as absent: treating an in-review blob
as "no review" would silently unlock the branch.

- `review.Load` returns `ErrLegacyReview` for such a ref.
- `review request` replaces it: it deletes the ref locally and on the remote,
  then creates the root. A remote blob ref would otherwise reject the push of
  the chain for ever.
- `issue close` refuses, with a message naming `git zf review request <slug>`
  and, for a review that is no longer wanted,
  `git push <remote> --delete refs/zf/reviews/<slug>`.
- The pre-push guard stays fail-open, as it is on every error today.
- `review list` skips the ref and prints one warning.
- Reconcile skips tracking refs that are not commits.

## Section 2: code and commands

### `git/`: one chain plumbing, two namespaces

The functions of `git/issue_ref.go` and `git/issue_ref_sync.go` (write a commit,
create a root ref, append with CAS on the local ref, tip, list, read a chain,
fetch, reconcile, pushed check, push) take a namespace:

```go
type ChainRefs struct {
    name     string // "issues" or "reviews": refs/zf/<name>/, refs/remotes/<remote>/zf/<name>/
    idIsRoot bool   // the ref name is the root commit ID (issues)
}

var (
    IssueRefs  = ChainRefs{name: "issues", idIsRoot: true}
    ReviewRefs = ChainRefs{name: "reviews"}
)
```

`idIsRoot` keeps the `ErrIssueRefCorrupt` check for issues and skips it for
reviews, whose ref is named by slug. The listing used by reconcile adds
`%(objecttype)` to its format and skips non-commits.

The commit writer takes a `sign` argument: sign when it is true or when
`commit.gpgsign` is true.

Two additions: `VerifyCommit(ctx, sha) error` (`git verify-commit`) and
`CommitSigned(ctx, sha) (bool, error)` (`git log -1 --format=%G?`, false for
`N`).

`git/review_ref.go` loses `ReviewRef`, the CAS write, the lease push and
`DeleteReviewRef`. `git/blob_ref.go` stays: branch refs still use it.

### `internal/chain`

`linearize` moves out of `issue/record.go` into `chain.Order`, which orders
nodes of `{ID, Parents, At}`. `issue.Fold` and `review.Fold` both call it.

### `review/` (new package)

Mirrors `issue/`: `record.go` holds `Op`, `State`, `Approval`, `DecodeOp` and
`Fold` (pure, no git); `repo.go` holds the glue.

| Function | Does |
|---|---|
| `Load(ctx, c, slug) (*State, error)` | Reads and folds one chain. `nil` when the ref does not exist; `ErrLegacyReview` for a blob. |
| `List(ctx, c) ([]State, []string, error)` | Every local chain, with warnings for unreadable ones. |
| `Append(ctx, c, slug, op, sign) error` | Fills `v`, `at`, `author`; writes the op. Creates the root when the chain does not exist. Nothing is pushed. |
| `Fetch(ctx, c) error` | Fetches into the tracking namespace and reconciles. |
| `Push(ctx, c, slug) error` | Plain fast-forward push. A rejected push triggers one fetch, merge and retry. |
| `Sync(ctx, c) error` | `Fetch`, then pushes every chain whose tip differs from its tracking ref. |

`List` runs two git processes per review, closed ones included.
`ponytail:` acceptable to a few hundred reviews; batch all chains through one
`git log --stdin` if listing gets slow.

### Call sites

| File | Change |
|---|---|
| `cmd/review/request.go` | `review.Sync`, `Load`, replace a legacy blob, `Append(request)`, `Push`. The CAS SHA goes away. |
| `cmd/review/start.go` | `Append(start)` and `Push` instead of rewriting the blob. |
| `cmd/review/deps.go` | `recordReviewDecision` appends `approve` (with `approved_sha`) or `reject`; `ensureReviewRecord` and `inReviewBranches` read `State`. |
| `cmd/review/list.go`, `status_cmd.go`, `track.go`, `fetch.go`, `sync.go` | Read through `review.Sync` / `Load` / `List`. `list` hides closed reviews. `status` prints each approver with its signature state. |
| `cmd/review/guard.go` | `review.Fetch` then `Load`; fail-open on any error. |
| `cmd/issueflow/review_guard.go` | `PendingReviewCommits` reads `State`. |
| `cmd/issue/close.go` | `reviewPreflight` reads `State`, runs the gate, and appends `close` where it deletes the ref today. |
| `issue/repo.go`, `issue/record.go` | Use `git.IssueRefs` and `chain.Order`. |
| `cmd/init/init.go` | Adds the two fetch refspecs; deletes stale `refs/zf/remote/*`. |
| `config/` | `[review] require-signed`. |

Every command that writes an op pushes it right away. When the push fails the
op stays local, a warning is printed, and the next `review.Sync` pushes it.

The SQLite `reviews` table is unchanged. It stays a cache filled from `State`,
as it is filled from the blob today.

### `git zf init`

When a remote is configured, `init` adds to `remote.<remote>.fetch`, unless
already present:

```
+refs/zf/reviews/*:refs/remotes/<remote>/zf/reviews/*
+refs/zf/issues/*:refs/remotes/<remote>/zf/issues/*
```

and deletes every local ref under `refs/zf/remote/`. git-zf's own fetch passes
the refspecs explicitly, so a clone that has not run `init` works. There a
`git fetch --prune` removes the tracking refs until the next git-zf fetch
restores them; in between, a push is a no-op and reconcile has nothing to do.

### Signing gate

New configuration, default off:

```toml
[review]
require-signed = true
```

The configuration file is per clone (`<repo>/.git/.git-zf.toml` or `$HOME`), so
each side sets it for itself.

- **Reviewer side.** With the flag, `review approve` always signs the op and
  fails when signing fails. Without it, the op is signed when `commit.gpgsign`
  is true, as issue ops are.
- **Developer side.** With the flag, `issue close` on an approved review passes
  only if some approval of the current round satisfies both:
  1. `git verify-commit <Approval.Commit>` succeeds;
  2. `Approval.ApprovedSHA` equals the tip the feature branch will have once
     reviewer commits are incorporated: the review branch tip when it
     fast-forwards the feature branch, the feature tip when there is nothing to
     incorporate.

  The check runs before the branch is touched. A feature branch that moved
  after the approval needs a merge to incorporate reviewer commits; its tip can
  match no `approved_sha`, so the close is refused. This stops a commit added
  after approval from riding on the signature.
- **Display.** `review status` shows, for each approval, `verified`, `signed,
  not verified` or `unsigned`, whether or not the flag is set.

Trust is git's own: the GPG keyring, or `gpg.ssh.allowedSignersFile` for SSH
signatures. git-zf does not check who the signer is, and does not stop the
branch author from approving.

## Section 3: error handling

| Situation | Behavior |
|---|---|
| Push of an op fails | Warning; the op stays local; the next `review.Sync` pushes it. |
| Push rejected (someone pushed first) | One fetch, merge and retry inside `review.Push`. |
| Fetch fails | Commands warn and continue on local refs, as today. The guard stays fail-open. |
| Gate: no approval verifies | Close refused. The message lists each approver and its signature state. |
| Gate: no verified approval covers the tip | Close refused: the branch has commits the approval does not cover; run `git zf review request` for a new round. |
| Unsigned approval, flag set on the developer side | The review is no longer in review, so it cannot be approved again in place. The message says to run `git zf review request` for a signed round. |
| Signing fails in `review approve` with the flag | The command fails; no op is written. |
| Legacy blob ref | See "Old blob refs". |
| Malformed op commit | Skipped by the fold and reported in `State.Warnings`. |

Two reviewers who push diverging commits to the same `<slug>@review` branch are
stopped by git on the branch push, before any review op is involved. That case
is not handled here.

## Testing

All on real on-disk repos; every assertion in a `t.Run`.

- `review/record_test.go`: a `TestFold` table. One row per rule of the fold
  table, plus: two concurrent requests; two concurrent approvals, one with
  commits; approve and reject in both sort orders; two concurrent rejects;
  request after close; unknown type and unknown version skipped.
- `internal/chain`: the ordering tests move with `linearize`.
- `git/`: the issue ref tests run against both namespaces where the behavior is
  shared; the root check is asserted for issues only; reconcile skips a blob
  tracking ref; tracking refs land under `refs/remotes/<remote>/zf/`.
- `review/repo_test.go`: `Append`, `Load`, `List`, `Sync`; two clones acting
  offline on one review, then syncing with no rejected push and one `merge`
  commit; `ErrLegacyReview` on a blob ref.
- `cmd/review`: the existing suites, ported from blob reads to `State`;
  `request` replacing a legacy blob; `list` hiding a closed review.
- `cmd/issue/close_e2e_test.go`: `close` op appended and the ref kept; a legacy
  blob refuses the close. Gate cases, signing with an SSH key made by
  `ssh-keygen` in a temp dir and skipped when `ssh-keygen` is missing: unsigned
  approval refused; signed approval passes; a commit added after approval
  refused; two approvals where only the second covers the tip passes.
- `cmd/init`: the refspecs are added once; stale `refs/zf/remote/*` are deleted.
- The close, start, merge and repo-issue suites stay green.

## Documentation

- `docs/review-refs.md`: the format, as `docs/issue-refs.md` does for issues.
- `docs/issue-refs.md`: the new tracking namespace, and that a plain
  `git fetch` brings the refs once `git zf init` has run.
- README and the testing section of `CLAUDE.md`.

## Out of scope

- Branch refs (`refs/zf/branches/*`) as chains: next spec.
- Removing the SQLite store: the spec after that. This one makes the `reviews`
  table redundant but does not delete it.
- A server-side hook, and a list of allowed reviewers.
- A git remote helper. The fetch refspecs added by `git zf init` cover the
  need.
- Migration of blob review refs.

## Revisions

- (a) 2026-10-05: `start`, `approve` and `reject` carry `round`; the fold ignores a decision written for another round (final review: a stale approval unlocked the next round).
- (b) `git zf init` configures every remote, not one (a repository with several remotes and no `origin` made init fail).
- (c) `CommitSigned` reads the commit header instead of `%G?` (`%G?` prints N for an SSH signature without an allowed-signers file).
- (d) `Fetch` takes a `silent` argument for the pre-push guard.
- (e) With `require-signed`, the request picker offers an approved review, so a refused close can be re-requested.
- (f) Review ops are JSON-encoded without HTML escaping, so `op.json` stays readable.
- (g) Remote branch listing skips the `zf/` tracking refs.
