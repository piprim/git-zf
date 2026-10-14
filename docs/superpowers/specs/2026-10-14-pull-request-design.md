# Design: pull requests on the forge

**Date:** 2026-10-14
**Status:** draft, for review

## Overview

GitHub and Forgejo host both the issues and the code. Their workflow merges
a branch through a pull request: opened once per branch, usually at its
first push; updated by every later push; checked by CI and reviewed on the
forge; merged by the forge, which closes the linked issue and deletes the
branch. git-zf has no pull request today: `issue close` and `branch merge`
merge locally, then offer to push the base.

This spec gives git-zf the pull request as the forge does it:

- a pull request is offered once per branch, at the push step of `git zf
  commit`, and kept being offered at each push until one exists;
- `git zf branch pr [branch]` opens one on demand, for a branch pushed with
  plain git;
- `issue close` and `branch merge` merge a branch that has an open pull
  request through the forge, with the strategy the operator picks, instead
  of locally.

**Decisions:**

- The forge is the issue tracker. The calls live behind an optional tracker
  capability, discovered by type assertion like `SingleLabel`. GitHub and
  Forgejo implement it; Redmine does not. The repo is the configured
  project, the token is the tracker's. "Issues on Redmine, code on GitHub"
  is out of scope: it would implement the same capability from another
  config section.
- The offer is at the push, not at the commit: a pull request has one
  answer per branch, and the push is the moment the branch reaches the
  forge. A declined offer comes back at the next push; the forge is the
  only state ("is there an open pull request for this head?"), no local
  "they said no" is recorded.
- At the push step the offer is one yes/no question that names the branch
  and the base; yes creates the pull request from the generated title and
  body, no form (edits are made on the forge). `branch pr` has the form:
  title, body, base, all editable. No draft toggle: a draft cannot be merged by close, and GitHub's REST API
  cannot turn a draft into a ready pull request.
- With an open pull request, close merges on the forge (option 1 of the
  brainstorm). A local merge pushed afterwards would leave a squashed or
  rebased pull request "closed without merge". Without a pull request, or
  without the capability, close is unchanged.
- The in-repo review gate (`reviewPreflight`) still runs before a forge
  merge: the forge's reviews are the forge's gate, git-zf's signed reviews
  stay git-zf's.

## Section 1: the tracker capability (`tracker/tracker.go`)

```go
// PullRequest is one pull request of the forge, as it names things.
type PullRequest struct {
    Number string // the forge's number, "12"
    URL    string
    Head   string // branch name
    Base   string
}

// MergeMethod is how the forge merges a pull request.
type MergeMethod string

const (
    MergeMethodMerge  MergeMethod = "merge"
    MergeMethodSquash MergeMethod = "squash"
    MergeMethodRebase MergeMethod = "rebase"
)

// ErrNoPullRequest is returned by FindPullRequest when the branch has no open
// pull request.
var ErrNoPullRequest = errors.New("tracker: no open pull request")

// PullRequests is the optional capability of a tracker that is also the
// forge hosting the repository: GitHub and Forgejo.
type PullRequests interface {
    // FindPullRequest returns the open pull request whose head is branch.
    FindPullRequest(ctx context.Context, branch string) (PullRequest, error)
    // CreatePullRequest opens one and returns it with its number and URL.
    CreatePullRequest(ctx context.Context, head, base, title, body string) (PullRequest, error)
    // MergePullRequest merges number with method. A refusal of the forge
    // (checks failing, not mergeable) is an error carrying its message.
    MergePullRequest(ctx context.Context, number string, method MergeMethod) error
}

// PullRequestsOf returns t's capability, or nil when the tracker has none
// (or t is nil).
func PullRequestsOf(t Tracker) PullRequests
```

`MergeMethod` is a tracker type, not `commit.MergeStrategy`, so the tracker
package keeps its imports. The callers map classic → merge, squash →
squash, rebase → rebase (`mergeflow.methodOf`).

`PullRequest.Clean()` drops the control characters of `URL`, `Head` and
`Base`, as `Issue.Clean` does; every adapter returns through it.

### GitHub (`tracker/github`)

go-github, on `ownerRepo()`:

- `FindPullRequest`: `PullRequests.List` with `State: "open"`, `Head:
  owner + ":" + branch`, first page; the first result, else
  `ErrNoPullRequest`.
- `CreatePullRequest`: `PullRequests.Create` with `Head`, `Base`, `Title`,
  `Body`.
- `MergePullRequest`: `PullRequests.Merge` with `MergeMethod: string(method)`.
  A 405 (not mergeable) or 409 (head changed) is returned with the API's
  message.

### Forgejo (`tracker/forgejo`)

The existing `doJSON` client, on `ownerRepo()`:

- `FindPullRequest`: `GET /repos/{owner}/{repo}/pulls?state=open` through
  `listPages`, filtered client-side on `head.ref == branch` (the API has no
  head filter). `ErrNoPullRequest` when no page has it.
- `CreatePullRequest`: `POST /repos/{owner}/{repo}/pulls` with `head`,
  `base`, `title`, `body`.
- `MergePullRequest`: `POST /repos/{owner}/{repo}/pulls/{index}/merge` with
  `{"Do": method}`. A 405 is returned with the API's message.

The wire struct `pullRequest` (number, html_url, head.ref, base.ref)
extends the `pullRequestRef` marker already used to drop pull requests
from the issue listing.

### Redmine

Nothing. `PullRequestsOf` returns nil.

### Fake (`tracker/fake`)

```go
PullRequests        []tracker.PullRequest // the open ones; FindPullRequest matches Head
RecordedMerges      []Merge               // {Number, Method}, appended by MergePullRequest
MergeError          error                 // MergePullRequest returns it when set: a forge refusal
```

`CreatePullRequest` appends with the next number and a `fake://pr/<n>` URL.
`MergePullRequest` removes the pull request from `PullRequests` and
records the merge. A test that needs a tracker without the capability
wraps the fake: `struct{ tracker.Tracker }{fake}` hides the methods.

## Section 2: opening a pull request

### The core (`cmd/branch/pr.go`)

```go
// PullRequestForm is the form of branch pr: the three fields, prefilled,
// and the answer. Esc declines (huh.ErrUserAborted).
type PullRequestForm func(ctx context.Context, draft *PullRequestDraft) (confirmed bool, err error)

type PullRequestDraft struct{ Title, Body, Base string }

// OpenPullRequest looks the branch up on the forge and, when it has no
// open pull request, runs form and creates one. It prints the URL in both
// cases. It is the step behind branch pr and behind the push proposal.
func OpenPullRequest(ctx context.Context, client *git.Client, forge tracker.PullRequests,
    branchName string, draft PullRequestDraft, form PullRequestForm) error
```

Prefill (`pullRequestDraft(ctx, client, cfg, branchName)`):

- `Base`: the parent branch recorded at start (`issueflow.ResolveParentBranch`
  over the branch chain), else `cfg.Branch.Base`.
- `Title`: the branch chain's issue title, else the branch name.
- `Body`: `Closes #N` when the issue has a tracker number
  (`issue.TrackerNumber`), else empty.

The form is a `tui.Form` (`tui.PullRequestForm`): three inputs and a
confirm whose default is yes.

### `git zf branch pr [branch]`

Registered next to `branch new`, `merge`, `prune`, `close`, and added to
the branch menu. Without an argument it takes the branch checked out in
the tree the command was typed in.

1. No tracker, or no capability: `error: the issue tracker <type> has no
   pull requests` and exit.
2. The branch must be on the forge: when `PushDryRun` reports something to
   push, the push proposal runs (`pushflow.Propose`, same flags as commit:
   `--push`, `--no-push`, `-y`). A declined push ends the command.
3. `OpenPullRequest` with the prefill and the huh form.

Non-interactive (`-y`, no TTY): the command refuses with `pull request
needs a title: run without -y`. No flags for the fields in this version.

### The push step of `git zf commit` (`cmd/pushflow`)

`pushflow.Opts` gains:

```go
// PullRequest, when set, offers a pull request after a successful push.
PullRequest *PullRequestOffer

type PullRequestOffer struct {
    // Open runs the lookup, the form and the create; nil ⇒ no offer.
    Open func(ctx context.Context) error
}
```

`Propose` calls `Open` right after `PushBranch` succeeds, and only then:
nothing to push, declined push, non-interactive run, skip: no offer. An
error from `Open` is a warning on stderr, never an error: the push already
happened.

`git zf commit` wires it: when `cfg.IssueTracker.Type != ""` it builds the
tracker as close does (a failing init is a warning), takes
`PullRequestsOf`, and when non-nil passes `Open` as
`branch.OpenPullRequest` on the current branch with the prefill and a
`PullRequestForm` that is one yes/no question, `Open a pull request for
"<branch>" against "<base>"?` (a confirm group like `tui.IssueConfirm`,
default yes), and never edits the draft. Yes creates the pull request from
the generated title and body; edits are made on the forge. A branch equal
to `cfg.Branch.Base` never gets an offer. The close push (the base) and
the review pushes pass no offer.

The commit package imports `cmd/branch` for the core; `cmd/branch` does
not import `cmd/commit` (checked: it imports `issueflow`, `mergeflow`,
`pushflow` and `cmdutil` only, so there is no cycle).

## Section 3: merging through the forge

### The engine (`cmd/mergeflow`)

```go
// ForgeMerge asks the forge to merge an open pull request of Source
// instead of merging locally.
type ForgeMerge struct {
    Forge  tracker.PullRequests
    Number string
}

// Params gains:
PullRequest *ForgeMerge // nil ⇒ local merge, as today

// Result gains:
ViaForge bool
```

With `PullRequest` set, `Run` keeps the dry-run, `PickStrategy` and
`ConfirmMerge`, then:

1. `Forge.MergePullRequest(Number, methodOf(strategy))`. An error is
   returned as is: nothing changed locally.
2. `client.Fetch`, then `FastForwardOnly(<remote>/<target>, <target>)` so
   the local base is the merged one. A local target that cannot be
   fast-forwarded (local commits on master) is a warning: the forge merge
   is done.

No commit-message form: the forge composes the message. No
`FastForwardDeferred`.

### Before the engine (`cmd/issue/close.go`, `cmd/branch/merge.go`)

Right after the branch is picked and materialized, both flows run
`forgePullRequest(ctx, deps.client, forge, branchName)`:

- `PullRequestsOf(deps.tracker)` nil, or `ErrNoPullRequest`: nil, the flow
  is unchanged. Any other lookup error is a warning and the flow is
  unchanged too (the forge is unreachable: merge locally, as today).
- A pull request found:
  - The merge target is `pr.Base`. The target picker (`chooseMergeTarget`,
    `--base`) is skipped; `--base` with another value is an error: `pull
    request #N targets "develop", not "master"`. A line says `pull request
    #N <URL> against master`.
  - The local branch must be the remote one: `ResolveRef(refs/heads/b)`
    equal to `ResolveRef(refs/remotes/<remote>/b)` after a fetch. Ahead:
    `branch "b" has unpushed commits: push it first`. Behind: `branch "b"
    is behind <remote>/b: fetch and merge first`. Both stop the flow before
    any change.
  - `reviewPreflight` runs as today (close only).
  - `mergeflow.Run` is called with `PullRequest: &ForgeMerge{...}`.

`branch merge` keeps refusing issue branches as the source; the forge path
applies to the branches it accepts.

### After the engine

Unchanged: `updateClosedStatus` (branch status merged, tracker status or
reconcile), worktree removal, branch deletion. The forge usually deletes
the remote branch itself; `doDeleteBranch` already checks
`RemoteBranchExists` first. The local delete is already forced for squash
and rebase, from `Result.Strategy`, which the forge path still returns: a
pull request squashed or rebased by the forge needs no extra flag. The push proposal of the base finds nothing to
push and stays quiet. The closing line names the forge: `Branch "b"
merged into "master" by pull request #N and closed.`

## Section 4: errors, non-interactive runs, tests

**Errors.** A forge call fails with the forge's message, no retry.
`branch pr`: an error. The push step: a warning after the push. Close:
an error before the merge, a warning after it, the rule close applies
today.

**Non-interactive.** `-y` or no TTY: the push step offers nothing, `branch
pr` refuses, close with a pull request follows the existing `-y` rules of
the strategy picker and the confirmations.

**Tests.**

- `tracker/github/github_test.go`, `tracker/forgejo/forgejo_test.go`:
  httptest servers for the three calls, the Forgejo head filtering over
  two pages, a 405 on merge returned with its message, `Clean` on the URL.
- `tracker/fake/fake_test.go`: create lists, find matches the head, merge
  records and removes, `MergeError`.
- `tracker/tracker_test.go`: `PullRequestsOf` on the fake, on a wrapped
  fake, on nil.
- `cmd/pushflow/pushflow_test.go`: the offer after a push, no offer on a
  declined push, no offer non-interactive, a failing `Open` is a warning.
- `cmd/branch/pr_test.go`: `OpenPullRequest` with the yes/no form of the
  push step: yes creates with the generated title, body and base; no
  creates nothing; an existing pull request skips the question.
- `cmd/branch/pr_e2e_test.go`: `TestRunPr_` on the prune rig with an origin
  and a scripted prompter: opens with the prefill, prints the URL of an
  existing one, pushes first, refuses without capability, refuses `-y`.
  The branch menu test gains the entry.
- `cmd/mergeflow/mergeflow_test.go`: one forge test per strategy (method
  recorded, local target fast-forwarded), the refusal leaves the repo
  untouched.
- `cmd/issue/close_pr_e2e_test.go`: `TestClose_PullRequest_` happy path,
  unpushed branch refused, behind refused, base from the pull request and
  `--base` conflict, no capability merges locally, forge refusal before
  any local change.
- `cmd/branch/merge_e2e_test.go`: one `TestRunMerge_PullRequest` happy
  path.

CLAUDE.md gains a "Testing the pull requests" section with the commands:

    mise exec -- go test ./tracker/... -run "PullRequest" -v
    mise exec -- go test ./cmd/pushflow/... -run "PullRequest" -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunPr_|^TestRunMerge_PullRequest" -v
    mise exec -- go test ./cmd/mergeflow/... -run "Forge" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_PullRequest" -v

## Out of scope

- A draft pull request, reviewers, labels or assignees on the pull request.
- A forge that is not the issue tracker.
- Opening a pull request from the review commands' pushes.
- Listing pull requests, or a pull request column in `issue list`.
