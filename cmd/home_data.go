package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"

	branchpkg "github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	reviewpkg "github.com/piprim/git-zf/review"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// homeKeys maps a rootMenu command to its key on the home screen. s, r and q
// are the screen's own.
var homeKeys = map[string]string{"commit": "c", "issue": "i", "branch": "b", "review": "v", "log": "l"}

// shortHash is the length a detached HEAD is shown at.
const shortHash = 7

// gatherHome reads what the home screen shows: one git status and one listing
// of each chain family, all local. It never fails: a read that fails is a
// warning and what depends on it is left out. hints holds, by command name,
// what waits behind each entry; a command with nothing waiting has no key.
func gatherHome(ctx context.Context, c *git.Client, prog string) (data tui.HomeData, hints map[string]string) {
	data, hints = tui.HomeData{Prog: prog}, make(map[string]string)
	var warnings []string
	failed := func(what string, err error) bool {
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("warning: %s: %v", what, err))
		}

		return err != nil
	}

	stop := c.IO().Spin("Reading the repository")
	// The warnings are written once the spinner is gone, on the Err read at
	// that moment: a writer read while it spins is stale after it.
	defer func() {
		stop()
		issueflow.PrintWarnings(c.IO().Err, warnings)
	}()

	bs, entries, err := c.Status(ctx)
	if !failed("could not read the working tree", err) {
		data.Entries = entries
		data.Branch, data.Upstream = bs.Head, bs.Upstream
		data.HasAB, data.Ahead, data.Behind = bs.HasAB, bs.Ahead, bs.Behind
		if bs.Head == "" && len(bs.OID) >= shortHash {
			data.Branch = "detached at " + bs.OID[:shortHash]
		}
	}
	counts := tui.CountStatus(entries)
	switch {
	case counts.Staged > 0:
		hints["commit"] = plural(counts.Staged, "staged file")
	case counts.Modified > 0:
		hints["commit"] = plural(counts.Modified, "modified file")
	}

	records, w, err := issuepkg.List(ctx, c)
	warnings = append(warnings, w...)
	failed("could not list the issues", err)
	states, w, err := branchpkg.List(ctx, c)
	warnings = append(warnings, w...)
	failed("could not list the branches", err)
	reviews, w, err := reviewpkg.List(ctx, c)
	warnings = append(warnings, w...)
	failed("could not list the reviews", err)

	open := 0
	for i := range records {
		if records[i].State == issuepkg.StateOpen {
			open++
		}
	}
	inReview := 0
	for i := range reviews {
		if !reviews[i].Closed && reviews[i].Status == reviewpkg.StatusInReview {
			inReview++
		}
	}
	count := func(name string, n int, what string) {
		if n > 0 {
			hints[name] = fmt.Sprintf("%d %s", n, what)
		}
	}
	count("issue", open, "open")
	count("branch", len(branchpkg.Rows(states, branchpkg.StatusInProgress)), "in progress")
	count("review", inReview, "in review")

	slug, state := branchIssue(bs.Head, states)
	data.Issue = issueLine(slug, state, records)
	for i := range reviews {
		if r := &reviews[i]; slug != "" && r.Slug == slug && !r.Closed {
			data.Review = strings.ReplaceAll(r.Status, "_", " ")
		}
	}

	return data, hints
}

// branchIssue returns the issue slug of the branch called head and the branch
// chain of that slug (nil when there is none): the slug before @review on a
// reviewer's branch, the chain that tracks head otherwise.
func branchIssue(head string, states []branchpkg.State) (slug string, state *branchpkg.State) {
	if head == "" {
		return "", nil
	}
	reviewSlug, isReview := branchpkg.CutReviewSuffix(head)
	for i := range states {
		st := &states[i]
		if (isReview && st.Slug == reviewSlug) || (!isReview && st.Entry(head) != nil) {
			return st.Slug, st
		}
	}

	if !isReview {
		return "", nil // an untracked branch: its name is no slug
	}

	return reviewSlug, nil
}

// issueLine is the header's "<id> <title>" of the issue behind slug: the
// record's, which follows `issue edit`, else what the branch chain froze at
// start, else "".
func issueLine(slug string, state *branchpkg.State, records []issuepkg.Record) string {
	if slug == "" {
		return ""
	}
	var byDisplay *issuepkg.Record
	for i := range records {
		rec := &records[i]
		if state != nil && state.IssueID != "" && rec.ID == state.IssueID {
			return rec.DisplayID() + " " + rec.Title
		}
		if rec.DisplayID() == slug {
			byDisplay = rec
		}
	}
	if byDisplay != nil {
		return byDisplay.DisplayID() + " " + byDisplay.Title
	}
	if state != nil {
		return strings.TrimSpace(state.Slug + " " + state.Title)
	}

	return ""
}

// plural is "1 word" or "n words".
func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	return fmt.Sprintf("%d %ss", n, word)
}

// homeEntries builds the home screen's entries from the menu's subcommands.
func homeEntries(subs []*cobra.Command, hints map[string]string) []tui.HomeEntry {
	entries := make([]tui.HomeEntry, len(subs))
	for i, s := range subs {
		entries[i] = tui.HomeEntry{
			Key: homeKeys[s.Name()], Name: s.Name(), Label: tui.TitleCase(s.Name()), Hint: hints[s.Name()],
		}
	}

	return entries
}

// refreshHome fetches the remote quietly: the branches, then the issue,
// branch and review chains. The first failure is one warning line; a client
// that saw the remote down fails the fetches after it at once. A remote ref
// that is not a chain is `issue sync`'s to name. No-op without a remote.
func refreshHome(ctx context.Context, c *git.Client) {
	warn := func(err error) {
		fmt.Fprintf(c.IO().Err, "warning: could not fetch, showing local data: %s\n", firstLine(err))
	}
	remote, err := c.Remote()
	if err != nil {
		// Several remotes and none chosen: say why nothing was fetched.
		warn(err)

		return
	}
	if remote == "" {
		return
	}

	stop := c.IO().Spin("Fetching " + remote)
	// In this order: the first failure is the one worth showing, the ones
	// after it only say the remote was not tried again.
	err = c.FetchQuiet(ctx)
	_, issueErr := issuepkg.FetchQuiet(ctx, c)
	if errors.Is(issueErr, git.ErrForeignChain) {
		issueErr = nil
	}
	err = cmpErr(err, issueErr, branchpkg.FetchQuiet(ctx, c), reviewpkg.Fetch(ctx, c, true))
	stop()
	if err != nil {
		warn(err)
	}
}

// cmpErr returns the first error that is not nil.
func cmpErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}

	return nil
}

// firstLine is the first line of err's text: git's error can span several.
func firstLine(err error) string {
	line, _, _ := strings.Cut(err.Error(), "\n")

	return line
}
