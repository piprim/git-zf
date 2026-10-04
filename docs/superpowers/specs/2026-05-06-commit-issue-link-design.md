# Commit–Issue Link Design

> **For agentic workers:** implement this spec via `superpowers:writing-plans` → `superpowers:subagent-driven-development`.

**Goal:** When `git zf commit` is run on an issue branch (`issueID@type@slug@uuid`), automatically pre-populate one commit message field with the issue ID, and optionally pre-select the commit type to match the branch type. Zero config: it just works when you're on an issue branch; silently skipped otherwise.

---

## Approach

`IssueHint` struct in the `commit` package carries the detected issue context. `cmd/commit` detects the hint (thin layer), passes it to `commit.FillOutForm`, which applies it inside `loadForm` before building the form.

---

## Architecture

```
git.Client.CurrentBranch()          ← new method, reads HEAD
        ↓
cmd/commit/commit.go  runE()        ← issueHintFromClient → IssueHint
        ↓
commit.FillOutForm(cfg, defaults, hint IssueHint)
        ↓
commit.loadForm()                   ← applyIssueHint + isValidCommitType
        ↓
tui.CommitMessageGroup()            ← unchanged, reads Value from items as before
```

### Files changed

| File | Change |
|------|--------|
| `git/git.go` | Add `CurrentBranch() (string, error)` |
| `commit/form.go` | Add `IssueHint`; `FillOutForm` + `loadForm` gain hint param; add `applyIssueHint`, `setItemValue`, `isValidCommitType` |
| `commit/form_test.go` | Add tests for `setItemValue`, `applyIssueHint`, `isValidCommitType` |
| `cmd/commit/commit.go` | Add `issueHintFromClient`; pass hint to `FillOutForm` |
| `git/git_test.go` | Add `TestCurrentBranch_*` |
| `cmd/commit/commit_test.go` | Add `TestIssueHintFromClient_*` |

---

## Components

### `git/git.go` — `CurrentBranch()`

```go
func (c *Client) CurrentBranch() (string, error) {
    head, err := c.repo.Head()
    if err != nil {
        return "", fmt.Errorf("read HEAD: %w", err)
    }

    return head.Name().Short(), nil
}
```

Returns the short branch name (e.g. `ABC-1@feat@my-feature@a1b2c3d4`). If HEAD is detached, returns the hash — `branch.Parse` will then fail and the hint will be empty.

---

### `commit/form.go` — `IssueHint` and helpers

```go
// IssueHint carries issue context detected from the current branch.
// Zero value means no issue branch — form fields are left unchanged.
type IssueHint struct {
    IssueID    string
    BranchType string
}
```

#### Fallback chain

Pre-population applies to the **first** field found in priority order:

1. `scope` → pre-filled with `hint.IssueID` (e.g. `ABC-1`)
2. `footer` → pre-filled with `Refs: <IssueID>`
3. `subject` → pre-filled with `(<IssueID>)`

If none of the three fields exist in the config, the items are returned unchanged.

```go
// setItemValue finds the first item with the given name and sets its Value.
// Reports whether a match was found.
func setItemValue(items []config.CommitItem, name string, value string) bool {
    for i := range items {
        if items[i].Name == name {
            items[i].Value = value

            return true
        }
    }

    return false
}

// applyIssueHint pre-populates one field using the fallback chain:
// scope → footer (Refs: ID) → subject ((ID)).
// Returns a clone of items; never mutates the original slice.
func applyIssueHint(items []config.CommitItem, hint IssueHint) []config.CommitItem {
    if hint.IssueID == "" {
        return items
    }

    out := slices.Clone(items)

    if setItemValue(out, "scope", hint.IssueID) {
        return out
    }
    if setItemValue(out, "footer", "Refs: "+hint.IssueID) {
        return out
    }
    if setItemValue(out, "subject", "("+hint.IssueID+")") {
        return out
    }

    return out
}

// isValidCommitType reports whether name matches a configured commit type.
func isValidCommitType(types []config.CommitTypeOption, name string) bool {
    return slices.ContainsFunc(types, func(t config.CommitTypeOption) bool {
        return t.Name == name
    })
}
```

#### `FillOutForm` signature change

```go
func FillOutForm(cfg *config.AppConfig, defaults tui.CommitOption, hint IssueHint) ([]byte, tui.CommitOption, error)
```

Inside `loadForm`, before building the form groups:

```go
items := applyIssueHint(cfg.CommitMessage.Items, hint)

var selectedType string
if isValidCommitType(cfg.CommitTypes, hint.BranchType) {
    selectedType = hint.BranchType
}
// pass items and selectedType to CommitMessageGroup
```

---

### `cmd/commit/commit.go` — `issueHintFromClient`

```go
func issueHintFromClient(c *git.Client) commitpkg.IssueHint {
    name, err := c.CurrentBranch()
    if err != nil {
        return commitpkg.IssueHint{}
    }

    b, err := branch.Parse(name)
    if err != nil {
        return commitpkg.IssueHint{}
    }

    return commitpkg.IssueHint{IssueID: b.IssueID(), BranchType: b.Type()}
}
```

`runE` change:

```go
hint := issueHintFromClient(client)
msg, opts, err := commitpkg.FillOutForm(c.appConfig, defaults, hint)
```

---

## Error handling

| Situation | Behaviour |
|-----------|-----------|
| `CurrentBranch` fails (detached HEAD, no repo) | `issueHintFromClient` returns zero `IssueHint` — form runs unchanged |
| `branch.Parse` fails (not an issue branch) | same — silent, zero config |
| `hint.IssueID` non-empty but no matching field in config | `applyIssueHint` returns the clone unchanged |
| `hint.BranchType` not in configured commit types | `isValidCommitType` returns false — type selector left at its default |

All failures degrade gracefully to "no pre-population". No errors are propagated from the hint detection path.

---

## Testing

### `setItemValue` — unit, `commit/form_test.go`

- item found → value set, returns true
- item not found → slice unchanged, returns false

### `applyIssueHint` — table-driven, `commit/form_test.go`

| items contain | IssueID | expected pre-filled field |
|---------------|---------|--------------------------|
| scope, footer, subject | `ABC-1` | scope = `ABC-1` |
| footer, subject (no scope) | `ABC-1` | footer = `Refs: ABC-1` |
| subject only | `ABC-1` | subject = `(ABC-1)` |
| none of the three | `ABC-1` | no change |
| scope, footer | `""` | no change (early return) |

Original items slice must be unchanged in all cases (clone check).

### `isValidCommitType` — unit, `commit/form_test.go`

- name matches a configured type → true
- name not in list → false
- empty slice → false

### `CurrentBranch` — `git/git_test.go`

Uses `git.NewClientAt(t.TempDir())` pattern. Init a repo, create a named branch, call `CurrentBranch`, verify name. Also test detached HEAD → error.

### `issueHintFromClient` — `cmd/commit/commit_test.go`

Uses `git.NewClientAt(t.TempDir())` pattern. Init a repo, check out an issue branch.

- branch named `ABC-1@feat@my-feat@a1b2c3d4` → `IssueHint{IssueID: "ABC-1", BranchType: "feat"}`
- branch named `main` (no `@`) → zero `IssueHint`
