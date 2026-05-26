package issue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"
	"github.com/mitchellh/go-homedir"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/internal/pkg"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/store"
	"github.com/piprim/git-zf/tracker"
	"github.com/spf13/cobra"
)

// StartDeps bundles the long-lived dependencies the start flow needs.
// Production code builds it via BuildStartDeps; tests inject directly.
// Exported because cmd/branch/branch.go's newRunE constructs one too.
type StartDeps struct {
	Client  *git.Client
	Cfg     *config.AppConfig
	Tracker tracker.Tracker // nil when cfg.IssueTracker.Type == "" OR factory failed (warn)
	Flags   issue.IssueStartFlags
}

// BuildStartDeps constructs the production StartDeps from a cobra command.
// Returns an error if the repo cannot be opened. When cfg.IssueTracker.Type
// == "" the returned deps.Tracker is nil (RunIssueStart treats that as "no
// tracker available"). When the tracker factory fails, the error is
// non-fatal — BuildStartDeps warns to client.IO().Err and returns deps with
// a nil tracker.
func BuildStartDeps(
	_ context.Context,
	cmd *cobra.Command,
	cfg *config.AppConfig,
	flags issue.IssueStartFlags,
) (StartDeps, error) {
	client, err := git.NewClient(&pkg.IO{
		In:  cmd.InOrStdin(),
		Out: cmd.OutOrStdout(),
		Err: cmd.ErrOrStderr(),
	})
	if err != nil {
		return StartDeps{}, fmt.Errorf("not a git repository: %w", err)
	}

	if cfg.Branch.Remote != "" {
		client.SetRemote(cfg.Branch.Remote)
	}

	deps := StartDeps{Client: client, Cfg: cfg, Flags: flags}

	if cfg.IssueTracker.Type != "" {
		t, err := tracker.New(cfg.IssueTracker)
		if err != nil {
			fmt.Fprintf(client.IO().Err, "warning: init tracker: %v\n", err)
		} else {
			deps.Tracker = t
		}
	}

	return deps, nil
}

func (i Issue) getStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
		RunE: i.startRunE,
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")

	return cmd
}

func (i Issue) startRunE(cmd *cobra.Command, _ []string) error {
	variant, err := cmd.Flags().GetString("variant")
	if err != nil {
		return fmt.Errorf("read --variant flag: %w", err)
	}

	flags := issue.IssueStartFlags{TrackerFirst: true, Variant: variant}

	deps, err := BuildStartDeps(cmd.Context(), cmd, i.appConfig, flags)
	if err != nil {
		return err
	}

	return RunIssueStart(cmd.Context(), deps, NewHuhStartPrompter())
}

// RunIssueStart is the prompter-driven core of the issue-start flow. Called
// from startRunE (production, via a huhStartPrompter) and from cmd/branch's
// newRunE (also production). Tests call it directly with a
// scriptedStartPrompter. TrackerFirst is carried inside deps.Flags.
//
// Returns nil on the empty-list short-circuit from pickIssue (operator
// declined the tracker toggle and no manual fallback was offered) and nil
// on the (nil, nil) abort path from prompter.ResolveBranchConflict.
func RunIssueStart(ctx context.Context, deps StartDeps, prompter StartPrompter) error {
	allowedBranchTypes := make([]string, 0, len(deps.Cfg.CommitTypes))
	for _, t := range deps.Cfg.CommitTypes {
		allowedBranchTypes = append(allowedBranchTypes, t.Name)
	}

	if len(allowedBranchTypes) == 0 {
		return errors.New("config: no commit types found")
	}

	pickedIssue, err := pickIssue(ctx, deps, prompter, allowedBranchTypes)
	if err != nil {
		return err
	}

	if pickedIssue == nil {
		return nil
	}

	useWorktree, err := resolveUseWorktree(ctx, deps, prompter)
	if err != nil {
		return err
	}

	if useWorktree {
		return createWorktreeFlow(ctx, deps, prompter, pickedIssue)
	}

	return createBranchFlow(ctx, deps, prompter, pickedIssue)
}

// pickIssue chooses between tracker-driven and user-driven issue input.
// Returns (nil, nil) when the operator declined the tracker toggle AND
// no manual fallback was offered (only reachable via tracker path with
// useTracker=false — but in that branch we explicitly call GetFromUser,
// so (nil, nil) is currently unreachable in practice; the caller still
// guards against it).
func pickIssue(
	ctx context.Context,
	deps StartDeps,
	prompter StartPrompter,
	allowedBranchTypes []string,
) (*issue.Issue, error) {
	if deps.Tracker == nil {
		got, err := issue.GetFromUser(ctx, prompter, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("issue from user: %w", err)
		}

		return got, nil
	}

	useTracker, err := prompter.PickUseTracker(ctx, deps.Cfg.IssueTracker.Type, deps.Flags.TrackerFirst)
	if err != nil {
		return nil, fmt.Errorf("pick use tracker: %w", err)
	}

	if !useTracker {
		got, err := issue.GetFromUser(ctx, prompter, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("issue from user: %w", err)
		}

		return got, nil
	}

	got, err := issue.GetFromTracker(ctx, prompter, deps.Tracker, allowedBranchTypes)
	if err != nil {
		return nil, fmt.Errorf("issue from tracker: %w", err)
	}

	return got, nil
}

// resolveUseWorktree consults the config override; falls back to the prompter
// only when the override is absent (nil).
func resolveUseWorktree(ctx context.Context, deps StartDeps, prompter StartPrompter) (bool, error) {
	if deps.Cfg.Branch.UseWorktree != nil {
		return *deps.Cfg.Branch.UseWorktree, nil
	}

	use, err := prompter.PickUseWorktree(ctx)
	if err != nil {
		return false, fmt.Errorf("pick use worktree: %w", err)
	}

	return use, nil
}

func createBranchFlow(ctx context.Context, deps StartDeps, prompter StartPrompter, picked *issue.Issue) error {
	b, base, err := prepareBranch(deps, picked)
	if err != nil {
		return err
	}

	b, err = prompter.ResolveBranchConflict(ctx, deps.Client, b, picked)
	if err != nil {
		return fmt.Errorf("resolve branch conflict: %w", err)
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	confirmed, err := prompter.ConfirmCreateBranch(ctx,
		fmt.Sprintf("Create branch %q based on %q?", branchName, base))
	if err != nil {
		return fmt.Errorf("confirm create branch: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(deps.Client.IO().Out, "Aborted.")

		return nil
	}

	if err := deps.Client.CreateBranch(branchName, base); err != nil {
		return fmt.Errorf("create branch: %w", err)
	}

	var tt *string
	if picked.TrackerType != "" {
		tt = &deps.Cfg.IssueTracker.Type
	}

	if err := persist(ctx, b, picked.Subject, tt); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: branch created but store record failed: %v\n", err)
	}

	fmt.Fprintf(deps.Client.IO().Out, "Switched to new branch %q (based on %q)\n", branchName, base)

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}

func createWorktreeFlow(ctx context.Context, deps StartDeps, prompter StartPrompter, picked *issue.Issue) error {
	b, base, err := prepareBranch(deps, picked)
	if err != nil {
		return err
	}

	b, err = prompter.ResolveBranchConflict(ctx, deps.Client, b, picked)
	if err != nil {
		return fmt.Errorf("resolve branch conflict: %w", err)
	}

	if b == nil {
		return nil
	}

	branchName := b.Name()

	repoRoot, err := deps.Client.WorkingTreeRoot()
	if err != nil {
		return fmt.Errorf("working tree root: %w", err)
	}

	repoName, err := deps.Client.RepoName()
	if err != nil {
		return fmt.Errorf("resolve repo name: %w", err)
	}

	path := worktreePath(repoRoot, deps.Cfg.Branch.WorktreeDir, repoName, branchName)

	confirmed, err := prompter.ConfirmCreateWorktree(ctx,
		fmt.Sprintf("Create worktree %q at %q based on %q?", branchName, path, base))
	if err != nil {
		return fmt.Errorf("confirm create worktree: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(deps.Client.IO().Out, "Aborted.")

		return nil
	}

	if err := deps.Client.CreateWorktree(ctx, branchName, base, path); err != nil {
		return fmt.Errorf("create worktree: %w", err)
	}

	var tt *string
	if picked.TrackerType != "" {
		tt = &deps.Cfg.IssueTracker.Type
	}

	if err := persist(ctx, b, picked.Subject, tt); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: worktree created but store record failed: %v\n", err)
	}

	fmt.Fprintf(deps.Client.IO().Out, "Created worktree %q at %q (based on %q)\n", branchName, path, base)

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	fmt.Fprintln(deps.Client.IO().Out, hintStyle.Render("Run 'cd "+path+"' to begin working."))

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}

// prepareBranch assembles the branch and resolves the base branch. Shared by
// createBranchFlow and createWorktreeFlow.
func prepareBranch(deps StartDeps, picked *issue.Issue) (b *branch.Branch, base string, err error) {
	b, err = branch.New(picked.ID, picked.Type, picked.Subject, deps.Flags.Variant)
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}

	base = deps.Cfg.Branch.Base
	if base == "" {
		base, err = deps.Client.DefaultBaseBranch()
		if err != nil {
			return nil, "", fmt.Errorf("detect base branch: %w", err)
		}
	}

	return b, base, nil
}

// updateTrackerStatus runs the tracker status-picker form and applies the
// chosen status. All errors are non-fatal warnings (the branch was already
// created — the operator must be able to clean up tracker drift manually).
func updateTrackerStatus(ctx context.Context, deps StartDeps, prompter StartPrompter, issueID string) {
	if deps.Tracker == nil {
		return
	}

	statuses, err := deps.Tracker.ListStatuses(ctx)
	if err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: could not fetch tracker statuses: %v\n", err)

		return
	}

	selected, err := prompter.PickTrackerStatus(ctx, issueID, deps.Cfg.IssueTracker.Type, statuses)
	if err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: status picker: %v\n", err)

		return
	}

	if selected == "" {
		return
	}

	if err := deps.Tracker.UpdateIssueStatus(ctx, issueID, selected); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: could not update tracker status: %v\n", err)
	}
}

func persist(ctx context.Context, b *branch.Branch, rawTitle string, trackerType *string) error {
	s, err := store.OpenRepo(ctx)
	if err != nil {
		return fmt.Errorf("failed to get store: %w", err)
	}
	defer func() { _ = s.Close() }()

	if err := s.InsertIssueWithBranch(ctx,
		&store.Issue{IDSlug: b.IssueID(), Title: rawTitle, StatusID: store.StatusIDInProgress, TrackerType: trackerType},
		&store.Branch{Name: b.Name(), Type: b.Type(), StatusID: store.StatusIDInProgress},
	); err != nil {
		return fmt.Errorf("insert issue with branch: %w", err)
	}

	return nil
}

// worktreePath computes the absolute path for a new worktree.
// baseDir overrides the default (sibling of repoRoot) when non-empty; ~ is expanded.
func worktreePath(repoRoot, baseDir, repoName, branchName string) string {
	base := baseDir
	if base == "" {
		base = filepath.Dir(repoRoot)
	} else if expanded, err := homedir.Expand(base); err == nil {
		base = expanded
	}

	return filepath.Join(base, repoName+"--"+branchName)
}
