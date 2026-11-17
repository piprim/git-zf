# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Build & Install

```bash
make                   # build ./bin/git-zf binary
make install           # copy binary to $(git --exec-path)/git-zf
./bin/git-zf install # alternative install without make
```

The Makefile builds for the host platform (`go build` defaults). Version info is injected via ldflags from `git describe` and `git log`.

## Running & Testing

```bash
mise exec -- go test ./...                    # run all tests
mise exec -- go test ./commit/... -run TestX  # run a single test
mise exec -- go build -o ./bin/git-zf .      # manual build
./bin/git-zf -d               # run with debug logging, printed to stdout
```

#### Testing the close flow

The close flow is end-to-end tested in `cmd/issue/close_e2e_test.go`. Tests
construct a real on-disk repo, a seeded branch chain, and the in-process
tracker fake at `tracker/fake/`, then drive the flow with a `scriptedPrompter`
that returns canned answers instead of opening huh forms.

To exercise just the close-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v

`issue close <id>` filters the candidates by the issue (`TestClose_ByID`,
`TestCloseCmd_ByID`); `issue abandon [<id>]` closes a repository issue without
a merge (`TestRunAbandon`, `TestAbandonCmd`).

When adding a new merge strategy or changing the merge/record/tracker
sequencing, add a corresponding E2E test alongside the existing happy-path
and failure-mode tests.

Worktree-held branches are covered by `newWorktreeCloseRig` in the same file
(feature branch checked out in a linked worktree). The engine-level worktree
cases live in `cmd/mergeflow/mergeflow_worktree_test.go` (`newWorktreeRig`).

    mise exec -- go test ./cmd/issue/... -run "^TestClose_Worktree" -v
    mise exec -- go test ./cmd/mergeflow/... -run "Worktree" -v

### Testing the start flow

The issue-start flow (used by `issue start`, `branch new` and `s` in
`issue list`) is end-to-end tested in `cmd/issue/start_e2e_test.go`,
`start_record_e2e_test.go`, `start_row_e2e_test.go` and
`start_id_e2e_test.go`. The pattern mirrors
the close-flow tests: a real on-disk repo, the fake tracker at
`tracker/fake/`, and a `scriptedStartPrompter` that returns canned answers
for every form. `branch new` (`RunIssueStart`) opens the manual form at
once, whatever the repository holds: it creates a branch, never picks an
issue. The branch needs an ID for its first segment: a typed one is used
as is (an issue living where git-zf does not look, no record), an empty one
is minted by filing a repo issue.
`issue start <id>` resolves the ID after a fetch (`issueflow.StartID`; only
an unknown ID reconciles with the mirror, and an ID nothing knows opens the
manual form with it filled in) and `issue start` without an ID is `issue
list`, no sync, whose `s` starts the row under the cursor
(`issueflow.StartRow`; a test lists the rows with `rowOf`).

To exercise just the start-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_|^TestStartRow_|^TestStartID_|^TestStartCmd_" -v

`IssueStartFlags.Tracker` is false for `branch new`, which never asks the
tracker, and true for `issue start` and `issue list`. When adding a toggle, confirm or picker form, extend
`StartPrompter` and add a matching E2E test alongside the existing
happy-path and failure-mode tests.

`git.Client.CreateWorktree` (`git/worktree.go`) is the only place a worktree
is created. After `git worktree add` it excludes an in-repo worktree
directory, then `initSubmodules` runs `git submodule update --init
--recursive` when the new worktree (not the main tree) holds a
`.gitmodules`. A failure there is a warning on Err with the command to
retry, never an error: the branch exists and must still be recorded.
`TestCreateWorktree_Submodules` (`git/worktree_test.go`) is not parallel: it
sets `protocol.file.allow=always` with `t.Setenv` to clone a local submodule.

    mise exec -- go test ./git/... -run "TestCreateWorktree" -v

A merge moves the commit recorded for a submodule and leaves its checkout
behind: `mergeflow.Run` ends with `git.Client.UpdateMovedSubmodules`, which
updates the submodules whose gitlink differs between the HEAD before the merge
and the one after, and no other, in the target's tree and in the source's
worktree (`TestRun_UpdatesMovedSubmodules`, `_SourceWorktree`).
`git.Client.RemoveWorktree` is the only place a worktree is removed. git
refuses one holding submodules, so it is removed with `--force`, but only
after `submoduleWorktreeSafe`: no modified or untracked file, no submodule
commit that is on no remote (the submodules of a worktree are stored in that
worktree alone). Otherwise it returns `git.ErrWorktreeHoldsWork`, for which
`mergeflow.removeHint` does not offer `--force`. Both tests set
`protocol.file.allow` and are not parallel.

    mise exec -- go test ./git/... -run "TestRemoveWorktree" -v
    mise exec -- go test ./cmd/mergeflow/... -run "TestRun_UpdatesMovedSubmodules|TestRemoveHint" -v

### Testing the prune flow

The branch-prune flow is end-to-end tested in `cmd/branch/prune_e2e_test.go`.
Tests construct a real on-disk repo + seeded branch chains, then drive the flow
with a `scriptedPrunePrompter` (canned confirm responses) or
`autoConfirmPrunePrompter` (mirrors `--yes`). `newPruneRigWithOrigin` adds a
remote, for the rules that depend on it (a branch still on the remote, an
unreachable remote).

To exercise just the prune-flow tests:

    mise exec -- go test ./cmd/branch/... -run "^TestRunPrune_" -v

For non-interactive use (CI, cron), pass `--yes` to skip the confirmation
prompt:

    git zf branch prune --yes

`branch close <branch-name>` (one branch, no confirmation) is tested in
`cmd/branch/close_e2e_test.go` on the same rig:

    mise exec -- go test ./cmd/branch/... -run "^TestRunCloseBranch|^TestCloseCmd" -v


### Testing the repo issues

Issues stored in the repository (`refs/zf/issues/*`) are tested at three
levels, all on real on-disk repos:

- `issue/record_test.go` — the fold (pure, no git).
- `git/issue_ref_test.go`, `git/issue_ref_sync_test.go` — plumbing: write and
  read a chain, fetch into `refs/remotes/<remote>/zf/issues/*`, reconcile, push.
- `issue/repo_test.go` — the glue (`Create`, `Append`, `Load`, `Resolve`,
  `Sync`), including two clones diverging and merging.
- `cmd/issue/record_e2e_test.go`, `record_ops_e2e_test.go` — the `new`, `show`,
  `edit`, `comment`, `label`, `sync` and `abandon [<id>]` commands, driven by a
  `scriptedRecordPrompter` on a `recordRig` (`newRecordRig(t, user, origin)`;
  pass `newBareOrigin(t)` to two rigs to get two clones of one remote).

    mise exec -- go test ./issue/... ./git/... -run "TestFold|TestIssueRef_|TestPushFetchSync" -v
    mise exec -- go test ./cmd/issue/... -run "^TestRun(New|Show|Edit|Comment|Label|Sync|Abandon)|^TestAbandonCmd" -v

The start, close and list integrations live next to their flows:
`start_record_e2e_test.go`, `close_record_e2e_test.go`, `list_record_test.go`.

When adding an op type, add its constant and its `Fold` case in
`issue/record.go` with a table case in `TestFold`. Unknown types must keep
being skipped: an older binary reads refs written by a newer one.

### Testing the tracker mirror

With `[issue-tracker] mirror = true`, the repo issues are mirrored with one
tracker project (`issue.Mirror`, `issue/mirror.go`). It is tested at four
levels, on real on-disk repos and the fake tracker:

- `issue/record_test.go` — `TestFold_Tracker`: the link and the tracker state.
- `git/chain_ref_test.go` — `TestChainRef_FixedRoot`: the deterministic root,
  pinned by a golden hash.
- `issue/mirror_test.go` — `Reconcile`: import, export, the eight rows of the
  state table (`TestReconcile_StateTable`), two clones importing offline.
- `issue/mirror_test.go` — the content: `TestReconcile_TitleAndDescription`,
  `TestReconcile_Labels`, `TestReconcile_LabelRuleRejection`,
  `TestReconcile_Comments`, `TestReconcile_StatusIsLabel`, `TestSyncOne`.
- `cmd/issue/mirror_e2e_test.go`, `close_mirror_e2e_test.go` — the commands.

    mise exec -- go test ./issue/... -run "TestReconcile|TestFold_Tracker|TestResolve_TrackerNumber" -v
    mise exec -- go test ./git/... -run "TestChainRef_FixedRoot" -v
    mise exec -- go test ./cmd/issue/... -run "^TestMirror|^TestClose_Mirror" -v
    mise exec -- go test ./issue/... -run "TestReconcile_(TitleAndDescription|Labels|LabelRuleRejection|Comments|StatusIsLabel)|TestSyncOne" -v
    mise exec -- go test ./cmd/issue/... -run "^TestMirror_(Edit|Comment|LabelPicker)|^TestClose_Mirror_StatusIsLabel" -v

The fake tracker is stateful for these calls: `CreateIssue` lists the issue,
`SetIssueOpen` / `CloseIssue` / `ReopenIssue` move it in and out of the open
listing. Use `CloseIssue` for "someone closed it in the tracker's UI": it is
not recorded in `RecordedOpens`.

`issue new` proposes to assign the issue to its creator (`--assign` without
the form): an `assign` op holding the git identity, shown by `issue show`.
The mirror's export then calls `AssignToMe` for a record assigned to the
clone's own identity (the fake: `RecordedAssigns`, `AssignErr`), and nowhere
else. When run interactively (`issue new` with no flags or `n` in `issue
list`), it then asks whether to start work on the issue right away
(`recordPrompter.ConfirmStart`, default no) and runs `issueflow.StartRow` on
yes (`runNewWithStart`, `TestRunNew_ProposeStart`,
`TestMirror_NewStartUsesTrackerNumber`).

    mise exec -- go test ./issue/... -run "TestFold_Assign|TestCreate_Assign|TestReconcile_ExportAssign" -v
    mise exec -- go test ./tracker/... -run "AssignToMe" -v
    mise exec -- go test ./cmd/issue/... -run "^TestRunNew_Assign|^TestNewCmd_Assign|^TestMirror_NewAssigns|^TestRunNew_ProposeStart|^TestMirror_NewStart" -v

`issue show` prints who opened an issue (`Record.Author`: the tracker's user
for an issue born in the tracker, the create op's git identity otherwise) and
who works on it (`Record.AssignedTo()`: the tracker's assignees, else the
local `assign` identity); `issue list` shows the first assignee, cut to 8
runes, in its `Assigned` column (`issue.AssignedCell`). The adapters report
`tracker.Issue.Author` and `Assignees`, and the mirror keeps them in the
`tracker_seen` snapshot, assignees in the tracker's order: `sameSnapshot`
comparing them is the whole backfill, so never make `contentSettled`,
`recordSettled` or `pullComments` look at them. The fake needs no knob: set
the fields on the issue, or through `Edit` for a reassignment in the web UI.

    mise exec -- go test ./tracker/... -run "AuthorAndAssignees" -v
    mise exec -- go test ./issue/... -run "TestFold_AuthorAndAssignees|TestReconcile_AuthorAndAssignees|TestAssignedCell|TestRecordRow_Assignee" -v
    mise exec -- go test ./cmd/issue/... -run "AuthorAndAssignee|TestBuildRows_TrackerAndRepository" -v

The fake's `Edit` and `WebComment` play the tracker's web UI (not recorded, the
issue's `UpdatedAt` moves); `StatusIsLabel` makes it behave like Redmine. A
reconcile runs state, content, state again, then writes `tracker_seen`; a
`SetLabels` that returns `tracker.ErrLabelRule` makes the record take the
tracker's labels back, any other error keeps the local change.

`importRootTemplate` (`issue/mirror.go`) is frozen: the ID of every imported
issue is the hash of a commit holding those bytes. Never edit it; if
`TestChainRef_FixedRoot` fails on the golden hash, the code is wrong. Only
`issue sync` (`r` in the table), the `issue start` picker (it has no `r`)
and the commands that create an issue or change its state reconcile;
`issue list` and any read-only command must not: the repo issues are the
mirror. `issue list` does not fetch either (`issueflow.ListRows` is local
work; `r` syncs): a picker that must be fresh fetches before calling it, as
`RunIssueStart` does. `Mirror.Labels` asks the tracker for its labels once
per process (a reconcile forgets them).

`issue.List` reads every issue with `ReadAllChains` (three git processes) and
`issue.PushAll` pushes many issues in one `git push` (`PushChainRefs`); a loop
of `Load` or `Push` over many issues is the thing to avoid.

    mise exec -- go test ./git/... -run "TestPushChainRefs" -v
    mise exec -- go test ./issue/... -run "TestPushAll|TestList_" -v

### Testing the branch chains

Tracked branches live in the repository (`refs/zf/branches/<slug>`, one chain
per issue): there is no local database. They share the chain plumbing of the
issues and reviews (`git.BranchRefs`) and are tested at the same levels:

- `branch/record_test.go` — the fold (pure, no git): `TestFold`, `TestRows`.
- `git/chain_ref_test.go` — `ReadAllChains` (every chain in three git
  processes), the leased delete of a legacy blob.
- `branch/repo_test.go` — `Start`, `SetStatus`, `Load`, `List`, `Find`, `Sync`,
  two clones starting variants offline, legacy blobs.
- the start, close, prune and review E2E suites — the commands.

    mise exec -- go test ./branch/... -v
    mise exec -- go test ./git/... -run "TestChainRef_ReadAllChains|TestChainRef_DeleteLease" -v

Seed a tracked branch in a test with `branchtest.Seed` (never by writing refs by
hand); `branchtest.Amend` fills the parent, tracker type or issue ID of a branch
a rig already seeded. A git client caches its remote name: add the remote
before the first seed.

When adding a branch op type, add its constant and its case in `apply`
(`branch/record.go`) with a table case in `TestFold`. Unknown types must keep
being skipped. A command that offers a choice of in-progress branches calls
`branch.Fetch` first; a hook or `commit` never does, and finds its branch with
`branch.Find`.

### Testing the review chains

Reviews stored in the repository (`refs/zf/reviews/*`) share the chain plumbing
of the issues (`git/chain_ref.go`, `git/chain_ref_sync.go`, parameterized by
`git.IssueRefs` / `git.ReviewRefs`) and are tested at the same levels:

- `internal/chain/chain_test.go` — the op ordering both folds use.
- `review/record_test.go` — the fold (pure, no git), including the concurrent
  cases.
- `git/chain_ref_test.go` — the review namespace, tracking refs under
  `refs/remotes/<remote>/zf/`, legacy blob refs.
- `review/repo_test.go` — `Append`, `Load`, `List`, `Sync`, two clones
  approving offline.
- `cmd/review/chain_e2e_test.go`, and `^TestClose_SignedGate` /
  `^TestClose_ReviewPreflight` in `cmd/issue/close_e2e_test.go` — the commands
  and the signing gate.

    mise exec -- go test ./internal/chain/... ./review/... -v
    mise exec -- go test ./git/... -run "TestChainRef|TestCommitSignature" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_SignedGate" -v

Seed a review in another package's test with `reviewtest.Seed` (never by
writing refs by hand). Tests that sign use `gittest.SSHSigner`, which skips
when `ssh-keygen` is missing.

When adding a review op type, add its constant and its case in `apply`
(`review/record.go`) with a table case in `TestFold`. Unknown types must keep
being skipped.

### Testing the log view

`git zf log` runs one of the log commands of `[log]` (`config.LogConfig`,
`config/log.go`: a shell line split with shlex, validated in `normalize`) in a
bubbletea list (`tui.LogModel`): enter shows the commit's stat and patch
(`git.ShowCommit`) in a dialog that scrolls both ways, `/` filters the lines
like the issue table does, 1-9 and `c` switch the command (`LogActions.Load`).
Without a TTY on stdout the default command is printed as `git log` would
(`git.PrintLog`). Arguments after `--` reach `git log`.

    mise exec -- go test ./config/... -run "TestLoad_Log|TestLogCmd" -v
    mise exec -- go test ./git/... -run "TestLog|TestShowCommit|TestPrintLog" -v
    mise exec -- go test ./tui/... -run "TestCommitHash|TestLogModel" -v
    mise exec -- go test ./cmd/log/... -v

The hash of a graph line is read by `commitHash` (`tui/log.go`) after
stripping colors; a graph-only line (`|/`) has none and enter ignores it, so a
command's format must start with the hash. In the dialog `/` searches as
`less` does (`tui/log_search.go`, `TestHighlight`, `TestLogModel_Search`):
matches in reverse video (the current one in yellow, `currentOn`), `n` next,
`p`/`N` previous, `/` again edits the last search. The built-in commands live in
`config/default.toml`; a user `[[log.cmds]]` array replaces them wholesale.

`L` on a merge at the tip of a local branch replaces it by one commit holding
its tree (`git.LinearizePlan`, `LinearizeMerge`, `PushLinearized` in
`git/linearize.go`): the plan fetches the branch's remote and refuses a merge
under the tip or an upstream that moved; the push that follows is leased on the
hash the plan fetched. The view runs each step as a `tea.Cmd` and asks y/n in
the hint (`LogActions.Plan`, `tui.Linearization`); `cmd/log.planLinearize`
words the questions.

    mise exec -- go test ./git/... -run "TestLinearize" -v
    mise exec -- go test ./tui/... -run "TestLogModel_Linearize" -v
    mise exec -- go test ./cmd/log/... -run "TestPlanLinearize" -v

### Testing the menus

`git zf` (no subcommand) and `git zf review` open an action menu built on
`cmdutil.RunMenu` (`cmd/cmdutil/menu.go`): it takes the parent command, a
title, the ordered subcommands to offer, and a `MenuPrompter`. The picked
subcommand's `RunE` runs with the *parent* command (cobra v1.1.3 has no
`SetContext`), so it sees its flags as unset and runs interactively. No TTY on
stdin → the group's help is printed; Esc → quiet exit.

    mise exec -- go test ./cmd/cmdutil/... -run "^TestRunMenu|^TestMenuOptions" -v
    mise exec -- go test ./cmd/ -run "^TestGetRootCmd_menu" -v
    mise exec -- go test ./cmd/review/... -run "^TestReviewRootCmd" -v

To add an entry, append the subcommand to `rootMenu` (`cmd/root.go`) or
`Review.menuSubs` (`cmd/review/review.go`), and extend the matching test.
`git zf branch` has no menu either: it opens the branch table (see "Testing
the branch table"). `git zf issue` has no
menu: it runs `issue list` (`TestIssueRootCmd_ListsByDefault`), whose table
prints to stdout when stdin is not a terminal. Enter opens a per-row menu
(`issueMenuEntries`, `TestIssueMenuEntries`, `TestIssueTable_Menu`), and the
keys `s`, `n`, `c` (the merge flow of `issue close <id>`), `a` (pressed
twice: an abandon has no undo), `l` and `r` stay as shortcuts: the menu and
the keys quit the table with a `tui.IssuePick` (`TestIssueTable_ActionKeys`),
comment included (`tui.IssuePickComment`);
`listAction` (`cmd/issue/list.go`, `TestListAction`) runs the command behind
the pick with the terminal free and the list loop reopens the table from
`tui.IssueTableState`. To add a
key, add its `IssuePick`, its case in `updateNormal` and in `listAction`. The
forms a key opens are built with `tui.Form`, never `huh.NewForm`: esc aborts
them (huh's default is ctrl+c alone) and `listAction` reopens the table on
`huh.ErrUserAborted`, `s` included. A
subcommand offered by a menu reads its string flags with `cmdutil.StringFlag`,
never `cmd.Flags().GetString`: the parent command does not define them.

### Testing the branch table

`git zf branch` opens a table of every local branch (`branch.TableRows`,
`tui.BranchTableModel`): S/T/R filter, enter opens a per-row menu
(`menuEntriesFor`, `cmd/branch/table.go`), the keys `C`, `c`, `m`, `u`, `a`
(twice), `t`, `l`, `D` (twice) act on the row directly (`keyActions`,
`offersKey`, `runKey`, `TestBranchTable_ActionKeys`), and `runBranchAction`
runs the entry with the terminal free, as `listAction` does for the issue
table. The actions of other packages are injected through `tableDeps` funcs; a
nil func removes its entry. `trackPlain` (`cmd/branch/track.go`) renames a
free-named branch to `<id>@<type>@<slug>` after the `branch new` form.

    mise exec -- go test ./branch/... -run "TestTableRows" -v
    mise exec -- go test ./tui/... -run "TestBranchTable_|TestRelativeTime" -v
    mise exec -- go test ./cmd/branch/... -run "^TestMenuEntriesFor|^TestKeyActions|^TestOffersKey|^TestRunBranchAction|^TestRunKey|^TestRunBranchTable_|^TestTrackPlain" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_ByBranchName" -v
    mise exec -- go test ./cmd/log/... -run "TestRunLog_Branch|TestWithoutAllRefs" -v

### Testing the merge flow

The shared merge engine (`cmd/mergeflow`) is unit-tested in
`cmd/mergeflow/mergeflow_test.go` (real on-disk repo, scripted
`mergeflow.Prompter`, one path per strategy plus conflict / abort / materialized
rollback). The `branch merge` command is end-to-end tested in
`cmd/branch/merge_e2e_test.go` with a `scriptedMergePrompter`.

    mise exec -- go test ./cmd/mergeflow/... -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunMerge_" -v

`issue close` and `branch merge` share `cmd/mergeflow` as their merge engine.
`branch merge` REFUSES issue branches as the source (it redirects to `issue
close`). When changing the refusal or the shared engine, keep the close E2E
suite green — it is the regression net for the extraction.


### Testing the pull requests

On GitHub and Forgejo the tracker is also the forge: `tracker.PullRequests`
(`tracker/tracker.go`, discovered with `tracker.PullRequestsOf`) finds, opens
and merges a pull request. `git zf commit` offers one after a push of a
feature branch (one yes/no question, `pushflow.PullRequestOffer`), `git zf
branch pr [branch]` opens one with an editable form (`cmd/branch/pr.go`,
`OpenPullRequest` is the shared step), and `issue close` / `branch merge`
merge a branch that has an open pull request on the forge
(`mergeflow.ForgeMerge`): strategy picker and confirm as today, then the
forge merges and the local base is fast-forwarded. The branch must be at the
remote's tip (`issueflow.CheckPushed`). The fake tracker plays the forge:
`PullRequests`, `RecordedPullRequests`, `RecordedMerges`, `MergeError`; wrap
it in `struct{ tracker.Tracker }{fake}` for a tracker without the capability.

    mise exec -- go test ./tracker/... -run "PullRequest" -v
    mise exec -- go test ./cmd/pushflow/... -run "PullRequest" -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunPr_|^TestPrCmd|^TestOpenPullRequest|^TestPullRequestDraftFor|^TestRunMerge_PullRequest" -v
    mise exec -- go test ./cmd/commit/... -run "TestPullRequestOffer|TestForgeOf" -v
    mise exec -- go test ./cmd/mergeflow/... -run "TestRun_Forge|TestMethodOf" -v
    mise exec -- go test ./cmd/issueflow/... -run "TestCheckPushed|TestForgePullRequest" -v
    mise exec -- go test ./cmd/issue/... -run "^TestClose_PullRequest" -v

The head of a branch's pull request is `issueflow.PullRequestHead`: the bare
branch when the clone's remote is the tracker's project, `owner:branch` when
the remote is a fork of it (the remote URL's owner differs from the
project's; a path remote is never a fork). Both adapters take the head as
given: GitHub's head filter is `owner:branch`, Forgejo matches the owner of
`head.repo.full_name` and the ref. A fork's pull request is found only by
that head: asked with the bare branch, it is not the branch's. After a forge
merge from a fork the base is fetched from the upstream
(`issueflow.ForkUpstream`, `ForgeMerge.Upstream`), fast-forwarded and pushed
to the fork.

    mise exec -- go test ./cmd/issueflow/... -run "TestPullRequestHead" -v
    mise exec -- go test ./cmd/branch/... -run "^TestRunPr_FromAFork" -v

### Testing the spinner

A step that waits on the remote, the forge or the tracker is wrapped in
`client.IO().Spin(msg)` (`internal/pkg/spin.go`), which returns an idempotent
`stop`: `issue close` (`runClose`, `getPickedBranch`, `updateClosedStatus`,
`doDeleteBranch`), `branch merge` (`runMergeSource`), the forge merge and the
rebase's fetch (`cmd/mergeflow`), `pushflow.Propose`, and the helpers every
issue command syncs through (`issueflow.FetchIssues`, `ReconcileIssues`,
`pushIssue`, `syncOne`, `labelChoices`; a Spin under a caller's Spin is a
no-op). It is a no-op when Err
is not a terminal, so the E2E rigs never spin; `spinOn` is tested on a buffer.

    mise exec -- go test -race ./internal/pkg/... -run "TestSpin" -v

While it spins, `IO.Out` and `IO.Err` are writers that wipe the spinner's line
first (a subprocess gets a pipe, not the TTY), and an unfinished line holds
the spinner until its newline. Two rules for a new call site: stop before any
form or bubbletea program (they draw on the terminal directly), and never
keep a writer read from `IO()` across a `Spin` or a `stop` — read
`IO().Err` at each write (`writerFunc` in `cmd/issue/close.go`).

## Architecture

Main packages under `github.com/piprim/git-zf`:

- **`cmd/`** — Cobra CLI entry point. `root.go` wires config loading (Viper) and the other commands.
- **`tui/`** — TUI form logic using `github.com/charmbracelet/huh`.
- **`git/`** — Thin wrappers around `go-git` and the git CLI, including the commit-chain plumbing (`chain_ref.go`).
- **`branch/`**, **`issue/`**, **`review/`** — what git-zf records in the repository, one chain family each under `refs/zf/`: a pure fold (`record.go`) and the git glue (`repo.go`). There is no database.

## Configuration

Config file: `.git-zf.toml` in the repository's git dir (`<repo>/.git/.git-zf.toml`) or `$HOME`. The repo-level file takes priority. The `commit-message.items` array overrides the default form fields; `commit-message.template` is a Go `text/template` string. If no config is found, the embedded `config/default.toml` is used.

## Go Toolchain

- Go is managed by **mise**. Run `mise install` if the expected Go version is not active.
- To launch Go command the proper way is to use `mise exec -- go…`

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **git-zf** (6282 symbols, 26192 relationships, 493 execution flows).

> Index stale? Run `node .gitnexus/run.cjs analyze --index-only` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? Bootstrap with `npx`, `bunx`, or `pnpm dlx` — e.g. `bunx gitnexus@latest analyze` (npm 11 npx crash; #1939).

## Always Do

- **MUST run impact before editing.** Use `impact({target: "symbolName", direction: "upstream"})` or `node .gitnexus/run.cjs impact "symbolName" --direction upstream --repo .`; report callers, processes, and risk. Never substitute grep for graph analysis.
- **MUST analyze graph changes before committing.** Use `detect_changes({scope: "all"})` (MCP) or `node .gitnexus/run.cjs detect-changes --scope all --repo .` (CLI fallback). `partial: true` or `truncated: true` is not a clean check — a zero means unseen, not unaffected; re-run it. For regression review: `detect_changes({scope: "compare", base_ref: "main"})` or `node .gitnexus/run.cjs detect-changes --scope compare --base-ref "main" --repo .`.
- MUST warn on HIGH/CRITICAL `risk` pre-edit; never use `riskSharedAxes` to waive a HIGH/CRITICAL `risk` warning. Compare File/symbol: MCP File omits axes; Graph-RAG expands File.
- **MUST treat `risk: UNKNOWN` as unresolved, not as low.** An empty caller set is not evidence the symbol is unused — it can also mean the callers are not resolvable by the index (plain-object property access, dynamic dispatch, cross-language calls). `impact` pairs `UNKNOWN` with a `riskNote` saying so. Confirm with a text search before treating the symbol as safe to change or delete; do not proceed on the strength of a zero.
- **MUST use `query({search_query: "concept"})` for concepts/flows, `context({name: "symbolName"})` for a named symbol, or `impact` for blast radius, on read-only callers, dependencies, imports, or execution flow.** Graph first; text search only for empty/`UNKNOWN`/literals.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method before MCP/CLI impact analysis.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis, and never read `UNKNOWN` as an all-clear — it means the walk could not answer, which is the one verdict that requires confirming by other means.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit before MCP/CLI graph change analysis.

## Resources

| Resource | Use for |
| --- | --- |
| `gitnexus://repo/git-zf/context` | Codebase overview, check index freshness |
| `gitnexus://repo/git-zf/clusters` | All functional areas |
| `gitnexus://repo/git-zf/processes` | All execution flows |
| `gitnexus://repo/git-zf/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
| --- | --- |
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
