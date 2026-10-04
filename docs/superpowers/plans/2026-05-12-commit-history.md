# Commit History Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users press `ctrl+r` during `git zf commit` to pick a past form submission and re-open the form pre-filled with those values.

**Architecture:** A new `command_history` SQLite table (migration `0003`) stores JSON payloads for every completed commit form submission. A thin `FormRunner` bubbletea wrapper intercepts `ctrl+r` before huh sees it, exposing `WantHistory() bool`. `FillOutForm` loops: on completion it saves to history and returns; on ctrl+r it runs a picker form (via `huh.NewSelect`) and re-opens the main form pre-filled or blank.

**Tech Stack:** Go, `modernc.org/sqlite`, `github.com/charmbracelet/huh` v1, `github.com/charmbracelet/bubbletea` v1

---

### Linter rules to respect throughout

- **nlreturn**: add a blank line before `return` (and `continue`) when it is NOT the sole statement in its block.
- **wrapcheck**: wrap errors from external packages with `fmt.Errorf("context: %w", err)`. Internal module packages do not require wrapping.
- **argument-limit**: max 5 parameters per function.
- **receiver-naming**: max 2-char receiver names.
- **line-length**: max 124 characters.
- **`any`** not `interface{}`. `make(map[K]V)` not `map[K]V{}`.

---

## File map

| File | Action | Responsibility |
|---|---|---|
| `store/migrations/0003_command_history.sql` | Create | DB schema for history table |
| `store/store.go` | Modify | `CommandHistoryRow`, `InsertCommandHistory`, `ListCommandHistory` |
| `store/store_test.go` | Modify | Tests for the two new store methods + update migration table check |
| `tui/runner.go` | Create | `FormRunner`, `RunForm` — bubbletea wrapper that intercepts ctrl+r |
| `tui/runner_test.go` | Create | Unit tests for `FormRunner.Update` |
| `commit/form.go` | Modify | `historyStore` interface, updated `FillOutForm`, helper fns |
| `commit/form_test.go` | Modify | Tests for `historyLabel`, `applyPayload` |
| `tui/commit.go` | Modify | Add ctrl+r hint note to commit message group |
| `cmd/commit/commit.go` | Modify | Open store, pass to `FillOutForm` |

---

### Task 1: Store migration

**Files:**
- Create: `store/migrations/0003_command_history.sql`

- [ ] **Step 1: Create the migration file**

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

- [ ] **Step 2: Verify the migration file is picked up**

The `store.migrate` function reads all `*.sql` files in `store/migrations/` sorted by name and applies them in order, tracking progress via `PRAGMA user_version`. File `0003_command_history.sql` will be applied after `0002_add_tracker_type.sql` automatically.

Run:
```bash
mise exec -- go build ./store/...
```
Expected: exits 0 (no compile error; the embedded FS picks up the new file).

---

### Task 2: Store types and methods

**Files:**
- Modify: `store/store.go`

- [ ] **Step 1: Write the failing test first** (see Task 3 — come back after writing the types)

Skip to Task 3, write the tests, then return here.

- [ ] **Step 2: Add `CommandHistoryRow` type and two methods to `store/store.go`**

Add after the existing type declarations (before the `const dbName` line):

```go
// CommandHistoryRow is one row from the command_history table.
type CommandHistoryRow struct {
	ID        int64
	Payload   json.RawMessage // raw JSON; caller unmarshals into the shape they need
	CreatedAt time.Time
}
```

Add the two methods at the end of the file (before the closing brace of the last function, or simply appended):

```go
// InsertCommandHistory records one completed form submission.
// payload is marshalled to JSON internally; it must be JSON-serialisable.
func (s *Store) InsertCommandHistory(ctx context.Context, command string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal payload: %w", err)
	}

	_, err = s.db.ExecContext(ctx,
		`INSERT INTO command_history (command, payload) VALUES (?, ?)`,
		command, string(data),
	)
	if err != nil {
		return fmt.Errorf("insert command history: %w", err)
	}

	return nil
}

// ListCommandHistory returns the most recent limit entries for command, newest-first.
func (s *Store) ListCommandHistory(ctx context.Context, command string, limit int) ([]CommandHistoryRow, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, payload, created_at FROM command_history
		 WHERE command = ? ORDER BY created_at DESC LIMIT ?`,
		command, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list command history query: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var result []CommandHistoryRow

	for rows.Next() {
		var r CommandHistoryRow
		var payloadStr, createdAtStr string

		if err := rows.Scan(&r.ID, &payloadStr, &createdAtStr); err != nil {
			return nil, fmt.Errorf("scan command history row: %w", err)
		}

		r.Payload = json.RawMessage(payloadStr)

		t, parseErr := parseSQLiteTime(createdAtStr)
		if parseErr != nil {
			return nil, fmt.Errorf("parse command_history created_at %q: %w", createdAtStr, parseErr)
		}

		r.CreatedAt = t
		result = append(result, r)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate command history: %w", err)
	}

	if result == nil {
		result = []CommandHistoryRow{}
	}

	return result, nil
}
```

- [ ] **Step 3: Build**

```bash
mise exec -- go build ./store/...
```
Expected: exits 0.

---

### Task 3: Store tests

**Files:**
- Modify: `store/store_test.go`

- [ ] **Step 1: Update `TestOpen_createsMigrations` to include the new table**

Find this slice in the existing test:
```go
tables := []string{"statuses", "issues", "branches"}
```
Change it to:
```go
tables := []string{"statuses", "issues", "branches", "command_history"}
```

- [ ] **Step 2: Add new test functions at the end of `store/store_test.go`**

```go
func TestInsertCommandHistory_roundTrip(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	payload := map[string]any{"type": "feat", "scope": "auth", "subject": "add OAuth"}

	if err := s.InsertCommandHistory(t.Context(), "commit", payload); err != nil {
		t.Fatalf("InsertCommandHistory: %v", err)
	}

	rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
	if err != nil {
		t.Fatalf("ListCommandHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}

	var got map[string]any
	if err := json.Unmarshal(rows[0].Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["type"] != "feat" {
		t.Errorf(`type = %q, want "feat"`, got["type"])
	}
	if got["scope"] != "auth" {
		t.Errorf(`scope = %q, want "auth"`, got["scope"])
	}
}

func TestListCommandHistory_newestFirst(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	for _, subj := range []string{"first", "second", "third"} {
		if err := s.InsertCommandHistory(t.Context(), "commit", map[string]any{"subject": subj}); err != nil {
			t.Fatalf("insert %s: %v", subj, err)
		}
	}

	rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
	if err != nil {
		t.Fatalf("ListCommandHistory: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}

	var first map[string]any
	if err := json.Unmarshal(rows[0].Payload, &first); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if first["subject"] != "third" {
		t.Errorf(`rows[0].subject = %q, want "third"`, first["subject"])
	}
}

func TestListCommandHistory_limit(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	for i := range 5 {
		if err := s.InsertCommandHistory(t.Context(), "commit", map[string]any{"n": i}); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	rows, err := s.ListCommandHistory(t.Context(), "commit", 3)
	if err != nil {
		t.Fatalf("ListCommandHistory: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("got %d rows, want 3", len(rows))
	}
}

func TestListCommandHistory_commandIsolation(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	if err := s.InsertCommandHistory(t.Context(), "commit", map[string]any{"a": 1}); err != nil {
		t.Fatalf("insert commit: %v", err)
	}
	if err := s.InsertCommandHistory(t.Context(), "branch", map[string]any{"b": 2}); err != nil {
		t.Fatalf("insert branch: %v", err)
	}

	rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
	if err != nil {
		t.Fatalf("ListCommandHistory: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("got %d rows, want 1 (branch entry must not appear)", len(rows))
	}
}

func TestListCommandHistory_empty(t *testing.T) {
	t.Parallel()

	s := openTestStore(t)

	rows, err := s.ListCommandHistory(t.Context(), "commit", 10)
	if err != nil {
		t.Fatalf("ListCommandHistory: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("got %d rows, want 0", len(rows))
	}
}
```

The new tests need `encoding/json` in the import block of `store_test.go`. Add it:

```go
import (
	"encoding/json"
	"testing"
	"time"
)
```

- [ ] **Step 3: Run the tests**

```bash
mise exec -- go test ./store/... -v -run "TestOpen_createsMigrations|TestInsertCommandHistory|TestListCommandHistory"
```
Expected: all listed tests PASS.

- [ ] **Step 4: Run full store test suite**

```bash
mise exec -- go test ./store/...
```
Expected: exits 0, all tests pass.

---

### Task 4: `tui/runner.go` — bubbletea wrapper

**Files:**
- Create: `tui/runner.go`

- [ ] **Step 1: Create the file**

```go
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

// FormRunner wraps a *huh.Form as a tea.Model to intercept ctrl+r before huh sees it.
type FormRunner struct {
	form        *huh.Form
	wantHistory bool
}

// WantHistory reports whether the user pressed ctrl+r during the form.
func (r *FormRunner) WantHistory() bool { return r.wantHistory }

// Init implements tea.Model.
func (r *FormRunner) Init() tea.Cmd { return r.form.Init() }

// Update implements tea.Model.
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

// View implements tea.Model.
func (r *FormRunner) View() string { return r.form.View() }

// RunForm runs form inside a bubbletea program.
// Returns (runner, nil) on normal completion or ctrl+r; call runner.WantHistory() to distinguish.
// Returns (runner, huh.ErrUserAborted) on ctrl+c / esc.
func RunForm(form *huh.Form) (*FormRunner, error) {
	r := &FormRunner{form: form}
	if _, err := tea.NewProgram(r).Run(); err != nil {
		return r, fmt.Errorf("run form: %w", err)
	}

	return r, r.form.Err()
}
```

- [ ] **Step 2: Build**

```bash
mise exec -- go build ./tui/...
```
Expected: exits 0.

---

### Task 5: `tui/runner_test.go` — unit tests for `FormRunner`

**Files:**
- Create: `tui/runner_test.go`

The `FormRunner.Update` method is a pure function on a struct — we can test it directly without running a full bubbletea program.

- [ ] **Step 1: Create the file**

```go
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
)

func newTestRunner() *FormRunner {
	form := huh.NewForm(huh.NewGroup(huh.NewInput().Title("test")))

	return &FormRunner{form: form}
}

func TestFormRunner_WantHistory_falseByDefault(t *testing.T) {
	r := newTestRunner()

	if r.WantHistory() {
		t.Error("WantHistory() = true, want false before any key press")
	}
}

func TestFormRunner_ctrlR_setsWantHistory(t *testing.T) {
	r := newTestRunner()

	msg := tea.KeyMsg{Type: tea.KeyCtrlR}
	model, cmd := r.Update(msg)

	if !r.WantHistory() {
		t.Error("WantHistory() = false, want true after ctrl+r")
	}

	if model != r {
		t.Error("Update must return the same *FormRunner pointer")
	}

	if cmd == nil {
		t.Error("Update must return a non-nil cmd (tea.Quit) on ctrl+r")
	}
}

func TestFormRunner_ctrlR_doesNotCrossContaminate(t *testing.T) {
	r1 := newTestRunner()
	r2 := newTestRunner()

	r1.Update(tea.KeyMsg{Type: tea.KeyCtrlR})

	if r2.WantHistory() {
		t.Error("r2.WantHistory() = true — mutation leaked between runners")
	}
}

func TestFormRunner_otherKey_doesNotSetWantHistory(t *testing.T) {
	r := newTestRunner()

	r.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if r.WantHistory() {
		t.Error("WantHistory() = true after esc, want false")
	}
}
```

- [ ] **Step 2: Run the tests**

```bash
mise exec -- go test ./tui/... -v -run "TestFormRunner"
```
Expected: all 4 tests PASS.

---

### Task 6: `commit/form.go` — history orchestration

**Files:**
- Modify: `commit/form.go`

This task touches every part of `form.go`: new imports, new interface, new constants, modified `loadForm` (add `prefill` param), new `FillOutForm` signature (add `ctx` and `hs` params), and three new helper functions.

- [ ] **Step 1: Update the import block**

Replace the existing import block with:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
)
```

- [ ] **Step 2: Add the `historyStore` interface and constants at the top of the file (after the import block)**

```go
const (
	historyLimit  = 50
	maxLabelLen   = 60
	historyCmd    = "commit"
	newBlankLabel = "New blank commit"
)

// historyStore is the subset of *store.Store used by FillOutForm.
// Injected as an interface so tests can supply a fake.
type historyStore interface {
	InsertCommandHistory(ctx context.Context, command string, payload any) error
	ListCommandHistory(ctx context.Context, command string, limit int) ([]store.CommandHistoryRow, error)
}
```

- [ ] **Step 3: Add `applyPayload` and `historyLabel` helper functions**

Add these before `FillOutForm`:

```go
// applyPayload clones items and sets each item's Value from payload (string values only).
// Returns the clone unmodified for keys that don't match any item name.
func applyPayload(items []config.CommitItem, payload map[string]any) []config.CommitItem {
	out := slices.Clone(items)
	for k, v := range payload {
		if s, ok := v.(string); ok {
			setItemValue(out, k, s)
		}
	}

	return out
}

// historyLabel builds a picker display string for one history entry:
// the first line of the assembled message (truncated to maxLabelLen) followed by [YYYY-MM-DD HH:MM].
func historyLabel(tmplText string, row store.CommandHistoryRow) (string, error) {
	var payload map[string]any
	if err := json.Unmarshal(row.Payload, &payload); err != nil {
		return "", fmt.Errorf("unmarshal history payload: %w", err)
	}

	var buf bytes.Buffer
	if err := assembleMessage(&buf, tmplText, payload); err != nil {
		return "", fmt.Errorf("assemble history label: %w", err)
	}

	subject := strings.SplitN(buf.String(), "\n", 2)[0]
	if len(subject) > maxLabelLen {
		subject = subject[:maxLabelLen]
	}

	return fmt.Sprintf("%-60s  [%s]", subject, row.CreatedAt.Format("2006-01-02 15:04")), nil
}
```

- [ ] **Step 4: Add `runHistoryPicker` function**

```go
// runHistoryPicker presents a select of the last historyLimit history entries.
// Returns (nil, nil) when history is empty (message printed) or user picks "New blank commit" or aborts.
// Returns (payload, nil) when user selects a history entry.
// Returns (nil, err) on unexpected errors.
func runHistoryPicker(ctx context.Context, tmplText string, hs historyStore) (map[string]any, error) {
	entries, err := hs.ListCommandHistory(ctx, historyCmd, historyLimit)
	if err != nil {
		return nil, fmt.Errorf("list history: %w", err)
	}

	if len(entries) == 0 {
		fmt.Println("No commit history yet.")

		return nil, nil
	}

	opts := make([]huh.Option[int], 0, len(entries)+1)
	opts = append(opts, huh.NewOption(newBlankLabel, -1))

	for i, e := range entries {
		label, labelErr := historyLabel(tmplText, e)
		if labelErr != nil {
			slog.Warn("could not build history label", "error", labelErr)
			label = fmt.Sprintf("entry #%d", e.ID)
		}

		opts = append(opts, huh.NewOption(label, i))
	}

	var selected int
	pickerForm := huh.NewForm(huh.NewGroup(
		huh.NewSelect[int]().
			Title("Pick a past commit:").
			Options(opts...).
			Value(&selected),
	))

	_, runErr := tui.RunForm(pickerForm)
	if errors.Is(runErr, huh.ErrUserAborted) {
		return nil, nil
	}
	if runErr != nil {
		return nil, fmt.Errorf("run picker: %w", runErr)
	}

	if selected == -1 {
		return nil, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(entries[selected].Payload, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal selected payload: %w", err)
	}

	return payload, nil
}
```

- [ ] **Step 5: Update `loadForm` to accept an optional `prefill` parameter**

Replace the existing `loadForm` signature and body:

```go
func loadForm(
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	hint IssueHint,
	prefill map[string]any,
) (form *huh.Form, extractMsg func() map[string]any, extractOpts func() tui.CommitOption) {
	slog.Debug("message template", "template", cfg.CommitMessage.Template)

	items := applyIssueHint(cfg.CommitMessage.Items, hint)

	var selectedType string
	if isValidCommitType(cfg.CommitTypes, hint.BranchType) {
		selectedType = hint.BranchType
	}

	if prefill != nil {
		items = applyPayload(items, prefill)
		if t, ok := prefill["type"].(string); ok && isValidCommitType(cfg.CommitTypes, t) {
			selectedType = t
		}
	}

	extractMsg = func() map[string]any {
		m := make(map[string]any, len(items)+1)
		m["type"] = selectedType

		for i := range items {
			m[items[i].Name] = items[i].Value
		}

		return m
	}

	groups := []*huh.Group{tui.CommitMessageGroup(cfg.CommitTypes, items, &selectedType)}

	opts := defaults
	if !defaults.AnyOptionSet() {
		groups = append(groups, tui.CommitOptionsGroup(&opts))
	}

	extractOpts = func() tui.CommitOption { return opts }

	return huh.NewForm(groups...), extractMsg, extractOpts
}
```

- [ ] **Step 6: Replace `FillOutForm` with the new orchestrating version**

Replace the existing `FillOutForm` function entirely:

```go
// FillOutForm presents the commit TUI form and orchestrates the ctrl+r history flow.
// hs must not be nil; pass a *store.Store opened from store.OpenRepo.
//
// Exit conditions:
//   - form completed → saves payload to history, assembles and returns the commit message.
//   - ctrl+r         → shows history picker, then re-opens form (pre-filled or blank).
//   - ctrl+c / esc   → returns ("", zero, huh.ErrUserAborted).
func FillOutForm(
	ctx context.Context,
	cfg *config.AppConfig,
	defaults tui.CommitOption,
	hint IssueHint,
	hs historyStore,
) ([]byte, tui.CommitOption, error) {
	var prefill map[string]any

	for {
		form, extractMsg, extractOpts := loadForm(cfg, defaults, hint, prefill)

		runner, err := tui.RunForm(form)
		if err != nil {
			return nil, tui.CommitOption{}, fmt.Errorf("failed to run the form: %w", err)
		}

		if runner.WantHistory() {
			prefill, err = runHistoryPicker(ctx, cfg.CommitMessage.Template, hs)
			if err != nil {
				return nil, tui.CommitOption{}, err
			}

			continue
		}

		answers := extractMsg()
		opts := extractOpts()

		if saveErr := hs.InsertCommandHistory(ctx, historyCmd, answers); saveErr != nil {
			slog.Warn("could not save commit history", "error", saveErr)
		}

		var buf bytes.Buffer
		if err := assembleMessage(&buf, cfg.CommitMessage.Template, answers); err != nil {
			return nil, tui.CommitOption{}, fmt.Errorf("assemble message: %w", err)
		}

		return buf.Bytes(), opts, nil
	}
}
```

- [ ] **Step 7: Build**

```bash
mise exec -- go build ./commit/...
```
Expected: exits 0 (the call site in `cmd/commit/commit.go` will break — fix that in Task 8).

If the build fails with "wrong number of arguments in call to commitpkg.FillOutForm": that's expected — fix it in Task 8. For now, build only the `commit` package itself:

```bash
mise exec -- go build github.com/piprim/git-zf/commit
```
Expected: exits 0.

---

### Task 7: `commit/form_test.go` — helper function tests

**Files:**
- Modify: `commit/form_test.go`

- [ ] **Step 1: Add a fake store type and tests for `applyPayload` and `historyLabel`**

Add the following at the end of `commit/form_test.go`:

```go
// fakeHistoryStore is a test double for historyStore.
type fakeHistoryStore struct {
	rows []store.CommandHistoryRow
	err  error
}

func (f *fakeHistoryStore) InsertCommandHistory(_ context.Context, _ string, _ any) error {
	return f.err
}

func (f *fakeHistoryStore) ListCommandHistory(_ context.Context, _ string, _ int) ([]store.CommandHistoryRow, error) {
	return f.rows, f.err
}

func TestApplyPayload_setsMatchingItems(t *testing.T) {
	items := []config.CommitItem{
		{Name: "scope"},
		{Name: "subject"},
		{Name: "body"},
	}
	payload := map[string]any{
		"scope":   "auth",
		"subject": "add OAuth",
		"unknown": "ignored",
	}

	got := applyPayload(items, payload)

	assertFieldValue(t, got, "scope", "auth")
	assertFieldValue(t, got, "subject", "add OAuth")
	assertFieldValue(t, got, "body", "")
}

func TestApplyPayload_doesNotMutateInput(t *testing.T) {
	items := []config.CommitItem{{Name: "scope"}, {Name: "subject"}}
	original := slices.Clone(items)

	applyPayload(items, map[string]any{"scope": "changed"})

	assertNoInputMutation(t, items, original)
}

func TestApplyPayload_nonStringValuesIgnored(t *testing.T) {
	items := []config.CommitItem{{Name: "scope"}}

	got := applyPayload(items, map[string]any{"scope": 42})

	assertFieldValue(t, got, "scope", "")
}

func TestHistoryLabel_format(t *testing.T) {
	row := store.CommandHistoryRow{
		ID:        1,
		Payload:   []byte(`{"type":"feat","scope":"auth","subject":"add OAuth"}`),
		CreatedAt: time.Date(2026, 5, 10, 14, 32, 0, 0, time.UTC),
	}
	tmpl := `{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}`

	label, err := historyLabel(tmpl, row)

	if err != nil {
		t.Fatalf("historyLabel: %v", err)
	}
	if !strings.Contains(label, "feat(auth): add OAuth") {
		t.Errorf("label does not contain message: %q", label)
	}
	if !strings.Contains(label, "[2026-05-10 14:32]") {
		t.Errorf("label does not contain date: %q", label)
	}
}

func TestHistoryLabel_truncatesLongSubject(t *testing.T) {
	longSubject := strings.Repeat("x", 80)
	row := store.CommandHistoryRow{
		ID:        2,
		Payload:   []byte(`{"type":"feat","subject":"` + longSubject + `"}`),
		CreatedAt: time.Date(2026, 5, 10, 14, 32, 0, 0, time.UTC),
	}

	label, err := historyLabel(`{{.type}}: {{.subject}}`, row)
	if err != nil {
		t.Fatalf("historyLabel: %v", err)
	}

	// The subject portion must be at most maxLabelLen chars.
	subject := strings.SplitN(label, "  [", 2)[0]
	if len(strings.TrimRight(subject, " ")) > maxLabelLen {
		t.Errorf("subject portion too long: %d > %d", len(strings.TrimRight(subject, " ")), maxLabelLen)
	}
}
```

The new tests need `store` and `time` imported in `commit/form_test.go`. Update the import block:

```go
import (
	"bytes"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/store"
)
```

- [ ] **Step 2: Run the new tests**

```bash
mise exec -- go test ./commit/... -v -run "TestApplyPayload|TestHistoryLabel"
```
Expected: all 5 new tests PASS.

- [ ] **Step 3: Run full commit test suite**

```bash
mise exec -- go test ./commit/...
```
Expected: exits 0.

---

### Task 8: Wire store into `cmd/commit/commit.go` and add ctrl+r hint to TUI

**Files:**
- Modify: `cmd/commit/commit.go`
- Modify: `tui/commit.go`

#### 8a — `cmd/commit/commit.go`

- [ ] **Step 1: Add `store` and `context` to the import block**

Replace the import block with:

```go
import (
	"fmt"
	"log/slog"

	"github.com/piprim/git-zf/branch"
	commitpkg "github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)
```

- [ ] **Step 2: Update `runE` to open the store and pass it to `FillOutForm`**

Replace the body of `runE` from the `hint := ...` line onward:

```go
func (c Commit) runE(cmd *cobra.Command, flags tui.CommitOption) error {
	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	authors, err := client.Authors()
	if err != nil {
		slog.Warn("could not load author list", "error", err)
		authors = []string{}
	}

	defaults := flags
	defaults.Authors = authors

	hint := issueHintFromClient(client)

	s, err := store.OpenRepo(cmd.Context())
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	msg, opts, err := commitpkg.FillOutForm(cmd.Context(), c.appConfig, defaults, hint, s)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}

	summary, err := client.Commit(cmd.Context(), msg, git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	})
	if err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	printCommitSummary(&summary)

	return nil
}
```

- [ ] **Step 3: Build the full binary**

```bash
mise exec -- go build ./...
```
Expected: exits 0.

#### 8b — `tui/commit.go`: add ctrl+r hint

- [ ] **Step 4: Add a `huh.NewNote` at the end of `CommitMessageGroup` to surface the ctrl+r shortcut**

In `tui/commit.go`, inside `CommitMessageGroup`, just before `return huh.NewGroup(msgFields...)`, add:

```go
msgFields = append(msgFields,
	huh.NewNote().Body("ctrl+r  pick from history"),
)
```

The full tail of `CommitMessageGroup` becomes:

```go
	// ... (existing loop over items) ...

	msgFields = append(msgFields,
		huh.NewNote().Body("ctrl+r  pick from history"),
	)

	return huh.NewGroup(msgFields...)
}
```

- [ ] **Step 5: Build and run full test suite**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```
Expected: both exit 0.

---

## Self-Review

### Spec coverage

| Spec requirement | Task |
|---|---|
| `command_history` table with `command`, `payload` (CHECK json_valid), `created_at`, index | Task 1 |
| `InsertCommandHistory(ctx, command, payload any)` | Task 2 |
| `ListCommandHistory(ctx, command, limit)` returning newest-first | Task 2 |
| `FormRunner` wrapping `*huh.Form` as `tea.Model` | Task 4 |
| `ctrl+r` → `wantHistory = true`, tea.Quit | Task 4 |
| `RunForm` returns `(*FormRunner, error)` | Task 4 |
| `RunForm` returns `huh.ErrUserAborted` on ctrl+c / esc | Task 4 |
| `WantHistory() bool` on result | Task 4 |
| `FillOutForm` saves payload after form completion | Task 6 |
| `FillOutForm` runs history picker on ctrl+r | Task 6 |
| When history empty → print "No commit history yet." | Task 6 (`runHistoryPicker`) |
| Picker: "New blank commit" pinned at position 0 | Task 6 (`runHistoryPicker`) |
| Picker: labels = assembled message truncated to 60 + timestamp | Task 6 (`historyLabel`) |
| Pre-fill items from selected history entry | Task 6 (`applyPayload` + `loadForm` prefill) |
| `historyStore` interface for test injection | Task 6 |
| `ctrl+r: pick from history` hint in main form | Task 8b |
| Store opened and passed in `cmd/commit/commit.go` | Task 8a |
| Store tests using `openTestStore` pattern | Task 3 |
| `FormRunner` unit tests | Task 5 |
| Helper function tests | Task 7 |

All spec requirements covered.

### Placeholder scan

No TBD, TODO, or "similar to Task N" patterns. Every step contains concrete code.

### Type consistency

- `historyStore` interface in `commit/form.go` references `store.CommandHistoryRow` — consistent with the type defined in `store/store.go`.
- `fakeHistoryStore` in `commit/form_test.go` implements `historyStore` — consistent with the interface.
- `loadForm` signature updated to 4 params including `prefill map[string]any` — consistent with every call site inside `FillOutForm`.
- `FillOutForm` updated to 5 params `(ctx, cfg, defaults, hint, hs)` — consistent with call in `cmd/commit/commit.go`.
- `RunForm` returns `(*FormRunner, error)` — consistent with usage in `FillOutForm` and `runHistoryPicker`.

---

**Plan complete and saved to `docs/superpowers/plans/2026-05-12-commit-history.md`.**

Two execution options:

**1. Subagent-Driven (recommended)** — fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
