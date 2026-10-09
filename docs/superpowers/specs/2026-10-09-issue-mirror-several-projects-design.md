# Design: the issue mirror over several tracker projects

**Date:** 2026-10-09
**Status:** approved design, not implemented
**Builds on:** `2026-10-06-issue-tracker-mirror-design.md`

## Overview

The mirror ties one repository to exactly one tracker project. This spec
lifts that: a repository mirrors several projects of one tracker. Roadmap
item 1.a names the three things that follow, and the code confirms each:

- **Project-aware tracker calls.** On GitHub and Forgejo, every per-issue
  call resolves the repository through the single configured project. Issue
  numbers restart per repository there, so a number alone no longer names an
  issue. Redmine numbers are global: only its listing and creation need the
  project.
- **Project-qualified slugs.** An imported issue is shown and branched as its
  tracker number. Two projects on GitHub or Forgejo can both have a `#42`,
  which collides in `refs/zf/branches/42` and in branch names.
- **A project picker in `issue new`.** A repo-born issue is exported by the
  reconcile, which must know which project to create it in. Today the record
  has no notion of a target project.

Already in place: the record's tracker link and the frozen import root both
carry the near slug, so the issue data model needs little. The "assigned to
me" listing already loops over several projects.

**Decisions taken during brainstorming:**

- **Slug policy:** an imported issue's display ID carries its near slug
  (`zf-42`) when, and only when, the repository's linked records, imported or
  exported, span more than one project. One project keeps today's bare numbers; nothing already
  written changes when a second project is added, and the flag never flips
  back. Rejected: qualifying always when the
  mirror is on (every single-project repository pays the transition today);
  qualifying only on trackers with per-project numbering (a rule that depends
  on the tracker type); qualifying every project but the default (a bare
  number would change meaning when the config is reordered).
- **Default project:** the first `[[issue-tracker.projects]]` entry. The
  picker preselects it, `issue new` without `--project` uses it, and a record
  with no project is exported to it. Rejected: requiring an explicit project
  (an old record would be stuck without a new `set_project` op).
- **Tracker interface:** a `project` parameter on the six per-project
  methods (approach 1). Rejected: a project-scoped sub-interface returned by
  `Project(near)` (twice the diff for the same behaviour); the project
  encoded in the issue ID string (one string with two meanings).
- One tracker type per repository stays. The near/far config form stays.
  Config backward compatibility is still not required.

## Section 1: configuration

- `mirror = true` needs a tracker type and **at least one** project, instead
  of exactly one. The load error says so.
- The first `[[issue-tracker.projects]]` entry is the **default project**.
  `IssueTrackerConfig` gains:

  ```go
  // DefaultProject is the near slug of the first configured project, "" with none.
  func (c *IssueTrackerConfig) DefaultProject() string
  // Project returns the configured project with this near slug.
  func (c *IssueTrackerConfig) Project(near string) (TrackerProject, bool)
  ```

- Nothing else in the config changes. The README says the first entry is the
  default and that moving it changes where unassigned records are exported.

## Section 2: data model and naming

### Record

`Record` gains `Project string`: the near slug of the project the issue
belongs to, or is to be exported to. Empty means the default project.

- The `create` op already has a `project` field, written by imports with
  `tracker_id`. A repo-born issue now writes `project` too, without
  `tracker_id`. The frozen import root template is untouched: repo-born
  creates go through `marshalOp`, not the template.
- Fold: `create` on the root sets `Record.Project` from `op.Project` (and
  `Tracker` when `tracker_id` is present, as today); `link_tracker` sets
  `Record.Project` to its `project` when the link is taken. An older binary
  ignores the field.

### Display ID

```go
// DisplayID is the ID shown to users and used in branch names: the tracker's
// number for an issue born in the tracker, prefixed with its near slug and a
// dash when qualify is set; the short hash otherwise.
func (r *Record) DisplayID(qualify bool) string
```

- `qualify` is decided from the repository's records alone:

  ```go
  // Qualified reports whether display IDs carry their project: the linked
  // records, imported or exported, span more than one project.
  func Qualified(records []Record) bool
  ```

  A link is never removed and records are append-only chains that are never
  deleted, so the flag is monotonic: once true, it stays true in every clone
  that has fetched, and no config edit can flip it back. The records travel
  with the issue refs, which the start, list and close flows fetch first, so
  every clone flips at the same moment: the first reconcile that imports or
  exports an issue of the second project. While the second project has no
  mirrored issue at all, nothing is qualified and nothing can collide. Rejected: a rule that reads the clone's config, alone or combined
  with the records. `.git-zf.toml` is per clone, so two clones would name
  one issue `42` and `zf-42` until both configs matched, and a config edit
  could flip a clone back to bare numbers after it named a branch `zf-42@…`.
- Commands that read every record (`list`, `start`, the mirror) compute the
  flag from what they read. `show`, `edit`, `comment` and `label` get it from
  `Resolve`, which reads every record (three git processes, what `issue list`
  already pays) and returns the flag with the record. Tests pass a literal.
- About twenty call sites change mechanically. A package-level setting was
  the alternative and is rejected: hidden state.
- An exported repo-born record keeps its short hash; its number is shown
  beside it, as today.

### Lookup

`Resolve(ctx, c, query) (Record, qualify bool, error)` reads every record,
computes `qualify` with `Qualified`, and tries, in order: the
full ID; among linked records, the one whose `DisplayID(qualify)` equals the
query, or whose bare number equals a query made of digits when exactly one
record has that number (a bare number matching several projects is an error
that lists the qualified IDs); an ID prefix. So `issue show other-42` finds
the other project's issue, and `issue show 42` still finds a number unique
across projects.

### Branch chain

The `start` op and the branch state (`branch/record.go`) gain `project`, the
near slug, written for every tracker-born branch from now on. Empty on old
chains. `branch.Row` exposes it.

The branch name parser splits on `@`, so a dash in the issue ID is already
accepted; the prune pattern's second alternative already captures
`other-42`.

### Tracker issues name their project by near slug

Adapters set `tracker.Issue.Project` to the near slug whenever the far slug
is a configured project, which is always the case for the mirror calls and
for the filtered "assigned to me" listing. The raw tracker name stays only
for Redmine's listing with no project configured. Inside git-zf one project
name circulates: the one the first mirror spec made the identity.

## Section 3: reconciliation

```go
// Mirror ties the issues of this repository to the tracker projects.
type Mirror struct {
	Tracker  tracker.Tracker
	Type     string   // tracker type, as configured
	Projects []string // near slugs, config order; the first is the default
}
```

- `owns`: the link has this `Type` and a project in `Projects`.
- **List.** One `ListProjectIssues(ctx, near)` per project, all before
  anything is written. Any listing failure ends the run with an error, as
  today. The listed issues are keyed by project and number: two projects can
  share a number.
- **Import.** Per project, as today, with the listing's project in the root.
  The index of linked numbers, bare imports (`healImport`) and losing
  duplicates is keyed by project and number too.
- **Export.** An open record without a link goes to `rec.Project`, or to the
  default when empty: `CreateIssue(ctx, project, …)`, then `link_tracker`
  with that project.
- **State.** Unchanged logic; every per-issue call passes
  `rec.Tracker.Project`.
- **Push** once, as today.
- Counts stay totals across projects; `issue sync` prints the same line.
  Warnings name the qualified display ID.
- Cost: one listing per project, plus the same per-issue calls.

## Section 4: tracker interface

The six per-project methods take `project`, the near slug, right after the
context:

| Method | Signature |
|---|---|
| `ListProjectIssues` | `(ctx, project) ([]Issue, error)` |
| `CreateIssue` | `(ctx, project, title, description) (Issue, error)` |
| `UpdateIssueStatus` | `(ctx, project, issueID, statusName) error` |
| `IsIssueClosed` | `(ctx, project, issueID) (bool, error)` |
| `AddComment` | `(ctx, project, issueID, body) error` |
| `SetIssueOpen` | `(ctx, project, issueID, open) error` |

- An empty `project` means the only configured one. With several configured
  and none named, the adapter returns an error that says so. This is the
  existing "exactly one project" check (`ownerRepo`, `project()`),
  generalized, in the same place.
- **GitHub and Forgejo** resolve owner and repository per call from the
  config entry whose near slug matches (`ownerRepo(project)`).
- **Redmine** uses the project for the listing and the creation only
  (`project(near)`); its issue numbers are global, so the per-issue calls
  ignore it.
- `ListIssues` ("assigned to me") and `ListStatuses` do not change.
- **The fake** keeps one list of issues, each carrying its `Project`.
  `ListProjectIssues` filters on it, `CreateIssue` stamps it, and the
  recorded `Create` and `Open` entries carry it. Numbers keep increasing
  across projects; a test that wants the same number in two projects seeds
  both issues by hand.
- The `issueResolver` interface of `cmd/branch/prune_tracker.go` follows the
  new `IsIssueClosed` signature.

## Section 5: commands

- **`issue new`.** A `--project <near-slug>` flag, validated against the
  config; passing it counts as a flag and skips the form. Interactive, with
  the mirror on and several projects, the form gains a select with the first
  entry preselected. With one project or the mirror off there is no select.
  The chosen or default project goes into the `create` op. The
  `recordPrompter.NewIssue` method takes the project list; empty means no
  select.
- **`issue start`** (and `branch new`). A pick from the live listing names
  its branch with the qualified display ID when `Qualified` says so, built
  from the issue's near slug (`Issue.Project`) and number, and the chain
  stores the project. With a mirror over several projects, a pick from a
  project no record is linked to yet reconciles first: the import flips the
  flag before the branch is named. That runs once per new project, at its
  first start, and never for a project with nothing to pick; a pick from a
  repo record never triggers it, the record being linked already. With one
  project, or the mirror off, `issue start` never reconciles. A pick from a repo record uses `rec.DisplayID(qualify)` and
  `rec.Tracker.Project`. An issue typed in the start form gets the default
  project, no picker (a one-line follow-up if wanted).
- **`issue list`.** Record rows carry the near slug as `Row.Project`, so the
  existing project column appears with several projects. The join of a
  branch to its record tries, in order: the record ID on the chain, the
  display ID, the bare number when exactly one record has it. The last step
  keeps a branch named `42@…` from before a second project joined to its
  record.
- **`issue show`.** Prints a `Project:` line for a repo-born record that has
  one and no link yet. The `Tracker:` line is unchanged.
- **`issue close <id>`, `edit`, `comment`, `label`.** No change beyond the
  lookup of section 2.
- **`issue close` on a tracker-born branch, the review transitions, `branch
  prune`.** They call the tracker with the project and number from
  section 6. Prune lists the records once per run, not once per branch.
- `issue sync` and the hooks do not change.

## Section 6: a branch's tracker project

One helper in `cmd/issueflow`:

```go
// TrackerRef resolves the tracker project and number of a branch's issue:
// the project on the branch chain and the slug without its "<project>-"
// prefix; else the record whose display ID is the slug; else ("", slug), and
// the adapter uses the only configured project or refuses.
func TrackerRef(ctx context.Context, c *git.Client, cfg *config.AppConfig, slug string, row *branch.Row) (project, number string)
```

1. The project on the branch chain (`row`, or the chain loaded by slug when
   `row` is nil). The number is the slug without the `<project>-` prefix, or
   the slug itself.
2. No project on the chain: the record whose display ID is the slug, found
   by `Resolve`. Its link gives both.
3. Neither: an empty project and the slug as number. The adapter then uses
   the only configured project, or refuses when several are configured. The
   caller prints a warning naming the branch and skips the tracker call.

## Section 7: error handling

- Config: `mirror = true` with no project is a load error; an unknown
  `--project` is a parse error naming the configured slugs.
- Tracker: a call with no project and several configured is an error, turned
  into a warning by every caller (status picker, review comment, prune,
  mirror).
- Lookup: an ambiguous bare number is an error that lists the candidates.
- Reconcile: one failed listing ends the run before any write, as today.

### Known limits

- The "second branch on the same issue" check compares exact names, so
  `42@feat@x` from before a second project and `zf-42@feat@x` after could
  coexist on one issue. The window is one fetch wide: a clone that names a
  branch before fetching the import that flipped the flag.
- A branch started from the live listing before this change, in a repository
  that later adds a second project, resolves its project through its record
  (step 2 of `TrackerRef`). If that record is gone, the tracker call is
  skipped with a warning.

## Testing

| Level | File | Covers |
|---|---|---|
| Config | `config/` tests | `mirror = true` with one, two and zero projects; `DefaultProject`, `Project(near)`; an unknown near slug |
| Fold | `issue/record_test.go` | `create` with `project` and no number sets `Record.Project`; `link_tracker` sets it; `DisplayID` with and without qualification, for born and exported records |
| Qualify | `issue/record_test.go` | `Qualified`: no records, unlinked records, or links of one project, is bare; links of two projects is qualified, whether imported or exported |
| Lookup | `issue/repo_test.go` | `Resolve` of `other-42`, of a bare number unique across projects, of an ambiguous one; the flag it returns |
| Reconcile | `issue/mirror_test.go` | two projects: both listed and imported; the same number in both gives two records with different roots (`TestChainRef_FixedRoot` golden untouched); export to the record's project and to the default; `owns` rejects a third project; one failed listing writes nothing; the state table on a non-default project |
| Adapters | each adapter's test file | GitHub and Forgejo resolve the repository per project and refuse an empty project with two configured; Redmine lists and creates per project and ignores it on per-issue calls; `Issue.Project` is the near slug |
| Fake | `tracker/fake` | listing filtered by project; creates and opens record it |
| Branch chain | `branch/record_test.go` | the `project` field folds; an old op without it reads as empty |
| `TrackerRef` | `cmd/issueflow` test | the three steps: chain project, record by display ID, neither |
| Commands | `cmd/issue/mirror_e2e_test.go` | `new` with the picker, with `--project`, with neither; `list` with the project column and the three-step join including an old `42@…` branch; `show` with the `Project:` line |
| Start flow | `start_record_e2e_test.go`, `start_e2e_test.go` | a live pick in a second project gets a qualified branch name and the project on the chain; a record pick likewise; with two projects configured and records of one, the flow reconciles before naming the branch, which comes out qualified |
| Close flow | `close_mirror_e2e_test.go` | the status picker call carries the project; a branch without one resolves through its record |
| Review, prune | their E2E suites | one case each: a qualified slug reaches the tracker as project and number |

Every assertion sits in its own `t.Run`.

## Documentation

- `docs/issue-refs.md`: the `project` field of `create` and `link_tracker`,
  qualified display IDs, the branch chain's `project`.
- README: several projects, the default entry, `--project`, the qualified
  slugs and what a bare number means after a second project.
- `CLAUDE.md`: a paragraph in "Testing the tracker mirror" for the
  two-project cases and `TrackerRef`.
- ROADMAP: item 1.a done; the start-form picker and the branch-name limit
  noted.

## Out of scope

- A project picker in the `issue start` form (default project for now).
- Moving an issue between projects (`set_project`).
- Mirroring some configured projects and not others: the switch is global.
