package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/piprim/git-zf/internal/pkg"
)

// CommitOptions configures Client.Commit.
type CommitOptions struct {
	All        bool
	Amend      bool
	NoVerify   bool
	Signoff    bool
	AllowEmpty bool
	Author     string // "Name <email>"; empty = git config identity
}

// Client wraps a go-git repository and exposes commit operations.
type Client struct {
	repo           *gogit.Repository
	io             *pkg.IO
	remote         string
	remoteResolved bool
}

// NewClient opens the git repository that contains the current directory.
// ioStreams configures the streams used for interactive operations; nil uses os.Stdin/Stdout/Stderr.
func NewClient(ioStreams *pkg.IO) (*Client, error) {
	repo, err := gogit.PlainOpenWithOptions(".", &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}

	return &Client{repo: repo, io: ioStreams}, nil
}

// NewClientAt opens the git repository rooted at dir.
// ioStreams configures the streams used for interactive operations; nil uses os.Stdin/Stdout/Stderr.
func NewClientAt(ioStreams *pkg.IO, dir string) (*Client, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open git repository at %s: %w", dir, err)
	}

	return &Client{repo: repo, io: ioStreams}, nil
}

// SetRemote pins the remote name used for all remote operations.
// Call this when the user has configured branch.remote explicitly.
func (c *Client) SetRemote(name string) {
	if name == "" {
		return
	}

	c.remote = name
	c.remoteResolved = true
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
	if c.remoteResolved {
		return c.remote, nil
	}

	remotes, err := c.repo.Remotes()
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}

	switch len(remotes) {
	case 0:
		c.remoteResolved = true

		return "", nil
	case 1:
		c.remote = remotes[0].Config().Name
		c.remoteResolved = true

		return c.remote, nil
	default:
		for _, r := range remotes {
			if r.Config().Name == "origin" {
				c.remote = "origin"
				c.remoteResolved = true

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

// WorkingTreeRoot returns the absolute path of the repository's working tree root.
func (c *Client) WorkingTreeRoot() (string, error) {
	wt, err := c.repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("get worktree: %w", err)
	}

	// billy.Filesystem embeds the Chroot interface which exposes Root().
	type rooter interface{ Root() string }
	if r, ok := wt.Filesystem.(rooter); ok {
		return r.Root(), nil
	}

	return "", fmt.Errorf("filesystem type %T does not expose Root()", wt.Filesystem)
}

// IO returns the injected IO streams. Callers should write status/diagnostic
// messages through these instead of os.Stdout/os.Stderr so Cobra-aware
// redirection (tests, subcommand piping, future TUI capture) keeps working.
func (c *Client) IO() *pkg.IO {
	return c.io
}

// IsDirty reports whether the working tree has tracked-file modifications or
// staged-but-uncommitted changes. Wraps `git status --porcelain --untracked-files=no`.
// Untracked files are intentionally NOT counted as dirty: `git reset --hard`
// does not touch untracked content, so their presence does not put user work
// at risk during rollback.
func (c *Client) IsDirty(ctx context.Context) (bool, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return false, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "status", "--porcelain", "--untracked-files=no")
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}

	return len(out) > 0, nil
}

// Checkout switches the working tree to branchName. Wraps `git checkout <name>`.
// Idempotent: when the working tree is already on branchName, the call is a
// no-op — the underlying `git checkout` is skipped so heavyweight `post-checkout`
// hooks don't fire for a same-branch "switch".
// Returns a wrapped error from the git CLI on failure (e.g. unknown branch,
// untracked file collision).
func (c *Client) Checkout(ctx context.Context, branchName string) error {
	current, err := c.CurrentBranch()
	if err == nil && current == branchName {
		return nil
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "checkout", branchName); err != nil {
		return fmt.Errorf("checkout %s: %w", branchName, err)
	}

	return nil
}

// ResolveRef returns the commit hash that `name` resolves to (with reference
// indirection followed). Use it for read-only ref lookups from packages that
// need a plumbing.Hash without taking a dependency on go-git's plumbing API.
func (c *Client) ResolveRef(name string) (plumbing.Hash, error) {
	ref, err := c.repo.Reference(plumbing.ReferenceName(name), true)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve ref %q: %w", name, err)
	}

	return ref.Hash(), nil
}

// CurrentBranch returns the short name of the branch HEAD points to.
// On a detached HEAD the returned name will not parse as an issue branch,
// so callers can simply ignore it.
func (c *Client) CurrentBranch() (string, error) {
	head, err := c.repo.Head()
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}

	return head.Name().Short(), nil
}

// Authors returns a deduplicated, alphabetically sorted list of commit author strings
// ("Name <email>") from the repository history.
// The current git config identity is prepended as the first (default) entry.
func (c *Client) Authors() ([]string, error) {
	iter, err := c.repo.Log(&gogit.LogOptions{})
	if err != nil {
		// empty repo (no commits yet) — not an error
		return []string{}, nil
	}

	seen := make(map[string]struct{})
	var list []string
	if err := iter.ForEach(func(commit *object.Commit) error {
		entry := commit.Author.Name + " <" + commit.Author.Email + ">"
		if _, ok := seen[entry]; !ok {
			seen[entry] = struct{}{}
			list = append(list, entry)
		}

		return nil
	}); err != nil {
		return nil, fmt.Errorf("walk commits: %w", err)
	}

	slices.Sort(list)

	cfg, err := c.repo.ConfigScoped(config.SystemScope)
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

// Commit records a commit with msg and the given options using the system git
// binary so that all configured hooks (pre-commit, commit-msg, post-commit) run.
func (c *Client) Commit(ctx context.Context, msg []byte, opts CommitOptions) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	f, err := os.CreateTemp("", "git-zf-msg-*")
	if err != nil {
		return fmt.Errorf("create temp msg file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(msg); err != nil {
		_ = f.Close()

		return fmt.Errorf("write commit msg: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close temp msg file: %w", err)
	}

	args := []string{"commit", "-F", f.Name()}
	if opts.All {
		args = append(args, "--all")
	}
	if opts.Amend {
		args = append(args, "--amend")
	}
	if opts.NoVerify {
		args = append(args, "--no-verify")
	}
	if opts.Signoff {
		args = append(args, "--signoff")
	}
	if opts.AllowEmpty {
		args = append(args, "--allow-empty")
	}
	if opts.Author != "" {
		args = append(args, "--author="+opts.Author)
	}

	if err := c.runInteractive(ctx, root, args...); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	return nil
}

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

// LocalBranchNames returns the short names of all local branches.
func (c *Client) LocalBranchNames() ([]string, error) {
	iter, err := c.repo.Branches()
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	var names []string
	if err := iter.ForEach(func(ref *plumbing.Reference) error {
		names = append(names, ref.Name().Short())

		return nil
	}); err != nil {
		return nil, fmt.Errorf("iterate branches: %w", err)
	}

	return names, nil
}

// RepoName returns a short identifier for this repository.
// Resolution order:
//
//	1. Last path segment of the configured remote URL, with ".git" stripped.
//	2. Base name of the working tree root directory (local-only fallback).
func (c *Client) RepoName() (string, error) {
	remote, err := c.Remote()
	if err != nil {
		return "", fmt.Errorf("resolve remote: %w", err)
	}

	if remote != "" {
		remotes, err := c.repo.Remotes()
		if err != nil {
			return "", fmt.Errorf("list remotes: %w", err)
		}

		for _, r := range remotes {
			if r.Config().Name == remote && len(r.Config().URLs) > 0 {
				u := r.Config().URLs[0]
				// Strip trailing slashes then take last segment.
				u = strings.TrimRight(u, "/")
				seg := u[strings.LastIndexAny(u, "/:")+1:]
				seg = strings.TrimSuffix(seg, ".git")

				if seg != "" {
					return seg, nil
				}
			}
		}
	}

	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	return filepath.Base(root), nil
}

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

// BranchExists returns true if refs/heads/<name> resolves locally. It does
// not consult remotes — see resolveBranchConflict for the rationale (no
// fetch on the happy path of `issue start`).
func (c *Client) BranchExists(name string) (bool, error) {
	_, err := c.repo.Reference(plumbing.NewBranchReferenceName(name), false)
	if err == nil {
		return true, nil
	}

	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return false, nil
	}

	return false, fmt.Errorf("lookup branch %q: %w", name, err)
}

// CreateBranch creates a new branch from baseBranch and checks it out.
func (c *Client) CreateBranch(name, baseBranch string) error {
	baseRef, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+baseBranch), true)
	if err != nil {
		return fmt.Errorf("resolve base branch %q: %w", baseBranch, err)
	}

	wt, err := c.repo.Worktree()
	if err != nil {
		return fmt.Errorf("get worktree: %w", err)
	}

	// Hash and Create together are intentional: go-git sets HEAD to the symbolic
	// ref (Branch) when Create is true, and uses Hash as the starting commit for
	// the new branch ref.
	if err := wt.Checkout(&gogit.CheckoutOptions{
		Hash:   baseRef.Hash(),
		Branch: plumbing.ReferenceName("refs/heads/" + name),
		Create: true,
		Keep:   true,
	}); err != nil {
		return fmt.Errorf("create branch %q: %w", name, err)
	}

	return nil
}

// CreateWorktree creates a new branch from baseBranch and checks it out
// in a linked worktree at path. Wraps `git worktree add -b <branch> <path> <base>`.
func (c *Client) CreateWorktree(ctx context.Context, branchName, baseBranch, path string) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if err := c.runInteractive(ctx, root, "worktree", "add", "-b", branchName, path, baseBranch); err != nil {
		return fmt.Errorf("create worktree %q: %w", path, err)
	}

	return nil
}
