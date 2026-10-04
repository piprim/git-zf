# Repo Issues (Plan 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Store issues in the repository under `refs/zf/issues/<id>` as event-sourced commit chains, with `issue new/show/comment/label/sync`, and make `issue start`, `issue close` and `issue list` read and write them when no tracker is involved.

**Architecture:** Three layers. `git/issue_ref*.go` is byte-level plumbing over the git CLI (one commit per op, `op.json` in the tree, a tracking namespace for the remote, fast-forward-only pushes). `issue/record.go` is the pure domain: the `Op` type and `Fold`, which turns a commit DAG into a `Record`. `issue/repo.go` glues the two (`Create`, `Append`, `Load`, `List`, `Resolve`, `Fetch`, `Push`, `Sync`). The commands in `cmd/issue` sit on that glue behind a small `recordPrompter` interface so E2E tests drive them without a terminal.

**Tech Stack:** Go 1.25 (run through `mise exec -- go …`), the system `git` binary (2.38+ is already required by the project), cobra v1.1.3, charmbracelet/huh, modernc SQLite (untouched). No new dependency.

**Spec:** `docs/superpowers/specs/2026-10-02-repo-issues-design.md` (sections 1 and 2; section 3 is Plan 2).

**Verified:** every code block below was compiled, tested and linted in a throwaway copy of the repository at commit `19809a1` before this plan was written. The full suite passed and `golangci-lint` reported no finding in the new files and no new finding in the modified ones.

## Global Constraints

- Go commands run as `mise exec -- go …`. Never call `go` directly.
- Git is driven through the CLI only (`git/` package helpers `c.output`, `c.gitCmd`, `c.runInteractive`). No go-git, no new module dependency.
- Ref layout: `refs/zf/issues/<id>` where `<id>` is the **full object ID** of the `create` commit. The 7-character short ID is display and input only.
- The remote's issue refs are fetched into the tracking namespace `refs/zf/remote/issues/*`. A fetch never writes `refs/zf/issues/*` directly and never prunes it.
- Issue refs are pushed with a plain, non-forced push. Never `--force`, never `--force-with-lease`.
- Each op is one commit whose tree holds a single file `op.json` with `"v": 1`. `at` is RFC 3339 UTC.
- `git commit-tree` ignores `commit.gpgsign`: the writer passes `-S` itself when `git config --bool commit.gpgsign` is `true`.
- `Resolve` accepts a full ID or a unique prefix of at least 4 characters.
- The SQLite schema does not change. No migration.
- Tests: every distinct assertion or scenario is wrapped in a named `t.Run(...)`, including single-assertion tests. Real on-disk repos, no mocks of git.
- GitNexus (project `CLAUDE.md`): run `impact({target, direction: "upstream"})` before editing any existing symbol and report the result; run `detect_changes()` before every commit. The impact results recorded below were confirmed on the index rebuilt at `19809a1` on 2026-10-03; re-run them at execution time, after `gitnexus analyze` if the index is reported stale.
- Commits: the user committed the spec themselves. Before the first commit, confirm whether the executor commits per task (the `Commit` steps below) or leaves each task staged for the user. Messages follow the repo's conventional-commit style.
- `docs/superpowers/` is in `.gitignore`: this plan and the spec are not part of any commit unless added with `git add -f`.

## Scope of Plan 1

Decisions taken while planning, where the spec's delivery line needed sharpening so that Plan 1 ships working software on its own:

- **Tracker paths are untouched.** With a tracker configured, `issue start` keeps its tracker picker and `issue list` keeps listing tracker issues. Records linked to a tracker do not exist until Plan 2 writes `link_tracker`. `RecordTrackerStatus`, the `in-progress-label` config key and the review commands' op writes are Plan 2.
- **The manual path is the repo path.** In `issue start` / `branch new`, the manual form's Issue ID becomes optional. Empty creates a repo issue; a typed ID keeps today's behavior (an issue living in a tracker git-zf does not talk to, e.g. Jira) and creates no record.
- **`issue.Issue` stays** and gains `RecordID`. Its replacement by `issue.Record` is Plan 2, when the tracker path is rewritten.
- **Ops written in Plan 1:** `create`, `set_state`, `add_label`, `remove_label`, `add_comment`, `merge`. The fold ignores every other type.
- **Labels are shown in the Title cell** of `issue list` (`Login fails [bug, ui]`) instead of a separate column, which would overflow a 120-column terminal. `--json` carries them as an array.
- **`issue sync`** is fetch, reconcile, push. No tracker involved.

Not in the spec and not in this plan: a command to edit an issue's title or description. Flag it to the user; it is one small task (`set_title` / `set_description` ops plus an `issue edit` form) if wanted.

## Impact Check

`gitnexus impact`, upstream, on the index rebuilt at `19809a1`. **Seven symbols are rated HIGH**: each has a single direct caller but sits on the `issue start` / `branch new` or `issue list` flows. The existing E2E suites for those flows are the regression net and must stay green.

| Symbol | File | Risk | Direct callers | Task |
|---|---|---|---|---|
| `pickIssue` | `cmd/issueflow/start.go` | **HIGH** | `RunIssueStart` | 7 |
| `getFromTracker` | `cmd/issueflow/start.go` | **HIGH** | `pickIssue` | 7 |
| `writePushBranchRef` | `cmd/issueflow/start.go` | **HIGH** | `createFlow` | 7 |
| `buildRows` | `cmd/issue/list.go` | **HIGH** | `runList` | 9 |
| `matchesStatus` | `tui/issue.go` | **HIGH** | `applyFilters` | 9 |
| `issueRowToTableRow` | `tui/issue.go` | **HIGH** | `applyFilters` | 9 |
| `RenderIssueTable` | `tty/issue.go` | **HIGH** | `runList` | 9 |
| `updateClosedStatus` | `cmd/issue/close.go` | LOW | `runClose` | 8 |
| `IssueInput` | `tui/issue.go` | LOW | `HuhStartPrompter.PickIssueFromUser` | 7 |
| `IssueActionSelect` | `tui/issue.go` | LOW | `Issue.runE` | 6 |

`StartPrompter` gains a method: its two implementers are `HuhStartPrompter` (`cmd/issueflow/start_prompter.go`) and `scriptedStartPrompter` (`cmd/issue/start_prompter_test.go`). Both are updated in Task 7.

## Review Focus

Inputs the spec implies but does not spell out. Each has a test in the task that owns the code.

1. **A label token starting with `-`** (`issue label <id> -wontfix`): the user expects it to remove the label, not to be parsed as command flags. Test: `TestLabelCmd_DashTokenIsNotAFlag` (Task 6).
2. **A title that slugs to nothing** (`???`) entered in `issue start` with an empty ID: the user expects a clear refusal and no orphan issue left in the repo. Test: `TestRunIssueStart_UnsluggableTitleCreatesNoIssue` (Task 7).
3. **A command run inside a linked worktree**: the user expects the same issues as in the main checkout. Test: `TestLinkedWorktreeSharesIssues` (Task 4).
4. **A remote that has no `refs/zf/issues/*` yet** (the first user of the feature): fetch and sync must succeed. Test: `TestIssueRef_RemoteWithoutIssueRefs` (Task 3).
5. **Several remotes, none named `origin`**: git-zf cannot pick one; the user expects the issue saved locally with a warning, not a failure. Test: `TestRunNew_AmbiguousRemoteWarns` (Task 5).

## File Structure

| File | Status | Responsibility |
|---|---|---|
| `issue/record.go` | new | `Op`, op-type constants, `DecodeOp`, `Fold`, `Record`. Pure, no git. |
| `issue/repo.go` | new | `Create`, `Append`, `Load`, `List`, `Resolve`, `Fetch`, `Push`, `Sync` over `*git.Client`. |
| `issue/issue.go` | modify | `Issue.RecordID`. |
| `git/issue_ref.go` | new | Local plumbing: write an op commit, create/append a chain, read a chain, list IDs. |
| `git/issue_ref_sync.go` | new | Remote plumbing: fetch into the tracking namespace, reconcile, push. |
| `git/branch_ref.go` | modify | `BranchRef.IssueID`. |
| `tui/issue_record.go` | new | huh forms: new issue, record picker, comment, label changes. |
| `tui/issue.go` | modify | Menu entries; optional Issue ID in `IssueInput`; list table reads the issue state and labels. |
| `tty/issue.go` | modify | Plain table: labels in the Title cell, `ISSUE STATUS` header. |
| `store/store.go`, `store/helpers.go` | modify | `IssueRow.Labels`, `IssueRow.State`, `TitleWithLabels`. |
| `cmd/issue/record.go` | new | `recordPrompter`, its huh implementation, shared helpers (`resolveRecord`, `pushIssue`, `fetchIssues`, `closeRepoIssue`). |
| `cmd/issue/new.go`, `show.go`, `comment.go`, `label.go`, `sync.go` | new | One subcommand each. |
| `cmd/issue/issue.go` | modify | Register the five subcommands; menu dispatch. |
| `cmd/issue/list.go` | modify | Merge repo issues into the store-backed listing. |
| `cmd/issue/close.go` | modify | Close the repo issue after the merge lands. |
| `cmd/issueflow/start.go`, `start_prompter.go` | modify | Repo-issue picker and creation on the manual path; `BranchRef.IssueID`. |
| `docs/issue-refs.md` | new | Op format reference. |
| `README.md`, `CLAUDE.md` | modify | User and contributor docs. |

---

### Task 1: Op, Fold and Record

**Files:**
- Create: `issue/record.go`
- Test: `issue/record_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const OpVersion = 1`; op types `OpCreate`, `OpSetState`, `OpAddLabel`, `OpRemoveLabel`, `OpAddComment`, `OpMerge`; states `StateOpen`, `StateClosed`.
  - `type Op struct { V int; Type, At, Author, Title, Description, BranchType, Value, Body string; ID string; Parents []string }` (`ID` and `Parents` are not serialized).
  - `func DecodeOp(id string, parents []string, payload []byte) (op Op, ok bool)`
  - `type Comment struct { ID, Author string; At time.Time; Body string }`
  - `type Record struct { ID, Title, Description, BranchType, State string; Labels []string; Comments []Comment; CreatedAt time.Time; Warnings []string }`
  - `func (r *Record) ShortID() string`, `func (r *Record) DisplayID() string`
  - `func Fold(id string, ops []Op) Record`

- [ ] **Step 1: Write the failing test**

Create `issue/record_test.go`:

```go
package issue

import (
	"slices"
	"testing"
)

func op(id, typ, at string, parents ...string) Op {
	return Op{V: OpVersion, ID: id, Type: typ, At: at, Parents: parents}
}

func TestFold(t *testing.T) {
	t.Parallel()

	create := op("c0", OpCreate, "2026-10-01T10:00:00Z")
	create.Title, create.Description, create.BranchType = "Login fails", "Steps…", "fix"

	t.Run("create sets the identity fields and an open state", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", []Op{create})
		if rec.ID != "c0" || rec.Title != "Login fails" || rec.Description != "Steps…" || rec.BranchType != "fix" {
			t.Errorf("unexpected record: %+v", rec)
		}
		if rec.State != StateOpen {
			t.Errorf("State = %q, want %q", rec.State, StateOpen)
		}
		if rec.CreatedAt.IsZero() {
			t.Error("CreatedAt is zero")
		}
	})

	t.Run("set_state closed then open ends open", func(t *testing.T) {
		t.Parallel()

		closed := op("c1", OpSetState, "2026-10-01T11:00:00Z", "c0")
		closed.Value = StateClosed
		reopened := op("c2", OpSetState, "2026-10-01T12:00:00Z", "c1")
		reopened.Value = StateOpen

		if got := Fold("c0", []Op{create, closed}).State; got != StateClosed {
			t.Errorf("after close: State = %q", got)
		}
		if got := Fold("c0", []Op{create, closed, reopened}).State; got != StateOpen {
			t.Errorf("after reopen: State = %q", got)
		}
	})

	t.Run("an unknown state value is ignored", func(t *testing.T) {
		t.Parallel()

		bad := op("c1", OpSetState, "2026-10-01T11:00:00Z", "c0")
		bad.Value = "wontfix"
		if got := Fold("c0", []Op{create, bad}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}
	})

	t.Run("labels are a sorted set and removing an absent label is a no-op", func(t *testing.T) {
		t.Parallel()

		a1 := op("c1", OpAddLabel, "2026-10-01T11:00:00Z", "c0")
		a1.Value = "ui"
		a2 := op("c2", OpAddLabel, "2026-10-01T11:01:00Z", "c1")
		a2.Value = "bug"
		a3 := op("c3", OpAddLabel, "2026-10-01T11:02:00Z", "c2")
		a3.Value = "ui"
		r1 := op("c4", OpRemoveLabel, "2026-10-01T11:03:00Z", "c3")
		r1.Value = "nope"

		got := Fold("c0", []Op{create, a1, a2, a3, r1}).Labels
		if !slices.Equal(got, []string{"bug", "ui"}) {
			t.Errorf("Labels = %v, want [bug ui]", got)
		}
	})

	t.Run("comments accumulate in order with their op ID", func(t *testing.T) {
		t.Parallel()

		k1 := op("c1", OpAddComment, "2026-10-01T11:00:00Z", "c0")
		k1.Body, k1.Author = "first", "A <a@x>"
		k2 := op("c2", OpAddComment, "2026-10-01T12:00:00Z", "c1")
		k2.Body = "second"

		got := Fold("c0", []Op{create, k1, k2}).Comments
		if len(got) != 2 || got[0].Body != "first" || got[1].Body != "second" {
			t.Fatalf("Comments = %+v", got)
		}
		if got[0].ID != "c1" || got[0].Author != "A <a@x>" {
			t.Errorf("first comment = %+v", got[0])
		}
	})

	t.Run("slice order does not matter", func(t *testing.T) {
		t.Parallel()

		closed := op("c1", OpSetState, "2026-10-01T11:00:00Z", "c0")
		closed.Value = StateClosed
		if got := Fold("c0", []Op{closed, create}).State; got != StateClosed {
			t.Errorf("State = %q, want %q", got, StateClosed)
		}
	})

	t.Run("concurrent ops are ordered by At then ID", func(t *testing.T) {
		t.Parallel()

		// Two clones diverge from c0: one closes at 11:00, the other reopens
		// at 12:00. A merge joins them. The later At wins.
		closed := op("bb", OpSetState, "2026-10-01T11:00:00Z", "c0")
		closed.Value = StateClosed
		opened := op("aa", OpSetState, "2026-10-01T12:00:00Z", "c0")
		opened.Value = StateOpen
		merge := op("m", OpMerge, "2026-10-01T13:00:00Z", "bb", "aa")

		if got := Fold("c0", []Op{create, closed, opened, merge}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}

		// Same At on both sides: the greater ID is applied last.
		opened.At = closed.At
		if got := Fold("c0", []Op{create, closed, opened, merge}).State; got != StateClosed {
			t.Errorf("equal At: State = %q, want %q (ID bb applied after aa)", got, StateClosed)
		}
	})

	t.Run("a causally later op beats a future-dated one", func(t *testing.T) {
		t.Parallel()

		// A clock set to 2099 closes the issue; a later op, written after
		// seeing it, reopens it with a correct clock.
		skewed := op("c1", OpSetState, "2099-01-01T00:00:00Z", "c0")
		skewed.Value = StateClosed
		later := op("c2", OpSetState, "2026-10-01T12:00:00Z", "c1")
		later.Value = StateOpen

		if got := Fold("c0", []Op{create, skewed, later}).State; got != StateOpen {
			t.Errorf("State = %q, want %q", got, StateOpen)
		}
	})

	t.Run("unknown op types are skipped", func(t *testing.T) {
		t.Parallel()

		future := op("c1", "set_milestone", "2026-10-01T11:00:00Z", "c0")
		future.Value = "v2"
		closed := op("c2", OpSetState, "2026-10-01T12:00:00Z", "c1")
		closed.Value = StateClosed

		rec := Fold("c0", []Op{create, future, closed})
		if rec.State != StateClosed || rec.Title != "Login fails" {
			t.Errorf("unexpected record: %+v", rec)
		}
	})

	t.Run("an empty chain folds to an open record with empty slices", func(t *testing.T) {
		t.Parallel()

		rec := Fold("c0", nil)
		if rec.State != StateOpen || rec.Labels == nil || rec.Comments == nil {
			t.Errorf("unexpected record: %+v", rec)
		}
	})
}

func TestDecodeOp(t *testing.T) {
	t.Parallel()

	t.Run("valid payload decodes and carries the commit identity", func(t *testing.T) {
		t.Parallel()

		got, ok := DecodeOp("abc", []string{"p"}, []byte(`{"v":1,"type":"add_label","at":"2026-10-01T10:00:00Z","value":"ui"}`))
		if !ok || got.Type != OpAddLabel || got.Value != "ui" || got.ID != "abc" || len(got.Parents) != 1 {
			t.Errorf("got %+v ok=%v", got, ok)
		}
	})

	for name, payload := range map[string]string{
		"invalid JSON":    `{not json`,
		"unknown version": `{"v":99,"type":"create"}`,
		"missing payload": ``,
	} {
		t.Run(name+" is rejected but keeps ID and parents", func(t *testing.T) {
			t.Parallel()

			got, ok := DecodeOp("abc", []string{"p"}, []byte(payload))
			if ok || got.Type != "" || got.ID != "abc" || len(got.Parents) != 1 {
				t.Errorf("got %+v ok=%v", got, ok)
			}
		})
	}
}

func TestRecordIDs(t *testing.T) {
	t.Parallel()

	rec := Record{ID: "0123456789abcdef0123456789abcdef01234567"}

	t.Run("ShortID is the first 7 characters", func(t *testing.T) {
		t.Parallel()

		if got := rec.ShortID(); got != "0123456" {
			t.Errorf("ShortID = %q", got)
		}
	})

	t.Run("DisplayID is the short ID for an unlinked record", func(t *testing.T) {
		t.Parallel()

		if got := rec.DisplayID(); got != "0123456" {
			t.Errorf("DisplayID = %q", got)
		}
	})

	t.Run("ShortID of a short ID is the ID itself", func(t *testing.T) {
		t.Parallel()

		short := Record{ID: "abc"}
		if got := short.ShortID(); got != "abc" {
			t.Errorf("ShortID = %q", got)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./issue/... -run "^(TestFold|TestDecodeOp|TestRecordIDs)$"`
Expected: build failure, `undefined: Op`, `undefined: Fold`, `undefined: DecodeOp`.

- [ ] **Step 3: Write the implementation**

Create `issue/record.go`:

```go
package issue

import (
	"cmp"
	"encoding/json"
	"slices"
	"time"
)

// OpVersion is the op.json schema version this binary writes and understands.
const OpVersion = 1

// Op types. Fold skips any type it does not know, so an older binary tolerates
// ops written by a newer one.
const (
	OpCreate      = "create"
	OpSetState    = "set_state"
	OpAddLabel    = "add_label"
	OpRemoveLabel = "remove_label"
	OpAddComment  = "add_comment"
	OpMerge       = "merge"
)

// Issue states.
const (
	StateOpen   = "open"
	StateClosed = "closed"
)

const shortIDLen = 7

// Op is one change to an issue: the content of op.json in one commit of the
// chain at refs/zf/issues/<id>. ID and Parents come from the commit itself and
// are not part of the JSON.
type Op struct {
	V      int    `json:"v"`
	Type   string `json:"type"`
	At     string `json:"at"` // RFC 3339, UTC
	Author string `json:"author,omitempty"`

	Title       string `json:"title,omitempty"`       // create
	Description string `json:"description,omitempty"` // create
	BranchType  string `json:"branch_type,omitempty"` // create
	Value       string `json:"value,omitempty"`       // set_state, add_label, remove_label
	Body        string `json:"body,omitempty"`        // add_comment

	ID      string   `json:"-"`
	Parents []string `json:"-"`
}

// DecodeOp builds the Op of commit id from its op.json payload. ok is false
// when the payload is missing, is not valid JSON or has an unknown version; the
// returned Op then has an empty Type, so Fold ignores it while its ID and
// Parents still keep the chain connected.
func DecodeOp(id string, parents []string, payload []byte) (op Op, ok bool) {
	if err := json.Unmarshal(payload, &op); err != nil || op.V != OpVersion {
		return Op{ID: id, Parents: parents}, false
	}

	op.ID, op.Parents = id, parents

	return op, true
}

// Comment is one add_comment op.
type Comment struct {
	ID     string    `json:"id"`
	Author string    `json:"author"`
	At     time.Time `json:"at"`
	Body   string    `json:"body"`
}

// Record is the current state of an issue: the fold of its op chain.
type Record struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	BranchType  string    `json:"branch_type"`
	State       string    `json:"state"`
	Labels      []string  `json:"labels"`
	Comments    []Comment `json:"comments"`
	CreatedAt   time.Time `json:"created_at"`

	// Warnings lists the commits Load skipped as malformed.
	Warnings []string `json:"-"`
}

// ShortID returns the first 7 characters of the ID.
func (r *Record) ShortID() string {
	if len(r.ID) <= shortIDLen {
		return r.ID
	}

	return r.ID[:shortIDLen]
}

// DisplayID is the ID shown to users and used in branch names.
func (r *Record) DisplayID() string {
	return r.ShortID()
}

// Fold computes the Record of issue id from its ops. The order of ops in the
// slice does not matter: they are linearized from their Parents.
func Fold(id string, ops []Op) Record {
	rec := Record{ID: id, State: StateOpen, Labels: []string{}, Comments: []Comment{}}
	labels := make(map[string]bool)

	ordered := linearize(ops)
	for i := range ordered {
		op := &ordered[i]
		switch op.Type {
		case OpCreate:
			rec.Title, rec.Description, rec.BranchType = op.Title, op.Description, op.BranchType
			if op.ID == id {
				rec.CreatedAt = parseAt(op.At)
			}
		case OpSetState:
			if op.Value == StateOpen || op.Value == StateClosed {
				rec.State = op.Value
			}
		case OpAddLabel:
			labels[op.Value] = true
		case OpRemoveLabel:
			delete(labels, op.Value)
		case OpAddComment:
			rec.Comments = append(rec.Comments, Comment{ID: op.ID, Author: op.Author, At: parseAt(op.At), Body: op.Body})
		}
	}

	for l := range labels {
		rec.Labels = append(rec.Labels, l)
	}
	slices.Sort(rec.Labels)

	return rec
}

func parseAt(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}

	return t.UTC()
}

// linearize orders ops so that every op comes after all of its parents. Ops
// with no ordering between them (written concurrently on two clones) are
// ordered by At, then by ID, so every clone computes the same sequence.
func linearize(ops []Op) []Op {
	byID := make(map[string]*Op, len(ops))
	for i := range ops {
		byID[ops[i].ID] = &ops[i]
	}

	pending := make(map[string]int, len(ops))
	children := make(map[string][]string, len(ops))
	for i := range ops {
		for _, p := range ops[i].Parents {
			if _, known := byID[p]; !known {
				continue
			}
			pending[ops[i].ID]++
			children[p] = append(children[p], ops[i].ID)
		}
	}

	var ready []string
	for i := range ops {
		if pending[ops[i].ID] == 0 {
			ready = append(ready, ops[i].ID)
		}
	}

	out := make([]Op, 0, len(ops))
	for len(ready) > 0 {
		// ponytail: re-sorts the ready set at every step, O(n² log n) worst
		// case; switch to container/heap if a chain reaches thousands of ops.
		slices.SortFunc(ready, func(a, b string) int {
			if c := parseAt(byID[a].At).Compare(parseAt(byID[b].At)); c != 0 {
				return c
			}

			return cmp.Compare(a, b)
		})

		next := ready[0]
		ready = ready[1:]
		out = append(out, *byID[next])

		for _, child := range children[next] {
			pending[child]--
			if pending[child] == 0 {
				ready = append(ready, child)
			}
		}
	}

	return out
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./issue/... -run "^(TestFold|TestDecodeOp|TestRecordIDs)$" -v`
Expected: PASS, every subtest listed.

- [ ] **Step 5: Commit**

Run `detect_changes()` first; expected: only new symbols in `issue/record.go`.

```bash
git add issue/record.go issue/record_test.go
git commit -m "feat(issue): add the op type and the fold that computes an issue record"
```

---

### Task 2: Local issue-ref plumbing

**Files:**
- Create: `git/issue_ref.go`
- Test: `git/issue_ref_test.go`

**Interfaces:**
- Consumes: existing `(*Client).gitCmd`, `(*Client).output`, `gitStderr`, `ZeroHash`; test helpers `newDiskRepo`, `mustGit`.
- Produces:
  - `var ErrIssueRefCorrupt`, `var ErrIssueNotFound`
  - `type IssueCommit struct { ID string; Parents []string; Payload []byte }`
  - `func (c *Client) CreateIssueRef(ctx context.Context, payload []byte, message string) (id string, err error)`
  - `func (c *Client) AppendIssueCommit(ctx context.Context, id string, payload []byte, message string) (commit string, err error)`
  - `func (c *Client) IssueTip(ctx context.Context, id string) (string, error)` (`""` when the ref does not exist)
  - `func (c *Client) ListIssueIDs(ctx context.Context) ([]string, error)`
  - `func (c *Client) ReadIssueCommits(ctx context.Context, id string) ([]IssueCommit, error)` (parents before children)
  - unexported, used by Task 3: `issueRefPrefix`, `(*Client).writeIssueCommit(ctx, payload, message, parents...)`, `(*Client).refTip(ctx, ref)`

- [ ] **Step 1: Write the failing test**

Create `git/issue_ref_test.go`:

```go
package git

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

func payloads(commits []IssueCommit) []string {
	out := make([]string, len(commits))
	for i, c := range commits {
		out[i] = string(c.Payload)
	}

	return out
}

func TestIssueRef_CreateAppendRead(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	id, err := client.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("the ref is named after the full ID of the root commit", func(t *testing.T) {
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "refs/zf/issues/"+id).Output()
		if err != nil {
			t.Fatalf("rev-parse: %v", err)
		}
		if got := strings.TrimSpace(string(out)); got != id || len(id) != 40 {
			t.Errorf("ref points at %q, id is %q", got, id)
		}
	})

	second, err := client.AppendIssueCommit(ctx, id, []byte(`{"type":"add_comment"}`), "add_comment")
	if err != nil {
		t.Fatalf("AppendIssueCommit: %v", err)
	}

	t.Run("IssueTip returns the appended commit", func(t *testing.T) {
		tip, err := client.IssueTip(ctx, id)
		if err != nil || tip != second {
			t.Errorf("IssueTip = %q, %v; want %q", tip, err, second)
		}
	})

	t.Run("IssueTip of an unknown issue is empty", func(t *testing.T) {
		tip, err := client.IssueTip(ctx, "0000000000000000000000000000000000000001")
		if err != nil || tip != "" {
			t.Errorf("IssueTip = %q, %v; want empty", tip, err)
		}
	})

	t.Run("ReadIssueCommits returns parents first with payloads and parent links", func(t *testing.T) {
		commits, err := client.ReadIssueCommits(ctx, id)
		if err != nil {
			t.Fatalf("ReadIssueCommits: %v", err)
		}
		got := payloads(commits)
		if len(got) != 2 || got[0] != `{"type":"create"}` || got[1] != `{"type":"add_comment"}` {
			t.Fatalf("payloads = %q", got)
		}
		if commits[0].ID != id || len(commits[0].Parents) != 0 {
			t.Errorf("root = %+v", commits[0])
		}
		if commits[1].ID != second || len(commits[1].Parents) != 1 || commits[1].Parents[0] != id {
			t.Errorf("second = %+v", commits[1])
		}
	})

	t.Run("ListIssueIDs lists the issue", func(t *testing.T) {
		ids, err := client.ListIssueIDs(ctx)
		if err != nil || len(ids) != 1 || ids[0] != id {
			t.Errorf("ListIssueIDs = %v, %v", ids, err)
		}
	})

	t.Run("AppendIssueCommit on an unknown issue fails with ErrIssueNotFound", func(t *testing.T) {
		_, err := client.AppendIssueCommit(ctx, "0000000000000000000000000000000000000001", []byte(`{}`), "x")
		if !errors.Is(err, ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})

	t.Run("a payload with multi-byte characters and newlines round-trips", func(t *testing.T) {
		body := "{\"body\":\"é à ü\\nline two\"}\n\n"
		id2, err := client.CreateIssueRef(ctx, []byte(body), "create")
		if err != nil {
			t.Fatalf("CreateIssueRef: %v", err)
		}
		commits, err := client.ReadIssueCommits(ctx, id2)
		if err != nil || len(commits) != 1 || string(commits[0].Payload) != body {
			t.Errorf("payload = %q, %v", payloads(commits), err)
		}
	})
}

func TestIssueRef_Corrupt(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	id, err := client.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("a ref whose name is not a root of its chain is rejected", func(t *testing.T) {
		wrong := "1111111111111111111111111111111111111111"
		mustGit(t, dir, "update-ref", "refs/zf/issues/"+wrong, id)

		_, err := client.ReadIssueCommits(ctx, wrong)
		if !errors.Is(err, ErrIssueRefCorrupt) {
			t.Errorf("err = %v, want ErrIssueRefCorrupt", err)
		}
	})

	t.Run("a commit without op.json yields a nil payload and keeps the chain readable", func(t *testing.T) {
		// Append a commit carrying the empty tree.
		emptyTree := "4b825dc642cb6eb9a060e54bf8d69288fbee4904"
		out, err := exec.CommandContext(ctx, "git", "-C", dir, "commit-tree", emptyTree, "-p", id, "-m", "junk").Output()
		if err != nil {
			t.Fatalf("commit-tree: %v", err)
		}
		junk := strings.TrimSpace(string(out))
		mustGit(t, dir, "update-ref", "refs/zf/issues/"+id, junk)

		if _, err := client.AppendIssueCommit(ctx, id, []byte(`{"type":"add_label"}`), "add_label"); err != nil {
			t.Fatalf("AppendIssueCommit: %v", err)
		}

		commits, err := client.ReadIssueCommits(ctx, id)
		if err != nil {
			t.Fatalf("ReadIssueCommits: %v", err)
		}
		if len(commits) != 3 || commits[1].Payload != nil || string(commits[2].Payload) != `{"type":"add_label"}` {
			t.Errorf("payloads = %q", payloads(commits))
		}
	})
}

func TestIssueRef_Signing(t *testing.T) {
	t.Parallel()

	client, dir := newDiskRepo(t)
	ctx := t.Context()

	// A gpg program that always fails: a write succeeds only if git did not
	// try to sign.
	mustGit(t, dir, "config", "gpg.program", "false")

	t.Run("commit.gpgsign=false writes an unsigned commit", func(t *testing.T) {
		if _, err := client.CreateIssueRef(ctx, []byte(`{"n":1}`), "create"); err != nil {
			t.Errorf("CreateIssueRef: %v", err)
		}
	})

	t.Run("commit.gpgsign=true makes the write sign", func(t *testing.T) {
		mustGit(t, dir, "config", "commit.gpgsign", "true")

		if _, err := client.CreateIssueRef(ctx, []byte(`{"n":2}`), "create"); err == nil {
			t.Error("expected the write to fail because signing was attempted")
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git/... -run "^TestIssueRef_(CreateAppendRead|Corrupt|Signing)$"`
Expected: build failure, `client.CreateIssueRef undefined`, `undefined: IssueCommit`.

- [ ] **Step 3: Write the implementation**

Create `git/issue_ref.go`:

```go
package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	issueRefPrefix = "refs/zf/issues/"
	issueOpFile    = "op.json"
)

// ErrIssueRefCorrupt is returned by ReadIssueCommits when refs/zf/issues/<id>
// does not name a root commit of its own chain.
var ErrIssueRefCorrupt = errors.New("issue ref does not name a root of its chain")

// ErrIssueNotFound is returned when refs/zf/issues/<id> does not exist.
var ErrIssueNotFound = errors.New("issue not found")

// IssueCommit is one commit of an issue chain: its ID, its parents and the raw
// content of its op.json (nil when the commit has no such file).
type IssueCommit struct {
	ID      string
	Parents []string
	Payload []byte
}

// outputStdin is output with stdin fed to the command.
func (c *Client) outputStdin(ctx context.Context, stdin []byte, args ...string) (string, error) {
	cmd := c.gitCmd(ctx, args...)
	cmd.Stdin = bytes.NewReader(stdin)

	out, err := cmd.Output()
	if err != nil {
		return "", gitStderr(err)
	}

	return strings.TrimSpace(string(out)), nil
}

// writeIssueCommit stores payload as op.json in a new commit with the given
// parents and returns the commit ID. It does not move any ref. The commit is
// signed when commit.gpgsign is true: `git commit-tree` ignores that setting
// on its own.
func (c *Client) writeIssueCommit(ctx context.Context, payload []byte, message string, parents ...string) (string, error) {
	blob, err := c.outputStdin(ctx, payload, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", fmt.Errorf("hash-object: %w", err)
	}

	tree, err := c.outputStdin(ctx, []byte("100644 blob "+blob+"\t"+issueOpFile+"\n"), "mktree")
	if err != nil {
		return "", fmt.Errorf("mktree: %w", err)
	}

	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	if sign, _ := c.output(ctx, "config", "--bool", "commit.gpgsign"); sign == "true" {
		args = append(args, "-S")
	}

	commit, err := c.output(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", err)
	}

	return commit, nil
}

// CreateIssueRef writes payload as the root commit of a new issue chain and
// creates refs/zf/issues/<id>, where id is that commit's ID. Returns id.
func (c *Client) CreateIssueRef(ctx context.Context, payload []byte, message string) (string, error) {
	id, err := c.writeIssueCommit(ctx, payload, message)
	if err != nil {
		return "", err
	}

	// The zero old-value makes update-ref fail if the ref already exists.
	if _, err := c.output(ctx, "update-ref", issueRefPrefix+id, id, ZeroHash.String()); err != nil {
		return "", fmt.Errorf("create issue ref: %w", err)
	}

	return id, nil
}

// AppendIssueCommit writes payload as a new commit on top of issue id and
// moves the ref to it with compare-and-swap. Returns the new commit ID.
func (c *Client) AppendIssueCommit(ctx context.Context, id string, payload []byte, message string) (string, error) {
	tip, err := c.IssueTip(ctx, id)
	if err != nil {
		return "", err
	}
	if tip == "" {
		return "", fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	commit, err := c.writeIssueCommit(ctx, payload, message, tip)
	if err != nil {
		return "", err
	}

	if _, err := c.output(ctx, "update-ref", issueRefPrefix+id, commit, tip); err != nil {
		return "", fmt.Errorf("update issue ref: %w", err)
	}

	return commit, nil
}

// IssueTip returns the commit refs/zf/issues/<id> points at, or "" when the
// ref does not exist.
func (c *Client) IssueTip(ctx context.Context, id string) (string, error) {
	return c.refTip(ctx, issueRefPrefix+id)
}

func (c *Client) refTip(ctx context.Context, ref string) (string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(refname)", ref)
	if err != nil {
		return "", fmt.Errorf("for-each-ref %s: %w", ref, err)
	}

	// for-each-ref also matches refs *under* ref; keep the exact name only.
	for _, line := range strings.Split(out, "\n") {
		if tip, name, ok := strings.Cut(line, " "); ok && name == ref {
			return tip, nil
		}
	}

	return "", nil
}

// ListIssueIDs returns the IDs of all local issue refs.
func (c *Client) ListIssueIDs(ctx context.Context) ([]string, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(refname)", issueRefPrefix)
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", issueRefPrefix, err)
	}

	ids := []string{}
	for _, name := range strings.Fields(out) {
		ids = append(ids, strings.TrimPrefix(name, issueRefPrefix))
	}

	return ids, nil
}

// ReadIssueCommits returns every commit of issue id, parents before children,
// each with the content of its op.json.
func (c *Client) ReadIssueCommits(ctx context.Context, id string) ([]IssueCommit, error) {
	ref := issueRefPrefix + id

	tip, err := c.IssueTip(ctx, id)
	if err != nil {
		return nil, err
	}
	if tip == "" {
		return nil, fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	out, err := c.output(ctx, "rev-list", "--topo-order", "--reverse", "--parents", tip)
	if err != nil {
		return nil, fmt.Errorf("rev-list %s: %w", ref, err)
	}

	var (
		commits []IssueCommit
		specs   []string
		rooted  bool
	)
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}

		commits = append(commits, IssueCommit{ID: fields[0], Parents: fields[1:]})
		specs = append(specs, fields[0]+":"+issueOpFile)
		if len(fields) == 1 && fields[0] == id {
			rooted = true
		}
	}

	if !rooted {
		return nil, fmt.Errorf("%s: %w", ref, ErrIssueRefCorrupt)
	}

	cmd := c.gitCmd(ctx, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(specs, "\n") + "\n")

	raw, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("cat-file --batch: %w", gitStderr(err))
	}

	if err := readBatchPayloads(bufio.NewReader(bytes.NewReader(raw)), commits); err != nil {
		return nil, fmt.Errorf("parse cat-file output for %s: %w", ref, err)
	}

	return commits, nil
}

// readBatchPayloads parses `git cat-file --batch` output, one entry per commit
// in order. An entry is either "<oid> <type> <size>\n<content>\n" or
// "<spec> missing\n".
func readBatchPayloads(r *bufio.Reader, commits []IssueCommit) error {
	const headerFields = 3 // "<oid> <type> <size>"

	for i := range commits {
		header, err := r.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read header: %w", err)
		}

		fields := strings.Fields(header)
		if len(fields) != headerFields {
			continue // "<spec> missing": the commit has no op.json
		}

		size, err := strconv.Atoi(fields[2])
		if err != nil {
			return fmt.Errorf("object size %q: %w", fields[2], err)
		}

		content := make([]byte, size)
		if _, err := io.ReadFull(r, content); err != nil {
			return fmt.Errorf("read content: %w", err)
		}
		if _, err := r.Discard(1); err != nil { // trailing newline
			return fmt.Errorf("read content terminator: %w", err)
		}

		if fields[1] == "blob" {
			commits[i].Payload = content
		}
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git/... -run "^TestIssueRef_(CreateAppendRead|Corrupt|Signing)$" -v`
Expected: PASS. `TestIssueRef_Signing` proves the `-S` handling: with a `gpg.program` that always fails, the write succeeds when `commit.gpgsign` is false and fails when it is true.

- [ ] **Step 5: Commit**

Run `detect_changes()` first; expected: only new symbols in `git/issue_ref.go`.

```bash
git add git/issue_ref.go git/issue_ref_test.go
git commit -m "feat(git): store issue ops as commit chains under refs/zf/issues"
```

---

### Task 3: Fetch, reconcile and push issue refs

**Files:**
- Create: `git/issue_ref_sync.go`
- Test: `git/issue_ref_sync_test.go`

**Interfaces:**
- Consumes: Task 2 (`issueRefPrefix`, `writeIssueCommit`, `refTip`, `IssueTip`, `ErrIssueNotFound`); existing `(*Client).Remote`, `(*Client).runInteractive`, `(*Client).IsAncestor(ctx, a, b)` (true when `a` is an ancestor of `b`), `c.root`; test helpers `newDiskRepo`, `newDiskRepoWithOrigin`, `mustGit`.
- Produces:
  - `func (c *Client) FetchIssueRefs(ctx context.Context) error` (no-op without a remote)
  - `func (c *Client) ReconcileIssueRefs(ctx context.Context, mergePayload []byte) (merged int, err error)`
  - `func (c *Client) IssueRefPushed(ctx context.Context, id string) (bool, error)` (always true without a remote)
  - `func (c *Client) PushIssueRef(ctx context.Context, id string) error` (fast-forward only; no-op without a remote)

- [ ] **Step 1: Write the failing test**

Create `git/issue_ref_sync_test.go`:

```go
package git

import (
	"path/filepath"
	"testing"
)

// cloneOf clones originDir into a fresh directory and returns a client on it.
func cloneOf(t *testing.T, originDir, user string) (*Client, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), user)
	mustGit(t, filepath.Dir(dir), "clone", "-q", originDir, dir)
	mustGit(t, dir, "config", "user.name", user)
	mustGit(t, dir, "config", "user.email", user+"@test.com")
	mustGit(t, dir, "config", "commit.gpgsign", "false")

	c, err := NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c, dir
}

func TestIssueRef_FetchReconcilePush(t *testing.T) {
	t.Parallel()

	alice, _, originDir := newDiskRepoWithOrigin(t)
	bob, _ := cloneOf(t, originDir, "bob")
	ctx := t.Context()
	merge := []byte(`{"type":"merge"}`)

	id, err := alice.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("an unpushed issue is reported as not pushed", func(t *testing.T) {
		pushed, err := alice.IssueRefPushed(ctx, id)
		if err != nil || pushed {
			t.Errorf("IssueRefPushed = %v, %v; want false", pushed, err)
		}
	})

	t.Run("push publishes the ref and marks it pushed", func(t *testing.T) {
		if err := alice.PushIssueRef(ctx, id); err != nil {
			t.Fatalf("PushIssueRef: %v", err)
		}
		pushed, err := alice.IssueRefPushed(ctx, id)
		if err != nil || !pushed {
			t.Errorf("IssueRefPushed = %v, %v; want true", pushed, err)
		}
	})

	t.Run("fetch + reconcile creates the missing local ref on another clone", func(t *testing.T) {
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileIssueRefs = %d, %v", merged, err)
		}
		tip, _ := bob.IssueTip(ctx, id)
		if tip != id {
			t.Errorf("bob tip = %q, want %q", tip, id)
		}
	})

	t.Run("a local-only issue survives fetch + reconcile", func(t *testing.T) {
		local, err := bob.CreateIssueRef(ctx, []byte(`{"type":"create","n":"offline"}`), "create")
		if err != nil {
			t.Fatalf("CreateIssueRef: %v", err)
		}
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		if _, err := bob.ReconcileIssueRefs(ctx, merge); err != nil {
			t.Fatalf("ReconcileIssueRefs: %v", err)
		}
		if tip, _ := bob.IssueTip(ctx, local); tip != local {
			t.Errorf("local-only issue lost: tip = %q", tip)
		}
	})

	aliceTip, err := alice.AppendIssueCommit(ctx, id, []byte(`{"from":"alice"}`), "add_comment")
	if err != nil {
		t.Fatalf("alice append: %v", err)
	}
	if err := alice.PushIssueRef(ctx, id); err != nil {
		t.Fatalf("alice push: %v", err)
	}

	t.Run("a clone that is behind is fast-forwarded", func(t *testing.T) {
		carol, _ := cloneOf(t, originDir, "carol")
		mustGit(t, carol.root, "update-ref", "refs/zf/issues/"+id, id)

		if err := carol.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := carol.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileIssueRefs = %d, %v", merged, err)
		}
		if tip, _ := carol.IssueTip(ctx, id); tip != aliceTip {
			t.Errorf("carol tip = %q, want %q", tip, aliceTip)
		}
	})

	bobTip, err := bob.AppendIssueCommit(ctx, id, []byte(`{"from":"bob"}`), "add_comment")
	if err != nil {
		t.Fatalf("bob append: %v", err)
	}

	t.Run("a diverged push is rejected and nothing is overwritten", func(t *testing.T) {
		if err := bob.PushIssueRef(ctx, id); err == nil {
			t.Fatal("expected the non-fast-forward push to fail")
		}
		if err := alice.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("alice fetch: %v", err)
		}
		pushed, _ := alice.IssueRefPushed(ctx, id)
		if !pushed {
			t.Error("origin no longer matches alice's tip")
		}
	})

	t.Run("reconcile merges the diverged chains and the push then succeeds", func(t *testing.T) {
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 1 {
			t.Fatalf("ReconcileIssueRefs = %d, %v; want 1 merge", merged, err)
		}

		commits, err := bob.ReadIssueCommits(ctx, id)
		if err != nil {
			t.Fatalf("ReadIssueCommits: %v", err)
		}
		last := commits[len(commits)-1]
		if len(commits) != 4 || len(last.Parents) != 2 || string(last.Payload) != string(merge) {
			t.Fatalf("chain = %q, last parents = %v", payloads(commits), last.Parents)
		}
		if last.Parents[0] != bobTip || last.Parents[1] != aliceTip {
			t.Errorf("merge parents = %v, want [%s %s]", last.Parents, bobTip, aliceTip)
		}

		if err := bob.PushIssueRef(ctx, id); err != nil {
			t.Fatalf("PushIssueRef after merge: %v", err)
		}
	})

	t.Run("a second reconcile is a no-op", func(t *testing.T) {
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Errorf("ReconcileIssueRefs = %d, %v", merged, err)
		}
	})

	t.Run("a clone that is ahead is left alone", func(t *testing.T) {
		ahead, err := bob.AppendIssueCommit(ctx, id, []byte(`{"from":"bob2"}`), "add_comment")
		if err != nil {
			t.Fatalf("append: %v", err)
		}
		if err := bob.FetchIssueRefs(ctx); err != nil {
			t.Fatalf("FetchIssueRefs: %v", err)
		}
		merged, err := bob.ReconcileIssueRefs(ctx, merge)
		if err != nil || merged != 0 {
			t.Fatalf("ReconcileIssueRefs = %d, %v", merged, err)
		}
		if tip, _ := bob.IssueTip(ctx, id); tip != ahead {
			t.Errorf("tip = %q, want %q", tip, ahead)
		}
	})
}

func TestIssueRef_NoRemote(t *testing.T) {
	t.Parallel()

	client, _ := newDiskRepo(t)
	ctx := t.Context()

	id, err := client.CreateIssueRef(ctx, []byte(`{"type":"create"}`), "create")
	if err != nil {
		t.Fatalf("CreateIssueRef: %v", err)
	}

	t.Run("fetch is a no-op", func(t *testing.T) {
		if err := client.FetchIssueRefs(ctx); err != nil {
			t.Errorf("FetchIssueRefs: %v", err)
		}
	})

	t.Run("push is a no-op", func(t *testing.T) {
		if err := client.PushIssueRef(ctx, id); err != nil {
			t.Errorf("PushIssueRef: %v", err)
		}
	})

	t.Run("the issue counts as pushed", func(t *testing.T) {
		pushed, err := client.IssueRefPushed(ctx, id)
		if err != nil || !pushed {
			t.Errorf("IssueRefPushed = %v, %v", pushed, err)
		}
	})

	t.Run("reconcile with no tracking refs does nothing", func(t *testing.T) {
		merged, err := client.ReconcileIssueRefs(ctx, []byte(`{}`))
		if err != nil || merged != 0 {
			t.Errorf("ReconcileIssueRefs = %d, %v", merged, err)
		}
	})
}

// The first user of the feature fetches from a remote that has no
// refs/zf/issues/* at all.
func TestIssueRef_RemoteWithoutIssueRefs(t *testing.T) {
	t.Parallel()

	client, _, _ := newDiskRepoWithOrigin(t)
	ctx := t.Context()

	t.Run("fetch succeeds", func(t *testing.T) {
		if err := client.FetchIssueRefs(ctx); err != nil {
			t.Errorf("FetchIssueRefs: %v", err)
		}
	})

	t.Run("reconcile has nothing to do", func(t *testing.T) {
		merged, err := client.ReconcileIssueRefs(ctx, []byte(`{}`))
		if err != nil || merged != 0 {
			t.Errorf("ReconcileIssueRefs = %d, %v", merged, err)
		}
	})

	t.Run("no issue is listed", func(t *testing.T) {
		ids, err := client.ListIssueIDs(ctx)
		if err != nil || len(ids) != 0 {
			t.Errorf("ListIssueIDs = %v, %v", ids, err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./git/... -run "^TestIssueRef_(FetchReconcilePush|NoRemote|RemoteWithoutIssueRefs)$"`
Expected: build failure, `alice.IssueRefPushed undefined`, `bob.FetchIssueRefs undefined`.

- [ ] **Step 3: Write the implementation**

Create `git/issue_ref_sync.go`:

```go
package git

import (
	"context"
	"fmt"
	"strings"
)

const (
	// issueRemoteRefPrefix is the tracking namespace: what the remote's
	// refs/zf/issues/* looked like at the last fetch or push.
	issueRemoteRefPrefix = "refs/zf/remote/issues/"
	issueFetchRefspec    = "+" + issueRefPrefix + "*:" + issueRemoteRefPrefix + "*"
)

// FetchIssueRefs fetches the remote's refs/zf/issues/* into the tracking
// namespace refs/zf/remote/issues/*. Local issue refs are never touched, so an
// issue created offline cannot be lost. No-op when no remote is configured.
func (c *Client) FetchIssueRefs(ctx context.Context) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	if err := c.runInteractive(ctx, c.root, "fetch", "--quiet", remote, issueFetchRefspec); err != nil {
		return fmt.Errorf("fetch issue refs: %w", err)
	}

	return nil
}

// ReconcileIssueRefs brings every local issue ref up to date with its tracking
// ref: a missing local ref is created, a local ref that is behind is
// fast-forwarded, and a diverged one gets a two-parent commit carrying
// mergePayload as its op.json. A local ref that is ahead is left alone.
// Returns the number of merge commits written.
func (c *Client) ReconcileIssueRefs(ctx context.Context, mergePayload []byte) (int, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(refname)", issueRemoteRefPrefix)
	if err != nil {
		return 0, fmt.Errorf("for-each-ref %s: %w", issueRemoteRefPrefix, err)
	}

	merged := 0
	for _, line := range strings.Split(out, "\n") {
		remoteTip, name, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		id := strings.TrimPrefix(name, issueRemoteRefPrefix)

		didMerge, err := c.reconcileIssueRef(ctx, id, remoteTip, mergePayload)
		if err != nil {
			return merged, fmt.Errorf("reconcile issue %s: %w", id, err)
		}
		if didMerge {
			merged++
		}
	}

	return merged, nil
}

func (c *Client) reconcileIssueRef(ctx context.Context, id, remoteTip string, mergePayload []byte) (bool, error) {
	ref := issueRefPrefix + id

	local, err := c.IssueTip(ctx, id)
	if err != nil {
		return false, err
	}

	if local == "" {
		_, err := c.output(ctx, "update-ref", ref, remoteTip, ZeroHash.String())

		return false, err
	}
	if local == remoteTip {
		return false, nil
	}

	// IsAncestor(a, b) reports whether a is an ancestor of b.
	if ahead, err := c.IsAncestor(ctx, remoteTip, local); err != nil || ahead {
		return false, err
	}

	behind, err := c.IsAncestor(ctx, local, remoteTip)
	if err != nil {
		return false, err
	}
	if behind {
		_, err := c.output(ctx, "update-ref", ref, remoteTip, local)

		return false, err
	}

	commit, err := c.writeIssueCommit(ctx, mergePayload, "merge", local, remoteTip)
	if err != nil {
		return false, err
	}

	_, err = c.output(ctx, "update-ref", ref, commit, local)

	return err == nil, err
}

// IssueRefPushed reports whether the remote already has the local tip of
// issue id, according to the tracking ref. Always true without a remote.
func (c *Client) IssueRefPushed(ctx context.Context, id string) (bool, error) {
	remote, err := c.Remote()
	if err != nil {
		return false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return true, nil
	}

	local, err := c.IssueTip(ctx, id)
	if err != nil {
		return false, err
	}

	tracked, err := c.refTip(ctx, issueRemoteRefPrefix+id)
	if err != nil {
		return false, err
	}

	return local == tracked, nil
}

// PushIssueRef pushes refs/zf/issues/<id> to the remote with a plain,
// non-forced push: it succeeds only as a fast-forward, so it can never
// overwrite ops pushed by someone else. On success the tracking ref is moved
// to the pushed tip. No-op when no remote is configured.
func (c *Client) PushIssueRef(ctx context.Context, id string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	ref := issueRefPrefix + id

	tip, err := c.IssueTip(ctx, id)
	if err != nil {
		return err
	}
	if tip == "" {
		return fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	if err := c.runInteractive(ctx, c.root, "push", "--quiet", remote, ref+":"+ref); err != nil {
		return fmt.Errorf("push issue ref %s: %w", id, err)
	}

	if _, err := c.output(ctx, "update-ref", issueRemoteRefPrefix+id, tip); err != nil {
		return fmt.Errorf("update tracking ref: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./git/... -run "^TestIssueRef_" -v`
Expected: PASS for all `TestIssueRef_*` tests. Git prints a `! [rejected]` line during "a diverged push is rejected": that is the behavior under test.

- [ ] **Step 5: Commit**

Run `detect_changes()` first; expected: only new symbols in `git/issue_ref_sync.go`.

```bash
git add git/issue_ref_sync.go git/issue_ref_sync_test.go
git commit -m "feat(git): fetch, reconcile and fast-forward push issue refs"
```

---

### Task 4: The issue glue layer

**Files:**
- Create: `issue/repo.go`
- Test: `issue/repo_test.go`

**Interfaces:**
- Consumes: Task 1 (`Op`, `DecodeOp`, `Fold`, `Record`, constants); Tasks 2 and 3 (all `*git.Client` issue methods, `git.ErrIssueNotFound`); existing `(*git.Client).ConfigUser(ctx) (string, error)` returning `"Name <email>"`.
- Produces:
  - `type NewIssue struct { Title, Description, BranchType string; Labels []string }`
  - `func Create(ctx context.Context, c *git.Client, in NewIssue) (Record, error)`
  - `func Append(ctx context.Context, c *git.Client, id string, op *Op) error` (fills `V`, `At`, `Author`)
  - `func Load(ctx context.Context, c *git.Client, id string) (Record, error)`
  - `func List(ctx context.Context, c *git.Client) (records []Record, warnings []string, err error)` (newest first)
  - `func Resolve(ctx context.Context, c *git.Client, query string) (Record, error)`
  - `func Fetch(ctx context.Context, c *git.Client) (merged int, err error)`
  - `func Push(ctx context.Context, c *git.Client, id string) error` (one fetch, merge and retry on rejection)
  - `type SyncResult struct { Merged, Pushed int; Failed []string }`
  - `func Sync(ctx context.Context, c *git.Client) (SyncResult, error)`
  - None of `Create` / `Append` pushes. Callers push.

- [ ] **Step 1: Write the failing test**

Create `issue/repo_test.go`:

```go
package issue

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/git"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}

	return strings.TrimSpace(string(out))
}

// newRepo creates a repository with one commit and the given user identity.
// origin, when non-empty, is added as the "origin" remote.
func newRepo(t *testing.T, user, origin string) *git.Client {
	t.Helper()

	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", user)
	runGit(t, dir, "config", "user.email", user+"@test.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGit(t, dir, "add", "base.txt")
	runGit(t, dir, "commit", "-q", "-m", "chore: init")
	if origin != "" {
		runGit(t, dir, "remote", "add", "origin", origin)
	}

	c, err := git.NewClientAt(nil, dir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	return c
}

func newOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	runGit(t, t.TempDir(), "init", "-q", "--bare", dir)

	return dir
}

func TestCreateLoadAppend(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	rec, err := Create(ctx, c, NewIssue{Title: "Login fails", Description: "Steps to reproduce", BranchType: "fix", Labels: []string{"ui", "bug"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("Create returns the folded record", func(t *testing.T) {
		if rec.Title != "Login fails" || rec.Description != "Steps to reproduce" || rec.BranchType != "fix" {
			t.Errorf("record = %+v", rec)
		}
		if rec.State != StateOpen || !slices.Equal(rec.Labels, []string{"bug", "ui"}) {
			t.Errorf("state/labels = %q %v", rec.State, rec.Labels)
		}
		if len(rec.ID) != 40 || rec.CreatedAt.IsZero() {
			t.Errorf("ID = %q, CreatedAt = %v", rec.ID, rec.CreatedAt)
		}
	})

	t.Run("Append stamps the author from the git identity", func(t *testing.T) {
		if err := Append(ctx, c, rec.ID, &Op{Type: OpAddComment, Body: "me too"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := Load(ctx, c, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if len(got.Comments) != 1 || got.Comments[0].Body != "me too" {
			t.Fatalf("Comments = %+v", got.Comments)
		}
		if got.Comments[0].Author != "alice <alice@test.com>" || got.Comments[0].At.IsZero() {
			t.Errorf("comment = %+v", got.Comments[0])
		}
	})

	t.Run("Append on an unknown issue fails", func(t *testing.T) {
		err := Append(ctx, c, strings.Repeat("0", 40), &Op{Type: OpAddComment, Body: "x"})
		if !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})

	t.Run("a malformed op is skipped and reported in Warnings", func(t *testing.T) {
		if _, err := c.AppendIssueCommit(ctx, rec.ID, []byte(`{not json`), "junk"); err != nil {
			t.Fatalf("AppendIssueCommit: %v", err)
		}
		if err := Append(ctx, c, rec.ID, &Op{Type: OpSetState, Value: StateClosed}); err != nil {
			t.Fatalf("Append: %v", err)
		}

		got, err := Load(ctx, c, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		if got.State != StateClosed || got.Title != "Login fails" {
			t.Errorf("record = %+v", got)
		}
		if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "WARN:") {
			t.Errorf("Warnings = %v", got.Warnings)
		}
	})
}

func TestListAndResolve(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	first, err := Create(ctx, c, NewIssue{Title: "First", Description: "", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	second, err := Create(ctx, c, NewIssue{Title: "Second", Description: "", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("List returns every issue", func(t *testing.T) {
		recs, warnings, err := List(ctx, c)
		if err != nil || len(recs) != 2 || len(warnings) != 0 {
			t.Fatalf("List = %d records, %v, %v", len(recs), warnings, err)
		}
	})

	t.Run("List skips a corrupt ref with a warning", func(t *testing.T) {
		wrong := strings.Repeat("1", 40)
		root, _ := c.WorkingTreeRoot()
		runGit(t, root, "update-ref", "refs/zf/issues/"+wrong, first.ID)

		recs, warnings, err := List(ctx, c)
		runGit(t, root, "update-ref", "-d", "refs/zf/issues/"+wrong)
		if err != nil || len(recs) != 2 {
			t.Fatalf("List = %d records, %v", len(recs), err)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], wrong) {
			t.Errorf("warnings = %v", warnings)
		}
	})

	t.Run("Resolve by full ID", func(t *testing.T) {
		got, err := Resolve(ctx, c, second.ID)
		if err != nil || got.Title != "Second" {
			t.Errorf("Resolve = %+v, %v", got, err)
		}
	})

	t.Run("Resolve by short ID", func(t *testing.T) {
		got, err := Resolve(ctx, c, first.ShortID())
		if err != nil || got.ID != first.ID {
			t.Errorf("Resolve = %+v, %v", got, err)
		}
	})

	t.Run("Resolve of an unknown ID fails with ErrIssueNotFound", func(t *testing.T) {
		_, err := Resolve(ctx, c, "ffffffff")
		if !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("Resolve rejects a prefix shorter than 4 characters", func(t *testing.T) {
		_, err := Resolve(ctx, c, first.ID[:3])
		if !errors.Is(err, git.ErrIssueNotFound) || !strings.Contains(err.Error(), "at least 4") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestResolve_Ambiguous(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	// Create issues until two share their first 4 hex characters is too slow;
	// instead alias one chain under a second ref name sharing a 39-char prefix.
	rec, err := Create(ctx, c, NewIssue{Title: "Twin", Description: "", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	last := "0"
	if strings.HasSuffix(rec.ID, "0") {
		last = "1"
	}
	twin := rec.ID[:39] + last
	root, _ := c.WorkingTreeRoot()
	runGit(t, root, "update-ref", "refs/zf/issues/"+twin, rec.ID)

	t.Run("an ambiguous prefix lists the candidates", func(t *testing.T) {
		_, err := Resolve(ctx, c, rec.ID[:10])
		if err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("err = %v", err)
		}
		if !strings.Contains(err.Error(), rec.ID) || !strings.Contains(err.Error(), twin) || !strings.Contains(err.Error(), "Twin") {
			t.Errorf("err should list both IDs and the title: %v", err)
		}
	})

	t.Run("the full ID still resolves", func(t *testing.T) {
		got, err := Resolve(ctx, c, rec.ID)
		if err != nil || got.Title != "Twin" {
			t.Errorf("Resolve = %+v, %v", got, err)
		}
	})
}

func TestPushFetchSync(t *testing.T) {
	t.Parallel()

	origin := newOrigin(t)
	alice := newRepo(t, "alice", origin)
	bob := newRepo(t, "bob", origin)
	ctx := t.Context()

	rec, err := Create(ctx, alice, NewIssue{Title: "Shared", Description: "", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := Push(ctx, alice, rec.ID); err != nil {
		t.Fatalf("Push: %v", err)
	}

	t.Run("Fetch brings the issue to another clone", func(t *testing.T) {
		if _, err := Fetch(ctx, bob); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		got, err := Load(ctx, bob, rec.ID)
		if err != nil || got.Title != "Shared" {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})

	t.Run("comments written offline on two clones are both kept", func(t *testing.T) {
		if err := Append(ctx, alice, rec.ID, &Op{Type: OpAddComment, Body: "from alice"}); err != nil {
			t.Fatalf("alice Append: %v", err)
		}
		if err := Push(ctx, alice, rec.ID); err != nil {
			t.Fatalf("alice Push: %v", err)
		}
		if err := Append(ctx, bob, rec.ID, &Op{Type: OpAddComment, Body: "from bob"}); err != nil {
			t.Fatalf("bob Append: %v", err)
		}
		// Bob's push is rejected first, then merged and retried inside Push.
		if err := Push(ctx, bob, rec.ID); err != nil {
			t.Fatalf("bob Push: %v", err)
		}
		if _, err := Fetch(ctx, alice); err != nil {
			t.Fatalf("alice Fetch: %v", err)
		}

		for name, c := range map[string]*git.Client{"alice": alice, "bob": bob} {
			got, err := Load(ctx, c, rec.ID)
			if err != nil {
				t.Fatalf("%s Load: %v", name, err)
			}
			bodies := []string{}
			for _, k := range got.Comments {
				bodies = append(bodies, k.Body)
			}
			slices.Sort(bodies)
			if !slices.Equal(bodies, []string{"from alice", "from bob"}) {
				t.Errorf("%s sees comments %v", name, bodies)
			}
		}
	})

	t.Run("Sync pushes unpushed issues and reports the count", func(t *testing.T) {
		offline, err := Create(ctx, bob, NewIssue{Title: "Offline", Description: "", BranchType: "fix"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		res, err := Sync(ctx, bob)
		if err != nil || res.Pushed != 1 || len(res.Failed) != 0 {
			t.Fatalf("Sync = %+v, %v", res, err)
		}

		if _, err := Fetch(ctx, alice); err != nil {
			t.Fatalf("alice Fetch: %v", err)
		}
		if got, err := Load(ctx, alice, offline.ID); err != nil || got.Title != "Offline" {
			t.Errorf("alice Load = %+v, %v", got, err)
		}
	})

	t.Run("a second Sync has nothing to do", func(t *testing.T) {
		res, err := Sync(ctx, bob)
		if err != nil || res.Pushed != 0 || res.Merged != 0 {
			t.Errorf("Sync = %+v, %v", res, err)
		}
	})
}

func TestSync_NoRemote(t *testing.T) {
	t.Parallel()

	c := newRepo(t, "alice", "")
	ctx := t.Context()

	if _, err := Create(ctx, c, NewIssue{Title: "Local", Description: "", BranchType: "feat"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Run("Sync without a remote succeeds and pushes nothing", func(t *testing.T) {
		res, err := Sync(ctx, c)
		if err != nil || res.Pushed != 0 || len(res.Failed) != 0 {
			t.Errorf("Sync = %+v, %v", res, err)
		}
	})
}

// Issue refs live in the common git dir, so an issue created from inside a
// linked worktree is the same issue seen from the main checkout.
func TestLinkedWorktreeSharesIssues(t *testing.T) {
	t.Parallel()

	main := newRepo(t, "alice", "")
	ctx := t.Context()
	root, _ := main.WorkingTreeRoot()
	wtDir := filepath.Join(t.TempDir(), "wt")
	runGit(t, root, "worktree", "add", "-q", "-b", "side", wtDir)

	wt, err := git.NewClientAt(nil, wtDir)
	if err != nil {
		t.Fatalf("NewClientAt: %v", err)
	}

	rec, err := Create(ctx, wt, NewIssue{Title: "From worktree", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create in worktree: %v", err)
	}

	t.Run("the main checkout sees the issue", func(t *testing.T) {
		got, err := Load(ctx, main, rec.ID)
		if err != nil || got.Title != "From worktree" {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})

	t.Run("a comment from the main checkout is seen in the worktree", func(t *testing.T) {
		if err := Append(ctx, main, rec.ID, &Op{Type: OpAddComment, Body: "from main"}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		got, err := Load(ctx, wt, rec.ID)
		if err != nil || len(got.Comments) != 1 {
			t.Errorf("Load = %+v, %v", got, err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./issue/...`
Expected: build failure, `undefined: Create`, `undefined: NewIssue`, `undefined: Sync`.

- [ ] **Step 3: Write the implementation**

Create `issue/repo.go`:

```go
package issue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/piprim/git-zf/git"
)

// minPrefixLen is the shortest ID prefix Resolve accepts.
const minPrefixLen = 4

// marshalOp fills the fields every op written by this clone shares (V, At,
// Author) and returns the op.json bytes.
func marshalOp(ctx context.Context, c *git.Client, op *Op) ([]byte, error) {
	author, _ := c.ConfigUser(ctx)
	op.V, op.At, op.Author = OpVersion, time.Now().UTC().Format(time.RFC3339), author

	payload, err := json.Marshal(op)
	if err != nil {
		return nil, fmt.Errorf("marshal %s op: %w", op.Type, err)
	}

	return payload, nil
}

// NewIssue is the input of Create.
type NewIssue struct {
	Title       string
	Description string
	BranchType  string
	Labels      []string
}

// Create writes a new issue (a create op, then one add_label op per label)
// and returns its record. Nothing is pushed.
func Create(ctx context.Context, c *git.Client, in NewIssue) (Record, error) {
	payload, err := marshalOp(ctx, c, &Op{
		Type: OpCreate, Title: in.Title, Description: in.Description, BranchType: in.BranchType,
	})
	if err != nil {
		return Record{}, err
	}

	id, err := c.CreateIssueRef(ctx, payload, OpCreate)
	if err != nil {
		return Record{}, fmt.Errorf("create issue: %w", err)
	}

	for _, label := range in.Labels {
		if err := Append(ctx, c, id, &Op{Type: OpAddLabel, Value: label}); err != nil {
			return Record{}, err
		}
	}

	return Load(ctx, c, id)
}

// Append writes op on top of issue id. V, At and Author are filled in here.
// Nothing is pushed.
func Append(ctx context.Context, c *git.Client, id string, op *Op) error {
	payload, err := marshalOp(ctx, c, op)
	if err != nil {
		return err
	}

	if _, err := c.AppendIssueCommit(ctx, id, payload, op.Type); err != nil {
		return fmt.Errorf("write %s op on issue %s: %w", op.Type, id, err)
	}

	return nil
}

// Load reads the chain of issue id and folds it. Malformed commits are skipped
// and named in Record.Warnings.
func Load(ctx context.Context, c *git.Client, id string) (Record, error) {
	commits, err := c.ReadIssueCommits(ctx, id)
	if err != nil {
		return Record{}, fmt.Errorf("read issue %s: %w", id, err)
	}

	ops := make([]Op, 0, len(commits))
	var warnings []string
	for _, commit := range commits {
		op, ok := DecodeOp(commit.ID, commit.Parents, commit.Payload)
		if !ok {
			warnings = append(warnings, fmt.Sprintf("WARN: issue %s: skipping malformed op %s", id, commit.ID))
		}
		ops = append(ops, op)
	}

	rec := Fold(id, ops)
	rec.Warnings = warnings

	return rec, nil
}

// List loads every local issue, newest first. An unreadable ref is skipped;
// warnings names it, along with every malformed op met on the way.
func List(ctx context.Context, c *git.Client) (records []Record, warnings []string, err error) {
	ids, err := c.ListIssueIDs(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("list issues: %w", err)
	}

	records = make([]Record, 0, len(ids))
	for _, id := range ids {
		rec, err := Load(ctx, c, id)
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("WARN: skipping issue ref %s: %v", id, err))

			continue
		}

		warnings = append(warnings, rec.Warnings...)
		records = append(records, rec)
	}

	slices.SortStableFunc(records, func(a, b Record) int { return b.CreatedAt.Compare(a.CreatedAt) })

	return records, warnings, nil
}

// Resolve finds the issue designated by query: a full ID, or a unique ID
// prefix of at least 4 characters.
func Resolve(ctx context.Context, c *git.Client, query string) (Record, error) {
	ids, err := c.ListIssueIDs(ctx)
	if err != nil {
		return Record{}, fmt.Errorf("list issues: %w", err)
	}

	if slices.Contains(ids, query) {
		return Load(ctx, c, query)
	}

	if len(query) < minPrefixLen {
		return Record{}, fmt.Errorf("issue %q: %w (use at least %d characters of the ID)",
			query, git.ErrIssueNotFound, minPrefixLen)
	}

	var matches []string
	for _, id := range ids {
		if strings.HasPrefix(id, query) {
			matches = append(matches, id)
		}
	}

	switch len(matches) {
	case 0:
		return Record{}, fmt.Errorf("issue %q: %w", query, git.ErrIssueNotFound)
	case 1:
		return Load(ctx, c, matches[0])
	default:
		lines := make([]string, 0, len(matches))
		for _, id := range matches {
			title := "(unreadable)"
			if rec, err := Load(ctx, c, id); err == nil {
				title = rec.Title
			}
			lines = append(lines, "  "+id+"  "+title)
		}

		return Record{}, fmt.Errorf("issue ID %q is ambiguous:\n%s", query, strings.Join(lines, "\n"))
	}
}

// Fetch fetches the remote's issue refs and reconciles the local ones with
// them, merging diverged chains. Returns the number of merges. No-op without
// a remote.
func Fetch(ctx context.Context, c *git.Client) (merged int, err error) {
	if err := c.FetchIssueRefs(ctx); err != nil {
		return 0, fmt.Errorf("fetch issues: %w", err)
	}

	payload, err := marshalOp(ctx, c, &Op{Type: OpMerge})
	if err != nil {
		return 0, err
	}

	merged, err = c.ReconcileIssueRefs(ctx, payload)
	if err != nil {
		return merged, fmt.Errorf("reconcile issues: %w", err)
	}

	return merged, nil
}

// Push pushes issue id. A rejected push (someone pushed first) triggers one
// fetch, merge and retry. No-op without a remote.
func Push(ctx context.Context, c *git.Client, id string) error {
	firstErr := c.PushIssueRef(ctx, id)
	if firstErr == nil {
		return nil
	}

	if _, err := Fetch(ctx, c); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushIssueRef(ctx, id); err != nil {
		return fmt.Errorf("push issue %s after merge: %w", id, err)
	}

	return nil
}

// SyncResult summarizes one Sync.
type SyncResult struct {
	Merged int      // diverged chains merged
	Pushed int      // issues pushed
	Failed []string // one line per issue that could not be pushed
}

// Sync fetches and reconciles every issue, then pushes the ones the remote
// does not have yet. No-op without a remote.
func Sync(ctx context.Context, c *git.Client) (SyncResult, error) {
	var res SyncResult

	merged, err := Fetch(ctx, c)
	if err != nil {
		return res, err
	}
	res.Merged = merged

	ids, err := c.ListIssueIDs(ctx)
	if err != nil {
		return res, fmt.Errorf("list issues: %w", err)
	}

	for _, id := range ids {
		pushed, err := c.IssueRefPushed(ctx, id)
		if err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", id, err))

			continue
		}
		if pushed {
			continue
		}

		if err := Push(ctx, c, id); err != nil {
			res.Failed = append(res.Failed, fmt.Sprintf("%s: %v", id, err))

			continue
		}
		res.Pushed++
	}

	return res, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `mise exec -- go test ./issue/... -v`
Expected: PASS, including `TestPushFetchSync/comments_written_offline_on_two_clones_are_both_kept` and `TestLinkedWorktreeSharesIssues`.

- [ ] **Step 5: Commit**

Run `detect_changes()` first; expected: only new symbols in `issue/repo.go`.

```bash
git add issue/repo.go issue/repo_test.go
git commit -m "feat(issue): create, load, resolve and sync issues stored in the repository"
```

---

### Task 5: `issue new` and `issue show`

**Files:**
- Create: `tui/issue_record.go`, `cmd/issue/record.go`, `cmd/issue/new.go`, `cmd/issue/show.go`
- Test: `cmd/issue/record_e2e_test.go`

The two commands are not registered on the `issue` command yet: Task 6 registers all five at once. Their `run*` functions are tested directly, as the existing flows are.

**Interfaces:**
- Consumes: Task 4 (`issuepkg.NewIssue`, `Create`, `Load`, `List`, `Resolve`, `Fetch`, `Push`, `Record`); existing `cmdutil.NewClientForCmd`, `runGitIn` (test helper in `cmd/issue/start_e2e_test.go`).
- Produces:
  - `tui.IssueRecordNew` (`""`), `tui.IssueNewForm(title, branchType, description, labels *string, allowedBranchTypes []string) *huh.Group`, `tui.IssueRecordPicker(records []issue.Record, picked *string, offerNew bool) *huh.Group`, `tui.IssueCommentInput(body *string) *huh.Group`, `tui.IssueLabelInput(changes *string) *huh.Group`; unexported `requiredText`, `branchTypeOptions` (reused by Task 7).
  - `type recordPrompter interface { NewIssue(ctx, allowedTypes []string, in *issuepkg.NewIssue) error; PickRecord(ctx, records []issuepkg.Record) (string, error); CommentBody(ctx) (string, error); LabelChanges(ctx) (string, error) }` and `huhRecordPrompter`.
  - `commitTypeNames(cfg) []string`, `cleanLabels([]string) []string`, `printWarnings(w, []string)`, `fetchIssues(ctx, client)`, `pushIssue(ctx, client, id)`, `resolveRecord(ctx, client, p, args) (issuepkg.Record, error)`.
  - `(Issue).getNewCmd()`, `(Issue).newRunE(cmd, in issuepkg.NewIssue, interactive bool) error`, `runNew(ctx, client, cfg, in, p recordPrompter) error` (`p == nil` skips the form).
  - `(Issue).getShowCmd()`, `(Issue).showRunE(cmd, args []string, jsonOut bool) error`, `runShow(ctx, client, p, args, jsonOut) error`.
  - Test helpers for later tasks: `scriptedRecordPrompter`, `recordRig`, `newRecordRig(t, user, origin)`, `newBareOrigin(t)`, `(*recordRig).onlyRecord(t)`.

- [ ] **Step 1: Write the failing test**

Create `cmd/issue/record_e2e_test.go`:

```go
package issue

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	issuepkg "github.com/piprim/git-zf/issue"
)

// scriptedRecordPrompter returns canned answers for the repo-issue forms.
type scriptedRecordPrompter struct {
	New     issuepkg.NewIssue // copied into the form input by NewIssue
	PickID  string            // returned by PickRecord; "" picks the first record offered
	Comment string
	Labels  string
	Err     error // returned by every method when non-nil

	PickedFrom []issuepkg.Record // records PickRecord was offered
}

var _ recordPrompter = (*scriptedRecordPrompter)(nil)

func (s *scriptedRecordPrompter) NewIssue(_ context.Context, _ []string, in *issuepkg.NewIssue) error {
	if s.Err != nil {
		return s.Err
	}
	*in = s.New

	return nil
}

func (s *scriptedRecordPrompter) PickRecord(_ context.Context, records []issuepkg.Record) (string, error) {
	s.PickedFrom = records
	if s.Err != nil {
		return "", s.Err
	}
	if s.PickID == "" {
		return records[0].ID, nil
	}

	return s.PickID, nil
}

func (s *scriptedRecordPrompter) CommentBody(_ context.Context) (string, error) {
	return s.Comment, s.Err
}

func (s *scriptedRecordPrompter) LabelChanges(_ context.Context) (string, error) {
	return s.Labels, s.Err
}

// recordRig is a real on-disk repo for the repo-issue commands.
type recordRig struct {
	dir    string
	client *git.Client
	cfg    *config.AppConfig
	stdout *bytes.Buffer
	stderr *bytes.Buffer
}

// newRecordRig creates a repo with one commit. origin, when non-empty, is
// added as the "origin" remote.
func newRecordRig(t *testing.T, user, origin string) *recordRig {
	t.Helper()

	dir := t.TempDir()
	runGitIn(t, dir, "init", "-q", "-b", "main")
	runGitIn(t, dir, "config", "user.name", user)
	runGitIn(t, dir, "config", "user.email", user+"@test.com")
	runGitIn(t, dir, "config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatalf("write base.txt: %v", err)
	}
	runGitIn(t, dir, "add", "base.txt")
	runGitIn(t, dir, "commit", "-q", "-m", "chore: init")
	if origin != "" {
		runGitIn(t, dir, "remote", "add", "origin", origin)
	}

	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	client, err := git.NewClientAt(&pkg.IO{In: bytes.NewReader(nil), Out: stdout, Err: stderr}, dir)
	if err != nil {
		t.Fatalf("git.NewClientAt: %v", err)
	}

	cfg := &config.AppConfig{}
	cfg.CommitTypes = []config.CommitTypeOption{{Name: "feat"}, {Name: "fix"}}

	return &recordRig{dir: dir, client: client, cfg: cfg, stdout: stdout, stderr: stderr}
}

func newBareOrigin(t *testing.T) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "origin.git")
	runGitIn(t, t.TempDir(), "init", "-q", "--bare", dir)

	return dir
}

// onlyRecord returns the single issue of the rig's repo.
func (r *recordRig) onlyRecord(t *testing.T) issuepkg.Record {
	t.Helper()

	records, _, err := issuepkg.List(t.Context(), r.client)
	if err != nil || len(records) != 1 {
		t.Fatalf("want exactly one issue, got %d (%v)", len(records), err)
	}

	return records[0]
}

func TestRunNew_Flags(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	in := issuepkg.NewIssue{Title: "  Login fails  ", Description: "Steps", Labels: []string{"ui", " ", "ui", "bug"}}

	err := runNew(t.Context(), rig.client, rig.cfg, in, nil)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})

	rec := rig.onlyRecord(t)

	t.Run("the record holds the trimmed title, the description and an open state", func(t *testing.T) {
		if rec.Title != "Login fails" || rec.Description != "Steps" || rec.State != issuepkg.StateOpen {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the type defaults to the first commit type", func(t *testing.T) {
		if rec.BranchType != "feat" {
			t.Errorf("BranchType = %q", rec.BranchType)
		}
	})
	t.Run("labels are trimmed and de-duplicated", func(t *testing.T) {
		if !slices.Equal(rec.Labels, []string{"bug", "ui"}) {
			t.Errorf("Labels = %v", rec.Labels)
		}
	})
	t.Run("the display ID is printed", func(t *testing.T) {
		if want := "Created issue " + rec.DisplayID() + ": Login fails"; !strings.Contains(rig.stdout.String(), want) {
			t.Errorf("stdout = %q, want it to contain %q", rig.stdout.String(), want)
		}
	})
	t.Run("no warning without a remote", func(t *testing.T) {
		if rig.stderr.Len() != 0 {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestRunNew_Form(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	p := &scriptedRecordPrompter{New: issuepkg.NewIssue{Title: "From form", BranchType: "fix"}}

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the record comes from the form answers", func(t *testing.T) {
		rec := rig.onlyRecord(t)
		if rec.Title != "From form" || rec.BranchType != "fix" {
			t.Errorf("record = %+v", rec)
		}
	})
}

func TestRunNew_Rejections(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		in   issuepkg.NewIssue
		want string
	}{
		"empty title":      {issuepkg.NewIssue{Title: "   "}, "title is required"},
		"unknown type":     {issuepkg.NewIssue{Title: "T", BranchType: "nope"}, `unknown type "nope"`},
		"whitespace title": {issuepkg.NewIssue{Title: "\t\n"}, "title is required"},
	} {
		t.Run(name+" is refused and nothing is written", func(t *testing.T) {
			t.Parallel()

			rig := newRecordRig(t, "alice", "")
			err := runNew(t.Context(), rig.client, rig.cfg, tc.in, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if records, _, _ := issuepkg.List(t.Context(), rig.client); len(records) != 0 {
				t.Errorf("an issue was created: %+v", records)
			}
		})
	}

	t.Run("a form error aborts", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		p := &scriptedRecordPrompter{Err: errors.New("user aborted")}
		if err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{}, p); err == nil {
			t.Fatal("expected an error")
		}
	})

	t.Run("no commit types configured is refused", func(t *testing.T) {
		t.Parallel()

		rig := newRecordRig(t, "alice", "")
		err := runNew(t.Context(), rig.client, &config.AppConfig{}, issuepkg.NewIssue{Title: "T"}, nil)
		if err == nil || !strings.Contains(err.Error(), "no commit types") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestRunNew_PushesToRemote(t *testing.T) {
	t.Parallel()

	origin := newBareOrigin(t)
	alice := newRecordRig(t, "alice", origin)
	bob := newRecordRig(t, "bob", origin)

	if err := runNew(t.Context(), alice.client, alice.cfg, issuepkg.NewIssue{Title: "Shared"}, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rec := alice.onlyRecord(t)

	t.Run("another clone sees the issue with show", func(t *testing.T) {
		if err := runShow(t.Context(), bob.client, &scriptedRecordPrompter{}, []string{rec.ShortID()}, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if !strings.Contains(bob.stdout.String(), "Shared") {
			t.Errorf("stdout = %q", bob.stdout.String())
		}
	})
}

func TestRunNew_UnreachableRemoteWarns(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", filepath.Join(t.TempDir(), "missing.git"))

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Offline"}, nil)

	t.Run("the command still succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the issue exists locally", func(t *testing.T) {
		if rec := rig.onlyRecord(t); rec.Title != "Offline" {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("a warning says it was not pushed", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "not pushed") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

func TestRunShow(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{
		Title: "Login fails", Description: "Steps to reproduce", BranchType: "fix", Labels: []string{"ui"},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Append(ctx, rig.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddComment, Body: "me too"}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	t.Run("plain output shows title, state, labels, description and comments", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ID}, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		out := rig.stdout.String()
		for _, want := range []string{rec.ShortID() + "  Login fails", "State: open", "Type: fix", "Labels: ui", "Steps to reproduce", "me too", "alice <alice@test.com>", "ID: " + rec.ID} {
			if !strings.Contains(out, want) {
				t.Errorf("output misses %q:\n%s", want, out)
			}
		}
	})

	t.Run("--json emits the record", func(t *testing.T) {
		rig.stdout.Reset()
		if err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ShortID()}, true); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		var got issuepkg.Record
		if err := json.Unmarshal(rig.stdout.Bytes(), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", rig.stdout.String(), err)
		}
		if got.ID != rec.ID || got.Title != "Login fails" || len(got.Comments) != 1 {
			t.Errorf("json record = %+v", got)
		}
	})

	t.Run("without an ID the picker is used", func(t *testing.T) {
		rig.stdout.Reset()
		p := &scriptedRecordPrompter{}
		if err := runShow(ctx, rig.client, p, nil, false); err != nil {
			t.Fatalf("runShow: %v", err)
		}
		if len(p.PickedFrom) != 1 || !strings.Contains(rig.stdout.String(), "Login fails") {
			t.Errorf("picker offered %d records, stdout = %q", len(p.PickedFrom), rig.stdout.String())
		}
	})

	t.Run("an unknown ID is an error", func(t *testing.T) {
		err := runShow(ctx, rig.client, &scriptedRecordPrompter{}, []string{"ffffffff"}, false)
		if !errors.Is(err, git.ErrIssueNotFound) {
			t.Errorf("err = %v, want ErrIssueNotFound", err)
		}
	})
}

func TestRunShow_NoIssues(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")

	t.Run("without an ID and without issues the error points at issue new", func(t *testing.T) {
		err := runShow(t.Context(), rig.client, &scriptedRecordPrompter{}, nil, false)
		if err == nil || !strings.Contains(err.Error(), "issue new") {
			t.Errorf("err = %v", err)
		}
	})
}

// With several remotes and none named "origin", git-zf cannot pick one. The
// issue must still be saved locally, with a warning.
func TestRunNew_AmbiguousRemoteWarns(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	runGitIn(t, rig.dir, "remote", "add", "upstream", newBareOrigin(t))
	runGitIn(t, rig.dir, "remote", "add", "fork", newBareOrigin(t))

	err := runNew(t.Context(), rig.client, rig.cfg, issuepkg.NewIssue{Title: "Two remotes"}, nil)

	t.Run("the command still succeeds", func(t *testing.T) {
		if err != nil {
			t.Fatalf("runNew: %v", err)
		}
	})
	t.Run("the issue exists locally", func(t *testing.T) {
		if rec := rig.onlyRecord(t); rec.Title != "Two remotes" {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the warning names the remote problem", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "multiple remotes") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run "^(TestRunNew|TestRunShow)"`
Expected: build failure, `undefined: recordPrompter`, `undefined: runNew`, `undefined: runShow`.

- [ ] **Step 3: Write the forms**

Create `tui/issue_record.go`:

```go
package tui

import (
	"errors"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/issue"
)

// IssueRecordNew is the value IssueRecordPicker stores when the user picks
// the "New issue…" entry instead of a record.
const IssueRecordNew = ""

const issueRecordPickerHeight = 10

func requiredText(s string) error {
	if s == "" {
		return errors.New("required")
	}

	return nil
}

// branchTypeOptions builds the select options for the branch type.
func branchTypeOptions(allowed []string) []huh.Option[string] {
	if len(allowed) == 0 {
		return []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	opts := make([]huh.Option[string], 0, len(allowed))
	for _, a := range allowed {
		opts = append(opts, huh.NewOption(a, a))
	}

	return opts
}

// IssueNewForm is the form of `issue new`. labels is one comma-separated line.
func IssueNewForm(title, branchType, description, labels *string, allowedBranchTypes []string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(requiredText).
			Value(title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(branchTypeOptions(allowedBranchTypes)...).
			Value(branchType),
		huh.NewText().
			Title("Description:").
			Value(description),
		huh.NewInput().
			Title("Labels (comma-separated, optional):").
			Value(labels),
	)
}

// IssueRecordPicker lists repo issues as "[short-id] title". picked receives
// the full ID of the chosen record. With offerNew, a first "New issue…" entry
// stores IssueRecordNew instead.
func IssueRecordPicker(records []issue.Record, picked *string, offerNew bool) *huh.Group {
	opts := make([]huh.Option[string], 0, len(records)+1)
	if offerNew {
		opts = append(opts, huh.NewOption("New issue…", IssueRecordNew))
	}

	for i := range records {
		opts = append(opts, huh.NewOption("["+records[i].DisplayID()+"] "+records[i].Title, records[i].ID))
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Pick an issue:").
			Options(opts...).
			Value(picked).
			Height(issueRecordPickerHeight),
	)
}

// IssueCommentInput is the multiline form of `issue comment`.
func IssueCommentInput(body *string) *huh.Group {
	return huh.NewGroup(
		huh.NewText().
			Title("Comment:").
			Validate(requiredText).
			Value(body),
	)
}

// IssueLabelInput asks for label changes as "+add -remove" tokens.
func IssueLabelInput(changes *string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Label changes:").
			Placeholder("+bug -wontfix").
			Validate(requiredText).
			Value(changes),
	)
}
```

- [ ] **Step 4: Write the prompter and the shared helpers**

Create `cmd/issue/record.go`:

```go
package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tui"
)

// recordPrompter resolves the forms of the repo-issue commands (new, show,
// comment, label). The production implementation opens huh forms; tests use
// scriptedRecordPrompter.
type recordPrompter interface {
	// NewIssue fills in from the `issue new` form.
	NewIssue(ctx context.Context, allowedTypes []string, in *issuepkg.NewIssue) error
	// PickRecord returns the full ID of the record the user picked.
	PickRecord(ctx context.Context, records []issuepkg.Record) (string, error)
	// CommentBody returns the comment text.
	CommentBody(ctx context.Context) (string, error)
	// LabelChanges returns "+add -remove" tokens on one line.
	LabelChanges(ctx context.Context) (string, error)
}

type huhRecordPrompter struct{}

var _ recordPrompter = huhRecordPrompter{}

func (huhRecordPrompter) NewIssue(ctx context.Context, allowedTypes []string, in *issuepkg.NewIssue) error {
	var labels string
	if err := huh.NewForm(
		tui.IssueNewForm(&in.Title, &in.BranchType, &in.Description, &labels, allowedTypes),
	).RunWithContext(ctx); err != nil {
		return fmt.Errorf("new issue form: %w", err)
	}

	in.Labels = strings.Split(labels, ",")

	return nil
}

func (huhRecordPrompter) PickRecord(ctx context.Context, records []issuepkg.Record) (string, error) {
	var id string
	if err := huh.NewForm(tui.IssueRecordPicker(records, &id, false)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("issue picker: %w", err)
	}

	return id, nil
}

func (huhRecordPrompter) CommentBody(ctx context.Context) (string, error) {
	var body string
	if err := huh.NewForm(tui.IssueCommentInput(&body)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("comment form: %w", err)
	}

	return body, nil
}

func (huhRecordPrompter) LabelChanges(ctx context.Context) (string, error) {
	var changes string
	if err := huh.NewForm(tui.IssueLabelInput(&changes)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("label form: %w", err)
	}

	return changes, nil
}

// commitTypeNames returns the configured commit types, which are the allowed
// branch types of an issue.
func commitTypeNames(cfg *config.AppConfig) []string {
	names := make([]string, 0, len(cfg.CommitTypes))
	for _, t := range cfg.CommitTypes {
		names = append(names, t.Name)
	}

	return names
}

// cleanLabels trims labels and drops empty and duplicate ones.
func cleanLabels(labels []string) []string {
	seen := make(map[string]bool, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}

	return out
}

func printWarnings(w io.Writer, warnings []string) {
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
}

// fetchIssues refreshes the local issue refs from the remote. A failure
// (offline, auth) is a warning: every command then works on local data.
func fetchIssues(ctx context.Context, client *git.Client) {
	if _, err := issuepkg.Fetch(ctx, client); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: could not fetch issues, using local data: %v\n", err)
	}
}

// pushIssue pushes issue id. A failure is a warning: the op is committed
// locally and goes out with the next push or `git zf issue sync`.
func pushIssue(ctx context.Context, client *git.Client, id string) {
	if err := issuepkg.Push(ctx, client, id); err != nil {
		fmt.Fprintf(client.IO().Err,
			"warning: issue saved locally but not pushed (run `git zf issue sync` later): %v\n", err)
	}
}

// resolveRecord returns the record named by args[0], or opens the picker over
// all local issues when no ID was given.
func resolveRecord(
	ctx context.Context, client *git.Client, p recordPrompter, args []string,
) (issuepkg.Record, error) {
	if len(args) > 0 {
		rec, err := issuepkg.Resolve(ctx, client, args[0])
		if err != nil {
			return issuepkg.Record{}, fmt.Errorf("resolve issue: %w", err)
		}
		printWarnings(client.IO().Err, rec.Warnings)

		return rec, nil
	}

	records, warnings, err := issuepkg.List(ctx, client)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("list issues: %w", err)
	}
	printWarnings(client.IO().Err, warnings)

	if len(records) == 0 {
		return issuepkg.Record{}, errors.New("no issues in this repository; create one with `git zf issue new`")
	}

	id, err := p.PickRecord(ctx, records)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("pick issue: %w", err)
	}

	rec, err := issuepkg.Load(ctx, client, id)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("load issue: %w", err)
	}

	return rec, nil
}
```

- [ ] **Step 5: Write `issue new`**

Create `cmd/issue/new.go`:

```go
package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getNewCmd() *cobra.Command {
	var in issuepkg.NewIssue

	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create an issue in the repository (no branch)",
		Long: `Create an issue stored in the repository under refs/zf/issues/ and push it.
No branch is created: run "git zf issue start" to begin work on it.
Passing any flag skips the form.`,
		Args: cobra.NoArgs,
	}

	f := cmd.Flags()
	f.StringVar(&in.Title, "title", "", "issue title")
	f.StringVar(&in.BranchType, "type", "", "branch type, one of the configured commit types (default: the first)")
	f.StringVar(&in.Description, "description", "", "issue description")
	f.StringArrayVar(&in.Labels, "label", nil, "label to add (repeatable)")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return i.newRunE(cmd, in, cmd.Flags().NFlag() == 0)
	}

	return cmd
}

// newRunE runs `issue new`. interactive opens the form; it is false when any
// flag was passed.
func (i Issue) newRunE(cmd *cobra.Command, in issuepkg.NewIssue, interactive bool) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	var p recordPrompter
	if interactive {
		p = huhRecordPrompter{}
	}

	return runNew(cmd.Context(), client, i.appConfig, in, p)
}

// runNew creates the issue described by in. A non-nil p fills in from the
// form first.
func runNew(ctx context.Context, client *git.Client, cfg *config.AppConfig, in issuepkg.NewIssue, p recordPrompter) error {
	types := commitTypeNames(cfg)
	if len(types) == 0 {
		return errors.New("config: no commit types found")
	}

	if p != nil {
		if err := p.NewIssue(ctx, types, &in); err != nil {
			return fmt.Errorf("issue form: %w", err)
		}
	}

	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return errors.New("issue title is required (--title)")
	}

	if in.BranchType == "" {
		in.BranchType = types[0]
	}
	if !slices.Contains(types, in.BranchType) {
		return fmt.Errorf("unknown type %q (want one of: %s)", in.BranchType, strings.Join(types, ", "))
	}

	in.Labels = cleanLabels(in.Labels)

	rec, err := issuepkg.Create(ctx, client, in)
	if err != nil {
		return fmt.Errorf("create issue: %w", err)
	}

	pushIssue(ctx, client, rec.ID)
	fmt.Fprintf(client.IO().Out, "Created issue %s: %s\n", rec.DisplayID(), rec.Title)

	return nil
}
```

- [ ] **Step 6: Write `issue show`**

Create `cmd/issue/show.go`:

```go
package issue

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

const showTimeLayout = "2006-01-02 15:04"

func (i Issue) getShowCmd() *cobra.Command {
	var jsonOut bool

	cmd := &cobra.Command{
		Use:   "show [<id>]",
		Short: "Show an issue stored in the repository and its comments",
		Long: `Show an issue stored in the repository. <id> is the full ID or a unique
prefix of at least 4 characters. Without <id> a picker lists the issues.`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().BoolVar(&jsonOut, "json", false, "print the issue as JSON")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return i.showRunE(cmd, args, jsonOut)
	}

	return cmd
}

func (i Issue) showRunE(cmd *cobra.Command, args []string, jsonOut bool) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runShow(cmd.Context(), client, huhRecordPrompter{}, args, jsonOut)
}

func runShow(ctx context.Context, client *git.Client, p recordPrompter, args []string, jsonOut bool) error {
	fetchIssues(ctx, client)

	rec, err := resolveRecord(ctx, client, p, args)
	if err != nil {
		return err
	}

	if jsonOut {
		if err := json.NewEncoder(client.IO().Out).Encode(rec); err != nil {
			return fmt.Errorf("encode json: %w", err)
		}

		return nil
	}

	renderRecord(client.IO().Out, &rec)

	return nil
}

func renderRecord(w io.Writer, rec *issuepkg.Record) {
	fmt.Fprintf(w, "%s  %s\n", rec.DisplayID(), rec.Title)
	fmt.Fprintf(w, "State: %s   Type: %s   Created: %s\n",
		rec.State, rec.BranchType, rec.CreatedAt.Local().Format(showTimeLayout))
	if len(rec.Labels) > 0 {
		fmt.Fprintf(w, "Labels: %s\n", strings.Join(rec.Labels, ", "))
	}
	fmt.Fprintf(w, "ID: %s\n", rec.ID)

	if rec.Description != "" {
		fmt.Fprintf(w, "\n%s\n", rec.Description)
	}

	for _, c := range rec.Comments {
		fmt.Fprintf(w, "\n--- %s  %s\n%s\n", c.At.Local().Format(showTimeLayout), c.Author, c.Body)
	}
}
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... -run "^(TestRunNew|TestRunShow)" -v`
Expected: PASS. `TestRunNew_UnreachableRemoteWarns` and `TestRunNew_AmbiguousRemoteWarns` print git errors to the rig's stderr buffer, not to the terminal.

Then the build, since `getNewCmd` and `getShowCmd` are not referenced yet and must still compile:

Run: `mise exec -- go build ./... && mise exec -- go vet ./cmd/issue/ ./tui/`
Expected: no output.

- [ ] **Step 8: Commit**

Run `detect_changes()` first; expected: only new symbols.

```bash
git add tui/issue_record.go cmd/issue/record.go cmd/issue/new.go cmd/issue/show.go cmd/issue/record_e2e_test.go
git commit -m "feat(issue): add issue new and issue show for issues stored in the repository"
```

---

### Task 6: `issue comment`, `issue label`, `issue sync`, registration and menu

**Files:**
- Create: `cmd/issue/comment.go`, `cmd/issue/label.go`, `cmd/issue/sync.go`
- Modify: `cmd/issue/issue.go`, `tui/issue.go` (constants block and `IssueActionSelect`)
- Test: `cmd/issue/record_ops_e2e_test.go`

**Interfaces:**
- Consumes: Task 5 (`recordPrompter`, `huhRecordPrompter`, `resolveRecord`, `fetchIssues`, `pushIssue`, `newRunE`, `showRunE`, test rig); Task 4 (`Append`, `Load`, `Sync`, `Op`).
- Produces:
  - `(Issue).getCommentCmd()`, `(Issue).commentRunE(cmd, args []string, message string) error`, `runComment(ctx, client, p, args, message) error`
  - `(Issue).getLabelCmd()`, `(Issue).labelRunE(cmd *cobra.Command, args []string) error`, `parseLabelChanges(tokens []string) ([]issuepkg.Op, error)`, `runLabel(ctx, client, p, args) error`
  - `(Issue).getSyncCmd()`, `(Issue).syncRunE(cmd) error`, `runSync(ctx, client) error`
  - `tui.IssueActionNameNew`, `IssueActionNameShow`, `IssueActionNameComment`, `IssueActionNameLabel`, `IssueActionNameSync`

GitNexus: run `impact` on `IssueActionSelect` (recorded: LOW, one caller `Issue.runE`) and on `GetRootCmd` and `runE` in `cmd/issue/issue.go` before editing them, and report the result.

- [ ] **Step 1: Write the failing test**

Create `cmd/issue/record_ops_e2e_test.go`:

```go
package issue

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	issuepkg "github.com/piprim/git-zf/issue"
)

func TestRunComment(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	comments := func(t *testing.T) []string {
		t.Helper()

		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		bodies := make([]string, len(got.Comments))
		for i, c := range got.Comments {
			bodies[i] = c.Body
		}

		return bodies
	}

	t.Run("--message adds the comment without the form", func(t *testing.T) {
		p := &scriptedRecordPrompter{Err: errors.New("form must not open")}
		if err := runComment(ctx, rig.client, p, []string{rec.ShortID()}, "  from flag  "); err != nil {
			t.Fatalf("runComment: %v", err)
		}
		if got := comments(t); !slices.Equal(got, []string{"from flag"}) {
			t.Errorf("comments = %q", got)
		}
	})

	t.Run("without --message the form supplies the body", func(t *testing.T) {
		p := &scriptedRecordPrompter{Comment: "from form\nsecond line"}
		if err := runComment(ctx, rig.client, p, []string{rec.ID}, ""); err != nil {
			t.Fatalf("runComment: %v", err)
		}
		if got := comments(t); len(got) != 2 || got[1] != "from form\nsecond line" {
			t.Errorf("comments = %q", got)
		}
	})

	t.Run("an empty comment is refused", func(t *testing.T) {
		p := &scriptedRecordPrompter{Comment: "   "}
		err := runComment(ctx, rig.client, p, []string{rec.ID}, "")
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("err = %v", err)
		}
		if got := comments(t); len(got) != 2 {
			t.Errorf("comments = %q", got)
		}
	})
}

func TestRunLabel(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat", Labels: []string{"old"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	labels := func(t *testing.T) []string {
		t.Helper()

		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil {
			t.Fatalf("Load: %v", err)
		}

		return got.Labels
	}

	t.Run("+name adds and -name removes", func(t *testing.T) {
		if err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, []string{rec.ShortID(), "+bug", "-old", "+ui"}); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if got := labels(t); !slices.Equal(got, []string{"bug", "ui"}) {
			t.Errorf("labels = %v", got)
		}
		if !strings.Contains(rig.stdout.String(), "labels: bug, ui") {
			t.Errorf("stdout = %q", rig.stdout.String())
		}
	})

	t.Run("without tokens the form supplies them", func(t *testing.T) {
		p := &scriptedRecordPrompter{Labels: "+p1  -ui"}
		if err := runLabel(ctx, rig.client, p, []string{rec.ID}); err != nil {
			t.Fatalf("runLabel: %v", err)
		}
		if got := labels(t); !slices.Equal(got, []string{"bug", "p1"}) {
			t.Errorf("labels = %v", got)
		}
	})

	for name, tokens := range map[string][]string{
		"a token without sign": {"bug"},
		"a bare plus":          {"+"},
		"a bare minus":         {"-"},
		"a blank name":         {"+  "},
	} {
		t.Run(name+" is refused and nothing changes", func(t *testing.T) {
			before := labels(t)
			err := runLabel(ctx, rig.client, &scriptedRecordPrompter{}, append([]string{rec.ID}, tokens...))
			if err == nil || !strings.Contains(err.Error(), "want +name or -name") {
				t.Fatalf("err = %v", err)
			}
			if got := labels(t); !slices.Equal(got, before) {
				t.Errorf("labels changed: %v → %v", before, got)
			}
		})
	}
}

func TestRunSync(t *testing.T) {
	t.Parallel()

	origin := newBareOrigin(t)
	alice := newRecordRig(t, "alice", origin)
	bob := newRecordRig(t, "bob", origin)
	ctx := t.Context()

	if err := runNew(ctx, alice.client, alice.cfg, issuepkg.NewIssue{Title: "Shared"}, nil); err != nil {
		t.Fatalf("runNew: %v", err)
	}
	rec := alice.onlyRecord(t)

	t.Run("sync on another clone brings the issue in", func(t *testing.T) {
		if err := runSync(ctx, bob.client); err != nil {
			t.Fatalf("runSync: %v", err)
		}
		if got := bob.onlyRecord(t); got.ID != rec.ID {
			t.Errorf("bob record = %+v", got)
		}
	})

	t.Run("comments made on both clones before syncing are merged", func(t *testing.T) {
		// Written straight to the store so neither clone pushes yet.
		for c, body := range map[*recordRig]string{alice: "from alice", bob: "from bob"} {
			if err := issuepkg.Append(ctx, c.client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddComment, Body: body}); err != nil {
				t.Fatalf("Append: %v", err)
			}
		}

		if err := runSync(ctx, alice.client); err != nil {
			t.Fatalf("alice sync: %v", err)
		}
		bob.stdout.Reset()
		if err := runSync(ctx, bob.client); err != nil {
			t.Fatalf("bob sync: %v", err)
		}
		if !strings.Contains(bob.stdout.String(), "1 merged, 1 pushed") {
			t.Errorf("bob stdout = %q", bob.stdout.String())
		}
		if err := runSync(ctx, alice.client); err != nil {
			t.Fatalf("alice second sync: %v", err)
		}

		for name, rig := range map[string]*recordRig{"alice": alice, "bob": bob} {
			got, err := issuepkg.Load(ctx, rig.client, rec.ID)
			if err != nil || len(got.Comments) != 2 {
				t.Errorf("%s sees %d comments (%v)", name, len(got.Comments), err)
			}
		}
	})

	t.Run("sync without a remote reports nothing to do", func(t *testing.T) {
		solo := newRecordRig(t, "solo", "")
		if err := runSync(ctx, solo.client); err != nil {
			t.Fatalf("runSync: %v", err)
		}
		if !strings.Contains(solo.stdout.String(), "0 merged, 0 pushed") {
			t.Errorf("stdout = %q", solo.stdout.String())
		}
	})
}

func TestIssueRootCmd_registersRecordCommands(t *testing.T) {
	t.Parallel()

	root := New(&config.AppConfig{}).GetRootCmd()

	for _, name := range []string{"new", "show", "comment", "label", "sync"} {
		t.Run("issue "+name+" is registered", func(t *testing.T) {
			t.Parallel()

			sub, _, err := root.Find([]string{name})
			if err != nil || sub.Name() != name {
				t.Errorf("Find(%q) = %v, %v", name, sub, err)
			}
		})
	}
}

// "-wontfix" comes after <id>, so cobra must hand it over as an argument and
// not parse it as flags. Not parallel: the command opens the repository of the
// current directory.
func TestLabelCmd_DashTokenIsNotAFlag(t *testing.T) {
	rig := newRecordRig(t, "alice", "")
	rec, err := issuepkg.Create(t.Context(), rig.client, issuepkg.NewIssue{Title: "T", BranchType: "feat", Labels: []string{"wontfix"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	t.Chdir(rig.dir)

	root := New(rig.cfg).GetRootCmd()
	root.SetArgs([]string{"label", rec.ShortID(), "+bug", "-wontfix"})
	root.SetOut(rig.stdout)
	root.SetErr(rig.stderr)
	execErr := root.ExecuteContext(t.Context())

	t.Run("the command succeeds", func(t *testing.T) {
		if execErr != nil {
			t.Fatalf("Execute: %v", execErr)
		}
	})
	t.Run("the label was removed and the other added", func(t *testing.T) {
		got, err := issuepkg.Load(t.Context(), rig.client, rec.ID)
		if err != nil || !slices.Equal(got.Labels, []string{"bug"}) {
			t.Errorf("labels = %v (%v)", got.Labels, err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run "^(TestRunComment|TestRunLabel|TestRunSync|TestIssueRootCmd_registersRecordCommands|TestLabelCmd_DashTokenIsNotAFlag)$"`
Expected: build failure, `undefined: runComment`, `undefined: runLabel`, `undefined: runSync`.

- [ ] **Step 3: Write `issue comment`**

Create `cmd/issue/comment.go`:

```go
package issue

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getCommentCmd() *cobra.Command {
	var message string

	cmd := &cobra.Command{
		Use:   "comment [<id>]",
		Short: "Comment on an issue stored in the repository",
		Long: `Add a comment to an issue stored in the repository and push it. <id> is
the full ID or a unique prefix of at least 4 characters. Without <id> a picker
lists the issues. --message skips the comment form.`,
		Args: cobra.MaximumNArgs(1),
	}

	cmd.Flags().StringVarP(&message, "message", "m", "", "comment text (skips the form)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return i.commentRunE(cmd, args, message)
	}

	return cmd
}

func (i Issue) commentRunE(cmd *cobra.Command, args []string, message string) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runComment(cmd.Context(), client, huhRecordPrompter{}, args, message)
}

func runComment(ctx context.Context, client *git.Client, p recordPrompter, args []string, message string) error {
	fetchIssues(ctx, client)

	rec, err := resolveRecord(ctx, client, p, args)
	if err != nil {
		return err
	}

	body := message
	if body == "" {
		body, err = p.CommentBody(ctx)
		if err != nil {
			return fmt.Errorf("comment form: %w", err)
		}
	}

	body = strings.TrimSpace(body)
	if body == "" {
		return errors.New("comment is empty")
	}

	if err := issuepkg.Append(ctx, client, rec.ID, &issuepkg.Op{Type: issuepkg.OpAddComment, Body: body}); err != nil {
		return fmt.Errorf("add comment: %w", err)
	}

	pushIssue(ctx, client, rec.ID)
	fmt.Fprintf(client.IO().Out, "Commented on issue %s\n", rec.DisplayID())

	return nil
}
```

- [ ] **Step 4: Write `issue label`**

`cmd.Flags().SetInterspersed(false)` is what lets `-wontfix` through as an argument: flag parsing stops at the first positional argument, the issue ID.

Create `cmd/issue/label.go`:

```go
package issue

import (
	"context"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getLabelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "label [<id> [+add|-remove]...]",
		Short: "Add or remove labels on an issue stored in the repository",
		Long: `Add (+name) or remove (-name) labels on an issue stored in the repository
and push it:

    git zf issue label 1a2b3c4 +bug -wontfix

<id> is the full ID or a unique prefix of at least 4 characters. Without
arguments a picker lists the issues, then a form asks for the changes.`,
	}

	// Everything after <id> is positional, so "-wontfix" is not read as flags.
	cmd.Flags().SetInterspersed(false)

	cmd.RunE = i.labelRunE

	return cmd
}

func (i Issue) labelRunE(cmd *cobra.Command, args []string) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runLabel(cmd.Context(), client, huhRecordPrompter{}, args)
}

// parseLabelChanges turns "+add" / "-remove" tokens into ops.
func parseLabelChanges(tokens []string) ([]issuepkg.Op, error) {
	ops := make([]issuepkg.Op, 0, len(tokens))
	for _, tok := range tokens {
		name := strings.TrimSpace(tok[min(1, len(tok)):])

		switch {
		case name == "":
			return nil, fmt.Errorf("label change %q: want +name or -name", tok)
		case strings.HasPrefix(tok, "+"):
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpAddLabel, Value: name})
		case strings.HasPrefix(tok, "-"):
			ops = append(ops, issuepkg.Op{Type: issuepkg.OpRemoveLabel, Value: name})
		default:
			return nil, fmt.Errorf("label change %q: want +name or -name", tok)
		}
	}

	return ops, nil
}

func runLabel(ctx context.Context, client *git.Client, p recordPrompter, args []string) error {
	fetchIssues(ctx, client)

	var idArgs, tokens []string
	if len(args) > 0 {
		idArgs, tokens = args[:1], args[1:]
	}

	rec, err := resolveRecord(ctx, client, p, idArgs)
	if err != nil {
		return err
	}

	if len(tokens) == 0 {
		line, err := p.LabelChanges(ctx)
		if err != nil {
			return fmt.Errorf("label form: %w", err)
		}
		tokens = strings.Fields(line)
	}

	ops, err := parseLabelChanges(tokens)
	if err != nil {
		return err
	}
	if len(ops) == 0 {
		return fmt.Errorf("no label change given for issue %s", rec.DisplayID())
	}

	for i := range ops {
		if err := issuepkg.Append(ctx, client, rec.ID, &ops[i]); err != nil {
			return fmt.Errorf("update labels: %w", err)
		}
	}

	pushIssue(ctx, client, rec.ID)

	updated, err := issuepkg.Load(ctx, client, rec.ID)
	if err != nil {
		return fmt.Errorf("reload issue: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issue %s labels: %s\n", updated.DisplayID(), strings.Join(updated.Labels, ", "))

	return nil
}
```

- [ ] **Step 5: Write `issue sync`**

Create `cmd/issue/sync.go`:

```go
package issue

import (
	"context"
	"fmt"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getSyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sync",
		Short: "Fetch, merge and push the issues stored in the repository",
		Long: `Fetch refs/zf/issues/* from the remote, merge issues that were changed on
both sides, and push the issues the remote does not have yet. Without a remote
there is nothing to do.`,
		Args: cobra.NoArgs,
	}

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		return i.syncRunE(cmd)
	}

	return cmd
}

func (i Issue) syncRunE(cmd *cobra.Command) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	return runSync(cmd.Context(), client)
}

func runSync(ctx context.Context, client *git.Client) error {
	res, err := issuepkg.Sync(ctx, client)
	if err != nil {
		return fmt.Errorf("sync issues: %w", err)
	}

	fmt.Fprintf(client.IO().Out, "Issues synced: %d merged, %d pushed.\n", res.Merged, res.Pushed)

	for _, line := range res.Failed {
		fmt.Fprintf(client.IO().Err, "WARN: not pushed: %s\n", line)
	}
	if len(res.Failed) > 0 {
		return fmt.Errorf("%d issue(s) could not be pushed", len(res.Failed))
	}

	return nil
}
```

- [ ] **Step 6: Add the menu entries**

In `tui/issue.go`, replace the three action constants at the top of the `const (` block:

```go
	IssueActionNameStart = "issueStart"
	IssueActionNameList  = "issueList"
	IssueActionNameClose = "issueClose"
```

with:

```go
	IssueActionNameStart   = "issueStart"
	IssueActionNameList    = "issueList"
	IssueActionNameClose   = "issueClose"
	IssueActionNameNew     = "issueNew"
	IssueActionNameShow    = "issueShow"
	IssueActionNameComment = "issueComment"
	IssueActionNameLabel   = "issueLabel"
	IssueActionNameSync    = "issueSync"
```

In `IssueActionSelect`, after the `Close` option line:

```go
				huh.NewOption("Close\n"+descStyle.Render("Close an issue"), IssueActionNameClose),
```

add:

```go
				huh.NewOption("New\n"+descStyle.Render("Create an issue in the repository"), IssueActionNameNew),
				huh.NewOption("Show\n"+descStyle.Render("Show an issue and its comments"), IssueActionNameShow),
				huh.NewOption("Comment\n"+descStyle.Render("Comment on an issue"), IssueActionNameComment),
				huh.NewOption("Label\n"+descStyle.Render("Add or remove labels"), IssueActionNameLabel),
				huh.NewOption("Sync\n"+descStyle.Render("Fetch and push repository issues"), IssueActionNameSync),
```

- [ ] **Step 7: Register the commands and dispatch the menu**

Apply to `cmd/issue/issue.go`:

```diff
--- a/cmd/issue/issue.go
+++ b/cmd/issue/issue.go
@@ -6,6 +6,7 @@
 	"github.com/charmbracelet/huh"
 	"github.com/piprim/git-zf/cmd/review"
 	"github.com/piprim/git-zf/config"
+	issuepkg "github.com/piprim/git-zf/issue"
 	_ "github.com/piprim/git-zf/tracker/forgejo" // registers forgejo + gitea adapters
 	_ "github.com/piprim/git-zf/tracker/github"  // registers github adapter
 	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
@@ -28,7 +29,11 @@
 		RunE:  i.runE,
 	}
 
-	cmd.AddCommand(i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd(), review.TrackCmd(i.appConfig))
+	cmd.AddCommand(
+		i.getStartCmd(), i.getIssueListCmd(), i.getCloseCmd(),
+		i.getNewCmd(), i.getShowCmd(), i.getCommentCmd(), i.getLabelCmd(), i.getSyncCmd(),
+		review.TrackCmd(i.appConfig),
+	)
 
 	return cmd
 }
@@ -48,6 +53,16 @@
 		return i.issueListRunE(cmd, issueListFlags{})
 	case tui.IssueActionNameClose:
 		return i.closeRunE(cmd, args)
+	case tui.IssueActionNameNew:
+		return i.newRunE(cmd, issuepkg.NewIssue{}, true)
+	case tui.IssueActionNameShow:
+		return i.showRunE(cmd, nil, false)
+	case tui.IssueActionNameComment:
+		return i.commentRunE(cmd, nil, "")
+	case tui.IssueActionNameLabel:
+		return i.labelRunE(cmd, nil)
+	case tui.IssueActionNameSync:
+		return i.syncRunE(cmd)
 	default:
 		fmt.Fprintln(cmd.OutOrStdout(), "Not yet implemented.")
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... -run "^(TestRunComment|TestRunLabel|TestRunSync|TestIssueRootCmd_registersRecordCommands|TestLabelCmd_DashTokenIsNotAFlag|TestStartRunE_InteractiveDispatch)$" -v`
Expected: PASS. `TestRunSync/comments_made_on_both_clones_before_syncing_are_merged` asserts the summary line `1 merged, 1 pushed`.

Run: `mise exec -- go test ./cmd/... ./tui/...`
Expected: PASS (the menu tests in `cmd/` and `cmd/cmdutil` are unaffected).

- [ ] **Step 9: Commit**

Run `detect_changes()` first; expected changed symbols: `Issue.GetRootCmd`, `Issue.runE`, `IssueActionSelect`, plus the new ones.

```bash
git add cmd/issue/comment.go cmd/issue/label.go cmd/issue/sync.go cmd/issue/issue.go cmd/issue/record_ops_e2e_test.go tui/issue.go
git commit -m "feat(issue): add issue comment, label and sync, and list the repo-issue commands in the menu"
```

---

### Task 7: `issue start` reads and creates repo issues

**Files:**
- Modify: `issue/issue.go`, `git/branch_ref.go`, `tui/issue.go` (`IssueInput` and the import block), `cmd/issueflow/start_prompter.go`, `cmd/issueflow/start.go`
- Test: `cmd/issue/start_record_e2e_test.go` (new), `cmd/issue/start_prompter_test.go` (modify), `git/branch_ref_test.go` (modify)

**Interfaces:**
- Consumes: Task 4 (`issue.Fetch`, `List`, `Create`, `Push`, `NewIssue`, `Record`, `StateOpen`); Task 5 (`tui.IssueRecordPicker`, `requiredText`, `branchTypeOptions`); existing `branch.Slug`.
- Produces:
  - `issue.Issue.RecordID string`
  - `git.BranchRef.IssueID string` (JSON `issue_id`, `omitempty`), read by Task 8
  - `StartPrompter.PickIssueFromRepo(ctx context.Context, records []issuepkg.Record) (*issuepkg.Record, error)` (nil record = "New issue…")
  - `getFromRepoOrUser(ctx, c *git.Client, p StartPrompter, allowedTypes []string) (*issue.Issue, error)`, `issueFromRecord(rec *issue.Record) *issue.Issue`
  - `getFromTracker` gains a last parameter `manual func() (*issue.Issue, error)`
  - `writePushBranchRef(ctx, deps, b *branch.Branch, trackerType, recordID string) error`

GitNexus: run `impact` on `pickIssue`, `getFromTracker`, `writePushBranchRef` (**recorded: HIGH**, each on the `issue start` and `branch new` flows) and on `IssueInput`, `BranchRef`, `Issue` (struct in `issue/issue.go`) and `StartPrompter` before editing, and report the result to the user before proceeding. The regression net is the whole `TestRunIssueStart_*` suite plus `cmd/branch`.

- [ ] **Step 1: Write the failing tests**

Create `cmd/issue/start_record_e2e_test.go`:

```go
package issue

import (
	"strings"
	"testing"

	"github.com/piprim/git-zf/cmd/issueflow"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// An empty issue ID in the manual form creates a repo issue and names the
// branch after its short ID.
func TestRunIssueStart_ManualEmptyIDCreatesRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "fix", Issue: tracker.Issue{Subject: "Login fails"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})

	records, _, listErr := issuepkg.List(t.Context(), rig.client)
	if listErr != nil || len(records) != 1 {
		t.Fatalf("want one repo issue, got %d (%v)", len(records), listErr)
	}
	rec := records[0]
	wantBranch := rec.ShortID() + "@fix@login-fails"

	t.Run("the repo issue holds the title and type", func(t *testing.T) {
		if rec.Title != "Login fails" || rec.BranchType != "fix" || rec.State != issuepkg.StateOpen {
			t.Errorf("record = %+v", rec)
		}
	})
	t.Run("the picker is not opened when the repo has no issue", func(t *testing.T) {
		if prompter.CapturedRepoRecords != nil {
			t.Errorf("PickIssueFromRepo was called with %+v", prompter.CapturedRepoRecords)
		}
	})
	t.Run("the branch is named after the short ID", func(t *testing.T) {
		exists, err := rig.client.BranchExists(wantBranch)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", wantBranch, exists, err)
		}
	})
	t.Run("the branch ref records the full issue ID", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(t.Context(), rec.ShortID())
		if err != nil || ref == nil {
			t.Fatalf("ReadBranchRef = %+v, %v", ref, err)
		}
		if ref.IssueID != rec.ID || ref.BranchName != wantBranch {
			t.Errorf("ref = %+v", ref)
		}
	})
}

// With open issues in the repository the picker is offered, and the picked
// record drives the branch name without opening the manual form.
func TestRunIssueStart_PicksRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()

	open, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Open one", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	closed, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Closed one", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Append(ctx, rig.client, closed.ID, &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	prompter := &scriptedStartPrompter{IssueFromRepo: &open, ConfirmBranch: true}

	runErr := issueflow.RunIssueStart(ctx, rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("RunIssueStart: %v", runErr)
		}
	})
	t.Run("only open issues are offered", func(t *testing.T) {
		if len(prompter.CapturedRepoRecords) != 1 || prompter.CapturedRepoRecords[0].ID != open.ID {
			t.Errorf("offered = %+v", prompter.CapturedRepoRecords)
		}
	})
	t.Run("the branch is created for the picked issue", func(t *testing.T) {
		want := open.ShortID() + "@feat@open-one"
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
	t.Run("no second issue is created", func(t *testing.T) {
		records, _, _ := issuepkg.List(ctx, rig.client)
		if len(records) != 2 {
			t.Errorf("repo has %d issues, want 2", len(records))
		}
	})
	t.Run("the branch ref records the full issue ID", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(ctx, open.ShortID())
		if err != nil || ref == nil || ref.IssueID != open.ID {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
}

// "New issue…" in the picker falls through to the manual form; a typed ID is
// kept as is and creates no record.
func TestRunIssueStart_PickerNewThenTypedID(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	ctx := t.Context()

	if _, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Existing", BranchType: "feat"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	prompter := &scriptedStartPrompter{
		IssueFromRepo: nil, // "New issue…"
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{ID: "JIRA-7", Subject: "Typed"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(ctx, rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("the picker was offered", func(t *testing.T) {
		if len(prompter.CapturedRepoRecords) != 1 {
			t.Errorf("offered = %+v", prompter.CapturedRepoRecords)
		}
	})
	t.Run("the typed ID names the branch", func(t *testing.T) {
		exists, err := rig.client.BranchExists("JIRA-7@feat@typed")
		if err != nil || !exists {
			t.Errorf("branch exists = %v (%v)", exists, err)
		}
	})
	t.Run("no repo issue is created for a typed ID", func(t *testing.T) {
		records, _, _ := issuepkg.List(ctx, rig.client)
		if len(records) != 1 {
			t.Errorf("repo has %d issues, want 1", len(records))
		}
	})
	t.Run("the branch ref has no issue ID", func(t *testing.T) {
		ref, err := rig.client.ReadBranchRef(ctx, "JIRA-7")
		if err != nil || ref == nil || ref.IssueID != "" {
			t.Errorf("ref = %+v, %v", ref, err)
		}
	})
}

// When the tracker fails, the fallback is the same manual path: an empty ID
// creates a repo issue instead of producing a branch with no issue ID.
func TestRunIssueStart_TrackerErrorFallbackCreatesRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	rig.tracker.Issues = nil // empty list ⇒ NotifyTrackerError ⇒ manual path

	prompter := &scriptedStartPrompter{
		UseTracker:    true,
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{Subject: "Fallback"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.deps(issuepkg.IssueStartFlags{TrackerFirst: true}), prompter)

	t.Run("no error", func(t *testing.T) {
		if err != nil {
			t.Fatalf("RunIssueStart: %v", err)
		}
	})
	t.Run("the tracker error was notified", func(t *testing.T) {
		if prompter.TrackerErrorNotifications != 1 {
			t.Errorf("notifications = %d", prompter.TrackerErrorNotifications)
		}
	})
	t.Run("a repo issue was created and names the branch", func(t *testing.T) {
		records, _, _ := issuepkg.List(t.Context(), rig.client)
		if len(records) != 1 {
			t.Fatalf("repo has %d issues, want 1", len(records))
		}
		want := records[0].ShortID() + "@feat@fallback"
		exists, err := rig.client.BranchExists(want)
		if err != nil || !exists {
			t.Errorf("branch %q exists = %v (%v)", want, exists, err)
		}
	})
}

// A title that slugs to nothing must be refused before the repo issue is
// written, so no orphan issue is left behind.
func TestRunIssueStart_UnsluggableTitleCreatesNoIssue(t *testing.T) {
	t.Parallel()

	rig := newStartRig(t)
	prompter := &scriptedStartPrompter{
		IssueFromUser: &issuepkg.Issue{Type: "feat", Issue: tracker.Issue{Subject: "???"}},
		ConfirmBranch: true,
	}

	err := issueflow.RunIssueStart(t.Context(), rig.noTrackerDeps(issuepkg.IssueStartFlags{}), prompter)

	t.Run("the flow fails naming the title", func(t *testing.T) {
		if err == nil || !strings.Contains(err.Error(), "empty branch name") {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("no repo issue was created", func(t *testing.T) {
		records, _, _ := issuepkg.List(t.Context(), rig.client)
		if len(records) != 0 {
			t.Errorf("repo has %d issues, want 0", len(records))
		}
	})
}
```

Apply to `cmd/issue/start_prompter_test.go` (the scripted prompter gains the new method):

```diff
--- a/cmd/issue/start_prompter_test.go
+++ b/cmd/issue/start_prompter_test.go
@@ -20,6 +20,13 @@
 	IssueFromUser    *issuepkg.Issue
 	IssueFromTracker *issuepkg.Issue
 
+	// IssueFromRepo is returned by PickIssueFromRepo; nil means "New issue…".
+	IssueFromRepo    *issuepkg.Record
+	IssueFromRepoErr error
+	// CapturedRepoRecords records what PickIssueFromRepo was offered; nil ⇒
+	// the picker was never opened.
+	CapturedRepoRecords []issuepkg.Record
+
 	// Toggle return values.
 	UseTracker  bool
 	UseWorktree bool
@@ -78,6 +85,12 @@
 	return s.IssueFromTracker, nil
 }
 
+func (s *scriptedStartPrompter) PickIssueFromRepo(_ context.Context, records []issuepkg.Record) (*issuepkg.Record, error) {
+	s.CapturedRepoRecords = records
+
+	return s.IssueFromRepo, s.IssueFromRepoErr
+}
+
 func (s *scriptedStartPrompter) NotifyTrackerError(_ context.Context, _ string) error {
 	s.TrackerErrorNotifications++
```

Apply to `git/branch_ref_test.go` (append the test at the end of the file):

```diff
--- a/git/branch_ref_test.go
+++ b/git/branch_ref_test.go
@@ -198,3 +198,32 @@
 		}
 	})
 }
+
+func TestBranchRef_IssueID(t *testing.T) {
+	t.Parallel()
+
+	client, _ := newDiskRepo(t)
+	id := "0123456789abcdef0123456789abcdef01234567"
+
+	t.Run("IssueID round-trips", func(t *testing.T) {
+		in := BranchRef{IssueSlug: "0123456", BranchName: "0123456@feat@x", CreatedAt: "2026-10-03T10:00:00Z", IssueID: id}
+		if _, err := client.WriteBranchRef(t.Context(), "0123456", in); err != nil {
+			t.Fatalf("WriteBranchRef: %v", err)
+		}
+		got, err := client.ReadBranchRef(t.Context(), "0123456")
+		if err != nil || got == nil || got.IssueID != id {
+			t.Errorf("ReadBranchRef = %+v, %v", got, err)
+		}
+	})
+
+	t.Run("a ref written without IssueID reads back empty", func(t *testing.T) {
+		in := BranchRef{IssueSlug: "OLD-1", BranchName: "OLD-1@feat@x", CreatedAt: "2026-10-03T10:00:00Z"}
+		if _, err := client.WriteBranchRef(t.Context(), "OLD-1", in); err != nil {
+			t.Fatalf("WriteBranchRef: %v", err)
+		}
+		got, err := client.ReadBranchRef(t.Context(), "OLD-1")
+		if err != nil || got == nil || got.IssueID != "" {
+			t.Errorf("ReadBranchRef = %+v, %v", got, err)
+		}
+	})
+}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... ./git/... -run "^(TestRunIssueStart_|TestBranchRef_IssueID)"`
Expected: build failure, `unknown field IssueID in struct literal of type BranchRef`, `ref.IssueID undefined`, `prompter.CapturedRepoRecords` used with no `PickIssueFromRepo` in the interface.

- [ ] **Step 3: Add the two fields**

Apply to `issue/issue.go`:

```diff
--- a/issue/issue.go
+++ b/issue/issue.go
@@ -29,5 +29,9 @@
 // store.Issue (persisted).
 type Issue struct {
 	Type string // feat, fix, doc, etc…
+	// RecordID is the full ID of the repo issue (refs/zf/issues/<RecordID>)
+	// this entity was built from; "" when the issue has no record (tracker
+	// issue, or an ID typed by hand).
+	RecordID string
 	tracker.Issue
 }
```

Apply to `git/branch_ref.go`:

```diff
--- a/git/branch_ref.go
+++ b/git/branch_ref.go
@@ -27,6 +27,11 @@
 	// tracker status update. omitempty keeps pre-existing refs backward-
 	// compatible — an absent field unmarshals to "" (treated as "manual").
 	TrackerType string `json:"tracker_type,omitempty"`
+	// IssueID is the full ID of the repo issue (refs/zf/issues/<IssueID>) the
+	// branch works on. The branch name carries only a 7-character short ID,
+	// which may be ambiguous; this field is not. Empty when the issue has no
+	// record in the repository.
+	IssueID string `json:"issue_id,omitempty"`
 }
 
 // WriteBranchRef writes a BranchRef as a git blob and updates the local ref
```

- [ ] **Step 4: Make the Issue ID optional in the manual form**

In `tui/issue.go`, remove `"errors"` from the import block (the function below was its only user), and replace the whole `IssueInput` function with:

```go
// IssueInput is the manual issue form of `issue start` / `branch new`. An
// empty issue ID means "create a new issue in the repository": the caller
// then assigns the new record's ID.
func IssueInput(issueID, title, branchType *string, allowedBranchTypes []string) *huh.Group {
	return huh.NewGroup(
		huh.NewInput().
			Title("Issue ID (leave empty to create a new repo issue):").
			Placeholder("ABC-42").
			Value(issueID),
		huh.NewInput().
			Title("Title:").
			Placeholder("Short description of the issue").
			Validate(requiredText).
			Value(title),
		huh.NewSelect[string]().
			Title("Type:").
			Options(branchTypeOptions(allowedBranchTypes)...).
			Value(branchType),
	)
}
```

`requiredText` and `branchTypeOptions` live in `tui/issue_record.go` (Task 5).

- [ ] **Step 5: Extend the prompter**

Apply to `cmd/issueflow/start_prompter.go`:

```diff
--- a/cmd/issueflow/start_prompter.go
+++ b/cmd/issueflow/start_prompter.go
@@ -54,6 +54,12 @@
 	// getFromUser and getFromTracker.
 	Prompter
 
+	// PickIssueFromRepo opens the picker over the open issues stored in the
+	// repository. Called only when at least one exists. A nil record with a
+	// nil error means the operator chose "New issue…": the flow falls through
+	// to PickIssueFromUser.
+	PickIssueFromRepo(ctx context.Context, records []issuepkg.Record) (*issuepkg.Record, error)
+
 	// PickUseTracker drives the "fetch from tracker?" toggle. Called only when
 	// cfg.IssueTracker.Type != "". trackerFirst controls the pre-selected
 	// option (true for `issue start`, false for `branch new`).
@@ -133,6 +139,23 @@
 	return &got, nil
 }
 
+func (*HuhStartPrompter) PickIssueFromRepo(
+	ctx context.Context, records []issuepkg.Record,
+) (*issuepkg.Record, error) {
+	var id string
+	if err := huh.NewForm(tui.IssueRecordPicker(records, &id, true)).RunWithContext(ctx); err != nil {
+		return nil, fmt.Errorf("repo issue picker: %w", err)
+	}
+
+	for i := range records {
+		if records[i].ID == id {
+			return &records[i], nil
+		}
+	}
+
+	return nil, nil // tui.IssueRecordNew
+}
+
 func (p *HuhStartPrompter) NotifyTrackerError(ctx context.Context, message string) error {
 	if err := huh.NewForm(tui.IssueTrackerError(message)).RunWithContext(ctx); err != nil {
 		return fmt.Errorf("error note: %w", err)
```

- [ ] **Step 6: Route the manual path through repo issues**

Apply to `cmd/issueflow/start.go`. The hunks, in order: `pickIssue` builds a `manual` closure and passes it down; the new `getFromRepoOrUser` and `issueFromRecord`; `getFromTracker` takes and calls `manual` instead of `getFromUser`; `createFlow` and `writePushBranchRef` carry the record ID into the branch ref.

```diff
--- a/cmd/issueflow/start.go
+++ b/cmd/issueflow/start.go
@@ -132,8 +132,14 @@
 	prompter StartPrompter,
 	allowedBranchTypes []string,
 ) (*issue.Issue, error) {
+	// manual is the non-tracker path: an open issue stored in the repository,
+	// a new one, or an ID typed by hand.
+	manual := func() (*issue.Issue, error) {
+		return getFromRepoOrUser(ctx, deps.Client, prompter, allowedBranchTypes)
+	}
+
 	if deps.Tracker == nil {
-		got, err := getFromUser(ctx, prompter, allowedBranchTypes)
+		got, err := manual()
 		if err != nil {
 			return nil, fmt.Errorf("issue from user: %w", err)
 		}
@@ -147,7 +153,7 @@
 	}
 
 	if !useTracker {
-		got, err := getFromUser(ctx, prompter, allowedBranchTypes)
+		got, err := manual()
 		if err != nil {
 			return nil, fmt.Errorf("issue from user: %w", err)
 		}
@@ -155,7 +161,7 @@
 		return got, nil
 	}
 
-	got, err := getFromTracker(ctx, prompter, deps.Tracker, allowedBranchTypes)
+	got, err := getFromTracker(ctx, prompter, deps.Tracker, allowedBranchTypes, manual)
 	if err != nil {
 		return nil, fmt.Errorf("issue from tracker: %w", err)
 	}
@@ -163,6 +169,77 @@
 	return got, nil
 }
 
+// getFromRepoOrUser is the manual path. It offers the open issues stored in
+// the repository, when there are any; otherwise, or when the operator picks
+// "New issue…", it opens the manual form. A form answer with an empty issue ID
+// creates a new issue in the repository and uses its ID; a typed ID is used as
+// is, with no record (an issue living in a tracker git-zf does not talk to).
+func getFromRepoOrUser(
+	ctx context.Context, c *git.Client, p StartPrompter, allowedTypes []string,
+) (*issue.Issue, error) {
+	errW := c.IO().Err
+
+	if _, err := issue.Fetch(ctx, c); err != nil {
+		fmt.Fprintf(errW, "warning: could not fetch issues, using local data: %v\n", err)
+	}
+
+	records, warnings, err := issue.List(ctx, c)
+	if err != nil {
+		return nil, fmt.Errorf("list repo issues: %w", err)
+	}
+	for _, w := range warnings {
+		fmt.Fprintln(errW, w)
+	}
+
+	open := make([]issue.Record, 0, len(records))
+	for i := range records {
+		if records[i].State == issue.StateOpen {
+			open = append(open, records[i])
+		}
+	}
+
+	if len(open) > 0 {
+		rec, err := p.PickIssueFromRepo(ctx, open)
+		if err != nil {
+			return nil, fmt.Errorf("repo issue picker: %w", err)
+		}
+		if rec != nil {
+			return issueFromRecord(rec), nil
+		}
+	}
+
+	got, err := getFromUser(ctx, p, allowedTypes)
+	if err != nil || got == nil || got.ID != "" {
+		return got, err
+	}
+
+	// Check the title before writing anything: an issue whose branch cannot
+	// be named would be left behind as an orphan.
+	if branch.Slug(got.Subject) == "" {
+		return nil, fmt.Errorf("title %q produces an empty branch name", got.Subject)
+	}
+
+	rec, err := issue.Create(ctx, c, issue.NewIssue{Title: got.Subject, BranchType: got.Type})
+	if err != nil {
+		return nil, fmt.Errorf("create repo issue: %w", err)
+	}
+	if err := issue.Push(ctx, c, rec.ID); err != nil {
+		fmt.Fprintf(errW, "warning: issue saved locally but not pushed (run `git zf issue sync` later): %v\n", err)
+	}
+
+	return issueFromRecord(&rec), nil
+}
+
+// issueFromRecord converts a repo issue to the in-flow entity. The display ID
+// goes into the branch name; RecordID keeps the unambiguous full ID.
+func issueFromRecord(rec *issue.Record) *issue.Issue {
+	return &issue.Issue{
+		Type:     rec.BranchType,
+		RecordID: rec.ID,
+		Issue:    tracker.Issue{ID: rec.DisplayID(), Subject: rec.Title, Description: rec.Description},
+	}
+}
+
 // getFromUser drives the manual issue-input flow via p.PickIssueFromUser.
 // Moved here from the issue domain package: it is application-layer
 // orchestration over the UI prompter, not entity logic.
@@ -176,9 +253,12 @@
 }
 
 // getFromTracker fetches issues via t.ListIssues, then either falls back to
-// the manual path (PickIssueFromUser) on error/empty-list, or drives the
+// the manual path (the manual callback) on error/empty-list, or drives the
 // tracker picker (PickIssueFromTracker). All form opening is delegated to p.
-func getFromTracker(ctx context.Context, p Prompter, t tracker.Tracker, allowedTypes []string) (*issue.Issue, error) {
+func getFromTracker(
+	ctx context.Context, p Prompter, t tracker.Tracker, allowedTypes []string,
+	manual func() (*issue.Issue, error),
+) (*issue.Issue, error) {
 	errMsg := ""
 	issues, listErr := t.ListIssues(ctx)
 	if listErr != nil {
@@ -194,7 +274,7 @@
 			return nil, fmt.Errorf("notify tracker error: %w", err)
 		}
 
-		return getFromUser(ctx, p, allowedTypes)
+		return manual()
 	}
 
 	out, err := p.PickIssueFromTracker(ctx, issues, allowedTypes)
@@ -308,7 +388,7 @@
 		}
 	}
 
-	if err := writePushBranchRef(ctx, deps, b.IssueID(), branchName, trackerType); err != nil {
+	if err := writePushBranchRef(ctx, deps, b, trackerType, picked.RecordID); err != nil {
 		fmt.Fprintf(deps.Client.IO().Err, "warning: write branch ref: %v\n", err)
 	}
 
@@ -566,13 +646,17 @@
 // writePushBranchRef writes a BranchRef to refs/zf/branches/<issueSlug> and
 // pushes it to the remote (best-effort). Called after every successful branch
 // or worktree creation so the parent-child relationship is available cross-machine.
-func writePushBranchRef(ctx context.Context, deps StartDeps, issueSlug, branchName, trackerType string) error {
+func writePushBranchRef(
+	ctx context.Context, deps StartDeps, b *branch.Branch, trackerType, recordID string,
+) error {
+	issueSlug := b.IssueID()
 	ref := git.BranchRef{
 		IssueSlug:   issueSlug,
-		BranchName:  branchName,
+		BranchName:  b.Name(),
 		ParentSlug:  deps.Flags.ParentIssueSlug,
 		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
 		TrackerType: trackerType,
+		IssueID:     recordID,
 	}
 	if _, err := deps.Client.WriteBranchRef(ctx, issueSlug, ref); err != nil {
 		return err
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestRunIssueStart_" -v`
Expected: PASS for the five new tests and all pre-existing `TestRunIssueStart_*` tests. The pre-existing ones type an ID in the scripted form, so they exercise the unchanged "typed ID" branch.

Run: `mise exec -- go test ./cmd/... ./git/... ./tui/... ./issue/...`
Expected: PASS. `cmd/branch` covers `branch new`, which shares `RunIssueStart`.

- [ ] **Step 8: Commit**

Run `detect_changes()` first; expected changed symbols: `pickIssue`, `getFromTracker`, `createFlow`, `writePushBranchRef`, `IssueInput`, `StartPrompter`, `HuhStartPrompter`, `BranchRef`, `Issue`, plus the new ones. Anything else is unexpected.

```bash
git add issue/issue.go git/branch_ref.go git/branch_ref_test.go tui/issue.go cmd/issueflow/start.go cmd/issueflow/start_prompter.go cmd/issue/start_prompter_test.go cmd/issue/start_record_e2e_test.go
git commit -m "feat(issue): start work on an issue stored in the repository, or create one from the manual form"
```

---

### Task 8: `issue close` closes the repo issue

**Files:**
- Modify: `cmd/issue/record.go` (append `closeRepoIssue`), `cmd/issue/close.go` (`updateClosedStatus`)
- Test: `cmd/issue/close_record_e2e_test.go`

**Interfaces:**
- Consumes: Task 7 (`git.BranchRef.IssueID`); Task 5 (`pushIssue`); Task 4 (`issuepkg.Append`, `Op`, `OpSetState`, `StateClosed`); existing `newCloseRig`, `scriptedPrompter`, `assertBranchAbsent`.
- Produces: `closeRepoIssue(ctx context.Context, client *git.Client, ref *git.BranchRef)` (no-op for a nil ref or an empty `IssueID`).

GitNexus: run `impact` on `updateClosedStatus` (recorded: LOW, one caller `runClose`) before editing, and report the result.

`cmd/issue/close.go` sits just under the linter's 500-line file limit. That is why the helper goes into `record.go` and the call site is a single line.

- [ ] **Step 1: Write the failing test**

Create `cmd/issue/close_record_e2e_test.go`:

```go
package issue

import (
	"strings"
	"testing"

	commitpkg "github.com/piprim/git-zf/commit"
	issuepkg "github.com/piprim/git-zf/issue"
)

// Closing a branch whose BranchRef names a repo issue closes that issue.
func TestClose_ClosesRepoIssue(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()

	rec, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Add thing", BranchType: "feat"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	ref, err := rig.client.ReadBranchRef(ctx, "ABC-1")
	if err != nil || ref == nil {
		t.Fatalf("ReadBranchRef = %+v, %v", ref, err)
	}
	ref.IssueID = rec.ID
	if _, err := rig.client.WriteBranchRef(ctx, "ABC-1", *ref); err != nil {
		t.Fatalf("WriteBranchRef: %v", err)
	}

	prompter := &scriptedPrompter{
		Branch:        rig.pickedBranchRow(),
		Strategy:      commitpkg.MergeStrategySquash,
		Confirm:       true,
		Message:       []byte("feat(thing): close ABC-1\n"),
		TrackerStatus: "Closed",
		DeleteBranch:  true,
	}

	runErr := runClose(ctx, rig.deps(), prompter)

	t.Run("no error", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("the repo issue is closed", func(t *testing.T) {
		got, err := issuepkg.Load(ctx, rig.client, rec.ID)
		if err != nil || got.State != issuepkg.StateClosed {
			t.Errorf("state = %q (%v)", got.State, err)
		}
	})
	t.Run("no warning is printed", func(t *testing.T) {
		if strings.Contains(rig.stderr.String(), "close repo issue") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
}

// A BranchRef naming an issue that does not exist must not fail the close:
// the merge already landed.
func TestClose_MissingRepoIssueIsAWarning(t *testing.T) {
	t.Parallel()

	rig := newCloseRig(t)
	ctx := t.Context()

	ref, err := rig.client.ReadBranchRef(ctx, "ABC-1")
	if err != nil || ref == nil {
		t.Fatalf("ReadBranchRef = %+v, %v", ref, err)
	}
	ref.IssueID = strings.Repeat("0", 39) + "1"
	if _, err := rig.client.WriteBranchRef(ctx, "ABC-1", *ref); err != nil {
		t.Fatalf("WriteBranchRef: %v", err)
	}

	prompter := &scriptedPrompter{
		Branch:       rig.pickedBranchRow(),
		Strategy:     commitpkg.MergeStrategySquash,
		Confirm:      true,
		Message:      []byte("feat(thing): close ABC-1\n"),
		DeleteBranch: true,
	}

	runErr := runClose(ctx, rig.deps(), prompter)

	t.Run("the close still succeeds", func(t *testing.T) {
		if runErr != nil {
			t.Fatalf("runClose: %v", runErr)
		}
	})
	t.Run("a warning names the failure", func(t *testing.T) {
		if !strings.Contains(rig.stderr.String(), "warning: close repo issue") {
			t.Errorf("stderr = %q", rig.stderr.String())
		}
	})
	t.Run("the feature branch is still deleted", func(t *testing.T) {
		assertBranchAbsent(t, rig.client, "ABC-1@feat@add-thing")
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_(ClosesRepoIssue|MissingRepoIssueIsAWarning)$" -v`
Expected: FAIL. `TestClose_ClosesRepoIssue/the_repo_issue_is_closed` reports `state = "open"`, and `TestClose_MissingRepoIssueIsAWarning/a_warning_names_the_failure` finds no warning.

- [ ] **Step 3: Add the helper**

Append to `cmd/issue/record.go`:

```go
// closeRepoIssue closes the repo issue a merged branch worked on: it writes
// set_state closed on the issue named by ref.IssueID and pushes it. A nil ref
// or one without an issue ID (tracker issue, hand-typed ID) is a no-op. Like
// the rest of updateClosedStatus, a failure is a warning: the merge already
// landed.
func closeRepoIssue(ctx context.Context, client *git.Client, ref *git.BranchRef) {
	if ref == nil || ref.IssueID == "" {
		return
	}

	id := ref.IssueID
	op := &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}
	if err := issuepkg.Append(ctx, client, id, op); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: close repo issue: %v\n", err)

		return
	}

	pushIssue(ctx, client, id)
}
```

- [ ] **Step 4: Call it from the close flow**

Apply to `cmd/issue/close.go`:

```diff
--- a/cmd/issue/close.go
+++ b/cmd/issue/close.go
@@ -699,6 +699,8 @@
 		}
 	}
 
+	closeRepoIssue(ctx, deps.client, existing)
+
 	// Only offer a tracker status update for tracker-born issues. The origin
 	// lives in the git object (BranchRef.TrackerType), not the local store, so
 	// this is correct on a reviewer's clone too. A manual issue (ref absent or
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... -run "^TestClose_" -v`
Expected: PASS for the two new tests and the whole pre-existing close suite (its BranchRefs have no `IssueID`, so `closeRepoIssue` is a no-op there).

Run: `mise exec -- go test ./cmd/mergeflow/... ./cmd/branch/...`
Expected: PASS.

- [ ] **Step 6: Commit**

Run `detect_changes()` first; expected changed symbol: `updateClosedStatus`, plus the new `closeRepoIssue`.

```bash
git add cmd/issue/record.go cmd/issue/close.go cmd/issue/close_record_e2e_test.go
git commit -m "feat(issue): close the repository issue when its branch is closed"
```

---

### Task 9: `issue list` shows repo issues

**Files:**
- Modify: `store/store.go` (`IssueRow`), `store/helpers.go`, `tty/issue.go`, `tui/issue.go` (table functions), `cmd/issue/list.go`
- Test: `cmd/issue/list_record_test.go` (new), `tui/issue_test.go` (modify)

**Interfaces:**
- Consumes: Task 4 (`issuepkg.List`, `Record`, `StateOpen`, `StateClosed`); Task 5 (`fetchIssues`, `printWarnings`, `newRecordRig`); existing `openTestIssueStore`, `buildFromStore`, `cmdutil.NewClientForCmd`.
- Produces:
  - `store.IssueRow.Labels []string` (JSON `labels`), `store.IssueRow.State string` (JSON `state`; `""` = no repo issue)
  - `store.TitleWithLabels(r *IssueRow) string`
  - `issueListInfra.client *git.Client` (nil skips repo issues)
  - `mergeRepoIssues(ctx, infra, rows []store.IssueRow, status string) ([]store.IssueRow, error)`
  - `matchesStatus(r *store.IssueRow, status string) bool` and `issueRowToTableRow(r *store.IssueRow, includeProject bool) btable.Row` now take a pointer: the row grew past the linter's by-value limit.

Behavior: with a tracker configured and reachable, nothing changes. Otherwise the store rows are enriched with the labels and state of their repo issue, and every repo issue without a branch is appended. The column that showed `N.A.` shows the issue state for those rows, so its header becomes `Issue Status`.

GitNexus: run `impact` on `buildRows`, `matchesStatus`, `issueRowToTableRow`, `RenderIssueTable` (**recorded: HIGH**, all on the `issue list` flow) and on `IssueRow` and `issueListRunE` before editing, and report the result to the user before proceeding.

- [ ] **Step 1: Write the failing tests**

Create `cmd/issue/list_record_test.go`:

```go
package issue

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
)

func TestBuildRows_RepoIssues(t *testing.T) {
	t.Parallel()

	rig := newRecordRig(t, "alice", "")
	ctx := t.Context()
	s := openTestIssueStore(t)

	started, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Started", BranchType: "feat", Labels: []string{"ui"}})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backlog, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Backlog", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	done, err := issuepkg.Create(ctx, rig.client, issuepkg.NewIssue{Title: "Done", BranchType: "fix"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := issuepkg.Append(ctx, rig.client, done.ID, &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}); err != nil {
		t.Fatalf("Append: %v", err)
	}

	// One repo issue has a branch; one store row is a legacy issue with no record.
	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: started.ShortID(), Title: "Started", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: started.ShortID() + "@feat@started", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: "JIRA-7", Title: "Legacy", StatusID: store.StatusIDInProgress},
		&store.Branch{Name: "JIRA-7@feat@legacy", Type: "feat", StatusID: store.StatusIDInProgress},
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	infra := issueListInfra{store: s, stderr: &bytes.Buffer{}, client: rig.client}

	bySlug := func(t *testing.T, status string) map[string]store.IssueRow {
		t.Helper()

		rows, err := buildRows(ctx, infra, status)
		if err != nil {
			t.Fatalf("buildRows: %v", err)
		}
		out := make(map[string]store.IssueRow, len(rows))
		for _, r := range rows {
			out[r.IssueSlug] = r
		}

		return out
	}

	t.Run("no filter lists branch rows and every repo issue once", func(t *testing.T) {
		rows := bySlug(t, "")
		if len(rows) != 4 {
			t.Fatalf("want 4 rows, got %d: %+v", len(rows), rows)
		}
	})

	t.Run("a started repo issue carries its branch, labels and state", func(t *testing.T) {
		row := bySlug(t, "")[started.ShortID()]
		if row.Branch == nil || row.State != issuepkg.StateOpen || !slices.Equal(row.Labels, []string{"ui"}) {
			t.Errorf("row = %+v", row)
		}
		if row.TrackerStatus == nil || *row.TrackerStatus != issuepkg.StateOpen {
			t.Errorf("TrackerStatus = %v", row.TrackerStatus)
		}
	})

	t.Run("a backlog repo issue appears with no branch", func(t *testing.T) {
		row, ok := bySlug(t, "")[backlog.ShortID()]
		if !ok || row.Branch != nil || row.Title != "Backlog" || row.State != issuepkg.StateOpen {
			t.Errorf("row = %+v (present %v)", row, ok)
		}
	})

	t.Run("a legacy store row without a record is kept unchanged", func(t *testing.T) {
		row := bySlug(t, "")["JIRA-7"]
		if row.Branch == nil || row.State != "" || row.TrackerStatus != nil {
			t.Errorf("row = %+v", row)
		}
	})

	t.Run("status open hides the closed repo issue", func(t *testing.T) {
		rows := bySlug(t, "open")
		if _, ok := rows[done.ShortID()]; ok {
			t.Errorf("closed issue listed under open: %+v", rows)
		}
		if _, ok := rows[backlog.ShortID()]; !ok {
			t.Errorf("backlog issue missing under open: %+v", rows)
		}
	})

	t.Run("status closed lists the closed repo issue and not the backlog", func(t *testing.T) {
		rows := bySlug(t, "closed")
		if _, ok := rows[done.ShortID()]; !ok {
			t.Errorf("closed issue missing: %+v", rows)
		}
		if _, ok := rows[backlog.ShortID()]; ok {
			t.Errorf("backlog issue listed under closed: %+v", rows)
		}
	})

	t.Run("json output carries labels and state", func(t *testing.T) {
		var buf bytes.Buffer
		if err := runList(ctx, &buf, infra, issueListFlags{jsonOut: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		var rows []store.IssueRow
		if err := json.Unmarshal(buf.Bytes(), &rows); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		found := false
		for _, r := range rows {
			if r.IssueSlug == started.ShortID() {
				found = r.State == "open" && slices.Equal(r.Labels, []string{"ui"})
			}
		}
		if !found {
			t.Errorf("json = %s", buf.String())
		}
	})

	t.Run("stdout table shows labels next to the title and the issue status header", func(t *testing.T) {
		var buf bytes.Buffer
		if err := runList(ctx, &buf, infra, issueListFlags{stdout: true}); err != nil {
			t.Fatalf("runList: %v", err)
		}
		out := buf.String()
		for _, want := range []string{"Started [ui]", "ISSUE STATUS", "Backlog"} {
			if !strings.Contains(out, want) {
				t.Errorf("table misses %q:\n%s", want, out)
			}
		}
	})
}
```

Apply to `tui/issue_test.go` (append the two tests at the end of the file):

```diff
--- a/tui/issue_test.go
+++ b/tui/issue_test.go
@@ -217,3 +217,43 @@
 		}
 	})
 }
+
+func TestMatchesStatus_RepoIssueState(t *testing.T) {
+	merged := &store.BranchRow{Status: store.BranchStatusMerged}
+
+	for name, tc := range map[string]struct {
+		row    store.IssueRow
+		status string
+		want   bool
+	}{
+		"closed record without branch is not open":    {store.IssueRow{State: "closed"}, statusOpen, false},
+		"closed record without branch is closed":      {store.IssueRow{State: "closed"}, statusClosed, true},
+		"open record without branch is open":          {store.IssueRow{State: "open"}, statusOpen, true},
+		"open record with a merged branch stays open": {store.IssueRow{State: "open", Branch: merged}, statusOpen, true},
+		"open record with a merged branch not closed": {store.IssueRow{State: "open", Branch: merged}, statusClosed, false},
+		"any record matches all":                      {store.IssueRow{State: "closed"}, statusAll, true},
+		"row without state falls back to its branch":  {store.IssueRow{Branch: merged}, statusClosed, true},
+	} {
+		t.Run(name, func(t *testing.T) {
+			if got := matchesStatus(&tc.row, tc.status); got != tc.want {
+				t.Errorf("matchesStatus = %v, want %v", got, tc.want)
+			}
+		})
+	}
+}
+
+func TestIssueRowToTableRow_Labels(t *testing.T) {
+	t.Run("labels are appended to the title cell", func(t *testing.T) {
+		row := issueRowToTableRow(&store.IssueRow{IssueSlug: "1a2b3c4", Title: "Login fails", Labels: []string{"bug", "ui"}}, false)
+		if row[1] != "Login fails [bug, ui]" {
+			t.Errorf("title cell = %q", row[1])
+		}
+	})
+
+	t.Run("no labels leaves the bare title", func(t *testing.T) {
+		row := issueRowToTableRow(&store.IssueRow{IssueSlug: "1", Title: "Plain"}, false)
+		if row[1] != "Plain" {
+			t.Errorf("title cell = %q", row[1])
+		}
+	})
+}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mise exec -- go test ./cmd/issue/... ./tui/... -run "^(TestBuildRows_RepoIssues|TestMatchesStatus_RepoIssueState|TestIssueRowToTableRow_Labels)$"`
Expected: build failure, `unknown field client in struct literal of type issueListInfra`, `unknown field State in struct literal of type store.IssueRow`.

- [ ] **Step 3: Extend the row type**

Apply to `store/store.go`:

```diff
--- a/store/store.go
+++ b/store/store.go
@@ -109,6 +109,11 @@
 	Project       string     `json:"project"`        // tracker project / repo; empty when unknown
 	TrackerStatus *string    `json:"tracker_status"` // nil → display "N.A."
 	Branch        *BranchRow `json:"branch"`         // nil → not started locally
+	// Labels and State are set for issues stored in the repository
+	// (refs/zf/issues/*). State is "open" or "closed"; "" means the row has no
+	// repo issue and its status is derived from the branch.
+	Labels []string `json:"labels"`
+	State  string   `json:"state"`
 }
 
 // CommandHistoryRow is one row from the command_history table.
```

Apply to `store/helpers.go`:

```diff
--- a/store/helpers.go
+++ b/store/helpers.go
@@ -1,5 +1,7 @@
 package store
 
+import "strings"
+
 // BranchFieldOrEmpty returns fn(b) or "∅" when b is nil.
 func BranchFieldOrEmpty(b *BranchRow, fn func(*BranchRow) string) string {
 	if b == nil {
@@ -17,3 +19,15 @@
 
 	return *s
 }
+
+// TitleWithLabels returns the row title followed by its labels in brackets,
+// e.g. "Login fails [bug, ui]", or the bare title when there are none.
+// ponytail: labels share the Title cell instead of getting their own column;
+// add a column if the title gets truncated too often in practice.
+func TitleWithLabels(r *IssueRow) string {
+	if len(r.Labels) == 0 {
+		return r.Title
+	}
+
+	return r.Title + " [" + strings.Join(r.Labels, ", ") + "]"
+}
```

- [ ] **Step 4: Update the plain table**

Apply to `tty/issue.go`:

```diff
--- a/tty/issue.go
+++ b/tty/issue.go
@@ -17,7 +17,7 @@
 		headers = append(headers, "PROJECT")
 	}
 
-	headers = append(headers, "TITLE", "BRANCH", "LOCAL STATUS", "TRACKER STATUS", "CREATED")
+	headers = append(headers, "TITLE", "BRANCH", "LOCAL STATUS", "ISSUE STATUS", "CREATED")
 
 	t := lgtable.New().
 		Headers(headers...).
@@ -29,14 +29,15 @@
 			return lipgloss.NewStyle()
 		})
 
-	for _, r := range rows {
+	for i := range rows {
+		r := &rows[i]
 		cells := []string{r.IssueSlug}
 		if includeProject {
 			cells = append(cells, r.Project)
 		}
 
 		cells = append(cells,
-			r.Title,
+			store.TitleWithLabels(r),
 			store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
 			store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
 			store.TrackerStatusOrNA(r.TrackerStatus),
```

- [ ] **Step 5: Update the TUI table**

Apply to `tui/issue.go` (four hunks: the title cell, the column header, `matchesStatus`, and the loop in `applyFilters` that now passes a pointer):

```diff
--- a/tui/issue.go
+++ b/tui/issue.go
@@ -238,7 +227,7 @@
 	}, nil
 }
 
-func issueRowToTableRow(r store.IssueRow, includeProject bool) btable.Row {
+func issueRowToTableRow(r *store.IssueRow, includeProject bool) btable.Row {
 	row := make(btable.Row, 0, issueTableMaxCols)
 	row = append(row, r.IssueSlug)
 
@@ -247,7 +236,7 @@
 	}
 
 	return append(row,
-		r.Title,
+		store.TitleWithLabels(r),
 		store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return b.BranchName }),
 		store.BranchFieldOrEmpty(r.Branch, func(b *store.BranchRow) string { return string(b.Status) }),
 		store.TrackerStatusOrNA(r.TrackerStatus),
@@ -270,18 +259,29 @@
 		btable.Column{Title: "Title", Width: issueTableColWidthTitle},
 		btable.Column{Title: "Branch", Width: issueTableColWidthBranch},
 		btable.Column{Title: "Local Status", Width: issueTableColWidthLocalStatus},
-		btable.Column{Title: "Tracker Status", Width: issueTableColWidthTrackerStatus},
+		btable.Column{Title: "Issue Status", Width: issueTableColWidthTrackerStatus},
 		btable.Column{Title: "Created", Width: issueTableColWidthCreated},
 	)
 }
 
-func matchesStatus(r store.IssueRow, status string) bool {
+// matchesStatus reports whether r belongs under the status tab. A row backed
+// by a repo issue (State set) follows the issue's own state; any other row
+// falls back to its branch status.
+func matchesStatus(r *store.IssueRow, status string) bool {
 	switch status {
-	case statusClosed:
-		return r.Branch != nil && r.Branch.Status == store.BranchStatusMerged
 	case statusAll:
 		return true
+	case statusClosed:
+		if r.State != "" {
+			return r.State == statusClosed
+		}
+
+		return r.Branch != nil && r.Branch.Status == store.BranchStatusMerged
 	default: // "open" and anything else
+		if r.State != "" {
+			return r.State == statusOpen
+		}
+
 		return r.Branch == nil || r.Branch.Status == store.BranchStatusInProgress
 	}
 }
@@ -293,7 +293,8 @@
 	q := strings.ToLower(text)
 	out := make([]btable.Row, 0, len(rows))
 
-	for _, r := range rows {
+	for i := range rows {
+		r := &rows[i]
 		if !matchesStatus(r, status) {
 			continue
 		}
```

- [ ] **Step 6: Merge repo issues into the listing**

Apply to `cmd/issue/list.go`:

```diff
--- a/cmd/issue/list.go
+++ b/cmd/issue/list.go
@@ -9,6 +9,9 @@
 
 	tea "github.com/charmbracelet/bubbletea"
 
+	"github.com/piprim/git-zf/cmd/cmdutil"
+	"github.com/piprim/git-zf/git"
+	issuepkg "github.com/piprim/git-zf/issue"
 	"github.com/piprim/git-zf/store"
 	"github.com/piprim/git-zf/tracker"
 	"github.com/piprim/git-zf/tty"
@@ -26,6 +29,9 @@
 	tracker tracker.Tracker
 	store   *store.Store
 	stderr  io.Writer
+	// client reads the issues stored in the repository. nil skips them, which
+	// leaves the store-only listing.
+	client *git.Client
 }
 
 func (ir Issue) getIssueListCmd() *cobra.Command {
@@ -64,10 +70,16 @@
 		}
 	}
 
+	client, err := cmdutil.NewClientForCmd(cmd, ir.appConfig)
+	if err != nil {
+		return fmt.Errorf("open repository: %w", err)
+	}
+
 	infra := issueListInfra{
 		tracker: t,
 		store:   s,
 		stderr:  cmd.OutOrStderr(),
+		client:  client,
 	}
 
 	return runList(ctx, os.Stdout, infra, flags)
@@ -135,7 +147,61 @@
 		fmt.Fprintf(infra.stderr, "warning: tracker unavailable, falling back to local store: %v\n", err)
 	}
 
-	return buildFromStore(ctx, infra.store, status)
+	rows, err := buildFromStore(ctx, infra.store, status)
+	if err != nil || infra.client == nil {
+		return rows, err
+	}
+
+	return mergeRepoIssues(ctx, infra, rows, status)
+}
+
+// mergeRepoIssues enriches the store rows with the issues stored in the
+// repository: a row whose issue has a record gets its labels and state, and
+// every record without a branch row is appended, so the backlog shows up
+// before anyone starts a branch. status filters the appended rows on the
+// issue state ("open" / "closed"; anything else keeps all).
+func mergeRepoIssues(
+	ctx context.Context, infra issueListInfra, rows []store.IssueRow, status string,
+) ([]store.IssueRow, error) {
+	fetchIssues(ctx, infra.client)
+
+	records, warnings, err := issuepkg.List(ctx, infra.client)
+	if err != nil {
+		return nil, fmt.Errorf("list repo issues: %w", err)
+	}
+	printWarnings(infra.stderr, warnings)
+
+	byDisplayID := make(map[string]*issuepkg.Record, len(records))
+	for i := range records {
+		byDisplayID[records[i].DisplayID()] = &records[i]
+	}
+
+	out := rows
+	started := make(map[string]bool, len(out))
+	for i := range out {
+		rec, ok := byDisplayID[out[i].IssueSlug]
+		if !ok {
+			continue
+		}
+		started[rec.ID] = true
+		out[i].Labels, out[i].State, out[i].TrackerStatus = rec.Labels, rec.State, &rec.State
+	}
+
+	for i := range records {
+		rec := &records[i]
+		if started[rec.ID] {
+			continue
+		}
+		if (status == issuepkg.StateOpen || status == issuepkg.StateClosed) && rec.State != status {
+			continue
+		}
+		out = append(out, store.IssueRow{
+			IssueSlug: rec.DisplayID(), Title: rec.Title,
+			Labels: rec.Labels, State: rec.State, TrackerStatus: &rec.State,
+		})
+	}
+
+	return out, nil
 }
 
 func buildFromTracker(ctx context.Context, infra issueListInfra) ([]store.IssueRow, error) {
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `mise exec -- go test ./cmd/issue/... ./tui/... ./tty/... ./store/... -v -run "^(TestBuildRows|TestRunIssueList|TestBuildIssueRows|TestMatchesStatus_RepoIssueState|TestIssueRowToTableRow_Labels|TestApplyFilters|TestRenderIssueTable)"`
Expected: PASS. The pre-existing `TestRunIssueList` case that expects `N.A.` still passes: it builds `issueListInfra` without a client, so no repo issue is merged.

Run: `mise exec -- go test ./...`
Expected: PASS for every package.

- [ ] **Step 8: Commit**

Run `detect_changes()` first; expected changed symbols: `IssueRow`, `issueListInfra`, `issueListRunE`, `buildRows`, `matchesStatus`, `issueRowToTableRow`, `applyFilters`, `buildIssueTableColumns`, `RenderIssueTable`, plus the new ones.

```bash
git add store/store.go store/helpers.go tty/issue.go tui/issue.go tui/issue_test.go cmd/issue/list.go cmd/issue/list_record_test.go
git commit -m "feat(issue): list the issues stored in the repository with their state and labels"
```

---

### Task 10: Documentation

**Files:**
- Create: `docs/issue-refs.md`
- Modify: `README.md`, `CLAUDE.md`

**Interfaces:**
- Consumes: the behavior delivered by Tasks 1 to 9.
- Produces: nothing code depends on.

- [ ] **Step 1: Write the op format reference**

Create `docs/issue-refs.md`:

````markdown
# Issue refs

git-zf stores each issue in the repository as a chain of commits under
`refs/zf/issues/<id>`. This page describes the format, for people reading it
with plain git:

```
git log refs/zf/issues/<id>
git show <commit>:op.json
```

## Layout

| Ref | Content |
|---|---|
| `refs/zf/issues/<id>` | The local issue. `<id>` is the full object ID of the issue's first commit. |
| `refs/zf/remote/issues/<id>` | What the remote had at the last fetch or push. Never edited by hand. |

The 7-character ID shown by `git zf issue list` and used in branch names is the
start of `<id>`. Commands accept the full ID or any unique prefix of at least 4
characters.

## Ops

Every change is one commit. Its tree holds one file, `op.json`; its parent is
the previous change. Author and committer are the user's git identity, and the
commit is signed when `commit.gpgsign` is true.

```json
{"v":1,"type":"add_comment","at":"2026-10-03T10:00:00Z","author":"Pi <pi@example.org>","body":"Reproduced on main."}
```

| `type` | Fields | Effect |
|---|---|---|
| `create` | `title`, `description`, `branch_type` | First commit of the chain. |
| `set_state` | `value`: `open` or `closed` | Opens or closes the issue. |
| `add_label` | `value` | Adds a label. |
| `remove_label` | `value` | Removes a label; nothing happens if it is absent. |
| `add_comment` | `body` | Adds a comment. |
| `merge` | none | Two-parent commit joining changes made on two clones. |

`v` is the format version, currently 1. `at` is RFC 3339, UTC.

## Reading an issue

The current state is the fold of all ops: a commit is applied after its
parents; commits with no order between them (made on two clones before either
synced) are applied by `at`, then by commit ID. The last `set_state` wins,
labels form a set, comments accumulate. An op of an unknown type or version is
skipped, so an older git-zf reads refs written by a newer one.

## Sharing

`git zf issue sync` fetches `refs/zf/issues/*` into `refs/zf/remote/issues/*`,
then for each issue:

- no local ref: the local ref is created;
- local behind: it is fast-forwarded;
- local ahead: nothing;
- both changed: a `merge` commit with both tips as parents is written.

Pushes are plain fast-forward pushes, never forced, so a push cannot discard
someone else's ops. Every command that writes an op pushes it right away; when
the push fails the op stays local and goes out with the next sync.

A plain `git clone` or `git fetch` does not bring these refs. Run
`git zf issue sync` (or any `git zf issue` command, which fetches them) once
after cloning.
````

- [ ] **Step 2: Update the README**

In `README.md`, in the `### Issue` section, replace the command block:

````markdown
```
$ git zf issue start
$ git zf issue list
$ git zf issue close
```
````

with:

````markdown
```
$ git zf issue start
$ git zf issue list
$ git zf issue close
$ git zf issue new              # create an issue in the repository, no branch
$ git zf issue show [<id>]      # show an issue and its comments
$ git zf issue comment [<id>]   # comment on an issue
$ git zf issue label [<id> +add -remove …]
$ git zf issue sync             # fetch, merge and push the repository issues
```
````

In the same section, in the `**issue start**` paragraph, replace the sentence
"fetch your open issues from the configured tracker (Redmine, GitHub, Forgejo/Gitea), or enter ID, title and type by hand."
with:
"fetch your open issues from the configured tracker (Redmine, GitHub, Forgejo/Gitea), or take the manual path: pick an open issue stored in the repository, or fill the form. Leaving the form's Issue ID empty creates a new issue in the repository; typing one (for a tracker git-zf does not talk to) uses it as is."

In the `**issue list**` paragraph, replace
"The tracker is the primary source when configured; the local store is the fallback. Columns: Issue ID · [Project] · Title · Branch · Local Status · Tracker Status · Created. `∅` means no local branch yet; `N.A.` means no tracker configured."
with:
"The tracker is the primary source when configured. Otherwise the list is the issues stored in the repository plus the branches of the local store. Columns: Issue ID · [Project] · Title · Branch · Local Status · Issue Status · Created. Labels follow the title in brackets. `∅` means no local branch yet; `N.A.` means the row has neither a tracker nor a repository issue."

In the `**issue close**` numbered list, replace item 4
"4. **Confirm.** The branch is marked `merged` and the issue `closed` in the local store."
with:
"4. **Confirm.** The branch is marked `merged` and the issue `closed` in the local store. An issue stored in the repository is closed there too, and pushed."

Then insert this subsection immediately before `#### Sub-tasks`:

````markdown
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
$ git zf issue show 1a2b3c4
$ git zf issue start            # pick it, the branch is 1a2b3c4@fix@login-fails-on-safari
```

- **`issue new`** opens a form (title, type, description, labels). Any flag
  (`--title`, `--type`, `--description`, `--label`, repeatable) skips it.
- **`issue show`**, **`issue comment`** and **`issue label`** take the issue ID
  shown by `issue list`: the 7-character ID, the full one, or any unique prefix
  of at least 4 characters. Without an ID they open a picker. `show --json`
  prints the record; `comment -m` skips the form.
- **`issue sync`** fetches the issues from the remote, merges the ones changed
  on both sides and pushes yours. Each command above also pushes its own
  change, so `sync` is mostly for bringing in other people's.

Two people can comment on or relabel the same issue offline: both changes are
kept when they sync. Nothing is ever force-pushed. The storage format is
described in [docs/issue-refs.md](docs/issue-refs.md).

Syncing these issues with Redmine, GitHub or Forgejo is not available yet.
````

In the `### Menu` paragraph, no change: the `issue` menu lists the new actions on its own.

- [ ] **Step 3: Update CLAUDE.md**

In `CLAUDE.md`, insert this section immediately before `### Testing the menus`:

````markdown
### Testing the repo issues

Issues stored in the repository (`refs/zf/issues/*`) are tested at three
levels, all on real on-disk repos:

- `issue/record_test.go` — the fold (pure, no git).
- `git/issue_ref_test.go`, `git/issue_ref_sync_test.go` — plumbing: write and
  read a chain, fetch into `refs/zf/remote/issues/*`, reconcile, push.
- `issue/repo_test.go` — the glue (`Create`, `Append`, `Load`, `Resolve`,
  `Sync`), including two clones diverging and merging.
- `cmd/issue/record_e2e_test.go`, `record_ops_e2e_test.go` — the `new`, `show`,
  `comment`, `label` and `sync` commands, driven by a `scriptedRecordPrompter`
  on a `recordRig` (`newRecordRig(t, user, origin)`; pass `newBareOrigin(t)` to
  two rigs to get two clones of one remote).

    mise exec -- go test ./issue/... ./git/... -run "TestFold|TestIssueRef_|TestPushFetchSync" -v
    mise exec -- go test ./cmd/issue/... -run "^TestRun(New|Show|Comment|Label|Sync)" -v

The start, close and list integrations live next to their flows:
`start_record_e2e_test.go`, `close_record_e2e_test.go`, `list_record_test.go`.

When adding an op type, add its constant and its `Fold` case in
`issue/record.go` with a table case in `TestFold`. Unknown types must keep
being skipped: an older binary reads refs written by a newer one.

````

- [ ] **Step 4: Check the docs against the binary**

```bash
make
./bin/git-zf issue --help
./bin/git-zf issue new --help
./bin/git-zf issue label --help
```

Expected: `issue --help` lists `close`, `comment`, `label`, `list`, `new`, `show`, `start`, `sync`, `track`; the flags named in the README exist.

Then a manual end-to-end pass in a scratch repository (not this one), which is the only check of the huh forms:

```bash
cd "$(mktemp -d)" && git init -q -b main && git commit -q --allow-empty -m init
/home/pi/code/pi/git-zf/bin/git-zf issue new          # fill the form
/home/pi/code/pi/git-zf/bin/git-zf issue list --stdout
/home/pi/code/pi/git-zf/bin/git-zf issue start        # pick the issue, create the branch
/home/pi/code/pi/git-zf/bin/git-zf issue show         # picker, then the record
git log --format='%h %s' refs/zf/issues/*
```

Expected: the issue appears in the list with state `open`, the branch is named `<short-id>@<type>@<slug>`, and `git log` shows one `create` commit.

- [ ] **Step 5: Final verification**

Run: `mise exec -- go test ./...`
Expected: PASS for every package.

Run: `golangci-lint run ./...`
Expected: no finding in the files this plan created, and no new finding in the files it modified (the repository has a pre-existing baseline; compare with `git stash; golangci-lint run ./... ; git stash pop` if in doubt).

- [ ] **Step 6: Commit**

Run `detect_changes()` first; expected: documentation files only.

```bash
git add docs/issue-refs.md README.md CLAUDE.md
git commit -m "docs: describe the issues stored in the repository"
```
