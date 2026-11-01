# `issue close` always merges; `issue abandon` closes without a merge

Date: 2026-10-29. Status: approved design, to be planned.

## Intent

`git zf issue close` does two unrelated things today. Without an ID it
merges a branch; with an ID it closes a repository issue without merging,
and the `c` key of the issue table runs that second form. A reader of the
docs met the two in a row and asked whether closing from the table was the
same as closing from the command. It is not, and nothing in the command's
name says so.

After this change, **close means merge**, everywhere, and the "will not be
worked on" case has its own word, **abandon**, with its own key.

## Goals

- `git zf issue close`, `git zf issue close <id>` and `c` in the issue
  table all run the merge flow. With an ID or a row, the issue's branch is
  the one merged.
- An issue with no branch in progress is not an error case the user has to
  decode: the command says so and names `issue abandon`.
- `git zf issue abandon [<id>]` and `a` in the table do what `issue close <id>`
  and `c` do today, with the same safeguards; without an ID a picker lists
  the issues.
- The documentation site and the command help describe the new split.

## Non-goals

- No change to the merge engine, the strategies, the review preflight, the
  tracker status step or the mirror.
- No change to `git zf branch close` (one branch recorded as abandoned) or
  `git zf branch merge`.
- No deletion of issues. "Abandon" closes the record; it does not remove it.
- No backward-compatible alias: `issue close <id>` changes meaning. The
  project is pre-1.0 and the previous meaning shipped days ago.

## Anti-goals

- `c` in the table must never close an issue without a merge again. If the
  merge cannot run, the key does nothing destructive.
- `issue abandon` must never merge or delete a branch.

## Behaviour

### `git zf issue close [<id>]`

1. `<id>` is resolved as `issue start <id>` resolves it: the full ID of a
   repository issue or a unique prefix of at least 4 characters, or the
   number of a tracker issue. The branch chain of a repository issue is
   looked up under its display ID and its short ID, as `issue abandon`
   does today (an exported issue may have a branch under either).
2. Without an ID, the flow is unchanged: fetch the branch refs, list the
   close candidates, pre-select the current branch, pick.
3. With an ID, the candidates are the in-progress branches of that issue.
   - None: print, on stdout, not as an error,
     `Issue <id> has no branch in progress. To close it without a merge: git zf issue abandon <id>`
     and exit 0. This matches the existing "No branches available to close."
     exit.
   - An ID that no repository issue and no branch chain carries (a typo, a
     stale number) is an error, exit 1: `no issue or in-progress branch
     matches "<id>"`. The hint above is for an issue that exists.
   - One: it is picked without the picker.
   - Several (variants: more than one in-progress branch on the issue's
     chain): the picker offers only those. A sub-task is its own issue with
     its own chain, closed by its own ID; its parent keeps refusing while a
     sub-task is open, as today.
4. From the picked branch on, the flow is the one of today: materialize,
   review preflight, pull request detection, conflict dry-run, strategy,
   commit form, confirm, record `merged`, tracker status, mirror reconcile,
   optional local delete.
5. `--base`, `--push` and `--no-push` apply with or without an ID. The
   refusal "cannot be combined with an issue ID" goes away.

### `git zf issue abandon [<id>]`

The current `issue close <id>` under a new name:

- `<id>` is the full ID of a repository issue or a unique prefix of at least
  4 characters. Without an ID the picker that `show`, `edit`, `comment` and
  `label` open without one lists the repository issues, as `issue close`
  without an ID lists the branches. A tracker issue that is not in the repository (no mirror)
  cannot be abandoned: the command fails as `issue show` does for an unknown
  ID.
- Already closed: `Issue <id> is already closed.`, exit 0.
- A branch of the issue in progress: refused with
  `issue <id> has a branch in progress (<names>): merge it with `git zf issue close <id>`, or abandon the branch with `git zf branch close <branch-name>`, then abandon the issue`.
- Otherwise: `set_state closed` is appended and pushed, the mirror
  reconciles, and the command prints `Abandoned issue <id>: <title>`.
- No flags.

### The issue table (`git zf issue list`, `git zf issue`)

| Key | Before | After |
|---|---|---|
| `c` | twice: close without merge, as `issue close <id>` | once: the merge flow for the row's issue, as `issue close <id>` |
| `a` | nothing | twice: abandon, as `issue abandon <id>`; the first press shows `a again abandons <id> (no undo)` |

- `c` on a row whose Branch column is `∅` prints the "no branch in
  progress" line and reopens the table.
- `c` needs no arming: the merge flow has its own confirm step. Cancelling
  it leaves the branches as they were, with one exception that predates
  this change: an approved review's pending reviewer commits are
  incorporated into the feature branch by the preflight, before the
  confirm, and an abort keeps them (they are approved work).
- The arming state that `c` used (`armClose`) moves to `a`. Any other key
  disarms it, as today.
- The table's help line and the `issue list` long description list both
  keys.

### Help text

- `issue close`: Short becomes `Close an issue by merging its branch`.
  The Long describes the picker without an ID, the pre-selection with one,
  the "no branch in progress" message and the hint to `issue abandon`.
- `issue abandon`: Short `Close an issue without merging anything`. The
  Long is the paragraph that `issue close` carries today for `<id>`,
  reworded for the new name.
- `issue list`: the Keys paragraph gains `a` and changes `c`.
- The reference pages regenerate with `make docs-gen`; the drift test
  enforces it.

## Documentation

- `website/docs/index.md`: the merge-assistant bullet stays; no change
  needed unless the wording "closes the issue" needs "merges its branch and
  closes the issue".
- `website/docs/workflows/close-an-issue.md`: the first section covers
  `issue close` and `issue close <id>`; "Close without merging" becomes
  "Abandon an issue" and documents `issue abandon <id>` and the `a` key.
- `website/docs/workflows/start-an-issue.md`: the key table and its warning
  move from `c` to `a`; `c` gets a row saying it merges, as `issue close
  <id>` does, with a link to the close page.
- `website/docs/under-the-hood/issue-refs.md` line about which commands
  reconcile: `issue close <id>` becomes `issue abandon <id>`.
- `CLAUDE.md`: the record-ops paragraph and test names that say `close <id>`
  say `abandon`.

## Code map (for the planner)

- `cmd/issue/close.go`: `closeRunE` no longer forks on `len(args)`; an ID
  is turned into a slug and handed to the picker step. `getPickedBranch`
  (or a sibling) takes an optional issue slug and filters
  `issueflow.CloseCandidates` by it; it prints the "no branch" line when the
  filter empties the list.
- `cmd/issue/record.go`: `runCloseByID` becomes `runAbandon`; it takes the
  `recordPrompter` and the args as `runLabel` does (`resolveRecord` opens
  the picker when they are empty); its refusal text and its final line
  change as above.
- `cmd/issue/abandon.go` (new): `getAbandonCmd`, registered in
  `cmd/issue/issue.go` next to `getCloseCmd`.
- `tui/issue.go`: `IssuePickAbandon` added; `c` picks `IssuePickClose`
  without arming; `a` arms then picks `IssuePickAbandon`. The disarm guard
  at the top of `Update` checks `"a"` instead of `"c"`.
- `cmd/issue/list.go`: `listAction` runs the merge flow for
  `IssuePickClose` and `runAbandon` for `IssuePickAbandon`. The merge flow
  needs a `ClosePrompter` and the close deps: `listAction` gains them the
  way it already carries the record and start prompters, so the E2E tests
  can script them.
- Tests to move or add: `TestRunCloseByID` → `TestRunAbandon` and the
  cobra-level `close <id>` test → `abandon <id>`; new `TestClose_ByID`
  cases in `cmd/issue/close_e2e_test.go` (one branch merged, no branch
  prints the hint and merges nothing, two variants reach the picker as the
  only candidates, `--base` accepted with an ID); `TestIssueTable_ActionKeys`
  for the new `c` and `a`; `TestListAction` for both picks; `TestClose_Mirror`
  and `close_record_e2e_test.go` wherever they drive `close <id>`.

## Calls made in this design

- One in-progress branch with an ID skips the picker. Reason: the user named
  the issue; asking "which branch" when there is one is a wasted prompt.
  Without an ID the picker stays even for one candidate, as today.
- "No branch in progress" exits 0 and prints to stdout. Reason: the sibling
  "No branches available to close." already does; scripts that loop over
  issues should not stop on it.
- `issue abandon` keeps refusing while a branch is in progress rather than
  offering to abandon the branch too. Reason: two records change hands in
  one command otherwise, and `branch close` exists for the branch.
- The `c` key loses its two-press arming. Reason: a merge is confirmed in
  its own flow; arming twice would be noise.
