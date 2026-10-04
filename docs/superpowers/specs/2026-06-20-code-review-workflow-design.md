# Code Review Workflow — Design Spec

**Date:** 2026-06-20
**Status:** Draft
**Scope:** git-zf code review lifecycle, branch model, commands, data model, sub-task composition

---

## 1. Context & Goals

git-zf must frame and enforce a team code review process without requiring an external issue
tracker. Review state is owned by git-zf itself (SQLite store + git refs), with optional
sync to Redmine, GitHub Issues, or any configured tracker.

The design must be forward-compatible with a future direction where git-zf becomes the
primary issue tracker and external trackers are optional sync targets.

---

## 2. Principles

- **Works without a tracker.** Review state is stored in the local SQLite store and in
  pushable git refs (`refs/zf/reviews/*`). A tracker, when configured, receives optional
  sync updates — it is never the source of truth.
- **Cross-machine visibility via git refs.** Any team member who runs `git fetch` sees the
  current review state of every branch without needing access to another developer's machine
  or a shared server.
- **No force-push.** The reviewer may push commits to the review branch (active review), but
  history is strictly additive. Force-push is never required and git-zf never performs one.
- **Parallel work.** A developer locked out of a branch under review can work on other
  branches concurrently. Sub-tasks of a complex issue can be developed and reviewed in
  parallel.
- **Iteration support.** Review is a multi-round process. Each rejection increments a round
  counter. The full history of review rounds is preserved in the store.

---

## 3. Roles

| Role | Responsibility |
|------|---------------|
| Developer | Opens issue, creates feature branch, submits for review, addresses feedback, closes issue |
| Reviewer | Reviews code (read-only or with pushed fixes), approves or rejects |

git-zf does not technically enforce who plays which role — the team's process does.
Both roles are available to any team member.

---

## 4. Issue Lifecycle & State Transitions

```
in_progress
    │
    ├─► [git zf review request]
    │
in_review  (branch LOCKED for developer)
    │
    ├─► [git zf review reject] ──► changes_requested ──► in_progress  (round N+1)
    │
    └─► [git zf review approve] ──► approved
                                        │
                                        └─► [git zf issue close] ──► merged
```

### State definitions

| State | Who sets it | Branch locked? | Meaning |
|-------|-------------|---------------|---------|
| `in_progress` | `issue start` / `review reject` | No | Developer is working |
| `in_review` | `review request` | **Yes** | Awaiting reviewer decision |
| `changes_requested` | `review reject` | No | Reviewer rejected; developer addressing feedback |
| `approved` | `review approve` | No | Ready to close; gate satisfied |
| `merged` | `issue close` | — | Branch merged into target (master or parent integration branch) |

### Lock semantics

When a branch is `in_review`:
- `issue close` refuses to run.
- git-zf warns if the developer attempts to push to the locked feature branch (via
  `pre-push` hook installed by `git zf install`).
- The reviewer may freely push to `<IssueID>@review`.

`approved` is transient: if `issue close` is aborted after approval, status stays `approved`
so the close can be re-run without re-review.

---

## 5. Branch Model

### Naming convention

The `branch` package uses `@` as the separator for all branch names:

```
{issue-id}@{type}@{slugified-title}           3-part (standard)
{issue-id}@{type}@{slugified-title}@{variant} 4-part (with --variant)
```

Example: `42@feature@login-bug`, `42@fix@null-pointer`

Review branches use the same `@` separator with a 2-part format — IssueID first, then the
`review` type qualifier, with no title slug:

```
{issue-id}@review
```

Example: `42@review`, `10.1@review`

Review branches are not parsed by `branch.Parse()` (which requires 3–4 parts);
`cmd/review/` constructs and recognises them independently via `strings.HasSuffix(name, "@review")`.

### Branch types in the workflow

| Type | Pattern | Created by | Pushed to remote |
|------|---------|-----------|-----------------|
| Feature / fix / doc / etc. | `42@feature@login-bug` | developer — `issue start` | yes |
| Review | `42@review` | reviewer — `review start` | yes |
| Integration (parent issue) | `42@feature@parent-title` | developer — `issue start` on parent | yes |

### Rules

- `<IssueID>@review` always uses the **bare IssueID** — no title suffix. The IssueID comes
  first, consistent with the project's `{issue-id}@{type}@...` convention. The association
  to the feature branch is by IssueID in the store, not by branch name. This eliminates
  naming divergence regardless of whether the feature branch carries a title slug.
- A review branch is always created from the **exact HEAD commit of the feature branch at
  the time of `review request`** — a snapshot, not a moving target.
- Because the feature branch is frozen during review, the review branch is always a linear
  extension of it. Fast-forward is always safe and conflict-free.
- Sub-task branches always branch from their **parent integration branch**, never from master
  directly.
- `@review` as a suffix is reserved and managed exclusively by git-zf.

---

## 6. Command Surface

### `git zf review` subcommand group

```
git zf review request <IssueID>
    Developer submits branch for review.
    - Sets issue status to in_review in store.
    - Records the current feature branch HEAD SHA in the store (snapshot ref).
    - Pushes refs/zf/reviews/<IssueID> to remote (cross-machine lock signal).
    - Installs pre-push hook if not already present (warns on locked-branch push).
    - Prints reviewer instructions: "Run: git zf review start <IssueID>"

git zf review start <IssueID>
    Reviewer begins the review.
    - Fetches remote; resolves feature branch HEAD at lock time (from git ref blob).
    - Creates <IssueID>@review from that exact commit.
    - Records reviewer identity (git config user.name/email) in reviews table.

git zf review approve <IssueID>
    Reviewer approves the branch.
    - If <IssueID>@review has commits ahead of feature/<IssueID>: sets has_commits=1.
    - Sets status to approved in store and updates refs/zf/reviews/<IssueID>.
    - Does NOT trigger issue close — the developer runs that deliberately.

git zf review reject <IssueID>
    Reviewer requests changes.
    - Sets status to changes_requested / in_progress in store.
    - Updates refs/zf/reviews/<IssueID> on remote.
    - Unlocks feature/<IssueID>.
    - If <IssueID>@review has NO commits ahead of feature/<IssueID>:
        deletes <IssueID>@review locally and remotely (nothing to preserve).
    - If <IssueID>@review HAS commits ahead of feature/<IssueID>:
        keeps <IssueID>@review (reviewer suggestions the developer must inspect).
        Prints:
          "<IssueID>@review has N commits with reviewer suggestions.
           Inspect with: git log feature/<IssueID>..<IssueID>@review
           Cherry-pick, adapt, or discard as needed.
           Run `git zf review request <IssueID>` when ready for round 2."
    - Increments round counter in reviews table.

git zf review list
    Lists all issues currently in_review or approved (any machine, via refs).
    - Fetches refs/zf/reviews/* from remote before displaying.
    - Reconciles local store from fetched refs.

git zf review status <IssueID>
    Shows full review history for an issue.
    - Round number, status, reviewer, timestamps, has_commits flag.
    - Fetches the issue's ref before displaying.

git zf review fetch
    Explicit: fetch all refs/zf/reviews/* and reconcile local store.
    Equivalent to a plain `git fetch` for review refs only.
    (A plain `git fetch` also works — this is a convenience alias.)

git zf review sync <IssueID>
    Merge the parent integration branch into a drifted sub-task branch after sibling
    sub-tasks have landed.
    - Detects drift: commits in feature/X not yet in feature/X.N.
    - Merges feature/X INTO feature/X.N (merge-forward, not rebase).
    - No force-push required: the merge commit is additive.
    - The merge commit is benign — issue close (squash/rebase/classic) flattens it.
    - Warns on conflict; does not auto-resolve.
```

### Modified `git zf issue close`

Before executing any merge strategy, `issue close` now runs a **review preflight**:

1. Check store: does an `approved` review record exist for this issue?
   - If no approved review and no reviews table entry at all: proceed (review is optional
     for teams that choose not to enforce it — enforcement is a future config flag).
   - If status is `in_review`: refuse with "Branch is locked for review. Awaiting reviewer
     decision."
   - If status is `changes_requested`: refuse with "Reviewer requested changes. Address
     feedback and run `git zf review request` for round N+1."
2. Check if `<IssueID>@review` exists (local or remote):
   - If yes and has commits ahead of `feature/<IssueID>`:
       Fast-forward `feature/<IssueID>` to `<IssueID>@review` tip.
       Delete `<IssueID>@review` (local + remote).
       Clean up `refs/zf/reviews/<IssueID>`.
   - If yes and no commits ahead: delete `<IssueID>@review` (no-op ff).
   - If no: proceed (read-only review with no branch created, or no review).
3. Proceed with existing merge strategy (squash / rebase / classic) into target branch.

For sub-tasks, the merge target is the parent integration branch (`feature/X`), not master.
For parent issues, the merge target is master (or configured base branch).

---

## 7. Sub-task Composition

### When to use sub-tasks

When an issue X is too large to develop and review atomically, it is split into sub-tasks.
Each sub-task is an independent issue with its own branch and review cycle. The parent issue
groups them and its close represents the full feature landing in master.

### Creating a parent issue with sub-tasks

```bash
git zf issue start X              # creates feature/X (integration branch from master)
git zf issue start X.1 --parent X # creates feature/X.1 from feature/X
git zf issue start X.2 --parent X # creates feature/X.2 from feature/X
git zf issue start X.3 --parent X # creates feature/X.3 from feature/X
```

`issue_relations` table is populated. `feature/X` is flagged as an integration branch —
`issue close X` refuses until all children are `merged`.

### Sub-task review cycle

Each sub-task follows the full review lifecycle independently:

```
feature/X.N  →  review request  →  X.N@review  →  approve / reject  →  issue close X.N
                                                                              ↓
                                                              merged into feature/X
```

Sub-tasks can be developed and reviewed **in parallel**. There is no required ordering
between X.1, X.2, X.3.

### Drift detection

After a sub-task lands in `feature/X`, sibling sub-task branches drift. git-zf detects
this on `review request` or `issue close` for those sibling branches and warns:

```
Warning: feature/X.2 is 3 commits behind feature/X (X.1 has landed).
Run `git zf review sync X.2` to merge feature/X into your branch before submitting for review.
```

`review sync X.2` merges the current tip of `feature/X` into `feature/X.2` (merge-forward).
No force-push is required. The resulting merge commit is flattened when the sub-task is
closed into `feature/X` via squash, rebase, or classic strategy.

### Closing the parent issue

Once all sub-tasks are `merged` into `feature/X`:

```bash
git zf review request X   # optional: integration review of the composed whole
git zf issue close X       # merges feature/X into master
```

The integration review of X is not enforced by git-zf — the team decides based on
complexity. The only guard is that all children must be `merged` before `issue close X`
is allowed.

### Visual diagram

```
master
  └─ feature/X  (integration branch)
       ├─ feature/X.1 ──X.1@review──► merged into feature/X  ✓
       ├─ feature/X.2 ──X.2@review──► merged into feature/X  ✓
       └─ feature/X.3 ──X.3@review──► merged into feature/X  ✓
                                              │
                                    [optional X@review]
                                              │
                                    feature/X ──► master  (git zf issue close X)
```

---

## 8. Data Model

### New table: `reviews`

```sql
CREATE TABLE reviews (
    id          INTEGER PRIMARY KEY,
    issue_id    TEXT    NOT NULL,
    round       INTEGER NOT NULL DEFAULT 1,
    reviewer    TEXT,
    status      TEXT    NOT NULL,            -- in_review | approved | changes_requested
    has_commits INTEGER NOT NULL DEFAULT 0,  -- 1 if reviewer pushed commits to review branch
    created_at  TEXT    NOT NULL,
    resolved_at TEXT
);
```

One row per review round. Full history is preserved across iterations.

### New table: `issue_relations`

```sql
CREATE TABLE issue_relations (
    parent_issue_id TEXT NOT NULL,
    child_issue_id  TEXT NOT NULL,
    PRIMARY KEY (parent_issue_id, child_issue_id)
);
```

### Extended statuses

New values added to the existing status vocabulary in `branches` and `issues` tables:

| Value | Description |
|-------|-------------|
| `in_review` | Branch locked; review in progress |
| `changes_requested` | Reviewer rejected; developer addressing feedback (round N+1) |
| `approved` | Review approved; gate open for `issue close` |

Existing values (`in_progress`, `merged`) are unchanged.

### Git refs schema

```
refs/zf/reviews/<IssueID>
```

Content: JSON blob stored as a git loose object, referenced by the ref.

```json
{
  "status": "in_review",
  "round": 2,
  "feature_sha": "a3f9c1d",
  "created_at": "2026-06-20T10:00:00Z"
}
```

| Field | Purpose |
|-------|---------|
| `status` | Current review status — mirrors store |
| `round` | Current round number |
| `feature_sha` | HEAD of feature branch at lock time — used by `review start` to create review branch from the right snapshot |
| `created_at` | ISO-8601 timestamp of the lock |

**Push:** `git push --force-with-lease=refs/zf/reviews/<IssueID>:<expected-old-sha> origin refs/zf/reviews/<IssueID>`
**Fetch:** `git fetch origin 'refs/zf/reviews/*:refs/zf/reviews/*'`

The local store is a reconciled cache of the ref state. On any command that reads review
state, git-zf fetches the relevant ref and reconciles before acting.

### Atomic updates & concurrency

Because multiple developers may attempt to update the same ref simultaneously (e.g. reviewer
approves while developer requests re-review on a different machine), all ref writes use
git's built-in CAS (Compare-And-Swap) primitives:

**Local atomicity** — every local ref write uses the three-argument form:

```
git update-ref refs/zf/reviews/<IssueID> <new-sha> <expected-old-sha>
```

This fails atomically if the ref has moved since it was last read, preventing silent
overwrites on the local machine.

**Remote atomicity** — every push uses `--force-with-lease` with an explicit expected value:

```
git push --force-with-lease=refs/zf/reviews/<IssueID>:<expected-old-sha> \
    origin refs/zf/reviews/<IssueID>
```

This fails if the remote ref no longer matches what was fetched, i.e. another developer
pushed a state change in the interim.

**Conflict resolution on CAS failure:**

1. Re-fetch `refs/zf/reviews/<IssueID>` from the remote.
2. Read the new state from the ref blob.
3. Reconcile against the local store state machine:
   - The allowed transitions are strict (`in_review` → `approved` | `changes_requested`).
   - A concurrent transition to the same target state: treat as a no-op, succeed silently.
   - A concurrent transition to a conflicting state (e.g. two simultaneous `approve` and
     `reject`): fail with a clear error — "Review state has changed remotely. Current state:
     <state>. Re-run your command or fetch to see the latest review status."
4. The developer resolves by re-reading the current state and deciding whether to proceed.

Because the state machine is deterministic and has no ambiguous concurrent transitions by
design, conflicts are rare and always resolvable without data loss.

---

## 9. Reviewer Workflow — Step by Step

### Round 1

```
1. Developer finishes work on feature/42.
2. Developer runs: git zf review request 42
   → status: in_review; feature/42 locked; refs/zf/reviews/42 pushed.
3. Developer notifies reviewer (tracker, chat, or verbally):
   "Issue 42 ready for review."
4. Reviewer runs: git fetch && git zf review start 42
   → 42@review created from feature/42 HEAD at lock time.
5a. Reviewer reads code only (read-only review):
      git zf review approve 42   or   git zf review reject 42
5b. Reviewer pushes fixes (active review):
      git push origin 42@review   (one or more commits)
      git zf review approve 42   or   git zf review reject 42
```

### On approval

```
6. Developer runs: git zf issue close 42
   → preflight detects approved status.
   → if 42@review has commits: fast-forwards feature/42 to 42@review tip.
   → deletes 42@review (local + remote).
   → cleans up refs/zf/reviews/42.
   → proceeds with merge strategy (squash / rebase / classic) into master.
```

### On rejection (no reviewer commits)

```
6. Reviewer ran: git zf review reject 42
   → feature/42 unlocked; 42@review deleted; round counter = 2.
7. Developer runs: git fetch   (receives updated ref)
   → local store reconciled; feature/42 is now in_progress.
8. Developer addresses feedback, pushes to feature/42.
9. Developer runs: git zf review request 42   (round 2)
   → new lock created; process repeats.
```

### On rejection (with reviewer commits)

```
6. Reviewer ran: git zf review reject 42
   → feature/42 unlocked; 42@review KEPT (has reviewer commits); round counter = 2.
7. Developer runs: git fetch   (receives updated ref + 42@review branch)
   → git-zf prints: "42@review has N commits. Inspect: git log feature/42..42@review"
8. Developer cherry-picks or adapts reviewer commits into feature/42.
9. Developer runs: git zf review request 42   (round 2)
   → stale 42@review is deleted as part of the new lock creation.
   → new lock created; process repeats.
```

---

## 10. Optional Tracker Sync

When a tracker is configured (`issueTracker.type` in `.git-zf.toml`):

| git-zf event | Tracker action |
|-------------|---------------|
| `review request` | Move issue to "in review" status |
| `review approve` | Move issue to "approved" / "resolved" |
| `review reject` | Move issue back to "in progress" |
| `issue close` (after approve) | Close issue in tracker |

Tracker sync is **best-effort and non-fatal**: a network failure or unknown status name
prints a warning but does not abort the git-zf operation. The store + git refs remain the
source of truth.

---

## 11. Lock Enforcement Tiers

Lock enforcement is a spectrum. git-zf implements the client-side tiers; server-side
enforcement is outside its scope but documented here so teams understand the full model.

### Tier 1 — Advisory (default, v1)

`git zf install` installs (or updates) a `pre-push` hook that:
- On push to a branch whose IssueID is `in_review` in the local store: prints a warning and
  exits non-zero, blocking the push.
- If the local store is stale (ref says `in_progress` but store says `in_review`): reconciles
  first, then re-checks.
- Does not block pushes to `<IssueID>@review` branches (the reviewer's branch).

**Limitation:** client-side hooks are bypassable with `git push --no-verify`. A developer
who deliberately or accidentally skips the hook can push to a locked branch unimpeded.
Tier 1 is suitable for teams where trust is high and the hook serves as a safety reminder,
not a security boundary.

### Tier 2 — Strict client-side (future, `review.enforce = true`)

When `review.enforce = true` is set in `.git-zf.toml`, git-zf will also:
- Refuse `git zf` commands that write to a locked branch (double-check at the application
  layer, not just the hook layer).
- Re-verify lock state by fetching the remote ref before any write, not just at hook time.

**Limitation:** still bypassable via `--no-verify` or by using raw git commands directly.
Tier 2 raises the friction barrier significantly but does not provide a hard security
guarantee.

### Tier 3 — Server-side enforcement (outside git-zf scope)

True compliance enforcement requires a server-side `update` or `pre-receive` hook on the
git server that rejects pushes to locked branches regardless of the client used. git-zf
does not install or manage server-side hooks. Teams requiring Tier 3 should implement it
using their hosting platform's mechanisms:

- **GitHub / GitLab / Gitea:** branch protection rules that restrict who can push to
  `feature/*` branches while a review ref exists.
- **Self-hosted bare git:** a `pre-receive` hook that reads `refs/zf/reviews/*` and rejects
  pushes to the corresponding feature branch when status is `in_review`.

The `refs/zf/reviews/*` ref schema (section 8) is deliberately simple so that a server-side
hook can read and enforce it without depending on git-zf itself.

---

## 12. Open Questions / Future Work

- **Naming convention refactor:** `@review` is a new reserved suffix for review branches
  (`<IssueID>@review`), consistent with the existing `@`-separated convention. A broader
  convention refactor (potentially breaking) is a separate effort.
- **git-zf as issue tracker:** The store + git refs design is intentionally forward-compatible
  with git-zf owning issue data entirely (external trackers become optional sync targets).
  This architectural shift is a separate roadmap item.
- **Review enforcement config flag:** `review.enforce = true` (Tier 2) raises client-side
  friction but cannot provide a hard security guarantee — that requires Tier 3 server-side
  hooks (see section 11). Not implemented in the first version.
- **Server-side hook reference implementation:** A sample `pre-receive` hook that enforces
  the lock by reading `refs/zf/reviews/*` would help teams adopting Tier 3. Out of scope
  for v1 but worth providing as documentation or a contrib script.
- **Integration review for parent issues:** Whether closing a parent issue X requires its own
  `review request` is a team policy decision, not enforced by git-zf in v1.
- **Reviewer assignment:** git-zf does not currently track who is the assigned reviewer
  beyond the `reviewer` field recorded at `review start` (from git config identity).
  Formal assignment (with notification) is a future feature.
- **`git zf issue start X.N --parent X` IssueID format:** The IssueID format for sub-tasks
  (dot notation X.1, or independent IDs with a parent link) depends on the tracker and the
  future issue-tracker direction. The `--parent` flag links any IssueID as a child,
  regardless of format.
