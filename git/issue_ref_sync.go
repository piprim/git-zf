package git

import (
	"context"
	"fmt"
	"strings"
)

const (
	// issueRemoteRefPrefix is the tracking namespace: what the remote's
	// refs/zf/issues/* looked like at the last fetch or push.
	issueRemoteRefPrefix = "refs/zf/remote/issues/"
	issueFetchRefspec    = "+" + issueRefPrefix + "*:" + issueRemoteRefPrefix + "*"
)

// FetchIssueRefs fetches the remote's refs/zf/issues/* into the tracking
// namespace refs/zf/remote/issues/*. Local issue refs are never touched, so an
// issue created offline cannot be lost. The tracking namespace is pruned: a
// tracking ref the remote no longer has (ref deleted there, or the remote URL
// now points elsewhere) is removed, so IssueRefPushed stops reporting that
// issue as pushed and the next sync pushes it again. No-op when no remote is
// configured.
func (c *Client) FetchIssueRefs(ctx context.Context) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	if err := c.runInteractive(ctx, c.root, "fetch", "--quiet", "--prune", remote, issueFetchRefspec); err != nil {
		return fmt.Errorf("fetch issue refs: %w", err)
	}

	return nil
}

// ReconcileIssueRefs brings every local issue ref up to date with its tracking
// ref: a missing local ref is created, a local ref that is behind is
// fast-forwarded, and a diverged one gets a two-parent commit carrying
// mergePayload as its op.json. A local ref that is ahead is left alone.
// Returns the number of merge commits written.
func (c *Client) ReconcileIssueRefs(ctx context.Context, mergePayload []byte) (int, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(refname)", issueRemoteRefPrefix)
	if err != nil {
		return 0, fmt.Errorf("for-each-ref %s: %w", issueRemoteRefPrefix, err)
	}

	merged := 0
	for _, line := range strings.Split(out, "\n") {
		remoteTip, name, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}

		id := strings.TrimPrefix(name, issueRemoteRefPrefix)

		didMerge, err := c.reconcileIssueRef(ctx, id, remoteTip, mergePayload)
		if err != nil {
			return merged, fmt.Errorf("reconcile issue %s: %w", id, err)
		}
		if didMerge {
			merged++
		}
	}

	return merged, nil
}

func (c *Client) reconcileIssueRef(ctx context.Context, id, remoteTip string, mergePayload []byte) (bool, error) {
	ref := issueRefPrefix + id

	local, err := c.IssueTip(ctx, id)
	if err != nil {
		return false, err
	}

	if local == "" {
		_, err := c.output(ctx, "update-ref", ref, remoteTip, ZeroHash.String())

		return false, err
	}
	if local == remoteTip {
		return false, nil
	}

	// IsAncestor(a, b) reports whether a is an ancestor of b.
	if ahead, err := c.IsAncestor(ctx, remoteTip, local); err != nil || ahead {
		return false, err
	}

	behind, err := c.IsAncestor(ctx, local, remoteTip)
	if err != nil {
		return false, err
	}
	if behind {
		_, err := c.output(ctx, "update-ref", ref, remoteTip, local)

		return false, err
	}

	commit, err := c.writeIssueCommit(ctx, mergePayload, "merge", local, remoteTip)
	if err != nil {
		return false, err
	}

	_, err = c.output(ctx, "update-ref", ref, commit, local)

	return err == nil, err
}

// IssueRefPushed reports whether the remote already has the local tip of
// issue id, according to the tracking ref. Always true without a remote.
func (c *Client) IssueRefPushed(ctx context.Context, id string) (bool, error) {
	remote, err := c.Remote()
	if err != nil {
		return false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return true, nil
	}

	local, err := c.IssueTip(ctx, id)
	if err != nil {
		return false, err
	}

	tracked, err := c.refTip(ctx, issueRemoteRefPrefix+id)
	if err != nil {
		return false, err
	}

	return local == tracked, nil
}

// PushIssueRef pushes refs/zf/issues/<id> to the remote with a plain,
// non-forced push: it succeeds only as a fast-forward, so it can never
// overwrite ops pushed by someone else. On success the tracking ref is moved
// to the pushed tip. No-op when no remote is configured.
func (c *Client) PushIssueRef(ctx context.Context, id string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	ref := issueRefPrefix + id

	tip, err := c.IssueTip(ctx, id)
	if err != nil {
		return err
	}
	if tip == "" {
		return fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	if err := c.runInteractive(ctx, c.root, "push", "--quiet", remote, ref+":"+ref); err != nil {
		return fmt.Errorf("push issue ref %s: %w", id, err)
	}

	if _, err := c.output(ctx, "update-ref", issueRemoteRefPrefix+id, tip); err != nil {
		return fmt.Errorf("update tracking ref: %w", err)
	}

	return nil
}
