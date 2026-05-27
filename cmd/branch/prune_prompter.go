package branch

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
)

// PrunePrompter resolves the single user-facing decision in the prune flow:
// whether to proceed with the destructive store mutations after the summary
// has been printed. The production implementation drives a huh form; the
// auto-confirm implementation (wired via --yes) returns true unconditionally;
// the scripted implementation in prune_prompter_test.go returns canned values
// for tests.
type PrunePrompter interface {
	// ConfirmPrune is called only when there is at least one branch to delete
	// or to mark merged, AND the run is not a dry-run.
	ConfirmPrune(ctx context.Context, toDelete, toMerge int) (confirmed bool, err error)
}

// autoConfirmPrunePrompter unconditionally returns (true, nil). It is wired
// by the --yes / -y flag on `branch prune`.
type autoConfirmPrunePrompter struct{}

// newAutoConfirmPrunePrompter returns the auto-confirm PrunePrompter wired by --yes.
func newAutoConfirmPrunePrompter() *autoConfirmPrunePrompter {
	return &autoConfirmPrunePrompter{}
}

func (p *autoConfirmPrunePrompter) ConfirmPrune(_ context.Context, _, _ int) (bool, error) {
	return true, nil
}

// Compile-time check.
var _ PrunePrompter = (*huhPrunePrompter)(nil)

// huhPrunePrompter is the production PrunePrompter — opens a real huh form
// asking the operator to confirm. Constructed once per `branch prune`
// invocation.
type huhPrunePrompter struct{}

// newHuhPrunePrompter returns the interactive PrunePrompter used by default.
func newHuhPrunePrompter() *huhPrunePrompter {
	return &huhPrunePrompter{}
}

func (p *huhPrunePrompter) ConfirmPrune(ctx context.Context, toDelete, toMerge int) (bool, error) {
	var confirmed bool
	if err := huh.NewForm(tui.BranchPruneConfirm(toDelete, toMerge, &confirmed)).RunWithContext(ctx); err != nil {
		return false, fmt.Errorf("confirm prune form: %w", err)
	}

	return confirmed, nil
}
