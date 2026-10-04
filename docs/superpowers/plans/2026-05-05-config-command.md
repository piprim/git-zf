# Config Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `git zf config show` (print active file + effective JSON) and `git zf config init` (write default config to a chosen path via TUI) as a new `cmd/config` package wired into the root command.

**Architecture:** New `cmd/config/` package — `Config` struct holds `*config.AppConfig`, mirrors the existing `cmd/issue/`, `cmd/branch/` pattern. The `config` package gains one exported helper `DefaultJSON()`. A new `tui/config.go` holds the destination picker. `cmd/root.go` wires the new package in.

**Tech Stack:** Go, Cobra, huh (TUI), go-homedir, encoding/json, viper (read-only), os, path/filepath.

---

## File structure

| Action | File | Responsibility |
|--------|------|----------------|
| Modify | `config/config.go` | Add `DefaultJSON() []byte` |
| Create | `tui/config.go` | `ConfigDestPicker` huh form |
| Create | `cmd/config/config.go` | `Config` struct, `New`, `GetRootCmd` |
| Create | `cmd/config/show.go` | `showRunE`, `toConfigOutput`, output structs |
| Create | `cmd/config/show_test.go` | Tests for show logic |
| Create | `cmd/config/init.go` | `initRunE`, `pickDest`, `buildPickerOpts`, `writeDest`, `fileExists` |
| Create | `cmd/config/init_test.go` | Tests for init logic |
| Modify | `cmd/root.go` | Wire `cmd/config` |

---

## Task 1: `config.DefaultJSON()` + test

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

- [ ] **Step 1: Write the failing test**

Add to `config/config_test.go`:

```go
func TestDefaultJSON_isValidJSON(t *testing.T) {
	b := config.DefaultJSON()
	if len(b) == 0 {
		t.Fatal("DefaultJSON returned empty bytes")
	}

	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatalf("DefaultJSON is not valid JSON: %v", err)
	}

	if _, ok := v["commit-types"]; !ok {
		t.Error("DefaultJSON missing 'commit-types' key")
	}
}
```

Add `"encoding/json"` to the import block in `config/config_test.go`.

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./config/... -run TestDefaultJSON -v
```

Expected: `FAIL` — `config.DefaultJSON` undefined.

- [ ] **Step 3: Add `DefaultJSON` to `config/config.go`**

Add after the `Load` function:

```go
// DefaultJSON returns the raw embedded default configuration bytes.
func DefaultJSON() []byte {
	return defaultJSON
}
```

- [ ] **Step 4: Run test to verify it passes**

```bash
mise exec -- go test ./config/... -run TestDefaultJSON -v
```

Expected: `PASS`.

- [ ] **Step 5: Run all config tests**

```bash
mise exec -- go test ./config/... -v
```

Expected: all existing tests still pass.

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): export DefaultJSON helper"
```

---

## Task 2: `tui.ConfigDestPicker` + test

**Files:**
- Create: `tui/config.go`

> Note: There is no dedicated test file for individual huh group constructors in this codebase (they require a real TTY). The function is tested indirectly via `cmd/config` integration. Skip a unit test here and document why.

- [ ] **Step 1: Create `tui/config.go`**

```go
package tui

import "github.com/charmbracelet/huh"

// ConfigDestPicker presents a select form for choosing where to write the config file.
// opts is built by the caller based on which paths already exist.
func ConfigDestPicker(opts []huh.Option[string], dest *string) *huh.Group {
	return huh.NewGroup(
		huh.NewSelect[string]().
			Title("Write config to:").
			Options(opts...).
			Value(dest),
	)
}
```

- [ ] **Step 2: Verify it compiles**

```bash
mise exec -- go build ./tui/...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add tui/config.go
git commit -m "feat(tui): add ConfigDestPicker for config init"
```

---

## Task 3: `cmd/config/config.go` + `show.go` + tests

**Files:**
- Create: `cmd/config/config.go`
- Create: `cmd/config/show.go`
- Create: `cmd/config/show_test.go`

- [ ] **Step 1: Write failing tests in `cmd/config/show_test.go`**

```go
package config

import (
	"bytes"
	"strings"
	"testing"

	appconfig "github.com/piprim/git-zf/config"
)

func TestToConfigOutput_masksToken(t *testing.T) {
	cfg := &appconfig.AppConfig{
		IssueTracker: appconfig.IssueTrackerConfig{
			Type:  "plane",
			URL:   "https://plane.example.com",
			Token: "super-secret",
		},
	}

	out := toConfigOutput(cfg)

	if out.IssueTracker.Token != "***" {
		t.Errorf("token = %q, want %q", out.IssueTracker.Token, "***")
	}
	if out.IssueTracker.Type != "plane" {
		t.Errorf("type = %q, want %q", out.IssueTracker.Type, "plane")
	}
}

func TestToConfigOutput_emptyTokenUnchanged(t *testing.T) {
	cfg := &appconfig.AppConfig{}

	out := toConfigOutput(cfg)

	if out.IssueTracker.Token != "" {
		t.Errorf("empty token should stay empty, got %q", out.IssueTracker.Token)
	}
}

func TestToConfigOutput_excludesProgName(t *testing.T) {
	cfg := &appconfig.AppConfig{ProgName: "git-zf"}

	out := toConfigOutput(cfg)

	b, err := marshalConfig(out)
	if err != nil {
		t.Fatalf("marshalConfig: %v", err)
	}

	if strings.Contains(string(b), "ProgName") || strings.Contains(string(b), "git-zf") {
		t.Errorf("output must not contain ProgName, got: %s", b)
	}
}

func TestShowRunE_printsConfigFileLine(t *testing.T) {
	cfg := &appconfig.AppConfig{
		CommitTypes: []appconfig.CommitTypeOption{{Name: "feat", Desc: "A new feature"}},
	}
	c := Config{appConfig: cfg}

	var buf bytes.Buffer
	cmd := c.getShowCmd()
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("showRunE: %v", err)
	}

	out := buf.String()
	// viper.ConfigFileUsed() returns "" in tests — expect the "no config file" line.
	if !strings.Contains(out, "no config file found") {
		t.Errorf("expected 'no config file found' in output, got:\n%s", out)
	}
	if !strings.Contains(out, `"commit-types"`) {
		t.Errorf("expected commit-types in JSON output, got:\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./cmd/config/... -v
```

Expected: `FAIL` — package does not exist yet.

- [ ] **Step 3: Create `cmd/config/config.go`**

```go
package config

import (
	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

// Config holds shared state for the config command group.
type Config struct {
	appConfig *appconfig.AppConfig
}

// New creates a Config command handler.
func New(appConfig *appconfig.AppConfig) Config {
	return Config{appConfig: appConfig}
}

// GetRootCmd returns the root cobra command for the config group.
func (c Config) GetRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage git-zf configuration",
	}
	cmd.AddCommand(c.getShowCmd(), c.getInitCmd())

	return cmd
}
```

- [ ] **Step 4: Create `cmd/config/show.go`**

```go
package config

import (
	"encoding/json"
	"fmt"

	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

type issueTrackerOutput struct {
	Type  string `json:"type"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

type configOutput struct {
	CommitTypes   []appconfig.CommitTypeOption  `json:"commit-types"`
	CommitMessage appconfig.CommitMessageConfig `json:"commit-message"`
	Branch        appconfig.BranchConfig        `json:"branch"`
	IssueTracker  issueTrackerOutput            `json:"issue-tracker"`
}

func (c Config) getShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Show active config file path and effective configuration",
		RunE:  c.showRunE,
	}
}

func (c Config) showRunE(cmd *cobra.Command, _ []string) error {
	path := viper.ConfigFileUsed()
	if path == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Config file: no config file found (built-in defaults apply)")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Config file: %s\n", path)
	}

	fmt.Fprintln(cmd.OutOrStdout())

	out := toConfigOutput(c.appConfig)

	b, err := marshalConfig(out)
	if err != nil {
		return err
	}

	fmt.Fprintln(cmd.OutOrStdout(), string(b))

	return nil
}

func toConfigOutput(cfg *appconfig.AppConfig) configOutput {
	token := cfg.IssueTracker.Token
	if token != "" {
		token = "***"
	}

	return configOutput{
		CommitTypes:   cfg.CommitTypes,
		CommitMessage: cfg.CommitMessage,
		Branch:        cfg.Branch,
		IssueTracker: issueTrackerOutput{
			Type:  cfg.IssueTracker.Type,
			URL:   cfg.IssueTracker.URL,
			Token: token,
		},
	}
}

func marshalConfig(out configOutput) ([]byte, error) {
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}

	return b, nil
}
```

- [ ] **Step 5: Run tests to verify they pass**

```bash
mise exec -- go test ./cmd/config/... -run "TestToConfig|TestShowRunE" -v
```

Expected: all 4 tests `PASS`.

- [ ] **Step 6: Commit**

```bash
git add cmd/config/config.go cmd/config/show.go cmd/config/show_test.go
git commit -m "feat(cmd/config): add config show subcommand"
```

---

## Task 4: `cmd/config/init.go` + tests

**Files:**
- Create: `cmd/config/init.go`
- Create: `cmd/config/init_test.go`

- [ ] **Step 1: Write failing tests in `cmd/config/init_test.go`**

```go
package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/huh"
	appconfig "github.com/piprim/git-zf/config"
	"github.com/spf13/cobra"
)

func TestBuildPickerOpts_homeOnlyNoExist(t *testing.T) {
	opts := buildPickerOpts("/home/user/.git-zf.json", "", false, false)

	if len(opts) != 1 {
		t.Fatalf("expected 1 option, got %d", len(opts))
	}
}

func TestBuildPickerOpts_homeExistsWithRepo(t *testing.T) {
	// homeExists=true, repoExists=false → home has [overwrite], repo has precedence note.
	opts := buildPickerOpts("/home/user/.git-zf.json", "/repo/.git-zf.json", true, false)

	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}

	if !strings.Contains(opts[0].Key, "[overwrite]") {
		t.Errorf("home option label %q missing [overwrite]", opts[0].Key)
	}

	if !strings.Contains(opts[1].Key, "takes precedence") {
		t.Errorf("repo option label %q missing precedence note", opts[1].Key)
	}
}

func TestBuildPickerOpts_bothExist(t *testing.T) {
	// homeExists=true, repoExists=true → both have [overwrite].
	opts := buildPickerOpts("/home/user/.git-zf.json", "/repo/.git-zf.json", true, true)

	if len(opts) != 2 {
		t.Fatalf("expected 2 options, got %d", len(opts))
	}

	if !strings.Contains(opts[0].Key, "[overwrite]") {
		t.Errorf("home option label %q missing [overwrite]", opts[0].Key)
	}

	if !strings.Contains(opts[1].Key, "[overwrite]") {
		t.Errorf("repo option label %q missing [overwrite]", opts[1].Key)
	}
}

func TestWriteDest_writesDefaultJSON(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, ".git-zf.json")

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)

	if err := writeDest(cmd, dest); err != nil {
		t.Fatalf("writeDest: %v", err)
	}

	content, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read written file: %v", err)
	}

	if !bytes.Equal(content, appconfig.DefaultJSON()) {
		t.Errorf("file content does not match DefaultJSON")
	}

	if !strings.Contains(buf.String(), dest) {
		t.Errorf("expected path %q in output, got: %s", dest, buf.String())
	}
}

func TestPickDest_autoSelectsHomeWhenOutsideRepoNoFile(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), ".git-zf.json")
	// repoPath="" simulates being outside a git repo; homePath does not exist.
	dest, err := pickDest(homePath, "")
	if err != nil {
		t.Fatalf("pickDest: %v", err)
	}

	if dest != homePath {
		t.Errorf("dest = %q, want %q", dest, homePath)
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "exists.json")
	if err := os.WriteFile(existing, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	if !fileExists(existing) {
		t.Error("fileExists returned false for existing file")
	}
	if fileExists(filepath.Join(dir, "missing.json")) {
		t.Error("fileExists returned true for missing file")
	}
}

// Compile-time check: ErrUserAborted is used in init.go.
var _ = huh.ErrUserAborted
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./cmd/config/... -run "TestBuild|TestWrite|TestPick|TestFile" -v
```

Expected: `FAIL` — functions not defined yet.

- [ ] **Step 3: Create `cmd/config/init.go`**

```go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/charmbracelet/huh"
	homedir "github.com/mitchellh/go-homedir"
	appconfig "github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

const configFileName = ".git-zf.json"

func (c Config) getInitCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Write a default config file to home or repo directory",
		RunE:  c.initRunE,
	}
}

func (c Config) initRunE(cmd *cobra.Command, _ []string) error {
	homePath, err := resolveHomePath()
	if err != nil {
		return err
	}

	repoPath := resolveRepoPath()

	dest, err := pickDest(homePath, repoPath)
	if err != nil {
		return err
	}

	if dest == "" {
		fmt.Fprintln(cmd.OutOrStdout(), "Aborted.")

		return nil
	}

	return writeDest(cmd, dest)
}

func resolveHomePath() (string, error) {
	home, err := homedir.Dir()
	if err != nil {
		return "", fmt.Errorf("get home dir: %w", err)
	}

	return filepath.Join(home, configFileName), nil
}

func resolveRepoPath() string {
	client, err := git.NewClient()
	if err != nil {
		return ""
	}

	root, err := client.WorkingTreeRoot()
	if err != nil || root == "" {
		return ""
	}

	return filepath.Join(root, configFileName)
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

	repoLabel := fmt.Sprintf("This repo (%s)", filepath.Base(repoPath))
	if repoExists {
		repoLabel += " [overwrite]"
	} else {
		repoLabel += " [takes precedence over home]"
	}

	return append(opts, huh.NewOption(repoLabel, repoPath))
}

func writeDest(cmd *cobra.Command, dest string) error {
	if err := os.WriteFile(dest, appconfig.DefaultJSON(), 0644); err != nil {
		return fmt.Errorf("write config to %s: %w", dest, err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Config written to %s\n", dest)

	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)

	return err == nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./cmd/config/... -run "TestBuild|TestWrite|TestPick|TestFile" -v
```

Expected: all tests `PASS`.

- [ ] **Step 5: Run all cmd/config tests**

```bash
mise exec -- go test ./cmd/config/... -v
```

Expected: all tests `PASS`.

- [ ] **Step 6: Commit**

```bash
git add cmd/config/init.go cmd/config/init_test.go
git commit -m "feat(cmd/config): add config init subcommand"
```

---

## Task 5: Wire `cmd/config` into `cmd/root.go`

**Files:**
- Modify: `cmd/root.go`

- [ ] **Step 1: Add import alias to `cmd/root.go`**

In the import block, add:

```go
cfgcmd "github.com/piprim/git-zf/cmd/config"
```

The existing `"github.com/piprim/git-zf/config"` import stays unchanged. The new package uses the `cfgcmd` alias to avoid collision.

- [ ] **Step 2: Instantiate and wire the command in `GetRootCmd`**

In `GetRootCmd`, after the existing command instantiations, add:

```go
cf := cfgcmd.New(appConfig)
```

Then add `cf.GetRootCmd()` to the `rootCmd.AddCommand(...)` call. The full `AddCommand` block becomes:

```go
rootCmd.AddCommand(
    cp.GetRootCmd(),
    co.GetRootCmd(),
    ir.GetRootCmd(),
    br.GetRootCmd(),
    vs.GetRootCmd(),
    in.GetRootCmd(),
    cf.GetRootCmd(),
)
```

- [ ] **Step 3: Build and verify it compiles**

```bash
mise exec -- go build ./...
```

Expected: no errors.

- [ ] **Step 4: Smoke-test the commands are registered**

```bash
./bin/git-zf config --help
```

Expected output contains:

```
Usage:
  git-zf config [command]

Available Commands:
  init        Write a default config file to home or repo directory
  show        Show active config file path and effective configuration
```

- [ ] **Step 5: Run full test suite**

```bash
mise exec -- go test ./...
```

Expected: all tests `PASS`.

- [ ] **Step 6: Commit**

```bash
git add cmd/root.go
git commit -m "feat(cmd): wire config command into root"
```
