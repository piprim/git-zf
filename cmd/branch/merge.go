package branch

import (
	"context"
	"errors"
	"fmt"

	branchpkg "github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/cmd/mergeflow"
	"github.com/piprim/git-zf/cmd/pushflow"
	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/store"
	"github.com/spf13/cobra"
)

func (b Branch) mergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge",
		Short: "Merge a branch into the current branch",
		Long: `Pick a local or remote branch and merge it into the current branch
(rebase, squash, or classic), then optionally delete it and push. Issue branches
are refused — use ` + "`git zf issue close`" + ` for those.`,
		RunE: b.mergeRunE,
	}
	pushflow.AddFlags(cmd)

	return cmd
}

func (b Branch) mergeRunE(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	c, err := cmdutil.NewClientForCmd(cmd, b.appConfig)
	if err != nil {
		return err
	}

	push, noPush := pushflow.ReadFlags(cmd)
	d := mergeDeps{
		client: c, store: s, cfg: b.appConfig,
		push: push, noPush: noPush, pushConfirm: pushflow.NewHuhConfirm(),
	}

	return runMerge(ctx, d, newHuhMergePrompter(c, s, b.appConfig))
}

// mergeDeps bundles what runMerge needs; the E2E rig builds it directly.
type mergeDeps struct {
	client       *git.Client
	store        *store.Store
	cfg          *config.AppConfig
	push, noPush bool
	pushConfirm  pushflow.ConfirmFunc
}

// runMerge merges a picked source branch into the current branch via the shared
// mergeflow engine, refusing issue-branch sources and running the post-merge
// delete/push steps.
func runMerge(ctx context.Context, d mergeDeps, prompter MergePrompter) (err error) {
	target, err := d.client.CurrentBranch()
	if err != nil {
		return fmt.Errorf("resolve current branch: %w", err)
	}
	// go-git reports a detached HEAD as the branch name "HEAD".
	if target == "" || target == "HEAD" {
		return errors.New("checkout a branch before merging into it (HEAD is detached)")
	}

	sources, err := collectSources(d.client, target)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		fmt.Fprintln(d.client.IO().Out, "No other branches to merge.")

		return nil
	}

	source, err := prompter.PickSource(ctx, sources)
	if err != nil {
		return err //nolint:wrapcheck // prompter already wraps
	}

	// SAFETY: refuse issue-branch sources — merging one here would bypass the
	// review incorporation, sub-task guard, and tracker update that issue close
	// runs. The gate is branch.Parse, so it also catches remote-only issue
	// branches the local store has never seen.
	if parsed, perr := branchpkg.Parse(source.Name); perr == nil {
		fmt.Fprintf(d.client.IO().Out,
			"%q is an issue branch (%s). Use \"git zf issue close\" to merge it safely —\n"+
				"that runs review incorporation, the sub-task guard, and the tracker update.\n",
			source.Name, parsed.IssueID())

		return nil
	}

	mergeCommitted := false
	created := false
	if source.RemoteOnly {
		created, err = issueflow.MaterializeBranch(ctx, d.client, store.BranchRow{BranchName: source.Name})
		if err != nil {
			return err
		}
	}

	// Roll back a just-materialized source when the merge does not commit, so an
	// aborted/failed merge leaves no orphaned local branch. FastForwardDeferred
	// and success both set mergeCommitted, so the branch (with its work) survives.
	defer func() {
		if !created || mergeCommitted {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		if delErr := d.client.DeleteLocalBranchSafe(cleanupCtx, source.Name, true, d.cfg.Branch.Base); delErr != nil {
			fmt.Fprintf(d.client.IO().Err, "warning: rollback materialized branch %q: %v\n", source.Name, delErr)
		}
	}()

	// A source checked out in a linked worktree cannot be checked out here;
	// give the engine a client on that worktree instead.
	srcClient, wt, err := mergeflow.SourceTree(ctx, d.client, source.Name)
	if err != nil {
		return err
	}

	prefill := func(_ commit.MergeStrategy, _, _ git.Hash) map[string]any {
		return map[string]any{"subject": fmt.Sprintf("Merge %q into %q", source.Name, target)}
	}

	res, err := mergeflow.Run(ctx, d.client, mergeflow.Params{
		Source: source.Name, Target: target, SourceMaterialized: created, SourceClient: srcClient,
	}, prompter, prefill)
	if err != nil {
		return err
	}

	if res.FastForwardDeferred {
		mergeCommitted = true // keep the materialized branch: the rebased commits live only on it
		fmt.Fprintf(d.client.IO().Out,
			"Commit landed on %q; fast-forward %q into it manually.\n", source.Name, target)

		return nil
	}
	if res.Aborted {
		fmt.Fprintln(d.client.IO().Out, "Aborted.")

		return nil
	}
	mergeCommitted = true

	// Post-merge: offer to remove the source's worktree (when it has one), then
	// to delete the source (local + remote), then propose push.
	worktreeRemoved := false
	if wt != nil {
		// invokedFrom is "" on purpose: the target is the current branch, so the
		// user cannot be standing in the source's worktree.
		worktreeRemoved, err = mergeflow.RemoveWorktreeStep(ctx, d.client, wt, "", prompter.ConfirmRemoveWorktree)
		if err != nil {
			return err
		}
	}

	if del, derr := prompter.ConfirmDeleteBranch(ctx, source.Name); derr != nil {
		return derr //nolint:wrapcheck // prompter already wraps
	} else if del {
		if wt != nil && !worktreeRemoved {
			fmt.Fprintf(d.client.IO().Err,
				"warning: branch %q is still checked out in its worktree; delete it after `git worktree remove`\n",
				source.Name)
		} else {
			force := res.Strategy == commit.MergeStrategySquash || res.Strategy == commit.MergeStrategyRebase
			if delErr := d.client.DeleteLocalBranch(ctx, source.Name, force); delErr != nil {
				fmt.Fprintf(d.client.IO().Err, "warning: delete branch: %v\n", delErr)
			}
		}
		if d.client.RemoteBranchExists(ctx, source.Name) {
			if rErr := d.client.DeleteRemoteBranch(ctx, source.Name); rErr != nil {
				fmt.Fprintf(d.client.IO().Err, "warning: delete remote branch: %v\n", rErr)
			}
		}
	}

	if d.pushConfirm != nil {
		skip, auto, rerr := pushflow.ResolveFlags(d.push, d.noPush, d.cfg.Push.Propose)
		if rerr != nil {
			return rerr //nolint:wrapcheck // pushflow already wraps
		}
		if perr := pushflow.Propose(ctx, d.client, pushflow.Opts{Branch: target, Skip: skip, AutoConfirm: auto}, d.pushConfirm); perr != nil {
			return perr //nolint:wrapcheck // pushflow already wraps
		}
	}

	fmt.Fprintf(d.client.IO().Out, "Branch %q merged into %q.\n", source.Name, target)

	return nil
}

// collectSources returns local ∪ remote-only branches minus target, with
// remote-only entries flagged. A RemoteBranchNames error degrades to local-only.
func collectSources(c *git.Client, target string) ([]SourceBranch, error) {
	locals, err := c.LocalBranchNames()
	if err != nil {
		return nil, fmt.Errorf("list local branches: %w", err)
	}

	localSet := make(map[string]bool, len(locals))
	var out []SourceBranch
	for _, n := range locals {
		if n == target {
			continue
		}
		localSet[n] = true
		out = append(out, SourceBranch{Name: n})
	}

	remotes, err := c.RemoteBranchNames()
	if err != nil {
		return out, nil //nolint:nilerr // best-effort: degrade to local-only
	}
	for _, n := range remotes {
		if n == target || localSet[n] {
			continue
		}
		out = append(out, SourceBranch{Name: n, RemoteOnly: true})
	}

	return out, nil
}
