# User documentation site — design

Date: 2026-10-28

## Intent

git-zf has one 470-line README that says almost everything, in one page with
no path through it. A new user cannot tell the install steps from the review
round from the config reference. This project turns that content into a
Docusaurus site that a user reads top to bottom on a first day and comes back
to as a reference, hosted on the author's own domain.

The site is for **users** of the tool: people who run `git zf` in their own
repositories. It is not a contributor guide. The reader knows git and has a
terminal; they do not know git-zf, and they may not know what a tracker
mirror or a linked worktree is.

## Goals

- Every command and every flag is documented, and that reference is generated
  from the binary so it cannot drift.
- The workflows are described as processes with steps, not as a list of
  commands: start an issue, commit, a review round, close, sub-tasks,
  worktrees, pull requests, pruning.
- The reader understands the difference between an issue and a branch, where
  git-zf keeps its state, and what the tracker sync does and does not do.
- The README shrinks to a short description, install, and a link to the site.

## Non-goals

- Deployment. The deliverable ends at a static build under `website/build`.
  The author syncs it to the domain with their own process. The site is
  served from the domain root: `baseUrl` is `/`, `url` is a placeholder the
  author fills in, `trailingSlash` is left at Docusaurus's default.
- A contributor or architecture guide. The specs under `docs/superpowers`
  stay where they are and are not part of the site.
- Versioned docs, i18n, search plugins, analytics. One version, English.
- Renaming any command. `branch close` keeps its name even though the docs
  must explain that it abandons rather than merges.

## Anti-goals

- A site that says the same thing as the README in a second place. After this
  project the site is the documentation and the README points at it.
- A reference page that is a thin copy of `--help`. If the generated reference
  is poor, the fix is a better help string in the code, not prose in the site.
- Pages that explain the on-disk format before the reader knows what a
  command does. The refs notes move to the end, under an "Under the hood"
  section.

## Constraints

- Go 1.25, Cobra v1.1.3 (which ships `github.com/spf13/cobra/doc`), Node 24,
  npm 11, all already on the machine through mise.
- The repository already has a `docs/` folder holding four refs notes and the
  superpowers specs. Docusaurus's default content folder is also `docs/`.
- Test conventions from `CLAUDE.md` apply to any Go change: every assertion
  in a `t.Run`, run with `mise exec -- go test`.
- The site is built with Docusaurus 3 (the current major), the classic
  preset, TypeScript config. No other theme or plugin unless a page cannot be
  written without it.

## What was decided

### 1. Where the site lives

Under `website/` at the repository root, a standard Docusaurus 3 project with
its own `docs/` content directory. The root `docs/` folder keeps the
superpowers specs only: the four refs notes (`branch-refs.md`,
`issue-refs.md`, `review-refs.md`, `merge-strategies.md`) move into the site
under `website/docs/under-the-hood/` and the README links that pointed at
them are updated. `website/node_modules`, `website/build` and
`website/.docusaurus` are gitignored.

The logo is `assets/git-zf-logo.webp`; the site uses it as its logo and
favicon source.

### 2. The command reference is generated

A small Go program, `website/gen/main.go` (or a `go run` target of the main
module, builder's choice), calls `doc.GenMarkdownTree` on the root command and
writes one Markdown file per command into `website/docs/reference/commands/`.
It runs from a `make docs-gen` target, never from an npm script: `npm run
build` only builds what is in `website/docs`, so the site builds from a fresh
clone without Go. The generated files are committed. A Go test (or `make
docs-check`) regenerates into a temporary directory and fails when the
committed files differ, so a changed help string cannot ship without its
reference page. (My call, from the builder check.)

The config is read when the command tree is built, not when it runs:
`cmd.GetRootCmd` calls `initConfig`, which overlays `$HOME/.git-zf.toml` and
`<repo>/.git/.git-zf.toml` on the embedded `config/default.toml`, and some
help text reads the result (`ProgName`, the commit types). A missing file is
not an error: `config.Load` skips files that do not exist, and not being in a
repository is tolerated by design, since `git zf version` must work anywhere.
There is no `PersistentPreRun` or `Execute` step to skip. So the generator
isolates itself by environment: it sets `HOME` to an empty temporary
directory and runs from a temporary directory that is not a git repository,
then calls `GetRootCmd` and passes the tree to `doc.GenMarkdownTree` without
`Execute`. Only the embedded defaults apply, on every machine.

Cobra's generator emits a header block, the long description, usage, flags,
inherited flags and a "SEE ALSO" list. The builder wraps it as needed so each
page gets Docusaurus front matter (`title`, `sidebar_label`) and the
auto-generated footer line is disabled (`DisableAutoGenTag`). The hidden
commands `review guard` and `review guard-commit` are not generated; the hook
page mentions them once.

Commands are named `git zf <sub>` in the generated pages, not `git-zf <sub>`,
since that is how users type them. The builder sets the root command's `Use`
for the generator run, or post-processes the output, whichever is simpler.

### 3. The help strings are expanded first

The generated reference is only as good as `Long` on each command. Today
these commands have a `Short` only, and the README holds the real
description. The first task of the plan is to give each of them a `Long`
that says what the command does, what it asks, what it refuses, and what it
pushes, written from the matching README paragraph:

- `review request`, `review start`, `review approve`, `review reject`,
  `review list`, `review status`, `review fetch`, `review sync`
- `issue list`
- `config`, `config init`, `config show`
- `install`, `uninstall`, `version`
- the group commands `issue`, `branch`, `review`

The root command's description ("Command line utility to standardize git
commit messages, golang version.") is replaced by the README's one-line
pitch. `Long` strings are plain text, wrapped at 80 columns, with no Markdown
beyond backticks, since they print in the terminal too.

### 4. Sidebar

```
Getting started
  Install                     release binary (goreleaser), `go install`, from
                              source; then `git zf install`, `git zf init`,
                              completion. The hidden hook commands `review
                              guard` / `guard-commit` are named here, once.
  First steps                 the menu, `git zf commit`, the commit form, config init
Concepts
  Issues and branches         the distinction below; the naming scheme
  Where git-zf keeps its state   refs/zf/*, no database, every clone sees it,
                              what `git fetch` brings after `init`
  Trackers and the mirror     tracker-first vs repo-first, what reconciles,
                              offline, two clones
Workflows
  Start an issue              `issue start` with and without id, the list keys,
                              worktree or branch, variants
  Commit                      the form, auto-fill, the review guard, push proposal
  Review a branch             the round, developer / reviewer sides, locks,
                              reviewer commits, `review sync`, signed approvals
  Close an issue              the seven steps, strategies, `issue close <id>`
  Sub-tasks
  Worktrees
  Pull requests               `branch pr`, forks, forge merge
  Pruning and abandoning      `branch prune`, `branch close`, `prune-tracker`
  Browse the log              the view, the six commands, `L`
Reference
  Commands                    generated, one page per command
  Configuration               every key of `config/default.toml`, by section
  Merge strategies            the table and when to pick which
Under the hood
  Branch refs, Issue refs, Review refs, Merge strategies internals
```

The sidebar is autogenerated from the directory tree, so the five top-level
folders need an order: each carries a `_category_.json` with its `label` and
`position` (1 to 5 in the order above), and each hand-written page carries a
`sidebar_position` in its front matter matching the order above. Without
this Docusaurus sorts the folders alphabetically. The generated command
pages carry no position and sort by name, which is the order `--help`
prints. (My call.)

Every workflow page ends with a "Commands used" list linking into the
reference. The command pages are generated and link back nowhere; the
workflow pages are the navigation. The Configuration and Merge strategies
reference pages are hand-written from the README's tables and
`config/default.toml`; no generator reads the config structs. (My call.)

`track` is registered under both `issue` and `review` from one constructor,
so two identical pages are generated; that is fine.

### 5. The issue-vs-branch concept, in the author's words

An issue is a piece of work with an identity: a ticket in the tracker, or an
issue stored in the repository when there is no tracker. A branch is where the
work happens in git. From the moment an issue is started it has a branch, so
the issue drives the flow: `issue start` names the branch after the issue,
`commit` pre-fills from it, `issue close` merges it into the base and offers
to update the tracker. A branch does not need an issue behind it: `branch
new` creates one with a typed ID that git-zf looks up nowhere, merged later
with `branch merge` and no tracker step.

Rule of thumb for the page: if there is a ticket, go through `issue`; if there
is not, go through `branch`.

The page states in one line, near the top, that `branch close` does **not**
merge: it records a branch as abandoned, without touching git branches. The
merge-and-update-the-tracker action is `issue close`. The two reference
pages are generated, so the cross-link lives in their `Long` strings.

### 6. The README afterwards

The README becomes: title and logo, the one-line pitch, a three-paragraph
description (what it wraps, what it stores in the repo, what trackers it
talks to), the install block, and a link to the site. Everything else is
deleted, not moved into a collapsed section. The `CLAUDE.md` references to
`docs/*.md` are updated to the new paths.

### 7. Content rules for the hand-written pages

- Second person, present tense, one idea per sentence.
- A page opens with what the reader can do after reading it.
- Terminal sessions are fenced blocks starting with `$ git zf …`. Keys in the
  TUI are in backticks (`s`, `tab`).
- A flag is mentioned in a workflow page only when it changes the flow (for
  instance `--variant`, `--parent`, `--base`). The full flag list is the
  generated reference.
- Admonitions (`:::note`, `:::warning`) only for things that lose data or
  cannot be undone: `c` twice in the list, `branch -D` after Squash and
  Rebase, a token in the query string.

## Left to the builder

- The exact split of README paragraphs into pages. The README is the source;
  nothing in it is dropped unless it is contributor-only, with one decided
  exception: the upgrade notes (re-running `git zf init` after the switch to
  commit chains, the SQLite store, the old review format, and the
  "Upgrading from the SQLite store" section of `branch-refs.md`) are dropped,
  not moved. The `ROADMAP.md` link in the Init section goes with them; the
  worktree caveat it pointed at stays as one sentence on the Install page.
- Whether the generator lives in `website/gen/` or as a `cmd/docs` hidden
  subcommand. Hidden subcommand is fine if it keeps the binary's dependencies
  unchanged (`cobra/doc` is already a transitive dependency).
- Docusaurus theme colours, navbar entries, footer. Keep the classic preset
  defaults with the logo; a dark mode toggle is on by default and stays.
- How the Makefile exposes the site: `make docs` (generate + build) and
  `make docs-serve` are suggested names.
- Order of the plan's tasks, except that expanding the help strings comes
  before generating the reference, and the README cut comes last.
