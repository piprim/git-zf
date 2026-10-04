# Config Refactor Implementation Design

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.
> Use Go Skills and code-review agent too.

**Goal:** Extract app-wide configuration from the `commit/` package into a dedicated `config/` package; promote commit types to a first-class top-level config key; rename `message` → `commit-message` and `tracker` → `issue-tracker`; load config once in the root command.

**Architecture:** A new `config/` package owns the embedded `default.json`, all config structs, and the `Load()` function. The root command calls `config.Load()` once after viper is initialised and stores the result in a package-level `appConfig` variable that all subcommands read. No subcommand touches viper or config loading directly.

**Tech Stack:** Go standard library (`encoding/json`, `embed`), Viper (mapstructure overlay), Cobra.

---

## New `.git-zf.json` JSON structure

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
    "type": "redmine",
    "url": "https://redmine.example.com",
    "token": "YOUR_API_KEY",
    "in-progress-status": "In Progress"
  }
}
```

Key changes from the old format:
- `message` → `commit-message`
- `tracker` → `issue-tracker`
- `commit-types` is a new top-level key; the `type` item is removed from `commit-message.items`

---

## Package structure

```
config/
  default.json   — formatted default config (replaces commit/defaultConfig.go raw string)
  config.go      — structs + Load()
```

### `config/config.go` — full struct definitions

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

// Add documentation
type CommitTypeOption struct {
    Name string `json:"name" mapstructure:"name"`
    Desc string `json:"desc" mapstructure:"desc"`
}

// Add documentation
type CommitItemOption struct {
    Name string `json:"name" mapstructure:"name"`
    Desc string `json:"desc" mapstructure:"desc"`
}

// Add documentation
type CommitItem struct {
    Name     string             `json:"name"     mapstructure:"name"`
    Desc     string             `json:"desc"     mapstructure:"desc"`
    Form     string             `json:"form"     mapstructure:"form"`
    Required bool               `json:"required" mapstructure:"required"`
    Options  []CommitItemOption `json:"options"  mapstructure:"options"`
    Value    string
}

// Add documentation
type CommitMessageConfig struct {
    Items    []CommitItem `json:"items"    mapstructure:"items"`
    Template string       `json:"template" mapstructure:"template"`
}

// Add documentation
type BranchConfig struct {
    Base string `json:"base" mapstructure:"base"`
}

// Add documentation
type IssueTrackerConfig struct {
    Type             string `json:"type"               mapstructure:"type"`
    URL              string `json:"url"                mapstructure:"url"`
    Token            string `json:"token"              mapstructure:"token"`
    InProgressStatus string `json:"in-progress-status" mapstructure:"in-progress-status"`
}

// Add documentation
type AppConfig struct {
    CommitTypes   []CommitTypeOption  `json:"commit-types"   mapstructure:"commit-types"`
    CommitMessage CommitMessageConfig `json:"commit-message" mapstructure:"commit-message"`
    Branch        BranchConfig        `json:"branch"         mapstructure:"branch"`
    IssueTracker  IssueTrackerConfig  `json:"issue-tracker"  mapstructure:"issue-tracker"`
}

// Load parses the embedded default.json then overlays any values present in viper.
// viper.Sub does not work for arrays, so viper.UnmarshalKey is used for all sections
// to keep the approach consistent.
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

---

## Data flow

```
cmd/root.go     initConfig()
                  viper.ReadInConfig()           (already there)
                  appConfig, err = config.Load() (new)
                  package-level var appConfig AppConfig
       │
cmd/commit.go   uses appConfig.CommitTypes, appConfig.CommitMessage
cmd/issue.go    uses appConfig.CommitTypes, appConfig.IssueTracker, appConfig.Branch
cmd/branch.go   uses appConfig.CommitTypes, appConfig.Branch
```

`initConfig()` already returns an error that `GetRootCmd` propagates, so config load errors surface cleanly without any cobra hook changes.

---

## tracker.Config → config.IssueTrackerConfig

`tracker.Config` in `tracker/tracker.go` is replaced by `config.IssueTrackerConfig`.
`tracker.New()` signature changes from `tracker.New(cfg tracker.Config)` to `tracker.New(cfg config.IssueTrackerConfig)`.
`tracker/` gains an import of `config/`; the `tracker.Config` type is deleted.

---

## Changes in `tui/commit.go`

`CommitMessageConfig`, `CommitItem`, `CommitItemOption` are deleted from `tui/commit.go` and replaced with imports from `config/`.

`CommitMessageGroup` signature changes:
```go
// before
func CommitMessageGroup(items []CommitItem) *huh.Group

// after — type select prepended from commitTypes; selectedType receives the chosen value
func CommitMessageGroup(commitTypes []config.CommitTypeOption, items []config.CommitItem, selectedType *string) *huh.Group
```

The type select is always the first field, built from `commitTypes`, writing into `selectedType`.
The `"select"` case in the item-rendering loop can be kept for future custom select items but will not be exercised by the default config.

`CommitOption` and `CommitOptionsGroup` are unchanged.

---

## Changes in `commit/form.go`

`DefaultMessageConfig()` is deleted (config is now loaded via `config.Load()`).

`FillOutForm` signature changes:
```go
// before
func FillOutForm(cfg tui.CommitMessageConfig, defaults tui.CommitOption) ([]byte, tui.CommitOption, error)

// after
func FillOutForm(cfg config.AppConfig, defaults tui.CommitOption) ([]byte, tui.CommitOption, error)
```

Inside, `tui.CommitMessageGroup` receives `cfg.CommitTypes` and `cfg.CommitMessage.Items`.
The template string comes from `cfg.CommitMessage.Template`.
`extractMsg` is updated to capture the type value from a dedicated `var selectedType string` that
the type select writes into, plus each item's `Value` from `cfg.CommitMessage.Items`:

```go
var selectedType string
// selectedType is passed as Value pointer to the type select in CommitMessageGroup

extractMsg = func() map[string]any {
    m := make(map[string]any, len(cfg.CommitMessage.Items)+1)
    m["type"] = selectedType
    for i := range cfg.CommitMessage.Items {
        m[cfg.CommitMessage.Items[i].Name] = cfg.CommitMessage.Items[i].Value
    }
    return m
}
```

---

## Changes in `cmd/issue.go`

- `loadMessageConfig()` call removed; use `appConfig` directly.
- `getAllowedBranchType` signature: `func getAllowedBranchType(types []config.CommitTypeOption) []string`
- Viper key reads replaced:
  - `viper.GetString("tracker.type")` → `appConfig.IssueTracker.Type`
  - `viper.GetString("tracker.url")` → `appConfig.IssueTracker.URL`
  - `viper.GetString("tracker.token")` → `appConfig.IssueTracker.Token`
  - `viper.GetString("tracker.in-progress-status")` → `appConfig.IssueTracker.InProgressStatus`
- `tracker.New(trackerCfg)` now passes `appConfig.IssueTracker` directly (type matches).
- `InProgressStatus` default fallback ("In Progress") moves into `config/default.json`.

---

## Changes in `cmd/branch.go`

- `viper.GetString("branch.base")` → `appConfig.Branch.Base`
- `getAllowedBranchType` updated to accept `[]config.CommitTypeOption`.

---

## Files touched summary

| File | Action |
|------|--------|
| `config/config.go` | **create** — structs + `Load()` |
| `config/default.json` | **create** — formatted default config |
| `commit/defaultConfig.go` | **delete** |
| `commit/form.go` | update `FillOutForm` signature; delete `DefaultMessageConfig()` |
| `tui/commit.go` | delete `CommitMessageConfig`, `CommitItem`, `CommitItemOption`; update `CommitMessageGroup` |
| `tui/commit_test.go` | update type references |
| `tracker/tracker.go` | delete `Config` type; `New()` accepts `config.IssueTrackerConfig` |
| `tracker/redmine/redmine.go` | update `New()` parameter type |
| `tracker/redmine/redmine_test.go` | update `tracker.Config{}` → `config.IssueTrackerConfig{}` |
| `cmd/root.go` | call `config.Load()` in `initConfig()`; store `appConfig` |
| `cmd/commit.go` | delete `loadMessageConfig()`; use `appConfig` |
| `cmd/issue.go` | use `appConfig`; update `getAllowedBranchType`; remove viper key reads |
| `cmd/branch.go` | use `appConfig.Branch.Base`; update `getAllowedBranchType` |

---

## Testing

- `config/config_test.go` — `TestLoad_defaults` verifies that `Load()` with no viper config returns the embedded defaults (10 commit types, correct template, empty tracker).
- `config/config_test.go` — `TestLoad_overlay` verifies that a viper-loaded JSON overrides only the specified keys.
- Existing `tui/commit_test.go` updated for new type imports.
- Existing `tracker/redmine/redmine_test.go` updated for new config type.
- All other existing tests continue to pass without modification.

## Documentation

Update the README.md according to the new configuration file structure.
