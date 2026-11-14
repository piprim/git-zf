package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/cmd/issueflow"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getNewCmd() *cobra.Command {
	var in issuepkg.NewIssue

	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create an issue in the repository",
		Long: `Create an issue stored in the repository under refs/zf/issues/ and push it,
then propose to start work on it right away. Passing any flag skips the form
and creates no branch.`,
		Args: cobra.NoArgs,
	}

	f := cmd.Flags()
	f.StringVar(&in.Title, "title", "", "issue title")
	f.StringVar(&in.BranchType, "type", "", "branch type, one of the configured commit types (default: the first)")
	f.StringVar(&in.Description, "description", "", "issue description")
	f.StringArrayVar(&in.Labels, "label", nil, "label to add (repeatable)")
	f.BoolVar(&in.Assign, "assign", false, "assign the issue to you")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		// Only this command's flags count: NFlag also counts the inherited
		// --debug.
		passed := slices.ContainsFunc([]string{"title", "type", "description", "label", "assign"}, cmd.Flags().Changed)

		return i.newRunE(cmd, in, !passed)
	}

	return cmd
}

// newRunE runs `issue new`. interactive opens the form; it is false when any
// flag was passed.
func (i Issue) newRunE(cmd *cobra.Command, in issuepkg.NewIssue, interactive bool) error {
	client, err := cmdutil.NewClientForCmd(cmd, i.appConfig)
	if err != nil {
		return fmt.Errorf("open repository: %w", err)
	}

	var p recordPrompter
	if interactive {
		p = huhRecordPrompter{}
	}

	_, err = runNewWithStart(
		cmd.Context(),
		issueflow.StartDeps{Client: client, Cfg: i.appConfig, Mirror: openMirror(i.appConfig, client.IO().Err)},
		in, p, issueflow.NewHuhStartPrompter(),
	)

	return err
}

// runNew creates the issue described by in without proposing to start it. A
// non-nil p fills in from the form first.
func runNew(ctx context.Context, client *git.Client, cfg *config.AppConfig, in issuepkg.NewIssue, p recordPrompter, m *issuepkg.Mirror) error {
	_, err := runNewWithStart(ctx, issueflow.StartDeps{Client: client, Cfg: cfg, Mirror: m}, in, p, nil)

	return err
}

// runNewWithStart creates the issue described by in and, when both p and sp
// are non-nil, proposes to start a branch for it right away. It reports
// whether the start flow ran to its end; a declined branch confirm counts,
// as for s.
func runNewWithStart(
	ctx context.Context, deps issueflow.StartDeps, in issuepkg.NewIssue, p recordPrompter, sp issueflow.StartPrompter,
) (bool, error) {
	rec, err := createIssue(ctx, deps.Client, deps.Cfg, in, p, deps.Mirror)
	if err != nil {
		return false, err
	}
	if p == nil || sp == nil {
		return false, nil
	}

	start, err := p.ConfirmStart(ctx, rec.DisplayID())
	if errors.Is(err, huh.ErrUserAborted) || (err == nil && !start) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("confirm start: %w", err)
	}

	err = issueflow.StartRow(ctx, deps, sp, &issuepkg.Row{IssueSlug: rec.ID, State: rec.State})
	if errors.Is(err, huh.ErrUserAborted) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("start issue: %w", err)
	}

	return true, nil
}

// createIssue creates the issue described by in, pushes it, reconciles the
// mirror and prints the Created line. A non-nil p fills in from the form
// first.
func createIssue(
	ctx context.Context, client *git.Client, cfg *config.AppConfig, in issuepkg.NewIssue, p recordPrompter, m *issuepkg.Mirror,
) (issuepkg.Record, error) {
	types := commitTypeNames(cfg)
	if len(types) == 0 {
		return issuepkg.Record{}, errors.New("config: no commit types found")
	}

	if p != nil {
		// The tracker is asked for its labels for the form alone: a flag-driven
		// run never reaches it.
		choices, single := labelChoices(ctx, client, m, nil)
		// The form proposes to assign the issue to its creator.
		in.Assign = true
		if err := p.NewIssue(ctx, types, choices, single, &in); err != nil {
			return issuepkg.Record{}, fmt.Errorf("issue form: %w", err)
		}
	}

	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return issuepkg.Record{}, errors.New("issue title is required (--title)")
	}

	if in.BranchType == "" {
		in.BranchType = types[0]
	}
	if !slices.Contains(types, in.BranchType) {
		return issuepkg.Record{}, fmt.Errorf("unknown type %q (want one of: %s)", in.BranchType, strings.Join(types, ", "))
	}

	in.Labels = issuepkg.CleanLabels(in.Labels)

	rec, err := issuepkg.Create(ctx, client, in)
	if err != nil {
		return issuepkg.Record{}, fmt.Errorf("create issue: %w", err)
	}

	pushIssue(ctx, client, rec.ID)
	// The reconcile reads every record: fetched first, or a record another
	// clone just linked looks unlinked here and is exported a second time.
	if m != nil {
		issueflow.FetchIssues(ctx, client)
	}
	issueflow.ReconcileIssues(ctx, client, m)
	// The reconcile may have linked the record: name it by its number.
	if m != nil {
		if linked, err := issuepkg.Load(ctx, client, rec.ID); err == nil {
			rec = linked
		}
	}
	fmt.Fprintf(client.IO().Out, "Created issue %s: %s\n", rec.DisplayID(), rec.Title)

	return rec, nil
}
