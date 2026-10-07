package git

import (
	"context"
	"errors"
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

// PushChainRefs pushes the refs of chains ids in one git push, fast-forward
// only like PushChainRef, then moves their tracking refs in one update-ref.
// A rejected ref fails the call; the other refs may have gone through, and the
// next push reports them up to date. No-op without a remote or without ids.
func (c *Client) PushChainRefs(ctx context.Context, ns ChainRefs, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	if remote == "" {
		return nil
	}

	tips, err := c.chainTips(ctx, ns.prefix())
	if err != nil {
		return err
	}

	args := []string{"push", "--quiet", remote}
	var updates strings.Builder
	for _, id := range ids {
		tip, ok := tips[id]
		if !ok || !tip.commit {
			return fmt.Errorf("%s: %w", id, ErrIssueNotFound)
		}

		ref := ns.prefix() + id
		args = append(args, ref+":"+ref)
		fmt.Fprintf(&updates, "update %s%s %s\n", ns.trackingPrefix(remote), id, tip.sha)
	}

	if err := c.runInteractive(ctx, c.root, args...); err != nil {
		return fmt.Errorf("push %d %s refs: %w", len(ids), ns.name, err)
	}

	if _, err := c.outputStdin(ctx, []byte(updates.String()), "update-ref", "--stdin"); err != nil {
		return fmt.Errorf("update tracking refs: %w", err)
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

		for _, ns := range []ChainRefs{ReviewRefs, IssueRefs, BranchRefs} {
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

// FetchChains fetches the remote's chains of the family and reconciles the
// local ones with them, merging diverged chains with a commit that carries
// mergePayload. silent prints nothing, for use in hooks. No-op without a
// remote.
//
// A failed fetch still reconciles: the tracking refs may hold a chain that a
// plain `git fetch` brought and no git-zf command has loaded yet. The fetch
// error is returned after.
func (c *Client) FetchChains(ctx context.Context, ns ChainRefs, mergePayload []byte, silent bool) error {
	fetchErr := c.FetchChainRefs(ctx, ns, silent)
	if fetchErr != nil {
		fetchErr = fmt.Errorf("fetch %s: %w", ns.name, fetchErr)
	}

	if _, err := c.ReconcileChainRefs(ctx, ns, mergePayload); err != nil {
		return errors.Join(fetchErr, fmt.Errorf("reconcile %s: %w", ns.name, err))
	}

	return fetchErr
}

// PushChain pushes chain id. A rejected push (someone pushed first) triggers
// one fetch, merge and retry. No-op without a remote.
func (c *Client) PushChain(ctx context.Context, ns ChainRefs, id string, mergePayload []byte) error {
	firstErr := c.PushChainRef(ctx, ns, id)
	if firstErr == nil {
		return nil
	}

	if err := c.FetchChains(ctx, ns, mergePayload, false); err != nil {
		return errors.Join(firstErr, err)
	}

	if err := c.PushChainRef(ctx, ns, id); err != nil {
		return fmt.Errorf("push %s %s after merge: %w", ns.name, id, err)
	}

	return nil
}

// SyncChains fetches and reconciles every chain of the family, then pushes the
// ones the remote does not have yet: an op whose push failed earlier goes out
// here. No-op without a remote.
func (c *Client) SyncChains(ctx context.Context, ns ChainRefs, mergePayload []byte) error {
	if err := c.FetchChains(ctx, ns, mergePayload, false); err != nil {
		return err
	}

	ids, err := c.UnpushedChainIDs(ctx, ns)
	if err != nil {
		return fmt.Errorf("list unpushed %s: %w", ns.name, err)
	}

	var failed []error
	for _, id := range ids {
		if err := c.PushChain(ctx, ns, id, mergePayload); err != nil {
			failed = append(failed, err)
		}
	}

	return errors.Join(failed...)
}
