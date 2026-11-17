# Issue Author and Assignee Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `issue show` and `issue list` say who created an issue and who the tracker assigns it to, read from the repository alone.

**Architecture:** The adapters report the tracker's author and assignees on `tracker.Issue`. The mirror stores them in the `tracker_seen` snapshot op it already writes; `Fold` exposes `Record.Author` and `Record.AssignedTo()`. `issue.Row` carries the first assignee, cut to 8 runes in a new `Assigned` column.

**Tech Stack:** Go, existing packages only (`tracker/*`, `issue`, `cmd/issue`, `cmd/issueflow`, `tui`, `tty`). No new dependency.

**Spec:** `docs/superpowers/specs/2026-11-15-issue-author-assignee-design.md`

## Global Constraints

- Run Go through mise: `mise exec -- go test ./...`.
- Every assertion or table row in a test sits in a named `t.Run`.
- Before editing a symbol, run GitNexus impact on it (`mcp__gitnexus__impact`, direction upstream) and report HIGH/CRITICAL risk; before handing over, run `detect_changes`.
- No `git commit`: the user commits. Tasks end on a green test run, not a commit.
- `importRootTemplate` (`issue/mirror.go`) is frozen: do not edit it.
- Do not touch `contentSettled`, `recordSettled` or `pullComments`: a settled issue costs no `GetIssue` and no `ListComments`.
- `issue show` and `issue list` never call the tracker or reconcile.
- Unknown op types and unknown JSON fields keep being skipped.
- Assignees keep the tracker's order: never sort them.

## Review Focus

1. **A tracker issue whose author was deleted** (`user: null`): the adapters must not panic; `Author` is `""` and the `Author:` line is left out. → Task 1 (Forgejo and Redmine nil-user cases), Task 4.
2. **A snapshot with no author and no assignees** (deleted user, nobody assigned, or a chain written by an older binary): two reconciles in a row must not rewrite the snapshot forever (`nil` vs `[]string{}`). → Task 2.
3. **An assignee shorter than 8 characters, or made of multi-byte runes**: the cell must not panic or split a rune. → Task 3.
4. **Control characters in an assignee read from a remote chain**: they must not reach the terminal. → Task 2 (`TestDecodeOp`).
5. **Tracker and local assignment disagree** (assigned locally to Pi, on the tracker to bob): the tracker's wins, in `issue show` and in the column. → Task 2.

---

### Task 1: The tracker reports author and assignees

**Files:**
- Modify: `tracker/tracker.go` (`Issue`, `Issue.Clean`)
- Modify: `tracker/forgejo/forgejo.go` (`issue` struct, `toIssue`)
- Modify: `tracker/github/github.go` (`toIssue`)
- Modify: `tracker/redmine/redmine.go` (`issue` struct, `toIssues`, and wherever a single issue is converted)
- Test: `tracker/tracker_test.go`, `tracker/forgejo/forgejo_test.go`, `tracker/github/github_test.go`, `tracker/redmine/redmine_test.go`

**Interfaces:**
- Produces: `tracker.Issue.Author string` (login on GitHub/Forgejo, user name on Redmine; `""` when unknown) and `tracker.Issue.Assignees []string` (tracker order; nil when none).

- [ ] **Step 1: Write the failing tests**

`tracker`: `TestIssueClean_AuthorAndAssignees`:

```go
in := tracker.Issue{Author: "al\x1bice", Assignees: []string{"b\x1bob"}}
out := in.Clean()
// t.Run("control characters are dropped"): out.Author == "alice" && out.Assignees[0] == "bob"
// t.Run("the input's slice is not written to"): in.Assignees[0] == "b\x1bob"
```

Each adapter, next to its existing listing test (same httptest style), one test `Test…_AuthorAndAssignees` with these `t.Run`s:

- "listing reports author and assignees in order": payload with author `alice` and assignees `bob`, `carol` → `Author == "alice"`, `Assignees == []string{"bob", "carol"}`.
- "a single read reports them too": same through `GetIssue`.
- "a deleted author is empty": `"user": null` (Forgejo, GitHub) / no `author` key (Redmine) → `Author == ""`, no panic.

Wire fields: Forgejo and GitHub `user.login`, `assignees[].login`; Redmine `author.name`, `assigned_to.name` (one assignee at most).

- [ ] **Step 2: Run them, expect FAIL** (unknown fields `Author`, `Assignees`)

Run: `mise exec -- go test ./tracker/... -run "AuthorAndAssignees" -v`

- [ ] **Step 3: Implement**

- `tracker.Issue`: add the two fields with a comment; `Clean` passes `Author` through `text.Line` and clones `Assignees` before cleaning each element, as it does `Labels`.
- Forgejo: add `User *struct{ Login string }` to `issue`; fill both fields in `toIssue` (so listing and single read share it). `AssignedToMe` logic in `toIssues` is untouched.
- GitHub: fill both in `toIssue` from `iss.GetUser().GetLogin()` and `iss.Assignees`.
- Redmine: add `Author *struct{ Name string }` and `Name` to `AssignedTo`; fill wherever a `tracker.Issue` is built.
- Forgejo's `User` and Redmine's `Author` are pointers: nil-check them before reading (go-github's getters are already nil-safe).
- `tracker/fake/fake.go` needs no change: `ProjectIssues` holds `tracker.Issue` values and `view` copies the struct, so a test sets `Author` and `Assignees` on the issue itself or through `Edit`.

- [ ] **Step 4: Run, expect PASS**

Run: `mise exec -- go test ./tracker/...`

---

### Task 2: The chain keeps them, the fold exposes them

**Files:**
- Modify: `issue/record.go` (`Op`, `DecodeOp`, `TrackerSnapshot`, `Record`, `Fold`)
- Modify: `issue/mirror.go` (`snapshotOf`, `sameSnapshot`, `snapshotOp`)
- Test: `issue/record_test.go`, `issue/mirror_test.go`

**Interfaces:**
- Consumes: `tracker.Issue.Author`, `tracker.Issue.Assignees` (Task 1).
- Produces:
  - `Op.Assignees []string` (`json:"assignees,omitempty"`, tracker_seen); the snapshot's author travels in the existing `Op.TrackerAuthor`.
  - `TrackerSnapshot.Author string` (`json:"author"`), `TrackerSnapshot.Assignees []string` (`json:"assignees"`).
  - `Record.Author string` (`json:"author"`).
  - `func (r *Record) AssignedTo() []string` — never nil.

- [ ] **Step 1: Write the failing tests**

`issue/record_test.go`, `TestFold_AuthorAndAssignees` (use the file's `op(...)` helper as `TestFold_Assign` does):

```go
create := op("c0", OpCreate, "2026-10-01T10:00:00Z"); create.Author = "Pi <pi@test.com>"
born := op("c0", OpCreate, "2026-10-01T10:00:00Z"); born.TrackerType, born.Project, born.TrackerID = "fake", "zf", "42"
seen := op("c1", OpTrackerSeen, "2026-10-01T11:00:00Z", "c0"); seen.TrackerAuthor, seen.Assignees = "alice", []string{"carol", "bob"}
assign := op("c2", OpAssign, "2026-10-01T12:00:00Z", "c1"); assign.Value = "Pi <pi@test.com>"
```

- "an issue created in git-zf: the create op's identity" → `Fold("c0", {create}).Author == "Pi <pi@test.com>"`.
- "an issue born in the tracker: the snapshot's author" → `Fold("c0", {born, seen}).Author == "alice"`.
- "born in the tracker, no snapshot yet: empty" → `Fold("c0", {born}).Author == ""`.
- "exported issue keeps its git identity" → `{create, seen}` → `"Pi <pi@test.com>"`.
- "assignees keep the tracker's order" → `Fold(...{born, seen}).AssignedTo()` equals `{"carol", "bob"}`.
- "the tracker's assignees win over the local one" → `{born, seen, assign}` → `{"carol", "bob"}`.
- "the local assignee when the tracker names nobody" → `{create, assign'}` (assign' parented on c0) → `{"Pi <pi@test.com>"}`.
- "nobody: empty, not nil" → `{create}` → `len == 0 && != nil`.
- "a snapshot without assignees holds an empty list, not nil" → `{born, seen'}` (seen' with no `Assignees`) → `TrackerSeen.Assignees != nil && len == 0`.

`TestDecodeOp` (add a case): an op JSON with `"assignees": ["b\u001bob"]` decodes to `Assignees == {"bob"}`.

`issue/mirror_test.go`, `TestReconcile_AuthorAndAssignees` (build on `importOne`, `countingTracker`, `opCount`, `mustReconcile`, `ft.Edit`, as `TestReconcile_ContentSettles` does; extend `countingTracker` with a `comments` counter for `ListComments` if it lacks one):

- "an import records them": tracker issue with `Author: "alice"`, `Assignees: {"bob"}` → loaded record has `Author == "alice"`, `AssignedTo() == {"bob"}`.
- "a reassignment on the tracker reaches the record": `ft.Edit(id, func(i *tracker.Issue){ i.Assignees = []string{"carol"} })`, reconcile → `{"carol"}`.
- "an old snapshot is backfilled": import an issue with empty author, then set `Author`/`Assignees` on `ft.ProjectIssues` directly (no `Edit`, so `UpdatedAt` does not move), reconcile → record has them, exactly one op more.
- "then nothing changed: no op, no GetIssue, no ListComments": reconcile again → `opCount` unchanged, `ct.gets == 0`, `ct.comments == 0`.
- "no author and nobody assigned settles too": import `trackerIssue("43", "B")` (no author), reconcile twice → second run writes no op.

- [ ] **Step 2: Run, expect FAIL**

Run: `mise exec -- go test ./issue/... -run "TestFold_AuthorAndAssignees|TestDecodeOp|TestReconcile_AuthorAndAssignees" -v`

- [ ] **Step 3: Implement**

- `DecodeOp`: `text.Line` on each `op.Assignees` element, next to the labels.
- `Fold`, `OpTrackerSeen` case: copy `op.TrackerAuthor` and a clone of `op.Assignees` (unsorted) into the snapshot; an empty list is `[]string{}`, never nil, as for `Labels`, so `issue show --json` prints `"assignees": []`.
- `Fold`: remember the `Author` of the root create op (`op.ID == id`); after the loop, `rec.Author` is the snapshot's author when `rec.Tracker != nil && rec.Tracker.Born && rec.TrackerSeen != nil`, else the root's.
- `AssignedTo`: snapshot assignees if any, else `[]string{r.Assignee}` if set, else `[]string{}`.
- `snapshotOf`: `Author: t.Author`, `Assignees: slices.Clone(t.Assignees)`, `[]string{}` when empty (same reason).
- `sameSnapshot`: also `a.Author == b.Author && slices.Equal(a.Assignees, b.Assignees)` (`slices.Equal` treats nil and empty alike — keep it that way).
- `snapshotOp`: set `TrackerAuthor` and `Assignees`.

- [ ] **Step 4: Run the package, expect PASS — including `TestReconcile_ContentSettles` and `TestChainRef_FixedRoot`**

Run: `mise exec -- go test ./issue/... && mise exec -- go test ./git/... -run "TestChainRef_FixedRoot"`

---

### Task 3: The `Assigned` column

**Files:**
- Modify: `issue/row.go` (`Row`, `RecordRow`, `RowCells`, new `AssignedCell`)
- Modify: `cmd/issueflow/rows.go` (`mergeTrackerIssues` both places, `mergeRepoIssues` branch-row copy)
- Modify: `tui/issue.go` (`buildIssueTableColumns`, new width constant `issueTableColWidthAssigned = 8`)
- Modify: `tty/issue.go` (header `ASSIGNED`)
- Modify: `cmd/issue/list.go` (Long help: `Columns: … Issue Status, Assigned, Created.`)
- Test: `issue/row_test.go`, `tty/issue_test.go`, `tui/issue_test.go`, and the file holding `TestBuildRows_TrackerAndRepository` (find it with GitNexus `context`)

**Interfaces:**
- Consumes: `Record.AssignedTo()` (Task 2), `tracker.Issue.Assignees` (Task 1).
- Produces: `Row.Assignee string` (`json:"assignee"`, the full first assignee, `""` when none); `func AssignedCell(r *Row) string`; `RowCells` order becomes ID, [project], title, branch, branch status, tracker status, **assigned**, created.

- [ ] **Step 1: Write the failing tests**

`issue/row_test.go`, `TestAssignedCell` (table, one `t.Run` per row):

| Assignee | want |
|---|---|
| `""` | `""` |
| `"bob"` | `"bob"` |
| `"alexander"` | `"alexande"` |
| `"Pi <pi@example.org>"` | `"Pi <pi@e"` |
| `"Élodie Müller"` | `"Élodie M"` |
| `"日本語のユーザー名前"` | `"日本語のユーザー"` |

`TestRecordRow` (add): a record with snapshot assignees `{"carol", "bob"}` → `row.Assignee == "carol"`; a record with only `Assignee: "Pi <pi@test.com>"` → that string in full.

`TestRowCells_Assigned`: `RowCells(&Row{IssueSlug: "1", Assignee: "alexander"}, false)` → second-to-last cell is `"alexande"`, last is the created cell.

`tty/issue_test.go` (add to `TestRenderIssueTable`): output contains `ASSIGNED` and `alexande`, not `alexander`.

`tui/issue_test.go`: `buildIssueTableColumns` has a column titled `Assigned`, width 8, right before `Created`; fix existing tests that index cells from the end.

`TestBuildRows_TrackerAndRepository` (add three `t.Run`s): `Row.Assignee` is set on (a) a branch row matched to a tracker issue, (b) an unstarted tracker issue, (c) a branch row matched to a repo issue.

- [ ] **Step 2: Run, expect FAIL**

Run: `mise exec -- go test ./issue/... ./tty/... ./tui/... ./cmd/issue/... ./cmd/issueflow/... -run "Assigned|TestRecordRow|TestRenderIssueTable|TestBuildRows|TestIssueTable" -v`

- [ ] **Step 3: Implement**

- `AssignedCell`: the first 8 runes of `r.Assignee` (`[]rune`, not bytes).
- `RecordRow`: `Assignee` is the first of `rec.AssignedTo()`.
- `rows.go`: first of `iss.Assignees` in both places of `mergeTrackerIssues`; `out[i].Assignee = rr.Assignee` in `mergeRepoIssues`.
- Column, header and help as listed under Files.

- [ ] **Step 4: Run, expect PASS**

Run: `mise exec -- go test ./issue/... ./tty/... ./tui/... ./cmd/...`

---

### Task 4: `issue show` prints them; docs

**Files:**
- Modify: `cmd/issue/show.go` (`renderRecord`)
- Modify: `cmd/issue/list.go` (`renderTrackerIssue`)
- Modify: `CLAUDE.md` (a short "Testing the author and assignee" paragraph under "Testing the tracker mirror", with the `go test -run` lines of this plan)
- Test: `cmd/issue/record_e2e_test.go` (`TestRunShow`), `cmd/issue/mirror_e2e_test.go`, and the test of `renderTrackerIssue` if one exists (else a new `TestRenderTrackerIssue_AuthorAndAssignees` beside the function's other tests)

**Interfaces:**
- Consumes: `Record.Author`, `Record.AssignedTo()`, `tracker.Issue.Author`, `tracker.Issue.Assignees`.

- [ ] **Step 1: Write the failing tests**

`renderRecord`, called directly on a `bytes.Buffer`:

- "an issue created in git-zf" → `Record{Author: "Pi <pi@example.org>"}` output contains the line `Author: Pi <pi@example.org>`.
- "tracker assignees are all listed" → snapshot assignees `{"bob", "carol"}` → `Assigned to: bob, carol`.
- "the tracker's assignees win" → plus `Assignee: "Pi <pi@example.org>"` → still `Assigned to: bob, carol`.
- "unknown author: no line" → `Author: ""` → output has no `Author:`.
- "nobody assigned: no line" → no `Assigned to:`.
- "the author line follows the state line" → index of `Author:` is after `State:` and before `ID:`.

`renderTrackerIssue`: `tracker.Issue{Author: "alice", Assignees: {"bob"}}` → `Author: alice` and `Assigned to: bob`; both absent when empty.

`TestMirror_ShowAuthorAndAssignee` (`mirror_e2e_test.go`, on the existing mirror rig): a fake issue with `Author: "alice"`, `Assignees: {"bob"}`, `issue sync`, then `issue show <n>` prints both lines and `ft.ListCalls` did not move during `show`.

- [ ] **Step 2: Run, expect FAIL**

Run: `mise exec -- go test ./cmd/issue/... -run "TestRunShow|TestRenderTrackerIssue|TestMirror_ShowAuthorAndAssignee" -v`

- [ ] **Step 3: Implement**

`renderRecord`: after the `Labels:` line, `Author: %s` when `rec.Author != ""`; replace the `rec.Assignee` test by `Assigned to: ` + `strings.Join(rec.AssignedTo(), ", ")` when non-empty. Same two lines in `renderTrackerIssue` after its `Labels:`. `issue show --json` needs nothing: `author` comes with the `Record`.

- [ ] **Step 4: Full verification**

Run: `mise exec -- go test ./... && make`
Expected: all packages PASS, `./bin/git-zf` built. Then GitNexus `detect_changes({scope: "all"})`; list the changed files for the user to commit.
