// Package pushflow implements the shared "offer to push a branch after the
// action" step used by issue close, commit, and the review lifecycle commands.
package pushflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/spf13/cobra"
)

// Pusher is the slice of *git.Client the proposal step needs.
type Pusher interface {
	Remote() (string, error)
	PushDryRun(ctx context.Context, branch string) (git.PushOutcome, bool, error)
	PushBranch(ctx context.Context, branch string) error
	IsAncestor(ctx context.Context, child, ancestor string) (bool, error)
	MergeDryRun(ctx context.Context, branchName, baseBranch string) ([]string, error)
	IO() *pkg.IO
}

// Compile-time check that the production client satisfies the role.
var _ Pusher = (*git.Client)(nil)

// ConfirmFunc asks the operator to confirm the push. Production uses a huh
// form (NewHuhConfirm); tests inject a stub.
type ConfirmFunc func(ctx context.Context, summary string) (bool, error)

// Opts configures one Propose call.
type Opts struct {
	Branch         string // branch to push
	Skip           bool   // --no-push or config push.propose=false
	AutoConfirm    bool   // --push: push without prompting
	NonInteractive bool   // -y / no TTY: skip unless AutoConfirm

	// Merge-vs-parent preview (commit). When IncludeMergePreview is true and
	// Parent is a non-empty branch/ref distinct from Branch, Propose prints how
	// Branch would merge into Parent (fast-forward / merge commit / conflicts /
	// already merged) alongside the push preview.
	IncludeMergePreview bool
	Parent              string
}

// Propose runs the preview → confirm → push step. It returns nil on every skip
// path (gated off, no remote, nothing to push, unreachable remote, declined).
// A confirmed push that fails returns the wrapped git error; the caller's
// already-completed action is not rolled back.
func Propose(ctx context.Context, c Pusher, opts Opts, confirm ConfirmFunc) error {
	if opts.Skip || opts.Branch == "" {
		return nil
	}

	remote, err := c.Remote()
	if err != nil || remote == "" {
		return nil
	}

	outcome, ok, err := c.PushDryRun(ctx, opts.Branch)
	if err != nil || !ok {
		// Unreachable remote or nothing to push → skip silently.
		return nil
	}

	fmt.Fprintf(c.IO().Out, "Push %q to %s — %s\n", opts.Branch, remote, outcome.Summary)

	if opts.IncludeMergePreview && opts.Parent != "" && opts.Parent != opts.Branch {
		if line := mergePreviewLine(ctx, c, opts.Branch, opts.Parent); line != "" {
			fmt.Fprintln(c.IO().Out, line)
		}
	}

	if opts.NonInteractive && !opts.AutoConfirm {
		return nil
	}

	proceed := opts.AutoConfirm
	if !proceed {
		proceed, err = confirm(ctx, fmt.Sprintf("Push %q to %s?", opts.Branch, remote))
		if err != nil {
			return fmt.Errorf("push confirm: %w", err)
		}
	}
	if !proceed {
		return nil
	}

	if err := c.PushBranch(ctx, opts.Branch); err != nil {
		return fmt.Errorf("push %q: %w", opts.Branch, err)
	}
	fmt.Fprintf(c.IO().Out, "Pushed %q to %s.\n", opts.Branch, remote)

	return nil
}

// mergePreviewLine describes how current would merge into parent, using the same
// read-only primitives as the close flow (IsAncestor + MergeDryRun via
// git merge-tree). Returns "" when the relationship cannot be determined (e.g.
// MergeDryRun errors), so the caller simply omits the line.
func mergePreviewLine(ctx context.Context, c Pusher, current, parent string) string {
	if merged, err := c.IsAncestor(ctx, current, parent); err == nil && merged {
		return fmt.Sprintf("Already merged into %s", parent)
	}
	if ff, err := c.IsAncestor(ctx, parent, current); err == nil && ff {
		return fmt.Sprintf("Fast-forwards into %s", parent)
	}
	conflicts, err := c.MergeDryRun(ctx, current, parent)
	if err != nil {
		return ""
	}
	if len(conflicts) > 0 {
		return fmt.Sprintf("⚠ Conflicts with %s: %s", parent, strings.Join(conflicts, ", "))
	}
	return fmt.Sprintf("Merges into %s with a merge commit (no conflicts)", parent)
}

// ResolveFlags combines the --push/--no-push flags with the config master
// switch into the (skip, autoConfirm) decision. The two flags are mutually
// exclusive.
func ResolveFlags(push, noPush, propose bool) (skip, autoConfirm bool, err error) {
	if push && noPush {
		return false, false, errors.New("--push and --no-push are mutually exclusive")
	}
	if noPush || !propose {
		return true, false, nil
	}

	return false, push, nil
}

// AddFlags registers --push and --no-push on cmd. Call from each command that
// offers the push proposal.
func AddFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("push", false, "push the resulting branch to the remote after the action (skips the prompt)")
	cmd.Flags().Bool("no-push", false, "skip the post-action push proposal")
}

// ReadFlags reads --push/--no-push from cmd. Missing flags read as false, so it
// is safe to call from a shared deps builder used by commands that did not add
// the flags. Mutual exclusion is enforced later by ResolveFlags.
func ReadFlags(cmd *cobra.Command) (push, noPush bool) {
	if cmd.Flags().Lookup("push") != nil {
		push, _ = cmd.Flags().GetBool("push")
	}
	if cmd.Flags().Lookup("no-push") != nil {
		noPush, _ = cmd.Flags().GetBool("no-push")
	}

	return push, noPush
}
