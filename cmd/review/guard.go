package review

import "github.com/spf13/cobra"

// getGuardCmd returns the internal `review guard <branch>` command. Nothing
// is locked for a review any more: it lets every push through, and stays so
// that a pre-push hook installed by an older `git zf init` keeps working.
// Hidden from help output.
func (r Review) getGuardCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "guard <branch>",
		Short:  "Internal: does nothing (called by the pre-push hook of an older git zf init)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE:   func(*cobra.Command, []string) error { return nil },
	}
}
