// Package mergeflow is the reusable merge engine shared by `issue close` and
// `branch merge`. It owns the generic middle of a branch merge — dry-run →
// pick strategy → confirm → execute (Classic/Squash/Rebase) — and knows nothing
// about issues, stores, or trackers. The engine advances Target; Source is
// merged into it. Each caller supplies its own Prompter and PrefillFunc;
// HuhPrompter holds the huh forms both callers share.
package mergeflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/piprim/git-zf/commit"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tui"
)

// Params identify WHAT to merge. The engine advances Target; Source is merged in.
type Params struct {
	Source string
	Target string

	// SourceMaterialized widens abort-rollback: when true, staged residue is
	// discarded on abort (the source branch is disposable / reproducible from
	// origin). close sets this for ref-derived picks; branch merge sets it when
	// it materialized a remote-only source.
	SourceMaterialized bool

	// SourceClient is a client opened at the linked worktree that has Source
	// checked out (see SourceTree). When nil the engine runs
	// single-tree on the client passed to Run. When set, Rebase performs its
	// source-side steps (merge, soft reset, commit) on that tree, because git
	// refuses to check out a branch that another worktree holds.
	SourceClient *git.Client

	// PullRequest, when set, merges Source's pull request on the forge
	// instead of locally (see ForgeMerge). The dry-run, the strategy picker
	// and the confirm run as for a local merge; no commit message is composed.
	PullRequest *ForgeMerge
}

// Prompter resolves the generic user-facing merge decisions. HuhPrompter is
// the production implementation, embedded by both callers' prompters.
type Prompter interface {
	PickStrategy(ctx context.Context) (commit.MergeStrategy, error)
	ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (confirmed bool, err error)
	ComposeMessage(ctx context.Context, prefill map[string]any) (msg []byte, opts tui.CommitOption, err error)
}

// PrefillFunc builds the commit-message prefill for the resolved tips. close
// returns an issue-flavored map; branch merge returns a plain merge map.
type PrefillFunc func(s commit.MergeStrategy, sourceTip, targetTip git.Hash) map[string]any

// Result reports what happened so callers run their own post-steps.
type Result struct {
	Strategy            commit.MergeStrategy
	Aborted             bool // user declined at ConfirmMerge
	FastForwardDeferred bool // rebase committed on Source but post-FF of Target failed
	ViaForge            bool // the forge merged the pull request; Target was fast-forwarded
}

// errFastForwardDeferred is internal; Run surfaces it as Result.FastForwardDeferred.
var errFastForwardDeferred = errors.New("commit created, fast-forward deferred")

type run struct {
	client       *git.Client // main tree: every target-side operation
	src          *git.Client // source-side operations; == client in single-tree mode
	source       string
	target       string
	materialized bool
	prompter     Prompter
	prefill      PrefillFunc
}

// Run merges Source into Target: dry-run conflict check → pick strategy →
// confirm → execute. It performs no post-merge steps (delete/push) — the caller
// runs those from Result.
func Run(ctx context.Context, client *git.Client, p Params, prompter Prompter, prefill PrefillFunc) (Result, error) {
	src := p.SourceClient
	if src == nil {
		src = client
	} else if remote, err := client.Remote(); err == nil && remote != "" {
		// The source-side MergeRebase resolves the remote on its own client;
		// pin it to the main client's remote so both agree.
		src.SetRemote(remote)
	}
	r := &run{
		client: client, src: src, source: p.Source, target: p.Target,
		materialized: p.SourceMaterialized, prompter: prompter, prefill: prefill,
	}

	// The Target may not exist locally; LocalOrRemoteRef falls back to origin/<target>.
	if err := r.mergeDryRun(ctx, client.LocalOrRemoteRef(p.Target)); err != nil {
		return Result{}, err
	}

	strategy, err := prompter.PickStrategy(ctx)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // prompter already wraps
	}
	confirmed, err := prompter.ConfirmMerge(ctx, p.Source, p.Target, strategy)
	if err != nil {
		return Result{}, err //nolint:wrapcheck // prompter already wraps
	}
	if !confirmed {
		return Result{Strategy: strategy, Aborted: true}, nil
	}

	// The commit each working tree is on before the merge moves it: HEAD, not
	// the target's tip. The submodules on disk match what is checked out (the
	// source, in a single tree), so the gitlinks that differ between that
	// commit and the final HEAD are the checkouts the merge left behind. The
	// source's own worktree counts too: the rebase merges the target into it.
	type treeHead struct {
		tree *git.Client
		head git.Hash
	}
	var heads []treeHead
	trees := []*git.Client{client}
	if src != client {
		trees = append(trees, src)
	}
	for _, tree := range trees {
		if head, err := tree.ResolveRef("HEAD"); err == nil {
			heads = append(heads, treeHead{tree, head})
		}
	}
	updateSubmodules := func() {
		for _, h := range heads {
			h.tree.UpdateMovedSubmodules(ctx, h.head)
		}
	}

	if p.PullRequest != nil {
		stop := client.IO().Spin("Merging the pull request #" + p.PullRequest.Number + " on the forge")
		defer stop()
		err := r.forge(ctx, p.PullRequest, strategy)
		stop()
		if err != nil {
			return Result{Strategy: strategy}, err
		}
		updateSubmodules()

		return Result{Strategy: strategy, ViaForge: true}, nil
	}

	switch strategy {
	case commit.MergeStrategyClassic:
		err = r.classic(ctx)
	case commit.MergeStrategySquash:
		err = r.squash(ctx)
	case commit.MergeStrategyRebase:
		err = r.rebase(ctx)
	default:
		return Result{Strategy: strategy}, fmt.Errorf("unknown strategy %q", strategy)
	}

	if errors.Is(err, errFastForwardDeferred) {
		// The target did not move, the source's tree did.
		updateSubmodules()

		return Result{Strategy: strategy, FastForwardDeferred: true}, nil
	}
	if err != nil {
		return Result{Strategy: strategy}, err
	}
	updateSubmodules()

	return Result{Strategy: strategy}, nil
}
