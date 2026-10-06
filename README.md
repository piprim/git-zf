# Git-ZF - Git Zen workFlow

<p align="center">
  <img src="./assets/git-zf-logo.webp" alt="Logo git-zf" width="272" />
</p>

> A TUI powered CLI that wraps a git-flow workflow (issue → branch → commit → review → close) with optional issue-tracker integration.

## Getting Started

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- Git

### Install

From source:

```bash
git clone https://github.com/piprim/git-zf.git
cd git-zf
make
sudo make install      # copies binary to $(git --exec-path)
```

Or via `go install`:

```bash
go install github.com/piprim/git-zf@latest
sudo git-zf install    # copies binary to $(git --exec-path)
```

> If `git --exec-path` is user-writable (e.g. Homebrew Git on macOS), omit `sudo`.

Check with `git zf version`. Remove with `git zf uninstall`.

Then, once per repository (and per submodule), run `git zf init` from the main checkout to install the review hooks (see [Init](#init)).

## Usage

Every command is interactive by default. Passing a flag skips the corresponding prompt, and `--help` lists the flags of any command.

### Menu

`git zf` without a subcommand opens a menu of the workflow commands — **Commit**, **Issue**, **Branch**, **Review** — and runs the one you pick with its interactive defaults. `git zf issue`, `git zf branch` and `git zf review` do the same one level down. Esc / ctrl+c leaves the menu quietly, and when stdin is not a terminal (scripts, CI) the menu is skipped and the usual `--help` text is printed. Setup commands (`init`, `install`, `uninstall`, `config`, `completion`, `version`) are not in the menu.

### Commit

```
$ git zf commit
```

Opens a commitizen-style form (type, scope, subject, body, footer — see [Commit message](#commit-message)) followed by an options page (stage all, amend, sign-off, hooks, push…). Every option has a flag (`-a`, `--amend`, `-s`, `-n`, `--push`, `--no-push`, `-y`…); if any commit flag is passed the options page is skipped and the flags are used directly.

On an issue branch the form is pre-filled from the branch name (see [Commit auto-fill](#commit-auto-fill-from-issue-branch)).

**Review guard** — on a feature branch whose review decision left reviewer commits on `<IssueID>@review` that are not yet in your branch, `git zf commit` offers to merge them in before opening the form. Declining aborts with a hint to run `git zf review sync`. `--no-verify` skips the guard, as it does the pre-commit hook.

### Issue

```
$ git zf issue start
$ git zf issue list
$ git zf issue close            # merge an in-progress branch and close its issue
$ git zf issue close <id>       # close a repository issue without merging
$ git zf issue new              # create an issue in the repository, no branch
$ git zf issue show [<id>]      # show an issue and its comments
$ git zf issue edit [<id>]      # change the title and description of an issue
$ git zf issue comment [<id>]   # comment on an issue
$ git zf issue label [<id> +add -remove …]
$ git zf issue sync             # fetch, merge and push the repository issues
```

**`issue start`** — start work on an issue: fetch your open issues from the configured tracker (Redmine, GitHub, Forgejo/Gitea), or take the manual path: pick an open issue stored in the repository, or fill the form. Leaving the form's Issue ID empty creates a new issue in the repository; typing one (for a tracker git-zf does not talk to) uses it as is. A branch named `{issue-id}@{type}@{slug}` (see [Branch naming](#branch-naming)) is created and checked out, **or a git worktree is created** so the main working tree stays untouched. A prompt asks which; pin the choice with `branch.use-worktree` in the config. When a worktree is created the command prints its path and a `cd` hint, since the shell cannot change directory for you. With a tracker configured, you can move the issue to "In Progress" in the same step. The branch is recorded in the repository, under `refs/zf/branches/<issue-id>` (see [Branches in the repository](#branches-in-the-repository)), and that record is pushed: every clone sees that the issue is in progress.

Pass `--variant=<label>` to create a parallel branch on an issue that already has one (see [Parallel branches per issue](#parallel-branches-per-issue)).

**`issue list`** — list issues enriched with local branch data. The tracker is the primary source when configured. Otherwise the list is the issues stored in the repository plus the tracked branches. Columns: Issue ID · [Project] · Title · Branch · Local Status · Issue Status · Created. Labels follow the title in brackets. `∅` means no branch started yet; `N.A.` means the row has neither a tracker nor a repository issue.

In the TUI: **`/`** filters rows (any column, case-insensitive), **`tab`** cycles the status filter (Open → Closed → All), **`p`** opens the project picker, **`q`** quits. Flags: `--status open|closed|all`, `--stdout` (plain table), `--json`.

**`issue close`** — close an in-progress issue. Pick a branch (the current one is pre-selected), then:

1. **Reviewer commits** left on `<IssueID>@review` by an approved or rejected review are incorporated (fast-forward or merge); a conflicting merge refuses with a hint to run `git zf review sync`. A parent issue with open sub-tasks is refused.
2. **Conflict dry-run** via `git merge-tree` against the target (`--base <branch>` overrides the default: the parent branch for a sub-task, otherwise the configured base). Conflicts abort the command before anything is touched.
3. **Pick a merge strategy** — Rebase (default), Squash or Classic (see [Merge strategies](#merge-strategies)) — and compose the final commit in the commitizen form, pre-filled from the issue.
4. **Confirm.** The branch is recorded as `merged` on its chain, which is pushed. An issue stored in the repository is closed there too, and pushed.
5. **Tracker status** picker, if a tracker is configured (or skip).
6. **Worktree removal**, if the branch was started in one. Never forced: a worktree with modified or untracked files is left in place. A `cd` hint back to the main checkout is printed when you ran the command from inside the removed worktree.
7. **Branch deletion**, locally and on the remote, then a push proposal. Classic uses `git branch -d`; Squash and Rebase need `-D` since neither preserves ancestry. A branch still held by a kept worktree is not deleted.

The picker also lists branches started in another clone, known from the fetched `refs/zf/branches/*` chains, so a teammate can close an issue they did not start: the branch is materialized from `origin/<branch>`.

Closing works from inside a linked worktree. Rebase runs its steps in the worktree holding the branch and fast-forwards the base from the main checkout; Squash and Classic run in the main checkout. Git refuses the close when the *base* branch is checked out in another linked worktree.

**`issue close <id>`** — close an issue stored in the repository without merging anything, for a duplicate or an issue that will not be worked on. The close is pushed. It is refused while a branch of the issue is in progress: merge it with `git zf issue close`, or abandon it with `git zf branch close <branch-name>`, then close the issue. `--base`, `--push` and `--no-push` belong to the merge and are refused with an ID.

#### Issues in the repository

Without a tracker, issues live in the repository itself, under
`refs/zf/issues/`, and travel with `git zf issue sync`. A teammate with a
fresh clone sees the same backlog, descriptions and comments, with no account
anywhere.

```
$ git zf issue new --title "Login fails on Safari" --type fix --label bug
Created issue 1a2b3c4: Login fails on Safari
$ git zf issue comment 1a2b3c4 -m "Reproduced on 17.4"
$ git zf issue label 1a2b3c4 +ui -bug
$ git zf issue edit 1a2b3c4 --title "Login fails on Safari 17"
$ git zf issue show 1a2b3c4
$ git zf issue start            # pick it, the branch is 1a2b3c4@fix@login-fails-on-safari
```

- **`issue new`** opens a form (title, type, description, labels). Any flag
  (`--title`, `--type`, `--description`, `--label`, repeatable) skips it.
- **`issue show`**, **`issue edit`**, **`issue comment`** and **`issue label`**
  take the issue ID shown by `issue list`: the 7-character ID, the full one, or
  any unique prefix of at least 4 characters. Without an ID they open a picker.
  `show --json` prints the record; `comment -m` skips the form.
- **`issue edit`** opens a form holding the current title and description.
  `--title` and `--description` skip it and change only the field passed;
  `--description ""` clears the description. A branch already started for the
  issue keeps its name.
- **`issue sync`** fetches the issues from the remote, merges the ones changed
  on both sides and pushes yours. Each command above also pushes its own
  change, so `sync` is mostly for bringing in other people's.

Two people can comment on or relabel the same issue offline: both changes are
kept when they sync. Nothing is ever force-pushed. The storage format is
described in [docs/issue-refs.md](docs/issue-refs.md).

Syncing these issues with Redmine, GitHub or Forgejo is not available yet.

#### Sub-tasks

An issue branch can serve as the integration branch for sub-tasks. Start one with `git zf issue start --parent=<parent-issue-slug>`, or simply pick the parent's branch in the base-branch picker that `issue start` shows when more than one candidate branch exists. A sub-task then:

- branches from and closes into the parent branch instead of the configured base,
- gets parent drift merged in by `git zf review sync`,
- must be closed before its parent can be closed.

The parent relation is recorded in `refs/zf/branches/<slug>`, so every clone has it.

#### Merge strategies

| Strategy | Mechanism | History on base | Submodule-safe |
|---|---|---|---|
| **Rebase** *(default)* | Real `git merge <remote>/<base>` + `git reset --soft`, one commit | one clean commit | ✅ yes |
| **Squash** | `git merge --squash` | one commit, no merge parent | ⚠️ no — `--squash` mishandles submodule gitlinks |
| **Classic** | `git merge --no-ff --no-commit`, local base FF-synced against `<remote>/<base>` first | merge commit + full feature history | ✅ yes |

- **Rebase** — your repo has submodules, or you want a clean linear history with one commit per issue. Recommended default. Any failure before the commit lands (form abort, hook rejection, signing failure) rolls the feature branch back to its original tip.
- **Squash** — no submodules involved and you want plain `git merge --squash` semantics (fewer git operations).
- **Classic** — you want the feature's full commit history on the base branch behind a merge commit (large features, bisect surface, audit trail). Refuses to merge into a local base that has diverged from the remote; run `git pull --ff-only` and retry. A failure before the commit runs `git merge --abort` automatically.

All three compose the final commit through the commitizen form. The mechanics, rollback rules and the reason Rebase is not `git rebase` are documented in [docs/merge-strategies.md](docs/merge-strategies.md).

### Branch

```
$ git zf branch new            # create a branch with manual input
$ git zf branch list           # list tracked branches
$ git zf branch merge          # merge a branch via TUI
$ git zf branch prune          # record branches merged or deleted outside git-zf
$ git zf branch close <name>   # record one branch as closed (abandoned)
$ git zf branch prune-tracker  # reap branches whose tracker issue is closed
```

**`branch new`** — the `issue start` flow with manual input pre-selected. Accepts `--variant=<label>` too.

**`branch list`** — tracked branches with their status, yours and the ones other clones started. It does not contact the remote: it shows what the last `git fetch` brought. Flags: `--status in_progress|merged|closed|all`, `--stdout`, `--json`.

**`branch merge`** — pick a local or remote-only branch and merge it into the current branch with one of the [merge strategies](#merge-strategies), then offer to delete the source (local + remote) and propose a push. Issue branches are refused: use `git zf issue close` for those, so the review, tracker and store steps still run. A source branch checked out in another working tree is merged in place; when that tree is a linked worktree you are offered to remove it after the commit lands (the main checkout is never removed). A branch held by a stale worktree entry is refused with a `git worktree prune` hint.

**`branch prune`** — record what happened to in-progress branches outside git-zf. A local branch already merged into the base is marked `merged`. A branch gone locally and on the remote is marked `closed`, if you started it; one that someone else started is listed and left to them, since it may be work they have not pushed yet. When you know such branches are abandoned (their author left, or you changed your git name or email), `--others` closes them too; `git zf issue track` on a closed branch reopens it. A branch still on the remote is left alone. Nothing is closed when the remote cannot be reached. Flags: `--base <branch>`, `--dry-run`, `--others`, `--yes` (skip the confirmation, for CI).

**`branch close <branch-name>`** — record one tracked branch as closed, and push that record. Nothing is merged and no git branch is deleted, so this is not `issue close`: it is for a branch that will not be merged. The branch does not have to exist in your clone, and it does not matter who started it. Only a branch in progress can be closed. To undo, check the branch out and run `git zf issue track`. Not in the `git zf branch` menu, since it needs a name.

**`branch prune-tracker`** — find local branches whose issue ID (parsed from the branch name) is closed in the configured tracker, then offer a per-branch action: safe-delete (default), force-delete or skip. Successful reaps record the branch as `closed`. Branches whose name does not parse are skipped silently; tracker lookup failures print a `WARN:` line and skip that branch. For non-interactive use pass exactly one of `--safe-delete`, `--force-delete` or `--skip-delete` to apply it to every candidate; `--dry-run` previews with no prompts or mutations.

```
$ git zf branch prune-tracker --dry-run
$ git zf branch prune-tracker --safe-delete --base main   # CI
```

#### Branches in the repository

The branches of an issue are recorded under `refs/zf/branches/<issue>` as a
chain of commits: one when a branch is started, one each time its status
changes (in progress, merged, closed). The chain also holds the issue's title,
its parent issue and the tracker it came from. It is pushed and fetched like
the review and issue chains, so every clone knows which branches are in
progress, and git-zf keeps no database of its own. See
[docs/branch-refs.md](docs/branch-refs.md).

### Review

```
$ git zf review           # pick an action from a menu
$ git zf review request   # developer: submit an issue branch for review (locks it)
$ git zf review start     # reviewer: create <IssueID>@review from the locked snapshot
$ git zf review approve   # reviewer: approve — the branch is ready to close
$ git zf review reject    # reviewer: request changes — unlocks the branch
$ git zf review list      # list open reviews: issues in review or approved
$ git zf review status    # show the round-by-round history for an issue
$ git zf review fetch     # sync review refs with the remote (fetch, merge, push)
$ git zf review sync      # bring a branch up to date: reviewer commits + parent drift
$ git zf review track     # register a branch created with plain git checkout
```

Peer-to-peer code review with no server-side component. Review state lives in git refs under `refs/zf/reviews/<IssueID>`, one commit per action (see [Reviews in the repository](#reviews-in-the-repository)), pushed to and fetched from the remote. The refs are the only record: nothing about a review is kept outside them. Concurrent actions from two machines are merged, never overwritten.

A review round:

1. **Developer** — `git zf review request`. Pick an in-progress branch; its review ref becomes `in_review` and the branch is **locked**: the pre-push hook installed by [`git zf init`](#init) rejects pushes to it until the reviewer decides (bypass: `git push --no-verify`).
2. **Reviewer** — `git fetch && git zf review start`. Pick an issue awaiting review; git-zf creates `<IssueID>@review` at the SHA captured at lock time and records your identity in the ref. Review the code; optionally commit fixes on the `@review` branch and push them.
3. **Reviewer decides**:
   - `review approve` — status `approved`; the developer can run `git zf issue close`, which incorporates any reviewer commits (see [Issue](#issue)).
   - `review reject` — status `changes_requested`, the branch is unlocked. An empty `@review` branch is deleted (locally and on the remote); one carrying reviewer commits is kept for the developer to inspect (`git log <feature>..<IssueID>@review`). While such commits await incorporation, **new commits on the feature branch are blocked** by the commit guard and the pre-commit hook until `git zf review sync` merges them in (bypass: `--no-verify`).
4. **Next round** — the developer pushes fixes and runs `review request` again: the round counter increments and the stale `@review` branch is removed. If it still carries unincorporated reviewer commits the request refuses to delete them and offers to merge them first.

`request`, `approve` and `reject` accept `--push` / `--no-push`, and propose a tracker status update when the issue came from a tracker.

- **`review list`** reads `refs/zf/reviews/*` (works on a fresh clone) and prints every open review, `in_review` or `approved`, with its round number. A closed review stays in the repository but is not listed.
- **`review status`** shows a round-by-round history for an issue: status, reviewer, timestamps, whether the reviewer pushed commits. It is read from the review chain, so it is the same on every clone.
- **`review fetch`** fetches the review chains, merges them with the local ones, and pushes the ones the remote lacks. Nothing is pruned locally. The interactive commands sync on their own; use this before scripting around review state.
- **`review sync`** brings an in-progress branch up to date (the current one is pre-selected). First it merges pending reviewer commits from `<IssueID>@review`; on conflict the merge is left in progress for you to resolve, then `git zf commit` concludes it. Then, for sub-task branches only, it merges the parent branch (`origin/<parent>`) into the sub-task; a conflict there is aborted and reported. A dirty working tree is refused for the first step (`git stash` first).
- **`review track`** registers the current branch without creating anything, for branches made with plain `git checkout`: a feature branch is recorded as in progress on its branch chain, a review branch records you as the reviewer on the review chain.

#### Reviews in the repository

A review is stored under `refs/zf/reviews/<issue>` as a chain of commits, one
per action (request, start, approve, reject, close). Two people acting on the
same review at once never get a rejected push: their actions are merged. The
review stays in the repository after the issue is closed. See
[docs/review-refs.md](docs/review-refs.md).

To require signed approvals, set `require-signed = true` under `[review]` in
`.git-zf.toml`: `review approve` then signs, and `issue close` refuses to merge
without an approval that `git verify-commit` accepts and that covers the commit
being merged. When the close is refused, `git zf review request` starts a new
round on the approved branch.

### Init

```
$ git zf init
```

Installs two hooks in the current repository:

- **pre-push** blocks pushes to branches locked for review. Bypass: `git push --no-verify`.
- **pre-commit** blocks commits on a feature branch while reviewer commits await incorporation. Bypass: `git commit --no-verify`.

It also configures every remote so that `git fetch` brings the reviews, issues and branch records stored under `refs/zf/`.

**Upgrading:** after installing a git-zf that stores reviews and branches as commit chains, re-run `git zf init` in each existing clone: it adds the fetch refspecs and removes the old tracking refs. A review that was in progress in the old format must be requested again (`git zf review request`). Branches are no longer read from `.git/git-zf.db`: upgrade every clone together, then run `git zf issue track` on each branch still in progress (see [docs/branch-refs.md](docs/branch-refs.md#upgrading-from-the-sqlite-store)).

Run it once per repository and per submodule (hooks go to the submodule's own git directory). **Run it from the main checkout, not from a linked worktree**: git reads hooks from the common git dir, and inside a worktree they would be written where git never looks (see [ROADMAP.md](ROADMAP.md)). Re-running is safe and idempotent. A foreign hook is never overwritten; a warning prints the snippet to add to it instead.

### Config

```
$ git zf config show   # active config path + effective config as JSON, token masked
$ git zf config init   # write the default config file interactively
```

`config init` writes to `$HOME/.git-zf.toml` when run outside a repo with no home config, otherwise a picker offers the home file or `<repo>/.git/.git-zf.toml`. An existing file is only overwritten after confirmation.

### Completion

```bash
git zf completion bash > ~/.local/share/bash-completion/completions/git-zf   # user-only
# or: sudo sh -c 'git zf completion bash > /etc/bash_completion.d/git-zf'   # system-wide
source ~/.bashrc
```

See the [Cobra shell-completion guide](https://cobra.dev/docs/how-to-guides/shell-completion/) for zsh, fish and PowerShell.

## Configuration

The config file is TOML. Two locations are supported; the repo-level file takes precedence:

| Location | Path | Notes |
|----------|------|-------|
| Home | `$HOME/.git-zf.toml` | Applied everywhere |
| Repo | `<repo>/.git/.git-zf.toml` | Inside `.git/` — never committed, can hold secrets |

`git zf config init` creates it, `git zf config show` prints the effective result. Every key is optional; the defaults are in [`config/default.toml`](config/default.toml).

### Commit types

```toml
[[commit-types]]
name = "feat"
desc = "A new feature"

[[commit-types]]
name = "fix"
desc = "A bug fix"
```

### Commit message

```toml
[commit-message]
template     = "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"
ref-format   = "Refs #%s"    # footer on regular commits on an issue branch
close-format = "Closes #%s"  # footer on the commit produced by `issue close`

[[commit-message.items]]
name     = "scope"
desc     = "Scope (users, db, poll…):"
form     = "input"

[[commit-message.items]]
name     = "subject"
desc     = "Concise description. Imperative, lower case, no final dot:"
form     = "input"
required = true

[[commit-message.items]]
name = "body"
desc = "Motivation for the change:"
form = "multiline"

[[commit-message.items]]
name = "footer"
desc = "Breaking changes and referenced issues:"
form = "multiline"
```

`template` is a Go `text/template` over the item names. `ref-format` and `close-format` are `fmt.Sprintf` patterns where `%s` is the issue ID (`ABC-42` for Redmine/Jira, `123` for GitHub/Forgejo). The defaults suit Redmine, GitHub, Forgejo, Gitea and GitLab; for Jira use `"Refs: %s"` and `"Fixes: %s"`.

### Branch naming

Branches are named `{issue-id}@{type}@{slugified-title}`, e.g. `ABC-42@feat@add-oauth-login`. The slug is capped at 50 characters so the ref stays under 100. It keeps ASCII letters and digits only; a title that leaves nothing (a title in a non-Latin script, for instance) gets the slug `issue`, as in `1a2b3c4@feat@issue`. The full title stays on the issue and in `issue list`.

```toml
[branch]
base         = "develop"     # default: auto-detected from <remote>/HEAD, then "main", then "master"
remote       = "upstream"    # default: auto-detected (see below)
use-worktree = true          # omit = ask at runtime; true = always worktree; false = always branch
worktree-dir = "~/worktrees" # omit = sibling of the repo root, named <repo>--<branch>
```

The default worktree path is a sibling of the repository root, `<repo>--<branch>` (e.g. `~/code/myapp--feat-123-login`). The repo name is taken from the remote URL when possible, so it is right even inside containers whose working directory is named differently.

**Remote auto-detection**, when `branch.remote` is not set:

| Repo state | Remote used |
|---|---|
| No remotes | Local-only mode — fetch is skipped, merges target the local base |
| Exactly one remote | That remote, whatever its name |
| Several remotes, one named `origin` | `origin` |
| Several remotes, none named `origin` | **Error** — set `branch.remote` |

#### Parallel branches per issue

The default name is unique per issue. For a second branch on the same issue (a spike, a parallel approach) pass `--variant=<label>` to `issue start` or `branch new`:

```bash
git zf issue start --variant=spike     # → ABC-42@feat@add-oauth-login@spike
```

The label is lowercased and slugged (letters, digits, hyphens) and must not be empty afterwards. When the default name collides with an existing branch, a prompt offers to **checkout** it, **create a variant**, or **abort**.

### Push

```toml
[push]
propose = true   # false disables the post-action "push now?" proposal everywhere
```

`--push` / `--no-push` on a command override the proposal for that run.

### Tracker integration

`issue start`, `issue list`, `issue close`, `branch prune-tracker` and the review commands can talk to **Redmine**, **GitHub** or **Forgejo / Gitea**:

```toml
[issue-tracker]
type     = "forgejo"                # "redmine", "github", "forgejo" or "gitea"
url      = "https://codeberg.org"
token    = "your_access_token"
projects = ["owner/repo"]           # optional filter; required for status updates on GitHub/Forgejo/Gitea
```

| Key | Description |
|-----|-------------|
| `type` | `"redmine"`, `"github"`, `"forgejo"` or `"gitea"` (the last two share one adapter). |
| `url` | Redmine: instance URL. GitHub: `https://api.github.com`, or `https://github.example.com/api/v3/` for Enterprise. Forgejo/Gitea: instance root, `/api/v1` is appended. |
| `token` | Redmine API key; GitHub personal access token with `repo` scope; Forgejo/Gitea access token with the `issue` scope. |
| `projects` | Optional list limiting which projects appear. Redmine: slugs or numeric IDs; every open issue of those projects is listed, whoever it is assigned to. GitHub/Forgejo/Gitea: `"owner/repo"`; only the issues assigned to you. Omitted = all issues assigned to you across the tracker. **Exactly one entry is required** to update issue status on GitHub/Forgejo/Gitea, whose issue endpoints are scoped to one repository. |

**Instance behind an HTTP Basic auth gate** (a reverse proxy protecting the whole site): put the gate credentials in the URL, `url = "https://user:password@forgejo.example.org"`. They are sent as `Authorization: Basic` for the proxy and the Forgejo token is passed as the `token` query parameter instead, which Forgejo/Gitea accept unless `[security] DISABLE_QUERY_AUTH_TOKEN = true` is set. A token in the query string can end up in the proxy's access logs.

When the tracker is unavailable or returns no issues, `issue start` falls back to manual input. Status pickers (`issue start`, `issue close`, review commands) show the live list of statuses from the tracker.

### Commit auto-fill from issue branch

On an issue branch such as `ABC-42@feat@add-oauth`, `git zf commit` and `issue close` pre-fill the form. The pre-fill is a hint; every field stays editable.

- `type` ← branch type
- `scope` ← issue ID, when the field exists
- `footer` ← `ref-format` (regular commit) or `close-format` (closing commit) applied to the issue ID, when the field exists
- `subject` ← `(ABC-42)`, only when neither `scope` nor `footer` exist

---

## LLM policy

This project is assisted by LLMs.
