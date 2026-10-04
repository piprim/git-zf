# CLI Restructure & go-git Migration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restructure `git cz` into `git cz commit` (backed by go-git v6) and `git cz issue` (stub), replacing all subprocess git calls in `git/git.go` with go-git, and extending the TUI commit form with a second group for commit options.

**Architecture:** Bottom-up — rewrite `git/` first (TDD with in-memory repos), then extend `commit/` form (TDD), then create new `cmd/` files, then strip `cmd/root.go` to a dispatcher. Each task leaves the codebase in a buildable state.

**Tech Stack:** `github.com/go-git/go-git/v6`, `github.com/go-git/go-billy/v5` (memfs for tests), `github.com/charmbracelet/huh`, `github.com/spf13/cobra`

---

## File Map

| File | Action | Responsibility |
|---|---|---|
| `go.mod` / `go.sum` | Modify | Add `go-git/v6` + `go-billy/v5` |
| `git/git.go` | Rewrite | go-git repo ops + `CommitOptions` + `Authors()`; keep `InstallSubCmd`/`execPath`/`copyFile` |
| `git/git_test.go` | Create | In-memory repo tests: basic commit, `--all` semantics, signoff, author, amend |
| `commit/form.go` | Extend | Add `FormOptions`, `BuildAuthorList()`; update `FillOutForm()` signature; add Group 2 |
| `commit/form_test.go` | Extend | Tests for `BuildAuthorList` + `FormOptions.anyOptionSet()` |
| `cmd/root.go` | Refactor | Strip to dispatcher; `initConfig()` no longer requires a git repo |
| `cmd/commit.go` | Create | `git cz commit` subcommand, all flags, wires form + git |
| `cmd/issue.go` | Create | `git cz issue` stub with full Cobra definition |

---

## Task 1: Add go-git dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add go-git v6 and go-billy**

```bash
cd /workspace
go get github.com/go-git/go-git/v6
go get github.com/go-git/go-billy/v5
```

> **Note:** If `go-git/v6` is not yet published on pkg.go.dev, run `go get github.com/go-git/go-git/v6@main` or check the latest tagged version. All import paths in subsequent tasks must match the version installed here.

- [ ] **Step 2: Verify build still compiles**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add go-git/v6 and go-billy/v5 dependencies"
```

---

## Task 2: Rewrite `git/git.go` with go-git (TDD)

**Files:**
- Create: `git/git_test.go`
- Modify: `git/git.go`

### Step 2a — Write failing tests first

- [ ] **Step 1: Create `git/git_test.go`**

```go
package git

import (
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage/memory"
	// Note: if go-git v6 ships its own memfs, update this import path accordingly.
	"github.com/go-git/go-billy/v5/memfs"
)

// newTestRepo creates an in-memory git repository with one initial commit.
// User is configured as "Test User <test@example.com>".
func newTestRepo(t *testing.T) *gogit.Repository {
	t.Helper()
	repo, err := gogit.Init(memory.NewStorage(), memfs.New())
	if err != nil {
		t.Fatalf("init in-memory repo: %v", err)
	}

	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}

	f, err := wt.Filesystem.Create("README.md")
	if err != nil {
		t.Fatalf("create README.md: %v", err)
	}
	_, _ = f.Write([]byte("# test"))
	_ = f.Close()

	if _, err := wt.Add("README.md"); err != nil {
		t.Fatalf("stage README.md: %v", err)
	}

	_, err = wt.Commit("chore: init", &gogit.CommitOptions{
		Author: &object.Signature{
			Name:  "Test User",
			Email: "test@example.com",
			When:  time.Now(),
		},
	})
	if err != nil {
		t.Fatalf("initial commit: %v", err)
	}

	return repo
}

// withRepo overrides the package-level openRepoFn for the duration of t.
func withRepo(t *testing.T, repo *gogit.Repository) {
	t.Helper()
	orig := openRepoFn
	openRepoFn = func() (*gogit.Repository, error) { return repo, nil }
	t.Cleanup(func() { openRepoFn = orig })
}

// stageNewFile creates filename in wt, writes content, and stages it.
func stageNewFile(t *testing.T, wt *gogit.Worktree, filename, content string) {
	t.Helper()
	f, err := wt.Filesystem.Create(filename)
	if err != nil {
		t.Fatalf("create %s: %v", filename, err)
	}
	_, _ = f.Write([]byte(content))
	_ = f.Close()
	if _, err := wt.Add(filename); err != nil {
		t.Fatalf("stage %s: %v", filename, err)
	}
}

func TestCommit_basic(t *testing.T) {
	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "hello")
	withRepo(t, repo)

	if err := Commit([]byte("feat: basic commit"), &CommitOptions{}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, _ := repo.Head()
	c, _ := repo.CommitObject(ref.Hash())
	if c.Message != "feat: basic commit" {
		t.Errorf("got message %q, want %q", c.Message, "feat: basic commit")
	}
}

func TestCommit_all_stagesTrackedOnly(t *testing.T) {
	repo := newTestRepo(t)
	wt, _ := repo.Worktree()

	// Modify the tracked file (README.md was in the initial commit).
	f, _ := wt.Filesystem.Create("README.md")
	_, _ = f.Write([]byte("# modified"))
	_ = f.Close()

	// Create an untracked file — must NOT end up in the commit.
	u, _ := wt.Filesystem.Create("untracked.txt")
	_, _ = u.Write([]byte("should not be staged"))
	_ = u.Close()

	withRepo(t, repo)

	if err := Commit([]byte("chore: all flag"), &CommitOptions{All: true}); err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, _ := repo.Head()
	c, _ := repo.CommitObject(ref.Hash())
	tree, _ := repo.TreeObject(c.TreeHash)

	if _, err := tree.File("README.md"); err != nil {
		t.Error("README.md not found in commit tree")
	}
	if _, err := tree.File("untracked.txt"); err == nil {
		t.Error("untracked.txt must not be in commit tree")
	}
}

func TestCommit_signoff(t *testing.T) {
	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "x")
	withRepo(t, repo)

	err := Commit([]byte("docs: readme"), &CommitOptions{
		Signoff: true,
		Author:  "Alice Dev <alice@example.com>",
	})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, _ := repo.Head()
	c, _ := repo.CommitObject(ref.Hash())
	if !strings.Contains(c.Message, "Signed-off-by: Alice Dev <alice@example.com>") {
		t.Errorf("signoff trailer not found in: %q", c.Message)
	}
}

func TestCommit_author(t *testing.T) {
	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "x")
	withRepo(t, repo)

	err := Commit([]byte("fix: author override"), &CommitOptions{
		Author: "Bob Builder <bob@example.com>",
	})
	if err != nil {
		t.Fatalf("Commit error: %v", err)
	}

	ref, _ := repo.Head()
	c, _ := repo.CommitObject(ref.Hash())
	if c.Author.Name != "Bob Builder" || c.Author.Email != "bob@example.com" {
		t.Errorf("author: got %q <%s>, want Bob Builder <bob@example.com>",
			c.Author.Name, c.Author.Email)
	}
}

func TestCommit_amend(t *testing.T) {
	repo := newTestRepo(t)
	wt, _ := repo.Worktree()
	stageNewFile(t, wt, "file.txt", "x")
	withRepo(t, repo)

	// Second commit — the one we will amend.
	_ = Commit([]byte("feat: to be amended"), &CommitOptions{})

	iter, _ := repo.Log(&gogit.LogOptions{})
	countBefore := 0
	_ = iter.ForEach(func(_ *object.Commit) error { countBefore++; return nil })

	// Amend: replace the tip commit message.
	if err := Commit([]byte("feat: amended message"), &CommitOptions{Amend: true}); err != nil {
		t.Fatalf("amend error: %v", err)
	}

	iter2, _ := repo.Log(&gogit.LogOptions{})
	countAfter := 0
	_ = iter2.ForEach(func(_ *object.Commit) error { countAfter++; return nil })
	if countAfter != countBefore {
		t.Errorf("commit count changed: %d → %d (expected no change)", countBefore, countAfter)
	}

	ref, _ := repo.Head()
	c, _ := repo.CommitObject(ref.Hash())
	if c.Message != "feat: amended message" {
		t.Errorf("tip message after amend: got %q", c.Message)
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./git/... -v 2>&1 | head -20
```

Expected: compile errors — `CommitOptions`, `openRepoFn` not yet defined.

### Step 2b — Implement `git/git.go`

- [ ] **Step 3: Replace `git/git.go` with this content**

```go
package git

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// CommitOptions configures git.Commit.
type CommitOptions struct {
	All        bool
	Amend      bool
	NoVerify   bool
	Signoff    bool
	AllowEmpty bool
	Author     string // "Name <email>"; empty = git config identity
}

// openRepoFn opens the current repository. Swappable in tests.
var openRepoFn = defaultOpenRepo

func defaultOpenRepo() (*gogit.Repository, error) {
	repo, err := gogit.PlainOpenWithOptions(".", &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}

	return repo, nil
}

// IsCurrentDirectoryGitRepo reports whether the current directory is inside a git repository.
func IsCurrentDirectoryGitRepo() (bool, error) {
	_, err := openRepoFn()
	if err != nil {
		return false, err
	}

	return true, nil
}

// WorkingTreeRoot returns the absolute path of the repository's working tree root.
func WorkingTreeRoot() (string, error) {
	repo, err := openRepoFn()
	if err != nil {
		return "", err
	}

	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("get worktree: %w", err)
	}

	// billy.Filesystem implementations for local repos expose Root() via this interface.
	type rooter interface{ Root() string }
	if r, ok := wt.Filesystem.(rooter); ok {
		return r.Root(), nil
	}

	return "", fmt.Errorf("filesystem does not expose root path")
}

// Authors returns a deduplicated, alphabetically sorted list of commit author strings
// ("Name <email>") from the repository history.
// The current git config identity is prepended as the first (default) entry.
func Authors() ([]string, error) {
	repo, err := openRepoFn()
	if err != nil {
		return nil, err
	}

	iter, err := repo.Log(&gogit.LogOptions{})
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}

	seen := make(map[string]struct{})
	var list []string
	_ = iter.ForEach(func(c *object.Commit) error {
		entry := c.Author.Name + " <" + c.Author.Email + ">"
		if _, ok := seen[entry]; !ok {
			seen[entry] = struct{}{}
			list = append(list, entry)
		}

		return nil
	})

	sort.Strings(list)

	cfg, err := repo.Config()
	if err == nil && cfg.User.Name != "" {
		current := cfg.User.Name + " <" + cfg.User.Email + ">"
		filtered := make([]string, 0, len(list))
		for _, a := range list {
			if a != current {
				filtered = append(filtered, a)
			}
		}
		list = append([]string{current}, filtered...)
	}

	return list, nil
}

// Commit records a commit with msg and the given options.
func Commit(msg []byte, opts *CommitOptions) error {
	repo, err := openRepoFn()
	if err != nil {
		return err
	}

	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	if opts.All {
		if err := wt.AddWithOptions(&gogit.AddOptions{All: true}); err != nil {
			return fmt.Errorf("stage files: %w", err)
		}
	}

	finalMsg := string(msg)
	if opts.Signoff {
		signer := opts.Author
		if signer == "" {
			if cfg, err := repo.Config(); err == nil && cfg.User.Name != "" {
				signer = cfg.User.Name + " <" + cfg.User.Email + ">"
			}
		}
		if signer != "" {
			finalMsg = strings.TrimRight(finalMsg, "\n") + "\n\nSigned-off-by: " + signer
		}
	}

	commitOpts := &gogit.CommitOptions{
		AllowEmptyCommits: opts.AllowEmpty,
		Amend:             opts.Amend,
		// Note: verify the exact field name for no-verify hooks in go-git v6.
		// It may be NoVerifyHooks, SkipHooks, or similar. Uncomment once confirmed:
		// NoVerifyHooks: opts.NoVerify,
	}

	if opts.Author != "" {
		name, email := parseAuthor(opts.Author)
		commitOpts.Author = &object.Signature{
			Name:  name,
			Email: email,
			When:  time.Now(),
		}
	}

	if _, err = wt.Commit(finalMsg, commitOpts); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

// parseAuthor splits "Name <email>" into name and email parts.
func parseAuthor(s string) (name, email string) {
	lt := strings.LastIndex(s, "<")
	gt := strings.LastIndex(s, ">")
	if lt >= 0 && gt > lt {
		return strings.TrimSpace(s[:lt]), s[lt+1 : gt]
	}

	return s, ""
}

// InstallSubCmd copies srcFilePath into Git's exec path as "git-<subCmdName>".
func InstallSubCmd(srcFilePath, subCmdName string) (string, error) {
	dstDir, err := execPath()
	if err != nil {
		return "", err
	}

	subCmdFileName := "git-" + subCmdName
	dstFilePath := filepath.Join(dstDir, subCmdFileName)
	if _, err := copyFile(dstFilePath, srcFilePath); err != nil {
		return dstFilePath, err
	}

	return dstFilePath, nil
}

func copyFile(dstName, srcName string) (written int64, err error) {
	src, err := os.Open(srcName)
	if err != nil {
		return
	}
	defer func() { _ = src.Close() }()

	dst, err := os.OpenFile(dstName, os.O_WRONLY|os.O_CREATE, 0755)
	if err != nil {
		return
	}
	defer func() { _ = dst.Close() }()

	return io.Copy(dst, src)
}

func execPath() (string, error) {
	cmd := exec.Command("git", "--exec-path")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("exec-path pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("exec-path start: %w", err)
	}
	result, err := io.ReadAll(stdout)
	if err != nil {
		return "", fmt.Errorf("exec-path read: %w", err)
	}
	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("exec-path wait: %w", err)
	}

	return strings.TrimSpace(string(result)), nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./git/... -v
```

Expected: all 5 tests pass.

> If `TestCommit_amend` fails because go-git v6 `CommitOptions` lacks an `Amend` field: remove `Amend: opts.Amend` from `commitOpts`, add a comment `// TODO: amend not yet supported by go-git v6`, and mark the test with `t.Skip("amend not yet supported by go-git v6")`.

- [ ] **Step 5: Commit**

```bash
git add git/git.go git/git_test.go
git commit -m "refactor(git): replace subprocess calls with go-git/v6"
```

---

## Task 3: Add `FormOptions` and `BuildAuthorList` to `commit/` (TDD)

**Files:**
- Modify: `commit/form_test.go`
- Modify: `commit/form.go`

### Step 3a — Write failing tests

- [ ] **Step 1: Add tests to `commit/form_test.go`**

Append after the last `}` in `form_test.go`:

```go
func TestBuildAuthorList_deduplication(t *testing.T) {
	all := []string{
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"Alice <alice@example.com>",
	}
	got := BuildAuthorList(all, "")
	want := []string{"Alice <alice@example.com>", "Bob <bob@example.com>"}
	if len(got) != len(want) {
		t.Fatalf("len: got %d, want %d — list: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestBuildAuthorList_sortOrder(t *testing.T) {
	all := []string{
		"Zoe <zoe@example.com>",
		"Alice <alice@example.com>",
		"Mia <mia@example.com>",
	}
	got := BuildAuthorList(all, "")
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Errorf("not sorted at [%d]: %q < %q", i, got[i], got[i-1])
		}
	}
}

func TestBuildAuthorList_currentUserFirst(t *testing.T) {
	all := []string{
		"Alice <alice@example.com>",
		"Bob <bob@example.com>",
		"Current User <current@example.com>",
	}
	current := "Current User <current@example.com>"
	got := BuildAuthorList(all, current)

	if len(got) == 0 {
		t.Fatal("empty list")
	}
	if got[0] != current {
		t.Errorf("first entry: got %q, want %q", got[0], current)
	}
	// current must not appear again elsewhere in the list
	for _, a := range got[1:] {
		if a == current {
			t.Errorf("current user duplicated in list: %v", got)
		}
	}
}

func TestBuildAuthorList_currentUserNotInHistory(t *testing.T) {
	all := []string{"Alice <alice@example.com>", "Bob <bob@example.com>"}
	current := "New User <new@example.com>"
	got := BuildAuthorList(all, current)
	if got[0] != current {
		t.Errorf("first entry: got %q, want %q", got[0], current)
	}
	if len(got) != 3 {
		t.Errorf("len: got %d, want 3 — list: %v", len(got), got)
	}
}

func TestFormOptions_anyOptionSet(t *testing.T) {
	cases := []struct {
		opts FormOptions
		want bool
	}{
		{FormOptions{}, false},
		{FormOptions{All: true}, true},
		{FormOptions{Amend: true}, true},
		{FormOptions{NoVerify: true}, true},
		{FormOptions{Signoff: true}, true},
		{FormOptions{AllowEmpty: true}, true},
		{FormOptions{Author: "Alice <a@b.com>"}, true},
	}
	for _, tc := range cases {
		if got := tc.opts.anyOptionSet(); got != tc.want {
			t.Errorf("%+v: anyOptionSet()=%v, want %v", tc.opts, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run to confirm they fail**

```bash
go test ./commit/... -v -run "TestBuildAuthorList|TestFormOptions" 2>&1 | head -20
```

Expected: compile errors — `BuildAuthorList` and `FormOptions` not yet defined.

### Step 3b — Implement

- [ ] **Step 3: Add `FormOptions` and `BuildAuthorList` to `commit/form.go`**

Add after the `MessageConfig` struct (before `assembleMessage`):

```go
// FormOptions holds commit option values for Group 2 of the TUI.
// Used both as flag-derived defaults (input) and as user selections (output).
type FormOptions struct {
	All        bool
	Amend      bool
	NoVerify   bool
	Signoff    bool
	AllowEmpty bool
	Author     string // "Name <email>"
}

// anyOptionSet reports true if any commit-option flag was passed.
// When true, Group 2 of the TUI is skipped.
func (o FormOptions) anyOptionSet() bool {
	return o.All || o.Amend || o.NoVerify || o.Signoff || o.AllowEmpty || o.Author != ""
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
	sort.Strings(unique)

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
```

Also add `"sort"` to the import block in `commit/form.go`.

- [ ] **Step 4: Run tests**

```bash
go test ./commit/... -v -run "TestBuildAuthorList|TestFormOptions"
```

Expected: all new tests pass. Existing `TestAssembleMessage` still passes.

- [ ] **Step 5: Commit**

```bash
git add commit/form.go commit/form_test.go
git commit -m "feat(commit): add FormOptions and BuildAuthorList"
```

---

## Task 4: Extend `commit/form.go` with Group 2 and new `FillOutForm` signature

**Files:**
- Modify: `commit/form.go`

- [ ] **Step 1: Update `FillOutForm` signature and add Group 2**

Replace the existing `FillOutForm` function and update `loadForm`:

```go
// FillOutForm presents the commit TUI form.
// Group 1: commit message fields (type, scope, subject, body, footer).
// Group 2: commit options (author, all, amend, no-verify, signoff, allow-empty).
//   Group 2 is skipped when defaults.anyOptionSet() is true (flags were passed).
// Returns the assembled commit message bytes and the (possibly user-modified) options.
func FillOutForm(defaults FormOptions, authors []string) ([]byte, FormOptions, error) {
	form, extractMsg, extractOpts, tmplText, err := loadForm(defaults, authors)
	if err != nil {
		log.Printf("loadForm failed, err=%v\n", err)
		return nil, FormOptions{}, err
	}

	if err := form.Run(); err != nil {
		return nil, FormOptions{}, fmt.Errorf("failed to run the form: %w", err)
	}

	answers := extractMsg()
	opts := extractOpts()

	var buf bytes.Buffer
	if err := assembleMessage(&buf, tmplText, answers); err != nil {
		log.Printf("assemble failed, err=%v\n", err)
		return nil, FormOptions{}, err
	}

	return buf.Bytes(), opts, nil
}
```

Replace `loadForm` with this version that accepts defaults and authors:

```go
func loadForm(defaults FormOptions, authors []string) (*huh.Form, func() map[string]any, func() FormOptions, string, error) {
	config := struct{ Message MessageConfig }{}
	if err := json.Unmarshal([]byte(defaultConfig), &config); err != nil {
		return nil, nil, nil, "", fmt.Errorf("failed to unmarshal: %w", err)
	}

	msgConfig := config.Message
	log.Printf("default config message tmpl: %s", msgConfig.Template)

	sub := viper.Sub("message")
	if sub == nil {
		log.Print("no message in config file")
	} else {
		if err := sub.Unmarshal(&msgConfig, func(cfg *mapstructure.DecoderConfig) { cfg.ZeroFields = true }); err != nil {
			log.Printf("ill message in config file, err=%v", err)
		}
	}

	// --- Group 1: commit message fields ---
	values := make([]string, len(msgConfig.Items))
	msgFields := make([]huh.Field, 0, len(msgConfig.Items))
	requireValidator := func(s string) error {
		if strings.TrimSpace(s) == "" {
			return errors.New("required")
		}

		return nil
	}

	var style = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#888888")).
		PaddingLeft(1)

	for i, item := range msgConfig.Items {
		switch item.Form {
		case "select":
			opts := make([]huh.Option[string], len(item.Options))
			for j, opt := range item.Options {
				name := titleCase(opt.Name)
				opts[j] = huh.NewOption(name+"\n"+style.Render(opt.Desc), opt.Name)
			}
			sel := huh.NewSelect[string]().
				Title(item.Desc).
				Options(opts...).
				Value(&values[i])
			if item.Required {
				sel = sel.Validate(requireValidator)
			}
			msgFields = append(msgFields, sel)
		case "input":
			inp := huh.NewInput().
				Title(titleCase(item.Name) + ":").
				Placeholder(item.Desc).
				Value(&values[i])
			if item.Required {
				inp = inp.Validate(requireValidator)
			}
			msgFields = append(msgFields, inp)
		case "multiline":
			txt := huh.NewText().
				Lines(2).
				Title(strings.ToTitle(item.Name)).
				Placeholder(item.Desc).
				Value(&values[i])
			if item.Required {
				txt = txt.Validate(requireValidator)
			}
			msgFields = append(msgFields, txt)
		default:
			log.Printf("unknown form type %q for item %q, skipping", item.Form, item.Name)
		}
	}

	items := msgConfig.Items
	extractMsg := func() map[string]any {
		m := make(map[string]any, len(items))
		for i, item := range items {
			m[item.Name] = values[i]
		}

		return m
	}

	groups := []*huh.Group{huh.NewGroup(msgFields...)}

	// --- Group 2: commit options (skipped when any flag was passed) ---
	opts := defaults
	if !defaults.anyOptionSet() {
		authorOpts := make([]huh.Option[string], len(authors))
		for i, a := range authors {
			authorOpts[i] = huh.NewOption(a, a)
		}
		if len(authorOpts) == 0 {
			authorOpts = []huh.Option[string]{huh.NewOption("(no authors found)", "")}
		}

		authorSel := huh.NewSelect[string]().
			Title("Author:").
			Options(authorOpts...).
			Value(&opts.Author)

		optFields := []huh.Field{
			authorSel,
			huh.NewConfirm().Title("Stage all tracked modified/deleted files? (--all)").Value(&opts.All),
			huh.NewConfirm().Title("Amend last commit? (--amend)").Value(&opts.Amend),
			huh.NewConfirm().Title("Skip hooks? (--no-verify)").Value(&opts.NoVerify),
			huh.NewConfirm().Title("Add Signed-off-by trailer? (--signoff)").Value(&opts.Signoff),
			huh.NewConfirm().Title("Allow empty commit? (--allow-empty)").Value(&opts.AllowEmpty),
		}
		groups = append(groups, huh.NewGroup(optFields...))
	}

	extractOpts := func() FormOptions { return opts }

	return huh.NewForm(groups...), extractMsg, extractOpts, msgConfig.Template, nil
}
```

- [ ] **Step 2: Build to confirm no compile errors**

```bash
go build ./...
```

Expected: compile errors in `cmd/root.go` because `commit.FillOutForm()` signature changed — that is expected and will be fixed in Task 7.

Run just the commit package tests:

```bash
go test ./commit/... -v
```

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add commit/form.go
git commit -m "feat(commit): add Group 2 (commit options) to TUI form"
```

---

## Task 5: Create `cmd/commit.go`

**Files:**
- Create: `cmd/commit.go`

- [ ] **Step 1: Create `cmd/commit.go`**

```go
package cmd

import (
	"fmt"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
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
var CommitCmd = &cobra.Command{
	Use:   "commit",
	Short: "Record changes to the repository",
	Long:  "Open the commitizen TUI to compose a standardised commit message, then commit using go-git.",
	RunE:  commitRunE,
}

func init() {
	f := CommitCmd.Flags()
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
}

func commitRunE(_ *cobra.Command, _ []string) error {
	if _, err := git.IsCurrentDirectoryGitRepo(); err != nil {
		return fmt.Errorf("does not seem to be a git repo: %w", err)
	}

	defaults := commit.FormOptions{
		All:        commitAll,
		Amend:      commitAmend,
		NoVerify:   commitNoVerify,
		Signoff:    commitSignoff,
		AllowEmpty: commitAllowEmpty,
		Author:     commitAuthor,
	}

	authors, err := git.Authors()
	if err != nil {
		authors = []string{} // graceful degradation: empty list shows no author select
	}

	msg, opts, err := commit.FillOutForm(defaults, authors)
	if err != nil {
		return err
	}

	return git.Commit(msg, &git.CommitOptions{
		All:        opts.All,
		Amend:      opts.Amend,
		NoVerify:   opts.NoVerify,
		Signoff:    opts.Signoff,
		AllowEmpty: opts.AllowEmpty,
		Author:     opts.Author,
	})
}
```

- [ ] **Step 2: Build the cmd package**

```bash
go build ./cmd/...
```

Expected: no errors (root.go error from Task 4 is still present but isolated).

---

## Task 6: Create `cmd/issue.go`

**Files:**
- Create: `cmd/issue.go`

- [ ] **Step 1: Create `cmd/issue.go`**

```go
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

// IssueCmd is the "git cz issue" subcommand stub.
// Full implementation is in Step 2 (tracker integration spec).
var IssueCmd = &cobra.Command{
	Use:   "issue",
	Short: "Start work on an issue (pick issue → create branch)",
	Long: `Browse issues from the configured tracker (Plane, Redmine, …),
select one, and create a properly named feature branch.

Not yet implemented — coming in Step 2.`,
	RunE: issueRunE,
}

func issueRunE(_ *cobra.Command, _ []string) error {
	fmt.Println("not yet implemented")
	return nil
}
```

---

## Task 7: Refactor `cmd/root.go` to a pure dispatcher

**Files:**
- Modify: `cmd/root.go`

- [ ] **Step 1: Replace `cmd/root.go`**

```go
package cmd

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/piprim/git-zf/git"
	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var isDebug bool

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

	rootCmd.AddCommand(CommitCmd, IssueCmd, VersionCmd, InstallCmd)

	return rootCmd, nil
}

// initConfig sets up logging and loads the .git-zf.json config file via Viper.
// Not being inside a git repo is not a fatal error here — git cz version/install must work anywhere.
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

	viper.SetConfigName(".git-zf")
	viper.SetConfigType("json")

	// Repo-root config takes priority over home: add it first so Viper searches it first.
	workingTreeRoot, err := git.WorkingTreeRoot()
	if err == nil && workingTreeRoot != "" {
		viper.AddConfigPath(workingTreeRoot)
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

	return nil
}
```

- [ ] **Step 2: Build the entire project**

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 3: Run all tests**

```bash
go test ./...
```

Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/root.go cmd/commit.go cmd/issue.go
git commit -m "feat: restructure CLI into git-cz commit/issue subcommands"
```

---

## Task 8: Final verification

- [ ] **Step 1: Build the binary**

```bash
go build -o commitizen-go .
```

Expected: binary produced with no errors.

- [ ] **Step 2: Smoke-test help output**

```bash
./commitizen-go --help
```

Expected output includes:
```
Available Commands:
  commit      Record changes to the repository
  install     Install this tool to git-core as git-cz
  issue       Start work on an issue (pick issue → create branch)
  version     ...
```

```bash
./commitizen-go commit --help
```

Expected: shows `--all`, `--amend`, `--no-verify`, `--signoff`, `--allow-empty`, `--author` flags.

```bash
./commitizen-go issue
```

Expected: prints `not yet implemented`.

- [ ] **Step 3: Run full test suite**

```bash
go test ./... -v
```

Expected: all tests pass, no skipped tests (unless `TestCommit_amend` is skipped with a note per Task 2 instructions).

- [ ] **Step 4: Final commit**

```bash
git add .
git commit -m "chore: final build verification — CLI restructure + go-git migration complete"
```

---

## Implementation Notes

1. **go-git v6 API verification:** The field names `CommitOptions.Amend`, `CommitOptions.NoVerifyHooks`, and `gogit.AddOptions.All` should be verified against the actual go-git v6 source during Task 2. Use `go doc github.com/go-git/go-git/v6` or read the module source.

2. **go-billy memfs import:** If go-git v6 ships its own filesystem package (not go-billy/v5), update the import in `git/git_test.go` to match.

3. **`viper.AddConfigPath` order:** Viper finds the first config file when searching paths in the order they were added. Repo root is added first (higher priority), home is added second (fallback). This matches the original behaviour.

4. **No-verify hooks:** go-git v5 does not run hooks at all. go-git v6 may run them by default. Once the `NoVerify` field name is confirmed, uncomment the relevant line in `git.Commit()`.
