package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	issuepkg "github.com/piprim/git-zf/issue"
	"github.com/spf13/cobra"
)

func (i Issue) getNewCmd() *cobra.Command {
	var in issuepkg.NewIssue

	cmd := &cobra.Command{
		Use:   "new",
		Short: "Create an issue in the repository (no branch)",
		Long: `Create an issue stored in the repository under refs/zf/issues/ and push it.
No branch is created: run "git zf issue start" to begin work on it.
Passing any flag skips the form.`,
		Args: cobra.NoArgs,
	}

	f := cmd.Flags()
	f.StringVar(&in.Title, "title", "", "issue title")
	f.StringVar(&in.BranchType, "type", "", "branch type, one of the configured commit types (default: the first)")
	f.StringVar(&in.Description, "description", "", "issue description")
	f.StringArrayVar(&in.Labels, "label", nil, "label to add (repeatable)")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		// Only this command's flags count: NFlag also counts the inherited
		// --debug.
		passed := slices.ContainsFunc([]string{"title", "type", "description", "label"}, cmd.Flags().Changed)

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

	return runNew(cmd.Context(), client, i.appConfig, in, p, openMirror(i.appConfig, client.IO().Err))
}

// runNew creates the issue described by in. A non-nil p fills in from the
// form first.
func runNew(ctx context.Context, client *git.Client, cfg *config.AppConfig, in issuepkg.NewIssue, p recordPrompter, m *issuepkg.Mirror) error {
	types := commitTypeNames(cfg)
	if len(types) == 0 {
		return errors.New("config: no commit types found")
	}

	if p != nil {
		if err := p.NewIssue(ctx, types, &in); err != nil {
			return fmt.Errorf("issue form: %w", err)
		}
	}

	in.Title = strings.TrimSpace(in.Title)
	if in.Title == "" {
		return errors.New("issue title is required (--title)")
	}

	if in.BranchType == "" {
		in.BranchType = types[0]
	}
	if !slices.Contains(types, in.BranchType) {
		return fmt.Errorf("unknown type %q (want one of: %s)", in.BranchType, strings.Join(types, ", "))
	}

	in.Labels = cleanLabels(in.Labels)

	rec, err := issuepkg.Create(ctx, client, in)
	if err != nil {
		return fmt.Errorf("create issue: %w", err)
	}

	pushIssue(ctx, client, rec.ID)
	// The reconcile reads every record: fetched first, or a record another
	// clone just linked looks unlinked here and is exported a second time.
	if m != nil {
		fetchIssues(ctx, client)
	}
	reconcileIssues(ctx, client, m)
	// The reconcile may have linked the record: name it by its number.
	if m != nil {
		if linked, err := issuepkg.Load(ctx, client, rec.ID); err == nil {
			rec = linked
		}
	}
	fmt.Fprintf(client.IO().Out, "Created issue %s: %s\n", rec.DisplayID(), rec.Title)

	return nil
}
