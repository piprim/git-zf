package branch

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/tty"
	"github.com/piprim/git-zf/tui"
)

// branchAction is one entry of a row's menu; its value is the label shown.
type branchAction string

const (
	actionCheckout   branchAction = "checkout"
	actionIssueClose branchAction = "issue close (merge)"
	actionMerge      branchAction = "merge into current"
	actionPR         branchAction = "pull request"
	actionAbandon    branchAction = "abandon (record closed, no merge)"
	actionTrack      branchAction = "track"
	actionLog        branchAction = "log"
	actionDelete     branchAction = "delete"
)

// menuEntriesFor is the menu of a row: RemoteOnly first, then Current, then
// the record. A plain branch named like an issue branch gets no merge entry
// (branch merge refuses issue-looking names).
func menuEntriesFor(row branch.Row) []branchAction {
	if row.RemoteOnly {
		return []branchAction{actionCheckout, actionLog}
	}
	var entries []branchAction
	switch {
	case row.Plain:
		entries = []branchAction{actionCheckout, actionTrack, actionMerge, actionPR, actionLog, actionDelete}
		if _, err := branch.Parse(row.BranchName); err == nil {
			entries = without(entries, actionMerge)
		}
	case row.Status == branch.StatusInProgress:
		entries = []branchAction{actionCheckout, actionIssueClose, actionPR, actionAbandon, actionLog, actionDelete}
	case row.Status == branch.StatusClosed:
		entries = []branchAction{actionCheckout, actionTrack, actionLog, actionDelete}
	default: // merged
		entries = []branchAction{actionCheckout, actionLog, actionDelete}
	}
	if row.Current {
		entries = without(entries, actionCheckout, actionMerge, actionDelete)
	}

	return entries
}

func without(entries []branchAction, drop ...branchAction) []branchAction {
	kept := make([]branchAction, 0, len(entries))
	for _, e := range entries {
		keep := true
		for _, d := range drop {
			if e == d {
				keep = false
			}
		}
		if keep {
			kept = append(kept, e)
		}
	}

	return kept
}

// tableDeps is what the table's actions need. The funcs of other commands
// are injected; a nil func removes its entry from the menu.
type tableDeps struct {
	client *git.Client
	cfg    *config.AppConfig
	remote string // the clone's remote, for a RemoteOnly row's ref; "" when none
	stdout io.Writer
	stderr io.Writer

	merge        func(ctx context.Context, source SourceBranch) error
	track        func(ctx context.Context, name string) error
	closeIssue   func(ctx context.Context, issueSlug, branchName string) error
	log          func(ctx context.Context, ref string) error
	pr           func(ctx context.Context, branchName string) error
	newBranch    func(ctx context.Context) error
	prune        func(ctx context.Context) error
	pruneTracker func(ctx context.Context) error
}

// offeredEntries is menuEntriesFor without the entries whose func is nil.
func offeredEntries(d tableDeps, row branch.Row) []branchAction {
	wired := map[branchAction]bool{
		actionMerge: d.merge != nil, actionTrack: d.track != nil, actionIssueClose: d.closeIssue != nil,
		actionLog: d.log != nil, actionPR: d.pr != nil,
		actionCheckout: true, actionAbandon: true, actionDelete: true,
	}
	var out []branchAction
	for _, e := range menuEntriesFor(row) {
		if wired[e] {
			out = append(out, e)
		}
	}

	return out
}

// runBranchTable loops table → menu → action until the table quits or an
// action that moves HEAD runs. stdout=true prints the rows (no TTY).
func runBranchTable(ctx context.Context, d tableDeps, p BranchMenuPrompter, opts tui.BranchTableOptions, stdout bool) error {
	for {
		rows, err := branch.TableRows(ctx, d.client, opts.Remote)
		if err != nil {
			return fmt.Errorf("list branches: %w", err)
		}
		if len(rows) == 0 {
			fmt.Fprintln(d.stdout, "No branches found.")

			return nil
		}
		if stdout {
			tty.RenderBranchRows(d.stdout, localRows(rows), opts.Remote)

			return nil
		}

		m, err := tea.NewProgram(tui.BranchTableModel(rows, opts), tea.WithContext(ctx)).Run()
		if err != nil {
			return fmt.Errorf("run table: %w", err)
		}
		pick, row := tui.PickedBranch(m)
		opts = tui.BranchTableState(m) // the next table opens as this one was left
		var reopen bool
		switch pick {
		case tui.BranchPickNone:
			return nil
		case tui.BranchPickRow:
			a, err := p.PickAction(ctx, row.BranchName, offeredEntries(d, *row))
			if errors.Is(err, huh.ErrUserAborted) {
				continue
			}
			if err != nil {
				return err
			}
			reopen, err = runBranchAction(ctx, d, p, a, *row)
		default:
			reopen, err = runPick(ctx, d, pick)
		}
		if !afterAction(reopen, err, d.stderr) {
			return nil
		}
	}
}

// afterAction decides whether the table reopens after an action: an aborted
// form (esc) reopens quietly, an error is printed and reopens, otherwise the
// action says (checkout and issue close moved HEAD: no reopen).
func afterAction(reopen bool, err error, stderr io.Writer) bool {
	switch {
	case errors.Is(err, huh.ErrUserAborted):
		return true
	case err != nil:
		fmt.Fprintln(stderr, "error:", err)

		return true
	}

	return reopen
}

func localRows(rows []branch.Row) []branch.Row {
	out := make([]branch.Row, 0, len(rows))
	for _, r := range rows {
		if !r.RemoteOnly {
			out = append(out, r)
		}
	}

	return out
}

// runPick runs the key that quit the table without a row: n, p, P. reopen is
// false after new (it checks a branch out).
func runPick(ctx context.Context, d tableDeps, pick tui.BranchPick) (reopen bool, err error) {
	switch pick {
	case tui.BranchPickNew:
		return false, d.newBranch(ctx)
	case tui.BranchPickPrune:
		return true, d.prune(ctx)
	case tui.BranchPickPruneTracker:
		return true, d.pruneTracker(ctx)
	}

	return true, nil
}

// runBranchAction runs one entry; reopen is false for checkout and issue
// close, which leave the user on another branch.
func runBranchAction(ctx context.Context, d tableDeps, p BranchMenuPrompter, a branchAction, row branch.Row) (reopen bool, err error) {
	switch a {
	case actionCheckout:
		return false, checkoutBranch(ctx, d.client, row.BranchName)
	case actionIssueClose:
		return false, d.closeIssue(ctx, row.IssueSlug, row.BranchName)
	case actionMerge:
		return true, d.merge(ctx, SourceBranch{Name: row.BranchName, RemoteOnly: row.RemoteOnly})
	case actionPR:
		return true, d.pr(ctx, row.BranchName)
	case actionAbandon:
		return true, runCloseBranch(ctx, d.client, row.BranchName)
	case actionTrack:
		return true, d.track(ctx, row.BranchName)
	case actionLog:
		// The full ref: a remote-only name has no local ref, and a short
		// name can be ambiguous with a path.
		ref := "refs/heads/" + row.BranchName
		if row.RemoteOnly {
			ref = "refs/remotes/" + d.remote + "/" + row.BranchName
		}

		return true, d.log(ctx, ref)
	case actionDelete:
		return true, deleteBranch(ctx, d.client, p, row)
	}

	return true, fmt.Errorf("unknown action %q", a)
}

// checkoutBranch checks name out, a remote-only name included (git creates
// the local branch with its upstream). A branch held by another working tree
// is refused, as mergeflow.SourceTree does.
func checkoutBranch(ctx context.Context, c *git.Client, name string) error {
	wt, err := c.HoldingWorktree(ctx, name)
	if err != nil {
		return fmt.Errorf("locate worktree for %q: %w", name, err)
	}
	switch {
	case wt == nil || git.SamePath(wt.Path, c.WorkingTreeRoot()):
	case wt.Prunable:
		return fmt.Errorf("branch %q is held by a stale worktree entry (%s): run `git worktree prune`", name, wt.Path)
	default:
		return fmt.Errorf("branch %q is checked out in worktree %s", name, wt.Path)
	}

	return c.Checkout(ctx, name)
}

// deleteBranch deletes the row's git branch, asking before forcing an
// unmerged one. The record is untouched: a local delete does not say whether
// a teammate still works on the branch; prune checks the remote.
func deleteBranch(ctx context.Context, c *git.Client, p BranchMenuPrompter, row branch.Row) error {
	err := c.DeleteLocalBranch(ctx, row.BranchName, false)
	if errors.Is(err, git.ErrBranchNotMerged) {
		force, cerr := p.ConfirmForceDelete(ctx, row.BranchName)
		if cerr != nil || !force {
			return cerr
		}
		err = c.DeleteLocalBranch(ctx, row.BranchName, true)
	}
	if err != nil {
		return err
	}
	fmt.Fprintf(c.IO().Out, "Branch %q deleted.", row.BranchName)
	if !row.Plain && row.Status == branch.StatusInProgress {
		fmt.Fprint(c.IO().Out, " Its record is still in progress: p (prune) records what happened to it (closed, or merged when its tip is in the base).")
	}
	fmt.Fprintln(c.IO().Out)

	return nil
}
