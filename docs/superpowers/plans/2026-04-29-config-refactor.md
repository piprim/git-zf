# Config Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
> Use Go Skills and code-review agent for each task.

**Goal:** Move all app-wide configuration into a new `config/` package; promote commit types to a top-level `commit-types` key; rename `message`→`commit-message` and `tracker`→`issue-tracker`; load config once in the root command.

**Architecture:** A new `config/` package owns `default.json` (embedded), all config structs (`AppConfig`, `CommitTypeOption`, `CommitItem`, `CommitMessageConfig`, `BranchConfig`, `IssueTrackerConfig`), and `Load()`. `cmd/root.go` calls `config.Load()` once in `initConfig()` and stores the result in a package-level `appConfig`. Subcommands read `appConfig` directly — no viper calls outside `config/`.

**Tech Stack:** Go stdlib (`encoding/json`, `embed`), Viper + mapstructure overlay, Cobra, charmbracelet/huh.

---

## File map

| File | Action |
|------|--------|
| `config/config.go` | **create** |
| `config/default.json` | **create** |
| `config/config_test.go` | **create** |
| `commit/defaultConfig.go` | **delete** |
| `commit/form.go` | modify — remove `DefaultMessageConfig`, update `FillOutForm` + `loadForm` |
| `tui/commit.go` | modify — remove `CommitMessageConfig`/`CommitItem`/`CommitItemOption`, update `CommitMessageGroup` |
| `tracker/tracker.go` | modify — delete `Config` type, `New`/`Register` take `config.IssueTrackerConfig` |
| `tracker/tracker_test.go` | modify — use `config.IssueTrackerConfig` |
| `tracker/redmine/redmine.go` | modify — `New` parameter type |
| `tracker/redmine/redmine_test.go` | modify — use `config.IssueTrackerConfig` |
| `cmd/root.go` | modify — add `appConfig` var, call `config.Load()` in `initConfig()` |
| `cmd/commit.go` | modify — delete `loadMessageConfig()`, use `appConfig` |
| `cmd/issue.go` | modify — use `appConfig`, update `getAllowedBranchType` |

---

### Task 1: Create the `config/` package

**Files:**
- Create: `config/config.go`
- Create: `config/default.json`
- Create: `config/config_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// config/config_test.go
package config_test

import (
	"testing"

	"github.com/spf13/viper"

	"github.com/piprim/git-zf/config"
)

func TestLoad_defaults(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.CommitTypes) != 10 {
		t.Errorf("CommitTypes len = %d, want 10", len(cfg.CommitTypes))
	}
	if cfg.CommitTypes[0].Name != "feat" {
		t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "feat")
	}
	if cfg.CommitMessage.Template == "" {
		t.Error("CommitMessage.Template is empty")
	}
	if len(cfg.CommitMessage.Items) != 4 {
		t.Errorf("CommitMessage.Items len = %d, want 4", len(cfg.CommitMessage.Items))
	}
	if cfg.IssueTracker.Type != "" {
		t.Errorf("IssueTracker.Type = %q, want empty", cfg.IssueTracker.Type)
	}
	if cfg.IssueTracker.InProgressStatus != "In Progress" {
		t.Errorf("IssueTracker.InProgressStatus = %q, want %q",
			cfg.IssueTracker.InProgressStatus, "In Progress")
	}
}

func TestLoad_overlay(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	viper.Set("commit-types", []map[string]any{
		{"name": "custom", "desc": "Custom type"},
	})

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.CommitTypes) != 1 {
		t.Errorf("CommitTypes len = %d, want 1", len(cfg.CommitTypes))
	}
	if cfg.CommitTypes[0].Name != "custom" {
		t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "custom")
	}
	// Unset keys fall back to defaults.
	if cfg.CommitMessage.Template == "" {
		t.Error("CommitMessage.Template should remain from defaults")
	}
}
```

- [ ] **Step 2: Run the tests — expect compile failure (package doesn't exist yet)**

```
go test ./config/...
```

Expected: `cannot find package "github.com/piprim/git-zf/config"`

- [ ] **Step 3: Create `config/default.json`**

```json
{
  "commit-types": [
    { "name": "feat",     "desc": "A new feature" },
    { "name": "fix",      "desc": "A bug fix" },
    { "name": "docs",     "desc": "Documentation only changes" },
    { "name": "style",    "desc": "Changes that do not affect the meaning of the code" },
    { "name": "refactor", "desc": "A code change that neither fixes a bug nor adds a feature" },
    { "name": "perf",     "desc": "A code change that improves performance" },
    { "name": "test",     "desc": "Adding missing tests" },
    { "name": "chore",    "desc": "Changes to the build process or auxiliary tools" },
    { "name": "revert",   "desc": "Revert to a commit" },
    { "name": "WIP",      "desc": "Work in progress" }
  ],
  "commit-message": {
    "items": [
      { "name": "scope",   "desc": "Scope (users, db, poll…):", "form": "input" },
      { "name": "subject", "desc": "Concise description. Imperative, lower case, no final dot:", "form": "input", "required": true },
      { "name": "body",    "desc": "Motivation for the change and contrast with previous behaviour:", "form": "multiline" },
      { "name": "footer",  "desc": "Breaking changes and referenced issues:", "form": "multiline" }
    ],
    "template": "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"
  },
  "branch": {
    "base": ""
  },
  "issue-tracker": {
    "type": "",
    "url": "",
    "token": "",
    "in-progress-status": "In Progress"
  }
}
```

- [ ] **Step 4: Create `config/config.go`**

```go
package config

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

//go:embed default.json
var defaultJSON []byte

// CommitTypeOption is a single commit type entry (e.g. "feat", "fix").
type CommitTypeOption struct {
	Name string `json:"name" mapstructure:"name"`
	Desc string `json:"desc" mapstructure:"desc"`
}

// CommitItemOption is a selectable option within a CommitItem select field.
type CommitItemOption struct {
	Name string `json:"name" mapstructure:"name"`
	Desc string `json:"desc" mapstructure:"desc"`
}

// CommitItem describes one field in the commit message form.
// Value is written by the form after the user submits.
type CommitItem struct {
	Name     string             `json:"name"     mapstructure:"name"`
	Desc     string             `json:"desc"     mapstructure:"desc"`
	Form     string             `json:"form"     mapstructure:"form"`
	Required bool               `json:"required" mapstructure:"required"`
	Options  []CommitItemOption `json:"options"  mapstructure:"options"`
	Value    string
}

// CommitMessageConfig holds the ordered list of form fields and the Go template
// used to assemble the commit message.
type CommitMessageConfig struct {
	Items    []CommitItem `json:"items"    mapstructure:"items"`
	Template string       `json:"template" mapstructure:"template"`
}

// BranchConfig holds branch-related settings.
// Base is the branch new branches are cut from; empty means auto-detect.
type BranchConfig struct {
	Base string `json:"base" mapstructure:"base"`
}

// IssueTrackerConfig holds connection parameters for one tracker instance.
type IssueTrackerConfig struct {
	Type             string `json:"type"               mapstructure:"type"`
	URL              string `json:"url"                mapstructure:"url"`
	Token            string `json:"token"              mapstructure:"token"`
	InProgressStatus string `json:"in-progress-status" mapstructure:"in-progress-status"`
}

// AppConfig is the top-level configuration for the application.
type AppConfig struct {
	CommitTypes   []CommitTypeOption  `json:"commit-types"   mapstructure:"commit-types"`
	CommitMessage CommitMessageConfig `json:"commit-message" mapstructure:"commit-message"`
	Branch        BranchConfig        `json:"branch"         mapstructure:"branch"`
	IssueTracker  IssueTrackerConfig  `json:"issue-tracker"  mapstructure:"issue-tracker"`
}

// Load parses the embedded default.json then overlays any values present in the
// global viper instance. viper.UnmarshalKey is used throughout because viper.Sub
// does not work correctly for array-typed keys.
func Load() (AppConfig, error) {
	var cfg AppConfig
	if err := json.Unmarshal(defaultJSON, &cfg); err != nil {
		return AppConfig{}, fmt.Errorf("parse default config: %w", err)
	}

	decoderOpt := func(dc *mapstructure.DecoderConfig) { dc.ZeroFields = true }

	for _, key := range []string{"commit-types", "commit-message", "branch", "issue-tracker"} {
		if !viper.IsSet(key) {
			continue
		}

		var target any
		switch key {
		case "commit-types":
			target = &cfg.CommitTypes
		case "commit-message":
			target = &cfg.CommitMessage
		case "branch":
			target = &cfg.Branch
		case "issue-tracker":
			target = &cfg.IssueTracker
		}

		if err := viper.UnmarshalKey(key, target, decoderOpt); err != nil {
			return AppConfig{}, fmt.Errorf("unmarshal %s: %w", key, err)
		}
	}

	return cfg, nil
}
```

- [ ] **Step 5: Run the tests — expect PASS**

```
go test ./config/...
```

Expected: `ok  github.com/piprim/git-zf/config`

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/default.json config/config_test.go
git commit -m "feat(config): add config package with AppConfig, default.json, and Load()"
```

---

### Task 2: Update `tui/commit.go`

**Files:**
- Modify: `tui/commit.go`

`CommitMessageConfig`, `CommitItem`, and `CommitItemOption` move to `config/`. `CommitMessageGroup` gains two parameters: `commitTypes` (builds the first select) and `selectedType *string` (receives the chosen value).

- [ ] **Step 1: Rewrite `tui/commit.go`**

Replace the full file content:

```go
package tui

import (
	"errors"
	"log"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/piprim/git-zf/config"
)

// CommitOption holds commit option values for the commit options group of the TUI.
// Used both as flag-derived defaults (input) and as user selections (output).
type CommitOption struct {
	Authors    []string
	Author     string
	All        bool
	Amend      bool
	NoVerify   bool
	Signoff    bool
	AllowEmpty bool
}

// AnyOptionSet reports true if any commit-option flag was passed.
// When true, the CommitOptionsGroup of the TUI should be skipped.
func (o CommitOption) AnyOptionSet() bool {
	return o.All || o.Amend || o.NoVerify || o.Signoff || o.AllowEmpty || o.Author != ""
}

var descStyle = lipgloss.NewStyle().
	Foreground(lipgloss.Color("#888888")).
	PaddingLeft(1)

// CommitMessageGroup builds the first commit form group.
// The type select is always first, populated from commitTypes and writing into selectedType.
// Remaining fields come from items (scope, subject, body, footer).
func CommitMessageGroup(commitTypes []config.CommitTypeOption, items []config.CommitItem, selectedType *string) *huh.Group {
	requireValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("required")
		}

		return nil
	}

	typeOpts := make([]huh.Option[string], len(commitTypes))
	for i, ct := range commitTypes {
		typeOpts[i] = huh.NewOption(titleCase(ct.Name)+"\n"+descStyle.Render(ct.Desc), ct.Name)
	}
	if len(typeOpts) == 0 {
		typeOpts = []huh.Option[string]{huh.NewOption("feat", "feat")}
	}

	msgFields := make([]huh.Field, 0, len(items)+1)
	msgFields = append(msgFields,
		huh.NewSelect[string]().
			Title("Type:").
			Options(typeOpts...).
			Value(selectedType),
	)

	for i := range items {
		f := &items[i]
		switch f.Form {
		case "select":
			opts := make([]huh.Option[string], len(f.Options))
			for j, opt := range f.Options {
				name := titleCase(opt.Name)
				opts[j] = huh.NewOption(name+"\n"+descStyle.Render(opt.Desc), opt.Name)
			}
			sel := huh.NewSelect[string]().
				Title(f.Desc).
				Options(opts...).
				Value(&f.Value)
			if f.Required {
				sel = sel.Validate(requireValidator)
			}
			msgFields = append(msgFields, sel)
		case "input":
			inp := huh.NewInput().
				Title(titleCase(f.Name) + ":").
				Placeholder(f.Desc).
				Value(&f.Value)
			if f.Required {
				inp = inp.Validate(requireValidator)
			}
			msgFields = append(msgFields, inp)
		case "multiline":
			txt := huh.NewText().
				Lines(2).
				Title(strings.ToTitle(f.Name)).
				Placeholder(f.Desc).
				Value(&f.Value)
			if f.Required {
				txt = txt.Validate(requireValidator)
			}
			msgFields = append(msgFields, txt)
		default:
			log.Printf("unknown form type %q for field %q, skipping", f.Form, f.Name)
		}
	}

	return huh.NewGroup(msgFields...)
}

// CommitOptionsGroup builds the second commit form group (author + commit flags).
// opt is written directly by the form fields.
func CommitOptionsGroup(opt *CommitOption) *huh.Group {
	authorOpts := make([]huh.Option[string], 0, len(opt.Authors))
	for _, a := range opt.Authors {
		authorOpts = append(authorOpts, huh.NewOption(a, a))
	}
	if len(authorOpts) == 0 {
		authorOpts = []huh.Option[string]{huh.NewOption("(no authors found)", "")}
	}

	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Author:").
			Options(authorOpts...).
			Value(&opt.Author),
		huh.NewConfirm().Title("Stage all tracked modified/deleted files? (--all)").Value(&opt.All),
		huh.NewConfirm().Title("Amend last commit? (--amend)").Value(&opt.Amend),
		huh.NewConfirm().Title("Skip hooks? (--no-verify)").Value(&opt.NoVerify),
		huh.NewConfirm().Title("Add Signed-off-by trailer? (--signoff)").Value(&opt.Signoff),
		huh.NewConfirm().Title("Allow empty commit? (--allow-empty)").Value(&opt.AllowEmpty),
	)
}

func titleCase(s string) string {
	c := cases.Title(language.English, cases.NoLower)

	return c.String(s)
}
```

- [ ] **Step 2: Run tests to verify the package still compiles and passes**

```
go test ./tui/...
```

Expected: `ok  github.com/piprim/git-zf/tui`

(Note: other packages that import `tui.CommitMessageConfig` etc. will break — that is expected and will be fixed in subsequent tasks.)

- [ ] **Step 3: Commit**

```bash
git add tui/commit.go
git commit -m "refactor(tui): remove CommitItem types (moved to config); update CommitMessageGroup signature"
```

---

### Task 3: Update `tracker/` — replace `tracker.Config` with `config.IssueTrackerConfig`

**Files:**
- Modify: `tracker/tracker.go`
- Modify: `tracker/tracker_test.go`
- Modify: `tracker/redmine/redmine.go`
- Modify: `tracker/redmine/redmine_test.go`

- [ ] **Step 1: Rewrite `tracker/tracker.go`**

```go
package tracker

import (
	"context"
	"fmt"
	"sync"

	"github.com/piprim/git-zf/config"
)

// Issue is the tracker-agnostic representation of a work item.
type Issue struct {
	TrackerType string
	ID          string
	Subject     string
	Description string
	Status      string
}

// Tracker is the contract every adapter must satisfy.
type Tracker interface {
	ListIssues(ctx context.Context) ([]Issue, error)
	UpdateIssueStatus(ctx context.Context, issueID, statusName string) error
}

var (
	registryMu sync.RWMutex
	registry   = map[string]func(config.IssueTrackerConfig) (Tracker, error){}
)

// Register adds a factory function for the named tracker type.
func Register(name string, fn func(config.IssueTrackerConfig) (Tracker, error)) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[name] = fn
}

// New constructs a Tracker from cfg using the registered factory.
func New(cfg config.IssueTrackerConfig) (Tracker, error) {
	registryMu.RLock()
	fn, ok := registry[cfg.Type]
	registryMu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("unknown tracker type %q: adapter not registered", cfg.Type)
	}

	return fn(cfg)
}
```

- [ ] **Step 2: Update `tracker/tracker_test.go`**

```go
package tracker_test

import (
	"context"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)

type stubTracker struct{}

func (s *stubTracker) ListIssues(_ context.Context) ([]tracker.Issue, error) { return nil, nil }
func (s *stubTracker) UpdateIssueStatus(_ context.Context, _, _ string) error { return nil }

func TestRegisterAndNew_happy(t *testing.T) {
	t.Parallel()

	const key = "stub-test-register"
	tracker.Register(key, func(_ config.IssueTrackerConfig) (tracker.Tracker, error) {
		return &stubTracker{}, nil
	})

	tr, err := tracker.New(config.IssueTrackerConfig{Type: key})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if tr == nil {
		t.Error("New returned nil tracker")
	}
}

func TestNew_unknownType(t *testing.T) {
	t.Parallel()

	_, err := tracker.New(config.IssueTrackerConfig{Type: "no-such-adapter-xyz"})
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-adapter-xyz") {
		t.Errorf("error should mention the unknown type, got: %v", err)
	}
}
```

- [ ] **Step 3: Update `tracker/redmine/redmine.go`**

Change the struct field and `New` signature (two lines):

```go
// redmineAdapter is the internal struct — change cfg field type:
type redmineAdapter struct {
	client *redminelib.Client
	cfg    config.IssueTrackerConfig
	http   *http.Client
}

// New — change parameter type:
func New(cfg config.IssueTrackerConfig) (tracker.Tracker, error) {
	if cfg.URL == "" {
		return nil, fmt.Errorf("redmine: URL is required")
	}
	if cfg.Token == "" {
		return nil, fmt.Errorf("redmine: Token is required")
	}

	c := redminelib.NewClient(cfg.URL, cfg.Token)

	return &redmineAdapter{client: c, cfg: cfg, http: &http.Client{}}, nil
}
```

Also add the `config` import and remove the now-unused `tracker` import alias if any. The full import block for `redmine.go` becomes:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	redminelib "github.com/mattn/go-redmine"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker"
)
```

- [ ] **Step 4: Update `tracker/redmine/redmine_test.go`**

Replace `tracker.Config{...}` with `config.IssueTrackerConfig{...}` (three occurrences) and add the `config` import:

```go
import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tracker/redmine"
)
```

Change each `tracker.Config{` to `config.IssueTrackerConfig{`:

```go
// TestListIssues_success
adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "test-key"})

// TestListIssues_authFailure
adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "bad-key"})

// TestUpdateIssueStatus_success
adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "key"})

// TestUpdateIssueStatus_statusNotFound
adapter, err := redmine.New(config.IssueTrackerConfig{URL: srv.URL, Token: "key"})
```

- [ ] **Step 5: Verify `tracker/redmine/init.go` needs no changes**

`init.go` calls `tracker.Register(trackerType, New)`. After updating `New`'s parameter type to
`config.IssueTrackerConfig`, `Register` expects exactly that signature — no edit required.
Confirm the file still looks like this (no action needed):

```go
package redmine

import "github.com/piprim/git-zf/tracker"

func init() {
	tracker.Register(trackerType, New)
}
```

- [ ] **Step 6: Run tracker tests**

```
go test ./tracker/... ./tracker/redmine/...
```

Expected:
```
ok  github.com/piprim/git-zf/tracker
ok  github.com/piprim/git-zf/tracker/redmine
```

- [ ] **Step 7: Commit**

```bash
git add tracker/tracker.go tracker/tracker_test.go tracker/redmine/redmine.go tracker/redmine/redmine_test.go
git commit -m "refactor(tracker): replace tracker.Config with config.IssueTrackerConfig"
```

---

### Task 4: Update `commit/form.go` and delete `commit/defaultConfig.go`

**Files:**
- Delete: `commit/defaultConfig.go`
- Modify: `commit/form.go`

`DefaultMessageConfig()` is deleted. `FillOutForm` and `loadForm` accept `config.AppConfig`. `loadForm` declares a local `selectedType string` and passes it to `tui.CommitMessageGroup`.

- [ ] **Step 1: Delete `commit/defaultConfig.go`**

```bash
git rm commit/defaultConfig.go
```

- [ ] **Step 2: Rewrite `commit/form.go`**

```go
package commit

import (
	"bytes"
	"fmt"
	"log"
	"slices"
	"strings"
	"text/template"

	"github.com/charmbracelet/huh"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tui"
)

// FillOutForm presents the commit TUI form.
// Group 1: type select + commit message fields (scope, subject, body, footer).
// Group 2: commit options (author, all, amend, no-verify, signoff, allow-empty).
//
//	Group 2 is skipped when defaults.AnyOptionSet() is true (flags were passed).
//
// Returns the assembled commit message bytes and the (possibly user-modified) options.
func FillOutForm(cfg config.AppConfig, defaults tui.CommitOption) ([]byte, tui.CommitOption, error) {
	form, extractMsg, extractOpts := loadForm(cfg, defaults)
	tmplText := cfg.CommitMessage.Template

	if err := form.Run(); err != nil {
		return nil, tui.CommitOption{}, fmt.Errorf("failed to run the form: %w", err)
	}

	answers := extractMsg()
	opts := extractOpts()

	var buf bytes.Buffer
	if err := assembleMessage(&buf, tmplText, answers); err != nil {
		log.Printf("assemble failed, err=%v\n", err)

		return nil, tui.CommitOption{}, fmt.Errorf("assemble message: %w", err)
	}

	return buf.Bytes(), opts, nil
}

// BuildAuthorList deduplicates all (may contain duplicates), sorts alphabetically,
// then prepends current as the first entry (removing it from its sorted position if present).
// If current is empty, the sorted deduplicated list is returned as-is.
func BuildAuthorList(all []string, current string) []string {
	seen := make(map[string]struct{})
	var unique []string

	for _, a := range all {
		if _, ok := seen[a]; !ok {
			seen[a] = struct{}{}
			unique = append(unique, a)
		}
	}

	slices.Sort(unique)

	if current == "" {
		return unique
	}

	filtered := make([]string, 0, len(unique))
	for _, a := range unique {
		if a != current {
			filtered = append(filtered, a)
		}
	}

	return append([]string{current}, filtered...)
}

// assembleMessage trims whitespace from all string answers, then executes tmplText writing the result to buf.
func assembleMessage(buf *bytes.Buffer, tmplText string, answers map[string]any) error {
	tmpl, err := template.New("").Parse(tmplText)
	if err != nil {
		return fmt.Errorf("failed to parse template: %w", err)
	}

	for k, v := range answers {
		if s, ok := v.(string); ok {
			answers[k] = strings.TrimSpace(s)
		}
	}

	if err := tmpl.Execute(buf, answers); err != nil {
		return fmt.Errorf("failed to execute template: %w", err)
	}

	return nil
}

func loadForm(
	cfg config.AppConfig,
	defaults tui.CommitOption,
) (form *huh.Form, extractMsg func() map[string]any, extractOpts func() tui.CommitOption) {
	log.Printf("message tmpl: %s", cfg.CommitMessage.Template)

	var selectedType string

	extractMsg = func() map[string]any {
		m := make(map[string]any, len(cfg.CommitMessage.Items)+1)
		m["type"] = selectedType

		for i := range cfg.CommitMessage.Items {
			m[cfg.CommitMessage.Items[i].Name] = cfg.CommitMessage.Items[i].Value
		}

		return m
	}

	groups := []*huh.Group{tui.CommitMessageGroup(cfg.CommitTypes, cfg.CommitMessage.Items, &selectedType)}

	opts := defaults
	if !defaults.AnyOptionSet() {
		groups = append(groups, tui.CommitOptionsGroup(&opts))
	}

	extractOpts = func() tui.CommitOption { return opts }

	return huh.NewForm(groups...), extractMsg, extractOpts
}
```

- [ ] **Step 3: Run commit package tests**

```
go test ./commit/...
```

Expected: `ok  github.com/piprim/git-zf/commit`

- [ ] **Step 4: Commit**

```bash
git add commit/form.go
git commit -m "refactor(commit): remove DefaultMessageConfig; FillOutForm accepts config.AppConfig"
```

---

### Task 5: Wire `cmd/` — load config once in root, update all subcommands

**Files:**
- Modify: `cmd/root.go`
- Modify: `cmd/commit.go`
- Modify: `cmd/issue.go`

- [ ] **Step 1: Update `cmd/root.go`**

Add a package-level `appConfig` var and call `config.Load()` at the end of `initConfig()`:

```go
package cmd

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	isDebug   bool
	appConfig config.AppConfig
)

// GetRootCmd builds and returns the root Cobra command.
func GetRootCmd() (*cobra.Command, error) {
	rootCmd := &cobra.Command{
		Use:  "commitizen-go",
		Long: `Command line utility to standardize git commit messages, golang version.`,
		PersistentPreRun: func(cmd *cobra.Command, _ []string) {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
		},
	}

	if err := initConfig(); err != nil {
		return nil, err
	}

	rootCmd.PersistentFlags().BoolVarP(&isDebug, "debug", "d", false,
		"debug mode, output debug info to debug.log")

	rootCmd.AddCommand(getCommitCmd(), getIssueCmd(), getBranchCmd(), getVersionCmd(), getInstallCmd())

	return rootCmd, nil
}

// initConfig sets up logging, loads the .git-zf.json config file via Viper, then
// parses the full AppConfig. Not being inside a git repo is not a fatal error —
// git cz version/install must work anywhere.
func initConfig() error {
	if !isDebug {
		log.SetOutput(io.Discard)
	} else {
		f, err := os.OpenFile("debug.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			return fmt.Errorf("failed to open debug.log: %w", err)
		}

		log.SetFlags(log.Lshortfile | log.LstdFlags)
		log.SetOutput(f)
	}

	home, err := homedir.Dir()
	if err != nil {
		return fmt.Errorf("get home dir failed: %w", err)
	}

	viper.SetConfigName(".git-zf.json")
	viper.SetConfigType("json")

	// Repo-root config takes priority over home: add it first so Viper searches it first.
	if client, err := git.NewClient(); err == nil {
		if root, err := client.WorkingTreeRoot(); err == nil && root != "" {
			viper.AddConfigPath(root)
		}
	}

	viper.AddConfigPath(home)

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			log.Println("can not find config file")
		} else {
			log.Printf("read config failed, err=%v\n", err)
		}
	} else {
		log.Println("read config success")
	}

	appConfig, err = config.Load()
	if err != nil {
		return fmt.Errorf("load app config: %w", err)
	}

	return nil
}
```

- [ ] **Step 2: Update `cmd/commit.go`**

Delete `loadMessageConfig()`, remove `mapstructure` and `viper` imports, pass `appConfig` to `FillOutForm`:

```go
package cmd

import (
	"fmt"
	"log"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

var (
	commitAll        bool
	commitAmend      bool
	commitNoVerify   bool
	commitSignoff    bool
	commitAllowEmpty bool
	commitAuthor     string
)

// CommitCmd is the "git cz commit" subcommand.
func getCommitCmd() *cobra.Command {
	var commitCmd = &cobra.Command{
		Use:   "commit",
		Short: "Record changes to the repository",
		Long:  "Open the commitizen TUI to compose a standardised commit message, then commit using go-git.",
		RunE:  commitRunE,
	}

	f := commitCmd.Flags()
	f.BoolVarP(&commitAll, "all", "a", false,
		"stage all tracked modified/deleted files before committing")
	f.BoolVar(&commitAmend, "amend", false,
		"replace the tip of the current branch")
	f.BoolVarP(&commitNoVerify, "no-verify", "n", false,
		"bypass pre-commit and commit-msg hooks")
	f.BoolVarP(&commitSignoff, "signoff", "s", false,
		"add Signed-off-by trailer to the commit message")
	f.BoolVar(&commitAllowEmpty, "allow-empty", false,
		"allow a commit with no changes")
	f.StringVar(&commitAuthor, "author", "",
		`override commit author as "Name <email>"`)

	return commitCmd
}

func commitRunE(_ *cobra.Command, _ []string) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	authors, err := client.Authors()
	if err != nil {
		log.Printf("could not load author list: %v", err)
		authors = []string{}
	}

	defaults := tui.CommitOption{
		Authors:    authors,
		All:        commitAll,
		Amend:      commitAmend,
		NoVerify:   commitNoVerify,
		Signoff:    commitSignoff,
		AllowEmpty: commitAllowEmpty,
		Author:     commitAuthor,
	}

	msg, opts, err := commit.FillOutForm(appConfig, defaults)
	if err != nil {
		return fmt.Errorf("failed to fill form: %w", err)
	}

	summary, err := client.Commit(msg, git.CommitOptions{
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

	printCommitSummary(summary)

	return nil
}

func printCommitSummary(s git.CommitSummary) {
	ref := s.Branch
	if s.IsRoot {
		ref += " (root-commit)"
	}

	fmt.Printf("[%s %s] %s\n", ref, s.ShortHash, s.Subject)

	if s.Files == 0 {
		return
	}

	fileWord := "files"
	if s.Files == 1 {
		fileWord = "file"
	}

	line := fmt.Sprintf(" %d %s changed", s.Files, fileWord)

	if s.Additions > 0 {
		word := "insertions"
		if s.Additions == 1 {
			word = "insertion"
		}

		line += fmt.Sprintf(", %d %s(+)", s.Additions, word)
	}

	if s.Deletions > 0 {
		word := "deletions"
		if s.Deletions == 1 {
			word = "deletion"
		}

		line += fmt.Sprintf(", %d %s(-)", s.Deletions, word)
	}

	fmt.Println(line)
}
```

- [ ] **Step 3: Update `cmd/issue.go`**

Replace `loadMessageConfig()` call with `appConfig`, update `getAllowedBranchType`, replace all viper reads with `appConfig` field accesses:

```go
package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	_ "github.com/piprim/git-zf/tracker/redmine" // registers redmine adapter
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

type issueStartFlags struct {
	trackerFirst bool
}

func getIssueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "issue",
		Short: "Manage issues",
		RunE:  issueRunE,
	}
	cmd.AddCommand(getIssueStartCmd())

	return cmd
}

func issueRunE(cmd *cobra.Command, args []string) error {
	var action string
	if err := huh.NewForm(tui.IssueActionSelect(&action)).Run(); err != nil {
		return fmt.Errorf("action select: %w", err)
	}

	switch action {
	case tui.IssueActionNameStart:
		return issueStartRunE(cmd, args)
	default:
		fmt.Println("Not yet implemented.")

		return nil
	}
}

func getIssueStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-cz.db.`,
		RunE: issueStartRunE,
	}
}

func issueStartRunE(cmd *cobra.Command, _ []string) error {
	return runIssueStart(cmd.Context(), issueStartFlags{trackerFirst: true})
}

// runIssueStart contains the full issue-start flow. trackerFirst=true for
// `issue start` (tracker pre-selected); false for `branch new` (manual pre-selected).
func runIssueStart(ctx context.Context, flags issueStartFlags) error {
	client, err := git.NewClient()
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}

	base := appConfig.Branch.Base
	if base == "" {
		base, err = client.DefaultBaseBranch()
		if err != nil {
			return fmt.Errorf("detect base branch: %w", err)
		}
	}

	allowedBranchTypes := getAllowedBranchType(appConfig.CommitTypes)
	if len(allowedBranchTypes) == 0 {
		return errors.New("config: no commit types found")
	}

	trackerCfg := appConfig.IssueTracker

	var issueID, title, branchType string
	var fromTracker bool
	var pickedIssue tracker.Issue
	var t tracker.Tracker

	if trackerCfg.Type != "" {
		t, err = tracker.New(trackerCfg)
		if err != nil {
			return fmt.Errorf("create tracker: %w", err)
		}

		var useTracker bool
		if err := huh.NewForm(tui.IssueTrackerToggle(&useTracker, flags.trackerFirst, trackerCfg.Type)).Run(); err != nil {
			return fmt.Errorf("tracker toggle: %w", err)
		}

		if useTracker {
			issues, listErr := t.ListIssues(ctx)
			if listErr != nil {
				if noteErr := huh.NewForm(tui.IssueTrackerError(listErr.Error())).Run(); noteErr != nil {
					return fmt.Errorf("error note: %w", noteErr)
				}
				// fallthrough to manual input
			} else if len(issues) == 0 {
				if noteErr := huh.NewForm(tui.IssueTrackerError("no open issues assigned to you")).Run(); noteErr != nil {
					return fmt.Errorf("error note: %w", noteErr)
				}
				// fallthrough to manual input
			} else {
				if err := huh.NewForm(tui.IssueTrackerPicker(issues, &pickedIssue, allowedBranchTypes, &branchType)).Run(); err != nil {
					return fmt.Errorf("tracker picker: %w", err)
				}

				issueID = pickedIssue.ID
				title = pickedIssue.Subject
				fromTracker = true
			}
		}
	}

	if !fromTracker {
		if err := huh.NewForm(tui.IssueInput(&issueID, &title, &branchType, allowedBranchTypes)).Run(); err != nil {
			return fmt.Errorf("issue form: %w", err)
		}
	}

	b, err := branch.New(issueID, branchType, title)
	if err != nil {
		return fmt.Errorf("assemble branch name: %w", err)
	}

	branchName := b.Name()

	var confirmed bool
	if err := huh.NewForm(tui.IssueConfirm(
		fmt.Sprintf("Create branch %q based on %q?", branchName, base), &confirmed,
	)).Run(); err != nil {
		return fmt.Errorf("confirm form: %w", err)
	}

	if !confirmed {
		fmt.Println("Aborted.")

		return nil
	}

	if err := client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	var tt *string
	if fromTracker {
		tt = &trackerCfg.Type
	}

	if err := persist(ctx, client, b, title, tt); err != nil {
		fmt.Fprintf(os.Stderr, "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Printf("Switched to new branch %q (based on %q)\n", branchName, base)

	if fromTracker {
		var updateStatus bool
		if err := huh.NewForm(tui.IssueUpdateStatusConfirm(
			pickedIssue.ID, trackerCfg.InProgressStatus, trackerCfg.Type, &updateStatus,
		)).Run(); err != nil {
			fmt.Fprintf(os.Stderr, "warning: status confirm form: %v\n", err)
		} else if updateStatus {
			if err := t.UpdateIssueStatus(ctx, pickedIssue.ID, trackerCfg.InProgressStatus); err != nil {
				fmt.Fprintf(os.Stderr, "warning: could not update tracker status: %v\n", err)
			}
		}
	}

	return nil
}

func getAllowedBranchType(types []config.CommitTypeOption) []string {
	allowedBranchTypes := make([]string, 0, len(types))
	for _, t := range types {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}

	return allowedBranchTypes
}

func persist(ctx context.Context, client *git.Client, b *branch.Branch, rawTitle string, trackerType *string) error {
	root, err := client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	s, err := store.Open(ctx, filepath.Join(root, ".git"))
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: rawTitle, StatusID: 1, TrackerType: trackerType},
		&store.Branch{UUID: b.ID(), Name: b.Name(), Type: b.Type(), StatusID: 1},
	); err != nil {
		return fmt.Errorf("insert issue with branch: %w", err)
	}

	return nil
}
```

- [ ] **Step 4: Run all tests**

```
go test ./...
```

Expected (all packages pass):
```
ok  github.com/piprim/git-zf/branch
ok  github.com/piprim/git-zf/cmd
ok  github.com/piprim/git-zf/commit
ok  github.com/piprim/git-zf/config
ok  github.com/piprim/git-zf/git
ok  github.com/piprim/git-zf/store
ok  github.com/piprim/git-zf/tracker
ok  github.com/piprim/git-zf/tracker/redmine
ok  github.com/piprim/git-zf/tui
```

- [ ] **Step 5: Build the binary to verify no linker issues**

```
go build -o /tmp/commitizen-go-test .
```

Expected: exits 0, binary written to `/tmp/commitizen-go-test`.

- [ ] **Step 6: Commit**

```bash
git add cmd/root.go cmd/commit.go cmd/issue.go
git commit -m "refactor(cmd): load config once in root; remove loadMessageConfig; use appConfig throughout"
```
