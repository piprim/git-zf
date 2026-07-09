# Git-ZF - Git Zen workFlow

<p align="center">
  <img src="./assets/git-zf-logo.webp" alt="Logo git-zf" width="272" />
</p>

> A TUI powered CLI that wraps a git-flow workflow (issue → branch → commit → close) with optional issue-tracker integration.

## Getting Started

### Prerequisites

- [Go 1.25+](https://go.dev/dl/)
- Git

### Install from source

```bash
git clone https://github.com/piprim/git-zf.git
cd git-zf
make
sudo make install      # copies binary to $(git --exec-path)
```

> On macOS with Homebrew Git the exec-path is user-writable; omit `sudo`.

### Install via `go install`

```bash
go install github.com/piprim/git-zf@latest
sudo git-zf install    # copies binary to $(git --exec-path)
```

> If `git --exec-path` is user-writable, omit `sudo`.

### Verify

```bash
git zf version
```

### Uninstall

`git zf uninstall`


## Usage

### Commit
```
$ git zf commit
```

```
Usage:
  git-zf commit [flags]

Flags:
  -a, --all                 stage all tracked modified/deleted files before committing
      --allow-empty         allow a commit with no changes
      --amend               replace the tip of the current branch
      --author string       override commit author as "Name <email>"
  -h, --help                help for commit
  -u, --include-untracked   stage untracked files before committing
      --no-push             skip the post-action push proposal
  -n, --no-verify           bypass pre-commit and commit-msg hooks
      --push                push the resulting branch to the remote after the action (skips the prompt)
  -s, --signoff             add Signed-off-by trailer to the commit message
  -y, --yes                 skip the commit options form and assume defaults
```

If any commit flag is passed, the options page of the TUI form is skipped and the flags are used directly.

**Review guard** — if the current branch is a feature branch (not `<IssueID>@review`) with unincorporated reviewer commits (a decided review — `approved` or `changes_requested` — where `<IssueID>@review` carries commits not yet in your branch), `git zf commit` offers to merge them in before opening the commit form. Declining aborts the commit with a hint to run `git zf review sync` first. `--no-verify` skips the guard entirely, same as it bypasses the pre-commit hook (see [Init](#init)).

### Issue
```
$ git zf issue
$ git zf issue start
$ git zf issue list
$ git zf issue close
```

**`issue start`** — start work on an issue: optionally fetch open issues from a tracker (Redmine), or enter an issue ID, title, and type manually. A properly named branch is created and checked out, **or a git worktree is created** so the main working tree stays untouched. Branch/worktree state is tracked in `.git/git-zf.db`. Pass `--variant=<label>` to create a parallel branch on an issue that already has one (see [Parallel branches per issue](#parallel-branches-per-issue)).

After the issue is selected, a prompt asks whether to create a plain branch or a worktree. When a worktree is created the command prints the path and a `cd` hint since the shell cannot change directory automatically:

```
Created worktree "feat/ABC-42-add-login" at "/home/user/code/myapp--feat-ABC-42-add-login" (based on "main")
Run 'cd /home/user/code/myapp--feat-ABC-42-add-login' to begin working.
```

The choice can be pinned via `branch.use-worktree` in the config (see [Branch naming](#branch-naming)).

If a tracker is configured, `issue start` pre-selects fetching from the tracker; after picking an issue you can update its status to "In Progress" in one step.

**`issue list`** — list issues enriched with local branch data. When a tracker is configured it is the primary source; the local store is used as fallback.

Columns: Issue ID · [Project] · Title · Branch · Local Status · Tracker Status · Created. The Project column appears automatically when issues span more than one project. `"∅"` means the issue has no local branch yet; `"N.A."` means no tracker is configured.

`issue list` flags:
```
--status string   filter by status: open, closed, all (default: open)
--stdout          print table to stdout without TUI
--json            print JSON array to stdout
```

In the interactive TUI:
- **`/`** — filter rows in real time (matches any column, case-insensitive); **`Enter`** to confirm, **`Esc`** to clear
- **`tab`** — cycle status filter (Open → Closed → All)
- **`p`** — open the project picker (↑/↓ or j/k to navigate, **`Enter`** to confirm, **`Esc`** to cancel)
- **`q`** — quit

**`issue close`** — close an in-progress issue: pick from the list of in-progress branches (the currently checked-out branch is pre-selected), merge into the base branch, update the local store, and optionally update the tracker status and delete the local branch.

The close flow:
1. A conflict dry-run is performed via `git merge-tree` — if conflicts are detected the command aborts without touching anything.
2. Choose merge strategy: **Rebase** (default, recommended — single clean commit, submodule-safe), **Squash** (`git merge --squash`, fast but not submodule-safe), or **Classic** (`--no-ff`, preserves full history). For all three strategies the final commit is composed through the commitizen TUI form, pre-filled from the branch's issue ID and type.
3. Confirm the merge. After a successful merge the branch is marked as `merged` in the local store and the issue is marked as `closed`.
4. If a tracker is configured, a status picker lets you update the remote issue status (or skip).
5. Optionally delete the local branch. Safe delete (`-d`) is used for classic merges; force delete (`-D`) for Squash and Rebase (neither preserves ancestry, so git requires `-D`).

#### Merge strategies

| Strategy | Mechanism | History on base | Submodule-safe |
|---|---|---|---|
| **Rebase** *(default)* | Real `git merge <remote>/<base>` + `git reset --soft <remote>/<base>` | one clean commit | ✅ yes |
| **Squash** | `git merge --squash` | one commit, no merge parent | ⚠️ no — `--squash` is known to mishandle submodule gitlinks |
| **Classic** | `git merge --no-ff --no-commit` + commitizen form (FF-syncs local base against `origin/<base>` first) | merge commit + full feature history | ✅ yes |

#### Rebase strategy — detailed flow

Rebase produces the same end state as Squash (one clean commit on the local base branch) but uses a different mechanic under the hood that correctly handles submodule pointers. The work happens on your feature branch, then fast-forwards onto local base. Concretely:

```
1. Pre-flight
   - `git status --porcelain --untracked-files=no` → abort if dirty.
     (Untracked files are intentionally ignored — they survive rollback
     and don't put your work at risk.)

2. Setup
   - Checkout the feature branch (idempotent — no `post-checkout` hook
     fires if you're already there).
   - Capture the feature tip SHA for safe rollback.
   - `git fetch <remote>` to pick up the latest remote base (no-op when
     no remote is configured — see `branch.remote` below).
   - `git merge-base --is-ancestor feature <remote>/<base>` → if feature
     has no commits ahead of `<remote>/<base>`, abort cleanly
     ("already integrated?"). Avoids producing an empty commit.
   - `git merge-tree` dry-run against `<remote>/<base>` → abort with the
     conflict file list if the endpoint can't merge cleanly.

3. Execute
   - `git merge --no-edit <remote>/<base>` — a *real* merge, which handles
     submodule gitlinks correctly. `--no-edit` suppresses $EDITOR for
     the transient merge-commit message. When no remote is configured,
     merges against the local `<base>` branch directly.
   - `git reset --soft <remote>/<base>` — collapse the merge into one
     staged diff. HEAD moves back, working tree stays at the merged state,
     the index is fully staged.

4. TUI commit form
   - The commitizen form opens pre-filled with type, scope, and the
     subject `Squashed close of <feature-tip> into <base-tip>.`
   - Submit → `git commit` lands one clean commit on the feature branch.
   - Esc / Ctrl+C / hook rejection → atomic rollback: feature is reset
     to its captured original tip via `git reset --hard`. You see
     `Rolled back: feature branch "<name>" restored to <sha>` on stderr.

5. Deploy
   - Checkout local `<base>` (idempotent).
   - `git merge --ff-only feature` to land the new commit.

6. Bookkeeping
   - Update local store and (if configured) tracker; prompt to delete
     the feature branch.
```

##### Why a real `git merge` instead of `git rebase`

A `git rebase` mechanism would have replayed the feature's commits one at a time. If commit #2 conflicts but commit #4 fixes it, the merge-tree dry-run reports the endpoint as clean — but the rebase still halts mid-way on commit #2, dumping you into a detached HEAD with conflict markers. A real merge looks at the *endpoint* of both branches, which is exactly what merge-tree predicts, so the dry-run's "clean" verdict is a guarantee.

Submodules are handled correctly because the gitlink quirk only affects `merge --squash`. Plain `git merge` resolves submodule pointers three-way like any other file.

##### Rollback semantics

The rebase orchestrator captures the feature ref's SHA *before* mutating anything. From that point until the final commit lands, any failure — TUI abort, pre-commit hook rejection, commit-msg hook rejection, signing failure — triggers `git reset --hard <featureOrigSHA>` on the feature branch. The feature is restored atomically and you see a `Rolled back: …` message on stderr. If the rollback itself fails, both the original error and the rollback error are surfaced so you know the repo is in a half-state and why.

The *post-commit* failure mode is treated differently. If the commit landed on the feature branch but `git merge --ff-only` refuses (because local `<base>` has diverged from the remote), the commit is **not** rolled back — it already exists as a clean, valid commit on the feature branch. Instead you see:

```
Commit created on "<feature>" but local <base> has diverged from <remote>/<base>.
Run `git pull --ff-only` on <base>, then `git merge --ff-only <feature>` to land it.
```

The store and tracker are not updated, the delete-branch prompt is skipped, and the close exits cleanly. You reconcile local base manually and FF the feature commit yourself.

#### Classic strategy — detailed flow

1. **Pre-flight** (shared with Rebase):
   - Refuses if the working tree has tracked modifications or staged changes.
   - Checks out the feature branch, captures its tip SHA.
   - Detects the configured remote and runs `git fetch <remote>` (no-op when no remote is configured).
   - Computes `remoteBase` as `<remote>/<base>` (or `<base>` when local-only).
   - Aborts if the feature branch has no commits ahead of `remoteBase` (already integrated).
   - Runs a `merge-tree` dry-run of feature vs `remoteBase`; aborts with the conflict file list if any conflict is predicted.

2. **Sync local base** (skipped when no remote):
   - Checks out the local base branch.
   - Runs `git merge --ff-only <remoteBase>` to bring local base up to `origin/<base>`.
   - Refuses with a remediation message if local base has diverged from `origin/<base>` — the operator runs `git pull --ff-only` and retries.

3. **Resolve integration target**: looks up `refs/heads/<base>` for the prefill subject SHA.

4. **Stage the merge**: runs `git merge --no-ff --no-commit <feature>`. `MERGE_HEAD` / `MERGE_MSG` are left in place; nothing is committed yet.

5. **TUI form** (pre-filled):
   - subject: `Merge <feature-tip-short> into <base-tip-short>.`
   - type / scope / authors default from the branch metadata, identical to Rebase / Squash.

6. **Commit**: `git commit -F <msgfile>` plus options from the form. Because `MERGE_HEAD` is present, Git produces a two-parent merge commit on base with the form-supplied message.

7. **Bookkeeping**: updates the local store and (optionally) the tracker, then offers the delete-branch prompt — unchanged from the other strategies. Classic uses safe delete (`-d`) since feature is now an ancestor of base.

If any step between 4 and a successful commit fails — TUI abort, pre-commit hook rejection, signing failure — `git merge --abort` is run automatically to clear `MERGE_HEAD` / `MERGE_MSG` and restore the working tree. The operator is left on base in a clean state.

##### When to choose each strategy

- **Rebase** — your repo has submodules, or you want a clean linear history with one commit per issue. Recommended default.
- **Squash** — no submodules involved, you want the existing `git merge --squash` semantics (fast, fewer git operations).
- **Classic** — you want to preserve the feature's full commit history on the base branch via a merge commit. The merge commit's message is composed through the commitizen TUI form (same UX as Rebase/Squash). Local base is FF-synced against `origin/<base>` before the merge; Classic refuses to merge into a base that has diverged from origin (operator runs `git pull --ff-only` and retries). Use when intermediate commits have value (large features, bisect surface, audit trail).

### Branch
```
$ git zf branch new            # create a branch with manual input
$ git zf branch list           # list tracked branches
$ git zf branch merge          # merge a branch via TUI
$ git zf branch prune          # clean up stale DB records (local-only)
$ git zf branch prune-tracker  # reap branches whose tracker issue is closed
```

`branch new` is the same flow as `issue start` but pre-selects manual input. Pass `--variant=<label>` to create a parallel branch on an issue that already has one (see [Parallel branches per issue](#parallel-branches-per-issue)).

`branch list` flags:
```
--status string   filter by status: in_progress, merged, closed, all (default: in_progress)
--stdout          print table to stdout without TUI
--json            print JSON array to stdout
```

`branch prune` flags:
```
--base string   base branch for merge detection (default: auto-detected)
--dry-run       show what would be pruned without executing
```

`branch prune-tracker` discovers local branches whose issue ID (regex-extracted from the branch name) is closed in the configured tracker, then offers per-branch reap actions. Successful reaps flip the corresponding store row to status `closed` (distinct from `merged` — which only the local `branch prune` produces when the tip is reachable from base).

By default, an interactive huh form is presented with one selector per candidate (safe-delete / force-delete / skip), with `safe-delete` pre-selected. Pass `--safe-delete`, `--force-delete`, or `--skip-delete` to apply that action to every candidate non-interactively (CI / scripting). The three action flags are mutually exclusive.

The discovery is per-issue — one tracker lookup per local branch whose name parses as an issue ID. Branches that don't parse are silently skipped; tracker lookup failures (404 / transport / auth) print a `WARN:` line and skip that branch without aborting the run.

`branch prune-tracker` flags:
```
--base string     base branch name to exclude from candidate discovery (default: auto-detect)
--dry-run         show what would be done; no prompts, no mutations
--safe-delete     non-interactive: apply `git branch -d` to every match
--force-delete    non-interactive: apply `git branch -D` to every match
--skip-delete     non-interactive: never touch refs; only flip store status to closed
```

Examples:
```
$ git zf branch prune-tracker --dry-run                # preview only
$ git zf branch prune-tracker --safe-delete            # CI: safe-delete every closed-issue branch
$ git zf branch prune-tracker --force-delete --base main
```

### Review
```
$ git zf review start     # reviewer: create <IssueID>@review from the locked snapshot
$ git zf review request   # developer: submit an issue branch for review (locks it)
$ git zf review approve   # reviewer: approve — the branch is ready to close
$ git zf review reject    # reviewer: request changes — unlocks the branch
$ git zf review list      # list issues currently in review or approved
$ git zf review status    # show the round-by-round history for an issue
$ git zf review fetch     # fetch review refs from the remote, reconcile the store
$ git zf review sync      # bring a branch up to date: reviewer commits + parent drift
$ git zf review track     # register a branch created with plain git checkout
```

Peer-to-peer code review with no server-side component. Review state lives in git refs under `refs/zf/reviews/<IssueID>` — a JSON blob holding the status, the round number, the feature HEAD SHA captured at lock time, and the reviewer identity — pushed to and fetched from the remote. The refs are the source of truth; the local SQLite store is only a cache. Ref writes and pushes use compare-and-swap, so two machines cannot silently overwrite each other's decision.

A review round:

1. **Developer** — `git zf review request`. A picker lists in-progress branches not already in review; the chosen issue gets a review ref with status `in_review`. The feature branch is now **locked**: the pre-push hook installed by [`git zf init`](#init) rejects pushes to it until the reviewer decides (bypass: `git push --no-verify`).
2. **Reviewer** — `git fetch && git zf review start`. A picker lists issues awaiting review; git-zf creates `<IssueID>@review` at the exact SHA captured at lock time and records the reviewer identity in the ref. Review the code; optionally commit fixes on the `@review` branch.
3. **Reviewer decides**:
   - `review approve` — status becomes `approved`; the developer can now run `git zf issue close`. Commits the reviewer pushed on the `@review` branch are incorporated on close: a fast-forward when the feature branch hasn't moved, or an automatic merge when it has diverged and merges cleanly. If the diverged merge conflicts, close refuses with a hint to run `git zf review sync`, resolve, and retry — close never leaves a merge in progress.
   - `review reject` — status becomes `changes_requested` and the feature branch is unlocked. An empty `@review` branch is deleted (locally and on the remote); if it carries reviewer commits it is kept so the developer can inspect (`git log <feature>..<IssueID>@review`) and cherry-pick before the next round. After a reject that leaves reviewer commits behind, **new commits on the feature branch are blocked** (the commit guard and the pre-commit hook installed by [`git zf init`](#init) both refuse) until `git zf review sync` incorporates them; bypass: `git commit --no-verify` / `git zf commit --no-verify`.
4. **Next round** — the developer pushes fixes and runs `git zf review request` again: the round counter increments and any stale `@review` branch from the previous round is removed. If that branch still carries unincorporated reviewer commits, the request refuses to delete them; an interactive run offers to merge them on the spot before proceeding.

If an issue tracker is configured and the issue originated from it, `request`, `approve`, and `reject` also propose updating the issue's tracker status after the action.

`review request`, `review approve` and `review reject` flags:
```
--no-push   skip the post-action push proposal
--push      push the resulting branch to the remote after the action (skips the prompt)
```

**`review list`** — reads `refs/zf/reviews/*` directly (works on a fresh clone with an empty store) and prints every issue whose status is `in_review` or `approved`, with its round number.

**`review status`** — picker over issues with review history, then a round-by-round listing: status, reviewer, opened/resolved timestamps, and whether the reviewer pushed commits. The latest round is reconciled from the ref, so decisions made on another machine show up.

**`review fetch`** — fetch `refs/zf/reviews/*` from the remote (pruning refs deleted remotely) and reconcile the local store. The interactive review commands fetch on their own; use this before scripting around review state.

**`review sync`** — brings any in-progress branch up to date; the picker pre-selects the branch you're currently on. Two steps run in order:

1. **Reviewer commits** — if a decision (`approved` or `changes_requested`) says `<IssueID>@review` carries commits not yet in the feature branch, they are merged in directly, without a confirm prompt (unlike the offer flow in [Commit](#commit), sync is picker-driven, not interactive). On conflicts the merge is left in progress with conflict markers in the working tree — resolve them, then run `git zf commit` to conclude the merge.
2. **Parent drift** — for sub-task branches only: merges the parent integration branch (read as `origin/<parent>` to catch teammates' pushes) into the sub-task. On conflicts the merge is aborted and reported — nothing is left half-merged.

A dirty working tree refuses step 1 outright, with a `git stash` → `git zf review sync` → `git stash pop` hint; a parent-only sync (step 2) is not pre-flighted for a dirty tree.

**`review track`** — register the current branch in the git-zf store without creating anything. Use it when the branch was created with plain `git checkout` instead of `git zf issue start` / `git zf review start`, so the review commands can find it.

There are also two hidden internal subcommands. `review guard <branch>` — the pre-push hook calls it for every pushed branch, and it exits non-zero when the branch is locked (`in_review`). `review guard-commit` — the pre-commit hook calls it before every commit, and it exits non-zero when the current branch has reviewer commits on `<IssueID>@review` awaiting incorporation. Both fail open on store or fetch errors; `review guard-commit` also exempts `@review` branches themselves and commits that conclude an in-progress merge (that's exactly how incorporation happens).

### Init
```
$ git zf init
```

Install the git-zf pre-push and pre-commit hooks into the current repository (see [Review](#review)):

- **pre-push** calls `git zf review guard` before every push, blocking pushes to branches that are locked for code review. Bypass: `git push --no-verify`.
- **pre-commit** calls `git zf review guard-commit` before every commit, blocking commits on a feature branch while reviewer commits on `<IssueID>@review` await incorporation. Bypass: `git commit --no-verify`.

Both hooks share the same install policy:

- Run `git zf install` first so the `git zf` binary is available in the git exec-path, then run `git zf init` once per repository (and per submodule).
- Works correctly in git submodules and linked worktrees: each hook is written to the repository's real git directory (resolved via `git rev-parse --git-dir`), not the parent repo.
- Re-running the command is safe: a hook that is already installed and up to date is left untouched (idempotent, checked independently per hook).
- If a foreign hook already exists for either file, it is **not** overwritten; a warning prints the snippet to add to your existing hook instead (`git zf review guard "..."` for pre-push, `git zf review guard-commit || exit 1` for pre-commit).

### Config
```
$ git zf config show
$ git zf config init
```

**`config show`** — print the active config file path followed by the effective configuration as formatted JSON. The `issue-tracker.token` field is masked as `***` so the output is safe to share or paste into issues.

Example output:
```
Config file: /home/user/.git-zf.json

{
  "commit-types": [...],
  ...
  "issue-tracker": {
    "type": "redmine",
    "url": "https://redmine.example.com",
    "token": "***"
  }
}
```

If no config file is found the header reads `no config file found (built-in defaults apply)`.

**`config init`** — interactively write the default config file. The destination is chosen based on context:

- **Outside a git repo, no home config**: the home path is selected automatically without a prompt.
- **Inside a git repo or a home config already exists**: a TUI picker lets you choose between `$HOME/.git-zf.json` and `<repo>/.git/.git-zf.json`. The repo-level file lives inside `.git/` so it is never committed and cannot leak secrets. It takes precedence over the home file when present.

If the target file already exists a confirmation prompt is shown before overwriting.

### Completion

For `Bash` users, set up completion with these steps:

1. **Generate Completion Script**  
   `git zf completion bash > ~/git-zf-completion.bash`
2. **Install System-Wide (recommended)**  
   `sudo mv ~/git-zf-completion.bash /etc/bash_completion.d/`
2. **Or Install User-Only**  
   ```bash
   mkdir -p ~/.local/share/bash-completion/completions
   mv ~/git-zf-completion.bash ~/.local/share/bash-completion/completions/git-zf
   ```
3. **Reload Your Shell**  
   `source ~/.bashrc`

Take a look at the [Cobra Shell-Specific Configuration](https://cobra.dev/docs/how-to-guides/shell-completion/) for the other supported shells.

### All commands
```txt
Usage:
  git-zf [command]

Available Commands:
  branch      Manage local branches
  commit      Record changes to the repository
  completion  Generate completion script
  config      Manage git-zf configuration
  help        Help about any command
  init        Initialize git-zf in the current repository (installs the pre-push and pre-commit hooks)
  install     Install this tool to git-core as zf
  issue       Manage issues
  review      Manage the code review lifecycle for an issue branch
  uninstall   uninstall zf from git-core
  version     Print version information and quit

Flags:
  -d, --debug   debug mode, print debug info to stdout
  -h, --help    help for git-zf
```

## Configuration

Config file: `.git-zf.json` (JSON). Two locations are supported; the repo-level file takes precedence over the home file:

| Location | Path | Notes |
|----------|------|-------|
| Home | `$HOME/.git-zf.json` | Applied everywhere |
| Repo | `<repo>/.git/.git-zf.json` | Inside `.git/` — never committed, can contain secrets |

Use `git zf config init` to create the file interactively, or `git zf config show` to inspect the currently active configuration.

The default configuration is embedded in [`config/default.toml`](https://github.com/piprim/git-zf/blob/master/config/default.toml).

### Commit types

Override the list of commit types shown in the type selector:

```json
{
  "commit-types": [
    { "name": "feat",  "desc": "A new feature" },
    { "name": "fix",   "desc": "A bug fix" },
    { "name": "chore", "desc": "Build process or tooling changes" }
  ]
}
```

### Commit message

Override the form fields, the message template, and/or the issue-reference formats:

```toml
[commit-message]
template    = "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"
ref-format   = "Refs #%s"   # footer text when committing on an issue branch (not closing)
close-format = "Closes #%s"  # footer text when closing an issue via `issue close`

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

`ref-format` and `close-format` are Go `fmt.Sprintf` patterns; the single `%s` verb is replaced by the issue ID (e.g. `ABC-42` for Redmine/Jira or `123` for GitHub).

| Key | Default | When used |
|-----|---------|-----------|
| `ref-format` | `"Refs #%s"` | Regular commits on an issue branch (`git zf commit`) |
| `close-format` | `"Closes #%s"` | Merge commits produced by `git zf issue close` |

Examples for common trackers:

| Tracker | `ref-format` | `close-format` |
|---------|-------------|----------------|
| Redmine / GitHub / GitLab | `"Refs #%s"` *(default)* | `"Closes #%s"` *(default)* |
| Jira | `"Refs: %s"` | `"Fixes: %s"` |
```

### Branch naming

Branch names follow the format `{issue-id}@{type}@{slugified-title}`, e.g.:

```
ABC-42@feat@add-oauth-login
```

The slugified-title is capped at 50 characters (with any dangling trailing hyphen
stripped) so the full ref stays comfortably under 100 characters in the worst case.

#### Parallel branches per issue

The default branch name is unique per issue. When you genuinely need two branches
on the same issue (a throwaway spike, parallel approach exploration, etc.), pass
`--variant=<label>`:

```bash
git zf issue start --variant=spike
# → ABC-42@feat@add-oauth-login@spike

git zf branch new --variant=approach-b
# → ABC-42@feat@add-oauth-login@approach-b
```

The label is lowercased and slugged (letters, digits, and hyphens only); it must
be non-empty after slugging.

When a default name collides with an existing branch, an interactive prompt
offers three choices: **Checkout** the existing branch, **Create a variant**
(asks for a label), or **Abort**. Legacy branches with random-hex suffixes (from
git-zf versions before this change) keep parsing without any conversion needed.

To override the base branch, remote name, or worktree behaviour:

```toml
[branch]
base         = "develop"     # default: auto-detected from <remote>/HEAD, then "main", then "master"
remote       = "upstream"    # default: auto-detected (see below)
use-worktree = true          # omit = ask at runtime; true = always worktree; false = always branch
worktree-dir = "~/worktrees" # omit = sibling of repo root
```

`use-worktree` is a three-state setting:

| Value | Behaviour |
|-------|-----------|
| omitted | A TUI prompt asks at runtime (default) |
| `true` | Always create a worktree, skip the prompt |
| `false` | Always create a plain branch, skip the prompt |

When `worktree-dir` is omitted the worktree is placed as a sibling of the repository root, named `<repo>--<branch>` (e.g. `~/code/myapp--feat-123-login`). The repo name is resolved from the remote URL when possible, so the path is correct even inside Docker containers where the working directory name may differ from the actual repository name.

**Remote auto-detection** — when `branch.remote` is not set, git-zf resolves the remote as follows:

| Repo state | Remote used |
|---|---|
| No remotes | Local-only mode — fetch is skipped, branches merged against local base |
| Exactly one remote | That remote, regardless of its name |
| Multiple remotes, one named `"origin"` | `"origin"` (standard git convention) |
| Multiple remotes, none named `"origin"` | **Error** — set `branch.remote` explicitly |

Set `branch.remote` whenever your primary remote is not named `"origin"` or when you have multiple remotes and want to pin one explicitly.

### Tracker integration

`git zf issue start` and `issue list` can fetch open issues assigned to you from a project tracker. Supported trackers: **Redmine** and **GitHub**.

Add an `issue-tracker` section to `.git-zf.json`:

**Redmine**
```json
{
  "issue-tracker": {
    "type": "redmine",
    "url": "https://redmine.example.com",
    "token": "YOUR_API_KEY"
  }
}
```

**GitHub** (public or GitHub Enterprise)
```json
{
  "issue-tracker": {
    "type": "github",
    "url": "https://api.github.com",
    "token": "ghp_yourPersonalAccessToken"
  }
}
```

For GitHub Enterprise, set `url` to your instance API root, e.g. `https://github.example.com/api/v3/`.

| Key | Description |
|-----|-------------|
| `type` | Tracker type: `"redmine"` or `"github"`. |
| `url` | Base URL of the tracker API. For GitHub use `https://api.github.com`. |
| `token` | API key (Redmine) or personal access token with `repo` scope (GitHub). |
| `projects` | Optional list of projects to show. Redmine: project slugs or numeric IDs. GitHub: `"owner/repo"` strings. When omitted all assigned issues are shown. |

**Filtering by project**

Use `projects` to limit which repositories or Redmine projects appear in `issue list`:

```json
{
  "issue-tracker": {
    "type": "github",
    "url": "https://api.github.com",
    "token": "ghp_...",
    "projects": ["myorg/backend", "myorg/frontend"]
  }
}
```

> **Note for `UpdateIssueStatus` via GitHub:** because GitHub's update endpoint requires the `owner/repo`, exactly one entry must be present in `projects` when using `issue close` with a GitHub tracker.

When a tracker is configured:
1. `issue start` asks whether to fetch issues from the tracker.
2. If yes, open issues assigned to you are listed; type any key to filter the list, pick one and select a branch type.
3. After the branch is created, a status picker shows the live list of statuses from the tracker; pick one or skip.
4. If the tracker is unavailable or returns no issues, the flow falls back to manual input.
5. `issue close` shows the same live status picker after merging, so you can move the issue to "Done", "Closed", or any other status in a single step.

### Commit auto-fill from issue branch

When you run `git zf commit` on an issue branch (e.g. `ABC-42@feat@add-oauth`), the issue ID and branch type are automatically pre-filled into the commit form. The pre-fill is a hint only; you can edit or clear any field before confirming.

**Regular commits** (`git zf commit`):
- `scope` ← issue ID (if the field exists)
- `footer` ← `ref-format % id` (e.g. `Refs #ABC-42`) — always set when the field exists, independent of scope
- `subject` ← `(ABC-42)` — fallback only when neither `scope` nor `footer` exist

**Closing commits** (`git zf issue close`):
- `scope` ← issue ID (if the field exists)
- `footer` ← `close-format % id` (e.g. `Closes #ABC-42`) — always set when the field exists
- `subject` ← `(ABC-42)` — fallback only when neither `scope` nor `footer` exist

The formats used for `footer` are configurable via `ref-format` and `close-format` (see [Commit message](#commit-message)).

---

## LLM policy

This project is assisted by LLMs.
