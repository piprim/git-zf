package branch

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/tui"
)

// BranchMenuPrompter asks the questions of the branch table: the row's menu
// and the force-delete confirmation. huh in production, scripted in tests.
type BranchMenuPrompter interface {
	// PickAction returns the picked entry, huh.ErrUserAborted on esc.
	PickAction(ctx context.Context, title string, entries []branchAction) (branchAction, error)
	ConfirmForceDelete(ctx context.Context, name string) (bool, error)
	// ConfirmRemoveWorktree asks before the worktree holding a branch to
	// delete is removed: git checks its modified and untracked files, not
	// the ignored ones.
	ConfirmRemoveWorktree(ctx context.Context, name, path string) (bool, error)
}

type huhBranchMenuPrompter struct{}

func (huhBranchMenuPrompter) PickAction(ctx context.Context, title string, entries []branchAction) (branchAction, error) {
	opts := make([]tui.MenuOption, len(entries))
	for i, e := range entries {
		opts[i] = tui.MenuOption{Label: string(e), Value: string(e)}
	}
	var picked string
	if err := tui.Form(tui.MenuSelect(title, opts, &picked)).RunWithContext(ctx); err != nil {
		return "", err //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
	}

	return branchAction(picked), nil
}

func (huhBranchMenuPrompter) ConfirmForceDelete(ctx context.Context, name string) (bool, error) {
	var ok bool
	title := fmt.Sprintf("Branch %q is not fully merged. Delete it anyway?", name)
	if err := tui.Form(huh.NewGroup(huh.NewConfirm().Title(title).Value(&ok))).RunWithContext(ctx); err != nil {
		return false, err //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
	}

	return ok, nil
}

func (huhBranchMenuPrompter) ConfirmRemoveWorktree(ctx context.Context, name, path string) (bool, error) {
	var ok bool
	title := fmt.Sprintf("Branch %q is checked out in the worktree %s. Remove that folder and delete the branch?", name, path)
	confirm := huh.NewConfirm().Title(title).
		Description("Its git-ignored files (local config, .env, build output) are deleted with it.").Value(&ok)
	if err := tui.Form(huh.NewGroup(confirm)).RunWithContext(ctx); err != nil {
		return false, err //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
	}

	return ok, nil
}
