# TOML Configuration with Two-Layer Merge — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `.git-zf.json` with `.git-zf.toml`, support two-layer merge (global `~/.git-zf.toml` + per-clone `.git/.git-zf.toml`), and add a dynamic section picker to `git zf config init`.

**Architecture:** Viper loads the global config with `ReadInConfig`, then merges the repo config with `MergeInConfig` (using explicit `SetConfigFile` for each). `config.Load()` is unchanged; the merge happens in `initConfig`. All AppConfig structs gain `toml:` tags (kept alongside existing `json:` and `mapstructure:` tags so `show` output stays JSON).

**Tech Stack:** `github.com/pelletier/go-toml` v1 (already indirect dep → promoted), `github.com/charmbracelet/huh` (existing), `github.com/spf13/viper` v1.7.0 (existing).

---

## File Map

| File | Action | What changes |
|---|---|---|
| `config/default.json` | Delete | Replaced by TOML |
| `config/default.toml` | Create | TOML equivalent of default.json |
| `config/config.go` | Modify | `toml:` tags, embed default.toml, `DefaultTOML()`, `Load()` uses toml.Unmarshal, `configFileName` constant |
| `config/config_test.go` | Modify | Rename `TestDefaultJSON` → TOML, update `TestLoad_projects` fixture, add `TestLoad_twoFileMerge` |
| `cmd/root.go` | Modify | `configFileExt = "toml"`, two-phase `initConfig` |
| `tui/config.go` | Modify | Add `ConfigSectionPicker` |
| `cmd/config/init.go` | Modify | Split `writeDest` → `writeHomeDest` + `writeRepoDest`, add `configToSectionMap` |
| `cmd/config/init_test.go` | Modify | `.json` → `.toml` paths, `TestWriteDest_writesDefaultJSON` → `TestWriteHomeDest_writesDefaultTOML` |
| `cmd/config/show.go` | No change | JSON output preserved; local output structs keep their `json:` tags |
| `cmd/config/show_test.go` | No change | Passes unchanged |

---

## Task 1: Create `config/default.toml` and promote dependency

**Files:**
- Create: `config/default.toml`
- Modify: `go.mod` (promote `pelletier/go-toml` from indirect to direct)

- [ ] **Step 1: Create `config/default.toml`**

```toml
[[commit-types]]
name = "feat"
desc = "A new feature"

[[commit-types]]
name = "fix"
desc = "A bug fix"

[[commit-types]]
name = "docs"
desc = "Documentation only changes"

[[commit-types]]
name = "style"
desc = "Changes that do not affect the meaning of the code"

[[commit-types]]
name = "refactor"
desc = "A code change that neither fixes a bug nor adds a feature"

[[commit-types]]
name = "perf"
desc = "A code change that improves performance"

[[commit-types]]
name = "test"
desc = "Adding missing tests"

[[commit-types]]
name = "chore"
desc = "Changes to the build process or auxiliary tools"

[[commit-types]]
name = "revert"
desc = "Revert to a commit"

[[commit-types]]
name = "WIP"
desc = "Work in progress"

[commit-message]
template = "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"

[[commit-message.items]]
name = "scope"
desc = "Scope (users, db, poll\xe2\x80\xa6):"
form = "input"

[[commit-message.items]]
name = "subject"
desc = "Concise description. Imperative, lower case, no final dot:"
form = "input"
required = true

[[commit-message.items]]
name = "body"
desc = "Motivation for the change and contrast with previous behaviour:"
form = "multiline"

[[commit-message.items]]
name = "footer"
desc = "Breaking changes and referenced issues:"
form = "multiline"

[branch]
base = ""

[issue-tracker]
type = ""
url = ""
token = ""
```

- [ ] **Step 2: Promote `pelletier/go-toml` to a direct dependency**

```bash
mise exec -- go get github.com/pelletier/go-toml@v1.8.0
```

Expected: `go.mod` now lists `github.com/pelletier/go-toml v1.8.0` without `// indirect`.

- [ ] **Step 3: Verify build still compiles**

```bash
mise exec -- go build ./...
```

Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add config/default.toml go.mod go.sum
git commit -m "feat(config): add default.toml and promote go-toml to direct dep"
```

---

## Task 2: Add `toml:` tags + update `config/config.go`

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go` (write failing test first)

- [ ] **Step 1: Write the failing test for `DefaultTOML`**

In `config/config_test.go`, replace `TestDefaultJSON_isValidJSON` with:

```go
func TestDefaultTOML_isValidTOML(t *testing.T) {
    t.Parallel()

    b := config.DefaultTOML()
    if len(b) == 0 {
        t.Fatal("DefaultTOML returned empty bytes")
    }

    var v map[string]any
    if err := toml.Unmarshal(b, &v); err != nil {
        t.Fatalf("DefaultTOML is not valid TOML: %v", err)
    }

    if _, ok := v["commit-types"]; !ok {
        t.Error("DefaultTOML missing 'commit-types' key")
    }
}
```

Add import `toml "github.com/pelletier/go-toml"` to the test file imports.

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./config/... -run TestDefaultTOML_isValidTOML -v
```

Expected: FAIL — `config.DefaultTOML undefined`.

- [ ] **Step 3: Update `config/config.go`**

Replace the entire file with the updated version below. Key changes:
- `//go:embed default.json` → `//go:embed default.toml`
- `var defaultJSON []byte` → `var defaultTOML []byte`
- Add `toml:` tags to every exported struct field (alongside existing `json:` and `mapstructure:` tags)
- Add `toml:"-"` to `AppConfig.ProgName` and `CommitItem.Value`
- `configFileName = ".git-zf.toml"` (was `.git-zf.json`)
- `Load()` uses `toml.Unmarshal(defaultTOML, &cfg)`
- `DefaultJSON()` removed; `DefaultTOML()` added
- Import: replace `"encoding/json"` with `toml "github.com/pelletier/go-toml"`

```go
package config

import (
	_ "embed"
	"fmt"
	"path/filepath"

	"github.com/mitchellh/go-homedir"
	"github.com/mitchellh/mapstructure"
	toml "github.com/pelletier/go-toml"
	"github.com/piprim/git-zf/git"
	"github.com/spf13/viper"
)

const (
	progName       = "git-zf"
	configFileName = ".git-zf.toml"
)

//go:embed default.toml
var defaultTOML []byte

// CommitTypeOption is a single commit type entry (e.g. "feat", "fix").
type CommitTypeOption struct {
	Name string `json:"name" toml:"name" mapstructure:"name"`
	Desc string `json:"desc" toml:"desc" mapstructure:"desc"`
}

// CommitItemOption is a selectable option within a CommitItem select field.
type CommitItemOption struct {
	Name string `json:"name" toml:"name" mapstructure:"name"`
	Desc string `json:"desc" toml:"desc" mapstructure:"desc"`
}

// CommitItem describes one field in the commit message form.
// Value is written by the form after the user submits.
type CommitItem struct {
	Name     string             `json:"name"     toml:"name"     mapstructure:"name"`
	Desc     string             `json:"desc"     toml:"desc"     mapstructure:"desc"`
	Form     string             `json:"form"     toml:"form"     mapstructure:"form"`
	Required bool               `json:"required" toml:"required" mapstructure:"required"`
	Options  []CommitItemOption `json:"options"  toml:"options"  mapstructure:"options"`
	Value    string             `json:"-"        toml:"-"        mapstructure:"-"`
}

// CommitMessageConfig holds the ordered list of form fields and the Go template
// used to assemble the commit message.
type CommitMessageConfig struct {
	Items    []CommitItem `json:"items"    toml:"items"    mapstructure:"items"`
	Template string       `json:"template" toml:"template" mapstructure:"template"`
}

// BranchConfig holds branch-related settings.
// Base is the branch new branches are cut from; empty means auto-detect.
type BranchConfig struct {
	Base string `json:"base" toml:"base" mapstructure:"base"`
}

// IssueTrackerConfig holds connection parameters for one tracker instance.
// Never log values of this type — Token is a secret.
type IssueTrackerConfig struct {
	Type     string   `json:"type"     toml:"type"     mapstructure:"type"`
	URL      string   `json:"url"      toml:"url"      mapstructure:"url"`
	Token    string   `json:"token"    toml:"token"    mapstructure:"token"`
	Projects []string `json:"projects" toml:"projects" mapstructure:"projects"`
}

// AppConfig is the top-level configuration for the application.
type AppConfig struct {
	ProgName      string              `toml:"-"`
	CommitTypes   []CommitTypeOption  `json:"commit-types"   toml:"commit-types"   mapstructure:"commit-types"`
	CommitMessage CommitMessageConfig `json:"commit-message" toml:"commit-message" mapstructure:"commit-message"`
	Branch        BranchConfig        `json:"branch"         toml:"branch"         mapstructure:"branch"`
	IssueTracker  IssueTrackerConfig  `json:"issue-tracker"  toml:"issue-tracker"  mapstructure:"issue-tracker"`
}

// Load parses the embedded default.toml then overlays any values present in the
// global viper instance. Each config section is handled individually so that a
// partial override (e.g. only commit-message.items) preserves unset defaults.
// viper.Sub is avoided because it silently returns nil for array-typed keys.
func Load() (*AppConfig, error) {
	var cfg AppConfig
	if err := toml.Unmarshal(defaultTOML, &cfg); err != nil {
		return nil, fmt.Errorf("parse default config: %w", err)
	}

	// zeroSlice zeroes the target before decoding so that a user-supplied slice
	// fully replaces the default instead of being appended to it.
	zeroSlice := func(dc *mapstructure.DecoderConfig) { dc.ZeroFields = true }

	if viper.IsSet("commit-types") {
		if err := viper.UnmarshalKey("commit-types", &cfg.CommitTypes, zeroSlice); err != nil {
			return nil, fmt.Errorf("unmarshal commit-types: %w", err)
		}
	}

	if viper.IsSet("commit-message.items") {
		if err := viper.UnmarshalKey("commit-message.items", &cfg.CommitMessage.Items, zeroSlice); err != nil {
			return nil, fmt.Errorf("unmarshal commit-message.items: %w", err)
		}
	}

	if viper.IsSet("commit-message.template") {
		cfg.CommitMessage.Template = viper.GetString("commit-message.template")
	}

	if viper.IsSet("branch.base") {
		cfg.Branch.Base = viper.GetString("branch.base")
	}

	// issue-tracker is decoded without zeroSlice: Projects defaults to nil, so
	// a user-supplied slice fully replaces it without merge ambiguity.
	if viper.IsSet("issue-tracker") {
		if err := viper.UnmarshalKey("issue-tracker", &cfg.IssueTracker); err != nil {
			return nil, fmt.Errorf("unmarshal issue-tracker: %w", err)
		}
	}

	cfg.ProgName = progName

	return &cfg, nil
}

// DefaultTOML returns the raw embedded default configuration bytes.
func DefaultTOML() []byte {
	return defaultTOML
}

// HomeDir returns the configuration directory path in the user home directory
// where live the config file.
func HomeDir() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	return home, nil
}

// RepoDir return the configuration directory path in the git repository of the
// project.
func RepoDir() string {
	client, err := git.NewClient()
	if err != nil {
		return ""
	}

	root, err := client.WorkingTreeRoot()
	if err != nil || root == "" {
		return ""
	}

	return filepath.Join(root, ".git")
}

// HomePath returns the configuration file path in the user home directory.
func HomePath() (string, error) {
	home, err := HomeDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, configFileName), nil
}

// RepoPath returns the configuration file path in the git repository of the project.
func RepoPath() string {
	repoDir := RepoDir()
	if repoDir == "" {
		return ""
	}

	return filepath.Join(repoDir, configFileName)
}
```

- [ ] **Step 4: Run the failing test — expect it now passes**

```bash
mise exec -- go test ./config/... -run TestDefaultTOML_isValidTOML -v
```

Expected: PASS.

- [ ] **Step 5: Run all config tests**

```bash
mise exec -- go test ./config/... -v
```

Expected: PASS (existing `TestLoad_*` tests still use `viper.Set()` — they are format-agnostic).

- [ ] **Step 6: Run linter**

```bash
golangci-lint run ./config/...
```

Expected: no violations.

- [ ] **Step 7: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): switch embedded config to TOML, add toml struct tags"
```

---

## Task 3: Update `config/config_test.go` — TOML fixtures and two-file merge

**Files:**
- Modify: `config/config_test.go`

- [ ] **Step 1: Write the failing two-file merge test**

Add at the bottom of `config/config_test.go`:

```go
func TestLoad_twoFileMerge(t *testing.T) {
	// Not parallel — modifies global viper state.
	viper.Reset()
	defer viper.Reset()

	globalTOML := []byte(`
[[commit-types]]
name = "custom"
desc = "Custom type"
`)

	localTOML := []byte(`
[issue-tracker]
type = "redmine"
url = "https://redmine.example.com"
token = "tok"
`)

	globalPath := filepath.Join(t.TempDir(), ".git-zf.toml")
	localPath := filepath.Join(t.TempDir(), ".git-zf.toml")

	if err := os.WriteFile(globalPath, globalTOML, 0o600); err != nil {
		t.Fatalf("write global: %v", err)
	}

	if err := os.WriteFile(localPath, localTOML, 0o600); err != nil {
		t.Fatalf("write local: %v", err)
	}

	viper.SetConfigType("toml")
	viper.SetConfigFile(globalPath)

	if err := viper.ReadInConfig(); err != nil {
		t.Fatalf("ReadInConfig global: %v", err)
	}

	viper.SetConfigFile(localPath)

	if err := viper.MergeInConfig(); err != nil {
		t.Fatalf("MergeInConfig local: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// commit-types from global override (replaces built-in defaults).
	if len(cfg.CommitTypes) != 1 {
		t.Errorf("CommitTypes len = %d, want 1", len(cfg.CommitTypes))
	}

	if cfg.CommitTypes[0].Name != "custom" {
		t.Errorf("CommitTypes[0].Name = %q, want %q", cfg.CommitTypes[0].Name, "custom")
	}

	// issue-tracker from local.
	if cfg.IssueTracker.Type != "redmine" {
		t.Errorf("IssueTracker.Type = %q, want %q", cfg.IssueTracker.Type, "redmine")
	}

	if cfg.IssueTracker.URL != "https://redmine.example.com" {
		t.Errorf("IssueTracker.URL = %q, want %q", cfg.IssueTracker.URL, "https://redmine.example.com")
	}

	// template preserved from built-in default (neither file sets it).
	if cfg.CommitMessage.Template == "" {
		t.Error("CommitMessage.Template should be preserved from built-in default")
	}
}
```

Also update `TestLoad_projects` to use a TOML fixture instead of JSON. Replace its `blob` constant:

```go
// old:
const blob = `{
    "issue-tracker": {
        "type": "github",
        ...
    }
}`

// new:
const blob = `
[issue-tracker]
type = "github"
url = "https://api.github.com"
token = "x"
projects = ["a/b", "c/d"]
`
```

Also update `v.SetConfigFile(cfgPath)` — change the file name in the temp path from `.git-zf.json` to `.git-zf.toml`:

```go
cfgPath := filepath.Join(dir, ".git-zf.toml")
```

And add `v.SetConfigType("toml")` before `v.ReadInConfig()`:

```go
v := viper.New()
v.SetConfigType("toml")
v.SetConfigFile(cfgPath)
if err := v.ReadInConfig(); err != nil {
    t.Fatalf("read cfg: %v", err)
}
```

Add required imports to `config_test.go`:
- `"os"` (for `os.WriteFile` in `TestLoad_twoFileMerge`)
- `toml "github.com/pelletier/go-toml"` (for `TestDefaultTOML_isValidTOML`)

Remove import `"encoding/json"` (no longer needed).

- [ ] **Step 2: Run new test to confirm it fails before the two-phase load is wired**

```bash
mise exec -- go test ./config/... -run TestLoad_twoFileMerge -v
```

Expected: PASS — the test already passes because it sets up Viper manually with the two-phase load. This test validates the merge behaviour, not `initConfig`.

- [ ] **Step 3: Run all config tests**

```bash
mise exec -- go test ./config/... -v
```

Expected: all PASS.

- [ ] **Step 4: Run linter**

```bash
golangci-lint run ./config/...
```

Expected: no violations.

- [ ] **Step 5: Commit**

```bash
git add config/config_test.go
git commit -m "test(config): update fixtures to TOML, add two-file merge test"
```

---

## Task 4: Update `cmd/root.go` — two-phase Viper load

**Files:**
- Modify: `cmd/root.go`

- [ ] **Step 1: Update `cmd/root.go`**

Change `configFileExt` and refactor `initConfig` to do a two-phase load. Replace the relevant constants and function:

```go
const (
	configFileName = ".git-zf"
	configFileExt  = "toml" // was "json"
)
```

Replace `initConfig` body (keep signature and surrounding code unchanged):

```go
func initConfig() error {
	if !isDebug {
		log.SetOutput(io.Discard)
	} else {
		f, err := os.OpenFile("debug.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			return fmt.Errorf("failed to open debug.log: %w", err)
		}

		log.SetFlags(log.Lshortfile | log.LstdFlags)
		log.SetOutput(f)
	}

	viper.SetConfigType(configFileExt)

	// Phase 1: load global config from home directory.
	homePath, err := config.HomePath()
	if err != nil {
		return fmt.Errorf("get home config path: %w", err)
	}

	viper.SetConfigFile(homePath)

	if err := viper.ReadInConfig(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.Printf("read global config %s: %v", homePath, err)
		} else {
			log.Println("no global config file found")
		}
	} else {
		log.Printf("loaded global config: %s", homePath)
	}

	// Phase 2: merge repo-local config on top (repo values win).
	if repoPath := config.RepoPath(); repoPath != "" {
		viper.SetConfigFile(repoPath)

		if err := viper.MergeInConfig(); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.Printf("merge repo config %s: %v", repoPath, err)
			} else {
				log.Println("no repo config file found")
			}
		} else {
			log.Printf("merged repo config: %s", repoPath)
		}
	}

	appConfig, err = config.Load()
	if err != nil {
		return fmt.Errorf("failed to load app config: %w", err)
	}

	return nil
}
```

Add `"errors"` to the import block in `cmd/root.go` if not already present.

- [ ] **Step 2: Build**

```bash
mise exec -- go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
mise exec -- go test ./... -v
```

Expected: all PASS.

- [ ] **Step 4: Run linter**

```bash
golangci-lint run ./cmd/...
```

Expected: no violations.

- [ ] **Step 5: Commit**

```bash
git add cmd/root.go
git commit -m "feat(config): two-phase Viper load — global then merge repo"
```

---

## Task 5: Add `tui.ConfigSectionPicker`

**Files:**
- Modify: `tui/config.go`

- [ ] **Step 1: Write a test verifying `ConfigSectionPicker` returns a group**

Create `tui/config_test.go`:

```go
package tui_test

import (
	"testing"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
)

func TestConfigSectionPicker_returnsGroup(t *testing.T) {
	t.Parallel()

	keys := []string{"branch", "commit-types", "issue-tracker"}
	var selected []string

	g := tui.ConfigSectionPicker(keys, &selected)

	if g == nil {
		t.Fatal("ConfigSectionPicker returned nil")
	}

	// Verify the group is a valid *huh.Group by running a type assertion.
	if _, ok := any(g).(*huh.Group); !ok {
		t.Errorf("expected *huh.Group, got %T", g)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./tui/... -run TestConfigSectionPicker_returnsGroup -v
```

Expected: FAIL — `tui.ConfigSectionPicker undefined`.

- [ ] **Step 3: Add `ConfigSectionPicker` to `tui/config.go`**

```go
// ConfigSectionPicker presents a multi-select form for choosing which config
// sections to write to the repo config file. keys is the dynamic list of
// section names derived from the marshalled config map. selected receives the
// chosen keys.
func ConfigSectionPicker(keys []string, selected *[]string) *huh.Group {
	opts := make([]huh.Option[string], len(keys))
	for i, k := range keys {
		opts[i] = huh.NewOption(k, k)
	}

	return huh.NewGroup(
		huh.NewMultiSelect[string]().
			Title("Which sections to include in the repo config?").
			Options(opts...).
			Value(selected),
	)
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
mise exec -- go test ./tui/... -run TestConfigSectionPicker_returnsGroup -v
```

Expected: PASS.

- [ ] **Step 5: Run linter**

```bash
golangci-lint run ./tui/...
```

Expected: no violations.

- [ ] **Step 6: Commit**

```bash
git add tui/config.go tui/config_test.go
git commit -m "feat(tui): add ConfigSectionPicker for repo config init"
```

---

## Task 6: Update `cmd/config/init.go` — section picker and TOML write

**Files:**
- Modify: `cmd/config/init.go`

- [ ] **Step 1: Write failing test for `writeHomeDest`**

In `cmd/config/init_test.go`, add:

```go
func TestWriteHomeDest_writesDefaultTOML(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".git-zf.toml")

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := writeHomeDest(cmd, dest); err != nil {
		t.Fatalf("writeHomeDest: %v", err)
	}

	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}

	if !bytes.Equal(content, appconfig.DefaultTOML()) {
		t.Errorf("file content does not match DefaultTOML")
	}

	if !strings.Contains(buf.String(), dest) {
		t.Errorf("expected path %q in output, got: %s", dest, buf.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./cmd/config/... -run TestWriteHomeDest_writesDefaultTOML -v
```

Expected: FAIL — `writeHomeDest undefined`.

- [ ] **Step 3: Rewrite `cmd/config/init.go`**

Replace the file with the updated version. Key changes:
- `writeDest` split into `writeHomeDest(cmd, dest)` and `writeRepoDest(cmd, dest, cfg)`
- `initRunE` dispatches on `dest == homePath`
- New `configToSectionMap(cfg)` helper marshals `AppConfig` → `map[string]any` via TOML round-trip
- `writeRepoDest` builds dynamic section keys, shows `ConfigSectionPicker`, writes filtered TOML

```go
package config

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"

	"github.com/charmbracelet/huh"
	toml "github.com/pelletier/go-toml"
	appconfig "github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

func (c Config) getInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write a default config file to home or repo directory",
		RunE:  c.initRunE,
	}
}

func (c Config) initRunE(cmd *cobra.Command, _ []string) error {
	homePath, err := appconfig.HomePath()
	if err != nil {
		return fmt.Errorf("failed to load home config path: %w", err)
	}

	repoPath := appconfig.RepoPath()

	dest, err := pickDest(homePath, repoPath)
	if err != nil {
		return err
	}

	if dest == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

		return nil
	}

	if fileExists(dest) {
		confirmed, err := confirmOverwrite(dest)
		if err != nil {
			return err
		}

		if !confirmed {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

			return nil
		}
	}

	if dest == homePath {
		return writeHomeDest(cmd, dest)
	}

	return writeRepoDest(cmd, dest, c.appConfig)
}

func pickDest(homePath, repoPath string) (string, error) {
	homeExists := fileExists(homePath)
	insideRepo := repoPath != ""

	if !insideRepo && !homeExists {
		return homePath, nil
	}

	repoExists := insideRepo && fileExists(repoPath)
	opts := buildPickerOpts(homePath, repoPath, homeExists, repoExists)

	var dest string
	if err := huh.NewForm(tui.ConfigDestPicker(opts, &dest)).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return "", nil
		}

		return "", fmt.Errorf("destination picker: %w", err)
	}

	return dest, nil
}

func buildPickerOpts(homePath, repoPath string, homeExists, repoExists bool) []huh.Option[string] {
	homeLabel := fmt.Sprintf("Home (%s)", homePath)
	if homeExists {
		homeLabel += " [overwrite]"
	}

	opts := []huh.Option[string]{huh.NewOption(homeLabel, homePath)}

	if repoPath == "" {
		return opts
	}

	repoLabel := fmt.Sprintf("This repo (%s)", repoPath)
	if repoExists {
		repoLabel += " [overwrite]"
	} else {
		repoLabel += " [takes precedence over home]"
	}

	return append(opts, huh.NewOption(repoLabel, repoPath))
}

func confirmOverwrite(path string) (bool, error) {
	var confirmed bool
	err := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("%s already exists. Overwrite?", path)).
			Value(&confirmed),
	)).Run()

	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return false, nil
		}

		return false, fmt.Errorf("confirm overwrite: %w", err)
	}

	return confirmed, nil
}

// writeHomeDest writes the full default TOML config to dest.
func writeHomeDest(cmd *cobra.Command, dest string) error {
	if err := os.WriteFile(dest, appconfig.DefaultTOML(), 0o600); err != nil {
		return fmt.Errorf("write config to %s: %w", dest, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", dest)

	return nil
}

// writeRepoDest shows a dynamic section picker populated from the current
// effective config, then writes only the selected sections as TOML to dest.
func writeRepoDest(cmd *cobra.Command, dest string, cfg *appconfig.AppConfig) error {
	sections, err := configToSectionMap(cfg)
	if err != nil {
		return err
	}

	keys := slices.Sorted(maps.Keys(sections))

	var selected []string
	if err := huh.NewForm(tui.ConfigSectionPicker(keys, &selected)).Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

			return nil
		}

		return fmt.Errorf("section picker: %w", err)
	}

	if len(selected) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "No sections selected. Nothing written.")

		return nil
	}

	filtered := make(map[string]any, len(selected))
	for _, k := range selected {
		filtered[k] = sections[k]
	}

	b, err := toml.Marshal(filtered)
	if err != nil {
		return fmt.Errorf("marshal repo config: %w", err)
	}

	if err := os.WriteFile(dest, b, 0o600); err != nil {
		return fmt.Errorf("write config to %s: %w", dest, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", dest)

	return nil
}

// configToSectionMap marshals cfg to TOML then unmarshals into a map so the
// section picker iterates dynamic keys derived from toml struct tags, not
// hardcoded names.
func configToSectionMap(cfg *appconfig.AppConfig) (map[string]any, error) {
	b, err := toml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config sections: %w", err)
	}

	m := make(map[string]any)
	if err := toml.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("unmarshal config sections: %w", err)
	}

	return m, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
```

- [ ] **Step 4: Run the failing test — expect it now passes**

```bash
mise exec -- go test ./cmd/config/... -run TestWriteHomeDest_writesDefaultTOML -v
```

Expected: PASS.

- [ ] **Step 5: Run linter**

```bash
golangci-lint run ./cmd/config/...
```

Expected: no violations.

- [ ] **Step 6: Commit**

```bash
git add cmd/config/init.go
git commit -m "feat(config): add section picker for repo init, split writeHomeDest/writeRepoDest"
```

---

## Task 7: Update `cmd/config/init_test.go` — fix paths and function names

**Files:**
- Modify: `cmd/config/init_test.go`

- [ ] **Step 1: Update all `.git-zf.json` references to `.git-zf.toml`**

Replace every occurrence of `.git-zf.json` with `.git-zf.toml` in `init_test.go`.

Affected lines (current content → new content):

```go
// TestBuildPickerOpts_homeOnlyNoExist
opts := buildPickerOpts("/home/user/.git-zf.toml", "", false, false)

// TestBuildPickerOpts_homeExistsWithRepo
opts := buildPickerOpts("/home/user/.git-zf.toml", "/repo/.git-zf.toml", true, false)

// TestBuildPickerOpts_bothExist
opts := buildPickerOpts("/home/user/.git-zf.toml", "/repo/.git-zf.toml", true, true)

// TestPickDest_autoSelectsHomeWhenOutsideRepoNoFile
homePath := filepath.Join(t.TempDir(), ".git-zf.toml")

// TestConfirmOverwrite_returnsFalseOnAbort
dest := filepath.Join(dir, "nonexistent.toml")

// TestFileExists
existing := filepath.Join(dir, "exists.toml")
```

- [ ] **Step 2: Replace `TestWriteDest_writesDefaultJSON` with updated test**

Remove `TestWriteDest_writesDefaultJSON` entirely (the new `TestWriteHomeDest_writesDefaultTOML` added in Task 6 covers it).

- [ ] **Step 3: Run all cmd/config tests**

```bash
mise exec -- go test ./cmd/config/... -v
```

Expected: all PASS.

- [ ] **Step 4: Run linter**

```bash
golangci-lint run ./cmd/config/...
```

Expected: no violations.

- [ ] **Step 5: Run full test suite**

```bash
mise exec -- go test ./... -v
```

Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/config/init_test.go
git commit -m "test(config): update init tests for TOML format"
```

---

## Self-Review

### Spec coverage

| Spec requirement | Task |
|---|---|
| `.git-zf.json` → `.git-zf.toml` | Task 1, 2, 4 |
| Global config `~/.git-zf.toml` | Task 4 |
| Local config `.git/.git-zf.toml` | Task 4 |
| Two-layer merge (global ← local) | Task 4, Task 3 (test) |
| `config init` home → full default | Task 6 (`writeHomeDest`) |
| `config init` repo → dynamic section picker | Task 5, 6 |
| Section picker iterates map keys (not hardcoded) | Task 6 (`configToSectionMap`) |
| Pre-populated values from effective config | Task 6 (`writeRepoDest` uses `c.appConfig`) |
| Hard cutover, no JSON fallback | Task 2 (remove `DefaultJSON`), Task 4 (no JSON paths) |

### Notes

- `viper.ConfigFileUsed()` after the two-phase load returns the repo path (last `SetConfigFile` call), even if the repo file wasn't found. The `show` command displays this path — minor UX roughness, out of scope.
- `show.go` output format remains JSON (not in spec). The `json:` tags on AppConfig structs are preserved alongside new `toml:` tags.
- `config/default.json` must be manually deleted after Task 1 (the embed directive changes to `default.toml`, so the JSON file becomes dead).
