# Design: Deterministic branch naming with opt-in variants

**Date:** 2026-05-24
**Status:** approved

## Overview

Today, every branch created by `git zf issue start` (or `branch new`) gets a 4-part name `<issueID>@<type>@<slug>@<8-hex-random>`. The random suffix only earns its keep when an operator wants more than one branch per issue (spike → real, parallel exploration, multi-dev on one ticket). In practice the rest of the system assumes 1:1: pickers, tracker hooks, and the close flow all behave as if one branch wins per issue. So the cost of N:1 is paid (ugly names, merge-subject noise, branches that are awkward to type or autocomplete, and a class of "called `branch.New` twice and got two different IDs" bugs) without realising its value.

This spec drops the random suffix as the default. New branches use the deterministic 3-part name `<issueID>@<type>@<slug>`. Operators who genuinely need a second branch on the same issue opt in with `--variant=<label>`. Collisions on the deterministic name surface as an interactive prompt (checkout existing, create a variant, or abort).

Existing repos keep working: the parser permanently accepts both 3-part and 4-part names, so legacy random-suffix branches stay parseable. The store schema, however, is reset — see "Store schema migration" below.

## Branch type and parser

The `branch` package becomes the source of truth for the name shape. No more `crypto/rand`.

```go
// New produces a Branch with optional variant.
//
//   variant == ""  → 3-part name: <issueID>@<type>@<slug>
//   variant != ""  → 4-part name: <issueID>@<type>@<slug>@<slugged-variant>
//
// Non-empty variant is run through Slug() (which also enforces MaxSlugLen);
// an empty result after slugging returns an error.
func New(issueID, branchType, title, variant string) (*Branch, error)

// Parse accepts either 3 or 4 parts. For 4-part input, the trailing segment
// is preserved verbatim and exposed via Variant() — it may be an operator
// label ("spike", "approach-b") OR a legacy random-hex suffix ("b39970db").
// Parse treats both the same way; the system no longer distinguishes them.
func Parse(name string) (*Branch, error)

func (b Branch) Variant() string  // "" for 3-part names
```

`Branch.id` (and the matching `ID()` accessor) is renamed to `variant`. `Branch.Name()` skips the trailing `@variant` segment when `variant == ""`. `shortUUID()` is deleted.

## Slug length cap

`Slug()` gains a hard length cap:

```go
const MaxSlugLen = 50  // characters, applied AFTER slugging
```

`Slug()` truncates to `MaxSlugLen` and strips any trailing `-` left over from a mid-word cut. So `"Add OAuth login flow with refresh-token rotation and SSO"` becomes `"add-oauth-login-flow-with-refresh-token-rotation"` (48 chars, clean break at a hyphen) rather than the same string with a dangling `-`.

Rationale for 50: keeps the full branch name (`ISSUE-1234@feat@<≤50>@<≤32-variant>`) under ~100 chars in the worst case — comfortably inside the readable-in-terminal range and well under git's 250-byte ref limit. Hard-coded; no config knob.

## Store schema migration

Migration `0004_branches_name_pk.sql`:

```sql
DROP TABLE branches;  -- cascade-drops the enforce_merged_at trigger.

CREATE TABLE branches (
    name       TEXT PRIMARY KEY,
    issue_id   INTEGER NOT NULL REFERENCES issues(id),
    type       TEXT NOT NULL,
    status_id  INTEGER NOT NULL DEFAULT 1 REFERENCES statuses(id),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    merged_at  DATETIME
);

CREATE TRIGGER enforce_merged_at
BEFORE UPDATE OF status_id ON branches
WHEN NEW.status_id = 2
BEGIN
    SELECT CASE WHEN NEW.merged_at IS NULL
        THEN RAISE(ABORT, 'merged_at must not be null when status is merged')
    END;
END;
```

This is a forward-only migration with no data preservation. Existing in-progress branch rows are wiped. Operators who care can re-`issue start` the affected branches: the parser still accepts the 4-part legacy names, so re-running with the same input recreates an equivalent row keyed on the same name. Local git refs are untouched. The `issues` table is untouched (it has value independent of branches: tracker linkage, history).

Go-side changes in `store/store.go`:

```go
// Branch loses UUID; name is the identity.
type Branch struct {
    Name      string
    IssueID   int64
    Type      string
    StatusID  int64
    CreatedAt time.Time
    MergedAt  *time.Time
}

// BranchRow loses UUID. All read methods (ListBranches,
// ListBranchesByIssueSlugs) drop the b.uuid SELECT column.
type BranchRow struct {
    IssueID    int64
    IssueSlug  string
    Title      string
    BranchName string
    Type       string
    Status     BranchStatus
    CreatedAt  time.Time
}

func (s *Store) UpdateBranchStatus(ctx context.Context, name string, statusID int64, mergedAt *time.Time) error
func (s *Store) DeleteBranch(ctx context.Context, name string) error
```

Caller updates: `cmd/branch/branch.go` (prune loop) and `cmd/issue/close.go` switch from `row.UUID` to `row.BranchName`. `cmd/issue/start.go` drops the `UUID:` field from the `store.Branch` literal.

## CLI flag and flow integration

`--variant` is added to `issue start` and threaded through the shared `RunIssueStart` so both `issue start` (tracker-first) and `branch new` (manual-first) accept it.

```go
// issue/issue.go
type IssueStartFlags struct {
    TrackerFirst bool
    Variant      string  // operator-supplied variant label; "" = no variant
}
```

`cmd/issue/start.go`:

```go
func (i Issue) getStartCmd() *cobra.Command {
    cmd := &cobra.Command{ /* … */ RunE: i.startRunE }
    cmd.Flags().String("variant", "",
        "create a parallel branch for the same issue (e.g. --variant=spike)")
    return cmd
}

func (i Issue) startRunE(cmd *cobra.Command, _ []string) error {
    variant, _ := cmd.Flags().GetString("variant")
    return i.RunIssueStart(cmd, issue.IssueStartFlags{
        TrackerFirst: true,
        Variant:      variant,
    })
}
```

The same flag is added to `cmd/branch/branch.go`'s `branch new` subcommand.

`prepareBranch` gains the flags argument and forwards the variant:

```go
func (i Issue) prepareBranch(
    pickedIssue *issue.Issue,
    client *git.Client,
    flags issue.IssueStartFlags,
) (b *branch.Branch, base string, err error) {
    b, err = branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject, flags.Variant)
    if err != nil {
        return nil, "", fmt.Errorf("assemble branch name: %w", err)
    }
    // base resolution unchanged
}
```

`--variant=""` is treated as omitted. An invalid variant (empty after slug) returns an error from `branch.New` and aborts cleanly before any git or store mutation.

## Conflict handling

The collision check happens after `branch.New(…)` succeeds but before any TUI confirm or git mutation. The git layer gains a read-side method:

```go
// git/git.go
// BranchExists returns true if refs/heads/<name> resolves locally.
func (c *Client) BranchExists(name string) (bool, error)
```

Local-ref only. Checking remote refs would force a fetch on every `issue start`, slowing the happy path; remote-only collisions surface at push time, as today.

A new file `cmd/issue/conflict.go`:

```go
// Returns (nil, nil) when the caller should exit (existing branch was checked
// out, or the operator aborted). Returns (newBranch, nil) when the caller
// should proceed with newBranch — either the original (no conflict) or a
// variant rebuilt from operator input.
func resolveBranchConflict(
    ctx context.Context,
    client *git.Client,
    b *branch.Branch,
    pickedIssue *issue.Issue,
) (*branch.Branch, error)
```

Flow:

1. `client.BranchExists(b.Name())` — if false, return `b` unchanged.
2. Show `tui.BranchConflictPicker` (new) with three options: `Checkout existing`, `Create a variant`, `Abort`.
3. **Checkout existing:** `client.Checkout(ctx, b.Name())`; print `Switched to existing branch "<name>"`; return `(nil, nil)`.
4. **Create a variant:** show `tui.VariantLabelInput` (new — single `huh.Input` with inline validation matching `branch.New`: non-empty after slugging); rebuild via `branch.New(issueID, type, title, label)`; loop back to step 1 (the operator's chosen variant could itself collide).
5. **Abort:** print `Aborted.`; return `(nil, nil)`. (Matches the existing convention across the seven other abort sites in the codebase: print and return nil. Changing that to a non-zero exit is a separate cross-cutting spec.)

In `cmd/issue/start.go`, between `prepareBranch` and the existing `IssueConfirm`:

```go
b, base, err := i.prepareBranch(pickedIssue, client, flags)
if err != nil {
    return err
}

b, err = resolveBranchConflict(cmd.Context(), client, b, pickedIssue)
if err != nil {
    return err
}
if b == nil {  // operator chose checkout-existing or abort
    return nil
}

branchName := b.Name()
// existing confirm + CreateBranch flow unchanged
```

`--variant` does **not** skip the resolver. If an operator's explicit variant also collides (e.g. running `--variant=spike` twice), the resolver's loop is exactly what they want.

`createWorktree` runs the same check before computing the path and prompting for confirmation. Note that this catches branch-ref collisions; worktree-path collisions are a separate concern git already errors on.

`persist` is unchanged. If the resolver returns a `*branch.Branch` with a variant, `persist` stores a fresh row keyed on the now-unique name.

## Documentation updates

Implementation **must not** ship without working through this checklist. Each item is load-bearing for user understanding of the new shape.

### User-facing docs

- [ ] **`README.md` § "Branch naming" (line ~367).** Replace the `{issue-id}@{type}@{slugified-title}@{short-uuid}` format string and the `ABC-42@feat@add-oauth-login@550e8400` example with the 3-part default. Note that the slug is capped at 50 characters.
- [ ] **`README.md` — new "Parallel branches per issue" subsection** (under or adjacent to "Branch naming"). Explain `--variant=<label>`, the conflict-prompt behaviour (checkout / variant / abort), and that legacy 4-part branches keep working unchanged.
- [ ] **`README.md` § `issue start` and `branch new` command summaries.** Mention the new `--variant` flag in the command paragraphs (lines ~79 and the corresponding `branch new` line).
- [ ] **`ROADMAP.md`.** Remove any line that treats the random suffix as a designed feature. If the file does not currently mention it, no edit needed — confirm explicitly during implementation.

### CLI help text

- [ ] **`--variant` flag** on `issue start` and `branch new`:

  ```
  --variant string   create a parallel branch for the same issue (e.g. --variant=spike).
                     Letters, digits, and hyphens; lowercased and slugged.
                     Default branches are 1:1 with their issue; use this only when
                     you need a second branch on the same issue.
  ```

### Go docstrings

- [ ] `branch.New` — full doc comment per the "Branch type and parser" section (3-part vs 4-part rules, slug enforcement, error conditions).
- [ ] `branch.Parse` — note acceptance of both 3- and 4-part inputs and the no-distinction policy on the trailing segment.
- [ ] `branch.Variant` — explain the empty-string case for 3-part names.
- [ ] `branch.Slug` — document `MaxSlugLen` truncation and trailing-hyphen stripping.
- [ ] `branch.MaxSlugLen` — one-liner explaining what the cap protects against.
- [ ] `git.Client.BranchExists` — note local-ref-only semantics.
- [ ] `store.UpdateBranchStatus`, `store.DeleteBranch` — update parameter names (`uuid` → `name`) in the doc comment.
- [ ] `cmd/issue.resolveBranchConflict` — flow summary and the `(nil, nil)` return contract.

### Config docs

No config docs change — no new config keys.

### Spec / plan cross-references

- [ ] When the implementation plan is written, link it back to this spec; when the spec is amended after plan kickoff, mirror the amendment summary in the plan.

## Test coverage

| Package | New or amended tests |
|---|---|
| `branch` | `TestNew` covers 3-part vs 4-part output; `TestNew_variant` covers slug normalisation and empty-after-slug rejection. `TestSlug` adds `MaxSlugLen` cases: exact cap, cap+1 cut at a hyphen, cap+1 cut mid-word with trailing hyphen stripped, unicode. `TestParse` covers both 3- and 4-part inputs and the `Variant()` accessor. `TestParse_invalid` keeps the existing negative cases minus the "three parts" entry (now valid). |
| `store` | New `TestMigration_0004` opens a v3 DB with seeded rows, applies migrations, asserts `branches` is empty and the trigger is reinstated. `TestUpdateBranchStatus` and `TestDeleteBranch` switched to name-keyed. `TestListBranches` no longer scans `uuid`. |
| `cmd/issue` | New `cmd/issue/conflict_test.go` exercises `resolveBranchConflict` against a fake `git.Client` for the three branches (checkout, variant, abort) and the loop-on-collision case. |
| `git` | `TestBranchExists` against a temp repo: returns false for unknown ref, true after creating a branch. |

## Out of scope

- Changing the exit code on user abort — the seven existing "Aborted." sites all return nil today; a unified non-zero-on-abort policy is a separate cross-cutting spec.
- Remote-ref collision detection. No fetch on the `issue start` happy path; remote-only collisions surface at push time.
- A `branch migrate-names` helper or any one-shot rename of legacy 4-part branches. Grandfather-forever is the chosen migration story.
- Touching the `commit` flow's `branch.Parse` call site beyond the free gain of accepting 3-part names; no other behaviour change there.
