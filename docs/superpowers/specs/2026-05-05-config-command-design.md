# Config Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `git zf config` command with two subcommands — `show` (inspect effective config) and `init` (write a config file).

**Architecture:** New `cmd/config/` package mirroring existing `cmd/issue/`, `cmd/branch/` pattern. A `Config` struct holds `*config.AppConfig`. The `config` package gains one exported helper (`DefaultJSON`). The root command wires the new package in.

**Tech Stack:** Go, Cobra, huh (TUI), go-homedir, encoding/json, viper (read-only, already initialised at startup).

---

## Subcommand: `git zf config show`

Prints two things:

1. **Config file in use** — `viper.ConfigFileUsed()`, or `"no config file found (built-in defaults apply)"` if empty.
2. **Effective config** — the fully merged `AppConfig` (defaults + user overrides) serialised as indented JSON. To exclude the runtime-only `ProgName` field and mask the token, define a local `configOutput` struct in `cmd/config/show.go` that mirrors `AppConfig` without `ProgName` and with `IssueTrackerConfig.Token` replaced by a masked string. Copy values from `AppConfig` into this struct before marshalling. This avoids mutating the live config.

Output format (stdout):

```
Config file: /home/user/.git-zf.json

{
  "commit-types": [ ... ],
  ...
  "issue-tracker": {
    "type": "plane",
    "url": "https://...",
    "token": "***"
  }
}
```

Works outside a git repo.

## Subcommand: `git zf config init`

Writes the embedded `default.json` to a user-chosen path. Uses a huh TUI picker to determine the destination. Logic:

### Destination resolution

1. Resolve `~/.git-zf.json` via `go-homedir`.
2. Resolve `<repo-root>/.git-zf.json` via `git.NewClient()` + `client.WorkingTreeRoot()`. If not in a git repo, the repo-root option is not offered.
3. Check existence of each candidate with `os.Stat`.

### TUI picker options (huh select)

| Situation | Options shown |
|-----------|---------------|
| Neither exists, inside repo | `Home (~/.git-zf.json)` / `This repo (.git-zf.json)` |
| Neither exists, outside repo | `Home (~/.git-zf.json)` only (auto-selected, no picker needed) |
| Home exists, repo does not | `Home (~/.git-zf.json) [overwrite]` / `This repo (.git-zf.json) [new — takes precedence]` |
| Repo exists, home does not | `Home (~/.git-zf.json) [new]` / `This repo (.git-zf.json) [overwrite]` |
| Both exist | `Home (~/.git-zf.json) [overwrite]` / `This repo (.git-zf.json) [overwrite]` |

After selection: write `config.DefaultJSON()` to the chosen path with mode `0644`.

On success: `"Config written to <path>"`.

On TUI cancel (`huh.ErrUserAborted` or equivalent): print `"Aborted."`, return `nil`.

## `config` package change

Add one exported function:

```go
// DefaultJSON returns the raw embedded default.json bytes.
func DefaultJSON() []byte { return defaultJSON }
```

No other changes to the `config` package.

## Error handling

- `show`: no errors expected (config is already loaded); marshal errors are returned wrapped.
- `init`: `os.WriteFile` errors are returned wrapped with path context.
- Outside a git repo: `init` silently omits the repo-root option; `show` still works.

## Testing

- `show`: unit test injecting a known `AppConfig` (with a non-empty token) and a fake viper path via a test helper; assert stdout contains the masked token and the path line.
- `init`:
  - No file anywhere → assert file created at chosen path with default JSON content.
  - Home file exists → assert repo-root option is available and produces a file at repo root.
  - Both exist → assert overwrite works for each choice.
  - Tests use `t.TempDir()` as fake home and repo root; no real filesystem side-effects.
