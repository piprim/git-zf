package git

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// chainTip is what one ref under a chain prefix points at.
type chainTip struct {
	sha    string
	commit bool
}

// chainTips lists the refs under prefix, keyed by the name after prefix.
func (c *Client) chainTips(ctx context.Context, prefix string) (map[string]chainTip, error) {
	out, err := c.output(ctx, "for-each-ref", "--format=%(objectname) %(objecttype) %(refname)", prefix)
	if err != nil {
		return nil, fmt.Errorf("for-each-ref %s: %w", prefix, err)
	}

	const fieldCount = 3 // "<oid> <type> <refname>"

	tips := make(map[string]chainTip)
	for line := range strings.SplitSeq(out, "\n") {
		fields := strings.SplitN(line, " ", fieldCount)
		if len(fields) != fieldCount {
			continue
		}

		tips[strings.TrimPrefix(fields[2], prefix)] = chainTip{sha: fields[0], commit: fields[1] == "commit"}
	}

	return tips, nil
}

// FetchChainRefs fetches the remote's chains into the tracking namespace
// refs/remotes/<remote>/zf/<name>/*. Local chain refs are never touched, so a
// chain written offline cannot be lost. The tracking namespace is pruned: a
// tracking ref the remote no longer has is removed, so ChainRefPushed stops
// reporting that chain as pushed and the next sync pushes it again. silent
// runs the fetch without printing anything, for use in hooks. No-op when no
// remote is configured.
func (c *Client) FetchChainRefs(ctx context.Context, ns ChainRefs, silent bool) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	args := []string{"fetch", "--quiet", "--prune", remote, ns.FetchRefspec(remote)}

	if silent {
		if err := c.gitCmd(ctx, args...).Run(); err != nil {
			return fmt.Errorf("fetch %s refs: %w", ns.name, err)
		}

		return nil
	}

	if err := c.runInteractive(ctx, c.root, args...); err != nil {
		return fmt.Errorf("fetch %s refs: %w", ns.name, err)
	}

	return nil
}

// ReconcileChainRefs brings every local chain ref up to date with its tracking
// ref: a missing local ref is created, a local ref that is behind is
// fast-forwarded, and a diverged one gets a two-parent commit carrying
// mergePayload as its op.json. A local ref that is ahead is left alone. A
// tracking ref that is not a commit (written by an older git-zf) is skipped; a
// local ref that is not a commit is replaced by the remote chain. Returns the
// number of merge commits written. No-op without a remote.
func (c *Client) ReconcileChainRefs(ctx context.Context, ns ChainRefs, mergePayload []byte) (int, error) {
	remote, err := c.Remote()
	if err != nil {
		return 0, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return 0, nil
	}

	tracked, err := c.chainTips(ctx, ns.trackingPrefix(remote))
	if err != nil {
		return 0, err
	}

	// One listing of the local tips, instead of one git process per chain.
	local, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return 0, err
	}

	merged := 0
	for _, id := range slices.Sorted(maps.Keys(tracked)) {
		if !tracked[id].commit {
			continue
		}

		didMerge, err := c.reconcileChainRef(ctx, ns, id, local[id], tracked[id].sha, mergePayload)
		if err != nil {
			return merged, fmt.Errorf("reconcile %s %s: %w", ns.name, id, err)
		}
		if didMerge {
			merged++
		}
	}

	return merged, nil
}

// reconcileChainRef reconciles one chain. local is the current tip of its
// local ref; its sha is "" when the ref does not exist.
func (c *Client) reconcileChainRef(
	ctx context.Context, ns ChainRefs, id string, local chainTip, remoteTip string, mergePayload []byte,
) (bool, error) {
	ref := ns.prefix() + id

	switch {
	case local.sha == "":
		_, err := c.output(ctx, "update-ref", ref, remoteTip, ZeroHash.String())

		return false, err
	case !local.commit:
		_, err := c.output(ctx, "update-ref", ref, remoteTip, local.sha)

		return false, err
	case local.sha == remoteTip:
		return false, nil
	}

	// IsAncestor(a, b) reports whether a is an ancestor of b.
	if ahead, err := c.IsAncestor(ctx, remoteTip, local.sha); err != nil || ahead {
		return false, err
	}

	behind, err := c.IsAncestor(ctx, local.sha, remoteTip)
	if err != nil {
		return false, err
	}
	if behind {
		_, err := c.output(ctx, "update-ref", ref, remoteTip, local.sha)

		return false, err
	}

	commit, err := c.writeChainCommit(ctx, mergePayload, "merge", false, local.sha, remoteTip)
	if err != nil {
		return false, err
	}

	_, err = c.output(ctx, "update-ref", ref, commit, local.sha)

	return err == nil, err
}

// ChainRefPushed reports whether the remote already has the local tip of
// chain id, according to the tracking ref. Always true without a remote.
func (c *Client) ChainRefPushed(ctx context.Context, ns ChainRefs, id string) (bool, error) {
	remote, err := c.Remote()
	if err != nil {
		return false, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return true, nil
	}

	local, err := c.ChainTip(ctx, ns, id)
	if err != nil {
		return false, err
	}

	tracked, err := c.refTip(ctx, ns.trackingPrefix(remote)+id)
	if err != nil {
		return false, err
	}

	return local == tracked, nil
}

// UnpushedChainIDs returns, sorted, the IDs of the local chains whose tip the
// remote does not have according to the tracking refs. Empty without a
// remote. Two git processes whatever the number of chains.
func (c *Client) UnpushedChainIDs(ctx context.Context, ns ChainRefs) ([]string, error) {
	remote, err := c.Remote()
	if err != nil {
		return nil, fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil, nil
	}

	local, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return nil, err
	}

	tracked, err := c.chainTips(ctx, ns.trackingPrefix(remote))
	if err != nil {
		return nil, err
	}

	var ids []string
	for id, tip := range local {
		if tip.commit && tracked[id].sha != tip.sha {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	return ids, nil
}

// PushChainRef pushes the ref of chain id to the remote with a plain,
// non-forced push: it succeeds only as a fast-forward, so it can never
// overwrite ops pushed by someone else. On success the tracking ref is moved
// to the pushed tip. No-op when no remote is configured.
func (c *Client) PushChainRef(ctx context.Context, ns ChainRefs, id string) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	ref := ns.prefix() + id

	tip, err := c.ChainTip(ctx, ns, id)
	if err != nil {
		return err
	}
	if tip == "" {
		return fmt.Errorf("%s: %w", id, ErrIssueNotFound)
	}

	if err := c.runInteractive(ctx, c.root, "push", "--quiet", remote, ref+":"+ref); err != nil {
		return fmt.Errorf("push %s ref %s: %w", ns.name, id, err)
	}

	if _, err := c.output(ctx, "update-ref", ns.trackingPrefix(remote)+id, tip); err != nil {
		return fmt.Errorf("update tracking ref: %w", err)
	}

	return nil
}

// ConfigureChainFetch adds the fetch refspecs of the chain families to the
// configuration of every remote, unless already present, and returns the
// remotes' names (empty when there is none). Git prunes by refspec: without
// these lines a plain `git fetch --prune` deletes the tracking refs under
// refs/remotes/<remote>/zf/, since the default refspec owns everything under
// refs/remotes/<remote>/. With them, a plain `git fetch` also brings the
// chains. It also deletes, best-effort, the tracking refs of the layout used
// before (refs/zf/remote/*), which nothing reads any more.
func (c *Client) ConfigureChainFetch(ctx context.Context) ([]string, error) {
	stale, _ := c.output(ctx, "for-each-ref", "--format=%(refname)", "refs/zf/remote/")
	for _, ref := range strings.Fields(stale) {
		_, _ = c.output(ctx, "update-ref", "-d", ref)
	}

	out, err := c.output(ctx, "remote")
	if err != nil {
		return nil, fmt.Errorf("list remotes: %w", err)
	}

	remotes := strings.Fields(out)
	for _, remote := range remotes {
		key := "remote." + remote + ".fetch"
		// --get-all exits 1 when the key is unset: an empty list, not an error.
		existing, _ := c.output(ctx, "config", "--get-all", key)
		configured := strings.Split(existing, "\n")

		for _, ns := range []ChainRefs{ReviewRefs, IssueRefs} {
			spec := ns.FetchRefspec(remote)
			if slices.Contains(configured, spec) {
				continue
			}
			if _, err := c.output(ctx, "config", "--add", key, spec); err != nil {
				return nil, fmt.Errorf("configure %s: %w", key, err)
			}
		}
	}

	return remotes, nil
}
