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

	"github.com/piprim/git-zf/internal/pkg"
)

// Hash is a git object id as printed by `git rev-parse`: lowercase hex, 40
// characters for SHA-1 repositories.
type Hash string

// ZeroHash is the null object id, returned alongside an error by the
// Resolve* methods.
const ZeroHash Hash = "0000000000000000000000000000000000000000"

// String returns the hex form.
func (h Hash) String() string { return string(h) }

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

// Client drives the system git binary against one working tree.
type Client struct {
	root           string // absolute working-tree root
	io             *pkg.IO
	remote         string
	remoteResolved bool
}

// NewClient opens the git repository that contains the current directory.
// ioStreams configures the streams used for interactive operations; nil uses os.Stdin/Stdout/Stderr.
func NewClient(ioStreams *pkg.IO) (*Client, error) {
	c, err := NewClientAt(ioStreams, ".")
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}

	return c, nil
}

// NewClientAt opens the git repository whose working tree contains dir.
// ioStreams configures the streams used for interactive operations; nil uses os.Stdin/Stdout/Stderr.
func NewClientAt(ioStreams *pkg.IO, dir string) (*Client, error) {
	out, err := exec.CommandContext(context.Background(), "git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("open git repository at %s: %w", dir, gitStderr(err))
	}

	return &Client{root: strings.TrimSpace(string(out)), io: ioStreams}, nil
}

// gitCmd builds `git -C <root> args...`.
func (c *Client) gitCmd(ctx context.Context, args ...string) *exec.Cmd {
	//nolint:gosec // args are subcommands and ref names assembled by this package
	return exec.CommandContext(ctx, "git", append([]string{"-C", c.root}, args...)...)
}

// output runs `git -C <root> args...` and returns its stdout with surrounding
// whitespace trimmed. A non-zero exit yields an error carrying git's stderr.
func (c *Client) output(ctx context.Context, args ...string) (string, error) {
	out, err := c.gitCmd(ctx, args...).Output()
	if err != nil {
		return "", gitStderr(err)
	}

	return strings.TrimSpace(string(out)), nil
}

// succeeds runs `git -C <root> args...` for its yes/no exit status: exit 0 is
// true, exit 1 is false, anything else is an error.
func (c *Client) succeeds(ctx context.Context, args ...string) (bool, error) {
	_, err := c.gitCmd(ctx, args...).Output()

	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil
	default:
		return false, gitStderr(err)
	}
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

	out, err := c.output(context.Background(), "remote")
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}

	names := strings.Fields(out)

	switch len(names) {
	case 0:
		c.remoteResolved = true

		return "", nil
	case 1:
		c.remote = names[0]
		c.remoteResolved = true

		return c.remote, nil
	default:
		if slices.Contains(names, "origin") {
			c.remote = "origin"
			c.remoteResolved = true

			return c.remote, nil
		}

		return "", fmt.Errorf("multiple remotes found (%s); set branch.remote in .git-zf.toml",
			strings.Join(names, ", "))
	}
}

// WorkingTreeRoot returns the absolute path of the repository's working tree root.
func (c *Client) WorkingTreeRoot() string {
	return c.root
}

// GitDir returns the absolute path of the repository's .git directory.
// For a regular repository this is "<worktree>/.git". For a submodule it is
// "<parent>/.git/modules/<name>" (because <worktree>/.git is a gitlink file,
// not a directory). For a linked worktree it is the per-worktree git dir.
// Resolved by shelling out to `git rev-parse --git-dir` to handle all forms.
func (c *Client) GitDir() (string, error) {
	return c.revParsePath("--git-dir")
}

// revParsePath resolves `git rev-parse <flag>` (--git-dir, --git-common-dir)
// to an absolute path.
func (c *Client) revParsePath(flag string) (string, error) {
	dir, err := c.output(context.Background(), "rev-parse", flag)
	if err != nil {
		return "", fmt.Errorf("git rev-parse %s: %w", flag, err)
	}

	if !filepath.IsAbs(dir) {
		dir = filepath.Join(c.root, dir)
	}

	return dir, nil
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
	out, err := c.output(ctx, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, fmt.Errorf("git status: %w", err)
	}

	return out != "", nil
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

	root := c.root

	if err := c.runInteractive(ctx, root, "checkout", branchName); err != nil {
		return fmt.Errorf("checkout %s: %w", branchName, err)
	}

	return nil
}

// ResolveRef returns the commit hash that `name` resolves to (with reference
// indirection followed). Wraps `git rev-parse --verify`.
func (c *Client) ResolveRef(name string) (Hash, error) {
	out, err := c.output(context.Background(), "rev-parse", "--verify", "--quiet", name)
	if err != nil {
		return ZeroHash, fmt.Errorf("resolve ref %q: %w", name, err)
	}

	return Hash(out), nil
}

// ResolveBranchRef resolves a branch short name to its commit hash. It tries
// refs/heads/<name> first, then falls back to refs/remotes/<remote>/<name> so
// that sub-task closes work when the parent integration branch was never
// checked out locally (exists only as a remote tracking ref).
func (c *Client) ResolveBranchRef(name string) (Hash, error) {
	if h, err := c.ResolveRef("refs/heads/" + name); err == nil {
		return h, nil
	}

	remote, err := c.Remote()
	if err != nil || remote == "" {
		return ZeroHash, fmt.Errorf("resolve branch %q: reference not found", name)
	}

	h, err := c.ResolveRef("refs/remotes/" + remote + "/" + name)
	if err != nil {
		return ZeroHash, fmt.Errorf("resolve branch %q: %w", name, err)
	}

	return h, nil
}

// CurrentBranch returns the short name of the branch HEAD points to.
// On a detached HEAD the returned name will not parse as an issue branch,
// so callers can simply ignore it.
func (c *Client) CurrentBranch() (string, error) {
	name, err := c.output(context.Background(), "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("read HEAD: %w", err)
	}

	return name, nil
}

// Authors returns a deduplicated list of commit author identities
// ("Name <email>") from the repository history, ordered by commit count
// (most active first). Uses `git shortlog -sne --all` so it walks every
// ref instead of just HEAD's ancestry, and tolerates partial packfiles
// that trip go-git's commit iterator (e.g. submodules with a malformed
// .idx). The current git config identity is prepended as the first
// (default) entry.
func (c *Client) Authors(ctx context.Context) ([]string, error) {
	out, err := c.output(ctx, "shortlog", "-sne", "--all")
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
	for line := range strings.SplitSeq(out, "\n") {
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

	if name, _ := c.output(ctx, "config", "user.name"); name != "" {
		email, _ := c.output(ctx, "config", "user.email")
		current := name + " <" + email + ">"
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

// gitStderr enriches a git *exec.ExitError with its captured stderr, so wrapped
// errors surface git's actual diagnostic instead of a bare "exit status N".
func gitStderr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if s := strings.TrimSpace(string(ee.Stderr)); s != "" {
			return fmt.Errorf("%w: %s", err, s)
		}
	}

	return err
}

// runGitPathspecStdin runs `git <sub> --pathspec-from-file=- --pathspec-file-nul`
// feeding the NUL-separated pathspec list (raw `ls-files -z` output) on stdin.
// Streaming the list avoids the ARG_MAX limit an unignored build/vendor
// directory of untracked files would otherwise hit, handles paths with spaces,
// and surfaces git's stderr on failure. Used for both the `add` (stage) and the
// `reset` (rollback) of the untracked set.
func (c *Client) runGitPathspecStdin(ctx context.Context, nulPaths []byte, sub string) error {
	_, err := c.outputStdin(ctx, nulPaths, sub, "--pathspec-from-file=-", "--pathspec-file-nul")

	return err
}

// untrackedPaths returns the NUL-separated list of untracked, non-ignored files
// (raw `ls-files --others --exclude-standard -z` output; .gitignore and
// .git/info/exclude respected). Empty when the working tree has none.
func (c *Client) untrackedPaths(ctx context.Context) ([]byte, error) {
	// Raw Output, not c.output: the NUL-separated list must not be trimmed.
	out, err := c.gitCmd(ctx, "ls-files", "--others", "--exclude-standard", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("list untracked files: %w", gitStderr(err))
	}

	return out, nil
}

// stageUntracked stages every untracked, non-ignored file so an
// --include-untracked commit picks them up, and returns the raw NUL-separated
// path list it staged (nil when there was nothing to stage). The caller keeps
// that list so it can roll the exact paths back out of the index if the commit
// later fails. The list is captured before staging because afterwards the files
// are no longer "others" and cannot be re-derived.
func (c *Client) stageUntracked(ctx context.Context) ([]byte, error) {
	paths, err := c.untrackedPaths(ctx)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}

	if err := c.runGitPathspecStdin(ctx, paths, "add"); err != nil {
		return nil, fmt.Errorf("stage untracked files: %w", err)
	}

	return paths, nil
}

// Commit records a commit with msg and the given options using the system git
// binary so that all configured hooks (pre-commit, commit-msg, post-commit) run.
func (c *Client) Commit(ctx context.Context, msg []byte, opts CommitOptions) error {
	root := c.root

	var stagedUntracked []byte
	if opts.IncludeUntracked {
		var err error

		stagedUntracked, err = c.stageUntracked(ctx)
		if err != nil {
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
		// A failed commit (e.g. a rejecting pre-commit hook) leaves the files we
		// staged for --include-untracked sitting in the index, where the user's
		// next commit would silently pick them up. Roll exactly those paths back
		// out of the index — unlike git's own --all, which commits through a
		// temporary index and never mutates the real one on failure. Files that
		// were already staged before the commit keep their staging.
		if len(stagedUntracked) > 0 {
			if rbErr := c.runGitPathspecStdin(ctx, stagedUntracked, "reset"); rbErr != nil {
				return fmt.Errorf("commit failed (%w); additionally, unstaging the untracked files staged by --include-untracked failed (%v) — they remain staged", err, rbErr)
			}
		}

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
		prefix := "refs/remotes/" + remote + "/"
		if target, err := c.output(context.Background(), "symbolic-ref", "--quiet", prefix+"HEAD"); err == nil {
			if name, ok := strings.CutPrefix(target, prefix); ok {
				return name, nil
			}

			return target[strings.LastIndex(target, "/")+1:], nil
		}
	}

	// Fall back to local branches.
	for _, name := range []string{"main", "master"} {
		if exists, _ := c.BranchExists(name); exists {
			return name, nil
		}
	}

	return "", errors.New("could not detect default base branch")
}

// LocalBranchNames returns the short names of all local branches.
func (c *Client) LocalBranchNames() ([]string, error) {
	out, err := c.output(context.Background(), "for-each-ref", "--format=%(refname)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	var names []string
	for _, ref := range strings.Fields(out) {
		names = append(names, strings.TrimPrefix(ref, "refs/heads/"))
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
		if u, err := c.output(context.Background(), "remote", "get-url", remote); err == nil {
			// Strip trailing slashes then take last segment.
			u = strings.TrimRight(u, "/")
			seg := u[strings.LastIndexAny(u, "/:")+1:]
			seg = strings.TrimSuffix(seg, ".git")

			if seg != "" {
				return seg, nil
			}
		}
	}

	return filepath.Base(c.root), nil
}

// IsMergedInto reports whether branchName's tip commit is reachable from baseBranch,
// i.e. whether the branch has been merged into base (mirrors git merge-base --is-ancestor).
func (c *Client) IsMergedInto(branchName, baseBranch string) (bool, error) {
	// ResolveBranchRef falls back to the remote-tracking branch when the base
	// was never checked out locally.
	baseHash, err := c.ResolveBranchRef(baseBranch)
	if err != nil {
		return false, fmt.Errorf("resolve base branch: %w", err)
	}

	return c.IsAncestor(context.Background(), "refs/heads/"+branchName, baseHash.String())
}

// CommitsAhead returns the number of commits in branchName that are not reachable
// from baseBranch. Uses `git rev-list --count <baseBranch>..<branchName>`.
func (c *Client) CommitsAhead(ctx context.Context, branchName, baseBranch string) (int, error) {
	out, err := c.output(ctx, "rev-list", "--count", baseBranch+".."+branchName)
	if err != nil {
		return 0, fmt.Errorf("rev-list --count %s..%s: %w", baseBranch, branchName, err)
	}

	var n int
	if _, err := fmt.Sscan(out, &n); err != nil {
		return 0, fmt.Errorf("parse rev-list count %q: %w", out, err)
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

	root := c.root

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

	return c.gitCmd(ctx, "ls-remote", "--exit-code", "--heads", remote, branchName).Run() == nil
}

// LsRemoteBranches asks the configured remote for its branches and returns
// their short names as a set. Unlike RemoteBranchNames, which reads the
// tracking refs of the last fetch, this is what the remote has now; no local
// ref is touched. Without a remote the set is empty; an unreachable remote is
// an error, which callers must not read as "the remote has no branch".
func (c *Client) LsRemoteBranches(ctx context.Context) (map[string]bool, error) {
	names := make(map[string]bool)

	remote, err := c.Remote()
	if err != nil {
		return nil, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return names, nil
	}

	out, err := c.output(ctx, "ls-remote", "--heads", remote)
	if err != nil {
		return nil, fmt.Errorf("ls-remote %s: %w", remote, err)
	}

	for line := range strings.SplitSeq(out, "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			names[strings.TrimPrefix(ref, "refs/heads/")] = true
		}
	}

	return names, nil
}

// DeleteLocalBranchSafe deletes branchName locally, switching to the
// configured base branch first when the current branch IS branchName
// (git refuses to delete the currently checked-out branch).
// cfgBase is used as the switch target; when empty the repo's default
// base branch (main/master) is auto-detected.
// On any checkout failure the function returns the error immediately.
// The delete is forced (-D): every caller removes a branch that may hold
// commits its base never received.
func (c *Client) DeleteLocalBranchSafe(ctx context.Context, branchName, cfgBase string) error {
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

	return c.DeleteLocalBranch(ctx, branchName, true)
}

// RunGitAt runs an arbitrary git command in dir with the client's IO streams.
// Exported for review subcommands that need low-level git operations.
func (c *Client) RunGitAt(ctx context.Context, dir string, args ...string) error {
	return c.runInteractive(ctx, dir, args...)
}

// ConfigUser returns the git config user identity as "Name <email>".
// Returns an empty string when not configured.
func (c *Client) ConfigUser(ctx context.Context) (string, error) {
	name, err := c.output(ctx, "config", "user.name")
	if err != nil {
		return "", nil
	}

	if email, _ := c.output(ctx, "config", "user.email"); email != "" {
		return name + " <" + email + ">", nil
	}
	return name, nil
}

// BranchExists returns true if refs/heads/<name> resolves locally. It does
// not consult remotes — see resolveBranchConflict for the rationale (no
// fetch on the happy path of `issue start`).
func (c *Client) BranchExists(name string) (bool, error) {
	exists, err := c.succeeds(context.Background(), "show-ref", "--verify", "--quiet", "refs/heads/"+name)
	if err != nil {
		return false, fmt.Errorf("lookup branch %q: %w", name, err)
	}

	return exists, nil
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

	// Starting from the resolved hash (not the branch name) never sets an
	// upstream, so a base that exists only as a remote-tracking ref behaves
	// exactly like a local one. Local modifications are carried over, as with
	// any checkout.
	if _, err := c.output(context.Background(), "checkout", "-b", name, baseHash.String()); err != nil {
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

	prefix := "refs/remotes/" + remote + "/"

	out, err := c.output(context.Background(), "for-each-ref", "--format=%(refname)", prefix)
	if err != nil {
		return nil, fmt.Errorf("list references: %w", err)
	}

	var names []string
	for _, ref := range strings.Fields(out) {
		// Skip the remote HEAD symref (refs/remotes/<remote>/HEAD) and the
		// git-zf chain tracking refs under zf/, which are never branches.
		if short := strings.TrimPrefix(ref, prefix); short != "" && short != "HEAD" && !strings.HasPrefix(short, "zf/") {
			names = append(names, short)
		}
	}

	return names, nil
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

// CreateLocalBranch creates refs/heads/<name> pointing at startPoint (a branch
// name, remote-tracking ref like "origin/X.1@feat@slug", or a SHA). It does not
// switch the working tree. Used to materialize a feature branch that exists only
// as a remote-tracking ref on a reviewer's clone so the merge strategies can
// resolve it by bare name.
func (c *Client) CreateLocalBranch(ctx context.Context, name, startPoint string) error {
	if _, err := c.output(ctx, "branch", name, startPoint); err != nil {
		return fmt.Errorf("git branch %s %s: %w", name, startPoint, err)
	}

	return nil
}
