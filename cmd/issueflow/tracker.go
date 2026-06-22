package issueflow

import (
	"context"
	"fmt"
	"io"

	"github.com/piprim/git-zf/tracker"
)

// ApplyTrackerStatus fetches tracker statuses, prompts the user to pick one,
// and applies it. All errors are non-fatal warnings. A nil tracker is a no-op.
func ApplyTrackerStatus(
	ctx context.Context,
	t tracker.Tracker,
	errW io.Writer,
	issueID, trackerType string,
	pick func(ctx context.Context, issueID, trackerType string, statuses []string) (string, error),
) {
	if t == nil {
		return
	}

	statuses, err := t.ListStatuses(ctx)
	if err != nil {
		fmt.Fprintf(errW, "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	selected, err := pick(ctx, issueID, trackerType, statuses)
	if err != nil {
		fmt.Fprintf(errW, "warning: status picker: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := t.UpdateIssueStatus(ctx, issueID, selected); err != nil {
		fmt.Fprintf(errW, "warning: update tracker status: %v\n", err)
	}
}
