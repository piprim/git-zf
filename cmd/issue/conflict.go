package issue

import (
	"errors"
	"fmt"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/issue"
)

// rebuildVariantBranch is the pure half of the variant flow: it takes the
// operator's picked issue and the label they typed, and returns a freshly
// constructed *branch.Branch. Extracted so it can be unit-tested without
// the TUI.
func rebuildVariantBranch(pickedIssue *issue.Issue, label string) (*branch.Branch, error) {
	if label == "" {
		return nil, errors.New("variant label is empty")
	}

	b, err := branch.New(pickedIssue.ID, pickedIssue.Type, pickedIssue.Subject, label)
	if err != nil {
		return nil, fmt.Errorf("rebuild branch with variant: %w", err)
	}

	return b, nil
}
