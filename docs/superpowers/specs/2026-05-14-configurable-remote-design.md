# Configurable Remote Name

**Date:** 2026-05-14
**Status:** Approved

## Problem

git-zf hardcodes `"origin"` in four places across three files. Repos whose only
remote is not named `"origin"` (e.g. `"pi"`, `"upstream"`) fail silently or
with cryptic errors. Repos with multiple remotes have no way to designate the
canonical one.

## Affected code

| Location | Hardcoded reference |
|----------|---------------------|
| `git/git.go` — `DefaultBaseBranch()` | `refs/remotes/origin/HEAD` |
| `git/git.go` — `IsMergedInto()` | `refs/remotes/origin/<base>` |
| `git/merge.go` — `MergeRebase()` | `origin/<base>` (merge + reset targets) |
| `git/merge.go` — `FetchOrigin()` | `git fetch origin` |
| `cmd/issue/close.go` (3 sites) | `"origin/" + baseBranch`, `FetchOrigin` |

## Design

### 1. Config

Add `remote` to `BranchConfig` and `config/default.toml`. Empty string means
auto-detect.

```toml
[branch]
base   = ""
remote = ""   # leave empty to auto-detect; set to "pi", "upstream", etc.
```

`config init` does not prompt for this — it is an advanced setting users
configure manually when needed.

### 2. `git.Client` — `remote` field and resolution

```go
type Client struct {
    repo   *gogit.Repository
    io     *pkg.IO
    remote string   // empty until resolved
}
```

**`SetRemote(name string)`** — pins the remote name. Called from the command
layer when `cfg.Branch.Remote != ""`.

**`Remote() (string, error)`** — returns the resolved remote name, resolving
lazily on first call:

1. `c.remote != ""` → return it (already pinned or cached from prior call).
2. List remotes via go-git.
3. Exactly one → cache in `c.remote` and return it.
4. Zero → return `("", nil)` — no remote, local-only mode.
5. More than one, one is named `"origin"` → use `"origin"` (mirrors standard git convention).
6. More than one, none named `"origin"` → return error:
   `multiple remotes found (<a>, <b>); set branch.remote in .git-zf.toml`

**Contract:** `("", nil)` means "no remote, local-only mode". Any non-nil error
means "ambiguous configuration — abort". Callers must check `err` before
inspecting the string; treating a non-nil error as an empty remote is a silent
data-loss bug.

### 3. Method changes

All changes are internal — no signature changes, callers are unaffected.

#### `git/merge.go`

`FetchOrigin` renamed to `Fetch`. Behavior:
- remote resolved → `git fetch <remote>`
- remote `""` → return nil immediately, no log, no warning — the orchestrator
  pipeline continues into local-only `MergeRebase` without interruption

`MergeRebase` — `remoteBase` computed as:
- remote resolved → `"<remote>/<base>"`
- remote `""` → `"<base>"` (local base branch directly)

Same local fallback applies to the `reset --soft` target.

#### `git/git.go`

`DefaultBaseBranch` — remote HEAD lookup:
- remote resolved → check `refs/remotes/<remote>/HEAD`
- remote `""` → skip remote lookup, go straight to local `main`/`master` fallback

`IsMergedInto` — remote tracking ref fallback:
- remote resolved → try `refs/remotes/<remote>/<base>`
- remote `""` → skip remote fallback, use local ref only

#### `cmd/issue/close.go`

- `FetchOrigin` call → `Fetch`
- `remoteBase` is computed by calling `Remote()` and handling both return values
  explicitly before any string comparison:

```go
remoteName, err := mc.client.Remote()
if err != nil {
    return fmt.Errorf("resolve remote: %w", err) // abort — ambiguous config
}

var remoteBase string
if remoteName != "" {
    remoteBase = remoteName + "/" + mc.baseBranch
} else {
    remoteBase = mc.baseBranch // local-only fallback
}
```

- `ResolveRef("refs/remotes/" + remoteBase)` → same conditional (only reached
  when `remoteName != ""`; when empty, `remoteBase` is already the local branch
  name and `ResolveRef("refs/remotes/main")` would be wrong — skip or guard)

`Remote()` is **not** called as a pre-flight in `closeRunE`. Squash and no-ff
strategies work entirely locally and must not be rejected for users who happen
to have multiple remotes. The error-first check lives inside `doRebaseClose`,
where it is the first call before any fetch or git mutation — so it still
fails fast within its own scope, before any state is changed.

The same error-first pattern applies in `start.go` and `branch.go` wherever
`Remote()` is called.

### 4. Command layer wiring

After creating the git client in every command that performs remote operations
(`cmd/issue/close.go`, `cmd/issue/start.go`, `cmd/branch/branch.go`):

```go
client, err := git.NewClient(io)
// ...
if cfg.Branch.Remote != "" {
    client.SetRemote(cfg.Branch.Remote)
}
```

### 5. Error handling

| Scenario | Behaviour |
|----------|-----------|
| No remotes | All methods degrade gracefully (no-op fetch, local refs only) |
| One remote | Auto-detected and cached silently |
| Multiple remotes, one named `"origin"` | `"origin"` used automatically (git convention) |
| Multiple remotes, none named `"origin"` | `Remote()` returns an actionable error naming the remotes and the config key to set |
| `SetRemote` called with explicit name | Used as-is, no detection performed |

### 6. Future: interactive remote picker

When a picker is added later, it becomes a third resolution path before the
multi-remote error:

```
1. cfg.Branch.Remote set?  → SetRemote(cfg value)
2. Single remote?          → cache and use
3. Multiple remotes?       → show TUI picker → SetRemote(picked)
```

Because the remote is stored on the client (`c.remote`), no signature changes
are needed at that point.

### 7. Testing

- `Remote()` unit tests: repo with 0, 1, and 2 remotes → verify each outcome.
- `Fetch` tests: replace `FetchOrigin` tests, inject remote via `SetRemote`.
- `MergeRebase` tests: verify correct ref used with and without remote.
- `DefaultBaseBranch` and `IsMergedInto`: verify local-only fallback when no remote.
- Command-layer wiring: existing close/branch tests cover end-to-end behaviour.
