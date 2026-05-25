package issue

import (
	"context"
	"errors"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tui"
)

// resolveBranchConflict checks whether b's name already exists locally and,
// if so, drives the operator through a picker:
//
//   - Checkout existing: switches to the colliding branch and returns (nil, nil).
//   - Create a variant:  prompts for a label, rebuilds the Branch with it, and
//     loops back to the existence check (the variant itself
//     could collide).
//   - Abort:              prints "Aborted." and returns (nil, nil).
//
// On a clean no-collision path, returns (b, nil). The caller must treat a
// (nil, nil) return as "stop here, do not create or persist".
func resolveBranchConflict(
	ctx context.Context,
	client *git.Client,
	b *branch.Branch,
	pickedIssue *issue.Issue,
) (*branch.Branch, error) {
	for {
		exists, err := client.BranchExists(b.Name())
		if err != nil {
			return nil, fmt.Errorf("check branch exists: %w", err)
		}

		if !exists {
			return b, nil
		}

		var action string
		if err := huh.NewForm(tui.BranchConflictPicker(b.Name(), &action)).RunWithContext(ctx); err != nil {
			return nil, fmt.Errorf("conflict picker: %w", err)
		}

		switch action {
		case "checkout":
			if err := client.Checkout(ctx, b.Name()); err != nil {
				return nil, fmt.Errorf("checkout existing: %w", err)
			}
			fmt.Fprintf(client.IO().Out, "Switched to existing branch %q\n", b.Name())

			return nil, nil
		case "abort":
			fmt.Fprintln(client.IO().Out, "Aborted.")

			return nil, nil
		case "variant":
			var label string
			if err := huh.NewForm(tui.VariantLabelInput(&label)).Run(); err != nil {
				return nil, fmt.Errorf("variant input: %w", err)
			}

			newB, err := rebuildVariantBranch(pickedIssue, label)
			if err != nil {
				return nil, err
			}
			b = newB
		default:
			return nil, fmt.Errorf("unknown conflict action %q", action)
		}
	}
}

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
