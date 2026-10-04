# TOML Configuration with Two-Layer Merge

**Date:** 2026-05-08
**Status:** Approved

## Goal

Replace the JSON config format with TOML and support a two-layer merge so users can keep shared settings (commit-types, commit-message template) in a global file while overriding only project-specific settings (issue-tracker, branch.base) in a per-clone repo file.

## File Format & Locations

The config file name changes from `.git-zf.json` to `.git-zf.toml`. Locations are unchanged:

| Layer | Path | Purpose |
|---|---|---|
| Global | `~/.git-zf.toml` | Shared defaults across all repos |
| Local | `.git/.git-zf.toml` | Per-clone overrides (not committed) |
| Built-in | embedded `config/default.toml` | Fallback when no file exists |

Priority: built-in < global < local.

### Typical global config

```toml
[[commit-types]]
name = "feat"
desc = "A new feature"

[[commit-types]]
name = "fix"
desc = "A bug fix"

# … more types …

[commit-message]
template = "{{.type}}{{with .scope}}({{.}}){{end}}: {{.subject}}{{with .body}}\n\n{{.}}{{end}}{{with .footer}}\n\n{{.}}{{end}}"

[[commit-message.items]]
name = "scope"
desc = "Scope (users, db, poll…):"
form = "input"

[[commit-message.items]]
name = "subject"
desc = "Concise description. Imperative, lower case, no final dot:"
form = "input"
required = true
```

### Typical minimal project config

```toml
[issue-tracker]
type = "redmine"
url = "https://redmine.example.com"
token = "secret"

[branch]
base = "develop"
```

## Config Loading & Merge

`initConfig` in `cmd/root.go` switches to a two-phase load:

1. `viper.SetConfigType("toml")`
2. `viper.ReadInConfig()` — loads global (`~/.git-zf.toml`) if present
3. If `.git/.git-zf.toml` exists → `viper.MergeInConfig()` — repo values override global values

Viper's `MergeInConfig` deep-merges scalars and maps; arrays are replaced whole (the desired behaviour for `commit-types` and `commit-message.items`). Keys absent from the repo file are inherited from global unchanged.

`config.Load()` is **unchanged** — it continues calling `viper.IsSet()` per section and overlaying on top of the built-in embedded defaults. The full resolution chain is:

```
built-in default.toml  ←  ~/.git-zf.toml  ←  .git/.git-zf.toml
(lowest priority)                              (highest priority)
```

### Code changes in `config/config.go`

- `config/default.json` → `config/default.toml`
- Embedded var: `defaultJSON []byte` → `defaultTOML []byte`
- `DefaultJSON()` removed; `DefaultTOML() []byte` added
- Struct tags: `json:"..."` → `toml:"..."` on all config structs (`mapstructure:"..."` tags unchanged)
- `Load()`: `json.Unmarshal(defaultJSON, &cfg)` → `toml.Unmarshal(defaultTOML, &cfg)` using `pelletier/go-toml` (already an indirect dep via Viper)

### Code changes in `config/config.go`

The `configFileName` constant changes from `".git-zf.json"` to `".git-zf.toml"`. This constant is used by `HomePath()` and `RepoPath()`, so both helper functions automatically return the correct `.toml` paths with no further changes.

### Code changes in `cmd/root.go`

```go
const (
    configFileName = ".git-zf"
    configFileExt  = "toml"   // was "json"
)
```

`initConfig` switches to an explicit two-file load using `SetConfigFile` to point Viper at each path before reading/merging. This avoids the ambiguity of the path-search approach and ensures `MergeInConfig` reads the repo file rather than re-reading the global file.

```go
viper.SetConfigType(configFileExt)

// Phase 1: load global config.
if homePath, err := config.HomePath(); err == nil {
    viper.SetConfigFile(homePath)
    if err := viper.ReadInConfig(); err != nil && !errors.Is(err, os.ErrNotExist) {
        return fmt.Errorf("read global config: %w", err)
    }
}

// Phase 2: merge repo-local config on top.
if repoPath := config.RepoPath(); repoPath != "" {
    viper.SetConfigFile(repoPath)
    if err := viper.MergeInConfig(); err != nil && !errors.Is(err, os.ErrNotExist) {
        return fmt.Errorf("read repo config: %w", err)
    }
}
```

## `config init` Command

The `init` subcommand destination picker is unchanged (home vs repo). Behaviour differs by destination:

**Home destination** — writes `DefaultTOML()` in full (all sections with built-in default values). Serves as a starting template.

**Repo destination** — the effective `AppConfig` is marshalled to a `map[string]any` (via a TOML encode/decode round-trip). The top-level keys of that map drive the checkbox list dynamically — no hardcoded section names. If a new section is added to `AppConfig` in the future, it appears in the picker automatically.

```
Which sections do you want to include in the repo config?

  [x] issue-tracker
  [ ] commit-types
  [ ] commit-message
  [x] branch
```

The user selects which keys to include. Only the selected keys are written to `.git/.git-zf.toml`, using the values from the effective config (global overlay already applied).

The `writeDest` function is refactored into two paths:
- `writeHomeDest(dest string)` — writes `DefaultTOML()`
- `writeRepoDest(cmd *cobra.Command, dest string, cfg *AppConfig)` — marshals `cfg` to `map[string]any`, builds checkboxes from map keys, writes only selected sections to TOML

## Migration

No automatic migration. On first run after upgrade, if `.git-zf.json` exists but no `.git-zf.toml` is found, Viper logs "config file not found" and falls back to built-in defaults. Users re-run `git zf config init` to create the new TOML file.

## Testing

- `config/default.toml` replaces `default.json` as the embedded fixture
- `TestDefaultJSON_isValidJSON` → `TestDefaultTOML_isValidTOML`
- Existing overlay tests (`TestLoad_overlay`, `TestLoad_overlay_partialCommitMessage`) switch from JSON fixture files to TOML
- New test `TestLoad_twoFilemerge`: global defines `commit-types`, local defines `issue-tracker`; asserts both are present in the loaded config
- `cmd/config/init_test.go`: new tests for the repo-destination section picker (checkbox selection → correct TOML output)
