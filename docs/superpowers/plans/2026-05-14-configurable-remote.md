# Configurable Remote Name Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace every hardcoded `"origin"` in git-zf with a configurable, auto-detected remote name that degrades gracefully in local-only repos.

**Architecture:** Add a `remote string` field to `git.Client` with lazy resolution via `Remote() (string, error)`. Config feeds `SetRemote(name)` at startup; `Remote()` auto-detects (single remote wins, `"origin"` convention for multiples, `("", nil)` for no remote). All affected methods call `c.Remote()` internally — no signature changes.

**Tech Stack:** Go, `github.com/go-git/go-git/v6`, `github.com/go-git/go-git/v6/config` (for `RemoteConfig` in tests), standard `testing` package.

---

### Task 1: Add `remote` to config

**Files:**
- Modify: `config/config.go` — add `Remote` field to `BranchConfig`
- Modify: `config/default.toml` — add `remote = ""`
- Modify: `config/config_test.go` — add test for `Remote` field

- [ ] **Step 1: Write the failing test**

In `config/config_test.go`, add a sub-case to the existing `Branch` config test (or add a new one). The exact location depends on what already exists — search for `BranchConfig` or `Branch.Base` tests and add beside them:

```go
t.Run("branch.remote is loaded from toml", func(t *testing.T) {
    t.Parallel()

    v := viper.New()
    v.SetConfigType("toml")
    err := v.ReadConfig(strings.NewReader(`
[branch]
remote = "upstream"
`))
    if err != nil {
        t.Fatalf("read config: %v", err)
    }
    // Swap the global viper, restore after test.
    orig := viper.GetViper()
    viper.Reset()
    if err := viper.MergeConfigMap(v.AllSettings()); err != nil {
        t.Fatalf("merge: %v", err)
    }
    t.Cleanup(func() { *viper.GetViper() = *orig })

    cfg, err := Load()
    if err != nil {
        t.Fatalf("Load: %v", err)
    }
    if cfg.Branch.Remote != "upstream" {
        t.Errorf("Branch.Remote = %q, want %q", cfg.Branch.Remote, "upstream")
    }
})
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./config/... -run TestLoad/branch.remote -v
```

Expected: FAIL — `cfg.Branch.Remote` field does not exist.

- [ ] **Step 3: Add `Remote` field to `BranchConfig` in `config/config.go`**

```go
// BranchConfig holds branch-related settings.
// Base is the branch new branches are cut from; empty means auto-detect.
// Remote is the git remote name to use; empty means auto-detect.
type BranchConfig struct {
	Base   string `json:"base"   toml:"base"   mapstructure:"base"`
	Remote string `json:"remote" toml:"remote" mapstructure:"remote"`
}
```

- [ ] **Step 4: Add `remote = ""` to `config/default.toml`**

Find the `[branch]` section and add the key:

```toml
[branch]
base   = ""
remote = ""
```

- [ ] **Step 5: Run test to verify it passes**

```bash
mise exec -- go test ./config/... -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/default.toml config/config_test.go
git commit -m "feat(config): add branch.remote configuration key"
```

---

### Task 2: Add `remote` field, `SetRemote`, and `Remote()` to `git.Client`

**Files:**
- Modify: `git/git.go` — add field + two methods
- Modify: `git/git_test.go` — new `TestRemote` covering all resolution cases

- [ ] **Step 1: Write the failing tests**

Add `TestRemote` to `git/git_test.go`. It lives in `package git` so it can construct `&Client{repo: repo}` directly. It needs the go-git config import — add `gogitcfg "github.com/go-git/go-git/v6/config"` to the import block.

```go
func TestRemote(t *testing.T) {
    t.Parallel()

    t.Run("returns empty string for repo with no remotes", func(t *testing.T) {
        t.Parallel()

        repo := newTestRepo(t)
        c := &Client{repo: repo}

        remote, err := c.Remote()
        if err != nil {
            t.Fatalf("Remote: %v", err)
        }
        if remote != "" {
            t.Errorf("Remote = %q, want %q", remote, "")
        }
    })

    t.Run("returns the sole remote name", func(t *testing.T) {
        t.Parallel()

        repo := newTestRepo(t)
        if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
            Name: "pi",
            URLs: []string{"https://example.com/repo.git"},
        }); err != nil {
            t.Fatalf("CreateRemote: %v", err)
        }
        c := &Client{repo: repo}

        remote, err := c.Remote()
        if err != nil {
            t.Fatalf("Remote: %v", err)
        }
        if remote != "pi" {
            t.Errorf("Remote = %q, want %q", remote, "pi")
        }
    })

    t.Run("returns origin when multiple remotes include origin", func(t *testing.T) {
        t.Parallel()

        repo := newTestRepo(t)
        for _, name := range []string{"origin", "upstream"} {
            if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
                Name: name,
                URLs: []string{"https://example.com/" + name + ".git"},
            }); err != nil {
                t.Fatalf("CreateRemote %s: %v", name, err)
            }
        }
        c := &Client{repo: repo}

        remote, err := c.Remote()
        if err != nil {
            t.Fatalf("Remote: %v", err)
        }
        if remote != "origin" {
            t.Errorf("Remote = %q, want %q", remote, "origin")
        }
    })

    t.Run("errors when multiple remotes exist with no origin", func(t *testing.T) {
        t.Parallel()

        repo := newTestRepo(t)
        for _, name := range []string{"pi", "upstream"} {
            if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
                Name: name,
                URLs: []string{"https://example.com/" + name + ".git"},
            }); err != nil {
                t.Fatalf("CreateRemote %s: %v", name, err)
            }
        }
        c := &Client{repo: repo}

        _, err := c.Remote()
        if err == nil {
            t.Fatal("Remote: expected error, got nil")
        }
        if !strings.Contains(err.Error(), "branch.remote") {
            t.Errorf("error %q does not mention branch.remote config key", err.Error())
        }
    })

    t.Run("SetRemote pins the name, bypassing detection", func(t *testing.T) {
        t.Parallel()

        repo := newTestRepo(t)
        c := &Client{repo: repo}
        c.SetRemote("pi")

        remote, err := c.Remote()
        if err != nil {
            t.Fatalf("Remote: %v", err)
        }
        if remote != "pi" {
            t.Errorf("Remote = %q, want %q", remote, "pi")
        }
    })

    t.Run("caches result on second call", func(t *testing.T) {
        t.Parallel()

        repo := newTestRepo(t)
        if _, err := repo.CreateRemote(&gogitcfg.RemoteConfig{
            Name: "pi",
            URLs: []string{"https://example.com/repo.git"},
        }); err != nil {
            t.Fatalf("CreateRemote: %v", err)
        }
        c := &Client{repo: repo}

        r1, err := c.Remote()
        if err != nil {
            t.Fatalf("first Remote: %v", err)
        }
        // Simulate the remote disappearing — cache should win.
        if err := repo.DeleteRemote("pi"); err != nil {
            t.Fatalf("DeleteRemote: %v", err)
        }
        r2, err := c.Remote()
        if err != nil {
            t.Fatalf("second Remote: %v", err)
        }
        if r1 != r2 {
            t.Errorf("second call = %q, want cached %q", r2, r1)
        }
    })
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./git/... -run TestRemote -v
```

Expected: compile error — `SetRemote` and `Remote` are undefined.

- [ ] **Step 3: Add the `remote` field and methods to `git/git.go`**

Add `remote string` to the `Client` struct:

```go
// Client wraps a go-git repository and exposes commit operations.
type Client struct {
	repo   *gogit.Repository
	io     *pkg.IO
	remote string
}
```

Add the two methods after `NewClientAt` (before `WorkingTreeRoot`):

```go
// SetRemote pins the remote name used for all remote operations.
// Call this when the user has configured branch.remote explicitly.
func (c *Client) SetRemote(name string) {
	c.remote = name
}

// Remote returns the resolved remote name, auto-detecting on first call.
//
// Resolution order:
//  1. Already pinned via SetRemote → return as-is.
//  2. Exactly one remote → cache and return its name.
//  3. Zero remotes → return ("", nil); caller treats this as local-only.
//  4. Multiple remotes, one named "origin" → use "origin" (git convention).
//  5. Multiple remotes, none named "origin" → error with actionable message.
func (c *Client) Remote() (string, error) {
	if c.remote != "" {
		return c.remote, nil
	}

	remotes, err := c.repo.Remotes()
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}

	switch len(remotes) {
	case 0:
		return "", nil
	case 1:
		c.remote = remotes[0].Config().Name

		return c.remote, nil
	default:
		for _, r := range remotes {
			if r.Config().Name == "origin" {
				c.remote = "origin"

				return c.remote, nil
			}
		}

		names := make([]string, len(remotes))
		for i, r := range remotes {
			names[i] = r.Config().Name
		}

		return "", fmt.Errorf("multiple remotes found (%s); set branch.remote in .git-zf.toml",
			strings.Join(names, ", "))
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
mise exec -- go test ./git/... -run TestRemote -v
```

Expected: all sub-tests PASS.

- [ ] **Step 5: Run the full git package tests to check for regressions**

```bash
mise exec -- go test ./git/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): add configurable remote with lazy auto-detection"
```

---

### Task 3: Rename `FetchOrigin` → `Fetch`, use `c.Remote()` internally

**Files:**
- Modify: `git/merge.go` — rename and update `FetchOrigin`
- Modify: `git/merge_test.go` — rename test, add no-remote no-op test

- [ ] **Step 1: Write the failing test for the no-remote no-op**

In `git/merge_test.go`, add a new test. The existing `TestFetchOrigin_updatesRemoteTrackingRef` will be renamed in step 3 — add the new case now with the new name so the rename is one edit:

```go
func TestFetch_noopWhenNoRemote(t *testing.T) {
    t.Parallel()

    // newDiskRepo creates a repo with no remotes.
    c, _ := newDiskRepo(t)

    if err := c.Fetch(t.Context()); err != nil {
        t.Fatalf("Fetch with no remote: %v", err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./git/... -run TestFetch_noopWhenNoRemote -v
```

Expected: compile error — method `Fetch` undefined.

- [ ] **Step 3: Rename `FetchOrigin` to `Fetch` and update the implementation in `git/merge.go`**

Replace the entire `FetchOrigin` function:

```go
// Fetch runs `git fetch <remote>`. Returns nil immediately when no remote is
// configured (local-only repo). Returns a wrapped error when the remote is
// unreachable or auth fails.
func (c *Client) Fetch(ctx context.Context) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	if remote == "" {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "fetch", remote); err != nil {
		return fmt.Errorf("fetch %s: %w", remote, err)
	}

	return nil
}
```

- [ ] **Step 4: Rename `TestFetchOrigin_updatesRemoteTrackingRef` → `TestFetch_updatesRemoteTrackingRef` in `git/merge_test.go`**

Find:
```go
func TestFetchOrigin_updatesRemoteTrackingRef(t *testing.T) {
```
Replace with:
```go
func TestFetch_updatesRemoteTrackingRef(t *testing.T) {
```

Find (inside that test):
```go
	if err := c.FetchOrigin(t.Context()); err != nil {
		t.Fatalf("FetchOrigin: %v", err)
	}
```
Replace with:
```go
	if err := c.Fetch(t.Context()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
```

- [ ] **Step 5: Update the two `FetchOrigin` call-sites in `TestMergeRebase_clean` and `TestMergeRebase_preservesSubmodulePointer`**

In `git/merge_test.go`, replace both remaining occurrences:

```go
// Before (line ~745):
if err := c.FetchOrigin(t.Context()); err != nil {
    t.Fatalf("FetchOrigin: %v", err)
}
// After:
if err := c.Fetch(t.Context()); err != nil {
    t.Fatalf("Fetch: %v", err)
}
```

```go
// Before (line ~907):
if err := c.FetchOrigin(t.Context()); err != nil {
    t.Fatalf("FetchOrigin: %v", err)
}
// After:
if err := c.Fetch(t.Context()); err != nil {
    t.Fatalf("Fetch: %v", err)
}
```

- [ ] **Step 6: Run all git package tests**

```bash
mise exec -- go test ./git/... -v
```

Expected: all tests PASS, no references to `FetchOrigin` remaining.

- [ ] **Step 7: Verify no remaining `FetchOrigin` references in non-test files**

```bash
grep -rn "FetchOrigin" /workspace --include="*.go"
```

Expected: only `cmd/issue/close.go` (fixed in Task 6).

- [ ] **Step 8: Commit**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): rename FetchOrigin→Fetch, use resolved remote internally"
```

---

### Task 4: Update `MergeRebase` to use `c.Remote()`

**Files:**
- Modify: `git/merge.go` — update `MergeRebase`
- Modify: `git/merge_test.go` — add no-remote test case

- [ ] **Step 1: Write the failing test**

Add to `git/merge_test.go`:

```go
func TestMergeRebase_noRemote(t *testing.T) {
    t.Parallel()

    // newDiskRepo: one commit on main, no remote.
    c, dir := newDiskRepo(t)

    run := func(args ...string) {
        t.Helper()
        cmd := exec.CommandContext(t.Context(), "git", args...)
        cmd.Dir = dir
        if out, err := cmd.CombinedOutput(); err != nil {
            t.Fatalf("git %v: %v\n%s", args, err, out)
        }
    }

    // Create a feature branch with one commit.
    run("checkout", "-b", "feature")
    if err := os.WriteFile(filepath.Join(dir, "feat.go"), []byte("package main\n"), 0o644); err != nil {
        t.Fatalf("write: %v", err)
    }
    run("add", "feat.go")
    run("commit", "-m", "feat: something")

    // MergeRebase against local main (no remote) must not error.
    if err := c.MergeRebase(t.Context(), "feature", "main"); err != nil {
        t.Fatalf("MergeRebase with no remote: %v", err)
    }

    branch, err := c.CurrentBranch()
    if err != nil {
        t.Fatalf("CurrentBranch: %v", err)
    }
    if branch != "feature" {
        t.Errorf("CurrentBranch = %q, want %q", branch, "feature")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mise exec -- go test ./git/... -run TestMergeRebase_noRemote -v
```

Expected: FAIL — `MergeRebase` tries `origin/main` which doesn't exist.

- [ ] **Step 3: Update `MergeRebase` in `git/merge.go`**

Replace the full function body:

```go
// MergeRebase prepares featureBranch for a single-commit close. When a remote
// is configured, it merges against remote/<baseBranch> and soft-resets to the
// same ref. When no remote is available (local-only repo), it uses the local
// baseBranch directly.
func (c *Client) MergeRebase(ctx context.Context, featureBranch, baseBranch string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}

	var remoteBase string
	if remote != "" {
		remoteBase = remote + "/" + baseBranch
	} else {
		remoteBase = baseBranch
	}

	if err := c.Checkout(ctx, featureBranch); err != nil {
		return fmt.Errorf("checkout %s: %w", featureBranch, err)
	}

	if err := c.runInteractive(ctx, root, "merge", "--no-edit", remoteBase); err != nil {
		return fmt.Errorf("merge %s: %w", remoteBase, err)
	}

	if err := c.runInteractive(ctx, root, "reset", "--soft", remoteBase); err != nil {
		return fmt.Errorf("reset --soft %s: %w", remoteBase, err)
	}

	return nil
}
```

- [ ] **Step 4: Run all merge tests**

```bash
mise exec -- go test ./git/... -run TestMergeRebase -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add git/merge.go git/merge_test.go
git commit -m "feat(git): MergeRebase falls back to local base when no remote"
```

---

### Task 5: Update `DefaultBaseBranch` and `IsMergedInto` to use `c.Remote()`

**Files:**
- Modify: `git/git.go` — update both methods
- Modify: `git/git_test.go` — extend both test functions

- [ ] **Step 1: Write failing tests**

In `git/git_test.go`, add sub-cases to `TestDefaultBaseBranch` and `TestIsMergedInto`.

For `TestDefaultBaseBranch`, add two sub-cases after the existing ones:

```go
t.Run("uses configured remote instead of origin for HEAD lookup", func(t *testing.T) {
    t.Parallel()

    repo := newTestRepo(t)
    // Simulate refs/remotes/pi/HEAD pointing to "develop".
    symRef := plumbing.NewSymbolicReference(
        plumbing.ReferenceName("refs/remotes/pi/HEAD"),
        plumbing.ReferenceName("refs/remotes/pi/develop"),
    )
    if err := repo.Storer.SetReference(symRef); err != nil {
        t.Fatalf("set pi/HEAD: %v", err)
    }

    client := &Client{repo: repo, remote: "pi"}
    base, err := client.DefaultBaseBranch()
    if err != nil {
        t.Fatalf("DefaultBaseBranch: %v", err)
    }
    if base != "develop" {
        t.Errorf("DefaultBaseBranch = %q, want %q", base, "develop")
    }
})

t.Run("skips remote HEAD lookup when no remote and falls back to local", func(t *testing.T) {
    t.Parallel()

    // newTestRepo creates a repo with no remotes; go-git default branch is "master".
    repo := newTestRepo(t)
    client := &Client{repo: repo}

    base, err := client.DefaultBaseBranch()
    if err != nil {
        t.Fatalf("DefaultBaseBranch: %v", err)
    }
    if base != "master" {
        t.Errorf("DefaultBaseBranch = %q, want %q", base, "master")
    }
})
```

For `TestIsMergedInto`, locate the existing test function and add a sub-case testing the remote tracking fallback with a custom remote name. Add after the existing sub-cases:

```go
t.Run("uses configured remote for tracking ref fallback", func(t *testing.T) {
    t.Parallel()

    repo := newTestRepo(t)

    // Create feature branch from the initial commit.
    wt, err := repo.Worktree()
    if err != nil {
        t.Fatalf("worktree: %v", err)
    }
    if err := wt.Checkout(&gogit.CheckoutOptions{Branch: "refs/heads/feature", Create: true}); err != nil {
        t.Fatalf("checkout feature: %v", err)
    }

    // Read HEAD hash (same commit on both branches at this point).
    head, err := repo.Head()
    if err != nil {
        t.Fatalf("head: %v", err)
    }

    // Simulate refs/remotes/pi/main pointing at HEAD.
    if err := repo.Storer.SetReference(plumbing.NewHashReference(
        plumbing.ReferenceName("refs/remotes/pi/main"),
        head.Hash(),
    )); err != nil {
        t.Fatalf("set pi/main: %v", err)
    }

    client := &Client{repo: repo, remote: "pi"}
    // feature == pi/main so it should be considered merged.
    merged, err := client.IsMergedInto("feature", "main")
    if err != nil {
        t.Fatalf("IsMergedInto: %v", err)
    }
    if !merged {
        t.Error("IsMergedInto = false, want true")
    }
})
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
mise exec -- go test ./git/... -run "TestDefaultBaseBranch|TestIsMergedInto" -v
```

Expected: the new sub-cases that rely on `c.remote` being honoured will FAIL or the `"pi"` remote ref won't be found.

- [ ] **Step 3: Update `DefaultBaseBranch` in `git/git.go`**

```go
// DefaultBaseBranch resolves the default base branch in priority order:
//  1. refs/remotes/<remote>/HEAD (skipped when no remote)
//  2. "main" if the local ref exists
//  3. "master" if the local ref exists
func (c *Client) DefaultBaseBranch() (string, error) {
	remote, err := c.Remote()
	if err != nil {
		return "", fmt.Errorf("resolve remote: %w", err)
	}

	if remote != "" {
		if ref, err := c.repo.Reference(plumbing.ReferenceName("refs/remotes/"+remote+"/HEAD"), false); err == nil {
			if ref.Type() == plumbing.SymbolicReference {
				parts := strings.Split(ref.Target().String(), "/")

				return parts[len(parts)-1], nil
			}
		}
	}

	// Fall back to local branches.
	for _, name := range []string{"main", "master"} {
		if _, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+name), false); err == nil {
			return name, nil
		}
	}

	return "", errors.New("could not detect default base branch")
}
```

- [ ] **Step 4: Update `IsMergedInto` in `git/git.go`**

```go
// IsMergedInto reports whether branchName's tip commit is reachable from baseBranch,
// i.e. whether the branch has been merged into base (mirrors git merge-base --is-ancestor).
func (c *Client) IsMergedInto(branchName, baseBranch string) (bool, error) {
	branchRef, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+branchName), true)
	if err != nil {
		return false, fmt.Errorf("resolve branch %q: %w", branchName, err)
	}

	baseRef, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+baseBranch), true)
	if err != nil {
		// Try remote tracking branch as fallback when a remote is configured.
		remote, rErr := c.Remote()
		if rErr != nil {
			return false, fmt.Errorf("resolve remote: %w", rErr)
		}

		if remote != "" {
			baseRef, err = c.repo.Reference(plumbing.ReferenceName("refs/remotes/"+remote+"/"+baseBranch), true)
		}

		if err != nil {
			return false, fmt.Errorf("resolve base branch %q: %w", baseBranch, err)
		}
	}

	branchCommit, err := c.repo.CommitObject(branchRef.Hash())
	if err != nil {
		return false, fmt.Errorf("branch commit: %w", err)
	}

	baseCommit, err := c.repo.CommitObject(baseRef.Hash())
	if err != nil {
		return false, fmt.Errorf("base commit: %w", err)
	}

	merged, err := branchCommit.IsAncestor(baseCommit)
	if err != nil {
		return false, fmt.Errorf("is ancestor: %w", err)
	}

	return merged, nil
}
```

- [ ] **Step 5: Run all git package tests**

```bash
mise exec -- go test ./git/... -v
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "feat(git): DefaultBaseBranch and IsMergedInto use resolved remote"
```

---

### Task 6: Wire command layer — `cmd/issue/close.go`

**Files:**
- Modify: `cmd/issue/close.go` — `SetRemote` wiring + `doRebaseClose` rewrite

- [ ] **Step 1: Add `SetRemote` wiring after client creation in `closeRunE`**

Find the block (around line 66–73):
```go
client, err := git.NewClient(&pkg.IO{
    In:  cmd.InOrStdin(),
    Out: cmd.OutOrStdout(),
    Err: cmd.ErrOrStderr(),
})
if err != nil {
    return fmt.Errorf("not a git repository: %w", err)
}
```

Add immediately after the error check:
```go
if i.appConfig.Branch.Remote != "" {
    client.SetRemote(i.appConfig.Branch.Remote)
}
```

- [ ] **Step 2: Rewrite the remote-dependent section of `doRebaseClose`**

The section to replace starts at the `FetchOrigin` call and ends after the hardcoded `remoteBase` assignment (lines ~346–350). Replace this block:

```go
// OLD — remove these three lines:
if err := mc.client.FetchOrigin(ctx); err != nil {
    return fmt.Errorf("fetch origin: %w", err)
}
remoteBase := "origin/" + mc.baseBranch
```

With:
```go
remoteName, err := mc.client.Remote()
if err != nil {
    return fmt.Errorf("resolve remote: %w", err)
}

if err := mc.client.Fetch(ctx); err != nil {
    return fmt.Errorf("fetch: %w", err)
}

var remoteBase string
if remoteName != "" {
    remoteBase = remoteName + "/" + mc.baseBranch
} else {
    remoteBase = mc.baseBranch
}
```

- [ ] **Step 3: Guard the `ResolveRef` call for local-only repos**

`close.go` does not import `plumbing`, so avoid declaring a typed variable. Instead,
compute the full ref string before the single `ResolveRef` call.

Find (around line 396):
```go
baseOriginSHA, err := mc.client.ResolveRef("refs/remotes/" + remoteBase)
if err != nil {
    return fmt.Errorf("resolve %s: %w", remoteBase, err)
}
```

Replace with:
```go
baseRef := "refs/remotes/" + remoteBase
if remoteName == "" {
    baseRef = "refs/heads/" + mc.baseBranch
}
baseOriginSHA, err := mc.client.ResolveRef(baseRef)
if err != nil {
    return fmt.Errorf("resolve %s: %w", baseRef, err)
}
```

No new imports required.

- [ ] **Step 4: Build to verify no compile errors**

```bash
mise exec -- go build ./cmd/...
```

Expected: builds clean.

- [ ] **Step 5: Run all tests**

```bash
mise exec -- go test ./... 
```

Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
git add cmd/issue/close.go
git commit -m "feat(close): wire configurable remote, handle local-only fallback"
```

---

### Task 7: Wire command layer — `cmd/issue/start.go` and `cmd/branch/branch.go`

**Files:**
- Modify: `cmd/issue/start.go` — `SetRemote` wiring
- Modify: `cmd/branch/branch.go` — `SetRemote` wiring

- [ ] **Step 1: Add `SetRemote` wiring in `cmd/issue/start.go`**

Find `RunIssueStart` (around line 34):
```go
client, err := git.NewClient(nil)
if err != nil {
    return fmt.Errorf("not a git repository: %w", err)
}
```

Add immediately after the error check:
```go
if i.appConfig.Branch.Remote != "" {
    client.SetRemote(i.appConfig.Branch.Remote)
}
```

- [ ] **Step 2: Add `SetRemote` wiring in `cmd/branch/branch.go`**

Find the `branchPruneRunE` function (around line 243):
```go
c, err := git.NewClient(&pkg.IO{
    In:  cmd.InOrStdin(),
    Out: cmd.OutOrStdout(),
    Err: cmd.ErrOrStderr(),
})
if err != nil {
    return fmt.Errorf("not a git repository: %w", err)
}
```

Add immediately after the error check:
```go
if b.appConfig.Branch.Remote != "" {
    c.SetRemote(b.appConfig.Branch.Remote)
}
```

- [ ] **Step 3: Build and run all tests**

```bash
mise exec -- go build ./...
mise exec -- go test ./...
```

Expected: clean build, all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add cmd/issue/start.go cmd/branch/branch.go
git commit -m "feat(cmd): wire branch.remote config into start and prune commands"
```

---

### Task 8: Final verification

- [ ] **Step 1: Run the full test suite**

```bash
mise exec -- go test ./... -v 2>&1 | tail -30
```

Expected: all packages PASS, no `FAIL` lines.

- [ ] **Step 2: Verify no remaining hardcoded `"origin"` in non-test production code**

```bash
grep -rn '"origin"' /workspace --include="*.go" | grep -v "_test.go"
```

Expected: zero results (or only results unrelated to remote operations, such as error message strings that reference "origin" as an example).

- [ ] **Step 3: Build the binary**

```bash
mise exec -- go build -o ./bin/git-zf .
```

Expected: binary produced without errors.
