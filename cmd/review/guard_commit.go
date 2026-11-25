package review

import "github.com/spf13/cobra"

// getGuardCommitCmd returns the internal `review guard-commit` command. It
// lets every commit through, and stays so that a pre-commit hook installed by
// an older `git zf init` keeps working. Hidden from help output.
func (r Review) getGuardCommitCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard-commit",
		Short:  "Internal: does nothing (called by the pre-commit hook of an older git zf init)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(*cobra.Command, []string) error { return nil },
	}
}
