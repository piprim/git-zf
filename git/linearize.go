package git

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
)

// Linearization is what replacing a merge by one commit holding its tree
// would do: the branch to move and the push that may follow.
type Linearization struct {
	// Merge is the full hash of the merge, Parent its first parent: the
	// parent of the new commit.
	Merge, Parent string
	// Branch is the local branch whose tip is the merge.
	Branch string
	// Upstream is the remote-tracking branch of Branch (origin/main) as
	// fetched, "" without one: nothing is pushed then. UpstreamHash is where
	// it was fetched, the lease of the push. Remote and RemoteRef are its
	// remote and its ref there (refs/heads/main).
	Upstream, UpstreamHash, Remote, RemoteRef string
	// Force is set when the push will not be a fast-forward: the upstream
	// holds the merge or one of its side commits.
	Force bool
}

// LinearizePlan says how the merge hash, the tip of a local branch, would be
// replaced by one commit, after fetching the branch's remote. It refuses a
// commit that is not a two-parent merge, the tip of no local branch, or whose
// upstream moved past it; the branch checked out wins when several end at
// the merge.
func (c *Client) LinearizePlan(ctx context.Context, hash string) (Linearization, error) {
	var p Linearization
	line, err := c.output(ctx, "rev-list", "--parents", "-n", "1", hash)
	if err != nil {
		return p, fmt.Errorf("read %s: %w", hash, err)
	}
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return p, fmt.Errorf("%s is not a merge of two parents", hash)
	}
	p.Merge, p.Parent = fields[0], fields[1]

	out, err := c.output(ctx, "for-each-ref", "--points-at", p.Merge, "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return p, fmt.Errorf("list branches: %w", err)
	}
	names := strings.Fields(out)
	current, _ := c.CurrentBranch()
	switch {
	case len(names) == 0:
		return p, fmt.Errorf("no local branch has %s as its tip", hash)
	case len(names) == 1:
		p.Branch = names[0]
	case slices.Contains(names, current):
		p.Branch = current
	default:
		return p, fmt.Errorf("several branches have %s as their tip (%s): check one out", hash, strings.Join(names, ", "))
	}

	out, err = c.output(ctx, "for-each-ref", "--format=%(upstream:short)%00%(upstream:remotename)%00%(upstream:remoteref)",
		"refs/heads/"+p.Branch)
	if err != nil {
		return p, fmt.Errorf("read upstream of %s: %w", p.Branch, err)
	}
	up := strings.Split(out, "\x00")
	if len(up) != 3 || up[0] == "" {
		return p, nil
	}
	p.Upstream, p.Remote, p.RemoteRef = up[0], up[1], up[2]

	if _, err := c.gitCmd(ctx, "fetch", "--quiet", p.Remote).Output(); err != nil {
		return p, fmt.Errorf("fetch %s: %w", p.Remote, gitStderr(err))
	}
	if p.UpstreamHash, err = c.output(ctx, "rev-parse", "--verify", "--quiet", p.Upstream); err != nil {
		// The branch was never pushed: nothing to lease on.
		p.Upstream, p.Remote, p.RemoteRef = "", "", ""

		return p, nil
	}
	behind, err := c.IsAncestor(ctx, p.UpstreamHash, p.Merge)
	if err != nil {
		return p, err
	}
	if !behind {
		return p, fmt.Errorf("%s moved to %s: pull first", p.Upstream, p.UpstreamHash[:7])
	}
	ff, err := c.IsAncestor(ctx, p.UpstreamHash, p.Parent)
	if err != nil {
		return p, err
	}
	p.Force = !ff

	return p, nil
}

// LinearizeMerge replaces the merge of p by one commit holding its tree on
// its first parent, with its message and author, and moves the branch there.
// The old tip stays in the branch's reflog; the working tree is untouched
// since the tree does not change. Returns the new commit.
func (c *Client) LinearizeMerge(ctx context.Context, p *Linearization) (string, error) {
	meta, err := c.output(ctx, "show", "-s", "--format=%an%x00%ae%x00%aD%x00%B", p.Merge)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", p.Merge, err)
	}
	parts := strings.SplitN(meta, "\x00", 4)
	if len(parts) != 4 {
		return "", fmt.Errorf("read %s: unexpected format %q", p.Merge, meta)
	}

	args := []string{"commit-tree", p.Merge + "^{tree}", "-p", p.Parent}
	if configured, _ := c.output(ctx, "config", "--bool", "commit.gpgsign"); configured == "true" {
		args = append(args, "-S")
	}
	cmd := c.gitCmd(ctx, args...)
	cmd.Stdin = strings.NewReader(parts[3])
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME="+parts[0], "GIT_AUTHOR_EMAIL="+parts[1], "GIT_AUTHOR_DATE="+parts[2])
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("commit-tree: %w", gitStderr(err))
	}
	commit := strings.TrimSpace(string(out))

	if _, err := c.output(ctx, "update-ref", "-m", "zf log: linearize "+p.Merge[:7],
		"refs/heads/"+p.Branch, commit, p.Merge); err != nil {
		return "", fmt.Errorf("move %s: %w", p.Branch, err)
	}

	return commit, nil
}

// PushLinearized pushes the branch of p to its upstream, the lease pinned to
// the hash the plan fetched: the push is refused if the remote moved since.
func (c *Client) PushLinearized(ctx context.Context, p *Linearization) error {
	_, err := c.gitCmd(ctx, "push", "--quiet", "--force-with-lease="+p.RemoteRef+":"+p.UpstreamHash,
		p.Remote, "refs/heads/"+p.Branch+":"+p.RemoteRef).Output()
	if err != nil {
		return fmt.Errorf("push %s to %s: %w", p.Branch, p.Remote, gitStderr(err))
	}

	return nil
}
