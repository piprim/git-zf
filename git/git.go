package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/config"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/piprim/git-zf/internal/pkg"
)

// CommitOptions configures Client.Commit.
type CommitOptions struct {
	All              bool
	Amend            bool
	NoVerify         bool
	Signoff          bool
	AllowEmpty       bool
	IncludeUntracked bool   // stage untracked (non-ignored) files before committing
	Author           string // "Name <email>"; empty = git config identity
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
			if r.Config().Name != "origin" {
				continue
			}

			c.remote = "origin"
			c.remoteResolved = true

			return c.remote, nil
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

// GitDir returns the absolute path of the repository's .git directory.
// For a regular repository this is "<worktree>/.git". For a submodule it is
// "<parent>/.git/modules/<name>" (because <worktree>/.git is a gitlink file,
// not a directory). For a linked worktree it is the per-worktree git dir.
// Resolved by shelling out to `git rev-parse --git-dir` to handle all forms.
func (c *Client) GitDir() (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(context.Background(), "git", "-C", root, "rev-parse", "--git-dir")

	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --git-dir: %w", err)
	}

	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(root, gitDir)
	}

	return gitDir, nil
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

// ResolveBranchRef resolves a branch short name to its commit hash. It tries
// refs/heads/<name> first, then falls back to refs/remotes/<remote>/<name> so
// that sub-task closes work when the parent integration branch was never
// checked out locally (exists only as a remote tracking ref).
func (c *Client) ResolveBranchRef(name string) (plumbing.Hash, error) {
	if h, err := c.ResolveRef("refs/heads/" + name); err == nil {
		return h, nil
	}

	remote, err := c.Remote()
	if err != nil || remote == "" {
		return plumbing.ZeroHash, fmt.Errorf("resolve branch %q: reference not found", name)
	}

	h, err := c.ResolveRef("refs/remotes/" + remote + "/" + name)
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("resolve branch %q: %w", name, err)
	}

	return h, nil
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

// Authors returns a deduplicated list of commit author identities
// ("Name <email>") from the repository history, ordered by commit count
// (most active first). Uses `git shortlog -sne --all` so it walks every
// ref instead of just HEAD's ancestry, and tolerates partial packfiles
// that trip go-git's commit iterator (e.g. submodules with a malformed
// .idx). The current git config identity is prepended as the first
// (default) entry.
func (c *Client) Authors(ctx context.Context) ([]string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return nil, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root, "shortlog", "-sne", "--all")

	out, err := cmd.Output()
	if err != nil {
		// shortlog exits non-zero on a brand-new repo with no refs.
		// Treat that as "no authors", consistent with prior behaviour.
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return []string{}, nil
		}

		return nil, fmt.Errorf("git shortlog: %w", err)
	}

	seen := make(map[string]struct{})
	var list []string
	for line := range strings.SplitSeq(string(out), "\n") {
		_, after, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}

		entry := strings.TrimSpace(after)
		if entry == "" {
			continue
		}

		if _, ok := seen[entry]; ok {
			continue
		}

		seen[entry] = struct{}{}
		list = append(list, entry)
	}

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

// stageUntracked runs `git add` on every untracked, non-ignored file so an
// --include-untracked commit picks them up. It lists paths with
// `ls-files --others --exclude-standard -z` (NUL-separated, .gitignore and
// .git/info/exclude respected) and passes each as its own argv element to
// `git add --`, so paths containing spaces are safe. An empty list is a no-op.
func (c *Client) stageUntracked(ctx context.Context, root string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", root,
		"ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return fmt.Errorf("list untracked files: %w", err)
	}

	var untracked []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" { // trailing element after the final NUL is empty
			untracked = append(untracked, p)
		}
	}
	if len(untracked) == 0 {
		return nil
	}

	args := append([]string{"-C", root, "add", "--"}, untracked...)
	if err := exec.CommandContext(ctx, "git", args...).Run(); err != nil {
		return fmt.Errorf("stage untracked files: %w", err)
	}

	return nil
}

// Commit records a commit with msg and the given options using the system git
// binary so that all configured hooks (pre-commit, commit-msg, post-commit) run.
func (c *Client) Commit(ctx context.Context, msg []byte, opts CommitOptions) error {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	if opts.IncludeUntracked {
		if err := c.stageUntracked(ctx, root); err != nil {
			return err // wrapped by stageUntracked; commit does not proceed
		}
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
//  1. Last path segment of the configured remote URL, with ".git" stripped.
//  2. Base name of the working tree root directory (local-only fallback).
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
			if r.Config().Name != remote || len(r.Config().URLs) == 0 {
				continue
			}

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

// CommitsAhead returns the number of commits in branchName that are not reachable
// from baseBranch. Uses `git rev-list --count <baseBranch>..<branchName>`.
func (c *Client) CommitsAhead(ctx context.Context, branchName, baseBranch string) (int, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return 0, fmt.Errorf("working tree root: %w", err)
	}

	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"rev-list", "--count", baseBranch+".."+branchName)
	out, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("rev-list --count %s..%s: %w", baseBranch, branchName, err)
	}

	var n int
	if _, err := fmt.Sscan(strings.TrimSpace(string(out)), &n); err != nil {
		return 0, fmt.Errorf("parse rev-list count %q: %w", strings.TrimSpace(string(out)), err)
	}

	return n, nil
}

// DeleteRemoteBranch deletes branchName on the configured remote.
// No-op when no remote is configured.
func (c *Client) DeleteRemoteBranch(ctx context.Context, branchName string) error {
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

	if err := c.runInteractive(ctx, root, "push", remote, "--delete", branchName); err != nil {
		return fmt.Errorf("delete remote branch %s: %w", branchName, err)
	}

	return nil
}

// RemoteBranchExists reports whether branchName exists on the configured remote.
// Uses git ls-remote so no fetch is required. Returns false on any error or
// when no remote is configured.
func (c *Client) RemoteBranchExists(ctx context.Context, branchName string) bool {
	remote, err := c.Remote()
	if err != nil || remote == "" {
		return false
	}
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return false
	}
	cmd := exec.CommandContext(ctx, "git", "-C", root,
		"ls-remote", "--exit-code", "--heads", remote, branchName)
	return cmd.Run() == nil
}

// DeleteLocalBranchSafe deletes branchName locally, switching to the
// configured base branch first when the current branch IS branchName
// (git refuses to delete the currently checked-out branch).
// cfgBase is used as the switch target; when empty the repo's default
// base branch (main/master) is auto-detected.
// On any checkout failure the function returns the error immediately.
func (c *Client) DeleteLocalBranchSafe(ctx context.Context, branchName string, force bool, cfgBase string) error {
	if cur, curErr := c.CurrentBranch(); curErr == nil && cur == branchName {
		base := cfgBase
		if base == "" {
			var dbErr error
			base, dbErr = c.DefaultBaseBranch()
			if dbErr != nil {
				return fmt.Errorf("detect default base before delete: %w", dbErr)
			}
		}
		if err := c.Checkout(ctx, base); err != nil {
			return fmt.Errorf("checkout %s before delete: %w", base, err)
		}
	}
	return c.DeleteLocalBranch(ctx, branchName, force)
}

// RunGitAt runs an arbitrary git command in dir with the client's IO streams.
// Exported for review subcommands that need low-level git operations.
func (c *Client) RunGitAt(ctx context.Context, dir string, args ...string) error {
	return c.runInteractive(ctx, dir, args...)
}

// ConfigUser returns the git config user identity as "Name <email>".
// Returns an empty string when not configured.
func (c *Client) ConfigUser(ctx context.Context) (string, error) {
	root, err := c.WorkingTreeRoot()
	if err != nil {
		return "", fmt.Errorf("working tree root: %w", err)
	}

	nameCmd := exec.CommandContext(ctx, "git", "-C", root, "config", "user.name")
	nameOut, err := nameCmd.Output()
	if err != nil {
		return "", nil
	}

	emailCmd := exec.CommandContext(ctx, "git", "-C", root, "config", "user.email")
	emailOut, _ := emailCmd.Output()

	name := strings.TrimSpace(string(nameOut))
	email := strings.TrimSpace(string(emailOut))
	if email != "" {
		return name + " <" + email + ">", nil
	}
	return name, nil
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

// LocalOrRemoteRef normalises a branch name into a ref usable by read-only
// operations (git merge-tree, git merge-base --is-ancestor): the bare name when
// it resolves as a local head, else "<remote>/<name>" when a remote is
// configured, else the bare name. This is the "the parent integration branch may
// exist only as a remote-tracking ref" case — a teammate's fresh clone that
// never checked the parent out locally. A BranchExists error degrades to the
// remote form (treated as not-found), matching the prior inline behaviour.
func (c *Client) LocalOrRemoteRef(name string) string {
	if exists, _ := c.BranchExists(name); exists {
		return name
	}

	if remote, _ := c.Remote(); remote != "" {
		return remote + "/" + name
	}

	return name
}

// CreateBranch creates a new branch from baseBranch and checks it out.
//
// baseBranch is resolved via ResolveBranchRef, so it may be a local head
// (refs/heads/<base>) OR a branch that exists only as a remote-tracking ref
// (refs/remotes/<remote>/<base>). The latter is the fresh-clone case: a teammate
// starts a sub-task off a parent integration branch that has been pushed but
// never checked out locally.
func (c *Client) CreateBranch(name, baseBranch string) error {
	baseHash, err := c.ResolveBranchRef(baseBranch)
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
		Hash:   baseHash,
		Branch: plumbing.ReferenceName("refs/heads/" + name),
		Create: true,
		Keep:   true,
	}); err != nil {
		return fmt.Errorf("create branch %q: %w", name, err)
	}

	return nil
}

// RemoteBranchNames returns the short names of the configured remote's tracking
// branches (refs/remotes/<remote>/*), with the "<remote>/" prefix stripped and
// the remote's HEAD symref skipped. Returns nil when no remote is configured.
//
// Used by the issue-start base picker so a parent integration branch that exists
// only on the remote (fresh clone, never checked out locally) is still offered
// as a base candidate — mirroring how LocalBranchNames feeds the local ones.
func (c *Client) RemoteBranchNames() ([]string, error) {
	remote, err := c.Remote()
	if err != nil {
		return nil, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil, nil
	}

	refs, err := c.repo.References()
	if err != nil {
		return nil, fmt.Errorf("list references: %w", err)
	}

	prefix := "refs/remotes/" + remote + "/"
	var names []string
	if err := refs.ForEach(func(ref *plumbing.Reference) error {
		// Skip the remote HEAD symref (refs/remotes/<remote>/HEAD).
		if ref.Type() == plumbing.SymbolicReference {
			return nil
		}

		full := ref.Name().String()
		if !strings.HasPrefix(full, prefix) {
			return nil
		}

		short := strings.TrimPrefix(full, prefix)
		if short == "" || short == "HEAD" {
			return nil
		}

		names = append(names, short)

		return nil
	}); err != nil {
		return nil, fmt.Errorf("iterate references: %w", err)
	}

	return names, nil
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

// ErrBranchNotMerged is returned (wrapped) by SafeDeleteBranch / DeleteLocalBranch
// when git refuses to delete the branch because its tip commit is not fully merged
// into HEAD or upstream. Detect with errors.Is.
var ErrBranchNotMerged = errors.New("git: branch not fully merged")

// SafeDeleteBranch invokes `git branch -d <name>` from the working tree root.
// On git's "not fully merged" refusal the returned error wraps ErrBranchNotMerged.
func (c *Client) SafeDeleteBranch(name string) error {
	return c.DeleteLocalBranch(context.Background(), name, false)
}

// ForceDeleteBranch invokes `git branch -D <name>` from the working tree root.
// Always destructive; safety check skipped.
func (c *Client) ForceDeleteBranch(name string) error {
	return c.DeleteLocalBranch(context.Background(), name, true)
}
