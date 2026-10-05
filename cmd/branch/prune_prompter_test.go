package branch

import "context"

// Compile-time check: scriptedPrunePrompter must satisfy PrunePrompter.
var _ PrunePrompter = (*scriptedPrunePrompter)(nil)

// scriptedPrunePrompter is the canned-response prompter used by
// prune_e2e_test.go. Each field corresponds to one return value; *Err fields
// inject errors.
type scriptedPrunePrompter struct {
	Confirm    bool
	ConfirmErr error

	// Call-counter so tests can assert ConfirmPrune was (or was not) called.
	ConfirmCalls int

	// LastToClose + LastToMerge capture the most recent call's arguments
	// so tests can assert the values handed to the prompter.
	LastToClose int
	LastToMerge int
}

func (s *scriptedPrunePrompter) ConfirmPrune(_ context.Context, toClose, toMerge int) (bool, error) {
	s.ConfirmCalls++
	s.LastToClose = toClose
	s.LastToMerge = toMerge

	if s.ConfirmErr != nil {
		return false, s.ConfirmErr
	}

	return s.Confirm, nil
}
