# Review on the Feature Branch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove the `<ID>@review` branch: the reviewer works on the feature branch, and `issue close` merges a reviewed branch only when its tip is exactly the approved commit.

**Architecture:** The review chain gains one optional field (`branch` on the request op). Three small pure-ish helpers in `cmd/issueflow` answer the questions every command asks (which branch is reviewed, how the local branch stands against the remote one, which commit is approved). The review commands and `runClose` are rewired onto them, then everything that existed for `@review` (guards, hooks, incorporation) is deleted.

**Tech Stack:** Go (run through `mise exec -- go …`), cobra, huh forms via `tui.Form`, the git CLI through `git.Client`. Tests: real on-disk repositories, scripted prompters.

**Spec:** `docs/superpowers/specs/2026-11-23-review-on-feature-branch-design.md`. Read it first; this plan argues from it and does not repeat its tables.

## Global Constraints

- Every `Test*` wraps each assertion or scenario in a named `t.Run`.
- Seed a review with `reviewtest.Seed`, a tracked branch with `branchtest.Seed`; never write refs by hand. A git client caches its remote name: add the remote before the first seed.
- A check that can refuse runs before the first change to a branch, the index or HEAD (spec, anti-goals). Every refusal test asserts that the branch tip and HEAD did not move.
- Every refusal names the commit and the command that gets the user out. No message recommends `--no-verify`, or a forced reset without naming what it discards.
- Unknown op types stay skipped; ops written by older binaries (no `branch`, `approved_sha` naming a `@review` tip) must still fold.
- A step that waits on the remote is wrapped in `client.IO().Spin(msg)`; stop it before any form. Never keep a writer read from `IO()` across a `Spin` or its `stop`.
- Forms are built with `tui.Form`, never `huh.NewForm`.
- Before editing a function, run `impact` on it (GitNexus). The index is stale: Task 1 starts by rebuilding it. A `risk: UNKNOWN` is confirmed with a text search, never read as safe.
- **No `git commit`.** The user commits. Each task ends with a checkpoint: run GitNexus `detect_changes`, list the changed files, write the proposed message to `COMMIT_MSG` at the repository root (conventional subject, bulleted body, backticked identifiers), and stop.
- Documentation changes with the command it describes, in the same task (spec, "Documentation"). After any change to a cobra `Short`/`Long`, run `mise exec -- make docs-gen`.
- Full suite before each checkpoint: `mise exec -- go test ./...` with no failure.

## Review Focus

Conditions the spec implies and that are easy to miss. Each has its test in the task named.

1. **A clone with no remote, or a branch never pushed.** The remote check and `review approve`'s comparison must be silent no-ops, not errors: most E2E rigs have no origin. (Tasks 3, 7)
2. **An approval with an empty or unknown `approved_sha`** (hand-made or very old op). The close must refuse with the "fetch the branch first" / new-round wording, not panic or treat "" as an ancestor of everything. (Task 3)
3. **The reviewed branch is checked out in another worktree of the reviewer's clone.** `review start` must report git's refusal with nothing changed, and `review approve` must still read the branch tip (a ref, not HEAD). (Tasks 4, 5)
4. **The feature branch is the current branch with HEAD detached nowhere near it, or the reviewer sits on the reviewed branch already with staged changes.** `review start`'s clean-tree check must look at the tree that will be changed, and only when a fast-forward or reset is needed. (Task 4)
5. **A `branch` field naming a branch of another issue, or starting with `-`.** It must fold as absent and never reach `git checkout` as an argument. (Task 1)

---

### Task 1: The `branch` field of the request op

**Files:**
- Modify: `review/record.go` (`Op`, `DecodeOp`, `State`, `apply`)
- Test: `review/record_test.go`
- Modify: `website/docs/under-the-hood/review-refs.md`

**Interfaces:**
- Produces: `review.Op.Branch string` (`json:"branch,omitempty"`), `review.State.Branch string` (the branch of the current round's request, "" when absent or invalid).

- [ ] **Step 1: Rebuild the GitNexus index**

Run: `node .gitnexus/run.cjs analyze --index-only`
Expected: ends without error; `node .gitnexus/run.cjs impact apply --direction upstream --repo .` then answers.

- [ ] **Step 2: Write the failing table cases in `TestFold`**

Add cases, each asserting `State.Branch`:
- request with `Branch: "42@feat@thing"` on slug `42` → `"42@feat@thing"`.
- request without `Branch` → `""`.
- request with `Branch: "43@feat@other"` on slug `42` (another issue) → `""`.
- request with `Branch: "--upload-pack=x"` → `""`.
- round 2 request without `Branch` after a round 1 request with one → `""` (the field belongs to the round).

Add to the `DecodeOp` test: a payload whose `branch` holds `"42@feat@a\x1b[31mb"` decodes to a `Branch` with no control character.

- [ ] **Step 3: Run them**

Run: `mise exec -- go test ./review/... -run "TestFold|TestDecodeOp" -v`
Expected: FAIL, `Branch` undefined.

- [ ] **Step 4: Implement**

`DecodeOp` cleans `op.Branch` with `text.Line`. In `apply`, `OpRequest` sets `st.Branch` to `op.Branch` when `branch.Parse(op.Branch)` succeeds and its `IssueID()` equals `st.Slug`, else `""`. Check that `review` may import `branch` without a cycle (`mise exec -- go build ./...`); if it cannot, validate with a function passed into `Fold`'s package from the callers instead and say so at the checkpoint.

- [ ] **Step 5: Run the package, update `review-refs.md`** (the `branch` field, optional, ignored by older binaries)

Run: `mise exec -- go test ./review/... -v`
Expected: PASS.

- [ ] **Step 6: Checkpoint** (see Global Constraints)

---

### Task 2: `review request` records the picked branch

**Files:**
- Modify: `cmd/review/request.go`, `cmd/review/review.go` (the `Long` of the `review` root command: the round without `@review`, lock or hook)
- Test: `cmd/review/review_e2e_test.go`, `cmd/review/request_guard_e2e_test.go` (delete), `cmd/review/legacy_branch_e2e_test.go`
- Modify: `website/docs/workflows/review.md` (rewritten here, first; later tasks amend their own section)

**Interfaces:**
- Consumes: `review.Op.Branch`.
- Produces: `runReviewRequest(ctx context.Context, deps reviewDeps, issueSlug, featureBranch string) error`.

- [ ] **Step 1: Write the failing tests**

- `TestReviewRequest_RecordsPickedVariant`: issue `42` with two in-progress branches `42@feat@a` and `42@feat@a@v2`; the scripted prompter picks the second. Assert the request op's `Branch == "42@feat@a@v2"` and `FeatureSHA` is that branch's tip.
- `TestReviewRequest_ApprovedIsOfferedForNewRound`: a review seeded `approved`, `require-signed` false; the picker offers the branch; after the request `State.Round == 2`, `Status == in_review`.
- `TestReviewRequest_OutputHasNoLock`: stdout does not contain "locked".

- [ ] **Step 2: Run them** — `mise exec -- go test ./cmd/review/... -run "TestReviewRequest_" -v`. Expected: FAIL.

- [ ] **Step 3: Implement**

`runReviewRequestInteractive` passes `picked.BranchName`. `runReviewRequest` no longer searches `LocalInProgress` for the branch, no longer calls `PendingReviewCommits` / `ReviewBranchAhead`, no longer deletes a `@review` branch, and writes `Branch` in the op. The `locked` filter keeps only `in_review`. The legacy-blob replacement path stays. Rewrite the command's `Long` without the lock and `@review`.

- [ ] **Step 4: Delete the tests of the removed behaviour** (`request_guard_e2e_test.go`, the pending-commit cases of the request) and fix the callers of `runReviewRequest` in tests.

- [ ] **Step 5: Rewrite `website/docs/workflows/review.md`** per the spec's "Documentation" section: the diagram, the round from both sides, no lock. Leave a short "At close" and "Upgrading" section to be filled by Tasks 8 and 10.

- [ ] **Step 6: `mise exec -- make docs-gen && mise exec -- go test ./...`** — Expected: PASS.

- [ ] **Step 7: Checkpoint**

---

### Task 3: The three shared questions (`cmd/issueflow`)

**Files:**
- Create: `cmd/issueflow/reviewed.go`
- Create: `cmd/issueflow/reviewed_test.go`
- Modify: `git/git.go` (one method), `git/git_test.go`
- Modify: `cmd/review/deps.go` (`branchNameForIssue` moves out)

**Interfaces:**
- Produces:

```go
// git
func (c *Client) HasCommit(ctx context.Context, sha string) bool // cat-file -e <sha>^{commit}; false for ""

// issueflow
func BranchNameForIssue(ctx context.Context, c *git.Client, slug string) (string, error) // moved from cmd/review, same behaviour
func ReviewedBranch(ctx context.Context, c *git.Client, st *review.State) (string, error) // st.Branch, else BranchNameForIssue

type RemoteRelation int
const (
	RemoteNoRemote     RemoteRelation = iota // the clone has no remote
	RemoteNotPushed                          // a remote, but the branch is not on it
	RemoteLocalMissing                       // on the remote, no local branch
	RemoteSame
	RemoteLocalAhead
	RemoteBehind
	RemoteDiverged
)
// RemoteOf compares refs/heads/<branchName> with refs/remotes/<remote>/<branchName>.
// remoteRef is "<remote>/<branchName>" whenever the clone has a remote, even
// for RemoteNotPushed (messages name it); "" for RemoteNoRemote. It reads refs
// only: no fetch. A branch that is neither local nor on the remote is
// RemoteNotPushed.
func RemoteOf(ctx context.Context, c *git.Client, branchName string) (rel RemoteRelation, remoteRef string, err error)

var (
	ErrApprovalMissing      = errors.New("approved commit not in this clone")
	ErrApprovedAhead        = errors.New("approved commit is ahead of the local branch")
	ErrCommitsAfterApproval = errors.New("commits made after the approval")
	ErrNotApprovedBranch    = errors.New("local branch is not the approved one")
	ErrApprovalsDiverged    = errors.New("approvals name diverged commits")
)
// ApprovedCommit returns the approved_sha that contains every other one of
// the round. Several approvals may name it (two reviewers on one commit): the
// caller checks signatures over all of those. Its errors carry the message
// the user reads: ErrApprovalMissing ("fetch the branch first") when a SHA is
// empty or not in the clone, ErrApprovalsDiverged ("run git zf review request
// for a new round") when none contains the others.
func ApprovedCommit(ctx context.Context, c *git.Client, approvals []review.Approval) (approvedSHA string, err error)
// CheckApprovedTip applies the rule A table to branchName's local tip.
// nil means "T is A". Errors wrap the sentinels above with the message.
func CheckApprovedTip(ctx context.Context, c *git.Client, branchName, approvedSHA string) error
```

- [ ] **Step 1: Write the failing tests** (`reviewed_test.go`, one real repo per test)

- `TestRemoteOf`: table over no remote → `RemoteNoRemote`; remote but branch not pushed → `RemoteNotPushed`; on the remote with no local branch → `RemoteLocalMissing`; same; local ahead; behind; diverged. Assert `remoteRef`. The close and `review approve` treat the first two alike (nothing to compare); `review start` does not (no remote: check the local branch; not pushed: refuse).
- `TestReviewedBranch`: `State.Branch` set → that name; empty → the branch chain's newest entry.
- `TestApprovedCommit`: one approval → its SHA; two on the same commit → that SHA; two, the second containing the first → the second's, whatever their order in the slice; two diverged → `ErrApprovalsDiverged`; an `ApprovedSHA` of `""` → `ErrApprovalMissing`; a SHA absent from the clone → `ErrApprovalMissing`; no approval → `ErrApprovalMissing`.
- `TestCheckApprovedTip`: one case per row of the spec's rule A table, asserting `errors.Is` on the sentinel and that the message contains the first 10 characters of `A`; the "not in the clone" case uses a well-formed SHA no object has.
- `TestHasCommit` in `git`: a commit → true; a tree SHA → false; `""` → false; garbage → false.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/issueflow/... ./git/... -run "TestRemoteOf|TestReviewedBranch|TestApprovedCommit|TestCheckApprovedTip|TestHasCommit" -v`. Expected: FAIL (undefined).

- [ ] **Step 3: Implement.** `CheckApprovedTip` tests `HasCommit` first, then equality, then the two `IsAncestor` directions; an `IsAncestor` error is returned, never dropped. Use GitNexus `rename`-aware moves for `branchNameForIssue` → `issueflow.BranchNameForIssue` (callers in `cmd/review`).

- [ ] **Step 4: Run the same command, then `mise exec -- go test ./...`.** Expected: PASS.

- [ ] **Step 5: Checkpoint**

---

### Task 4: `review start` on the feature branch

**Files:**
- Modify: `cmd/review/start.go`
- Test: `cmd/review/review_e2e_test.go` (start cases), `cmd/review/chain_e2e_test.go`

**Interfaces:**
- Consumes: `issueflow.ReviewedBranch`, `issueflow.RemoteOf`, `git.Client.HasCommit`, `IsAncestor`, `IsDirty`, `FastForwardOnly`, `ResetHard`.
- Produces: `runReviewStart(ctx, deps, issueSlug) error`, same signature; after it the reviewed branch is checked out.

- [ ] **Step 1: Write the failing tests** (two clones of one bare origin: developer and reviewer)

- `TestReviewStart_CreatesLocalBranchFromRemote`: reviewer has no local branch → HEAD is the feature branch at the remote tip; no `<ID>@review` exists; a `start` op names the reviewer.
- `TestReviewStart_FastForwardsWhenBehind`.
- `TestReviewStart_ResetsStaleCopy`: reviewer's local branch is an old commit the developer rebased away → tip is the remote tip; stdout contains the old tip's short SHA; `git cat-file -e <old>` still succeeds.
- `TestReviewStart_StaleCopyDirtyTreeRefuses`: same with an uncommitted change → error; HEAD and the branch tip unchanged; no `start` op.
- `TestReviewStart_SubmittedCommitNotOnRemoteRefuses`: developer requested without pushing → error naming `feature_sha`'s short form; reviewer's local branch (an older one) unchanged; HEAD unchanged.
- `TestReviewStart_KeepsUnpushedReviewerCommits`: started, reviewer commits, start again → tip unchanged, one `start` op only.
- `TestReviewStart_UnpushedReviewerCommitsAndRemoteMoved`: tip unchanged; stdout names `git merge <remote>/<branch>`.
- `TestReviewStart_BranchHeldByAnotherWorktree` (Review Focus 3): error from git's checkout; nothing changed.
- `TestReviewStart_OnTheBranchWithStagedChange_NothingNeeded` (Review Focus 4): reviewer already on the branch at the remote tip with a staged file → succeeds, staged file intact.
- `TestReviewStart_NoRemote`: a clone without remote whose local branch contains `feature_sha` → checked out, started.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/review/... -run "TestReviewStart_" -v`. Expected: FAIL.

- [ ] **Step 3: Implement the spec's five steps in order** (fetch; checks on refs; checkout; apply; start op). Work out "what the local branch needs" as one value (`none`, `create`, `fastForward`, `reset`, `warnDiverged`) computed before any change, from the spec's table. The dirty check runs only for `fastForward` and `reset`. "Already started by this user" skips the start op. Rewrite `Short`/`Long`.

- [ ] **Step 4: Update the existing start tests** that expect `<ID>@review` (`TestReviewStart_FetchesCommitObjects`, `TestReviewStart_RecordsReviewer`, `TestReviewStart_WarnsWhenReviewerCannotBeRecorded`: the last two run without an origin, the `RemoteNoRemote` path); in `review.md`, the reviewer's side of the round.

`TestFullParallelReviewScenario` (`review_e2e_test.go`) checks out `X.1@review` / `X.2@review` and fetches `X.2@review` after `runReviewStart`. In this task its checkouts and its fetch move to the feature branches. `runReviewApprove` / `runReviewReject` still look for `<ID>@review` until Task 5 and, finding none, record `has_commits = false` and approve `feature_sha`: the scenario's assertions on reviewer commits are marked with a `// Task 5` comment and skipped with `t.Skip` inside their own `t.Run`, and Task 5 restores them. Nothing else may be skipped.

- [ ] **Step 5: `mise exec -- make docs-gen && mise exec -- go test ./...`.** Expected: PASS. (The close still handles a review with no `@review` branch: nothing pending.)

- [ ] **Step 6: Checkpoint**

---

### Task 5: `review approve`, `review reject`, the review rows

**Files:**
- Modify: `cmd/review/deps.go` (`recordReviewDecision`, `reviewDecision`, `reviewBranches`), `cmd/review/approve.go`, `cmd/review/reject.go`, `cmd/review/status_cmd.go`
- Test: `cmd/review/review_e2e_test.go`, `cmd/review/reject_reason_e2e_test.go`, `cmd/review/chain_e2e_test.go`

**Interfaces:**
- Consumes: `issueflow.ReviewedBranch`, `RemoteOf`.
- Produces: `reviewDecision{round int; featureBranch string; hasCommits bool}` (the `reviewBranch` and `branchExists` fields go).

- [ ] **Step 1: Write the failing tests**

- `TestReviewApprove_RecordsFeatureTip`: reviewer commits on the feature branch, approves → `ApprovedSHA` is the feature tip, `HasCommits` true; without a commit → `ApprovedSHA == feature_sha`, `HasCommits` false.
- `TestReviewApprove_MissingLocalBranchRefuses`, `TestReviewApprove_StaleLocalBranchRefuses`: error contains `git zf review start`; no approve op written.
- `TestReviewApprove_RemoteAheadRefuses`: the developer pushes after the reviewer's start; the reviewer has **not** fetched → approve refuses (it fetches itself); no op written.
- `TestReviewApprove_NoRemote` (Review Focus 1): approves.
- `TestReviewApprove_BranchInAnotherWorktree` (Review Focus 3): `ApprovedSHA` is that branch's tip.
- `TestReviewReject_WithReviewerCommits`: `HasCommits` true; stdout contains `git log <feature_sha short>..<branch>`. `_WithoutCommits`: false. `_StaleLocalBranch`: false, and the push proposal is not made (scripted push confirm not called).
- `TestReviewBranches_RowCarriesReviewedBranch`: the row's `BranchName` is the feature branch, for a request with `Branch` and for one without.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/review/... -run "TestReviewApprove_|TestReviewReject_|TestReviewBranches_" -v`. Expected: FAIL.

- [ ] **Step 3: Implement.** `has_commits` = `feature_sha` is a strict ancestor of the local tip. Approve: best-effort `client.Fetch` (warning on failure) under a `Spin`, then the three refusals, then the op. Reject needs no local branch. Both propose the push of the feature branch when `hasCommits`. `reviewBranches` uses `st.Branch`, else the branch chain already loaded by `issueTitles`; drop the per-issue `branch.Load` loop of `status_cmd.go` if that makes it redundant. Rewrite the `Long` of both commands.

- [ ] **Step 4: Remove the `@review` cleanup tests of reject; update `review.md`** (decision section). Replace `TestReviewApprove_RecordsApprovedSHA` (`chain_e2e_test.go`, its second sub-test creates `77@review`) by `TestReviewApprove_RecordsFeatureTip`. Restore the assertions of `TestFullParallelReviewScenario` skipped in Task 4: `grep -n "Task 5" cmd/review/*_test.go` must return nothing.

- [ ] **Step 5: `mise exec -- make docs-gen && mise exec -- go test ./...`.** Expected: PASS.

- [ ] **Step 6: Checkpoint**

---

### Task 6: `review sync` and `review track` lose their review-branch halves

**Files:**
- Modify: `cmd/review/sync.go`, `cmd/review/track.go`, `cmd/review/prompter.go`
- Test: `cmd/review/sync_e2e_test.go`, `cmd/review/review_e2e_test.go` (track), `cmd/review/prompter_test.go`
- Modify: `website/docs/workflows/sub-tasks.md`, `review.md`

**Interfaces:**
- Produces: `runReviewSync(ctx, deps, issueSlug, childBranch string) error`; `ReviewPrompter` without `Confirm`.

- [ ] **Step 1: Write the failing tests**

- `TestReviewSync_ParentOnly`: a sub-task behind its parent is merged; a branch without parent is not a candidate ("Nothing to sync.").
- `TestReviewSync_UsesPickedVariant`: two variants of a sub-task, the second picked → the second is merged into.
- `TestReviewTrack_ReviewBranchNameRefused`: on a branch named `42@review`, `ErrNotTrackable`; its message has no `@review`.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/review/... -run "TestReviewSync_|TestReviewTrack_" -v`. Expected: FAIL.

- [ ] **Step 3: Implement.** Delete `mergeReviewerCommits` and step 1 of `runReviewSync`; candidates are the branches with a parent. Delete the reviewer half of `track.go`, the "currently locked" note, and reword `ErrNotTrackable` and the help. Delete `ReviewPrompter.Confirm` and its implementations (text-search its callers first).

- [ ] **Step 4: Delete the pending-commit sync tests; update the two doc pages.** Other callers of `runReviewSync` to fix: `legacy_branch_e2e_test.go`, `review_e2e_test.go`. Reviewer-track tests to delete: `TestTrack_Reviewer_RegistersReviewBranch`, `TestTrack_Reviewer_ErrorWhenNoRefExists`, `TestTrack_Reviewer_ClosedReviewIsNoOpenReview` (`chain_e2e_test.go`), and the "a review name without a review" sub-test of `TestTrackBranch`.

- [ ] **Step 5: `mise exec -- make docs-gen && mise exec -- go test ./...`.** Expected: PASS.

- [ ] **Step 6: Checkpoint**

---

### Task 7: The close sequence and the remote branch check

**Files:**
- Modify: `cmd/issue/close.go` (`runClose`), `cmd/issue/close_prompter.go`, the scripted prompter in `cmd/issue/close_prompter_test.go`
- Test: `cmd/issue/close_e2e_test.go`, `cmd/issue/close_pr_e2e_test.go`
- Modify: `website/docs/workflows/close-an-issue.md`

**Interfaces:**
- Consumes: `issueflow.RemoteOf`, `MaterializeBranch`, `mergeflow.SourceTree`, `git.Client.Fetch`, `MergeDryRun`, `FastForwardOnly`, `MergeForward`, `AbortMerge`, `IsDirty`.
- Produces:

```go
// RemoteOffer is what the close asks before taking the remote's commits.
type RemoteOffer struct {
	Branch, RemoteRef   string
	LocalTip, RemoteTip string // short SHAs
	Commits             int    // commits the remote holds and the local branch lacks
	Merge               bool   // true: a merge (diverged), default answer no; false: a fast-forward
}
// ErrCannotAsk is returned by ConfirmTakeRemote when stdin is not a terminal.
var ErrCannotAsk = errors.New("no terminal to ask on")
// ErrRemoteMoved is returned by runClose when it stopped at the remote check.
var ErrRemoteMoved = errors.New("the remote branch moved")

// in ClosePrompter:
ConfirmTakeRemote(ctx context.Context, offer RemoteOffer) (take bool, err error)
```

- [ ] **Step 1: Write the failing tests** (a rig with an origin; `newCloseRig` variants exist for worktrees and origins — reuse them)

Each asserts the base branch tip is unchanged and `errors.Is(err, ErrRemoteMoved)` unless said otherwise.
- `TestClose_Remote_BehindYes`: no review; fast-forwarded to the remote tip; stdout has `git log <old>..<branch>` and "run the close again"; a second `runClose` closes.
- `TestClose_Remote_YesInstructions`: with an approved review, behind and landing on the approved commit → stdout says to run the close again; behind and landing past it → stdout names `git zf review request`; diverged and merged → the same.
- `TestClose_Remote_BehindNo`: nothing moved; stdout has `git merge --ff-only <remote>/<branch>`.
- `TestClose_Remote_DivergedCleanYes`: a merge commit on the feature branch, kept; `offer.Merge` was true.
- `TestClose_Remote_DivergedNo`: nothing moved.
- `TestClose_Remote_DivergedConflict`: prompter not called; error names the conflicting file; no `MERGE_HEAD`.
- `TestClose_Remote_DirtyTree`: two sub-cases (behind, diverged), yes answered → refused, nothing moved.
- `TestClose_Remote_CannotAsk`: prompter returns `ErrCannotAsk` → as "no".
- `TestClose_Remote_Unreachable`: origin URL points nowhere → warning on stderr, the close completes (`err == nil`).
- `TestClose_Remote_NoRemote` and `TestClose_Remote_NeverPushed` (Review Focus 1): prompter not called, the close completes.
- `TestClose_Remote_LocalAhead`: prompter not called, the close completes.
- `TestClose_Remote_MaterializedAtFreshTip`: the branch exists only on origin and origin moved since this clone's last fetch → no question, the merged commit is the fresh tip.
- `TestClose_Remote_Worktree`: the branch is in a linked worktree and behind, yes → the fast-forward happened in that worktree.
- In `close_pr_e2e_test.go`, rewrite `TestClose_PullRequest_BehindRefused` → `TestClose_PullRequest_BehindAsks`: the question is asked before any forge call (`RecordedMerges` empty).

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/issue/... -run "TestClose_Remote_|TestClose_PullRequest_BehindAsks" -v`. Expected: FAIL.

- [ ] **Step 3: Implement.** Reorder `runClose` to the spec's eight steps (fetch → materialize → source tree → remote check → preflight → pull request → target → merge). The remote check is one function `checkRemoteBranch(ctx, deps, tree *git.Client, picked *branch.Row, prompter ClosePrompter) error`; it stops the spinner before asking. After a yes it reads the review of `picked.IssueSlug` (best effort) to choose the instruction it prints, per the spec: the new tip is the approved commit → "run the close again"; an approved review and a tip past it → "run `git zf review request` for a new round"; no open review, or one about another branch → "look at what came in, then run the close again". The production `ConfirmTakeRemote` returns `ErrCannotAsk` when stdin is not a terminal (use the helper the other no-TTY paths of the repo use; text-search `IsTerminal`), and its confirm defaults to no when `offer.Merge`. The scripted prompter records the offers it was shown. Keep `reviewPreflight` as it is in this task.

- [ ] **Step 4: Update `close-an-issue.md`** (the remote check, the question, why the close then stops) and the tables' callers if a returned `ErrRemoteMoved` needs no special case (`listAction`, `afterAction`: confirm by reading them that a non-nil error pauses and reopens).

- [ ] **Step 5: `mise exec -- go test ./...`.** Expected: PASS.

- [ ] **Step 6: Checkpoint**

---

### Task 8: Rule A at close

**Files:**
- Modify: `cmd/issue/close.go` (`reviewPreflight`, `checkSignedApproval`, the pull request block), `cmd/mergeflow/forge.go`
- Test: `cmd/issue/close_e2e_test.go`, `cmd/issue/close_pr_e2e_test.go`, `cmd/mergeflow/forge_test.go`
- Modify: `website/docs/workflows/close-an-issue.md`, `review.md` ("At close"), `under-the-hood/merge-engine.md`

**Interfaces:**
- Consumes: `issueflow.ReviewedBranch`, `ApprovedCommit`, `CheckApprovedTip` and their sentinels.
- Produces: `reviewPreflight(ctx, deps, picked, src) (cleanup func(context.Context), err error)` unchanged in signature; `mergeflow.ForgeMerge` without `PushFirst`.

- [ ] **Step 1: Write the failing tests**

- `TestClose_RuleA`: one sub-test per row of the spec's table, each with `errors.Is` on the `issueflow` sentinel and the feature tip unmoved; the "T is A" row closes and the review is marked closed.
- `TestClose_RuleA_Incident`: local branch = an old commit adding `old-migration.txt`; origin = a rebased branch adding `renamed-migration.txt`, approved; remote check answered no in one sub-test (stopped), and in the other the local branch is reset by hand to `A` and the close merges only `renamed-migration.txt` into the base.
- `TestClose_RuleA_TwoApprovals`: contained (tip on the first → `ErrApprovedAhead`; on the second → closes), diverged → `ErrApprovalsDiverged`.
- `TestClose_RuleA_EmptyApprovedSHA` (Review Focus 2): `ErrApprovalMissing`, no panic.
- `TestClose_RuleA_TwoStep`: the two-run cases of the spec, now that rule A is active. Remote moved past the approval, first close answers yes (fast-forwarded, `ErrRemoteMoved`), second close → `ErrCommitsAfterApproval`. Diverged without conflict, yes (merged, stopped), second close → `ErrCommitsAfterApproval`, the merge commit still on the branch. Developer behind the reviewer's approved commits, yes, second close → closes.
- The signed gate. These stay, re-seeded so the approval names the feature tip: `TestClose_SignedGate_UnsignedApprovalRefused`, `_RefusalWayOutWorks`, `_SignedApprovalPasses`, `_FlagOffIgnoresSignatures`, `_ThroughRunClose`, `TestClose_Worktree_SignedGateRefusesUnsignedApproval`. New: `TestClose_SignedGate_UnsignedContainsSigned` (a signed approval on a commit, an unsigned one on a descendant: `A` is the descendant, refused with `ErrApprovalNotSigned`) and `TestClose_SignedGate_TwoApprovalsSameCommit` (one unsigned, one signed, both on `A`: closes).
- `TestClose_Variant_NotGatedByReview`: variant B closed while A's review is `in_review`, `changes_requested`, `approved` → closes each time; A's review still open with the same status.
- `close_pr_e2e_test.go`: replace the three `TestClose_PullRequest_ReviewedBranch*` by `TestClose_PullRequest_ApprovedTipMergedByForge` (one recorded merge, review closed) and `TestClose_PullRequest_ApprovedWithUnpushedCommitRefused` (rule A refuses before the forge).
- `forge_test.go`: delete the `PushFirst` sub-test.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/issue/... -run "TestClose_RuleA|TestClose_SignedGate|TestClose_Variant_|TestClose_PullRequest_" -v`. Expected: FAIL.

- [ ] **Step 3: Implement.** In `reviewPreflight`: resolve the reviewed branch and return `(nil, nil)` when `picked.BranchName` differs, before the switch on the status. `approved`: `ApprovedCommit`; when `require-signed`, at least one approval of the round whose `ApprovedSHA` is that commit has `SignatureState == SigVerified` (`checkSignedApproval` reduced to that); then `CheckApprovedTip`. Delete the incorporation, `ErrFeatureBranchStale`, `ErrReviewBranchRewritten`, `ErrReviewSyncNeeded` (text-search its users), `localExists` / `remoteTrackingExists`; the cleanup writes and pushes the close op only. Delete `PushFirst`, `tipBefore`, `tipAfter`. Reword the `in_review` message (no "locked"); keep `ErrBranchLockedForReview`'s identity unless nothing else needs the name.

- [ ] **Step 4: Delete the tests of the incorporation** (`TestClose_ReviewPreflight_Diverged*`, `_IncorporatesRemoteOnlyReviewerCommits`, `TestClose_ReviewerInitiated*` cases that rely on `@review`, `TestClose_Worktree_IncorporatesReviewerCommits`, `TestClose_Worktree_MergesDivergedReviewerCommits`, the helper `seedApprovedReviewBranch`, the two tests added on 2026-11-23, and the signed-gate tests that create `<slug>@review` or expect `ErrApprovalNotSigned` for a commit mismatch rule A now names itself: `TestClose_SignedGate_UncoveredVerifiedApprovalHintsFetch`, `_CommitAfterApprovalRefused`, `_SecondApprovalCoversReviewerCommits`, `_DivergedBranchRefusedUntouched`, `_ReviewerCommitsNeedTheirOwnApproval`, `_CommitsAfterReviewerApprovalRefused`, `_RemoteTrackingReviewBranch`) and re-seed the survivors so their approval names the feature tip.

- [ ] **Step 5: Update the three doc pages** (each refusal with its way out).

- [ ] **Step 6: `mise exec -- go test ./...`.** Expected: PASS.

- [ ] **Step 7: Checkpoint**

---

### Task 9: Delete what existed for `@review`

**Files:**
- Delete: `branch/review.go`, `branch/review_test.go`, `cmd/issueflow/review_guard.go` and its test, `cmd/commit/review_guard.go`, `cmd/commit/review_guard_test.go`, `cmd/review/guard_commit_test.go`
- Modify: `cmd/review/guard.go`, `cmd/review/guard_commit.go` (bodies), `cmd/commit/commit.go`, `cmd/home_data.go`, `cmd/issue/close.go:499`, `cmd/branch/merge.go`, `git/merge.go`
- Test: `cmd/review/review_e2e_test.go`, `cmd/home_data_test.go`, `cmd/commit/commit_test.go`, `git/merge_test.go`
- Modify: `website/docs/workflows/commit.md`, `concepts/issues-and-branches.md`, `under-the-hood/branch-refs.md`, `index.md`

**Interfaces:**
- Produces: `review guard <branch>` and `review guard-commit` exit 0 and print nothing, whatever the review state.

- [ ] **Step 1: Write the failing tests**

- `TestReviewGuard_IsANoOp`: on a branch whose review is `in_review`, `runReviewGuard` returns nil.
- `TestReviewGuardCommit_IsANoOp`.
- `TestGatherHome_ReviewNamedBranchIsOrdinary`: HEAD on `42@review` is not reported as issue `42`'s review.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/... -run "IsANoOp|ReviewNamedBranchIsOrdinary" -v`. Expected: FAIL.

- [ ] **Step 3: Implement the deletions.** Before deleting `cmd/review/guard_commit_test.go`, move its helper `mustRunGit` to `cmd/review/review_e2e_test.go`: `legacy_branch_e2e_test.go` and `sync_e2e_test.go` call it. Remove the "review branches excluded" case of `cmd/issue/close_test.go` with the `IsReviewBranch` filter of `mergeTargetCandidates`. In `cmd/home_data_test.go`, the "a review branch" sub-test is the only cover of `d.Review == "in review"`, the `1 in review` hint and "a closed review is ignored": keep those assertions, on a checkout of the feature branch `77@feat@x`. The two guard commands keep their cobra definition (hidden, same args) with a body that returns nil. `git zf commit` no longer calls `guardPendingReview`. `branchIssue`, `hintFromBranchName` and `currentIssueSlug` lose their `@review` branch. `cmd/branch/merge.go`'s refusal no longer says "review incorporation". Delete `git.ErrMergeConflicts` and `MergeLeaveConflicts` only after a text search shows no caller.

- [ ] **Step 4: Verify nothing is left**

Run: `grep -rn "@review\|ReviewBranchName\|IsReviewBranch\|CutReviewSuffix\|PendingReview" --include=*.go . | grep -v "_test.go"`
Expected: only the retired hook scripts of `cmd/init/init.go` (Task 10 owns them) and comments that Task 10's upgrade note needs. Anything else is fixed here.

- [ ] **Step 5: Update the four doc pages; `mise exec -- make docs-gen && mise exec -- go test ./...`.** Expected: PASS.

- [ ] **Step 6: Checkpoint**

---

### Task 10: `git zf init` retires the hooks

**Files:**
- Modify: `cmd/init/init.go`
- Test: `cmd/init/init_test.go`
- Modify: `website/docs/getting-started/install.md`, `first-steps.md`, `README.md`, `review.md` ("Upgrading"), `CLAUDE.md`

**Interfaces:**
- Produces: `retiredHooks []hookSpec` (name + the exact script once installed); `managedHooks` is gone.

- [ ] **Step 1: Write the failing tests**

- `TestInit_InstallsNoHook`: fresh repository → no `pre-push`, `pre-commit`, `pre-rebase` file; the fetch refspecs are configured.
- `TestInit_DeletesRetiredHooks`: each of the three scripts written byte-identical beforehand → file gone, stdout names it.
- `TestInit_KeepsForeignHook`: a `pre-commit` with other content → untouched; when it contains `git zf review guard`, stdout says the call can be removed.
- Delete `TestInit_InstallsBothHooks`, `TestInit_Idempotent`'s hook assertion, `TestInit_PreservesForeignPreCommit`, `TestInit_PreRebaseRefusesReviewBranch`.
- `TestInit_LinkedWorktree_InstallsHooksInCommonDir` and `TestInit_HooksPath` assert an installation today: rewrite them to assert that a retired hook is deleted from the common git dir of a linked worktree, and from the directory `core.hooksPath` names.

- [ ] **Step 2: Run** — `mise exec -- go test ./cmd/init/... -v`. Expected: FAIL.

- [ ] **Step 3: Implement.** Keep the three script constants verbatim (they are what the comparison needs). Rewrite `Short`/`Long`.

- [ ] **Step 4: Documentation.** `install.md`, `first-steps.md`, `README.md`: what `init` still does. `review.md` "Upgrading": every clone upgrades, runs `git zf init` again, and an open review with a `@review` branch is finished with `git merge --ff-only <remote>/<ID>@review` then `git zf issue close`, or by a new round. `CLAUDE.md`: rewrite "Testing the review chains" and every mention of the pre-push and pre-commit hooks, and add the test commands of Tasks 3 to 8.

- [ ] **Step 5: Verify the documentation's done-condition**

Run: `mise exec -- make docs-gen && mise exec -- go test ./...`
Expected: PASS, `TestGenerate_MatchesCommitted` included.
Run, after the generation so the command pages are checked too: `grep -rnEi "@review|pre-push|pre-commit hook|locked|unlock" website/docs README.md`
Expected: only lines of the "Upgrading" note. A hit under `reference/commands` is fixed in the command's `Long`, then regenerated.

- [ ] **Step 6: Checkpoint**

---

### Task 11: End-to-end check by hand and final review

**Files:** none changed unless a defect is found.

- [ ] **Step 1: Build** — `mise exec -- make`. Expected: `./bin/git-zf` built.

- [ ] **Step 2: Replay the incident in a scratch directory** (two clones of one bare repository, under the session's scratchpad, never under the project): developer creates a branch, pushes, the "reviewer" clone keeps a copy; developer rebases with a renamed file and force-pushes, requests a review; reviewer runs `review start` (expect: old copy reset, old SHA printed), approves, closes. Expected: the base holds the renamed file only.

- [ ] **Step 3: Replay "developer closes after reviewer commits"**: first close asks, takes the commits and stops; second close merges.

- [ ] **Step 4: Dispatch one fresh reviewer** on the whole diff against the spec (the user's chosen execution style: one review at the end). Fix what it confirms.

- [ ] **Step 5: Final checkpoint**: full suite, GitNexus `detect_changes --scope compare --base-ref master`, list of changed files, `COMMIT_MSG`.
