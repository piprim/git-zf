# Review on the feature branch — design

Date: 2026-11-23, revised 2026-11-24 after a review against the code.
Status: to approve.

## Intent

Remove the `<ID>@review` branch. The reviewer works on the feature branch
itself, and `issue close` merges a reviewed branch only when its tip is
exactly the commit a reviewer approved.

**Why.** On 2026-11-19, in the `cpro` repository, a close put four migrations
on `master` where two were expected. The reviewer's local feature branch was a
copy from July, from before the developer's rebase. `review start` had created
`<ID>@review` from the submitted commit, so that old copy was never looked at
until `issue close` merged `<ID>@review` into it. The merge had no conflict
(the files had different names) and both versions of the work went to
`master`.

The second branch is what hid the stale copy, and it is also what every
reconciliation step exists for: fast-forward or merge at close, the choice
between the local and the remote `@review`, `review sync`, the commit guard.
The client-side locks around it can all be bypassed with `--no-verify`, so
they guard nothing.

## Goals

- One branch per issue during a review: no `@review` branch is created, read
  or deleted any more.
- What `issue close` merges for a reviewed issue is exactly what was approved.
- `review start` always brings the reviewer's local feature branch to the
  remote one; a stale copy is replaced there, and `issue close` refuses one
  before anything is changed.
- Less code: the lock, the pending-commit guard and the incorporation step go.

## Non-goals

- Server-side enforcement. git-zf has no server; someone who merges by hand
  with `git merge` is outside its reach, as today.
- Preventing a rebase of the feature branch. It is allowed; it invalidates the
  approval, and the close says so.
- A new op type on the review chain (`refs/zf/reviews/<ID>`). One optional
  field is added to the `request` op (see the model).

## Anti-goals

- A guard that can be bypassed with a flag or answered with one keystroke. No
  hook is installed, and no question lets unapproved commits reach the base:
  the one question a close asks (take the remote commits in?) ends that close,
  and the next one applies rule A.
- A silent merge of two versions of the same work.
- A command that changes a branch, the index or HEAD and then refuses. Every
  check that can refuse runs before the first change.

## Approaches considered

**Who may push to the feature branch during a review.**

1. The reviewer alone: the pre-push lock lets the identity recorded by
   `review start` through. Rejected by the user: it is a client hook, it can
   be bypassed, so it guards nothing.
2. **Both, no lock (chosen).** The feature branch is an ordinary shared branch
   during the review. What protects `master` is the rule at close.

**What `issue close` requires of an approved review.**

A. **Strict (chosen): the tip is exactly an approved commit.** Any commit
   after the approval, a typo or a merge of the base included, needs a new
   round (`review request`, `review approve`). One equality, no dialog.
B. Lenient: the approved commit is an ancestor of the tip; the close lists the
   later commits and asks. Rejected: the confirmation is a guard that guards
   nothing.

## The model

The ops of the review chain are the same: `request` (with `feature_sha`),
`start`, `approve` (with `approved_sha`), `reject`, `close`.

- **The reviewed branch (author's call).** The `request` op gains an optional
  `branch` field: the name of the feature branch submitted. `State.Branch` is
  the field of the current round's request. The reviewer commands and the
  close read the branch from it, so the reviewer still needs nothing but the
  review chain, and an issue with several branches (variants) is never
  ambiguous. A round requested by an older binary has no such field: the
  branch is then the one `branchNameForIssue` returns, as the decision
  commands do today.
- **The field comes from another clone.** `review.DecodeOp` cleans it with
  `text.Line`, as it does the author. A name that `branch.Parse` rejects, or
  whose issue ID is not the review's slug, is treated as absent: it is printed
  on the terminal and handed to git as a ref.
- `feature_sha` is the tip of the feature branch when the review was
  requested.
- `approved_sha` is the tip of the reviewer's local feature branch when they
  approved.
- The reviewer's commits are `feature_sha..approved_sha` on the feature
  branch. Commits the developer pushed during the review and that the reviewer
  pulled before approving are in that range too, and are covered.
- `has_commits` on an approve or reject op keeps its name and means: the local
  branch holds commits on top of `feature_sha` (`feature_sha` is a strict
  ancestor of the tip). A local branch that is missing, or that does not
  contain `feature_sha`, gives `false`.

### The sequence of a close

`runClose` (`cmd/issue/close.go`) runs these steps in this order. Today the
branch is materialized before any fetch and the pull request is looked up
before the review preflight; both orders are wrong for this design.

1. Pick the branch.
2. Fetch the remote. When it cannot be reached, a warning is printed and the
   close goes on with the refs the clone has, as the review sync does today.
3. `MaterializeBranch`: a branch that exists only on the remote is created at
   the tip just fetched. Its rollback on an aborted close is unchanged.
4. `SourceTree`: the worktree that holds the branch.
5. The remote branch check (below).
6. `reviewPreflight`: rule A (below).
7. The pull request: `ForgePullRequest`, the `--base` check, `CheckPushed`. A
   forge merge still refuses a branch with unpushed commits or absent from
   the remote; "behind" can no longer reach it.
8. The merge target and the merge, unchanged.

A spinner is stopped before the question of step 5.

### The remote branch at close

Decided by the user: a close checks that `<remote>/<branch>` has not moved.
When it has, the close offers to bring the remote commits in and then stops,
whatever the answer: what came in has to be looked at, and reviewed, before a
close merges it. It runs for every close of a branch that exists on the
remote, reviewed or not.

| Local branch against the remote one | Close does |
| --- | --- |
| same commit, or local ahead (commits not pushed yet) | goes on |
| local behind (strict ancestor) | offers to fast-forward the local branch to the remote one, then stops |
| each holds commits the other lacks, and a dry-run merge has no conflict | offers to merge the remote branch into the local one, then stops |
| each holds commits the other lacks, and the merge would conflict | stops: names both tips and the conflicting files |

The offer (decided by the user) is one yes/no question that says how many
commits the remote holds and names both tips.

- **No**: the close stops and prints the command that takes the commits.
  Nothing is changed.
- **Cannot ask** (no terminal): the same as no. It is the prompter that knows:
  the new method of `ClosePrompter` returns "cannot ask" when stdin is not a
  terminal. `runClose` never tests the terminal itself: the E2E rigs have
  none, and their scripted prompter must still be asked.
- **Yes**: the fast-forward or the merge runs in the worktree that holds the
  branch. That tree must be clean, for the fast-forward as for the merge, and
  it is checked before either (today only the merge path checks): otherwise
  the close stops and says so. Then the close stops (decided by the user),
  and says what came in (`git log <old tip>..<branch>`) and what to do next:
  - the new tip is the approved commit (the developer taking the reviewer's
    commits): run the close again, rule A lets it through;
  - the new tip is past the approved commit, which is what any merge commit
    is: run `git zf review request` for a new round. The merge stays on the
    branch: it is what that round reviews;
  - the branch has no review: look at what came in, then run the close again.

No close ever merges into the base in the run that changed the feature
branch.

**What a stopped close returns (author's call).** A non-nil sentinel error
(`ErrRemoteMoved`), after a yes as after a no. The issue was not closed: a
script must see a failure, and the issue and branch tables then print the
message, pause and reopen, where a nil return would close the table over the
message. The text after a yes reads as an instruction, not as a failure.

**A rebased remote (author's call).** A diverged branch is either work done in
two places, where a merge is right, or a branch rewritten on the remote, where
a merge brings back what the rebase dropped (the incident: no conflict, four
migrations). git cannot tell the two apart. So the question for a diverged
branch says so in its text, and its default answer is no. With a review, the
merged branch goes to a new round, where a reviewer sees it. Without a
review, the close has stopped and the user is shown what came in before they
run it again.

**Without a review (author's call).** The same rows apply to a branch that
has no review: closing from an old copy is as wrong there. Asking, or
stopping, on a branch that is behind or diverged is new behaviour for those
closes.

### The review at close

`reviewPreflight` first resolves the reviewed branch (`State.Branch`, else
`branchNameForIssue`).

**Another branch of the same issue (author's call).** When the branch being
closed is not the reviewed one (another variant), the review does not gate
the close and stays open, whatever its status: the check comes before the
switch on the status, so a variant is not refused because its sibling is
`in_review` or `changes_requested`. Today such a close is refused in those two
statuses, and when the review is approved it proceeds and marks it closed.

For the reviewed branch, `in_review` and `changes_requested` refuse the close
as today; the messages no longer speak of a lock. `approved` applies rule A.

### Rule A

`T` is the local tip of the reviewed branch.

**The approved commit `A` (author's call).** A round has one approval, except
when two reviewers approved at the same time. `A` is the `approved_sha` that
contains every other `approved_sha` of the round. When there is none (two
reviewers approved diverged commits) the close refuses and asks for a new
round. With `review.require-signed`, the approval that names `A` must carry a
verified signature, or the close refuses as it does today for an unsigned
approval.

| Situation | Close does |
| --- | --- |
| `A` is not in the clone | refuses: fetch the branch first |
| `T` is `A` | proceeds |
| `T` is a strict ancestor of `A` | refuses: the approved commit is ahead of the local branch; names `A` and how to move to it (`git merge --ff-only <A>`) |
| `T` is a strict descendant of `A` | refuses: commits were made after the approval, run `git zf review request` for a new round |
| anything else (stale copy, rebased branch) | refuses: the local branch is not the approved one; names `A` |

The first row is tested first, with an explicit check that the commit object
exists: `git.Client.IsAncestor` returns an error for a missing object, and a
caller that drops it would report the last row.

Every refusal happens before the branch, the index or the working tree is
touched, and the close never moves the feature branch before the merge. The
merge strategies run after this check and are unchanged: a rebase or squash
done by the close itself is not a commit "after the approval".

## Commands

### `review request`

- Records `feature_sha` and the branch name, and proposes to push the branch,
  as today.
- **The branch recorded is the one picked.** Today
  `runReviewRequestInteractive` passes only the issue slug and
  `runReviewRequest` takes the first in-progress branch of that issue, which
  can be another variant. The picked branch name is passed down and is the
  one resolved for `feature_sha` and written in the op.
- No longer says the branch is locked.
- No longer looks for pending reviewer commits and no longer deletes a
  `@review` branch.
- An `approved` review is always offered for a new round (today only with
  `require-signed`): under rule A a new round is the way out of "commits after
  the approval".
- Still refuses while the review is `in_review`.

### `review start`

Fetch, check, then change. Nothing is changed before every check has passed.

1. Fetch the remote (as today).
2. Check, comparing refs only:
   - `<remote>/<branch>` exists and contains `feature_sha`. Otherwise refuse:
     the developer has not pushed what they submitted; the message names
     `feature_sha` and the remote branch. In a clone without a remote, the
     local branch must contain `feature_sha`.
   - Work out what the local branch needs:

     | Local branch | Needs |
     | --- | --- |
     | absent | to be created from `<remote>/<branch>` |
     | the remote tip | nothing |
     | a strict ancestor of the remote tip | a fast-forward |
     | does not contain `feature_sha` | a reset to `<remote>/<branch>`: it is an old copy (the developer rebased, as in the incident) |
     | contains `feature_sha`, and the remote tip is its ancestor | nothing: the commits the remote lacks are the reviewer's own, not pushed yet |
     | contains `feature_sha`, and each holds commits the other lacks | nothing, with a warning (below) |

   - When a fast-forward or a reset is needed, the working tree must be
     clean. Otherwise refuse and say so.
3. Switch to the reviewed branch. A checkout git refuses (a conflicting
   change, a branch held by another worktree) is reported as git reports it;
   nothing has been changed yet.
4. Apply what step 2 worked out. After a reset, the message gives the commit
   the branch was on, which stays reachable through the reflog
   (`git branch <name> <sha>` brings it back).
5. Record the reviewer (`start` op) and push it, as today.

The last row of the table is a reviewer with unpushed commits while the
developer pushed more. The branch is left alone and the command says that the
remote holds commits to merge before approving (`git merge
<remote>/<branch>`): `review approve` would refuse otherwise.

Decided by the user: the local branch is always brought to the remote one, so
the reviewer never reviews or closes from an old copy.

Running it again on a review already started by this user is the same
sequence without step 5.

### `review approve`

- Fetches the remote first, best effort (author's call): a warning when it
  cannot be reached, and the comparison below then uses the refs the clone
  has. The pickers of the decision commands fetch only the chains today.
- `approved_sha` is the local tip of the reviewed branch.
- Refused when the local branch does not exist or does not contain
  `feature_sha`: the message says to run `git zf review start`, which brings
  the branch to the remote one. Today a missing branch falls back to
  approving `feature_sha`.
- Refused when `<remote>/<branch>` holds commits the local branch lacks: the
  approval would not cover them and the close would refuse. The message says
  to take them and look at them first.
- Proposes to push the feature branch when `has_commits`, in place of the
  push of `@review`.

### `review reject`

- Records the reject op with the reason, as today. It needs no local branch:
  a missing or stale one gives `has_commits = false` and nothing is pushed.
- No branch to keep or delete. When `has_commits`, proposes to push the
  feature branch and prints `git log <feature_sha>..<branch>` as the way to
  see the reviewer's commits.

### `review sync`

Keeps only its second step: merging the parent branch into a sub-task. The
candidates are the branches that have a parent. The picked branch name is
passed down, as in `review request`.

### `review track`

Only the feature-branch half remains (record the branch as in progress). The
half that recorded a reviewer from a checked-out `@review` branch goes:
`review start` does it. Its help, `ErrNotTrackable` and the "currently locked
for review" note lose their mentions of `@review` and of the lock.

### `review list`, `review status`, `review fetch`

Unchanged in behaviour. The rows they show (`reviewBranches`,
`cmd/review/deps.go`) carry the reviewed branch (`State.Branch`, else the
branch chain already loaded for the titles) where they carried the synthetic
`<ID>@review` name.

### `git zf commit`

The pending-review guard (`guardPendingReview`, `cmd/commit/review_guard.go`)
and its `--no-verify` exemption for it are removed.

### `issue close`

- The sequence, the remote check and rule A above.
- `reviewPreflight` loses the incorporation (fast-forward, dry-run, merge)
  and the two guards added on 2026-11-23 (`ErrFeatureBranchStale`,
  `ErrReviewBranchRewritten`), which rule A subsumes.
- `checkSignedApproval` reduces to: the approval that names `A` has a
  verified signature.
- The preflight no longer moves the branch, so the forge never needs a push
  before its merge: `ForgeMerge.PushFirst` and the `tipBefore` / `tipAfter`
  comparison go.
- The cleanup after the merge writes and pushes the `close` op; it deletes no
  branch.

### `branch merge`

The refusal of an issue branch (`cmd/branch/merge.go`) no longer speaks of
"review incorporation".

### `git zf init` and the hooks

git-zf installs no hook any more. The three managed scripts (`pre-push`,
`pre-commit`, and the `pre-rebase` added on 2026-11-23) are retired:

- `git zf init` deletes a hook file whose content is byte-identical to a
  retired script and says so. The `pre-push` and `pre-commit` scripts never
  changed since they were introduced, so one version of each is enough. A
  hook with any other content is left alone, with a note when it still calls
  `git zf review guard`.
- `review guard` and `review guard-commit` stay as hidden commands that do
  nothing and exit 0, so a clone where `init` has not been run again keeps
  pushing and committing.
- `init` keeps configuring the fetch refspecs.

## Transition

- **Open reviews with a `@review` branch.** No dedicated code. An approved
  review whose `approved_sha` is the tip of `@review` hits the "strict
  ancestor" row of rule A: the developer fast-forwards the feature branch to
  that commit by hand (`git merge --ff-only <remote>/<ID>@review`) and
  closes. When that is not a fast-forward, they merge by hand and request a
  new round.
- **Leftover `@review` branches** are no longer special: the branch table
  shows them as ordinary untracked branches, to delete with `D`. The home
  screen stops treating a checked-out `@review` as a review in progress.
- **Mixed versions.** An older binary closing a review made with the new one
  finds no `@review` branch and merges its local feature branch as it is,
  which is the behaviour this design removes. Every clone must upgrade; the
  release note says so.

## Constraints

- Unknown op types stay skipped, and ops written by older binaries
  (`has_commits`, `approved_sha` naming a `@review` tip, a request without
  `branch`) must still fold.
- Tests follow the project's rules: real on-disk repositories, scripted
  prompters, `t.Run` for every assertion, `reviewtest.Seed` to seed a review.
- Every refusal names the commit and the command that gets the user out; no
  message recommends `--no-verify` or a forced reset without naming what it
  discards.

## Tests

End-to-end, next to the existing suites.

**`cmd/issue/close_e2e_test.go`**

- The remote branch: behind and yes (fast-forwarded onto the approved commit,
  stopped, base untouched; the second close goes through); behind and no
  (stopped, nothing moved); remote moved after the approval and yes
  (fast-forwarded, stopped, the second close asks for a new round); diverged
  without conflict and yes (merged, stopped, the merge kept, the second close
  asks for a new round); diverged and no; diverged with a conflict (stopped,
  no merge left in progress); dirty tree, on the fast-forward and on the
  merge; a prompter that cannot ask (stopped, nothing moved); unreachable
  remote (warning, close goes on); a branch without a review, behind and
  diverged.
- A branch only on the remote, whose remote moved since the last fetch: it is
  materialized at the fresh tip and closed, with no question.
- One test per row of the rule A table, each checking the branch did not move
  on a refusal; the incident itself (stale local copy plus a renamed file on
  the developer's side, no textual conflict).
- `require-signed` with a signed approval on the tip and with an unsigned
  one. Two approvals in a round, one containing the other (the tip on the
  first is refused, on the second closes) and diverged (refused).
- Variants: variant B closed while variant A is `in_review`,
  `changes_requested` and `approved`; the close proceeds and A's review stays
  open in its status.

**`cmd/issue/close_pr_e2e_test.go`**

- `TestClose_PullRequest_BehindRefused` now meets the remote check, which
  runs before the pull request: it is rewritten for the question.
- `TestClose_PullRequest_ReviewedBranchIsPushedFirst`,
  `..._ReviewedBranchDeclinedLeavesOriginUntouched` and
  `..._ReviewedBranchWithUnpushedCommitRefused` seed `<ID>@review` and expect
  the incorporation and `PushFirst`. They are replaced by: a reviewed branch
  with a pull request, approved on its tip and pushed, merged by the forge;
  the same with an unpushed commit, refused.

**`cmd/review`**

- `start`: branch absent locally; behind the remote; stale copy reset to the
  remote (old tip printed and still reachable); stale copy with a dirty tree
  refused with HEAD and the branch unmoved; submitted commit missing from the
  remote refused with the local branch unmoved; unpushed reviewer commits kept
  when started twice; unpushed reviewer commits with a remote that moved
  (branch untouched, warning).
- `approve`: tip recorded; local branch missing refused; stale copy refused;
  remote ahead refused, seen through the fetch `approve` does itself.
- `reject`: with reviewer commits; without; with a stale local branch
  (`has_commits` false, nothing pushed).
- `request`: on an approved review; on an issue with two in-progress
  variants, the picked one (not the first) is in the op's `branch` and
  `feature_sha`.
- A round without the `branch` field, seeded as an older binary wrote it.
- `sync` with a parent only.

**Elsewhere**

- `review/record_test.go` — `TestFold`: the `branch` field of a request; a
  name with control characters, and one that is not a branch of the issue,
  folded as absent.
- `cmd/mergeflow/forge_test.go` — the `PushFirst` case goes.
- `cmd/init` — a retired hook is deleted, a foreign hook is kept, nothing is
  installed on a fresh repository.
- `cmd/home_data_test.go`, `cmd/commit/commit_test.go` — the cases that
  resolve an issue from a `<ID>@review` name go with `CutReviewSuffix`.
- The tests of the lock, the commit guard, the incorporation and
  `ReviewBranchAhead` are deleted with the code.

`reviewtest.Seed` keeps its signature and writes a request without `branch`
(author's call): the existing suites then run the older-binary path, and the
tests above that need the field write it themselves or go through
`review request`.

## Removed

- `branch/review.go` and `branch/review_test.go` in full
  (`ReviewBranchName`, `IsReviewBranch`, `CutReviewSuffix`), and their
  callers (`cmd/home_data.go`, `cmd/commit/commit.go`, `cmd/issue/close.go`,
  `cmd/review/*`).
- `cmd/issueflow/review_guard.go` (`PendingReview`, `ReviewBranchAhead`,
  `PendingReviewCommits`, `PendingReviewForHEAD`).
- `cmd/commit/review_guard.go`, the bodies of `cmd/review/guard.go` and
  `guard_commit.go`, `mergeReviewerCommits` in `cmd/review/sync.go`.
- `ReviewPrompter.Confirm` (`cmd/review/prompter.go`): its one caller was the
  pending-commits question of `review request`.
- `git.ErrMergeConflicts` and `git.Client.MergeLeaveConflicts`
  (`git/merge.go`): their two callers go. To check with a text search before
  deleting.
- `ForgeMerge.PushFirst` (`cmd/mergeflow/forge.go`) and `tipBefore` /
  `tipAfter` in `cmd/issue/close.go`.
- The hook scripts in `cmd/init/init.go`, kept only as the retired list.

## Documentation

The documentation is part of the work, not a follow-up: a step that changes a
command updates its pages in the same step.

- **`website/docs/workflows/review.md` — rewritten, first.** It is the page
  people follow to run a round. It must give the new round from both sides:
  the diagram (request, start on the feature branch, commit and push on that
  branch, approve or reject, close), what `review start` does to the local
  branch (always brought to the remote one, an old copy replaced), that the
  branch is not locked, how the developer takes the reviewer's commits, the
  strict rule at close with each refusal and the command that gets out of it,
  the question a close asks when the remote moved and why it then stops, and
  `review sync` reduced to sub-tasks.
- `website/docs/workflows/close-an-issue.md` — the remote check, the question,
  rule A.
- `website/docs/workflows/commit.md` — the pending-review guard is gone.
- `website/docs/workflows/sub-tasks.md` — `review sync`.
- `website/docs/under-the-hood/review-refs.md` — the `branch` field of the
  request op, what `approved_sha` and `has_commits` mean now.
- `website/docs/under-the-hood/merge-engine.md`, `branch-refs.md`,
  `concepts/issues-and-branches.md`, `index.md` — every mention of `@review`,
  the lock or the hooks.
- `website/docs/getting-started/install.md`, `first-steps.md` and `README.md`
  — `git zf init` installs no hook; what it still does.
- The generated command pages (`make docs-gen`); `TestGenerate_MatchesCommitted`
  fails until they are regenerated.
- An upgrade note (in `review.md` and the release notes): every clone must
  upgrade, run `git zf init` again to drop the old hooks, and what to do with
  a review that is open with a `@review` branch.
- `CLAUDE.md`: the "Testing the review chains" section, and the mentions of
  the pre-push and pre-commit hooks.

Done means no page under `website/docs` or `README.md` mentions `@review`, a
lock or a git-zf hook, except the upgrade note.

## Left to the builder

- The wording of the messages, within the constraint above.
- Whether rule A lives in `reviewPreflight` or in a small function of
  `cmd/issueflow` shared with `review approve` and `review start` (all three
  need "does this branch contain that commit").
- Whether `CheckPushed` keeps its own fetch now that the close fetches first.
- The order of the work, as long as the close E2E suite stays green at each
  step.
