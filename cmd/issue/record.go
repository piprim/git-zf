package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tui"
)

// recordPrompter resolves the forms of the repo-issue commands (new, show,
// comment, label). The production implementation opens huh forms; tests use
// scriptedRecordPrompter.
type recordPrompter interface {
	// NewIssue fills in from the `issue new` form.
	NewIssue(ctx context.Context, allowedTypes []string, in *issuepkg.NewIssue) error
	// PickRecord returns the full ID of the record the user picked.
	PickRecord(ctx context.Context, records []issuepkg.Record) (string, error)
	// CommentBody returns the comment text.
	CommentBody(ctx context.Context) (string, error)
	// LabelChanges returns "+add -remove" tokens on one line.
	LabelChanges(ctx context.Context) (string, error)
}

type huhRecordPrompter struct{}

var _ recordPrompter = huhRecordPrompter{}

func (huhRecordPrompter) NewIssue(ctx context.Context, allowedTypes []string, in *issuepkg.NewIssue) error {
	var labels string
	if err := huh.NewForm(
		tui.IssueNewForm(&in.Title, &in.BranchType, &in.Description, &labels, allowedTypes),
	).RunWithContext(ctx); err != nil {
		return fmt.Errorf("new issue form: %w", err)
	}

	in.Labels = strings.Split(labels, ",")

	return nil
}

func (huhRecordPrompter) PickRecord(ctx context.Context, records []issuepkg.Record) (string, error) {
	var id string
	if err := huh.NewForm(tui.IssueRecordPicker(records, &id, false)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("issue picker: %w", err)
	}

	return id, nil
}

func (huhRecordPrompter) CommentBody(ctx context.Context) (string, error) {
	var body string
	if err := huh.NewForm(tui.IssueCommentInput(&body)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("comment form: %w", err)
	}

	return body, nil
}

func (huhRecordPrompter) LabelChanges(ctx context.Context) (string, error) {
	var changes string
	if err := huh.NewForm(tui.IssueLabelInput(&changes)).RunWithContext(ctx); err != nil {
		return "", fmt.Errorf("label form: %w", err)
	}

	return changes, nil
}

// commitTypeNames returns the configured commit types, which are the allowed
// branch types of an issue.
func commitTypeNames(cfg *config.AppConfig) []string {
	names := make([]string, 0, len(cfg.CommitTypes))
	for _, t := range cfg.CommitTypes {
		names = append(names, t.Name)
	}

	return names
}

// cleanLabels trims labels and drops empty and duplicate ones.
func cleanLabels(labels []string) []string {
	seen := make(map[string]bool, len(labels))
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		l = strings.TrimSpace(l)
		if l == "" || seen[l] {
			continue
		}
		seen[l] = true
		out = append(out, l)
	}

	return out
}

func printWarnings(w io.Writer, warnings []string) {
	for _, line := range warnings {
		fmt.Fprintln(w, line)
	}
}

// fetchIssues refreshes the local issue refs from the remote. A failure
// (offline, auth) is a warning: every command then works on local data.
func fetchIssues(ctx context.Context, client *git.Client) {
	if _, err := issuepkg.Fetch(ctx, client); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: could not fetch issues, using local data: %v\n", err)
	}
}

// pushIssue pushes issue id. A failure is a warning: the op is committed
// locally and goes out with the next push or `git zf issue sync`.
func pushIssue(ctx context.Context, client *git.Client, id string) {
	if err := issuepkg.Push(ctx, client, id); err != nil {
		fmt.Fprintf(client.IO().Err,
			"warning: issue saved locally but not pushed (run `git zf issue sync` later): %v\n", err)
	}
}

// resolveRecord returns the record named by args[0], or opens the picker over
// all local issues when no ID was given.
func resolveRecord(
	ctx context.Context, client *git.Client, p recordPrompter, args []string,
) (issuepkg.Record, error) {
	if len(args) > 0 {
		rec, err := issuepkg.Resolve(ctx, client, args[0])
		if err != nil {
			return issuepkg.Record{}, fmt.Errorf("resolve issue: %w", err)
		}
		printWarnings(client.IO().Err, rec.Warnings)

		return rec, nil
	}

	records, warnings, err := issuepkg.List(ctx, client)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("list issues: %w", err)
	}
	printWarnings(client.IO().Err, warnings)

	if len(records) == 0 {
		return issuepkg.Record{}, errors.New("no issues in this repository; create one with `git zf issue new`")
	}

	id, err := p.PickRecord(ctx, records)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("pick issue: %w", err)
	}

	rec, err := issuepkg.Load(ctx, client, id)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("load issue: %w", err)
	}

	return rec, nil
}
