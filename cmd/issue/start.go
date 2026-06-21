package issue

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

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
	Client             *git.Client
	Cfg                *config.AppConfig
	Tracker            tracker.Tracker // nil when cfg.IssueTracker.Type == ""
	Flags              issue.IssueStartFlags
	BaseBranchOverride string // set by PickBaseBranch; skips store re-query in prepareBranch
}

// BuildStartDeps constructs the production StartDeps from a cobra command.
// Returns an error if the repo cannot be opened or if a configured tracker
// (cfg.IssueTracker.Type != "") fails to initialise. When
// cfg.IssueTracker.Type == "" the returned deps.Tracker is nil and
// RunIssueStart falls through to the manual issue-input flow.
func BuildStartDeps(
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
			return StartDeps{}, fmt.Errorf("init tracker %q: %w", cfg.IssueTracker.Type, err)
		}

		deps.Tracker = t
	}

	return deps, nil
}

func (i Issue) getStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start work on an issue (create branch)",
		Long: `Enter issue details, then a properly named branch is created and
checked out from the default base branch. Branch state is saved to .git/git-zf.db.`,
	}

	cmd.Flags().String("variant", "",
		"create a parallel branch for the same issue (e.g. --variant=spike)")
	cmd.Flags().String("parent", "",
		"parent issue slug — creates this as a sub-task branching from the parent integration branch")

	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		variant, err := cmd.Flags().GetString("variant")
		if err != nil {
			return fmt.Errorf("read --variant flag: %w", err)
		}

		parent, err := cmd.Flags().GetString("parent")
		if err != nil {
			return fmt.Errorf("read --parent flag: %w", err)
		}

		return i.startRunE(cmd, variant, parent)
	}

	return cmd
}

// startRunE drives the tracker-first issue-start flow. variant carries the
// --variant flag value; the interactive dispatcher (runE) passes "" because
// the issue root command defines no such flag.
func (i Issue) startRunE(cmd *cobra.Command, variant, parentSlug string) error {
	flags := issue.IssueStartFlags{TrackerFirst: true, Variant: variant, ParentIssueSlug: parentSlug}

	deps, err := BuildStartDeps(cmd, i.appConfig, flags)
	if err != nil {
		return err
	}

	return RunIssueStart(cmd.Context(), deps, NewHuhStartPrompter())
}

// RunIssueStart is the prompter-driven core of the issue-start flow. Called
// from startRunE (production, via a HuhStartPrompter) and from cmd/branch's
// newRunE (also production). Tests call it directly with a
// scriptedStartPrompter. TrackerFirst is carried inside deps.Flags.
//
// Returns nil on two non-error exits: (1) pickIssue returns (nil, nil) — only
// possible when a test prompter does so; production prompters always return
// a non-nil issue or an error — and (2) prompter.ResolveBranchConflict
// returns (nil, nil) to signal the operator aborted or checked out an
// existing branch.
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

	// When --parent was not explicitly set, offer a base branch picker so the
	// user can create a sub-task without knowing the flag exists.
	if deps.Flags.ParentIssueSlug == "" {
		if gitBranches, brErr := deps.Client.LocalBranchNames(); brErr == nil && len(gitBranches) > 1 {
			defaultBase, dbErr := resolveDefaultBase(deps)
			if dbErr != nil {
				return fmt.Errorf("resolve default base: %w", dbErr)
			}
			baseBranch, pbErr := prompter.PickBaseBranch(ctx, defaultBase, gitBranches)
			if pbErr != nil {
				return fmt.Errorf("pick base branch: %w", pbErr)
			}
			deps.BaseBranchOverride = baseBranch
			// If the chosen branch is tracked by git-zf, record it as the parent (best-effort).
			// Open the store via the client's git dir so this works in tests (temp dirs)
			// as well as production (CWD is the repo).
			if gitDir, gdErr := deps.Client.GitDir(); gdErr == nil {
				if s, openErr := store.Open(ctx, gitDir); openErr == nil {
					defer func() { _ = s.Close() }()
					if rows, listErr := s.ListBranches(ctx, store.BranchStatusAll); listErr == nil {
						for _, r := range rows {
							if r.BranchName == baseBranch {
								deps.Flags.ParentIssueSlug = r.IssueSlug
								break
							}
						}
					}
				}
			}
		}
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
// Returns nil only if a test prompter returns nil; production prompters
// always return a non-nil issue or an error. The (nil, nil) guard in
// RunIssueStart exists for test safety.
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
	// Open a single store connection shared by prepareBranch (parent lookup)
	// and InsertIssueRelation, avoiding two separate connections.
	// Use the client's git dir so this works in tests (temp dirs) as well as
	// production (CWD is the repo).
	var parentStore *store.Store
	if deps.Flags.ParentIssueSlug != "" {
		gitDir, gdErr := deps.Client.GitDir()
		if gdErr != nil {
			return fmt.Errorf("resolve git dir for parent store: %w", gdErr)
		}
		var openErr error
		parentStore, openErr = store.Open(ctx, gitDir)
		if openErr != nil {
			return fmt.Errorf("open store for parent lookup: %w", openErr)
		}
		defer func() { _ = parentStore.Close() }()
	}

	b, base, err := prepareBranch(ctx, deps, picked, parentStore)
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

	if deps.Flags.ParentIssueSlug != "" && parentStore != nil {
		if err := parentStore.InsertIssueRelation(ctx, deps.Flags.ParentIssueSlug, b.IssueID()); err != nil {
			fmt.Fprintf(deps.Client.IO().Err, "warning: record parent relation: %v\n", err)
		}
	}

	if err := writePushBranchRef(ctx, deps, b.IssueID(), branchName); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: write branch ref: %v\n", err)
	}

	fmt.Fprintf(deps.Client.IO().Out, "Switched to new branch %q (based on %q)\n", branchName, base)

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}

func createWorktreeFlow(ctx context.Context, deps StartDeps, prompter StartPrompter, picked *issue.Issue) error {
	var parentStore *store.Store
	if deps.Flags.ParentIssueSlug != "" {
		gitDir, gdErr := deps.Client.GitDir()
		if gdErr != nil {
			return fmt.Errorf("resolve git dir for parent store: %w", gdErr)
		}
		var openErr error
		parentStore, openErr = store.Open(ctx, gitDir)
		if openErr != nil {
			return fmt.Errorf("open store for parent lookup: %w", openErr)
		}
		defer func() { _ = parentStore.Close() }()
	}

	b, base, err := prepareBranch(ctx, deps, picked, parentStore)
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

	if deps.Flags.ParentIssueSlug != "" && parentStore != nil {
		if err := parentStore.InsertIssueRelation(ctx, deps.Flags.ParentIssueSlug, b.IssueID()); err != nil {
			fmt.Fprintf(deps.Client.IO().Err, "warning: record parent relation: %v\n", err)
		}
	}

	if err := writePushBranchRef(ctx, deps, b.IssueID(), branchName); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: write branch ref: %v\n", err)
	}

	fmt.Fprintf(deps.Client.IO().Out, "Created worktree %q at %q (based on %q)\n", branchName, path, base)

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	fmt.Fprintln(deps.Client.IO().Out, hintStyle.Render("Run 'cd "+path+"' to begin working."))

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}

// resolveDefaultBase returns the configured base branch, falling back to the
// repo's default (main/master) when not set.
func resolveDefaultBase(deps StartDeps) (string, error) {
	if deps.Cfg.Branch.Base != "" {
		return deps.Cfg.Branch.Base, nil
	}
	return deps.Client.DefaultBaseBranch()
}

// prepareBranch assembles the branch and resolves the base branch. Shared by
// createBranchFlow and createWorktreeFlow.
//
// When deps.Flags.ParentIssueSlug is set, parentStore (must be non-nil) is
// queried to find the parent's in-progress branch, which becomes the base.
func prepareBranch(ctx context.Context, deps StartDeps, picked *issue.Issue, parentStore *store.Store) (b *branch.Branch, base string, err error) {
	b, err = branch.New(picked.ID, picked.Type, picked.Subject, deps.Flags.Variant)
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}

	if deps.Flags.ParentIssueSlug != "" {
		if deps.BaseBranchOverride != "" {
			// Already resolved by PickBaseBranch — use directly, no store query needed.
			return b, deps.BaseBranchOverride, nil
		}
		// --parent set via flag; look up the branch from the provided store.
		if parentStore == nil {
			return nil, "", fmt.Errorf("no store available to resolve parent issue %q", deps.Flags.ParentIssueSlug)
		}
		branches, err := parentStore.ListBranches(ctx, store.BranchStatusInProgress)
		if err != nil {
			return nil, "", fmt.Errorf("list branches for parent %q: %w", deps.Flags.ParentIssueSlug, err)
		}
		for _, br := range branches {
			if br.IssueSlug == deps.Flags.ParentIssueSlug {
				return b, br.BranchName, nil
			}
		}
		return nil, "", fmt.Errorf("no in-progress branch found for parent issue %q", deps.Flags.ParentIssueSlug)
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

// writePushBranchRef writes a BranchRef to refs/zf/branches/<issueSlug> and
// pushes it to the remote (best-effort). Called after every successful branch
// or worktree creation so the parent-child relationship is available cross-machine.
func writePushBranchRef(ctx context.Context, deps StartDeps, issueSlug, branchName string) error {
	ref := git.BranchRef{
		IssueSlug:  issueSlug,
		BranchName: branchName,
		ParentSlug: deps.Flags.ParentIssueSlug,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	if _, err := deps.Client.WriteBranchRef(ctx, issueSlug, ref); err != nil {
		return err
	}
	return deps.Client.PushBranchRef(ctx, issueSlug)
}
