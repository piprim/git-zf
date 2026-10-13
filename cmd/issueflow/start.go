package issueflow

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/piprim/git-zf/branch"
	"github.com/piprim/git-zf/cmd/cmdutil"
	"github.com/piprim/git-zf/config"
	"github.com/piprim/git-zf/git"
	"github.com/piprim/git-zf/issue"
	"github.com/piprim/git-zf/tracker"
	"github.com/piprim/git-zf/tui"
	"github.com/spf13/cobra"
)

// StartDeps bundles the long-lived dependencies the start flow needs.
// Production code builds it via BuildStartDeps; tests inject directly.
// Exported because cmd/branch/branch.go's newRunE constructs one too.
type StartDeps struct {
	Client  *git.Client
	Cfg     *config.AppConfig
	Tracker tracker.Tracker // nil when cfg.IssueTracker.Type == ""
	// Mirror is non-nil when the issues are mirrored with the tracker; the
	// listing is then the repository's, never the tracker's.
	Mirror *issue.Mirror
	// Stderr receives the warnings of the tracker listing and of the repo
	// issues; nil means Client.IO().Err, where the fetch and the reconcile
	// always write.
	Stderr             io.Writer
	Flags              issue.IssueStartFlags
	BaseBranchOverride string // set by PickBaseBranch; skips the parent lookup in prepareBranch
}

// errW is where the warnings go.
func (d StartDeps) errW() io.Writer {
	if d.Stderr != nil {
		return d.Stderr
	}

	return d.Client.IO().Err
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
		deps.Mirror = MirrorOf(cfg, t)
	}

	return deps, nil
}

// allowedTypes are the branch types this clone's commit types allow.
func allowedTypes(deps StartDeps) ([]string, error) {
	types := make([]string, 0, len(deps.Cfg.CommitTypes))
	for _, t := range deps.Cfg.CommitTypes {
		types = append(types, t.Name)
	}
	if len(types) == 0 {
		return nil, errors.New("config: no commit types found")
	}

	return types, nil
}

// RunIssueStart is the prompter-driven issue-start flow: the open issues
// are listed (ListRows), one is picked in the table (PickRow) or a new one
// is asked for in the manual form, and StartIssue creates its branch.
// Called from cmd/issue's startRunE and cmd/branch's newRunE with a
// HuhStartPrompter; tests call it with a scripted one. An empty table is
// skipped: the manual form opens at once.
//
// Returns nil on the non-error exits: the operator aborted at the confirm
// or in the conflict loop, or a test prompter returned no issue.
func RunIssueStart(ctx context.Context, deps StartDeps, prompter StartPrompter) error {
	types, err := allowedTypes(deps)
	if err != nil {
		return err
	}

	// The picker has no `r` key: it fetches, and with the mirror on
	// reconciles, once, so an issue a teammate just filed is offered and not
	// filed again.
	FetchIssues(ctx, deps.Client)
	if deps.Flags.Tracker {
		ReconcileIssues(ctx, deps.Client, deps.Mirror)
	}

	rows, err := ListRows(ctx, deps, issue.StateOpen)
	if err != nil {
		return fmt.Errorf("list issues: %w", err)
	}

	var row *issue.Row
	if len(rows) > 0 {
		if row, err = prompter.PickRow(ctx, rows); err != nil {
			return fmt.Errorf("pick issue: %w", err)
		}
	}

	var picked *issue.Issue
	if row == nil {
		picked, err = newIssueFromUser(ctx, deps.Client, prompter, types)
	} else {
		picked, err = issueFromRow(ctx, deps, prompter, row, types)
	}
	if err != nil || picked == nil {
		return err
	}

	return StartIssue(ctx, deps, prompter, picked)
}

// StartRow starts the issue of a list row: `s` in `issue list`.
func StartRow(ctx context.Context, deps StartDeps, prompter StartPrompter, row *issue.Row) error {
	types, err := allowedTypes(deps)
	if err != nil {
		return err
	}

	picked, err := issueFromRow(ctx, deps, prompter, row, types)
	if err != nil || picked == nil {
		return err
	}

	return StartIssue(ctx, deps, prompter, picked)
}

// StartIssue creates the branch of a picked issue: base branch, worktree,
// confirm, create, record, push, tracker status.
func StartIssue(ctx context.Context, deps StartDeps, prompter StartPrompter, pickedIssue *issue.Issue) error {
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
			deps.Flags.ParentIssueSlug = resolveParentSlug(baseBranch)
		}
	}

	useWorktree, err := resolveUseWorktree(ctx, deps, prompter)
	if err != nil {
		return err
	}

	creator := branchCreator
	if useWorktree {
		creator = worktreeCreator
	}

	return createFlow(ctx, deps, prompter, pickedIssue, creator)
}

// issueFromRow converts a picked row. A repo issue (State set) is resolved
// and goes through the record path; any other row is asked its type with
// the one-issue form and starts as a tracker issue when the tracker listed
// it this run (TrackerStatus set), as a manual one otherwise: a branch
// started with a typed ID stays manual, and branch new never reaches the
// tracker. The row's branch, if any, is ignored here: the conflict loop
// deals with an existing branch name.
func issueFromRow(
	ctx context.Context, deps StartDeps, p StartPrompter, row *issue.Row, types []string,
) (*issue.Issue, error) {
	if row.State == "" {
		src := tracker.Issue{ID: row.IssueSlug, Subject: row.Title}
		if row.TrackerStatus != nil {
			src.TrackerType = deps.Cfg.IssueTracker.Type
		}
		got, err := p.PickIssueFromTracker(ctx, []tracker.Issue{src}, types)
		if err != nil {
			return nil, fmt.Errorf("tracker issue form: %w", err)
		}
		if got == nil {
			return nil, nil
		}
		got.Issue = src

		return got, nil
	}

	// A branch started under another slug is joined to its record by the
	// full ID its chain carries.
	query := row.IssueSlug
	if row.Branch != nil && row.Branch.IssueID != "" {
		query = row.Branch.IssueID
	}
	rec, err := issue.Resolve(ctx, deps.Client, query)
	if err != nil {
		return nil, fmt.Errorf("resolve issue %s: %w", row.IssueSlug, err)
	}
	if rec.Tracker != nil && rec.Tracker.Born {
		return fromImportedRecord(ctx, p, &rec, types)
	}
	if err := checkRecordType(&rec, types); err != nil {
		return nil, err
	}

	return issueFromRecord(&rec), nil
}

// newIssueFromUser is the manual form. A typed ID is used as is, with no
// record (an issue living in a tracker git-zf does not talk to); an empty
// one prepares a new repo issue, which createFlow publishes once the branch
// exists, so every abort in between (base picker, confirm, branch conflict,
// git failure) leaves no issue behind.
func newIssueFromUser(ctx context.Context, c *git.Client, p StartPrompter, types []string) (*issue.Issue, error) {
	got, err := p.PickIssueFromUser(ctx, types)
	if err != nil {
		return nil, fmt.Errorf("issue input: %w", err)
	}
	if got == nil || got.ID != "" {
		return got, nil
	}

	id, err := issue.Prepare(ctx, c, issue.NewIssue{Title: got.Subject, BranchType: got.Type})
	if err != nil {
		return nil, fmt.Errorf("prepare repo issue: %w", err)
	}

	rec := issue.Record{ID: id}
	got.ID, got.RecordID, got.RecordPending = rec.DisplayID(), id, true

	return got, nil
}

// publishRepoIssue makes the issue prepared by newIssueFromUser exist and
// pushes it. Called once the branch exists; a no-op for an issue that was not
// prepared by this run (tracker issue, typed ID, existing repo issue).
// Failures are warnings, like every other post-creation step: the branch is
// there and the operator can re-create the issue with `git zf issue new`.
func publishRepoIssue(ctx context.Context, c *git.Client, picked *issue.Issue) {
	if !picked.RecordPending {
		return
	}

	id, errW := picked.RecordID, c.IO().Err

	if err := issue.Publish(ctx, c, id); err != nil {
		fmt.Fprintf(errW, "warning: branch created but the repo issue could not be saved: %v\n", err)

		return
	}

	if err := issue.Push(ctx, c, id); err != nil {
		fmt.Fprintf(errW, "warning: issue saved locally but not pushed (run `git zf issue sync` later): %v\n", err)
	}
}

// checkRecordType refuses a repo issue whose branch type this clone cannot
// use. The type comes from the issue's create op, written on another clone:
// commit types are per-clone config, so it may be unknown here, and nothing
// stops an op from carrying an empty or malformed one. Checked before the type
// reaches the branch name.
func checkRecordType(rec *issue.Record, allowedTypes []string) error {
	if slices.Contains(allowedTypes, rec.BranchType) {
		return nil
	}

	return fmt.Errorf(
		"issue %s has type %q, which is not one of this clone's commit types (%s): add it to commit-types in the config",
		rec.DisplayID(), rec.BranchType, strings.Join(allowedTypes, ", "))
}

// issueFromRecord converts a repo issue to the in-flow entity. The display ID
// goes into the branch name; RecordID keeps the unambiguous full ID.
func issueFromRecord(rec *issue.Record) *issue.Issue {
	return &issue.Issue{
		Type:     rec.BranchType,
		RecordID: rec.ID,
		Issue:    tracker.Issue{ID: rec.DisplayID(), Subject: rec.Title, Description: rec.Description},
	}
}

// fromImportedRecord converts an issue imported from the tracker. Its record
// has no branch type (the tracker has none), so the type is asked with the
// form a live tracker listing uses, offered this one issue. The result starts
// like a tracker issue: the tracker number names the branch and the branch
// chain records the tracker origin; RecordID ties it to the record.
func fromImportedRecord(
	ctx context.Context, p StartPrompter, rec *issue.Record, allowedTypes []string,
) (*issue.Issue, error) {
	src := tracker.Issue{
		TrackerType: rec.Tracker.Type, ID: rec.Tracker.ID, Subject: rec.Title, Description: rec.Description,
	}

	got, err := p.PickIssueFromTracker(ctx, []tracker.Issue{src}, allowedTypes)
	if err != nil {
		return nil, fmt.Errorf("tracker issue form: %w", err)
	}
	if got == nil {
		return nil, nil
	}

	got.Issue, got.RecordID = src, rec.ID

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

// createFlowCreator is the mode-specific middle of createFlow: confirm with
// the user, create the branch or worktree, and print the success (or abort)
// message. Returns created=false with no error when the user aborts.
// kind ("branch" / "worktree") is used in the record-failure warning.
type createFlowCreator func(
	ctx context.Context, deps StartDeps, prompter StartPrompter, branchName, base string,
) (created bool, kind string, err error)

// createFlow implements the shared prepare→resolve-conflict→[creator]→record→tracker
// pipeline. creator handles the mode-specific confirm+create+output middle.
func createFlow(
	ctx context.Context, deps StartDeps,
	prompter StartPrompter,
	picked *issue.Issue, creator createFlowCreator) error {
	b, base, err := prepareBranch(ctx, deps, picked)
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

	publishRepoIssue(ctx, deps.Client, picked)

	// trackerType is the originating tracker ("" = manual). The branch chain
	// records it, and the review commands of every clone read it back.
	trackerType := ""
	if picked.TrackerType != "" {
		trackerType = deps.Cfg.IssueTracker.Type
	}

	op := &branch.Op{
		Branch: b.Name(), BranchType: b.Type(), Title: picked.Subject,
		Parent: deps.Flags.ParentIssueSlug, TrackerType: trackerType, IssueID: picked.RecordID,
	}
	if err := branch.Start(ctx, deps.Client, b.IssueID(), op); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: %s created but its record failed: %v\n", kind, err)
	} else if err := branch.Push(ctx, deps.Client, b.IssueID()); err != nil {
		fmt.Fprintf(deps.Client.IO().Err, "warning: push branch ref: %v\n", err)
	}

	if picked.TrackerType != "" {
		// Every error here is a non-fatal warning: the branch was already
		// created, and the operator can clean up tracker drift manually.
		ApplyTrackerStatus(
			ctx, deps.Tracker, deps.Client.IO().Err,
			picked.ID, deps.Cfg.IssueTracker.Type, prompter.PickTrackerStatus)
	}

	return nil
}

func branchCreator(
	ctx context.Context,
	deps StartDeps,
	prompter StartPrompter,
	branchName,
	base string) (bool, string, error) {
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

func worktreeCreator(
	ctx context.Context, deps StartDeps,
	prompter StartPrompter, branchName, base string) (bool, string, error) {
	repoRoot := deps.Client.WorkingTreeRoot()

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

	fmt.Fprintln(deps.Client.IO().Out, tui.HintStyle.Render("Run 'cd "+path+"' to begin working."))

	return true, "worktree", nil
}

// resolveParentSlug derives the parent issue slug from the chosen base branch,
// so that the new branch's chain records its parent. The slug is the one in the
// branch name (<slug>@<type>@<title>), which also covers a parent branch that
// exists only as a remote-tracking ref. Returns "" when the base is not a
// git-zf issue branch (e.g. main), leaving the new branch parentless.
func resolveParentSlug(baseBranch string) string {
	if parsed, perr := branch.Parse(baseBranch); perr == nil {
		return parsed.IssueID()
	}

	return ""
}

// baseBranchCandidates returns the branches offerable as a base for a new
// sub-task: every local branch plus every remote-tracking branch (with the
// remote prefix stripped), deduplicated with locals taking precedence. A branch
// present both locally and on the remote appears once. Including remote-only
// branches is what lets a fresh-clone teammate base a sub-task on a parent
// integration branch that has been pushed but never checked out locally.
func baseBranchCandidates(c *git.Client) ([]string, error) {
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
// the branch and worktree creators of createFlow.
//
// When deps.Flags.ParentIssueSlug is set, the parent's newest in-progress
// branch becomes the base.
func prepareBranch(
	ctx context.Context, deps StartDeps, picked *issue.Issue,
) (b *branch.Branch, base string, err error) {
	b, err = branch.New(picked.ID, picked.Type, picked.Subject, deps.Flags.Variant)
	if err != nil {
		return nil, "", fmt.Errorf("assemble branch name: %w", err)
	}

	// An explicit base chosen via the interactive picker (PickBaseBranch) always
	// wins, including a remote-only parent branch. Checked before the --parent
	// lookup so the operator's pick is never silently overridden by the config
	// base.
	if deps.BaseBranchOverride != "" {
		return b, deps.BaseBranchOverride, nil
	}

	if deps.Flags.ParentIssueSlug != "" {
		// --parent set via flag: the parent may have been started in another
		// clone, so read its chain after a best-effort fetch.
		if err := branch.Fetch(ctx, deps.Client); err != nil {
			fmt.Fprintf(deps.Client.IO().Err, "warning: fetch branch refs: %v\n", err)
		}

		parent, err := branch.Load(ctx, deps.Client, deps.Flags.ParentIssueSlug)
		if err != nil {
			return nil, "", fmt.Errorf("read branches of parent %q: %w", deps.Flags.ParentIssueSlug, err)
		}
		if parent != nil {
			if rows := branch.Rows([]branch.State{*parent}, branch.StatusInProgress); len(rows) > 0 {
				return b, rows[0].BranchName, nil
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

// worktreePath computes the absolute path for a new worktree.
// baseDir overrides the default (sibling of repoRoot) when non-empty; ~ is expanded.
func worktreePath(repoRoot, baseDir, repoName, branchName string) string {
	base := baseDir
	if base == "" {
		base = filepath.Dir(repoRoot)
	} else if rest, ok := strings.CutPrefix(base, "~"); ok && (rest == "" || rest[0] == '/') {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, rest)
		}
	}

	return filepath.Join(base, repoName+"--"+branchName)
}
