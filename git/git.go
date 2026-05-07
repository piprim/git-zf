package git

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	gogit "github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// CommitSummary holds display information about a newly created commit.
type CommitSummary struct {
	ShortHash string
	Branch    string
	IsRoot    bool
	Subject   string
	Files     int
	Additions int
	Deletions int
}

// CommitOptions configures Client.Commit.
type CommitOptions struct {
	All   bool
	Amend bool
	// NoVerify — go-git v6 does not execute hooks; reserved for a future subprocess fallback.
	NoVerify   bool
	Signoff    bool
	AllowEmpty bool
	Author     string // "Name <email>"; empty = git config identity
}

// Client wraps a go-git repository and exposes commit operations.
type Client struct {
	repo *gogit.Repository
}

// NewClient opens the git repository that contains the current directory.
func NewClient() (*Client, error) {
	repo, err := gogit.PlainOpenWithOptions(".", &gogit.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open git repository: %w", err)
	}

	return &Client{repo: repo}, nil
}

// NewClientAt opens the git repository rooted at dir.
// Used in tests and tooling that need to open a repo at a specific path.
func NewClientAt(dir string) (*Client, error) {
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		return nil, fmt.Errorf("open git repository at %s: %w", dir, err)
	}

	return &Client{repo: repo}, nil
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

	cfg, err := c.repo.Config()
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
// It returns a CommitSummary suitable for printing to the user.
func (c *Client) Commit(msg []byte, opts CommitOptions) (CommitSummary, error) {
	wt, err := c.repo.Worktree()
	if err != nil {
		return CommitSummary{}, fmt.Errorf("get worktree: %w", err)
	}

	finalMsg := string(msg)
	if opts.Signoff {
		signer := opts.Author
		if signer == "" {
			if cfg, err := c.repo.Config(); err == nil && cfg.User.Name != "" {
				signer = cfg.User.Name + " <" + cfg.User.Email + ">"
			}
		}
		if signer != "" {
			finalMsg = strings.TrimRight(finalMsg, "\n") + "\n\nSigned-off-by: " + signer
		}
	}

	// CommitOptions.All stages only tracked modified/deleted files, matching
	// `git commit --all` semantics (untracked files are NOT included).
	commitOpts := &gogit.CommitOptions{
		All:               opts.All,
		AllowEmptyCommits: opts.AllowEmpty,
		Amend:             opts.Amend,
	}

	if opts.Author != "" {
		name, email := parseAuthor(opts.Author)
		commitOpts.Author = &object.Signature{
			Name:  name,
			Email: email,
			When:  time.Now(),
		}
	}

	hash, err := wt.Commit(finalMsg, commitOpts)
	if err != nil {
		return CommitSummary{}, fmt.Errorf("commit: %w", err)
	}

	summary, err := c.buildSummary(hash, finalMsg)
	if err != nil {
		return CommitSummary{}, err
	}

	return summary, nil
}

func (c *Client) buildSummary(hash plumbing.Hash, msg string) (CommitSummary, error) {
	commit, err := c.repo.CommitObject(hash)
	if err != nil {
		return CommitSummary{}, fmt.Errorf("read commit: %w", err)
	}

	stats, err := commit.Stats()
	if err != nil {
		return CommitSummary{}, fmt.Errorf("commit stats: %w", err)
	}

	head, err := c.repo.Head()
	if err != nil {
		return CommitSummary{}, fmt.Errorf("read HEAD: %w", err)
	}

	subject := strings.TrimSpace(strings.SplitN(msg, "\n", 2)[0])

	var files, add, del int
	for _, f := range stats {
		files++
		add += f.Addition
		del += f.Deletion
	}

	return CommitSummary{
		ShortHash: hash.String()[:7],
		Branch:    head.Name().Short(),
		IsRoot:    len(commit.ParentHashes) == 0,
		Subject:   subject,
		Files:     files,
		Additions: add,
		Deletions: del,
	}, nil
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

// DefaultBaseBranch resolves the default base branch in priority order:
//  1. refs/remotes/origin/HEAD
//  2. "main" if the local ref exists
//  3. "master" if the local ref exists
func (c *Client) DefaultBaseBranch() (string, error) {
	// Try remote HEAD first.
	if ref, err := c.repo.Reference(plumbing.ReferenceName("refs/remotes/origin/HEAD"), false); err == nil {
		if ref.Type() == plumbing.SymbolicReference {
			parts := strings.Split(ref.Target().String(), "/")

			return parts[len(parts)-1], nil
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

// IsMergedInto reports whether branchName's tip commit is reachable from baseBranch,
// i.e. whether the branch has been merged into base (mirrors git merge-base --is-ancestor).
func (c *Client) IsMergedInto(branchName, baseBranch string) (bool, error) {
	branchRef, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+branchName), true)
	if err != nil {
		return false, fmt.Errorf("resolve branch %q: %w", branchName, err)
	}

	baseRef, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+baseBranch), true)
	if err != nil {
		// Try remote tracking branch as fallback.
		baseRef, err = c.repo.Reference(plumbing.ReferenceName("refs/remotes/origin/"+baseBranch), true)
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
