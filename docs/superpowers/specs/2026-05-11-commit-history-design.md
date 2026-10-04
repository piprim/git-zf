# Commit History — Design Spec

**Date:** 2026-05-11
**Command:** `git zf commit`
**Scope:** per-repo browsable history of commit form submissions

---

## Problem

When `git zf commit` is run and a pre-commit hook fails, the user must re-open the form and retype every field from scratch. More broadly, reusing a recent commit message pattern requires manual re-entry.

## Goal

Allow the user to pick a past commit form submission from within the running form, pre-filling all fields with the selected entry's values.

---

## UX Flow

1. `git zf commit` → main form opens blank (or issue-hinted, unchanged from today)
2. User presses `ctrl+r` → form exits with a "wants history" signal (distinct from a plain abort)
3. A short history-picker form appears: a single select listing the last 50 entries newest-first, with "New blank commit" pinned at position 0
4. User selects an entry → main form re-opens with all fields pre-filled from that entry
5. User selects "New blank commit" → main form re-opens blank
6. On either path the user can complete or abort the main form normally

A `ctrl+r: pick from history` hint is shown in the main form's help footer.

---

## Store — `command_history` table

New migration `0003_command_history.sql` added to `store/migrations/`:

```sql
CREATE TABLE command_history (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    command    TEXT     NOT NULL,
    payload    TEXT     NOT NULL CHECK (json_valid(payload)),
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX command_history_command_created
    ON command_history (command, created_at DESC);
```

- `command` — name of the git-zf subcommand (e.g. `"commit"`). Future commands (`branch`, `issue start`) use the same table at no extra cost.
- `payload` — JSON object mapping form field names to their submitted values (e.g. `{"type":"feat","scope":"auth","subject":"add OAuth",...}`). The `CHECK` constraint catches serialisation bugs at the DB layer.
- No status field: every completed form submission is recorded unconditionally. Failed hook attempts are captured because saving happens after form completion and before `client.Commit()`.

### New store methods

```go
// InsertCommandHistory records one form submission. payload must be JSON-serialisable.
// Fire-and-forget — no update needed later.
InsertCommandHistory(ctx context.Context, command string, payload any) error

// ListCommandHistory returns the most recent limit entries for command, newest-first.
ListCommandHistory(ctx context.Context, command string, limit int) ([]CommandHistoryRow, error)
```

```go
type CommandHistoryRow struct {
    ID        int64
    Payload   json.RawMessage // raw JSON; caller unmarshals into the shape they need
    CreatedAt time.Time
}
```

Limit: 50 entries fetched. No automatic pruning; a future `git zf history clear` can handle it if needed.

---

## `ctrl+r` Detection — bubbletea wrapper

`form.Run()` does not distinguish which key caused an abort. A thin `formRunner` type (unexported — callers use the exported `RunForm` function) wraps `*huh.Form` as a `tea.Model` and runs it inside an explicit bubbletea program so we can intercept keys before huh sees them.

**Location: `tui/runner.go`** — the runner has no commit-specific logic; it wraps any `*huh.Form`. Placing it in the `tui` package alongside `tui/commit.go` and `tui/branch.go` means future commands (`branch`, `issue start`) reuse `RunForm` at no extra cost.

`ctrl+r` is a user intent, not a failure — it must not pollute the error channel. `RunForm` returns `(*FormRunner, error)`: the error covers genuine failures (bubbletea crash, `huh.ErrUserAborted`), while `WantHistory()` on the result exposes the history intent as a plain boolean.

```go
// tui/runner.go

// FormRunner is the result of RunForm. It implements tea.Model internally
// and exposes WantHistory to let callers check whether the user pressed ctrl+r.
type FormRunner struct {
    form        *huh.Form
    wantHistory bool
}

// WantHistory reports whether the user pressed ctrl+r during the form.
func (r *FormRunner) WantHistory() bool { return r.wantHistory }

func (r *FormRunner) Init() tea.Cmd { return r.form.Init() }

func (r *FormRunner) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    if key, ok := msg.(tea.KeyMsg); ok && key.String() == "ctrl+r" {
        r.wantHistory = true
        return r, tea.Quit
    }
    updated, cmd := r.form.Update(msg)
    if f, ok := updated.(*huh.Form); ok {
        r.form = f
    }

    return r, cmd
}

func (r *FormRunner) View() string { return r.form.View() }

// RunForm runs form inside a bubbletea program.
// Returns (runner, nil) on normal completion or ctrl+r — call runner.WantHistory() to distinguish.
// Returns (runner, huh.ErrUserAborted) on ctrl+c / esc.
func RunForm(form *huh.Form) (*FormRunner, error) {
    r := &FormRunner{form: form}
    if _, err := tea.NewProgram(r).Run(); err != nil {
        return r, fmt.Errorf("run form: %w", err)
    }

    return r, r.form.Err() // Err() is nil on completion, huh.ErrUserAborted on abort
}
```

`ctrl+r` is consumed by the wrapper and never reaches huh (no conflict with huh's own key map). All other messages — navigation, text input, window resize — are forwarded unchanged.

Because `Update` returns the same pointer (`r`) each time, bubbletea's runtime always holds the same `*FormRunner`. Mutations to `r.wantHistory` are therefore visible through the original pointer after `p.Run()` returns.

`FillOutForm` calls `tui.RunForm(form)` instead of `form.Run()` and branches on the result:

| Exit condition | `err` | `runner.WantHistory()` | action |
|---|---|---|---|
| Form completed | `nil` | `false` | save history, assemble message |
| `ctrl+r` pressed | `nil` | `true` | run history picker |
| `ctrl+c` / `esc` | `huh.ErrUserAborted` | `false` | propagate to caller |

---

## `FillOutForm` Orchestration

Location: `commit/form.go`. The function signature is unchanged; all new behaviour is internal.

```
FillOutForm(cfg, defaults, hint, store)
│
├─ run main form via formRunner
│   ├─ completed → save payload to history → assemble message → return (msg, opts, nil)
│   ├─ ctrl+r    → history empty? → print "No commit history yet." → run main form (blank)
│   │             history exists? → run history picker form
│   │               ├─ entry selected    → apply values to items → run main form (pre-filled)
│   │               └─ "new blank commit" → run main form (blank)
│   └─ aborted   → return ("", zero, huh.ErrUserAborted)
│
└─ (repeat from history picker branch as needed)
```

**History picker form** — a single `huh.NewForm` containing one `huh.NewSelect`. Option labels:

```
New blank commit
feat(auth): add OAuth flow           [2026-05-10 14:32]
fix(api): handle nil pointer          [2026-05-09 09:11]
…
```

Label format: first line of the assembled message truncated to 60 chars, followed by `[YYYY-MM-DD HH:MM]`. Entries are built by re-assembling the payload through the configured template so the label matches exactly what would have been committed.

**Saving** occurs after the form completes and before `client.Commit()` is called. The payload is the `map[string]any` returned by `extractMsg()` — same data used to assemble the commit message.

`store` is injected into `FillOutForm` as a minimal interface so tests can supply a fake:

```go
type historyStore interface {
    InsertCommandHistory(ctx context.Context, command string, payload any) error
    ListCommandHistory(ctx context.Context, command string, limit int) ([]CommandHistoryRow, error)
}
```

When no history exists (`ListCommandHistory` returns an empty slice), the `ctrl+r` path displays a brief informational message — "No commit history yet." — and stops there. The main form re-runs so the user can continue, but no picker is shown and no silent blank re-open occurs.

---

## Testing

| Layer | What is tested |
|---|---|
| `store` | `InsertCommandHistory` + `ListCommandHistory` using existing in-memory SQLite pattern from `store_test.go` |
| `commit/form` | `FillOutForm` extended: assert history is saved after form completion; assert fields are pre-filled when an entry is selected from the picker |
| `formRunner` | Table-driven: `ctrl+r` → `errWantsHistory`; `ctrl+c` → `huh.ErrUserAborted`; normal completion → `nil` |

---

## Files Touched

| File | Change |
|---|---|
| `store/migrations/0003_command_history.sql` | new migration |
| `store/store.go` | `InsertCommandHistory`, `ListCommandHistory`, `CommandHistoryRow` |
| `store/store_test.go` | new tests for above |
| `tui/runner.go` | new file — `FormRunner`, `RunForm` |
| `tui/runner_test.go` | new file — tests for `RunForm` |
| `commit/form.go` | `FillOutForm` orchestration; calls `tui.RunForm` |
| `commit/form_test.go` | extended tests |
| `tui/commit.go` | add `ctrl+r` hint to form help footer |
| `cmd/commit/commit.go` | open store, pass to `FillOutForm` |
