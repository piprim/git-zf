package branch

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
)

// TrackerPrunePrompter resolves the per-branch reap action for the whole
// candidate batch in a single call. Mirrors PrunePrompter's role in the
// existing prune flow.
type TrackerPrunePrompter interface {
	// DecideReap returns a map from candidate BranchName to action, where
	// action is one of: "safe", "force", "skip". Callers must not invoke
	// DecideReap with an empty candidates slice.
	DecideReap(ctx context.Context, candidates []trackerCandidate) (map[string]string, error)
}

// fixedActionPrompter is wired by --safe-delete / --force-delete / --skip-delete.
// It returns the same action for every candidate without prompting the user.
type fixedActionPrompter struct {
	action string // "safe" | "force" | "skip"
}

func newFixedActionPrompter(action string) *fixedActionPrompter {
	return &fixedActionPrompter{action: action}
}

func (p *fixedActionPrompter) DecideReap(_ context.Context, candidates []trackerCandidate) (map[string]string, error) {
	out := make(map[string]string, len(candidates))
	for _, c := range candidates {
		out[c.BranchName] = p.action
	}

	return out, nil
}

var _ TrackerPrunePrompter = (*fixedActionPrompter)(nil)

// huhTrackerPrunePrompter is the interactive default. It builds one huh.Form
// containing a single huh.Group with one stacked Select per candidate.
// "safe" is pre-selected.
type huhTrackerPrunePrompter struct{}

func newHuhTrackerPrunePrompter() *huhTrackerPrunePrompter {
	return &huhTrackerPrunePrompter{}
}

func (p *huhTrackerPrunePrompter) DecideReap(ctx context.Context, candidates []trackerCandidate) (map[string]string, error) {
	if len(candidates) == 0 {
		return map[string]string{}, nil
	}

	// Per-candidate value cells; pointers handed to huh so Submit writes back.
	values := make([]string, len(candidates))
	fields := make([]huh.Field, 0, len(candidates))
	for i := range candidates {
		values[i] = "safe" // pre-selected default
		v := &values[i]
		c := candidates[i]
		fields = append(fields,
			huh.NewSelect[string]().
				Title(fmt.Sprintf("%s  (issue %s closed in tracker)", c.BranchName, c.IssueID)).
				Options(
					huh.NewOption("safe-delete (git branch -d)", "safe"),
					huh.NewOption("force-delete (git branch -D)", "force"),
					huh.NewOption("skip ref delete; only mark closed", "skip"),
				).
				Value(v),
		)
	}

	if err := huh.NewForm(huh.NewGroup(fields...)).RunWithContext(ctx); err != nil {
		return nil, fmt.Errorf("tracker-prune decide form: %w", err)
	}

	out := make(map[string]string, len(candidates))
	for i := range candidates {
		out[candidates[i].BranchName] = values[i]
	}

	return out, nil
}

var _ TrackerPrunePrompter = (*huhTrackerPrunePrompter)(nil)
