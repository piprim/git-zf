// Package mergeflow is the reusable merge engine shared by `issue close` and
// `branch merge`. It owns the generic middle of a branch merge — dry-run →
// pick strategy → confirm → execute (Classic/Squash/Rebase) — and knows nothing
// about issues, stores, or trackers. The engine advances Target; Source is
// merged into it. Each caller supplies its own Prompter and PrefillFunc.
package mergeflow

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-git/go-git/v6/plumbing"
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
}

// Prompter resolves the generic user-facing merge decisions. issue close's
// *huhPrompter already satisfies it; branch merge ships its own implementation.
type Prompter interface {
	PickStrategy(ctx context.Context) (commit.MergeStrategy, error)
	ConfirmMerge(ctx context.Context, source, target string, s commit.MergeStrategy) (confirmed bool, err error)
	ComposeMessage(ctx context.Context, prefill map[string]any) (msg []byte, opts tui.CommitOption, err error)
}

// PrefillFunc builds the commit-message prefill for the resolved tips. close
// returns an issue-flavored map; branch merge returns a plain merge map.
type PrefillFunc func(s commit.MergeStrategy, sourceTip, targetTip plumbing.Hash) map[string]any

// Result reports what happened so callers run their own post-steps.
type Result struct {
	Strategy            commit.MergeStrategy
	Aborted             bool // user declined at ConfirmMerge
	FastForwardDeferred bool // rebase committed on Source but post-FF of Target failed
}

// errFastForwardDeferred is internal; Run surfaces it as Result.FastForwardDeferred.
var errFastForwardDeferred = errors.New("commit created, fast-forward deferred")

type run struct {
	client       *git.Client
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
	r := &run{
		client: client, source: p.Source, target: p.Target,
		materialized: p.SourceMaterialized, prompter: prompter, prefill: prefill,
	}

	// The Target may not exist locally; LocalOrRemoteRef falls back to origin/<target>.
	dryRunBase := client.LocalOrRemoteRef(p.Target)
	conflicts, err := client.MergeDryRun(ctx, p.Source, dryRunBase)
	if err != nil {
		return Result{}, fmt.Errorf("merge dry-run: %w", err)
	}
	if len(conflicts) > 0 {
		fmt.Fprintln(client.IO().Out, "Conflicts detected:")
		for _, f := range conflicts {
			fmt.Fprintln(client.IO().Out, "  "+f)
		}
		fmt.Fprintln(client.IO().Out, "Aborting.")

		return Result{}, fmt.Errorf("merge conflicts in branch %q", p.Source)
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
		return Result{Strategy: strategy, FastForwardDeferred: true}, nil
	}
	if err != nil {
		return Result{Strategy: strategy}, err
	}

	return Result{Strategy: strategy}, nil
}
