package branch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

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

// menuEntriesFor is the menu of a row: NoBranch (a record alone: nothing but
// abandon applies), then RemoteOnly, then Current, then
// the record. A plain branch named like an issue branch gets no merge entry
// (branch merge refuses issue-looking names).
func menuEntriesFor(row *branch.Row) []branchAction {
	if row.NoBranch {
		return []branchAction{actionAbandon}
	}
	if row.RemoteOnly {
		if !row.Plain && row.Status == branch.StatusInProgress {
			return []branchAction{actionCheckout, actionAbandon, actionLog, actionDelete}
		}

		return []branchAction{actionCheckout, actionLog, actionDelete}
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
func offeredEntries(d *tableDeps, row *branch.Row) []branchAction {
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

var keyActions = map[string]branchAction{
	"C": actionCheckout,
	"c": actionIssueClose,
	"m": actionMerge,
	"u": actionPR,
	"a": actionAbandon,
	"t": actionTrack,
	"l": actionLog,
	"D": actionDelete,
}

func offersKey(d *tableDeps, row *branch.Row, key string) bool {
	a, ok := keyActions[key]
	if !ok {
		return false
	}

	return slices.Contains(offeredEntries(d, row), a)
}

func runKey(
	ctx context.Context, d *tableDeps, p BranchMenuPrompter, key string, row *branch.Row,
) (reopen bool, err error) {
	if !offersKey(d, row, key) {
		return true, fmt.Errorf("key %q is not offered for branch %q", key, row.BranchName)
	}

	return runBranchAction(ctx, d, p, keyActions[key], row)
}

// runBranchTable loops table → menu → action until the table quits or an
// action that moves HEAD runs. stdout=true prints the rows (no TTY).
func runBranchTable(
	ctx context.Context, d *tableDeps, p BranchMenuPrompter, initial *tui.BranchTableOptions, stdout bool,
) error {
	opts := *initial // a copy: each loop replaces it by the state the table was left in
	opts.Offers = func(row branch.Row, key string) bool { return offersKey(d, &row, key) }
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
			tty.RenderBranchRows(d.stdout, defaultRows(rows), opts.Remote)

			return nil
		}

		m, err := tui.RunScreen(ctx, tui.BranchTableModel(rows, opts))
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
			reopen, err = runMenu(ctx, d, p, row)
		case tui.BranchPickKey:
			reopen, err = runKey(ctx, d, p, tui.PickedBranchKey(m), row)
		default:
			reopen, err = runPick(ctx, d, pick)
		}
		if !afterAction(reopen, err, d.stderr) {
			return nil
		}
	}
}

// runMenu is enter on a row: the menu of its entries, then the picked one.
// Both errors are the caller's to hand to afterAction: a menu left with esc
// reopens the table quietly, a failed action is printed.
func runMenu(ctx context.Context, d *tableDeps, p BranchMenuPrompter, row *branch.Row) (reopen bool, err error) {
	title := row.BranchName
	if row.NoBranch {
		title += " — no branch here; started by " + row.Author + ", who may not have pushed it yet"
	}
	a, err := p.PickAction(ctx, title, offeredEntries(d, row))
	if err != nil {
		return true, fmt.Errorf("pick action: %w", err)
	}

	return runBranchAction(ctx, d, p, a, row)
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
		tui.Pause(stderr)

		return true
	}

	return reopen
}

// defaultRows is what the table shows before R: the local branches and the
// remote-only ones recorded in progress.
func defaultRows(rows []branch.Row) []branch.Row {
	out := make([]branch.Row, 0, len(rows))
	for i := range rows {
		if r := &rows[i]; !r.RemoteOnly || (!r.Plain && r.Status == branch.StatusInProgress) {
			out = append(out, rows[i])
		}
	}

	return out
}

// runPick runs the key that quit the table without a row: n, p, P. reopen is
// false after new (it checks a branch out).
func runPick(ctx context.Context, d *tableDeps, pick tui.BranchPick) (reopen bool, err error) {
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
func runBranchAction(
	ctx context.Context, d *tableDeps, p BranchMenuPrompter, a branchAction, row *branch.Row,
) (reopen bool, err error) {
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

	return c.Checkout(ctx, name) //nolint:wrapcheck // Checkout names the branch
}

// deleteBranch deletes the row's git branch, asking before forcing an
// unmerged one. A linked worktree holding the branch goes first
// (releaseWorktree): git refuses the delete otherwise. The record is
// untouched: a local delete does not say whether a teammate still works on
// the branch; prune checks the remote.
func deleteBranch(ctx context.Context, c *git.Client, p BranchMenuPrompter, row *branch.Row) error {
	if row.RemoteOnly {
		return deleteRemoteBranch(ctx, c, p, row)
	}

	force, proceed, err := releaseWorktree(ctx, c, p, row.BranchName)
	if err != nil || !proceed {
		return err
	}

	err = c.DeleteLocalBranch(ctx, row.BranchName, force)
	if errors.Is(err, git.ErrBranchNotMerged) {
		ok, cerr := p.ConfirmForceDelete(ctx, row.BranchName)
		if cerr != nil || !ok {
			return cerr //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
		}
		err = c.DeleteLocalBranch(ctx, row.BranchName, true)
	}
	if err != nil {
		return err //nolint:wrapcheck // DeleteLocalBranch names the branch
	}
	fmt.Fprintf(c.IO().Out, "Branch %q deleted.", row.BranchName)
	if !row.Plain && row.Status == branch.StatusInProgress {
		fmt.Fprint(c.IO().Out, " Its record is still in progress: p (prune) records what happened to it"+
			" (closed, or merged when its tip is in the base) once it is gone from the remote; a abandons it now.")
	}
	fmt.Fprintln(c.IO().Out)

	return nil
}

// deleteRemoteBranch deletes a branch that exists on the remote alone, after
// a confirmation that says whether the base branch holds its tip: the delete is for
// every clone. Nothing is fetched: the delete is leased on the tip this clone
// knows, so a branch that moved on the remote is refused; one the remote no
// longer has loses its stale remote-tracking ref here. A record in progress is then closed (runCloseBranch): nothing
// is left to work on. Closed, never merged: see classifyPrune.
func deleteRemoteBranch(ctx context.Context, c *git.Client, p BranchMenuPrompter, row *branch.Row) error {
	remote, err := c.Remote()
	if err != nil {
		return fmt.Errorf("resolve remote: %w", err)
	}
	// The hash the question is asked about is the one the delete is leased on.
	tip, err := c.ResolveRef("refs/remotes/" + remote + "/" + row.BranchName)
	if err != nil {
		return err //nolint:wrapcheck // ResolveRef names the ref
	}
	base, err := c.DefaultBaseBranch()
	if err != nil {
		return fmt.Errorf("detect base branch: %w", err)
	}
	// Merged on the forge and fetched, or merged here and not pushed: either
	// base holds the tip. A clone may have no local base at all.
	merged := false
	for _, ref := range []string{"refs/heads/" + base, "refs/remotes/" + remote + "/" + base} {
		if _, rerr := c.ResolveRef(ref); rerr != nil || merged {
			continue
		}
		if merged, err = c.IsAncestor(ctx, string(tip), ref); err != nil {
			return err //nolint:wrapcheck // IsAncestor names the command
		}
	}
	ok, err := p.ConfirmDeleteRemote(ctx, row.BranchName, remote, base, merged)
	if err != nil || !ok {
		return err //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
	}

	stop := c.IO().Spin("Deleting " + remote + "/" + row.BranchName)
	gone, err := c.DeleteRemoteBranchAt(ctx, row.BranchName, tip)
	stop()
	if err != nil {
		return err //nolint:wrapcheck // DeleteRemoteBranchAt names the branch
	}
	if gone {
		fmt.Fprintf(c.IO().Out,
			"Branch %q was already gone on %s: its stale local copy is removed.\n", row.BranchName, remote)
	} else {
		fmt.Fprintf(c.IO().Out, "Branch %q deleted on %s.\n", row.BranchName, remote)
	}
	if row.Plain || row.Status != branch.StatusInProgress {
		return nil
	}

	return closeRecord(ctx, c, row.BranchName, "")
}

// releaseWorktree removes the linked worktree holding name, when there is
// one, so that the branch can be deleted. Every question comes before the
// removal: the force of an unmerged branch first (force), then the folder,
// named, since its ignored files go with it. proceed is false when the user
// said no to either; nothing was touched then. The removal runs without
// --force: modified or untracked files stop the delete. The main working
// tree and a stale entry are refused, as checkoutBranch does; the tree the
// command runs in is left to git's own refusal.
func releaseWorktree(
	ctx context.Context, c *git.Client, p BranchMenuPrompter, name string,
) (force, proceed bool, err error) {
	wt, err := c.HoldingWorktree(ctx, name)
	if err != nil {
		return false, false, fmt.Errorf("locate worktree for %q: %w", name, err)
	}
	switch {
	case wt == nil || git.SamePath(wt.Path, c.WorkingTreeRoot()):
		return false, true, nil
	case wt.Main:
		return false, false, fmt.Errorf("branch %q is checked out in the main working tree (%s)", name, wt.Path)
	case wt.Prunable:
		return false, false, fmt.Errorf(
			"branch %q is held by a stale worktree entry (%s): run `git worktree prune`", name, wt.Path)
	}

	merged, err := mergedForDelete(ctx, c, name)
	if err != nil {
		return false, false, err
	}
	if !merged {
		ok, cerr := p.ConfirmForceDelete(ctx, name)
		if cerr != nil || !ok {
			return false, false, cerr //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
		}
	}

	ok, err := p.ConfirmRemoveWorktree(ctx, name, wt.Path)
	if err != nil {
		return false, false, err //nolint:wrapcheck // huh.ErrUserAborted must stay detectable
	}
	if !ok {
		fmt.Fprintf(c.IO().Out,
			"Worktree %s kept, and branch %q with it: git cannot delete a branch a worktree holds.\n", wt.Path, name)

		return false, false, nil
	}
	if err := c.RemoveWorktree(ctx, wt.Path); err != nil {
		return false, false, fmt.Errorf("branch %q is checked out in a worktree: %w", name, err)
	}
	fmt.Fprintf(c.IO().Out, "Worktree %s removed.\n", wt.Path)

	return !merged, true, nil
}

// mergedForDelete says what `git branch -d` would: name is merged when its
// upstream holds its tip, or HEAD for a branch without an upstream.
func mergedForDelete(ctx context.Context, c *git.Client, name string) (bool, error) {
	ref := "refs/heads/" + name
	if merged, err := c.IsAncestor(ctx, ref, ref+"@{upstream}"); err == nil {
		return merged, nil
	}

	return c.IsAncestor(ctx, ref, "HEAD") //nolint:wrapcheck // IsAncestor names the command
}
