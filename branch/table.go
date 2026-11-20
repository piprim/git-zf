package branch

import (
	"context"
	"fmt"
	"slices"

	"github.com/piprim/git-zf/git"
)

// TableRows is one Row per local branch in name order, then one per branch
// that exists only on remote (the name the caller got from c.Remote(); ""
// skips them). A tracked branch carries its record, a plain one is Plain.
// Last come the records in progress whose branch is nowhere in this clone
// (NoBranch), newest first: the home screen counts them, and they are
// abandoned from here. A merged or closed one gives no row. Nothing is
// fetched.
func TableRows(ctx context.Context, c *git.Client, remote string) ([]Row, error) {
	states, warnings, err := List(ctx, c)
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		warnf(c, "%s", w)
	}
	byName := map[string]Row{}
	for _, r := range Rows(states, StatusAll) {
		if _, dup := byName[r.BranchName]; !dup {
			byName[r.BranchName] = r
		}
	}

	locals, err := c.LocalBranchNames()
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}
	slices.Sort(locals)
	var remotes []string
	prefixes := []string{"refs/heads/"}
	if remote != "" {
		if remotes, err = c.RemoteBranchNames(); err != nil {
			return nil, err
		}
		slices.Sort(remotes)
		prefixes = append(prefixes, "refs/remotes/"+remote+"/")
	}
	dates, err := c.CommitterDates(ctx, prefixes...)
	if err != nil {
		return nil, err
	}
	current, _ := c.CurrentBranch()

	rows := make([]Row, 0, len(locals)+len(remotes))
	for _, name := range locals {
		r, ok := byName[name]
		if !ok {
			r = Row{BranchName: name, Plain: true}
		}
		r.Current = name == current && current != "HEAD"
		r.LastCommit = dates["refs/heads/"+name]
		rows = append(rows, r)
	}
	for _, name := range remotes {
		if _, local := slices.BinarySearch(locals, name); local {
			continue
		}
		r, ok := byName[name]
		if !ok {
			r = Row{BranchName: name, Plain: true}
		}
		r.RemoteOnly = true
		r.LastCommit = dates["refs/remotes/"+remote+"/"+name]
		rows = append(rows, r)
	}
	inProgress := Rows(states, StatusInProgress)
	for i := range inProgress {
		r := &inProgress[i]
		_, local := slices.BinarySearch(locals, r.BranchName)
		_, onRemote := slices.BinarySearch(remotes, r.BranchName)
		if !local && !onRemote {
			r.NoBranch = true
			rows = append(rows, *r)
		}
	}

	return rows, nil
}
