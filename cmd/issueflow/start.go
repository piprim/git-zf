package issueflow

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/mitchellh/go-homedir"
	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
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
	client, err := cmdutil.NewClientForCmd(cmd, cfg)
	if err != nil {
		return StartDeps{}, err
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

// RunIssueStart is the prompter-driven core of the issue-start flow. Called
// from cmd/issue's startRunE (production, via a HuhStartPrompter) and from
// cmd/branch's newRunE (also production). Tests call it directly with a
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
	// user can create a sub-task without knowing the flag exists. Candidates are
	// the union of local branches and remote-tracking branches: on a fresh clone
	// the parent integration branch exists only as origin/<name> (never checked
	// out locally), and it must still be offerable as a base — git.CreateBranch
	// resolves a remote-only base via its remote-tracking ref.
	if deps.Flags.ParentIssueSlug == "" {
		if candidates, brErr := baseBranchCandidates(deps.Client); brErr == nil && len(candidates) > 1 {
			defaultBase, dbErr := resolveDefaultBase(deps)
			if dbErr != nil {
				return fmt.Errorf("resolve default base: %w", dbErr)
			}
			baseBranch, pbErr := prompter.PickBaseBranch(ctx, defaultBase, candidates)
			if pbErr != nil {
				return fmt.Errorf("pick base branch: %w", pbErr)
			}
			deps.BaseBranchOverride = baseBranch
			deps.Flags.ParentIssueSlug = resolveParentSlug(ctx, deps.Client, baseBranch)
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
		got, err := getFromUser(ctx, prompter, allowedBranchTypes)
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
		got, err := getFromUser(ctx, prompter, allowedBranchTypes)
		if err != nil {
			return nil, fmt.Errorf("issue from user: %w", err)
		}

		return got, nil
	}

	got, err := getFromTracker(ctx, prompter, deps.Tracker, allowedBranchTypes)
	if err != nil {
		return nil, fmt.Errorf("issue from tracker: %w", err)
	}

	return got, nil
}

// getFromUser drives the manual issue-input flow via p.PickIssueFromUser.
// Moved here from the issue domain package: it is application-layer
// orchestration over the UI prompter, not entity logic.
func getFromUser(ctx context.Context, p Prompter, allowedTypes []string) (*issue.Issue, error) {
	out, err := p.PickIssueFromUser(ctx, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("issue input: %w", err)
	}

	return out, nil
}

// getFromTracker fetches issues via t.ListIssues, then either falls back to
// the manual path (PickIssueFromUser) on error/empty-list, or drives the
// tracker picker (PickIssueFromTracker). All form opening is delegated to p.
func getFromTracker(ctx context.Context, p Prompter, t tracker.Tracker, allowedTypes []string) (*issue.Issue, error) {
	errMsg := ""
	issues, listErr := t.ListIssues(ctx)
	if listErr != nil {
		errMsg = listErr.Error()
	}

	if listErr == nil && len(issues) == 0 {
		errMsg = "no open issues assigned to you"
	}

	if errMsg != "" {
		if err := p.NotifyTrackerError(ctx, errMsg); err != nil {
			return nil, fmt.Errorf("notify tracker error: %w", err)
		}

		return getFromUser(ctx, p, allowedTypes)
	}

	out, err := p.PickIssueFromTracker(ctx, issues, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("tracker picker: %w", err)
	}

	return out, nil
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

// createFlowCreator is the mode-specific middle of createFlow: confirm with
// the user, create the branch or worktree, and print the success (or abort)
// message. Returns created=false with no error when the user aborts.
// kind ("branch" / "worktree") is used in the persist-failure warning.
type createFlowCreator func(
	ctx context.Context, deps StartDeps, prompter StartPrompter, branchName, base string,
) (created bool, kind string, err error)

func createBranchFlow(ctx context.Context, deps StartDeps, prompter StartPrompter, picked *issue.Issue) error {
	return createFlow(ctx, deps, prompter, picked, branchCreator)
}

func createWorktreeFlow(ctx context.Context, deps StartDeps, prompter StartPrompter, picked *issue.Issue) error {
	return createFlow(ctx, deps, prompter, picked, worktreeCreator)
}

// createFlow implements the shared prepare→resolve-conflict→[creator]→persist→tracker
// pipeline. creator handles the mode-specific confirm+create+output middle.
func createFlow(ctx context.Context, deps StartDeps, prompter StartPrompter, picked *issue.Issue, creator createFlowCreator) error {
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

	created, kind, err := creator(ctx, deps, prompter, branchName, base)
	if err != nil {
		return err
	}

	if !created {
		return nil
	}

	// trackerType is the originating tracker ("" = manual). It is recorded both
	// in the local store (as a cache) and in the BranchRef git object (the
	// cross-machine source of truth read back by the review commands).
	trackerType := ""
	if picked.TrackerType != "" {
		trackerType = deps.Cfg.IssueTracker.Type
	}

	var tt *string
	if trackerType != "" {
		tt = &trackerType
	}

	if err := persist(ctx, b, picked.Subject, tt); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: %s created but store record failed: %v\n", kind, err)
	}

	if deps.Flags.ParentIssueSlug != "" && parentStore != nil {
		if err := parentStore.InsertIssueRelation(ctx, deps.Flags.ParentIssueSlug, b.IssueID()); err != nil {
			fmt.Fprintf(deps.Client.IO().Err, "warning: record parent relation: %v\n", err)
		}
	}

	if err := writePushBranchRef(ctx, deps, b.IssueID(), branchName, trackerType); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: write branch ref: %v\n", err)
	}

	if picked.TrackerType != "" {
		updateTrackerStatus(ctx, deps, prompter, picked.ID)
	}

	return nil
}

func branchCreator(ctx context.Context, deps StartDeps, prompter StartPrompter, branchName, base string) (bool, string, error) {
	confirmed, err := prompter.ConfirmCreateBranch(ctx,
		fmt.Sprintf("Create branch %q based on %q?", branchName, base))
	if err != nil {
		return false, "", fmt.Errorf("confirm create branch: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(deps.Client.IO().Out, "Aborted.")

		return false, "", nil
	}

	if err := deps.Client.CreateBranch(branchName, base); err != nil {
		return false, "", fmt.Errorf("create branch: %w", err)
	}

	fmt.Fprintf(deps.Client.IO().Out, "Switched to new branch %q (based on %q)\n", branchName, base)

	return true, "branch", nil
}

func worktreeCreator(ctx context.Context, deps StartDeps, prompter StartPrompter, branchName, base string) (bool, string, error) {
	repoRoot, err := deps.Client.WorkingTreeRoot()
	if err != nil {
		return false, "", fmt.Errorf("working tree root: %w", err)
	}

	repoName, err := deps.Client.RepoName()
	if err != nil {
		return false, "", fmt.Errorf("resolve repo name: %w", err)
	}

	path := worktreePath(repoRoot, deps.Cfg.Branch.WorktreeDir, repoName, branchName)

	confirmed, err := prompter.ConfirmCreateWorktree(ctx,
		fmt.Sprintf("Create worktree %q at %q based on %q?", branchName, path, base))
	if err != nil {
		return false, "", fmt.Errorf("confirm create worktree: %w", err)
	}

	if !confirmed {
		fmt.Fprintln(deps.Client.IO().Out, "Aborted.")

		return false, "", nil
	}

	if err := deps.Client.CreateWorktree(ctx, branchName, base, path); err != nil {
		return false, "", fmt.Errorf("create worktree: %w", err)
	}

	fmt.Fprintf(deps.Client.IO().Out, "Created worktree %q at %q (based on %q)\n", branchName, path, base)

	hintStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD700"))
	fmt.Fprintln(deps.Client.IO().Out, hintStyle.Render("Run 'cd "+path+"' to begin working."))

	return true, "worktree", nil
}

// resolveParentSlug derives the parent issue slug from the chosen base branch so
// the parent relation (store) and the branch ref's ParentSlug are recorded.
//
// It prefers the local store — authoritative when the base is a git-zf-tracked
// branch present locally — and falls back to parsing the branch name
// (<slug>@<type>@<title>) when the store has no matching row. The fallback is the
// fresh-clone case: the parent integration branch exists only as a
// remote-tracking ref, so it is absent from the local store, but its name still
// encodes the slug. Returns "" when the base is not a git-zf issue branch (e.g.
// main), leaving the new branch parentless.
func resolveParentSlug(ctx context.Context, c *git.Client, baseBranch string) string {
	if gitDir, gdErr := c.GitDir(); gdErr == nil {
		if s, openErr := store.Open(ctx, gitDir); openErr == nil {
			defer func() { _ = s.Close() }()
			if rows, listErr := s.ListBranches(ctx, store.BranchStatusAll); listErr == nil {
				for _, r := range rows {
					if r.BranchName == baseBranch {
						return r.IssueSlug
					}
				}
			}
		}
	}

	if parsed, perr := branch.Parse(baseBranch); perr == nil {
		return parsed.IssueID()
	}

	return ""
}

// BaseBrancher is the slice of *git.Client that baseBranchCandidates needs:
// the local and remote-tracking branch listers. Declared as a tiny interface so
// candidate assembly is unit-testable without a real repository.
type BaseBrancher interface {
	LocalBranchNames() ([]string, error)
	RemoteBranchNames() ([]string, error)
}

// Compile-time check that the production client satisfies the role.
var _ BaseBrancher = (*git.Client)(nil)

// baseBranchCandidates returns the branches offerable as a base for a new
// sub-task: every local branch plus every remote-tracking branch (with the
// remote prefix stripped), deduplicated with locals taking precedence. A branch
// present both locally and on the remote appears once. Including remote-only
// branches is what lets a fresh-clone teammate base a sub-task on a parent
// integration branch that has been pushed but never checked out locally.
func baseBranchCandidates(c BaseBrancher) ([]string, error) {
	locals, err := c.LocalBranchNames()
	if err != nil {
		return nil, fmt.Errorf("list local branches: %w", err)
	}

	// Remote-tracking branches are best-effort: a missing/erroring remote must
	// not block the local-only picker (the prior behaviour).
	remotes, _ := c.RemoteBranchNames()

	seen := make(map[string]bool, len(locals)+len(remotes))
	out := make([]string, 0, len(locals)+len(remotes))
	add := func(name string) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		out = append(out, name)
	}

	for _, n := range locals {
		add(n)
	}
	for _, n := range remotes {
		add(n)
	}

	return out, nil
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

	// An explicit base chosen via the interactive picker (PickBaseBranch) always
	// wins — including a remote-only parent branch that is absent from the local
	// store on a fresh clone. Checked before the --parent store lookup so the
	// operator's pick is never silently overridden by the config base.
	if deps.BaseBranchOverride != "" {
		return b, deps.BaseBranchOverride, nil
	}

	if deps.Flags.ParentIssueSlug != "" {
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
	ApplyTrackerStatus(ctx, deps.Tracker, deps.Client.IO().Err, issueID, deps.Cfg.IssueTracker.Type, prompter.PickTrackerStatus)
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
func writePushBranchRef(ctx context.Context, deps StartDeps, issueSlug, branchName, trackerType string) error {
	ref := git.BranchRef{
		IssueSlug:   issueSlug,
		BranchName:  branchName,
		ParentSlug:  deps.Flags.ParentIssueSlug,
		CreatedAt:   time.Now().UTC().Format(time.RFC3339),
		TrackerType: trackerType,
	}
	if _, err := deps.Client.WriteBranchRef(ctx, issueSlug, ref); err != nil {
		return err
	}
	return deps.Client.PushBranchRef(ctx, issueSlug)
}
