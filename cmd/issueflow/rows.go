package issueflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
)

// MirrorOf returns the issue mirror over an already built tracker, or nil
// when the mirror is off or there is no tracker.
func MirrorOf(cfg *config.AppConfig, t tracker.Tracker) *issue.Mirror {
	tc := cfg.IssueTracker
	if !tc.Mirror || t == nil || len(tc.Projects) != 1 {
		return nil
	}

	return &issue.Mirror{Tracker: t, Type: tc.Type, Project: tc.Projects[0].Name}
}

// PrintWarnings prints one warning per line.
func PrintWarnings(w io.Writer, warnings []string) {
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
}

// FetchIssues refreshes the local issue refs from the remote. A failure
// (offline, auth) is a warning: every command then works on local data. A
// remote ref that is not an issue chain is left out without a word here:
// `issue sync` names it and the way out, once, when asked.
func FetchIssues(ctx context.Context, client *git.Client) {
	if _, err := issue.Fetch(ctx, client); err != nil && !errors.Is(err, git.ErrForeignChain) {
		fmt.Fprintf(client.IO().Err, "warning: could not fetch issues, using local data: %v\n", err)
	}
}

// ReconcileIssues runs the mirror's reconcile, warnings to stderr. A nil m
// is a no-op with an empty result.
func ReconcileIssues(ctx context.Context, client *git.Client, m *issue.Mirror) issue.MirrorResult {
	res, err := m.Reconcile(ctx, client)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: tracker mirror: %v\n", err)
	}
	PrintWarnings(client.IO().Err, res.Warnings)

	return res
}

// ListRows lists the tracked branches, the tracker's open issues and the
// issues stored in the repository, merged into one row per issue; status
// ("open", "closed", "all", "" for every row) filters them as the list's
// tab does. With deps.Flags.Tracker false the tracker is not asked, nor
// the mirror reconciled: the rows are the branches and the repo issues.
// The tracker is asked once: with the mirror on, the reconcile's listing
// is the one shown.
func ListRows(ctx context.Context, deps StartDeps, status string) ([]issue.Row, error) {
	rows, err := buildFromBranches(ctx, deps.Client)
	if err != nil {
		return nil, err
	}

	FetchIssues(ctx, deps.Client)

	var issues []tracker.Issue
	if deps.Flags.Tracker {
		issues = ReconcileIssues(ctx, deps.Client, deps.Mirror).Listed
		if deps.Mirror == nil && deps.Tracker != nil {
			if issues, err = deps.Tracker.ListIssues(ctx); err != nil {
				fmt.Fprintf(deps.errW(), "warning: tracker unavailable, listing the repository alone: %v\n", err)
			}
		}
	}

	rows, err = mergeRepoIssues(ctx, deps, mergeTrackerIssues(issues, rows))
	if err != nil || status == "" {
		return rows, err
	}

	return slices.DeleteFunc(rows, func(r issue.Row) bool { return !r.MatchesStatus(status) }), nil
}

// mergeTrackerIssues adds the tracker's open issues to the branch rows: a row
// whose branch names a tracker issue gets its title, project, status and
// creation date, and every other issue is appended.
func mergeTrackerIssues(issues []tracker.Issue, rows []issue.Row) []issue.Row {
	byID := make(map[string]*tracker.Issue, len(issues))
	for i := range issues {
		byID[issues[i].ID] = &issues[i]
	}

	seen := make(map[string]bool, len(rows))
	for i := range rows {
		iss, ok := byID[rows[i].IssueSlug]
		if !ok {
			continue
		}
		seen[iss.ID] = true
		status := iss.Status
		rows[i].Title, rows[i].Project, rows[i].TrackerStatus, rows[i].CreatedAt = iss.Subject, iss.Project, &status, iss.CreatedAt
		rows[i].AssignedToMe = iss.AssignedToMe
	}

	for i := range issues {
		iss := &issues[i]
		if seen[iss.ID] {
			continue
		}
		status := iss.Status
		rows = append(rows, issue.Row{
			IssueSlug: iss.ID, Title: iss.Subject, Project: iss.Project, TrackerStatus: &status, CreatedAt: iss.CreatedAt,
			AssignedToMe: iss.AssignedToMe,
		})
	}

	return rows
}

// mergeRepoIssues enriches the branch rows with the issues stored in the
// repository: a row whose issue has a record gets its title, labels and
// state, and every record without a branch row is appended, so the backlog
// shows up before anyone starts a branch.
func mergeRepoIssues(ctx context.Context, deps StartDeps, rows []issue.Row) ([]issue.Row, error) {
	records, warnings, err := issue.List(ctx, deps.Client)
	if err != nil {
		return nil, fmt.Errorf("list repo issues: %w", err)
	}
	PrintWarnings(deps.errW(), warnings)

	// A branch names its issue by the full ID in its start op. A branch
	// started before that op carried one, or seeded by hand, falls back to
	// its slug, which is the display ID the branch was named after.
	byID := make(map[string]*issue.Record, len(records))
	byDisplayID := make(map[string]*issue.Record, len(records))
	for i := range records {
		byID[records[i].ID] = &records[i]
		byDisplayID[records[i].DisplayID()] = &records[i]
	}

	out := rows
	started := make(map[string]bool, len(out))
	for i := range out {
		var rec *issue.Record
		ok := false
		if out[i].Branch != nil {
			rec, ok = byID[out[i].Branch.IssueID]
		}
		if !ok {
			rec, ok = byDisplayID[out[i].IssueSlug]
		}
		if !ok {
			continue
		}
		started[rec.ID] = true
		// The record's title, not the one the branch chain froze at start: it
		// follows `issue edit`.
		rr := issue.RecordRow(rec)
		out[i].Title, out[i].Labels, out[i].State = rr.Title, rr.Labels, rr.State
		out[i].TrackerStatus, out[i].TrackerID, out[i].CreatedAt = rr.TrackerStatus, rr.TrackerID, rr.CreatedAt
	}

	for i := range records {
		if started[records[i].ID] {
			continue
		}
		out = append(out, issue.RecordRow(&records[i]))
	}

	return out, nil
}

func buildFromBranches(ctx context.Context, c *git.Client) ([]issue.Row, error) {
	branches, err := branch.ListRows(ctx, c, branch.StatusAll)
	if err != nil {
		return nil, fmt.Errorf("list branches: %w", err)
	}

	rows := make([]issue.Row, len(branches))
	for i := range branches {
		b := branches[i]
		rows[i] = issue.Row{
			IssueSlug: b.IssueSlug,
			Title:     b.Title,
			Branch:    &b,
		}
	}

	return rows, nil
}
