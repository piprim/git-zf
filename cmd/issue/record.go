package issue

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
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
	// EditIssue edits title and description in place; both come prefilled.
	EditIssue(ctx context.Context, title, description *string) error
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

func (huhRecordPrompter) EditIssue(ctx context.Context, title, description *string) error {
	if err := huh.NewForm(tui.IssueEditForm(title, description)).RunWithContext(ctx); err != nil {
		return fmt.Errorf("edit issue form: %w", err)
	}

	return nil
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
	_, err := issuepkg.Fetch(ctx, client)
	switch {
	case errors.Is(err, git.ErrForeignChain):
		fmt.Fprintf(client.IO().Err,
			"warning: %v\n(`git zf issue sync` on a clone that has the issue repairs the remote, "+
				"unless an older git-zf merged the foreign chain in: that takes a fix by hand)\n", err)
	case err != nil:
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

// mirrorOf returns the issue mirror over an already built tracker, or nil
// when the mirror is off or there is no tracker.
func mirrorOf(cfg *config.AppConfig, t tracker.Tracker) *issuepkg.Mirror {
	tc := cfg.IssueTracker
	if !tc.Mirror || t == nil || len(tc.Projects) != 1 {
		return nil
	}

	return &issuepkg.Mirror{Tracker: t, Type: tc.Type, Project: tc.Projects[0].NearSlug}
}

// openMirror builds the tracker and returns the issue mirror, or nil when the
// mirror is off. A tracker that cannot be built is a warning: the command
// then works on local data.
func openMirror(cfg *config.AppConfig, errW io.Writer) *issuepkg.Mirror {
	if !cfg.IssueTracker.Mirror {
		return nil
	}

	t, err := tracker.New(cfg.IssueTracker)
	if err != nil {
		fmt.Fprintf(errW, "warning: issue mirror off, could not initialize tracker: %v\n", err)

		return nil
	}

	return mirrorOf(cfg, t)
}

// reconcileIssues mirrors the issues with the tracker. A failure is a
// warning: the next run reconciles from the chains. A nil m is a no-op.
func reconcileIssues(ctx context.Context, client *git.Client, m *issuepkg.Mirror) issuepkg.MirrorResult {
	res, err := m.Reconcile(ctx, client)
	if err != nil {
		fmt.Fprintf(client.IO().Err, "warning: tracker mirror: %v\n", err)
	}
	printWarnings(client.IO().Err, res.Warnings)

	return res
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

// closeRepoIssue closes the repo issue a merged branch worked on: it writes
// set_state closed on the issue named by ref.IssueID and pushes it. A nil
// state or one without an issue ID (tracker issue, hand-typed ID) is a no-op.
// Like the rest of updateClosedStatus, a failure is a warning: the merge
// already landed. The caller fetched the issue refs. With a mirror, the
// tracker issue is then closed by the reconcile.
func closeRepoIssue(ctx context.Context, client *git.Client, ref *branch.State, m *issuepkg.Mirror) {
	if ref == nil || ref.IssueID == "" {
		return
	}

	id := ref.IssueID
	op := &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}
	if err := issuepkg.Append(ctx, client, id, op); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: close repo issue: %v\n", err)

		return
	}

	pushIssue(ctx, client, id)
	reconcileIssues(ctx, client, m)
}

// runCloseByID closes the repo issue named by query (full ID or unique prefix)
// without merging anything: the way out for a duplicate or a wontfix. It
// refuses while a branch of the issue is in progress, so that the work is
// merged (`issue close`) or abandoned (`branch close`) knowingly.
func runCloseByID(ctx context.Context, client *git.Client, query string, m *issuepkg.Mirror) error {
	fetchIssues(ctx, client)

	rec, err := resolveRecord(ctx, client, nil, []string{query})
	if err != nil {
		return err
	}

	if rec.State == issuepkg.StateClosed {
		fmt.Fprintf(client.IO().Out, "Issue %s is already closed.\n", rec.DisplayID())

		return nil
	}

	// Another clone may have started a branch for the issue.
	if err := branch.Fetch(ctx, client); err != nil {
		fmt.Fprintf(client.IO().Err, "warning: fetch branch refs: %v\n", err)
	}

	// The branch chain is keyed by the display ID, like the join of `issue
	// list`. A chain that cannot be read stops the close: its branches may be
	// in progress.
	st, err := branch.Load(ctx, client, rec.DisplayID())
	if err != nil {
		return fmt.Errorf("read the branches of issue %s: %w", rec.DisplayID(), err)
	}

	var inProgress []string
	if st != nil {
		for i := range st.Entries {
			if st.Entries[i].Status == branch.StatusInProgress {
				inProgress = append(inProgress, st.Entries[i].Name)
			}
		}
	}
	if len(inProgress) > 0 {
		return fmt.Errorf(
			"issue %s has a branch in progress (%s): merge it with `git zf issue close`, "+
				"or abandon it with `git zf branch close <branch-name>`, then close the issue",
			rec.DisplayID(), strings.Join(inProgress, ", "))
	}

	op := &issuepkg.Op{Type: issuepkg.OpSetState, Value: issuepkg.StateClosed}
	if err := issuepkg.Append(ctx, client, rec.ID, op); err != nil {
		return fmt.Errorf("close issue: %w", err)
	}

	pushIssue(ctx, client, rec.ID)
	reconcileIssues(ctx, client, m)
	fmt.Fprintf(client.IO().Out, "Closed issue %s: %s\n", rec.DisplayID(), rec.Title)

	return nil
}
