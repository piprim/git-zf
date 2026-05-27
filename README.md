# Git-ZF - Git Zen workFlow

<p align="center">
  <img src="./assets/git-zf-logo.webp" alt="Logo git-zf" width="272" />
</p>

> Command line utility to manage git workflow, connect to issue trackers, and standardize commit messages through a TUI.

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
  -a, --all             stage all tracked modified/deleted files before committing
      --allow-empty     allow a commit with no changes
      --amend           replace the tip of the current branch
      --author string   override commit author as "Name <email>"
  -h, --help            help for commit
  -n, --no-verify       bypass pre-commit and commit-msg hooks
  -s, --signoff         add Signed-off-by trailer to the commit message
  -y, --yes             skip the commit options form and assume defaults
```

If any commit flag is passed, the options page of the TUI form is skipped and the flags are used directly.

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

#### Testing the close flow

The close flow is end-to-end tested in `cmd/issue/close_e2e_test.go`. Tests
construct a real on-disk repo, a seeded SQLite store, and the in-process
tracker fake at `tracker/fake/`, then drive the flow with a `scriptedPrompter`
that returns canned answers instead of opening huh forms.

To exercise just the close-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v

When adding a new merge strategy or changing the merge/store/tracker
sequencing, add a corresponding E2E test alongside the existing happy-path
and failure-mode tests.

#### Testing the start flow

The issue-start flow (used by both `issue start` and `branch new`) is
end-to-end tested in `cmd/issue/start_e2e_test.go`. The pattern mirrors the
close-flow tests: a real on-disk repo, the fake tracker at `tracker/fake/`,
and a `scriptedStartPrompter` that returns canned answers for every huh form.

To exercise just the start-flow tests:

    mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_" -v

When adding a new toggle, confirm, or picker form, extend `StartPrompter` and
add a matching E2E test alongside the existing happy-path and failure-mode
tests.

#### Testing the prune flow

The branch-prune flow is end-to-end tested in `cmd/branch/prune_e2e_test.go`.
Tests construct a real on-disk repo + seeded store, then drive the flow with
a `scriptedPrunePrompter` (canned confirm responses) or
`autoConfirmPrunePrompter` (mirrors `--yes`).

To exercise just the prune-flow tests:

    mise exec -- go test ./cmd/branch/... -run "^TestRunPrune_" -v

For non-interactive use (CI, cron), pass `--yes` to skip the confirmation
prompt:

    git zf branch prune --yes

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
--base string     base branch (default: auto-detect) — used by --safe-delete's ancestry check
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
  install     Install this tool to git-core as git-zf
  uninstall   uninstall this tool from git-core
  issue       Manage issues
  version     Print version information and quit

Flags:
  -d, --debug   debug mode, output debug info to debug.log
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

Override the form fields and/or the message template:

```json
{
  "commit-message": {
    "items": [
      { "name": "scope",   "desc": "Scope (users, db, poll…):", "form": "input" },
      { "name": "subject", "desc": "Concise description. Imperative, lower case, no final dot:", "form": "input", "required": true },
      { "name": "body",    "desc": "Motivation for the change:", "form": "multiline" },
      { "name": "footer",  "desc": "Breaking changes and referenced issues:", "form": "multiline" }
    ],
    "template": "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"
  }
}
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

When you run `git zf commit` on an issue branch (e.g. `ABC-42@feat@add-oauth`), the issue ID is automatically pre-filled into the commit form — into `scope` if that field exists, otherwise `footer`, otherwise `subject` as a fallback. The pre-fill is a hint only; you can edit or clear it before confirming.
